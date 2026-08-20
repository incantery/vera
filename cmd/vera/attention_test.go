package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAttentionPublishesTheWaitingSet(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	now := time.Now()

	waiting, _ := s.tasks.capture("pick a database", now.Add(-time.Hour))
	s.tasks.mutate(waiting.ID, func(x *task) error {
		x.Col, x.Ask, x.Workspace = "waiting", "postgres or sqlite?", "/Users/x/dev/rook"
		return nil
	})
	s.tasks.capture("still in the inbox", now.Add(-time.Hour))

	sys := &attentionSystem{s: s, path: filepath.Join(t.TempDir(), "attention.jsonl")}
	acts := sys.Tick(s.world(now))
	if len(acts) != 1 || !acts[0].Free {
		t.Fatalf("one free publish owed, got %+v", acts)
	}
	acts[0].Run()

	b, err := os.ReadFile(sys.path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("the waiting card plus the spend line belong in the feed:\n%s", b)
	}
	for _, want := range []string{waiting.ID, "postgres or sqlite?", `"kind":"waiting"`, `"dir":"/Users/x/dev/rook"`, `"source":"vera"`, `"at":"`} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("feed line must carry %q:\n%s", want, lines[0])
		}
	}
	if !strings.Contains(lines[1], `"kind":"spend"`) || !strings.Contains(lines[1], "today") {
		t.Fatalf("the second line is the day's spend:\n%s", lines[1])
	}

	// Unchanged set, fresh file: nothing owed.
	if acts := sys.Tick(s.world(now)); len(acts) != 0 {
		t.Fatalf("no change must mean no write: %+v", acts)
	}

	// The ask gets answered: the set changes, and the feed empties —
	// a resolved ask leaves the bar.
	s.tasks.mutate(waiting.ID, func(x *task) error { x.Col = "progress"; return nil })
	acts = sys.Tick(s.world(now))
	if len(acts) != 1 {
		t.Fatalf("emptying the set is a change: %+v", acts)
	}
	acts[0].Run()
	if b, _ := os.ReadFile(sys.path); strings.Contains(string(b), "waiting") {
		t.Fatalf("resolved asks must leave the feed (spend line may stay):\n%s", b)
	}
}

func TestAttentionRestampsAnHourOldFeed(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	now := time.Now()

	w, _ := s.tasks.capture("the ask", now.Add(-time.Hour))
	s.tasks.mutate(w.ID, func(x *task) error {
		x.Col, x.Ask = "waiting", "yes or no?"
		return nil
	})
	sys := &attentionSystem{s: s, path: filepath.Join(t.TempDir(), "attention.jsonl")}
	acts := sys.Tick(s.world(now))
	if len(acts) != 1 {
		t.Fatalf("first publish owed: %+v", acts)
	}
	acts[0].Run()

	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(sys.path, old, old); err != nil {
		t.Fatal(err)
	}
	// Same set, stale file: rook's 24h guard is approaching, re-stamp.
	acts = sys.Tick(s.world(now))
	if len(acts) != 1 {
		t.Fatalf("an hour-old feed must be re-stamped: %+v", acts)
	}
}
