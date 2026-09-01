package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/attach"
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
	Observe func(Event)
	// Notes is where what the fleet learns about a repository is
	// written down — Vera's home, in practice. The fleet resolves a
	// repo, reads its conventions and lands branches in it on every
	// task; without this it forgets all of that between tasks. Nil
	// keeps no notes.
	Notes      Notes
	Thresholds Thresholds
	// Lifecycle is the machine's own record of when it was not here to
	// work — asleep, or off the network. Nil is a machine that never
	// goes away, which is the right belief for a test and the wrong
	// one for a laptop.
	Lifecycle *Lifecycle
	// Every is the supervision cadence. Default 15s; pokes cut it short.
	Every time.Duration

	// AutoResume: a task whose pane went away (the multiplexer was
	// restarted) is reopened by the supervisor on its own, a few times,
	// with a pause between. Off means a person says resume.
	AutoResume bool
	// AutoLand: a ship task that says done is landed by the supervisor
	// the way its mode says — merged, or pushed as a PR — and a scout
	// is closed once its report has been seen. Off means a person says
	// land. Landing is what the person asked for when they started the
	// task; asking them to type it again is the supervisor handing its
	// job back.
	AutoLand bool
	// Run runs a check command in a directory and returns its output
	// and whether it passed. Nil uses `sh -c`.
	Run func(ctx context.Context, dir, command string) (string, error)
	// InstallDir is where landing rebuilds a repository's commands:
	// the directory the running daemon's own binary came from. Empty
	// means a landing never builds anything, which is right for
	// everything except the repository this daemon is built from.
	//
	// It is the daemon's own directory rather than $GOPATH/bin on
	// purpose: `go install` writes to the latter, verad runs from the
	// former, and a landing that put the new binary somewhere the
	// running process will never look is a landing that quietly did
	// nothing.
	InstallDir string

	mu      sync.Mutex
	poke    chan struct{}
	known   map[string]State // last state per task, to report changes once
	resumes map[string]resumeTry
}

type resumeTry struct {
	n  int
	at time.Time
}

// Notes is the fleet's side of Vera's memory: a project file per
// repository, and a line when a task lands in one. Deliberately plain
// strings — the fleet should not know what a home directory is.
type Notes interface {
	// Project records a repository the fleet is about to work in.
	// Called on every spawn; expected to be idempotent.
	Project(name, root, branch string, conventions []string) error
	// Landed records a task that finished in one.
	Landed(name, root, task, brief string) error
}

// Event is one change of what Vera believes about a task.
type Event struct {
	// Kind says which sort of moment this is. StateChanged is the
	// original and the common one; the others exist because a change
	// of belief is not the only thing worth telling somebody about,
	// and a second Observe-shaped hook per event type would be three
	// hooks nobody remembers to wire.
	Kind EventKind
	Task *Task
	// State and Prev are set on StateChanged.
	State State
	Prev  State
	// Said is the status line, on TaskSaid: the agent's own words.
	Said *Status
	// Err is why, on LandFailed.
	Err string
	At  time.Time
}

// EventKind is which sort of moment an Event carries.
type EventKind string

const (
	// StateChanged: Vera believes something different about a task.
	StateChanged EventKind = "state"
	// TaskSpawned: a room was opened.
	TaskSpawned EventKind = "spawned"
	// TaskSaid: a line was appended to a task's status log, by the
	// agent, by Vera, or by the person answering.
	TaskSaid EventKind = "said"
	// TaskLanded / LandFailed: the branch went home, or would not.
	TaskLanded EventKind = "landed"
	LandFailed EventKind = "land-failed"
)

// tell hands an event to whoever is listening. Nobody listening is the
// normal case in a test and half the reason this is one line.
func (f *Fleet) tell(ev Event) {
	if f.Observe == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	f.Observe(ev)
}

// Request is what a person or the mind asks for.
type Request struct {
	Project string // any path inside the repo
	Name    string // worktree/branch name; generated when empty
	Kind    Kind
	Mode    Mode
	Brief   string
	// Images are pictures that came with the ask, as absolute paths on
	// this machine. The agent opens them; nothing here does.
	Images []string
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
	// Machine is what the machine was doing over the window this task
	// was judged across — set only when it went away in it. It is what
	// makes "interrupted" sayable: which absence, and how long.
	Machine Machine `json:"machine,omitzero"`
	// AutoLand says whether the supervisor lands this itself. It is on
	// the view because it changes what is TRUE to say about a finished
	// task: with it on, "ready to land" is wrong — nobody is waiting
	// for the person, the landing is already happening.
	AutoLand bool `json:"auto_land,omitempty"`
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
		AutoLand:   true,
		HasSession: claudeHasSession,
		Trust:      inheritTrust,
		Thresholds: DefaultThresholds,
		Lifecycle:  &Lifecycle{},
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
	// Before anything is opened: the first task in a repository is
	// what teaches Vera the repository exists, and it should still
	// have taught her that if the spawn then fails.
	f.note(repo)
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
		Images:      req.Images,
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
	f.tell(Event{Kind: TaskSpawned, Task: t})
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
	t, err := f.Store.Load(id)
	if err != nil {
		return err
	}
	if err := f.Store.Append(id, st); err != nil {
		return err
	}
	// The agent's own sentence, passed on before it is boiled down to
	// a state. "blocked on which database to use" is the most useful
	// line anything in here produces, and classification throws the
	// words away.
	f.tell(Event{Kind: TaskSaid, Task: t, Said: &st, At: st.At})
	f.Poke()
	return nil
}

// Answer types a reply into the task's pane and sends it — a person
// (or Vera) resolving what the agent asked. The status log records it,
// so a returning phone sees the decision as well as the question.
//
// Images are pictures that came with the answer: "here is the failure
// you asked about". They are named in what is typed, because the agent
// on the other end reads files and the pane carries text. What goes in
// the log is what was typed, so the record and the pane agree.
func (f *Fleet) Answer(ctx context.Context, id, text string, images ...string) error {
	t, err := f.Store.Load(id)
	if err != nil {
		return err
	}
	// One line, not a paragraph: this is TYPED into a pane, and a
	// newline typed into a terminal is a Return that would send half
	// the answer.
	text = attach.Line(text, images)
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
	return f.land(ctx, t)
}

// land does what the mode says, then closes the room. Nothing is
// killed until the landing has succeeded: a merge that fails leaves
// the agent where it is, so it can be told what went wrong.
func (f *Fleet) land(ctx context.Context, t *Task) (err error) {
	// Every path out of here is worth a line, and there are six of
	// them: a deferred report is the only way to be sure none is
	// missed the next time a seventh is added.
	defer func() {
		if err != nil {
			f.tell(Event{Kind: LandFailed, Task: t, Err: err.Error()})
			return
		}
		f.tell(Event{Kind: TaskLanded, Task: t})
	}()
	switch {
	case t.Kind == Ship && t.Mode == DirectPR:
		url, err := f.openPR(ctx, t)
		if err != nil {
			return err
		}
		t.PR = url
		_ = f.Store.Append(t.ID, Status{Verb: Done, Text: "opened " + url, By: "vera"})
	case t.Kind == Ship:
		if t.Mode == NoMistakes {
			if err := f.check(ctx, t); err != nil {
				return err
			}
		}
		repo := Repo{Root: t.Project, Name: baseName(t.Project)}
		ahead, _ := distance(t.Worktree, repo.DefaultBranch(), t.Branch)
		if err := repo.Merge(t.Name); err != nil {
			return err
		}
		_ = f.Store.Append(t.ID, Status{Verb: Done, Text: fmt.Sprintf("merged %s into %s (%d commit%s)", t.Branch, repo.DefaultBranch(), ahead, plural(ahead)), By: "vera"})
		// Landing means running. A change to the daemon that is merged
		// and not built is a change nobody is using, and the person
		// finds out by watching old behaviour and disbelieving the
		// notice. A build that fails is a landing that failed, and
		// goes down the same path.
		if built, err := f.install(ctx, repo); err != nil {
			return err
		} else if built != "" {
			_ = f.Store.Append(t.ID, Status{Verb: Done, By: "vera",
				Text: fmt.Sprintf("landed %s — %s; run vera restart to pick it up", t.ID, built)})
		}
	default:
		_ = f.Store.Append(t.ID, Status{Verb: Done, Text: "closed", By: "vera"})
	}
	if f.Notes != nil {
		if err := f.Notes.Landed(baseName(t.Project), t.Project, t.Name, t.Brief); err != nil {
			slog.Warn("fleet: could not record the landing", "task", t.ID, "error", err.Error())
		}
	}
	_ = f.Mux.Kill(ctx, t.Pane)
	f.closeRoom(ctx, t)
	return f.close(t)
}

// install rebuilds the repository's commands into InstallDir, and says
// what it built. Nothing to do — no InstallDir, or a repository whose
// conventions do not ask for it — is "" and no error.
//
// Every directory under cmd/ is a command, which is Go's own
// convention and needs no second list in rook.toml to drift from it.
func (f *Fleet) install(ctx context.Context, repo Repo) (string, error) {
	if f.InstallDir == "" || !LoadConventions(repo.Root).Installs(repo.Name) {
		return "", nil
	}
	names, err := commandsIn(repo.Root)
	if err != nil || len(names) == 0 {
		return "", err
	}
	run := f.Run
	if run == nil {
		run = shellRun
	}
	for _, name := range names {
		// Beside the target and then renamed over it, never written in
		// place. One of the binaries being replaced is the process
		// doing the replacing, and writing new code into the file a
		// running Mach-O is paged from is how you crash it. A rename
		// is atomic and leaves the running inode alone: this process
		// keeps running the old one until it is restarted, which is
		// exactly what the notice tells the person to do.
		final := filepath.Join(f.InstallDir, name)
		// The version is stamped the way a hand build stamps it, so
		// `vera version` after a landing names the commit, not "dev".
		out, err := run(ctx, repo.Root, fmt.Sprintf("go build -ldflags \"-X main.version=$(git describe --always --dirty)\" -o %s ./cmd/%s && mv -f %s %s",
			shellQuote(final+".new"), name, shellQuote(final+".new"), shellQuote(final)))
		if err != nil {
			return "", fmt.Errorf("go build ./cmd/%s: %s", name, tail(out, 600))
		}
	}
	return strings.Join(names, " and ") + " rebuilt", nil
}

// commandsIn lists the directories under cmd/, which is where a Go
// repository keeps the things it builds.
func commandsIn(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// shellQuote makes a path safe for the `sh -c` the build goes through.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// note records the repository in Vera's memory: where it is, what it
// branches from, and what its conventions say. Failure is a warning,
// never a refusal to start work — a task must not fail because a
// notebook could not be written in.
func (f *Fleet) note(repo Repo) {
	if f.Notes == nil {
		return
	}
	c := LoadConventions(repo.Root)
	var found []string
	if len(c.Copy) > 0 {
		found = append(found, "copied into each worktree: "+strings.Join(c.Copy, ", "))
	}
	if len(c.Link) > 0 {
		found = append(found, "linked into each worktree: "+strings.Join(c.Link, ", "))
	}
	if len(c.Check) > 0 {
		found = append(found, "checks before landing: `"+strings.Join(c.Check, "`, `")+"`")
	}
	if err := f.Notes.Project(repo.Name, repo.Root, repo.DefaultBranch(), found); err != nil {
		slog.Warn("fleet: could not write the project note", "project", repo.Name, "error", err.Error())
	}
}

// check runs the repository's landing checks in the worktree. The
// first failure is the answer, with the end of its output.
func (f *Fleet) check(ctx context.Context, t *Task) error {
	run := f.Run
	if run == nil {
		run = shellRun
	}
	for _, cmd := range LoadConventions(t.Project).Check {
		out, err := run(ctx, t.Worktree, cmd)
		if err != nil {
			return fmt.Errorf("check `%s` failed: %s", cmd, tail(out, 600))
		}
	}
	return nil
}

func shellRun(ctx context.Context, dir, command string) (string, error) {
	c := exec.CommandContext(ctx, "sh", "-c", command)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}

// openPR pushes the branch and opens a pull request with gh. The
// worktree stays: a PR that needs changes needs somewhere to make them.
func (f *Fleet) openPR(ctx context.Context, t *Task) (string, error) {
	if out, err := git(t.Worktree, "push", "-u", "origin", t.Branch); err != nil {
		return "", fmt.Errorf("push %s: %s", t.Branch, tail(out, 300))
	}
	c := exec.CommandContext(ctx, "gh", "pr", "create", "--fill", "--head", t.Branch)
	c.Dir = t.Worktree
	out, err := c.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr create: %s", tail(string(out), 300))
	}
	// gh prints the URL last.
	lines := strings.Fields(strings.TrimSpace(string(out)))
	if len(lines) == 0 {
		return "", errors.New("gh pr create said nothing")
	}
	return lines[len(lines)-1], nil
}

// autoLand is the supervisor landing a task that said done. A failure
// is written down once and becomes a decision; a newer done — the
// agent fixed what was wrong — is a reason to try again.
func (f *Fleet) autoLand(ctx context.Context, t *Task, last *Status) State {
	if !t.LandFailedAt.IsZero() && !last.At.After(t.LandFailedAt) {
		return Decision
	}
	if err := f.land(ctx, t); err != nil {
		slog.Warn("fleet: could not land", "task", t.ID, "error", err.Error())
		t.LandFailedAt, t.LandFailure = time.Now(), err.Error()
		_ = f.Store.Save(t)
		_ = f.Store.Append(t.ID, Status{Verb: Blocked, Text: "could not land: " + err.Error() + " — tell the agent what to fix and it will say done again, or land it by hand", By: "vera"})
		return Decision
	}
	return Closed
}

// closeScout ends a scout whose report has been seen: the deliverable
// was delivered, the pane is done.
func (f *Fleet) closeScout(ctx context.Context, t *Task) {
	_ = f.Mux.Kill(ctx, t.Pane)
	_ = f.Store.Append(t.ID, Status{Verb: Done, Text: "report delivered", By: "vera"})
	_ = f.close(t)
}

// Seen marks everything a task has said as shown to the person.
func (f *Fleet) Seen(id string) error {
	all, err := f.Store.Statuses(id)
	if err != nil {
		return err
	}
	if err := f.Store.Present(id, len(all)); err != nil {
		return err
	}
	f.Poke()
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

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
		v := View{Task: t, AutoLand: f.AutoLand}
		v.Last, _ = f.Store.Last(t.ID)
		v.Unread, _ = f.Store.Unread(t.ID)
		v.Report = f.Store.Report(t.ID)
		v.State, v.Machine = f.classify(t, v.Last, panes)
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
		// The heartbeat is also the fallback sleep detector: a sweep
		// that comes an hour after the last one is an hour this
		// machine was not running, whether or not anything told us.
		f.Lifecycle.Beat(time.Now())
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
	// While the machine is away, the supervisor watches and says
	// nothing else. Landing needs a network and a person's repository
	// to be reachable; resuming a room needs a machine that will still
	// be there in a second. Both wait for the machine to come back.
	away := f.Lifecycle != nil && !f.Lifecycle.Awake()
	for _, t := range tasks {
		if f.Projects != nil && !t.Closed {
			// A task's repository is a known one for as long as the
			// task exists, whatever panes are open.
			f.Projects.Remember(Repo{Root: t.Project, Name: baseName(t.Project)})
		}
		last, _ := f.Store.Last(t.ID)
		state, _ := f.classify(t, last, panes)
		if state == Finished && f.AutoLand && last != nil && !away {
			switch t.Kind {
			case Ship:
				state = f.autoLand(ctx, t, last)
			case Scout:
				if unread, _ := f.Store.Unread(t.ID); len(unread) == 0 {
					f.closeScout(ctx, t)
					state = Closed
				}
			}
		}
		if state == Gone && panes != nil && f.AutoResume && !away && f.mayResume(t.ID) {
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
		f.tell(Event{Kind: StateChanged, Task: t, State: state, Prev: prev, At: time.Now()})
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

func (f *Fleet) classify(t *Task, last *Status, panes map[mux.ID]mux.Pane) (State, Machine) {
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
	// The window this task is judged over runs from its last sign of
	// life — or, before it has shown one, from when it was opened.
	from := e.Latest()
	if from.IsZero() {
		from = t.Spawned
	}
	e.Machine = f.Lifecycle.Since(from, e.Now)
	return Classify(e, f.Thresholds), e.Machine
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
