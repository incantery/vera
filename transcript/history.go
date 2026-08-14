// The history read: one transcript rendered as the conversation it
// records — user turns, assistant turns, tool work folded to a count.
// A fork's transcript replays the whole conversation it continued, so
// reading the NEWEST fork in a lineage is reading the agent's life to
// date; that is what makes an agent page possible without stitching.
package transcript

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// Msg is one bubble of the conversation.
type Msg struct {
	Role  string    `json:"role"` // "user" | "assistant"
	Text  string    `json:"text"`
	At    time.Time `json:"at,omitzero"`
	Tools int       `json:"tools,omitempty"` // tool calls folded into this turn
	Steps []Step    `json:"steps,omitempty"` // the same calls, named and in order
}

// Step is one tool call as a reader would skim it: the tool and the
// most human-salient input field. The step list is capped; Tools keeps
// the true count.
type Step struct {
	Tool   string `json:"tool"`
	Detail string `json:"detail,omitempty"`
}

// stepCap bounds a turn's step list — a 200-call turn reads as its
// first stepCap steps plus the count, not as a wall.
const stepCap = 40

// historyBytes bounds one history read. Deep transcripts lose their
// oldest turns off the top, which the page says honestly.
const historyBytes = 1 << 20

// History renders a transcript's tail as conversation turns, oldest
// first. Assistant stream lines merge into one turn until a human
// speaks again; tool calls and their results fold into that turn's
// count. Harness noise (command wrappers, tool results) is not
// conversation and does not appear.
func History(path string) []Msg {
	data, err := readTail(path, historyBytes)
	if err != nil {
		return nil
	}
	var out []Msg
	var cur *Msg // the assistant turn being accumulated
	flush := func() {
		if cur != nil && (cur.Text != "" || cur.Tools > 0) {
			out = append(out, *cur)
		}
		cur = nil
	}
	for raw := range bytes.SplitSeq(data, []byte("\n")) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var l wireLine
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		// The /compact continuation summary (and anything else the
		// harness marks transcript-only) is plumbing written through
		// the user's mouth, not conversation.
		if l.IsSidechain || l.IsCompactSum || l.TranscriptOnly || l.Message == nil {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, l.Timestamp)
		switch l.Type {
		case "user":
			if isToolResult(l.Message.Content) {
				continue // the assistant's own machinery coming back
			}
			text := strings.TrimSpace(contentText(l.Message.Content))
			if text == "" || harnessNoise(text) {
				continue
			}
			flush()
			out = append(out, Msg{Role: "user", Text: text, At: ts})
		case "assistant":
			if cur == nil {
				cur = &Msg{Role: "assistant", At: ts}
			}
			cur.Tools += countToolUse(l.Message.Content)
			for _, st := range toolSteps(l.Message.Content) {
				if len(cur.Steps) < stepCap {
					cur.Steps = append(cur.Steps, st)
				}
			}
			if t := contentText(l.Message.Content); t != "" {
				if cur.Text != "" {
					cur.Text += "\n\n"
				}
				cur.Text += t
			}
		}
	}
	flush()
	return out
}

// harnessNoise: lines the harness wrote through the user's mouth —
// local-command wrappers, interruption markers — are plumbing, not
// something the human said.
func harnessNoise(text string) bool {
	for _, p := range []string{"<local-command", "<command-name>", "[Request interrupted"} {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

func toolSteps(raw json.RawMessage) []Step {
	var blocks []struct {
		Type  string          `json:"type"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []Step
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name != "" {
			out = append(out, Step{Tool: b.Name, Detail: toolDetail(b.Input)})
		}
	}
	return out
}

func countToolUse(raw json.RawMessage) int {
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return 0
	}
	n := 0
	for _, b := range blocks {
		if b.Type == "tool_use" {
			n++
		}
	}
	return n
}
