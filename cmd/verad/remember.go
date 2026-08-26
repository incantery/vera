// Deciding what was worth keeping.
//
// One extra model call per exchange, made AFTER the reply has gone, so
// none of it lands on the latency the person actually feels. It reads
// the exchange plus what is already known, and answers with a revision
// rather than a pile of new facts — which is what lets "I moved to
// Austin" correct "lives in Denver" instead of contradicting it.
//
// Since memory became a directory the revision speaks in slugs rather
// than numbers: `lives-in-austin` written over `lives-in-denver` is a
// file rewritten, and the model naming the file it means is the same
// act as a person renaming one. Numbers were an id scheme nobody could
// read; a slug is the fact, short.
//
// Most exchanges should produce nothing. That is stated hard in the
// prompt and it is the single thing most worth watching: a memory that
// grows on every turn is a memory that has learned nothing in
// particular, and it makes every later prompt longer, slower and more
// expensive for no gain.
//
// This is a bridge. Vera writes her own memory with tools at mote
// milestone 5, and then extraction is a thing she does deliberately
// rather than a thing that happens to her.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/incantery/vera/home"
)

const remembering = `You maintain long-term memory about one person, from their conversations with an assistant.

The memory is a directory of files, one fact per file, named by a short slug. You are given the index — the slug and the fact — then one exchange. Decide what, if anything, should change.

Keep ONLY things likely to still be true weeks from now:
  - stable facts about them (where they live, what they do, who is in their life) — type "user"
  - lasting preferences, constraints, dislikes, and how they want to be worked with — type "feedback"
  - ongoing work, plans and commitments with a horizon — type "project"
  - where something of theirs lives: a repository, a dashboard, a document — type "reference"

Never keep:
  - what they asked, or anything about this exchange as an event
  - anything the assistant said, unless the person confirmed it about themselves
  - passing states ("tired today"), one-off facts they looked up, or general knowledge
  - anything already known, in any wording

Correct rather than accumulate: if something contradicts or refines a known fact, UPDATE that fact by its slug. Give the update the slug it should have afterwards only if the fact itself changed subject; otherwise keep the existing slug.

Write each fact as a short, self-contained third-person statement that will still make sense with no other context. "Lives in Vienna", not "moved there last year".

Resolve every relative date against today's date, given below, and write the absolute one. "Starts at Grafana on 2 September 2026", never "starts in two weeks" — a fact that expires quietly is worse than one you never kept.

Slugs are lowercase words joined by hyphens, and say what the fact is about: "lives-in-vienna", "prefers-short-answers", "rebuilding-vera-on-mote".

Answer with JSON and nothing else:
{"add": [{"name": "lives-in-vienna", "type": "user", "fact": "Lives in Vienna."}],
 "update": [{"name": "lives-in-denver", "type": "user", "fact": "Lives in Austin."}],
 "remove": ["owns-a-dog"]}

Most exchanges change nothing. For those, answer exactly: {}`

// remember runs the extraction. It is called on its own goroutine and
// deliberately returns nothing: a failure to learn is not a failure of
// the exchange, and must never surface to the person.
func (m *Mind) remember(conversation, said, answered string) {
	if m.Memory == nil {
		return
	}

	// Its own context. The request context belongs to a reply that has
	// already been sent, and is about to be cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	known := m.Memory.Recite()
	if known == "" {
		known = "(nothing yet)"
	}
	exchange := "Today is " + time.Now().Format("Monday, 2 January 2006") +
		".\n\nKnown:\n" + known + "\nExchange:\nThem: " + said + "\nAssistant: " + answered

	started := time.Now()
	var out strings.Builder
	var used usage
	_, err := m.stream(ctx, []chatMessage{
		{Role: "system", Content: remembering},
		{Role: "user", Content: exchange},
	}, nil, func(delta string) error {
		out.WriteString(delta)
		return nil
	}, &used)

	if err != nil {
		slog.Warn("remembering failed", "error", err.Error(), "conversation", conversation)
		return
	}

	revision, ok := parseRevision(out.String())
	if !ok {
		slog.Warn("remembering returned something unreadable",
			"conversation", conversation, "reply", trim(out.String(), 200))
		return
	}

	before := m.Memory.Count()
	if !revision.Empty() {
		if err := m.Memory.Apply(revision, conversation); err != nil {
			slog.Warn("could not write what was remembered", "error", err.Error(), "conversation", conversation)
		}
	}

	// Extraction is a real cost on every exchange, so it is metered
	// the same way the reply is — under its own operation name, so it
	// can be told apart in Grafana rather than quietly inflating the
	// cost of chatting.
	m.recordSecondary(ctx, "remember", used, time.Since(started))

	added, updated, removed := revision.Counts()
	slog.Info("remembered",
		"gen_ai.conversation.id", conversation,
		"added", added, "updated", updated, "removed", removed,
		"facts_before", before, "facts_after", m.Memory.Count(),
		"gen_ai.usage.input_tokens", used.Prompt,
		"gen_ai.usage.output_tokens", used.Completion,
		"took_ms", time.Since(started).Milliseconds(),
	)
	spend(ctx, used.Prompt, used.Completion)
}

// parseRevision is forgiving about the wrapping and strict about the
// shape. Models fence JSON in markdown often enough that refusing it
// would throw away good answers — and they write "replace" for
// "update" often enough that refusing that would too.
func parseRevision(raw string) (home.Revision, bool) {
	s := strings.TrimSpace(raw)
	if fence := strings.Index(s, "```"); fence >= 0 {
		s = s[fence+3:]
		if nl := strings.IndexByte(s, '\n'); nl >= 0 && !strings.HasPrefix(strings.TrimSpace(s[:nl]), "{") {
			s = s[nl+1:]
		}
		if end := strings.Index(s, "```"); end >= 0 {
			s = s[:end]
		}
	}
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return home.Revision{}, false
	}
	var wire struct {
		home.Revision
		Replace []home.Note `json:"replace"`
	}
	if json.Unmarshal([]byte(s[start:end+1]), &wire) != nil {
		return home.Revision{}, false
	}
	r := wire.Revision
	r.Update = append(r.Update, wire.Replace...)
	return r, true
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
