// Chat: the vera agent in conversation with the owner — summoned in a
// small terminal popup over their work (rook's companion slot), so
// answers are brief and situated. The one verb chat may exercise is
// the steward's ANSWER: relaying the owner's reply to a waiting card,
// because the owner saying "tell T-3 to retry" IS the disposal the
// board's rules require. Everything else it does with words.
package drive

import (
	"context"
	"regexp"
	"strings"
)

// A ChatTurn is one exchange line in the standing thread.
type ChatTurn struct {
	Role string // "owner" | "vera"
	Text string
}

const chatSysPrompt = `You are vera: the owner's engineering aide, keeper of their kanban board of AI worker agents. You are chatting with the owner in a small terminal popup summoned over their work — be brief and direct, a sentence or three unless they ask for depth. Plain text, no markdown.
Below you see the board as it stands, and where the owner is standing (their tmux session and directory) when known. Answer about the board, the work, and what needs them, from what is shown — never invent cards or facts.
When the owner tells you to send a reply to a waiting card, say what you are doing in one short line and end your message with a line exactly:
ANSWER <card-id> — <the reply, in the owner's voice, first person, at most 60 words>
Only for a card shown waiting with an ask, and only relaying what the owner just decided. Never ANSWER for authorization beyond a card's goal, anything destructive or irreversible, credentials or secrets, or facts the owner has not given — explain instead. Otherwise never emit an ANSWER line.`

var answerLineRe = regexp.MustCompile(`^ANSWER\s+(\S+)\s+[—-]+\s+(.+)$`)

// SplitChatReply separates the visible reply from ANSWER directives.
// Chat prose parses leniently: only exactly-shaped lines are moves,
// everything else is words for the owner.
func SplitChatReply(content string) (prose string, moves []StewardMove) {
	var kept []string
	for line := range strings.SplitSeq(content, "\n") {
		if m := answerLineRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			moves = append(moves, StewardMove{Verb: "answer", Task: m[1], Why: strings.TrimSpace(m[2])})
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), moves
}

// Chat is one turn: the standing thread, the board, the owner's place,
// their message — vera's words back, plus any ANSWER the words carry.
func (m *LLM) Chat(ctx context.Context, board, place string, thread []ChatTurn, user string) (string, []StewardMove, error) {
	sys := chatSysPrompt + "\n\nThe board right now:\n" + board
	if place != "" {
		sys += "\n\nWhere the owner is: " + place
	}
	msgs := []chatMsg{{"system", sys}}
	for _, t := range thread {
		role := "user"
		if t.Role == "vera" {
			role = "assistant"
		}
		msgs = append(msgs, chatMsg{role, t.Text})
	}
	msgs = append(msgs, chatMsg{"user", user})
	content, err := m.Complete(ctx, msgs)
	if err != nil {
		return "", nil, err
	}
	prose, moves := SplitChatReply(content)
	return prose, moves, nil
}
