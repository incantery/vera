// The LLM judge: goal plus the whole conversation in, DONE-or-CONTINUE
// out — the same verdict shape vera's agent plugin judges by, so a
// drive means one thing everywhere it runs.
package drive

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// JudgeSysPrompt is the supervisor's whole brief. It is exported
// because the judge's VOCABULARY — what DONE, CONTINUE and ESCALATE
// mean, and the standing rule that a wrong answer typed into a repo is
// worse than a question asked — is the part that must not fork. rook's
// agent plugin judges on its own wire and its own spend meter, but by
// these words; a second copy would be two products quietly disagreeing
// about when to stop.
const JudgeSysPrompt = `You supervise an AI coding agent (the worker) for its owner, who set one goal and stepped away. You read the conversation so far — the messages sent on the owner's behalf and the worker's replies — and decide what happens next.
Reply with exactly this shape:
First line: DONE, CONTINUE, or ESCALATE.
The lines after:
- DONE: one sentence saying how the goal was met.
- CONTINUE: the exact next message to send the worker — direct and specific, at most 120 words, plain text. Answer routine questions yourself when the goal and the conversation already contain the answer: approve steps the goal explicitly asks for, pick the option that serves the goal, push past deflections ("I don't have access") toward best effort when that is what the goal wants.
- ESCALATE: the question for the owner, one or two sentences, quoting what the worker needs.
ESCALATE — never guess — when the worker needs: authorization beyond what the goal grants; anything destructive or hard to reverse (deleting, force-pushing, publishing, spending); credentials or secrets; or information only the owner has. A wrong answer typed into a repo is worse than a question asked.
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
	content, err := j.Complete(ctx, []chatMsg{
		{"system", JudgeSysPrompt},
		{"user", JudgePrompt(goal, history, j.maxReply())},
	})
	if err != nil {
		return Verdict{}, err
	}
	return ParseVerdict(content)
}

// JudgePrompt lays the goal and the whole conversation out for the
// supervisor. Exported alongside the brief for the same reason: the
// judge reads a FORMAT, and a caller who writes the transcript
// differently is asking a different question of the same words.
//
// maxReply caps each reply on the way in — a 300KB turn is not a
// judgment worth $0.10 of context. Zero means the default.
func JudgePrompt(goal string, history []Exchange, maxReply int) string {
	if maxReply <= 0 {
		maxReply = 12000
	}
	var b strings.Builder
	b.WriteString("The owner's goal:\n" + goal + "\n")
	for i, e := range history {
		reply := e.Reply
		if len(reply) > maxReply {
			reply = reply[:maxReply] + "\n[truncated]"
		}
		fmt.Fprintf(&b, "\nMessage %d sent to the worker:\n%s\n\nThe worker's reply %d:\n%s\n",
			i+1, e.Prompt, i+1, reply)
	}
	b.WriteString("\nDecide.")
	return b.String()
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
	case "ESCALATE":
		if rest == "" {
			return Verdict{}, errors.New("the judge escalated without saying what to ask")
		}
		return Verdict{Escalate: true, Reason: rest}, nil
	}
	return Verdict{}, errors.New("the judge broke shape: " + snip(content, 80))
}
