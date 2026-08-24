package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Claude Code is told to ring Vera's doorbell.
//
// This is firstmate's Stop-hook idea without the machinery around it.
// firstmate needed the hook to re-arm a watcher and to block a "blind
// stop" — because between turns there was no process to notice. Vera
// is that process. So a hook is one curl of the harness's own event
// JSON to one loopback endpoint, and nothing it says is believed
// outright: the supervisor re-reads the pane and the worktree, same as
// it does for a mux event. A hook that stops firing degrades to
// polling.
//
// Two events matter. Stop: the agent's turn ended and it is waiting.
// Notification: the harness wants a person — a permission prompt, an
// idle prompt — which is the one thing a pane looking busy cannot
// tell you, and the thing that had Vera saying "running" at an agent
// stuck on "do you want to proceed?".

// harnessSettings is the subset of Claude Code's settings.json we write.
type harnessSettings struct {
	Permissions harnessPermissions       `json:"permissions"`
	Hooks       map[string][]hookMatcher `json:"hooks"`
}

type harnessPermissions struct {
	Allow []string `json:"allow,omitempty"`
}

type hookMatcher struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// writeHarnessSettings writes the settings file a task's `claude` is
// started with (--settings). hookURL is loopback-only on Vera's side;
// the incarnation rides in it so a pane from a previous spawn of the
// same task cannot speak for this one. statusURL, when set, is
// pre-approved: the brief tells the agent to curl it, and an agent
// asking permission to do what it was told to do is the first stall.
func writeHarnessSettings(dir, hookURL, statusURL string) (string, error) {
	// The hook's stdin is the event as JSON; it goes up verbatim.
	curl := "curl -s -m 2 -X POST " + hookURL + " -H 'Content-Type: application/json' --data-binary @- >/dev/null 2>&1 || true"
	s := harnessSettings{Hooks: map[string][]hookMatcher{
		"Stop":         {{Hooks: []hookCommand{{Type: "command", Command: curl, Timeout: 5}}}},
		"Notification": {{Hooks: []hookCommand{{Type: "command", Command: curl, Timeout: 5}}}},
	}}
	if statusURL != "" {
		s.Permissions.Allow = []string{"Bash(curl -s -X POST " + statusURL + "*)"}
	}
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

// HookEvent is what Claude Code sends a hook on stdin — the fields
// Vera reads. Unknown ones are ignored.
type HookEvent struct {
	Name             string `json:"hook_event_name"`
	Message          string `json:"message"`
	NotificationType string `json:"notification_type"`
	Title            string `json:"title"`
}

// writeEnvFile writes KEY=VALUE pairs as a file `sh` can source, mode
// 0600: this is where a telemetry token lives on disk, and nowhere
// else.
func writeEnvFile(dir string, env []string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		b.WriteString(k + "='" + strings.ReplaceAll(v, "'", `'\''`) + "'\n")
	}
	path := filepath.Join(dir, "env")
	return path, os.WriteFile(path, []byte(b.String()), 0o600)
}
