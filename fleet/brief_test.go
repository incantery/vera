package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffold(t *testing.T) {
	ship := &Task{Kind: Ship, Mode: DirectPR, Brief: "Add dark mode.", Worktree: "/w/repo--dm", Branch: "dm", Project: "/w/repo"}
	s := scaffold(ship, "http://127.0.0.1:4780/fleet/x/status", "/state/fleet/x/report.md")
	if !strings.HasPrefix(s, "Add dark mode.") {
		t.Error("the person's words come first")
	}
	for _, want := range []string{"/w/repo--dm", "never cd into /w/repo", "gh pr create", "Do not merge", "summary of what you changed", "/state/fleet/x/report.md", "do not restart or kill services", "curl -s -X POST http://127.0.0.1:4780/fleet/x/status", "blocked", "done",
		"vera events --repo repo --since 7d", "~/.local/state/vera/events"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	scout := &Task{Kind: Scout, Brief: "Why is it slow?", Worktree: "/w/repo"}
	s = scaffold(scout, "", "/state/fleet/y/report.md")
	if !strings.Contains(s, "Do not modify files") || strings.Contains(s, "curl") || !strings.Contains(s, "Write your report") {
		t.Errorf("scout scaffold: %s", s)
	}
}

func TestInheritTrust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude.json")
	write := func(v any) {
		b, _ := json.Marshal(v)
		os.WriteFile(path, b, 0o600)
	}
	read := func() map[string]map[string]any {
		b, _ := os.ReadFile(path)
		var top struct {
			Projects map[string]map[string]any `json:"projects"`
			Other    string                    `json:"other"`
		}
		json.Unmarshal(b, &top)
		if top.Other != "kept" {
			t.Error("unrelated keys must survive")
		}
		return top.Projects
	}

	// A main checkout Claude Code never trusted (or never opened):
	// the room is trusted anyway — Vera made it.
	write(map[string]any{"other": "kept", "projects": map[string]any{"/r": map[string]any{"hasTrustDialogAccepted": false}}})
	if err := inheritTrust("/r", "/r--wt"); err != nil {
		t.Fatal(err)
	}
	if read()["/r--wt"]["hasTrustDialogAccepted"] != true {
		t.Error("the room should be trusted regardless of the main checkout")
	}

	// Trusted: the worktree inherits, other fields untouched.
	write(map[string]any{"other": "kept", "projects": map[string]any{"/r": map[string]any{"hasTrustDialogAccepted": true, "mcpServers": map[string]any{}}}})
	if err := inheritTrust("/r", "/r--wt"); err != nil {
		t.Fatal(err)
	}
	p := read()
	if p["/r--wt"]["hasTrustDialogAccepted"] != true || p["/r"]["mcpServers"] == nil {
		t.Errorf("%v", p)
	}
	// Idempotent.
	if err := inheritTrust("/r", "/r--wt"); err != nil {
		t.Fatal(err)
	}
}

// A screenshot the person handed over rides in the brief, named as
// evidence. The agent in the room has eyes; Vera does not, and the
// whole point of the task may be in the picture.
func TestScaffoldNamesTheImages(t *testing.T) {
	task := &Task{
		Kind: Ship, Brief: "Fix the overlap in the header.",
		Worktree: "/w/repo--fix", Branch: "fix", Project: "/w/repo",
		Images: []string{"/state/vera/images/c/aa.png", "/state/vera/images/c/bb.png"},
	}
	s := scaffold(task, "", "")
	for _, want := range []string{"2 images", "/state/vera/images/c/aa.png", "/state/vera/images/c/bb.png", "do not commit"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	// The person's words are still the first thing the agent reads.
	if !strings.HasPrefix(s, "Fix the overlap in the header.") {
		t.Error("the images displaced the ask")
	}
	// And a task with none reads exactly as it did before.
	plain := scaffold(&Task{Kind: Ship, Brief: "Fix it.", Worktree: "/w", Project: "/w"}, "", "")
	if strings.Contains(plain, "attached") {
		t.Errorf("a task with no pictures talks about pictures:\n%s", plain)
	}
}
