// Suggest: the co-pilot's opening bid. The worker just finished a
// turn and the owner must answer; the vera agent reads the exchange
// and offers the owner a digest of where things stand plus one to
// three replies they could send, best first. The owner clicking one
// instead of typing their own is the seed of a feedback loop — how
// often vera's bid is good enough to send is the measure of how much
// vera can drive.
package drive

import (
	"context"
	"errors"
	"strings"
)

const suggestSysPrompt = `You are a busy engineer's co-pilot, watching their AI coding agent (the worker) work. The worker just finished a turn; the engineer (the owner) must answer it. Read the exchange and write, in the owner's interest:
Line 1: "HAPPENED: " + what the worker just did, at most 25 words.
Line 2: "NOW: " + where the work stands and what the worker is waiting on, at most 20 words.
Then 1 to 3 numbered lines ("1. ", "2. ", "3. "), each ONE complete reply the owner could send, best first. Each reply is in the owner's own voice: first person, direct, plain text, at most 30 words. Ground every reply in the exchange — approve and name the next step, answer the worker's question, redirect, or ask for proof. Never invent facts or decisions the exchange does not support; if the worker asked something the exchange cannot answer, make the reply ask for what is missing instead of guessing.
Nothing else: no headings, no blank lines, no commentary.`

// Suggest reads one finished exchange and returns the digest and the
// ranked replies. A reply that breaks the asked shape is salvaged
// rather than retried — same discipline as Digest.
func (m *LLM) Suggest(ctx context.Context, task, prompt, reply string) (happened, now string, replies []string, err error) {
	if len(reply) > digestMaxChars {
		reply = reply[:digestMaxChars] + "\n[truncated]"
	}
	var b strings.Builder
	if task != "" {
		b.WriteString("The worker's task:\n" + snip(task, 200) + "\n\n")
	}
	if prompt != "" {
		b.WriteString("The owner had asked:\n" + snip(prompt, 600) + "\n\n")
	}
	b.WriteString("The worker's reply:\n" + reply + "\n\nWrite the two lines and the suggested replies.")
	content, err := m.Complete(ctx, []chatMsg{
		{"system", suggestSysPrompt},
		{"user", b.String()},
	})
	if err != nil {
		return "", "", nil, err
	}
	happened, now, replies = salvageSuggest(content)
	if len(replies) == 0 {
		return "", "", nil, errors.New("the model sent nothing usable")
	}
	return happened, now, replies, nil
}

// salvageSuggest takes what came: the two labeled lines by prefix,
// anything numbered or bulleted as a reply, bounded at three.
func salvageSuggest(content string) (happened, now string, replies []string) {
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if cut, ok := strings.CutPrefix(line, "HAPPENED:"); ok {
			happened = strings.TrimSpace(cut)
			continue
		}
		if cut, ok := strings.CutPrefix(line, "NOW:"); ok {
			now = strings.TrimSpace(cut)
			continue
		}
		if len(replies) >= 3 {
			continue
		}
		if len(line) > 2 && line[0] >= '1' && line[0] <= '9' && (line[1] == '.' || line[1] == ')') {
			replies = append(replies, strings.TrimSpace(line[2:]))
		} else if cut, ok := strings.CutPrefix(line, "- "); ok {
			replies = append(replies, cut)
		}
	}
	return happened, now, replies
}
