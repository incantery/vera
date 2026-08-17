// The steward: the vera agent reading the whole board the way an
// engineering manager reads a wall of cards — not to do the work, but
// to say what the board itself cannot: this looks finished, this is
// the one to start next, this has been waiting long enough that
// someone should know. Moves are advice with an address; the server
// decides which survive its guards, and every irreversible transition
// still belongs to the owner.
package drive

import (
	"context"
	"errors"
	"strings"
)

// A StewardMove is one piece of addressed advice: a verb, the card it
// names, and the one-sentence why the owner will read.
type StewardMove struct {
	Verb string // "done" | "start" | "answer" | "note"
	Task string // the card id the move names
	Why  string // the sentence — or, for "answer", the drafted reply itself
}

const stewardSysPrompt = `You are the steward of a kanban board for a busy engineer (the owner). AI worker agents do the work; the owner makes every irreversible call. Read the board and propose at most 3 moves — or none: silence is the right answer for a board that is fine.
Reply with one move per line, each exactly one of these shapes:
DONE <card-id> — <one sentence: why this card's work appears finished, citing the evidence shown>
START <card-id> — <one sentence: why this card is the one to start next>
ANSWER <card-id> — <the exact reply to send the worker, in the owner's voice>
NOTE <card-id> — <one sentence the owner should see about this card>
Or the single line NOTHING when no move earns its place.
Rules: DONE only when the card's own record — its state, its last reply, its log — says the work landed. START only for an inbox card whose moment has plainly come — a deadline near, its ground free, the work it waited on finished. When the card already names its ground, START begins read-only analysis without waiting for the owner; otherwise it becomes a proposal the owner decides — so reserve START for work that genuinely deserves to move now. ANSWER only for a card shown "waiting on the owner", and only when the goal and the record already contain the answer — approve steps the goal plainly grants, pick the option that serves the goal, restate details the history shows. NEVER answer asks about authorization beyond the goal, anything destructive or irreversible, credentials or secrets, or facts only the owner knows — a NOTE may flag those instead. The reply is in the owner's voice: first person, direct, at most 60 words; the owner sees it verbatim before it is sent. NOTE only for something the board does not already say: a card aging in waiting, work that looks stuck or circling, two cards that look like the same work. Use only the card ids shown; never invent one. Nothing else: no headings, no commentary.`

// Steward reads the rendered board and returns the parsed moves. The
// caller guards and applies them; this is only the asking.
func (m *LLM) Steward(ctx context.Context, board string) ([]StewardMove, error) {
	content, err := m.Complete(ctx, []chatMsg{
		{"system", stewardSysPrompt},
		{"user", board + "\n\nPropose the moves, or NOTHING."},
	})
	if err != nil {
		return nil, err
	}
	return ParseStewardMoves(content)
}

// ParseStewardMoves salvages what came, suggest-style: well-shaped
// lines parse, the rest is ignored, at most three survive. NOTHING
// (or an answer with no usable line among only noise) is a broken
// shape only when it claimed to have moves — an empty answer with a
// clean NOTHING is the model doing its job.
func ParseStewardMoves(content string) ([]StewardMove, error) {
	var out []StewardMove
	sawNothing := false
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.EqualFold(strings.TrimRight(line, ".!"), "NOTHING") {
			sawNothing = true
			continue
		}
		verb, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		var v string
		switch strings.ToUpper(verb) {
		case "DONE":
			v = "done"
		case "START":
			v = "start"
		case "ANSWER":
			v = "answer"
		case "NOTE":
			v = "note"
		default:
			continue
		}
		id, why, _ := strings.Cut(rest, "—")
		id = strings.TrimSpace(id)
		if id == "" || strings.ContainsAny(id, " \t") {
			continue
		}
		out = append(out, StewardMove{Verb: v, Task: id, Why: strings.TrimSpace(why)})
		if len(out) == 3 {
			break
		}
	}
	if len(out) == 0 && !sawNothing {
		return nil, errors.New("the steward broke shape: " + snip(content, 80))
	}
	return out, nil
}
