package dump

import (
	"fmt"
	"strings"
	"time"
)

// Transcripts and the README: what a person opens first. Everything
// here is derived from files also in the dump verbatim, so a reader
// who wants the raw thing has it beside the readable one.

func renderConversation(c conversation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Conversation %s\n\n", c.id)
	first, last := c.entries[0], c.entries[len(c.entries)-1]
	fmt.Fprintf(&b, "%d exchanges · %s → %s · model %s", len(c.entries),
		first.At.Local().Format("2006-01-02 15:04:05"), last.At.Local().Format("15:04:05"), last.Model)
	if first.Device != "" {
		fmt.Fprintf(&b, " · from %s", first.Device)
	}
	b.WriteString("\n\nThe system prompt of the last exchange is beside this file as `" + c.id + ".system.md`; every exchange's is in the `.jsonl`.\n")
	for i, e := range c.entries {
		fmt.Fprintf(&b, "\n---\n\n## %d · %s", i+1, e.At.Local().Format("15:04:05"))
		fmt.Fprintf(&b, "  ·  %s · %d→%d tokens", (time.Duration(e.TookMs) * time.Millisecond).Round(100*time.Millisecond), e.InputTokens, e.OutputTokens)
		if e.FirstSignMs > 0 {
			fmt.Fprintf(&b, " · first sign %.1fs", float64(e.FirstSignMs)/1000)
		}
		if e.TraceID != "" {
			fmt.Fprintf(&b, " · trace `%s`", e.TraceID)
		}
		b.WriteString("\n\n**you:** " + e.Said + "\n")
		for _, r := range e.Rounds {
			fmt.Fprintf(&b, "\n> **%s** `%s`", r.Tool, oneLine(string(r.Args), 300))
			if r.Task != "" {
				fmt.Fprintf(&b, " → task `%s`", r.Task)
			}
			if r.Session != "" {
				fmt.Fprintf(&b, " → session `%s`", r.Session)
			}
			if r.CostUSD > 0 {
				fmt.Fprintf(&b, " · $%.3f", r.CostUSD)
			}
			fmt.Fprintf(&b, " · %s\n>\n> %s\n", (time.Duration(r.TookMs) * time.Millisecond).Round(100*time.Millisecond), quote(trimText(r.Result, 2000)))
		}
		if e.Answered != "" {
			b.WriteString("\n**vera:** " + e.Answered + "\n")
		}
		if e.Error != "" {
			b.WriteString("\n**error:** " + e.Error + "\n")
		}
	}
	return b.String()
}

func renderCosts(c *collected, res Result) string {
	var b strings.Builder
	b.WriteString("# What it cost\n\n")
	b.WriteString("Claude Code figures are what the tokens would cost at API list prices (see dump/session.go for the table); on a subscription they are not a bill, but they still say which task was the expensive one. Vera's own model is shown in tokens.\n\n")
	var in, out int
	for _, conv := range c.conversations {
		for _, e := range conv.entries {
			in += e.InputTokens
			out += e.OutputTokens
		}
	}
	fmt.Fprintf(&b, "## Vera\n\n%d exchanges · %d input tokens · %d output tokens\n\n", countEntries(c), in, out)
	if len(c.tasks) > 0 {
		b.WriteString("## Fleet\n\n")
		for _, tb := range c.tasks {
			var usd float64
			priced := true
			for _, s := range tb.sessions {
				u, p := s.CostAll()
				usd += u
				priced = priced && p
			}
			fmt.Fprintf(&b, "- `%s` %s in %s — %d session(s)", tb.task.ID, tb.task.Kind, shortPath(tb.task.Project), len(tb.sessions))
			if len(tb.sessions) > 0 {
				fmt.Fprintf(&b, " · %s", dollars(usd, priced))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(c.delegated) > 0 {
		b.WriteString("## Delegations\n\n")
		for _, s := range c.delegated {
			usd, priced := s.CostAll()
			fmt.Fprintf(&b, "- `%s` · %d turns · %s\n", s.ID, s.Turns, dollars(usd, priced))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Total\n\nClaude Code sessions: %d · %s\n", res.Sessions, dollars(res.CostUSD, res.Priced))
	return b.String()
}

func renderReadme(o Options, c *collected, res Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Vera dump · %s\n\n", o.Now.Format("2006-01-02 15:04"))
	if o.Note != "" {
		fmt.Fprintf(&b, "> %s\n\n", o.Note)
	}
	if !c.from.IsZero() {
		fmt.Fprintf(&b, "Covers %s → %s.\n\n", c.from.Local().Format("2006-01-02 15:04:05"), c.to.Local().Format("2006-01-02 15:04:05"))
	}
	fmt.Fprintf(&b, "Secrets from ~/.config/vera and the pairing identity are replaced with %s throughout.\n\n", redacted)

	b.WriteString("## Conversations\n\n")
	if len(c.conversations) == 0 {
		b.WriteString("none\n")
	}
	for _, conv := range c.conversations {
		last := conv.entries[len(conv.entries)-1]
		fmt.Fprintf(&b, "- `%s` — %d exchanges, last at %s: “%s” → `conversations/%s.md`\n",
			conv.id, len(conv.entries), last.At.Local().Format("15:04:05"), oneLine(last.Said, 80), conv.id)
	}

	b.WriteString("\n## Fleet tasks\n\n")
	if len(c.tasks) == 0 {
		b.WriteString("none\n")
	}
	for _, tb := range c.tasks {
		t := tb.task
		fmt.Fprintf(&b, "- `%s` %s in %s (%s) — spawned %s", t.ID, t.Kind, shortPath(t.Project), t.Name, t.Spawned.Local().Format("01-02 15:04"))
		if t.Closed {
			b.WriteString(", closed")
		}
		if n := len(tb.statuses); n > 0 {
			s := tb.statuses[n-1]
			fmt.Fprintf(&b, "; last status **%s** %s", s.Verb, oneLine(s.Text, 100))
		}
		fmt.Fprintf(&b, " → `fleet/%s/`\n", t.ID)
	}

	if len(c.delegated) > 0 {
		b.WriteString("\n## Delegations\n\n")
		for _, s := range c.delegated {
			fmt.Fprintf(&b, "- `%s` → `delegate/%s.md`\n", s.ID, s.ID)
		}
	}

	if res.Memories > 0 {
		fmt.Fprintf(&b, "\n## What she knew\n\n%d memory %s, and the index that went into every prompt → `home/MEMORY.md`, `home/memory/`. What she knows about each repository → `home/projects/`.\n",
			res.Memories, plural(res.Memories, "file", "files"))
	}

	fmt.Fprintf(&b, "\n## Cost\n\n%d Claude Code session(s), %s — details in `costs.md`.\n", res.Sessions, dollars(res.CostUSD, res.Priced))

	b.WriteString(`
## Layout

- ` + "`conversations/<id>.md`" + ` — the transcript with every tool round; ` + "`.jsonl`" + ` is the journal verbatim (system prompt per exchange, tokens, timings, trace ids); ` + "`.system.md`" + ` the last system prompt
- ` + "`fleet/<task>/`" + ` — task.json, brief.md, status.log (the agent's own status verbs), report.md, claude.json (harness settings), run (the launch), env.keys, and ` + "`sessions/*.jsonl`" + `: the agent's Claude Code sessions verbatim, summarized in sessions.md
- ` + "`delegate/`" + ` — Claude Code sessions of quick delegations
- ` + "`home/`" + ` — Vera's home as it stands now: ` + "`MEMORY.md`" + ` (the index, verbatim as the prompt carried it), ` + "`memory/<slug>.md`" + ` one fact per file, ` + "`projects/<name>.md`" + ` what she knows about each repository. Her ` + "`notes/`" + ` are not included
- ` + "`verad/`" + ` — the runfile, known projects, and the log for this window (±10 min)
- ` + "`claude/settings.json`" + ` — the person's global Claude Code settings
- ` + "`config.keys`, `versions.txt`, `costs.md`" + `

Trace ids in the transcript match the spans in Grafana Tempo when telemetry was on; fleet sessions carry the ` + "`vera.task`" + ` attribute.
`)
	return b.String()
}

func countEntries(c *collected) int {
	n := 0
	for _, conv := range c.conversations {
		n += len(conv.entries)
	}
	return n
}

func dollars(usd float64, priced bool) string {
	if !priced {
		if usd == 0 {
			return "no price known for the model(s)"
		}
		return fmt.Sprintf("≈ $%.2f (some models unpriced)", usd)
	}
	return fmt.Sprintf("≈ $%.2f", usd)
}

func quote(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n> ")
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	return trimText(s, n)
}

func trimText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func shortPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 && i < len(p)-1 {
		return p[i+1:]
	}
	return p
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
