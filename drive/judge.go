// The LLM judge: goal plus the whole conversation in, DONE-or-CONTINUE
// out — the same verdict shape rook's agent plugin judges by, so a
// drive means one thing everywhere it runs.
package drive

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const judgeSysPrompt = `You supervise an AI coding agent (the worker) for its owner, who set one goal and stepped away. You read the conversation so far — the messages sent on the owner's behalf and the worker's replies — and decide whether the goal is met.
Reply with exactly this shape:
First line: DONE or CONTINUE.
The lines after: if DONE, one sentence saying how the goal was met. If CONTINUE, the exact next message to send the worker — direct and specific, at most 120 words, plain text. When the worker deflects ("I don't have access", "I cannot know that"), push for its best effort or best hypothesis when that is what the goal asks for.
Nothing else: no headings, no quotes around the message.`

// LLMJudge is the drive's supervisor on the shared wire. Replies are
// capped on the way in — a 300KB turn is not a judgment worth $0.10 of
// context.
type LLMJudge struct {
	*LLM
	MaxReply int // per-reply input cap in bytes; default 12000
}

func (j *LLMJudge) maxReply() int {
	if j.MaxReply > 0 {
		return j.MaxReply
	}
	return 12000
}

func (j *LLMJudge) Judge(ctx context.Context, goal string, history []Exchange) (Verdict, error) {
	var b strings.Builder
	b.WriteString("The owner's goal:\n" + goal + "\n")
	for i, e := range history {
		reply := e.Reply
		if len(reply) > j.maxReply() {
			reply = reply[:j.maxReply()] + "\n[truncated]"
		}
		fmt.Fprintf(&b, "\nMessage %d sent to the worker:\n%s\n\nThe worker's reply %d:\n%s\n",
			i+1, e.Prompt, i+1, reply)
	}
	b.WriteString("\nDecide.")
	content, err := j.Complete(ctx, []chatMsg{
		{"system", judgeSysPrompt},
		{"user", b.String()},
	})
	if err != nil {
		return Verdict{}, err
	}
	return ParseVerdict(content)
}

// ParseVerdict holds the judge to the shape it was asked for. A broken
// shape is an error, not a guess: a misread verdict sends words into
// somebody's conversation.
func ParseVerdict(content string) (Verdict, error) {
	head, rest, _ := strings.Cut(strings.TrimSpace(content), "\n")
	rest = strings.TrimSpace(rest)
	switch strings.ToUpper(strings.TrimRight(strings.TrimSpace(head), ".:!")) {
	case "DONE":
		return Verdict{Done: true, Reason: rest}, nil
	case "CONTINUE":
		if rest == "" {
			return Verdict{}, errors.New("the judge said CONTINUE and nothing else")
		}
		return Verdict{Prompt: rest}, nil
	}
	return Verdict{}, errors.New("the judge broke shape: " + snip(content, 80))
}
