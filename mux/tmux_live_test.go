package mux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestTmuxLive runs the backend against a real tmux on a throwaway
// socket. Skipped when tmux is not installed; never touches the
// person's servers.
func TestTmuxLive(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := fmt.Sprintf("vera-test-%d", os.Getpid())
	m := NewTmux(socket, "")
	ctx := context.Background()
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })

	if _, err := m.List(ctx); err != ErrUnavailable {
		t.Fatalf("no server yet: want ErrUnavailable, got %v", err)
	}

	p, err := m.Spawn(ctx, Spawn{Session: "vera-test", Name: "one", Dir: t.TempDir(), Command: []string{"sh", "-c", "echo hello from vera; sleep 30"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID.Session != "vera-test" {
		t.Fatalf("pane %+v", p)
	}
	// A second spawn into an existing session is a new window, not a
	// new session.
	p2, err := m.Spawn(ctx, Spawn{Session: "vera-test", Name: "two", Command: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}
	if p2.ID.Session != "vera-test" || p2.ID.Window == p.ID.Window {
		t.Fatalf("second pane %+v", p2)
	}

	panes, err := m.List(ctx)
	if err != nil || len(panes) != 2 {
		t.Fatalf("list: %v %+v", err, panes)
	}

	time.Sleep(200 * time.Millisecond)
	lines, err := m.Capture(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "hello from vera") {
		t.Fatalf("capture: %q", lines)
	}

	// Send + Enter reach the process: cat echoes.
	if err := m.Send(ctx, p2.ID, "typed by vera"); err != nil {
		t.Fatal(err)
	}
	if err := m.Enter(ctx, p2.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	lines, _ = m.Capture(ctx, p2.ID)
	if n := strings.Count(strings.Join(lines, "\n"), "typed by vera"); n != 2 {
		t.Fatalf("cat should show the line typed and echoed, got %d in %q", n, lines)
	}

	got, err := m.Get(ctx, p2.ID)
	if err != nil || got.Command != "cat" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := m.Get(ctx, ID{"vera-test", "99", "0"}); err != ErrNoPane {
		t.Fatalf("missing pane: want ErrNoPane, got %v", err)
	}

	// Nobody is attached, so there is no focus.
	if _, err := m.Focus(ctx); err != ErrNoFocus {
		t.Fatalf("focus: want ErrNoFocus, got %v", err)
	}

	if err := m.Kill(ctx, p2.ID); err != nil {
		t.Fatal(err)
	}
	panes, _ = m.List(ctx)
	if len(panes) != 1 {
		t.Fatalf("after kill: %+v", panes)
	}
}
