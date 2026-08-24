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
	for _, want := range []string{"/w/repo--dm", "never cd into /w/repo", "gh pr create", "Do not merge", "summary of what you changed", "/state/fleet/x/report.md", "curl -s -X POST http://127.0.0.1:4780/fleet/x/status", "blocked", "done"} {
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

	// Untrusted main checkout: nothing is written.
	write(map[string]any{"other": "kept", "projects": map[string]any{"/r": map[string]any{"hasTrustDialogAccepted": false}}})
	if err := inheritTrust("/r", "/r--wt"); err == nil {
		t.Error("should refuse when the main checkout is untrusted")
	}
	if _, ok := read()["/r--wt"]; ok {
		t.Error("wrote trust it should not have")
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
