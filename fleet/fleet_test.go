package fleet

import (
	"context"
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
	mu    sync.Mutex
	panes map[mux.ID]mux.Pane
	typed []string
	next  int
	down  bool
}

func newFakeMux() *fakeMux { return &fakeMux{panes: map[mux.ID]mux.Pane{}} }

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
	p := mux.Pane{ID: mux.ID{Session: s.Session, Window: string(rune('0' + f.next)), Pane: "0"}, Command: s.Command[0], Path: s.Dir, Active: time.Now()}
	f.panes[p.ID] = p
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
	f.TurnEndedURL = func(id, inc string) string {
		return "http://127.0.0.1:1/fleet/" + id + "/turn-ended?incarnation=" + inc
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
	settings, _ := os.ReadFile(filepath.Join(store.TaskDir(task.ID), "claude.json"))
	if !strings.Contains(string(settings), "turn-ended?incarnation="+task.Incarnation) {
		t.Errorf("hook settings: %s", settings)
	}

	f.sweep(ctx)
	if len(events) != 1 || events[0].State != Running {
		t.Fatalf("after spawn: %+v", events)
	}
	f.sweep(ctx)
	if len(events) != 1 {
		t.Fatal("unchanged state reported again")
	}

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
	if strings.Join(m.typed, "") != "use the second option\n" {
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
	views, _ := f.Tasks(ctx)
	if len(views) != 1 || views[0].State != Decision || len(views[0].Unread) != 3 {
		t.Fatalf("views %+v", views)
	}
	store.Present(task.ID, 3)
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
