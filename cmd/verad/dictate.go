// Dictation: words meant for the cursor, not for Vera.
//
// The Mac app holds a key, the person talks, and what they said lands
// in whatever application has focus. Raw recognition is not what anyone
// wants typed — fillers, restarts, "no wait, I mean", no punctuation —
// so the text comes through here for one fast, narrow model pass first.
//
// Two rules keep this honest. It is a cleanup, not a rewrite: the model
// may remove and repair, never add or answer. And it is OPTIONAL: the
// app inserts the raw words if this is slow, down, or mindless, because
// dictation that depends on a language model being up is dictation you
// cannot rely on.
package main

import (
	"context"
	"strings"
	"time"

	"github.com/incantery/mote/provider"
)

// Dictation is one utterance headed for the cursor, with what is known
// about where the cursor is.
type Dictation struct {
	Text   string       `json:"text"`
	Device string       `json:"device,omitempty"`
	App    *ObservedApp `json:"app,omitempty"`
}

// Cleaned is what goes back. Raw says whether the model was skipped —
// the app shows it, so "it typed my ums" has a visible reason.
type Cleaned struct {
	Text string `json:"text"`
	Raw  bool   `json:"raw,omitempty"`
}

const dictationPrompt = `You clean up dictated speech so it can be typed where the person's cursor is.

Rules:
- Remove fillers (um, uh, like, you know), false starts and repeated words.
- Apply self-corrections: "send it Tuesday, no, Wednesday" becomes "send it Wednesday".
- Add punctuation and capitalisation. Keep the person's words, tone and order otherwise.
- Spoken punctuation and formatting words ("comma", "new line", "question mark") become the symbol.
- Never add content, never answer questions in the text, never address the person. Output ONLY the cleaned text, nothing else.
- Do not include internal or system XML tags in your response.
- If the text is already clean, return it unchanged.`

// cleanBudget is how long the cursor may wait. Past this the app types
// the raw words, and the model's answer is thrown away.
const cleanBudget = 2500 * time.Millisecond

// clean is the pass. It uses the same streamed call as an exchange, with
// no tools and no history: the cursor has no conversation.
func (m *Mind) clean(ctx context.Context, d Dictation) (Cleaned, error) {
	text := strings.TrimSpace(d.Text)
	if text == "" {
		return Cleaned{Text: "", Raw: true}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, cleanBudget)
	defer cancel()

	system := dictationPrompt
	if d.App != nil && d.App.Name != "" {
		system += "\n\nThe cursor is in " + d.App.Name + ". " + styleFor(d.App)
	}
	req := m.request(m.base(), system, []provider.Message{provider.User(text)}, nil)
	// The cursor is waiting. A model that stops to think has already
	// missed the budget and the raw words go in instead, so this is the
	// one call that asks for no reasoning at all — and asks for no
	// effort either, because a model that takes both can refuse the
	// pair (thinking off above high effort is an error on Claude 5).
	req.Thinking, req.Effort = provider.ThinkingOff, ""

	var out strings.Builder
	var used usage
	spent, err := m.Provider.Stream(ctx, req,
		func(ev provider.Event) {
			if ev.Kind == provider.KindDelta {
				out.WriteString(ev.Text)
			}
		})
	used.add(spent)
	spend(ctx, used.Prompt, used.Completion)
	if err != nil {
		return Cleaned{Text: text, Raw: true}, err
	}
	cleaned := strings.TrimSpace(out.String())
	if cleaned == "" {
		return Cleaned{Text: text, Raw: true}, nil
	}
	return Cleaned{Text: cleaned}, nil
}

// styleFor is the one place the focused application changes the
// cleanup. It is a hint about register, not a rewrite instruction.
func styleFor(app *ObservedApp) string {
	id := strings.ToLower(app.BundleID)
	switch {
	case strings.Contains(id, "ghostty"), strings.Contains(id, "terminal"), strings.Contains(id, "iterm"), strings.Contains(id, "rook"):
		return "That is a terminal: keep it literal, lowercase commands and paths exactly as spoken, no trailing period on a lone command."
	case strings.Contains(id, "slack"), strings.Contains(id, "messages"), strings.Contains(id, "discord"):
		return "That is a chat app: casual register, light punctuation."
	case strings.Contains(id, "mail"):
		return "That is email: complete sentences, proper punctuation."
	}
	return ""
}
