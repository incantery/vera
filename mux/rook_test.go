package mux

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseBlocks(t *testing.T) {
	text := "1\tmain:1\tversions\t103x42\t/Users/me/rook\n3\tmain:2\tzsh\t103x42\t/Users/me/rook\n7\tglobal:pin\tclaude\t40x42\t/x\nbad line\n"
	bs := parseBlocks(text)
	if len(bs) != 3 {
		t.Fatalf("got %d blocks", len(bs))
	}
	if bs[0].id != 1 || bs[0].ws != "main" || bs[0].slot != "1" || bs[0].fg != "versions" || bs[0].cols != 103 || bs[0].rows != 42 || bs[0].cwd != "/Users/me/rook" {
		t.Errorf("%+v", bs[0])
	}
	if bs[2].slot != "pin" || bs[2].ws != "global" {
		t.Errorf("%+v", bs[2])
	}
	p := bs[1].pane()
	if p.ID != (ID{"main", "2", "3"}) || p.Command != "zsh" || p.Path != "/Users/me/rook" {
		t.Errorf("%+v", p)
	}
	if id, ok := blockID(p.ID); !ok || id != 3 {
		t.Error("blockID")
	}
	if _, ok := blockID(ID{"s", "1", "zero"}); ok {
		t.Error("non-numeric pane should not be a block")
	}
}

func TestRowsOf(t *testing.T) {
	// The shape blockSnapshot emits: sync on, clear+home+reset, rows
	// painted via CUP with SGR runs, cursor placed, sync off.
	frame := "\x1b[?2026h\x1b[2J\x1b[H\x1b[0m" +
		"\x1b[1;1H\x1b[38;2;1;2;3mhello\x1b[0m world" +
		"\x1b[2;1H$ " +
		"\x1b[3;1H\x1b[1mbold\x1b[0m\x1b[5Cafter" +
		"\x1b]2;title\x07" +
		"\x1b[2;3H\x1b[6 q\x1b[?25h\x1b[?2026l"
	rows := rowsOf([]byte(frame), 5)
	want := []string{"hello world", "$", "bold     after", "", ""}
	if strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", rows)
	}
	// Truncated escape at the end must not panic.
	_ = rowsOf([]byte("abc\x1b["), 2)
}

// TestRookLive drives the engine the person is running. Opt-in: it
// creates a workspace in THEIR rook, which switches their view.
func TestRookLive(t *testing.T) {
	if os.Getenv("VERA_ROOK_LIVE") == "" {
		t.Skip("set VERA_ROOK_LIVE=1 to run against the live rook (it will switch your workspace)")
	}
	r := NewRook("")
	ctx := context.Background()
	if _, err := r.List(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := r.Spawn(ctx, Spawn{Session: "vera-test", Dir: t.TempDir(), Command: []string{"sh", "-c", "echo hello from vera; exec cat"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Kill(ctx, p.ID) })
	time.Sleep(500 * time.Millisecond)
	lines, err := r.Capture(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "hello from vera") {
		t.Fatalf("capture: %q", lines)
	}
	if err := r.Send(ctx, p.ID, "typed by vera"); err != nil {
		t.Fatal(err)
	}
	if err := r.Enter(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	lines, _ = r.Capture(ctx, p.ID)
	if strings.Count(strings.Join(lines, "\n"), "typed by vera") != 2 {
		t.Fatalf("cat should echo: %q", lines)
	}
	// A second spawn into the same workspace is a new window.
	p2, err := r.Spawn(ctx, Spawn{Session: "vera-test", Command: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}
	if p2.ID.Session != "vera-test" || p2.ID.Pane == p.ID.Pane {
		t.Fatalf("second pane %+v", p2)
	}
	if err := r.Narrow(ctx, p2.ID, 40); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if b, _ := r.find(ctx, p2.ID); b.cols != 40 {
		t.Errorf("lease did not resize: %+v", b)
	}
	if err := r.Widen(ctx, p2.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.Kill(ctx, p2.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := r.Get(ctx, p2.ID); err != ErrNoPane {
		t.Errorf("after kill: %v", err)
	}
}
