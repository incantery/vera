package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// assistantText is a mid-turn assistant stream line; endTurn (from the
// scanner's fixtures) is the turn's last.
func assistantText(at time.Duration, text string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":"%s","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, ts(at), text)
}

func assistantEndTurn(at time.Duration, text string) string { return endTurn(at, text) }

func writeHistoryFixture(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHistoryRendersTurnsAndFoldsTools(t *testing.T) {
	path := writeHistoryFixture(t,
		prompt(0, "fix the flaky test"),
		toolUse(1),
		toolResult(2),
		assistantText(3, "Found it — the sleep races the poll."),
		assistantEndTurn(4, "Fixed: the test now waits on the channel."),
		prompt(5, "ship it"),
		assistantEndTurn(6, "Tagged and pushed."),
	)
	h := History(path)
	if len(h) != 4 {
		t.Fatalf("turns: %d — %+v", len(h), h)
	}
	if h[0].Role != "user" || h[0].Text != "fix the flaky test" {
		t.Fatalf("h[0]: %+v", h[0])
	}
	// One assistant turn: two stream lines merged, one tool folded in.
	if h[1].Role != "assistant" || h[1].Tools != 1 ||
		!strings.Contains(h[1].Text, "races the poll") || !strings.Contains(h[1].Text, "waits on the channel") {
		t.Fatalf("h[1]: %+v", h[1])
	}
	if h[2].Text != "ship it" || h[3].Text != "Tagged and pushed." {
		t.Fatalf("tail: %+v %+v", h[2], h[3])
	}
}

func TestHistoryDropsHarnessNoiseAndSidechains(t *testing.T) {
	path := writeHistoryFixture(t,
		prompt(0, "<local-command-caveat>Caveat: local commands</local-command-caveat>"),
		prompt(1, "[Request interrupted by user]"),
		`{"type":"assistant","timestamp":"`+ts(2)+`","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent chatter"}]}}`,
		prompt(3, "the real question"),
		assistantEndTurn(4, "the real answer"),
	)
	h := History(path)
	if len(h) != 2 || h[0].Text != "the real question" || h[1].Text != "the real answer" {
		t.Fatalf("h: %+v", h)
	}
}

// Claude Code writes the /compact continuation summary into the
// transcript as a user record marked isCompactSummary (and
// isVisibleInTranscriptOnly) — tens of thousands of characters of
// harness plumbing the conversation view must never render as
// something the human typed.
func TestHistoryHidesTheCompactSummary(t *testing.T) {
	path := writeHistoryFixture(t,
		prompt(0, "real question before the compact"),
		assistantEndTurn(1, "real answer"),
		`{"type":"user","timestamp":"`+ts(2)+`","isCompactSummary":true,"isVisibleInTranscriptOnly":true,"message":{"role":"user","content":"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion..."}}`,
		prompt(3, "real question after the compact"),
	)
	h := History(path)
	for _, m := range h {
		if strings.HasPrefix(m.Text, "This session is being continued") {
			t.Fatalf("the compact summary surfaced as a %s turn — harness plumbing rendered as conversation", m.Role)
		}
	}
	if len(h) != 3 {
		t.Fatalf("want the 3 real turns, got %d: %+v", len(h), h)
	}
}

// The step timeline: a turn's tool calls, named and in order, beside
// the folded count — direct mode renders them the way the TUI would.
func TestHistoryCarriesToolSteps(t *testing.T) {
	path := writeHistoryFixture(t,
		prompt(0, "run the tests"),
		`{"type":"assistant","timestamp":"`+ts(1)+`","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./...","description":"Run the suite"}}]}}`,
		toolResult(2),
		`{"type":"assistant","timestamp":"`+ts(3)+`","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/tmp/x.go"}}]}}`,
		toolResult(4),
		assistantEndTurn(5, "done"),
	)
	h := History(path)
	if len(h) != 2 || h[1].Tools != 2 {
		t.Fatalf("h: %+v", h)
	}
	steps := h[1].Steps
	if len(steps) != 2 ||
		steps[0].Tool != "Bash" || steps[0].Detail != "Run the suite" ||
		steps[1].Tool != "Edit" || steps[1].Detail != "/tmp/x.go" {
		t.Fatalf("steps: %+v", steps)
	}
}

func TestHistoryOfAMissingFileIsEmptyNotAnError(t *testing.T) {
	if h := History(filepath.Join(t.TempDir(), "gone.jsonl")); h != nil {
		t.Fatalf("h: %+v", h)
	}
}

func TestScanCarriesTheLatestToolCall(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "s-tool", 0,
		prompt(0, "run the tests"),
		`{"type":"assistant","timestamp":"`+ts(1)+`","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./...","description":"Run the suite"}}]}}`,
	)
	s := one(t, testScanner(dir), t0.Add(2*time.Minute))
	if s.ToolName != "Bash" || s.ToolDetail != "Run the suite" {
		t.Fatalf("tool: %q %q", s.ToolName, s.ToolDetail)
	}
}

func TestScanKeepsTheUsageSplit(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "s-usage", 0,
		prompt(0, "go"),
		`{"type":"assistant","timestamp":"`+ts(1)+`","message":{"role":"assistant","model":"claude-fable-5","stop_reason":"end_turn","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":1200,"cache_read_input_tokens":300000,"cache_creation_input_tokens":5000,"output_tokens":800}}}`,
	)
	s := one(t, testScanner(dir), t0.Add(2*time.Minute))
	if s.CtxTokens != 307000 || s.CtxIn != 1200 || s.CtxCacheRd != 300000 || s.CtxCacheWr != 5000 || s.CtxOut != 800 {
		t.Fatalf("split: %+v", s)
	}
	if s.Model != "claude-fable-5" {
		t.Fatalf("model: %q", s.Model)
	}
}
