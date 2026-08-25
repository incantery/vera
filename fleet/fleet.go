package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/mux"
)

// Fleet is the supervisor: it opens rooms, watches them, and says when
// one needs a person.
type Fleet struct {
	Mux   mux.Mux
	Store *Store
	// Projects resolves what a request calls its repository — a path
	// or a name — and remembers every one a task runs in.
	Projects *Projects
	// Harness is argv for the coding agent; the brief is appended as
	// the first prompt. Default: claude in auto mode.
	Harness []string
	// Model picks the harness's model for a task. Vera chooses, always:
	// an agent nobody is watching must not inherit whatever the person
	// last used at their own terminal. Nil means the harness default.
	Model func(t *Task) string
	// HookURL builds the loopback URL the harness's hooks POST their
	// event JSON to, for a task and incarnation.
	HookURL func(id, incarnation string) string
	// StatusURL builds the loopback URL the agent reports its status
	// verbs to; empty leaves the instructions out of the brief.
	StatusURL func(id string) string
	// Env is extra environment for the harness — telemetry, mostly. It
	// goes through a private file the pane's shell sources, never
	// through the pane's screen: a token typed into a terminal is a
	// token in its scrollback.
	Env func(t *Task) []string
	// HasSession says whether the harness has a session to continue in
	// a directory. Nil means assume yes. Default: Claude Code's layout.
	HasSession func(dir string) bool
	// Trust pre-answers the harness's "trust this folder?" for a new
	// worktree. Nil skips it; the default inherits Claude Code's
	// answer for the main checkout.
	Trust func(project, worktree string) error
	// Observe hears every change of belief. Nil is fine.
	Observe    func(Event)
	Thresholds Thresholds
	// Every is the supervision cadence. Default 15s; pokes cut it short.
	Every time.Duration

	// AutoResume: a task whose pane went away (the multiplexer was
	// restarted) is reopened by the supervisor on its own, a few times,
	// with a pause between. Off means a person says resume.
	AutoResume bool

	mu      sync.Mutex
	poke    chan struct{}
	known   map[string]State // last state per task, to report changes once
	resumes map[string]resumeTry
}

type resumeTry struct {
	n  int
	at time.Time
}

// Event is one change of what Vera believes about a task.
type Event struct {
	Task  *Task
	State State
	Prev  State
	At    time.Time
}

// Request is what a person or the mind asks for.
type Request struct {
	Project string // any path inside the repo
	Name    string // worktree/branch name; generated when empty
	Kind    Kind
	Mode    Mode
	Brief   string
}

// View is a task as the phone and the mind see it: the record plus
// what is currently believed about it.
type View struct {
	*Task
	State  State    `json:"state"`
	Last   *Status  `json:"last,omitempty"`
	Unread []Status `json:"unread,omitempty"`
	// Report is what the task wrote to its report file, if anything.
	Report string `json:"report,omitempty"`
}

func New(m mux.Mux, store *Store) *Fleet {
	return &Fleet{
		Mux:   m,
		Store: store,
		// auto: nobody is at this terminal to approve anything, and
		// the first thing acceptEdits asked about was the status curl
		// the brief itself requested. auto runs Claude Code's own
		// classifier over each call; the brief still says to reserve
		// "blocked" for real forks.
		Harness:    []string{"claude", "--permission-mode", "auto"},
		Model:      DefaultModel,
		HasSession: claudeHasSession,
		Trust:      inheritTrust,
		Thresholds: DefaultThresholds,
		Every:      15 * time.Second,
		AutoResume: true,
		poke:       make(chan struct{}, 1),
		known:      map[string]State{},
		resumes:    map[string]resumeTry{},
	}
}

// Poke asks the supervisor to look now.
func (f *Fleet) Poke() {
	select {
	case f.poke <- struct{}{}:
	default:
	}
}

// Spawn opens a room and starts the agent in it with the brief. For a
// ship task that is a fresh worktree and branch; a scout works in the
// main checkout and is expected to touch nothing.
func (f *Fleet) Spawn(ctx context.Context, req Request) (*Task, error) {
	if strings.TrimSpace(req.Brief) == "" {
		return nil, errors.New("a task needs a brief")
	}
	var err error
	var repo Repo
	if strings.TrimSpace(req.Project) == "" {
		// Nothing named: the repository in front of the person.
		if p, ferr := f.Mux.Focus(ctx); ferr == nil && p.Path != "" {
			req.Project = p.Path
		} else {
			return nil, errors.New("no repository named, and none is in front of them")
		}
	}
	if f.Projects != nil {
		repo, err = f.Projects.Resolve(ctx, req.Project)
	} else {
		repo, err = FindRepo(req.Project)
	}
	if err != nil {
		return nil, err
	}
	if f.Projects != nil {
		f.Projects.Remember(repo)
	}
	if req.Kind == "" {
		req.Kind = Ship
	}
	if req.Mode == "" && req.Kind == Ship {
		req.Mode = LocalOnly
	}
	id := newID()
	name := req.Name
	if name == "" {
		name = "vera-" + id
	}
	t := &Task{
		ID:          id,
		Project:     repo.Root,
		Name:        name,
		Kind:        req.Kind,
		Mode:        req.Mode,
		Brief:       req.Brief,
		Spawned:     time.Now(),
		Incarnation: newID(),
	}
	switch req.Kind {
	case Ship:
		wt, err := repo.New(name, "", LoadConventions(repo.Root))
		if err != nil {
			return nil, err
		}
		t.Worktree, t.Branch, t.Session = wt.Path, wt.Branch, repo.Session(name)
	case Scout:
		t.Worktree, t.Session = repo.Root, repo.Session("")
	default:
		return nil, fmt.Errorf("unknown task kind %q", req.Kind)
	}

	if req.Kind == Ship && f.Trust != nil {
		if err := f.Trust(repo.Root, t.Worktree); err != nil {
			slog.Warn("fleet: could not pre-trust the worktree; the agent may wait on a dialog", "task", id, "error", err.Error())
		}
	}
	pane, err := f.open(ctx, t, nil)
	if err != nil {
		if req.Kind == Ship {
			if wt, gerr := repo.Get(name); gerr == nil {
				_ = repo.Remove(wt, true) // nothing was done in it
			}
		}
		return nil, err
	}
	t.Pane = pane.ID
	if err := f.Store.Save(t); err != nil {
		return nil, err
	}
	_ = f.Store.Append(id, Status{Verb: Working, Text: "spawned in " + t.Session, By: "vera"})
	slog.Info("fleet: spawned", "task", id, "kind", t.Kind, "session", t.Session, "pane", t.Pane.String())
	f.Poke()
	return t, nil
}

// Hook is the harness's event arriving. The incarnation must match; a
// hook from an earlier life of the task says nothing. Stop marks the
// turn ended. A Notification that wants a person — a permission
// prompt, an idle prompt — is recorded as blocked, in the harness's
// words, so the picture says "needs you" instead of "running".
func (f *Fleet) Hook(id, incarnation string, ev HookEvent) error {
	t, err := f.Store.Load(id)
	if err != nil {
		return err
	}
	if t.Incarnation != incarnation {
		return errors.New("stale incarnation")
	}
	switch ev.Name {
	case "Stop":
		t.TurnEnded = time.Now()
		if err := f.Store.Save(t); err != nil {
			return err
		}
	case "Notification":
		switch ev.NotificationType {
		case "permission_prompt", "idle_prompt", "elicitation_dialog":
			// An idle prompt after the agent said done is the harness
			// sitting at its prompt with nothing to do — not a task
			// reopened as blocked. A permission prompt is a question
			// whatever was said before it.
			if last, _ := f.Store.Last(id); last != nil && last.Verb.Terminal() && ev.NotificationType == "idle_prompt" {
				break
			}
			text := strings.TrimSpace(ev.Message)
			if text == "" {
				text = ev.NotificationType
			}
			_ = f.Store.Append(id, Status{Verb: Blocked, Text: "the agent's terminal is asking: " + text, By: "harness"})
		}
	}
	f.Poke()
	return nil
}

// open starts the harness in the task's room and returns its pane.
// extra is argv between the harness and the prompt; the prompt is the
// scaffolded brief for a fresh start, or a nudge for a resume.
func (f *Fleet) open(ctx context.Context, t *Task, extra []string) (*mux.Pane, error) {
	argv := append([]string{}, f.Harness...)
	if f.Model != nil {
		if model := f.Model(t); model != "" {
			argv = append(argv, "--model", model)
		}
	}
	statusURL := ""
	if f.StatusURL != nil {
		statusURL = f.StatusURL(t.ID)
	}
	if f.HookURL != nil {
		settings, err := writeHarnessSettings(f.Store.TaskDir(t.ID), f.HookURL(t.ID, t.Incarnation), statusURL)
		if err != nil {
			return nil, err
		}
		argv = append(argv, "--settings", settings)
	}
	argv = append(argv, extra...)
	if len(extra) == 0 {
		argv = append(argv, scaffold(t, statusURL, f.Store.ReportPath(t.ID)))
	} else {
		argv = append(argv, "Vera here: your terminal was restarted. Carry on from where you were; your status curl and report path are unchanged. Post a working status first.")
	}
	env := []string{"VERA_TASK=" + t.ID}
	if f.Env != nil {
		env = append(env, f.Env(t)...)
	}
	envFile, err := writeEnvFile(f.Store.TaskDir(t.ID), env)
	if err != nil {
		return nil, err
	}
	// The launch is a script, and the pane is told one short line to
	// run it. A mux that starts panes by typing into a shell (rook,
	// today) cannot be handed a brief on the command line: a pty in
	// canonical mode keeps 1024 bytes of a line and drops the rest, so
	// a long brief arrived truncated and its Enter never came. The
	// script sources the env, cds into the room, and execs the harness
	// — the pane's foreground program is the agent, and neither the
	// brief nor a token ever crosses the screen.
	run, err := writeRunScript(f.Store.TaskDir(t.ID), envFile, t.Worktree, argv)
	if err != nil {
		return nil, err
	}
	return f.Mux.Spawn(ctx, mux.Spawn{
		Session: t.Session,
		Name:    t.Name,
		Dir:     t.Worktree,
		Command: []string{"sh", run},
	})
}

// Resume picks a task up whose pane is gone — the multiplexer was
// restarted, the window was closed — in the same room, with the
// harness continuing its last session there. The worktree, branch and
// log are all still where they were; only the pane is new. This is
// what makes the fleet survive a rook restart, which is firstmate's
// "kill the session anytime and the next one reconciles".
func (f *Fleet) Resume(ctx context.Context, id string) (*Task, error) {
	t, err := f.Store.Load(id)
	if err != nil {
		return nil, err
	}
	if t.Closed {
		return nil, errors.New("the task is closed")
	}
	old, err := f.Mux.Get(ctx, t.Pane)
	if err == nil && !old.Dead && !IsShell(old.Command) {
		return t, errors.New("the task's terminal is still there; nothing to resume")
	}
	if _, err := os.Stat(t.Worktree); err != nil {
		return nil, fmt.Errorf("the task's checkout is gone: %s", t.Worktree)
	}
	if err == nil && !old.Dead {
		// The agent exited and left its shell: take the pane with it,
		// or the room fills with dead prompts.
		_ = f.Mux.Kill(ctx, t.Pane)
	}
	t.Incarnation = newID()
	t.TurnEnded = time.Time{}
	t.Resumed = time.Now()
	// --continue: the harness's own most recent session in that
	// directory, which is this task's — when there is one. An agent
	// that died before it ever spoke has nothing to continue, and
	// asking would exit with "no conversation found"; it starts over
	// with the brief instead.
	var extra []string
	how := "continuing its session"
	if f.HasSession == nil || f.HasSession(t.Worktree) {
		extra = []string{"--continue"}
	} else {
		how = "starting over with the brief; it had no session to continue"
	}
	pane, err := f.open(ctx, t, extra)
	if err != nil {
		return nil, err
	}
	t.Pane = pane.ID
	if err := f.Store.Save(t); err != nil {
		return nil, err
	}
	if f.Projects != nil {
		f.Projects.Remember(Repo{Root: t.Project, Name: baseName(t.Project)})
	}
	_ = f.Store.Append(id, Status{Verb: Working, Text: "resumed in " + t.Session + " after its terminal went away, " + how, By: "vera"})
	slog.Info("fleet: resumed", "task", id, "pane", t.Pane.String())
	f.Poke()
	return t, nil
}

// TurnEnded is the Stop hook, for callers that speak only that.
func (f *Fleet) TurnEnded(id, incarnation string) error {
	return f.Hook(id, incarnation, HookEvent{Name: "Stop"})
}

// DefaultModel is Vera's choice when nobody said: the capable one for
// work that changes code, the quick one for reading.
func DefaultModel(t *Task) string {
	if t.Kind == Scout {
		return "sonnet"
	}
	return "opus"
}

// Report appends a status line — the agent's own word, or a person's.
func (f *Fleet) Report(id string, st Status) error {
	if _, err := f.Store.Load(id); err != nil {
		return err
	}
	if err := f.Store.Append(id, st); err != nil {
		return err
	}
	f.Poke()
	return nil
}

// Answer types a reply into the task's pane and sends it — a person
// (or Vera) resolving what the agent asked. The status log records it,
// so a returning phone sees the decision as well as the question.
func (f *Fleet) Answer(ctx context.Context, id, text string) error {
	t, err := f.Store.Load(id)
	if err != nil {
		return err
	}
	if err := f.Mux.Send(ctx, t.Pane, text); err != nil {
		return err
	}
	if err := f.Mux.Enter(ctx, t.Pane); err != nil {
		return err
	}
	_ = f.Store.Append(id, Status{Verb: Resolved, Text: text, By: "person"})
	// The turn is theirs again.
	t.TurnEnded = time.Time{}
	_ = f.Store.Save(t)
	f.Poke()
	return nil
}

// Land finishes a ship task the way its mode allows. local-only merges
// into the main checkout; the other modes are the agent's job (it was
// briefed to open the PR) and Land only closes the room. Refusals come
// from the worktree layer untouched: a dirty checkout or a merge that
// does not apply is reported, not forced.
func (f *Fleet) Land(ctx context.Context, id string) error {
	t, err := f.Store.Load(id)
	if err != nil {
		return err
	}
	if t.Closed {
		return errors.New("already closed")
	}
	if t.Kind == Ship && t.Mode == LocalOnly {
		repo := Repo{Root: t.Project, Name: baseName(t.Project)}
		_ = f.Mux.Kill(ctx, t.Pane) // the checkout must be quiet to merge
		if err := repo.Merge(t.Name); err != nil {
			return err
		}
		_ = f.Store.Append(id, Status{Verb: Done, Text: "merged " + t.Branch + " into " + repo.DefaultBranch(), By: "vera"})
	} else {
		_ = f.Mux.Kill(ctx, t.Pane)
		_ = f.Store.Append(id, Status{Verb: Done, Text: "closed", By: "vera"})
	}
	f.closeRoom(ctx, t)
	return f.close(t)
}

// Teardown ends a task without landing it. force discards unlanded
// work, which is never Vera's decision — the caller says so.
func (f *Fleet) Teardown(ctx context.Context, id string, force bool) error {
	t, err := f.Store.Load(id)
	if err != nil {
		return err
	}
	if t.Closed {
		return errors.New("already closed")
	}
	if t.Kind == Ship {
		repo := Repo{Root: t.Project, Name: baseName(t.Project)}
		wt, err := repo.Get(t.Name)
		if err == nil {
			if err := repo.Remove(wt, force); err != nil {
				return err
			}
		}
	}
	_ = f.Mux.Kill(ctx, t.Pane)
	f.closeRoom(ctx, t)
	_ = f.Store.Append(id, Status{Verb: Failed, Text: "torn down", By: "vera"})
	return f.close(t)
}

// closeRoom takes down the task's own workspace — the one made for
// it, never a shared one — on a mux that can.
func (f *Fleet) closeRoom(ctx context.Context, t *Task) {
	if t.Kind != Ship || t.Session == "" {
		return
	}
	if sc, ok := f.Mux.(mux.SessionCloser); ok {
		if err := sc.CloseSession(ctx, t.Session); err != nil {
			slog.Warn("fleet: could not close the task's workspace", "task", t.ID, "session", t.Session, "error", err.Error())
		}
	}
}

func (f *Fleet) close(t *Task) error {
	t.Closed, t.ClosedAt = true, time.Now()
	if err := f.Store.Save(t); err != nil {
		return err
	}
	f.Poke()
	return nil
}

// Tasks is every task with what is believed about it, open first.
func (f *Fleet) Tasks(ctx context.Context) ([]View, error) {
	tasks, err := f.Store.List()
	if err != nil {
		return nil, err
	}
	panes := f.panes(ctx)
	var open, closed []View
	for _, t := range tasks {
		v := View{Task: t}
		v.Last, _ = f.Store.Last(t.ID)
		v.Unread, _ = f.Store.Unread(t.ID)
		v.Report = f.Store.Report(t.ID)
		v.State = f.classify(t, v.Last, panes)
		if t.Closed {
			closed = append(closed, v)
		} else {
			open = append(open, v)
		}
	}
	return append(open, closed...), nil
}

// Supervise watches until ctx ends: every task is classified on a
// cadence and on every poke, and a change of belief is reported once.
func (f *Fleet) Supervise(ctx context.Context) error {
	every := f.Every
	if every == 0 {
		every = 15 * time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		f.sweep(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		case <-f.poke:
			// Let the pane or the hook's cause settle before reading.
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (f *Fleet) sweep(ctx context.Context) {
	tasks, err := f.Store.List()
	if err != nil {
		return
	}
	panes := f.panes(ctx)
	for _, t := range tasks {
		if f.Projects != nil && !t.Closed {
			// A task's repository is a known one for as long as the
			// task exists, whatever panes are open.
			f.Projects.Remember(Repo{Root: t.Project, Name: baseName(t.Project)})
		}
		last, _ := f.Store.Last(t.ID)
		state := f.classify(t, last, panes)
		if state == Gone && panes != nil && f.AutoResume && f.mayResume(t.ID) {
			// The mux is reachable and this task's pane is not in it:
			// the mux was restarted under it. Reopen the room.
			if _, err := f.Resume(ctx, t.ID); err == nil {
				state = Running
			} else {
				slog.Warn("fleet: auto-resume failed", "task", t.ID, "error", err.Error())
			}
		}
		f.mu.Lock()
		prev, seen := f.known[t.ID]
		f.known[t.ID] = state
		f.mu.Unlock()
		if seen && prev == state {
			continue
		}
		if !seen && state == Closed {
			continue // history, not news
		}
		slog.Info("fleet: state", "task", t.ID, "state", state, "prev", prev)
		if f.Observe != nil {
			f.Observe(Event{Task: t, State: state, Prev: prev, At: time.Now()})
		}
	}
}

// mayResume bounds automatic resumes: three tries per task, a minute
// apart — a room that keeps dying is a person's problem, not a loop.
func (f *Fleet) mayResume(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	try := f.resumes[id]
	if try.n >= 3 || time.Since(try.at) < time.Minute {
		return false
	}
	f.resumes[id] = resumeTry{n: try.n + 1, at: time.Now()}
	return true
}

// panes reads the mux once per sweep; nil means the mux is away and
// every pane counts as gone.
func (f *Fleet) panes(ctx context.Context) map[mux.ID]mux.Pane {
	list, err := f.Mux.List(ctx)
	if err != nil {
		return nil
	}
	m := make(map[mux.ID]mux.Pane, len(list))
	for _, p := range list {
		m[p.ID] = p
	}
	return m
}

func (f *Fleet) classify(t *Task, last *Status, panes map[mux.ID]mux.Pane) State {
	e := Evidence{Now: time.Now(), TurnEnded: t.TurnEnded, Last: last, Closed: t.Closed}
	if p, ok := panes[t.Pane]; ok && !p.Dead {
		e.PaneAlive = true
		e.PaneActive = p.Active
		// Just spawned, the pane may still read as the shell for a
		// moment before it becomes the agent; give it that moment.
		e.AgentAlive = !IsShell(p.Command) || time.Since(t.Spawned) < 20*time.Second || time.Since(t.Resumed) < 20*time.Second
	}
	if e.PaneAlive && !t.Closed {
		// Bounded: a poll must never cost more than a glance.
		e.LastWrite = NewestWrite(t.Worktree, e.PaneActive, 6, 300*time.Millisecond)
	}
	return Classify(e, f.Thresholds)
}

func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func baseName(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
