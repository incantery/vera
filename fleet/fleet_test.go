package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/incantery/vera/mux"
)

// fakeMux is a mux in memory: enough to spawn, type, and be listed.
type fakeMux struct {
	mu      sync.Mutex
	panes   map[mux.ID]mux.Pane
	closed  []string            // sessions closed
	spawned map[mux.ID]string   // the last argv element: the brief
	argv    map[mux.ID][]string // the whole command
	typed   []string
	next    int
	down    bool
}

func newFakeMux() *fakeMux {
	return &fakeMux{panes: map[mux.ID]mux.Pane{}, spawned: map[mux.ID]string{}, argv: map[mux.ID][]string{}}
}

func (f *fakeMux) Name() string { return "fake" }
func (f *fakeMux) Focus(context.Context) (*mux.Pane, error) {
	return nil, mux.ErrNoFocus
}
func (f *fakeMux) Get(_ context.Context, id mux.ID) (*mux.Pane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.panes[id]
	if !ok {
		return nil, mux.ErrNoPane
	}
	return &p, nil
}
func (f *fakeMux) List(context.Context) ([]mux.Pane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, mux.ErrUnavailable
	}
	var out []mux.Pane
	for _, p := range f.panes {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeMux) Spawn(_ context.Context, s mux.Spawn) (*mux.Pane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	// The fake's "foreground program" is what the run script execs.
	cmd := s.Command[0]
	if len(s.Command) == 2 && s.Command[0] == "sh" {
		if b, err := os.ReadFile(s.Command[1]); err == nil && strings.Contains(string(b), "'fake-agent'") {
			cmd = "fake-agent"
		}
	}
	p := mux.Pane{ID: mux.ID{Session: s.Session, Window: string(rune('0' + f.next)), Pane: "0"}, Command: cmd, Path: s.Dir, Active: time.Now()}
	f.panes[p.ID] = p
	f.spawned[p.ID] = s.Command[len(s.Command)-1]
	f.argv[p.ID] = s.Command
	return &p, nil
}
func (f *fakeMux) Kill(_ context.Context, id mux.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.panes, id)
	return nil
}
func (f *fakeMux) Send(_ context.Context, id mux.ID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typed = append(f.typed, text)
	return nil
}
func (f *fakeMux) Enter(_ context.Context, id mux.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typed = append(f.typed, "\n")
	return nil
}
func (f *fakeMux) Capture(context.Context, mux.ID) ([]string, error) { return nil, nil }
func (f *fakeMux) GoTo(context.Context, mux.ID) error                { return nil }
func (f *fakeMux) Narrow(context.Context, mux.ID, int) error         { return nil }
func (f *fakeMux) Widen(context.Context, mux.ID) error               { return nil }
func (f *fakeMux) Watch(ctx context.Context, _ func(mux.Event)) error {
	<-ctx.Done()
	return ctx.Err()
}
func (f *fakeMux) Poke() {}
func (f *fakeMux) CloseSession(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, name)
	for id := range f.panes {
		if id.Session == name {
			delete(f.panes, id)
		}
	}
	return nil
}

func (f *fakeMux) touch(id mux.ID, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.panes[id]
	p.Active = at
	f.panes[id] = p
}

func TestFleetLifecycle(t *testing.T) {
	r := newRepo(t)
	m := newFakeMux()
	store := NewStore(filepath.Join(t.TempDir(), "fleet"))
	var events []Event
	f := New(m, store)
	f.Harness = []string{"fake-agent"}
	f.HookURL = func(id, inc string) string {
		return "http://127.0.0.1:1/fleet/" + id + "/hook?incarnation=" + inc
	}
	f.StatusURL = func(id string) string { return "http://127.0.0.1:1/fleet/" + id + "/status" }
	f.Trust = func(project, worktree string) error { return nil }
	f.Env = func(t *Task) []string {
		return []string{"OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic s3cret", "TOK=it's"}
	}
	f.Observe = func(e Event) { events = append(events, e) }
	f.Thresholds = Thresholds{Quiet: time.Minute, Stale: 10 * time.Minute}
	ctx := context.Background()

	task, err := f.Spawn(ctx, Request{Project: r.Root, Name: "feat", Brief: "add a thing"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Kind != Ship || task.Mode != LocalOnly || task.Branch != "feat" || task.Session != "proj--feat" {
		t.Fatalf("task %+v", task)
	}
	if _, err := os.Stat(task.Worktree); err != nil {
		t.Fatal("worktree not created")
	}
	// The harness was started in the worktree with the hook settings
	// and the brief.
	p := m.panes[task.Pane]
	if p.Path != task.Worktree || p.Command != "fake-agent" {
		t.Errorf("pane %+v", p)
	}
	// The pane is told one short line; the launch is a script, and the
	// env a private file it sources. Nothing long or secret is typed.
	argv := m.argv[task.Pane]
	if len(argv) != 2 || argv[0] != "sh" || !strings.HasSuffix(argv[1], "/run") {
		t.Fatalf("command shape: %q", argv)
	}
	runb, _ := os.ReadFile(argv[1])
	run := string(runb)
	if !strings.Contains(run, "cd '"+task.Worktree+"'") || !strings.Contains(run, "exec 'fake-agent' '--model' 'opus'") || strings.Contains(run, "s3cret") {
		t.Errorf("run script:\n%s", run)
	}
	envPath := filepath.Join(store.TaskDir(task.ID), "env")
	envb, _ := os.ReadFile(envPath)
	if st, _ := os.Stat(envPath); st.Mode().Perm() != 0o600 {
		t.Errorf("env file mode %v", st.Mode().Perm())
	}
	for _, want := range []string{"VERA_TASK='" + task.ID + "'", "OTEL_EXPORTER_OTLP_HEADERS='Authorization=Basic s3cret'", `TOK='it'\''s'`} {
		if !strings.Contains(string(envb), want) {
			t.Errorf("env file missing %q:\n%s", want, envb)
		}
	}
	for _, want := range []string{"add a thing", task.Worktree, "branch feat", "Do not merge", "/fleet/" + task.ID + "/status", "blocked"} {
		if !strings.Contains(run, want) {
			t.Errorf("brief (in the run script) missing %q", want)
		}
	}
	settings, _ := os.ReadFile(filepath.Join(store.TaskDir(task.ID), "claude.json"))
	for _, want := range []string{"hook?incarnation=" + task.Incarnation, `"Stop"`, `"Notification"`, `"Bash(curl -s -X POST http://127.0.0.1:1/fleet/` + task.ID + `/status*)"`} {
		if !strings.Contains(string(settings), want) {
			t.Errorf("hook settings missing %q:\n%s", want, settings)
		}
	}
	// Vera picked the model; the harness did not inherit one.
	if !strings.Contains(run, "'--model' 'opus'") {
		t.Errorf("model not chosen: %s", run)
	}
	if def := New(m, store).Harness; strings.Join(def, " ") != "claude --permission-mode auto" {
		t.Errorf("default harness: %q", def)
	}

	f.sweep(ctx)
	if len(events) != 1 || events[0].State != Running {
		t.Fatalf("after spawn: %+v", events)
	}
	f.sweep(ctx)
	if len(events) != 1 {
		t.Fatal("unchanged state reported again")
	}

	// A permission prompt in the harness is "blocked", in its words.
	if err := f.Hook(task.ID, task.Incarnation, HookEvent{Name: "Notification", NotificationType: "permission_prompt", Message: "Claude needs your permission to use Bash"}); err != nil {
		t.Fatal(err)
	}
	f.sweep(ctx)
	if events[len(events)-1].State != Decision {
		t.Fatalf("a permission prompt should read as a decision, got %s", events[len(events)-1].State)
	}
	// The person answers it; the log moves on.
	if err := f.Answer(ctx, task.ID, "1"); err != nil {
		t.Fatal(err)
	}
	m.touch(task.Pane, time.Now())
	f.sweep(ctx)

	// The Stop hook rings: waiting on a person. A stale incarnation is
	// ignored.
	if err := f.TurnEnded(task.ID, "old"); err == nil {
		t.Error("stale incarnation accepted")
	}
	m.touch(task.Pane, time.Now().Add(-2*time.Minute))
	if err := f.TurnEnded(task.ID, task.Incarnation); err != nil {
		t.Fatal(err)
	}
	f.sweep(ctx)
	if events[len(events)-1].State != Waiting {
		t.Fatalf("expected waiting, got %s", events[len(events)-1].State)
	}

	// A person answers: typed and sent, logged, and the turn is the
	// agent's again.
	if err := f.Answer(ctx, task.ID, "use the second option"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.typed, "") != "1\nuse the second option\n" {
		t.Errorf("typed %q", m.typed)
	}
	m.touch(task.Pane, time.Now())
	f.sweep(ctx)
	if events[len(events)-1].State != Running {
		t.Fatalf("expected running after answer, got %s", events[len(events)-1].State)
	}

	// The agent's own word.
	if err := f.Report(task.ID, Status{Verb: Blocked, Text: "which API?"}); err != nil {
		t.Fatal(err)
	}
	f.sweep(ctx)
	if events[len(events)-1].State != Decision {
		t.Fatalf("expected decision, got %s", events[len(events)-1].State)
	}
	os.WriteFile(store.ReportPath(task.ID), []byte("# Findings\n\nthe thing\n"), 0o644)
	views, _ := f.Tasks(ctx)
	if len(views) != 1 || views[0].State != Decision || len(views[0].Unread) != 5 || views[0].Report != "# Findings\n\nthe thing" {
		t.Fatalf("views %+v", views)
	}
	store.Present(task.ID, 5)
	views, _ = f.Tasks(ctx)
	if len(views[0].Unread) != 0 {
		t.Error("presented lines still unread")
	}

	// Land: commit in the worktree, then merge home.
	os.WriteFile(filepath.Join(task.Worktree, "thing.txt"), []byte("x\n"), 0o644)
	gitRun(t, task.Worktree, "add", "thing.txt")
	gitRun(t, task.Worktree, "commit", "-q", "-m", "the thing")
	if err := f.Land(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "thing.txt")); err != nil {
		t.Error("not merged home")
	}
	if _, ok := m.panes[task.Pane]; ok {
		t.Error("pane not killed")
	}
	if len(m.closed) != 1 || m.closed[0] != task.Session {
		t.Errorf("the task's workspace should be closed on land: %v", m.closed)
	}
	views, _ = f.Tasks(ctx)
	if views[0].State != Closed || !views[0].Closed {
		t.Errorf("after land: %+v", views[0])
	}
	if err := f.Land(ctx, task.ID); err == nil {
		t.Error("landing twice should refuse")
	}
}

func TestFleetGoneAndTeardown(t *testing.T) {
	r := newRepo(t)
	m := newFakeMux()
	f := New(m, NewStore(filepath.Join(t.TempDir(), "fleet")))
	f.Harness = []string{"fake-agent"}
	f.Trust = nil
	ctx := context.Background()
	task, err := f.Spawn(ctx, Request{Project: r.Root, Brief: "scout it", Kind: Scout})
	if err != nil {
		t.Fatal(err)
	}
	if task.Worktree != r.Root || strings.HasPrefix(task.Name, "vera-") == false {
		t.Fatalf("scout task %+v", task)
	}
	m.Kill(ctx, task.Pane)
	views, _ := f.Tasks(ctx)
	if views[0].State != Gone {
		t.Errorf("expected gone, got %s", views[0].State)
	}
	if err := f.Teardown(ctx, task.ID, false); err != nil {
		t.Fatal(err)
	}
	views, _ = f.Tasks(ctx)
	if views[0].State != Closed {
		t.Errorf("expected closed, got %s", views[0].State)
	}
}

func TestFleetSpawnCleansUpOnMuxFailure(t *testing.T) {
	r := newRepo(t)
	m := newFakeMux()
	f := New(&failingMux{m}, NewStore(filepath.Join(t.TempDir(), "fleet")))
	f.Trust = nil
	if _, err := f.Spawn(context.Background(), Request{Project: r.Root, Name: "x", Brief: "b"}); err == nil {
		t.Fatal("expected spawn error")
	}
	if _, err := r.Get("x"); err == nil {
		t.Error("worktree left behind after failed spawn")
	}
}

type failingMux struct{ *fakeMux }

func (failingMux) Spawn(context.Context, mux.Spawn) (*mux.Pane, error) {
	return nil, mux.ErrUnavailable
}

func TestFleetResumesAfterThePaneIsGone(t *testing.T) {
	r := newRepo(t)
	m := newFakeMux()
	f := New(m, NewStore(filepath.Join(t.TempDir(), "fleet")))
	f.Harness = []string{"fake-agent"}
	f.Trust = nil
	sessions := false
	f.HasSession = func(string) bool { return sessions }
	ctx := context.Background()
	task, err := f.Spawn(ctx, Request{Project: r.Root, Name: "back", Brief: "keep going"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resume(ctx, task.ID); err == nil {
		t.Fatal("resume with the pane alive should refuse")
	}
	m.Kill(ctx, task.Pane)
	views, _ := f.Tasks(ctx)
	if views[0].State != Gone {
		t.Fatalf("expected gone, got %s", views[0].State)
	}
	// The supervisor brings it back on its own; a manual resume then
	// finds the pane alive.
	f.sweep(ctx)
	if _, err := f.Resume(ctx, task.ID); err == nil {
		t.Fatal("auto-resume should have reopened it already")
	}
	if _, ok := m.panes[task.Pane]; ok {
		t.Fatal("the old pane must not be back")
	}
	// Kill it again: the manual path, with auto off.
	f.AutoResume = false
	for id := range m.panes {
		m.Kill(ctx, id)
	}
	again, err := f.Resume(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Pane == task.Pane || again.Incarnation == task.Incarnation || again.Worktree != task.Worktree {
		t.Fatalf("resume: %+v", again)
	}
	runOf := func(id mux.ID) string {
		b, _ := os.ReadFile(m.argv[id][1])
		return string(b)
	}
	cmd := runOf(again.Pane)
	if strings.Contains(cmd, "--continue") || !strings.Contains(cmd, "keep going") || !strings.Contains(cmd, "---") {
		t.Errorf("with no session to continue, resume starts over with the brief: %q", cmd)
	}
	// With a session, it continues instead.
	sessions = true
	for id := range m.panes {
		m.Kill(ctx, id)
	}
	again, err = f.Resume(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	cmd = runOf(again.Pane)
	if !strings.Contains(cmd, "--continue") || strings.Contains(cmd, "---") {
		t.Errorf("resume should continue, not re-brief: %q", cmd)
	}
	// An agent that exited and left its shell reads as gone too, and
	// resume replaces the shell pane rather than adding to it.
	shell := m.panes[again.Pane]
	shell.Command = "zsh"
	m.panes[again.Pane] = shell
	f.AutoResume = false
	t0 := time.Now().Add(-time.Minute)
	tk, _ := f.Store.Load(task.ID)
	tk.Resumed, tk.Spawned = t0, t0
	f.Store.Save(tk)
	views, _ = f.Tasks(ctx)
	if views[0].State != Gone {
		t.Fatalf("a bare shell should read as gone, got %s", views[0].State)
	}
	third, err := f.Resume(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.panes[again.Pane]; ok {
		t.Error("the shell pane should have been killed")
	}
	if third.Pane == again.Pane {
		t.Error("resume should open a new pane")
	}
	if p := m.panes[third.Pane]; p.Path != task.Worktree {
		t.Errorf("resumed elsewhere: %+v", p)
	}
	views, _ = f.Tasks(ctx)
	if views[0].State != Running {
		t.Errorf("after resume: %s", views[0].State)
	}
}

func TestProjectsResolveByName(t *testing.T) {
	r := newRepo(t)
	m := newFakeMux()
	m.Spawn(context.Background(), mux.Spawn{Session: "x", Dir: r.Root, Command: []string{"zsh"}})
	p := &Projects{Mux: m, File: filepath.Join(t.TempDir(), "projects.json")}
	ctx := context.Background()
	got, err := p.Resolve(ctx, "PROJ")
	if err != nil || got.Root != r.Root {
		t.Fatalf("by name: %v %+v", err, got)
	}
	if got, err := p.Resolve(ctx, r.Root); err != nil || got.Root != r.Root {
		t.Fatalf("by path: %v %+v", err, got)
	}
	if _, err := p.Resolve(ctx, "nope"); err == nil || !strings.Contains(err.Error(), "known: proj") {
		t.Fatalf("miss should list names: %v", err)
	}
	// Forgotten by the mux, still known: it was remembered.
	m.panes = map[mux.ID]mux.Pane{}
	p2 := &Projects{Mux: m, File: p.File}
	if got, err := p2.Resolve(ctx, "proj"); err != nil || got.Root != r.Root {
		t.Fatalf("remembered: %v %+v", err, got)
	}
}

// An idle prompt after the agent said done is the harness at rest,
// not a finished task reopened as blocked; a permission prompt after
// done is still a question.
func TestHookIdleAfterDone(t *testing.T) {
	store := NewStore(t.TempDir())
	f := &Fleet{Store: store}
	task := &Task{ID: "aa11", Incarnation: "x"}
	if err := store.Save(task); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(task.ID, Status{Verb: Done, Text: "shipped", By: "agent"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Hook(task.ID, "x", HookEvent{Name: "Notification", NotificationType: "idle_prompt", Message: "Claude is waiting for your input"}); err != nil {
		t.Fatal(err)
	}
	if last, _ := store.Last(task.ID); last.Verb != Done {
		t.Fatalf("idle prompt after done reopened the task: %s", last.Verb)
	}
	if err := f.Hook(task.ID, "x", HookEvent{Name: "Notification", NotificationType: "permission_prompt", Message: "Bash?"}); err != nil {
		t.Fatal(err)
	}
	if last, _ := store.Last(task.ID); last.Verb != Blocked {
		t.Fatalf("a permission prompt is a question: %s", last.Verb)
	}
}

// The supervisor lands a ship task that says done; a landing that
// fails is a decision, tried again only after a newer done.
func TestAutoLand(t *testing.T) {
	r := newRepo(t)
	m := newFakeMux()
	f := New(m, NewStore(filepath.Join(t.TempDir(), "fleet")))
	f.Harness = []string{"fake-agent"}
	f.Trust = nil
	var events []Event
	f.Observe = func(ev Event) { events = append(events, ev) }
	ctx := context.Background()
	task, err := f.Spawn(ctx, Request{Project: r.Root, Brief: "ship it", Kind: Ship})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(task.Worktree, "thing.txt"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(task.Worktree, "README"), []byte("hi\nfrom the branch\n"), 0o644)
	gitRun(t, task.Worktree, "add", "thing.txt", "README")
	gitRun(t, task.Worktree, "commit", "-q", "-m", "the thing")

	// The main checkout has uncommitted work on a file the branch also
	// changes: landing cannot happen yet. (An untracked scratch file
	// and unrelated edits would not have stood in the way.)
	os.WriteFile(filepath.Join(r.Root, "scratch.txt"), []byte("untracked, harmless\n"), 0o644)
	os.WriteFile(filepath.Join(r.Root, "README"), []byte("hi\nlocal edit\n"), 0o644)
	if err := f.Report(task.ID, Status{Verb: Done, Text: "shipped", By: "agent"}); err != nil {
		t.Fatal(err)
	}
	f.sweep(ctx)
	last, _ := f.Store.Last(task.ID)
	if last.Verb != Blocked || !strings.Contains(last.Text, "could not land") {
		t.Fatalf("a failed landing should be a decision: %+v", last)
	}
	if events[len(events)-1].State != Decision {
		t.Fatalf("state after failed landing: %s", events[len(events)-1].State)
	}
	if _, ok := m.panes[task.Pane]; !ok {
		t.Fatal("the agent's pane must survive a failed landing")
	}
	n := len(events)
	f.sweep(ctx)
	if len(events) != n {
		t.Fatal("a failed landing should not be retried without a newer done")
	}

	// The person cleans up the one clash; the scratch file stays; the
	// agent says done again; it lands.
	gitRun(t, r.Root, "checkout", "README")
	time.Sleep(10 * time.Millisecond)
	if err := f.Report(task.ID, Status{Verb: Done, Text: "shipped, again", By: "agent"}); err != nil {
		t.Fatal(err)
	}
	f.sweep(ctx)
	if _, err := os.Stat(filepath.Join(r.Root, "thing.txt")); err != nil {
		t.Fatal("not merged home")
	}
	last, _ = f.Store.Last(task.ID)
	if last.Verb != Done || !strings.Contains(last.Text, "merged "+task.Branch) || !strings.Contains(last.Text, "1 commit)") {
		t.Errorf("landing status: %+v", last)
	}
	if _, ok := m.panes[task.Pane]; ok {
		t.Error("pane not killed after landing")
	}
	if events[len(events)-1].State != Closed {
		t.Errorf("state after landing: %s", events[len(events)-1].State)
	}
	if out := gitRun(t, r.Root, "log", "--oneline", "-1"); !strings.Contains(out, "Merge") {
		t.Errorf("landing should be a merge commit (--no-ff): %s", out)
	}
}

// A no-mistakes task passes the repository's checks first.
func TestAutoLandChecks(t *testing.T) {
	r := newRepo(t)
	os.WriteFile(filepath.Join(r.Root, "rook.toml"), []byte("[land]\ncheck = [\"true\", \"false\"]\n"), 0o644)
	gitRun(t, r.Root, "add", "rook.toml")
	gitRun(t, r.Root, "commit", "-q", "-m", "conventions")
	m := newFakeMux()
	f := New(m, NewStore(filepath.Join(t.TempDir(), "fleet")))
	f.Harness = []string{"fake-agent"}
	f.Trust = nil
	var ran []string
	f.Run = func(_ context.Context, dir, command string) (string, error) {
		ran = append(ran, command)
		if command == "false" {
			return "FAIL: TestX\n", errors.New("exit 1")
		}
		return "", nil
	}
	ctx := context.Background()
	task, err := f.Spawn(ctx, Request{Project: r.Root, Brief: "carefully", Kind: Ship, Mode: NoMistakes})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(task.Worktree, "thing.txt"), []byte("x\n"), 0o644)
	gitRun(t, task.Worktree, "add", "thing.txt")
	gitRun(t, task.Worktree, "commit", "-q", "-m", "the thing")
	f.Report(task.ID, Status{Verb: Done, Text: "shipped", By: "agent"})
	f.sweep(ctx)
	if len(ran) != 2 || ran[0] != "true" {
		t.Fatalf("checks run: %v", ran)
	}
	last, _ := f.Store.Last(task.ID)
	if last.Verb != Blocked || !strings.Contains(last.Text, "check `false` failed: FAIL: TestX") {
		t.Fatalf("a failed check should be the decision: %+v", last)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "thing.txt")); err == nil {
		t.Fatal("merged despite a failed check")
	}
}

// A scout closes when its report has been seen, not before.
func TestScoutClosesWhenSeen(t *testing.T) {
	r := newRepo(t)
	m := newFakeMux()
	f := New(m, NewStore(filepath.Join(t.TempDir(), "fleet")))
	f.Harness = []string{"fake-agent"}
	f.Trust = nil
	ctx := context.Background()
	task, err := f.Spawn(ctx, Request{Project: r.Root, Brief: "look around", Kind: Scout})
	if err != nil {
		t.Fatal(err)
	}
	f.Report(task.ID, Status{Verb: Done, Text: "report written", By: "agent"})
	f.sweep(ctx)
	views, _ := f.Tasks(ctx)
	if views[0].State != Finished || views[0].Closed {
		t.Fatalf("a finished scout nobody has read stays open: %+v", views[0].State)
	}
	if err := f.Seen(task.ID); err != nil {
		t.Fatal(err)
	}
	f.sweep(ctx)
	views, _ = f.Tasks(ctx)
	if !views[0].Closed {
		t.Fatal("a scout whose report was seen should close")
	}
	if _, ok := m.panes[task.Pane]; ok {
		t.Error("scout pane not killed")
	}
}

// notebook is a Notes that keeps what it was told, in memory.
type notebook struct {
	projects map[string]string // name → root, branch and conventions, joined
	landings []string
}

func (n *notebook) Project(name, root, branch string, conventions []string) error {
	if n.projects == nil {
		n.projects = map[string]string{}
	}
	n.projects[name] = root + " " + branch + " " + strings.Join(conventions, "; ")
	return nil
}

func (n *notebook) Landed(name, root, task, brief string) error {
	n.landings = append(n.landings, name+" "+task+" "+brief)
	return nil
}

// The fleet already learns where a repository is and what its
// conventions say on every task, and then throws it away. This is
// where it stops throwing it away.
func TestTheFleetWritesDownTheRepositoryItWorksIn(t *testing.T) {
	r := newRepo(t)
	m := newFakeMux()
	f := New(m, NewStore(filepath.Join(t.TempDir(), "fleet")))
	f.Harness = []string{"fake-agent"}
	f.Trust = nil
	note := &notebook{}
	f.Notes = note
	ctx := context.Background()

	task, err := f.Spawn(ctx, Request{Project: r.Root, Brief: "do the thing\nand tell nobody", Kind: Ship})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := note.projects["proj"]
	if !ok {
		t.Fatalf("spawning taught Vera nothing about the repo: %+v", note.projects)
	}
	if !strings.Contains(got, r.Root) || !strings.Contains(got, "main") {
		t.Errorf("the project note misses where the repo is: %q", got)
	}
	// rook.toml is the conventions a person already wrote down once.
	if !strings.Contains(got, ".env") {
		t.Errorf("the conventions did not make it into the note: %q", got)
	}

	os.WriteFile(filepath.Join(task.Worktree, "thing.txt"), []byte("x\n"), 0o644)
	gitRun(t, task.Worktree, "add", "thing.txt")
	gitRun(t, task.Worktree, "commit", "-q", "-m", "the thing")
	if err := f.Land(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if len(note.landings) != 1 || !strings.Contains(note.landings[0], task.Name) {
		t.Fatalf("the landing was not written down: %+v", note.landings)
	}
	if !strings.Contains(note.landings[0], "do the thing") {
		t.Errorf("the landing line does not say what the task was: %q", note.landings[0])
	}
}

// A notebook that cannot be written in must not stop work.
func TestANotebookFailureDoesNotStopATask(t *testing.T) {
	r := newRepo(t)
	m := newFakeMux()
	f := New(m, NewStore(filepath.Join(t.TempDir(), "fleet")))
	f.Harness = []string{"fake-agent"}
	f.Trust = nil
	f.Notes = brokenNotes{}
	if _, err := f.Spawn(context.Background(), Request{Project: r.Root, Brief: "still works", Kind: Scout}); err != nil {
		t.Fatalf("a task refused to start because a note could not be written: %v", err)
	}
}

type brokenNotes struct{}

func (brokenNotes) Project(string, string, string, []string) error {
	return errors.New("read-only file system")
}
func (brokenNotes) Landed(string, string, string, string) error {
	return errors.New("read-only file system")
}

// Landing means running. In the repository the daemon is built from,
// a merge is followed by a build into the directory the daemon came
// from, and the notice says what to do about it.
func TestLandingRebuildsTheBinaries(t *testing.T) {
	r := newRepo(t)
	// The repository has commands, and says so the way Go does.
	for _, name := range []string{"vera", "verad"} {
		if err := os.MkdirAll(filepath.Join(r.Root, "cmd", name), 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(r.Root, "cmd", name, "main.go"), []byte("package main\n"), 0o644)
	}
	os.WriteFile(filepath.Join(r.Root, "rook.toml"), []byte("[land]\ninstall = true\n"), 0o644)
	gitRun(t, r.Root, "add", "cmd", "rook.toml")
	gitRun(t, r.Root, "commit", "-q", "-m", "commands")

	m := newFakeMux()
	f := New(m, NewStore(filepath.Join(t.TempDir(), "fleet")))
	f.Harness = []string{"fake-agent"}
	f.Trust = nil
	f.InstallDir = t.TempDir()
	var built []string
	f.Run = func(_ context.Context, dir, command string) (string, error) {
		if dir != r.Root {
			t.Errorf("the build should run in the main checkout, not %s", dir)
		}
		built = append(built, command)
		return "", nil
	}

	ctx := context.Background()
	task, err := f.Spawn(ctx, Request{Project: r.Root, Brief: "change the daemon", Kind: Ship})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(task.Worktree, "thing.txt"), []byte("x\n"), 0o644)
	gitRun(t, task.Worktree, "add", "thing.txt")
	gitRun(t, task.Worktree, "commit", "-q", "-m", "the thing")
	f.Report(task.ID, Status{Verb: Done, Text: "shipped", By: "agent"})
	f.sweep(ctx)

	if len(built) != 2 {
		t.Fatalf("both commands should have been built: %v", built)
	}
	for i, name := range []string{"vera", "verad"} {
		// Built beside the target and renamed over it: one of these is
		// the process doing the building.
		final := filepath.Join(f.InstallDir, name)
		want := "go build -ldflags \"-X main.version=$(git describe --always --dirty)\" -o '" + final + ".new' ./cmd/" + name +
			" && mv -f '" + final + ".new' '" + final + "'"
		if built[i] != want {
			t.Errorf("build %d:\n got %s\nwant %s", i, built[i], want)
		}
	}
	statuses, _ := f.Store.Statuses(task.ID)
	last := statuses[len(statuses)-1]
	want := "landed " + task.ID + " — vera and verad rebuilt; run vera restart to pick it up"
	if last.Text != want {
		t.Errorf("notice:\n got %q\nwant %q", last.Text, want)
	}
}

// A build that fails is a landing that failed: the task is blocked
// with the error, and the room stays open so it can be told.
func TestABrokenBuildBlocksTheLanding(t *testing.T) {
	r := newRepo(t)
	os.MkdirAll(filepath.Join(r.Root, "cmd", "vera"), 0o755)
	os.WriteFile(filepath.Join(r.Root, "cmd", "vera", "main.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(r.Root, "rook.toml"), []byte("[land]\ninstall = true\n"), 0o644)
	gitRun(t, r.Root, "add", "cmd", "rook.toml")
	gitRun(t, r.Root, "commit", "-q", "-m", "commands")

	m := newFakeMux()
	f := New(m, NewStore(filepath.Join(t.TempDir(), "fleet")))
	f.Harness = []string{"fake-agent"}
	f.Trust = nil
	f.InstallDir = t.TempDir()
	f.Run = func(context.Context, string, string) (string, error) {
		return "cmd/vera/main.go:3:2: undefined: nope\n", errors.New("exit 1")
	}

	ctx := context.Background()
	task, _ := f.Spawn(ctx, Request{Project: r.Root, Brief: "break it", Kind: Ship})
	os.WriteFile(filepath.Join(task.Worktree, "thing.txt"), []byte("x\n"), 0o644)
	gitRun(t, task.Worktree, "add", "thing.txt")
	gitRun(t, task.Worktree, "commit", "-q", "-m", "the thing")
	f.Report(task.ID, Status{Verb: Done, Text: "shipped", By: "agent"})
	f.sweep(ctx)

	last, _ := f.Store.Last(task.ID)
	if last.Verb != Blocked || !strings.Contains(last.Text, "undefined: nope") {
		t.Fatalf("a failed build should block the task with its error: %+v", last)
	}
	if _, ok := m.panes[task.Pane]; !ok {
		t.Error("the agent's pane must survive a failed build, so it can be told")
	}
}

// Everywhere else, landing is a merge and nothing more.
func TestLandingDoesNotBuildInOtherRepositories(t *testing.T) {
	r := newRepo(t)
	os.MkdirAll(filepath.Join(r.Root, "cmd", "thing"), 0o755)
	gitRun(t, r.Root, "add", "-A")
	gitRun(t, r.Root, "commit", "-q", "--allow-empty", "-m", "commands")

	f := New(newFakeMux(), NewStore(filepath.Join(t.TempDir(), "fleet")))
	f.InstallDir = t.TempDir()
	ran := 0
	f.Run = func(context.Context, string, string) (string, error) { ran++; return "", nil }
	built, err := f.install(context.Background(), r)
	if err != nil || built != "" || ran != 0 {
		t.Fatalf("a repository that did not ask should not be built: %q %v (%d commands run)", built, err, ran)
	}

	// Unless it says so.
	os.WriteFile(filepath.Join(r.Root, "rook.toml"), []byte("[land]\ninstall = true\n"), 0o644)
	if built, err := f.install(context.Background(), r); err != nil || built != "thing rebuilt" {
		t.Fatalf("with [land] install = true: %q %v", built, err)
	}
}

// The default is a fact about one repository, and rook.toml overrides
// it either way.
func TestInstallsByDefaultOnlyForVera(t *testing.T) {
	if !(Conventions{}).Installs("vera") {
		t.Error("vera should install by default")
	}
	if (Conventions{}).Installs("rook") {
		t.Error("nothing else should")
	}
	no := false
	if (Conventions{Install: &no}).Installs("vera") {
		t.Error("[land] install = false should win for vera too")
	}
	yes := true
	if !(Conventions{Install: &yes}).Installs("rook") {
		t.Error("[land] install = true should win anywhere")
	}
}
