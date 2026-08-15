// Plan: the fifth part vera plays on the shared wire. The owner says
// what they need in plain words; vera answers with the operational
// shape — where the work should live (an offered workspace, a fresh
// one, or nowhere a directory of files can reach), whether the need
// ends or stands, and the exact goal a worker would be handed. The
// plan is a bid, not an act: the owner nods before anything is made.
package drive

import (
	"context"
	"errors"
	"strings"
)

const planSysPrompt = `You are vera, an owner's agent-runner. The owner says what they need in plain words; you decide the operational plan: where the work should live and what goal to hand a worker agent. A worker is an AI coding agent operating on ONE directory — but a workspace is not only for code: plans, research, lists, and documents are files too.
Answer in exactly these labeled lines, nothing else — no headings, no blank lines, no commentary:
"KIND: " + one of: repo (the work clearly continues one of the offered workspaces) | new (it deserves a fresh workspace) | none (no directory of files could hold this work).
If KIND is repo: "WHERE: " + the offered workspace path, copied verbatim from the list. Never name a path not offered.
If KIND is new: "HOME: " + code (a software project) or life (anything else), then "NAME: " + a short kebab-case directory name for it.
"CADENCE: " + once (a task that ends) or standing (an ongoing need the owner will keep returning to).
If the ask names a date or deadline: "DEADLINE: " + that date as YYYY-MM-DD, computed from today's date.
"GOAL: " + the instruction to hand the worker: one or two sentences, imperative, concrete, self-contained. For a standing need, the goal is the FIRST pass only.
"WHY: " + one short sentence the owner reads to judge your plan. If KIND is none, WHY says what this ask actually needs instead.
Prefer new over repo unless the ask plainly continues that workspace's own work. Never invent facts the ask does not carry.`

// Plan is vera's answer to one ask: the shape of the work before any
// of it exists.
type Plan struct {
	Kind     string `json:"kind"`               // repo | new | none
	Where    string `json:"where,omitempty"`    // kind repo: the offered path
	Home     string `json:"home,omitempty"`     // kind new: code | life
	Name     string `json:"name,omitempty"`     // kind new: workspace dir name
	Cadence  string `json:"cadence"`            // once | standing
	Deadline string `json:"deadline,omitempty"` // YYYY-MM-DD, only if spoken
	Goal     string `json:"goal,omitempty"`
	Why      string `json:"why,omitempty"`
}

// Plan reads one ask against the offered workspaces and returns the
// bid. Shape violations are salvaged, never retried — the same
// discipline as Digest and Suggest.
func (m *LLM) Plan(ctx context.Context, ask string, repos []string, today string) (Plan, error) {
	var b strings.Builder
	b.WriteString("Today is " + today + ".\n\nOffered workspaces:\n")
	if len(repos) == 0 {
		b.WriteString("(none)\n")
	}
	for _, r := range repos {
		b.WriteString("- " + r + "\n")
	}
	b.WriteString("\nThe owner says:\n" + snip(ask, 600) + "\n\nWrite the plan lines.")
	content, err := m.Complete(ctx, []chatMsg{
		{"system", planSysPrompt},
		{"user", b.String()},
	})
	if err != nil {
		return Plan{}, err
	}
	p := salvagePlan(content)
	if p.Kind == "" || (p.Kind != "none" && p.Goal == "") {
		return Plan{}, errors.New("the model sent nothing usable")
	}
	return p, nil
}

// salvagePlan takes what came: labeled lines by prefix, normalized
// where the vocabulary is closed, everything else as spoken.
func salvagePlan(content string) Plan {
	var p Plan
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		label, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		switch strings.ToUpper(strings.TrimSpace(label)) {
		case "KIND":
			switch v := strings.ToLower(rest); v {
			case "repo", "new", "none":
				p.Kind = v
			}
		case "WHERE":
			p.Where = rest
		case "HOME":
			switch v := strings.ToLower(rest); v {
			case "code", "life":
				p.Home = v
			}
		case "NAME":
			p.Name = strings.ToLower(snip(rest, 60))
		case "CADENCE":
			switch v := strings.ToLower(rest); v {
			case "once", "standing":
				p.Cadence = v
			}
		case "DEADLINE":
			p.Deadline = snip(rest, 20)
		case "GOAL":
			p.Goal = rest
		case "WHY":
			p.Why = rest
		}
	}
	if p.Cadence == "" {
		p.Cadence = "once"
	}
	return p
}
