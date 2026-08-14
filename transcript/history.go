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
	Think []string  `json:"think,omitempty"` // reasoning excerpts, bounded
	Ctx   int       `json:"ctx,omitempty"`   // context tokens after this turn
}

// Step is one tool call as a reader would skim it: the tool and the
// most human-salient input field. An edit carries its diff. The step
// list is capped; Tools keeps the true count.
type Step struct {
	Tool   string `json:"tool"`
	Detail string `json:"detail,omitempty"`
	Diff   *Diff  `json:"diff,omitempty"`
}

// Diff is one Edit/Write as the reader would review it — the old and
// new text, bounded, straight from the tool call's own input.
type Diff struct {
	File string `json:"file"`
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
	All  bool   `json:"all,omitempty"` // replace_all
}

// stepCap bounds a turn's step list — a 200-call turn reads as its
// first stepCap steps plus the count, not as a wall. thinkCap and
// diffChars bound the reasoning and diff payloads the same way.
const (
	stepCap   = 40
	thinkCap  = 3
	diffChars = 1600
)

// historyBytes bounds one history read. Deep transcripts lose their
// oldest turns off the top, which the page says honestly. 4MB, not
// 1MB: a single pasted screenshot rides the transcript as ~1MB of
// base64, and one image-heavy turn must not eat the whole scrollback.
const historyBytes = 4 << 20

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
			for _, th := range thinkingText(l.Message.Content) {
				if len(cur.Think) < thinkCap {
					cur.Think = append(cur.Think, th)
				}
			}
			if u := l.Message.Usage; u != nil {
				// The window after this turn: same sum the scanner uses.
				cur.Ctx = u.In + u.CacheRd + u.CacheWr
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
// local-command wrappers, interruption markers, background-task
// notifications, image-dimension notes — are plumbing, not something
// the human said.
func harnessNoise(text string) bool {
	for _, p := range []string{
		"<local-command", "<command-name>", "[Request interrupted",
		"[SYSTEM NOTIFICATION", "<task-notification>", "<system-reminder>",
		"[Image: original",
	} {
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
			out = append(out, Step{Tool: b.Name, Detail: toolDetail(b.Input), Diff: editDiff(b.Name, b.Input)})
		}
	}
	return out
}

// editDiff lifts an Edit or Write into a reviewable diff — the tool
// call's own input IS the change, no filesystem read needed.
func editDiff(name string, raw json.RawMessage) *Diff {
	if name != "Edit" && name != "Write" {
		return nil
	}
	var in struct {
		File    string `json:"file_path"`
		Old     string `json:"old_string"`
		New     string `json:"new_string"`
		Content string `json:"content"`
		All     bool   `json:"replace_all"`
	}
	if json.Unmarshal(raw, &in) != nil || in.File == "" {
		return nil
	}
	d := &Diff{File: in.File, Old: clip(in.Old, diffChars), All: in.All}
	if name == "Write" {
		d.New = clip(in.Content, diffChars)
	} else {
		d.New = clip(in.New, diffChars)
	}
	if d.Old == "" && d.New == "" {
		return nil
	}
	return d
}

// clip bounds multi-line text without flattening it — a diff keeps
// its line breaks or it is not a diff.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "\n…"
}

func thinkingText(raw json.RawMessage) []string {
	var blocks []struct {
		Type     string `json:"type"`
		Thinking string `json:"thinking"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "thinking" && strings.TrimSpace(b.Thinking) != "" {
			out = append(out, Snip(b.Thinking, 400))
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
