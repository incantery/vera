package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
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
	// Trust pre-answers the harness's "trust this folder?" for a new
	// worktree. Nil skips it; the default inherits Claude Code's
	// answer for the main checkout.
	Trust func(project, worktree string) error
	// Observe hears every change of belief. Nil is fine.
	Observe    func(Event)
	Thresholds Thresholds
	// Every is the supervision cadence. Default 15s; pokes cut it short.
	Every time.Duration

	mu    sync.Mutex
	poke  chan struct{}
	known map[string]State // last state per task, to report changes once
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
		Trust:      inheritTrust,
		Thresholds: DefaultThresholds,
		Every:      15 * time.Second,
		poke:       make(chan struct{}, 1),
		known:      map[string]State{},
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
	repo, err := FindRepo(req.Project)
	if err != nil {
		return nil, err
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
	argv := append([]string{}, f.Harness...)
	if f.Model != nil {
		if model := f.Model(t); model != "" {
			argv = append(argv, "--model", model)
		}
	}
	statusURL := ""
	if f.StatusURL != nil {
		statusURL = f.StatusURL(id)
	}
	if f.HookURL != nil {
		settings, err := writeHarnessSettings(f.Store.TaskDir(id), f.HookURL(id, t.Incarnation), statusURL)
		if err != nil {
			return nil, err
		}
		argv = append(argv, "--settings", settings)
	}
	argv = append(argv, scaffold(t, statusURL, f.Store.ReportPath(id)))
	env := []string{"VERA_TASK=" + id}
	if f.Env != nil {
		env = append(env, f.Env(t)...)
	}
	envFile, err := writeEnvFile(f.Store.TaskDir(id), env)
	if err != nil {
		return nil, err
	}
	// The shell sources the file, then becomes the harness: the pane's
	// foreground program is the agent, and the file's contents never
	// cross the screen.
	command := append([]string{"sh", "-c", `set -a; . "$0"; set +a; exec "$@"`, envFile}, argv...)
	pane, err := f.Mux.Spawn(ctx, mux.Spawn{
		Session: t.Session,
		Name:    name,
		Dir:     t.Worktree,
		Command: command,
	})
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
	_ = f.Store.Append(id, Status{Verb: Failed, Text: "torn down", By: "vera"})
	return f.close(t)
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
		last, _ := f.Store.Last(t.ID)
		state := f.classify(t, last, panes)
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
