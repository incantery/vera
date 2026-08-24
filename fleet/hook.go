package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Claude Code is told to ring Vera's doorbell when its turn ends.
//
// This is firstmate's Stop-hook idea without the machinery around it.
// firstmate needed the hook to re-arm a watcher and to block a "blind
// stop" — because between turns there was no process to notice. Vera
// is that process. So the hook is one curl, it carries no data beyond
// which task and which incarnation, and nothing it says is believed:
// the supervisor re-reads the pane and the worktree, same as it does
// for a tmux hook. A hook that stops firing degrades to polling.

// harnessSettings is the subset of Claude Code's settings.json we write.
type harnessSettings struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

type hookMatcher struct {
	Hooks []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// writeHarnessSettings writes the settings file a task's `claude` is
// started with (--settings). turnEndedURL is loopback-only on Vera's
// side; the incarnation rides in the URL so a pane from a previous
// spawn of the same task cannot speak for this one.
func writeHarnessSettings(dir, turnEndedURL string) (string, error) {
	curl := "curl -s -m 2 -X POST " + turnEndedURL + " >/dev/null 2>&1 || true"
	s := harnessSettings{Hooks: map[string][]hookMatcher{
		"Stop": {{Hooks: []hookCommand{{Type: "command", Command: curl, Timeout: 5}}}},
	}}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "claude.json")
	return path, os.WriteFile(path, b, 0o644)
}
