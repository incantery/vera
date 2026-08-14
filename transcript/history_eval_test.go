//go:build eval

// Part of the issue-reproduction suite (`go test -tags eval ./...`):
// deterministic repros live here beside the LLM evals, red until the
// bug they pin is fixed, then they move into the default suite.
package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Claude Code writes the /compact continuation summary into the
// transcript as a user record marked isCompactSummary (and
// isVisibleInTranscriptOnly). It is harness plumbing, tens of
// thousands of characters of internal state — but History renders it
// as if the human typed it, so the conversation view opens on a wall
// of summary after every compact.
func TestHistoryHidesTheCompactSummary(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2026-08-13T20:00:00Z","message":{"role":"user","content":"real question before the compact"}}`,
		`{"type":"assistant","timestamp":"2026-08-13T20:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"real answer"}]}}`,
		`{"type":"user","timestamp":"2026-08-13T20:10:00Z","isCompactSummary":true,"isVisibleInTranscriptOnly":true,"message":{"role":"user","content":"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion..."}}`,
		`{"type":"user","timestamp":"2026-08-13T20:11:00Z","message":{"role":"user","content":"real question after the compact"}}`,
	}
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs := History(path)
	for _, m := range msgs {
		if strings.HasPrefix(m.Text, "This session is being continued") {
			t.Fatalf("the compact summary surfaced as a %s turn (%d chars) — harness plumbing rendered as conversation", m.Role, len(m.Text))
		}
	}
	if len(msgs) != 3 {
		t.Fatalf("want the 3 real turns, got %d", len(msgs))
	}
}
