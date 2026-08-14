// The membrane's two directions, as verbs on the shared wire.
//
// Inbound — Digest: a finished reply compressed to a headline and
// bullets under Simplified-Technical-English discipline, because the
// core of an eight-paragraph answer is usually three bullets, and the
// human should read the three bullets first and the eight paragraphs
// by choice. Same shape rook's agent plugin has always written.
//
// Outbound — Expand: the human's rough words become the message they
// MEAN, phrased for the worker with specifics pulled from what the
// worker just said. The rough words and the sent words both stay on
// the record; a membrane that hid its rephrasing would be a
// ventriloquist, not an assistant.
package drive

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const digestSysPrompt = `You compress a verbose technical assistant reply into a digest a busy engineer reads in five seconds. Follow Simplified Technical English (ASD-STE100) discipline: active voice, one idea per sentence, simple words. Keep code identifiers, commands, paths, and numbers exactly as written.
Report; never command. Name the actor. Work the reply says the assistant did stays the assistant's ("verified X", "removed Y"). The assistant's own proposals stay offers ("offers to fix Z"), never orders to the reader. Write an imperative line ONLY where the reply itself asks the reader a question or gives the reader steps to run — put those lines first.
Write exactly this shape:
First line: the core outcome or answer. One sentence, at most 15 words. No preamble. It must be true on its own — when the cap forces a choice, drop detail, never bend a fact.
Then up to 5 lines, each "- " plus one fact, decision, or request taken from the reply. At most 20 words each.
Nothing else: no headings, no blank lines, no closing remark.`

const digestMaxChars = 16000

// Digest compresses one reply. A reply that breaks the asked shape is
// salvaged rather than retried — a long digest still beats no digest,
// and a retry is money spent on pedantry.
func (m *LLM) Digest(ctx context.Context, prompt, reply string) (headline string, bullets []string, err error) {
	if len(reply) > digestMaxChars {
		reply = reply[:digestMaxChars] + "\n[truncated]"
	}
	content, err := m.Complete(ctx, []chatMsg{
		{"system", digestSysPrompt},
		{"user", "The human asked:\n" + snip(prompt, 600) + "\n\nThe reply to compress:\n" + reply},
	})
	if err != nil {
		return "", nil, err
	}
	headline, bullets = salvageDigest(content)
	if headline == "" {
		return "", nil, errors.New("the model sent nothing usable")
	}
	return headline, bullets, nil
}

// salvageDigest takes what came: first non-bullet line as headline,
// everything bullet-shaped kept, bounded.
func salvageDigest(content string) (string, []string) {
	var headline string
	var bullets []string
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if cut, ok := strings.CutPrefix(line, "- "); ok {
			bullets = append(bullets, cut)
		} else if cut, ok := strings.CutPrefix(line, "• "); ok {
			bullets = append(bullets, cut)
		} else if headline == "" {
			headline = line
		}
	}
	if len(bullets) > 6 {
		bullets = bullets[:6]
	}
	return headline, bullets
}

const compileSysPrompt = `You compile a task intent from a busy engineer (the owner) into ONE drive goal for an AI coding agent (the worker). The goal is what a supervisor will judge every reply against, so it must carry concrete completion criteria. Keep the owner's decisions and scope EXACTLY; sharpen, never widen. Imperative voice, at most 60 words, plain text only — no headings, no bullets, no quotes.`

// CompileGoal turns an owner's intent into the goal a drive judges
// against. The intent and the goal both stay on the record — the
// task's card shows who wrote what.
func (m *LLM) CompileGoal(ctx context.Context, intent string) (string, error) {
	content, err := m.Complete(ctx, []chatMsg{
		{"system", compileSysPrompt},
		{"user", "The owner's intent:\n" + intent + "\n\nWrite the drive goal."},
	})
	if err != nil {
		return "", err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("the model sent nothing usable")
	}
	return content, nil
}

const expandSysPrompt = `You phrase messages from a busy engineer (the owner) to their AI coding agent (the worker). The owner speaks roughly — a fragment, a decision, an intent. You write the message they MEAN: keep their decisions and intent EXACTLY, expand and sharpen with specifics from the worker's last reply, answer questions the worker asked when the owner's words answer them. First person as the owner, direct, specific, at most 150 words, plain text only — no greeting, no signature, no markdown.
If the owner's words are already a complete, clear message, return them nearly unchanged. Never invent decisions the owner did not make.`

// Expand writes the message the human means from the words they typed,
// anchored in the worker's last turn so the specifics are real.
func (m *LLM) Expand(ctx context.Context, rough, lastPrompt, lastReply string) (string, error) {
	var b strings.Builder
	if lastPrompt != "" {
		b.WriteString("The owner had asked:\n" + snip(lastPrompt, 600) + "\n\n")
	}
	if lastReply != "" {
		reply := lastReply
		if len(reply) > digestMaxChars {
			reply = reply[:digestMaxChars] + "\n[truncated]"
		}
		b.WriteString("The worker's last reply:\n" + reply + "\n\n")
	}
	fmt.Fprintf(&b, "The owner now says, roughly:\n%s\n\nWrite the message they mean.", rough)
	content, err := m.Complete(ctx, []chatMsg{
		{"system", expandSysPrompt},
		{"user", b.String()},
	})
	if err != nil {
		return "", err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("the model sent nothing usable")
	}
	return content, nil
}
