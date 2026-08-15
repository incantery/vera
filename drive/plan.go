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
"KIND: " + one of: repo (the work clearly continues one of the offered workspaces) | new (it deserves a fresh workspace) | ask (the plan cannot be shaped without one missing fact) | none (no directory of files could hold this work).
If KIND is repo: "WHERE: " + the offered workspace path, copied verbatim from the list. Never name a path not offered. An offered line may carry " # name: note" after the path — the owner's own description of that ground; trust it when matching work to a workspace, and never copy the annotation into WHERE.
If KIND is ask: "ASK: " + ONE question whose answer would let you plan — use it when the ask points at something real that is not offered (a site, an app, a repo you cannot see) or hinges on a fact you cannot default. Never ask when a defensible default exists, and never ask about anything the owner already stated — a named place, date, or preference in the ask is a fact, not a question. Never invent a workspace to stand in for ground you cannot see.
If KIND is new: "HOME: " + code or life, then "NAME: " + a short kebab-case directory name for it. Building any tool, script, or automation — or anything that reads machine data — is code, even when the subject is personal; life is for plans, research, and documents a person reads.
"CADENCE: " + once (a task that ends) or standing (an ongoing need the owner will keep returning to — routines, habits, and practices like meal prep, tracking, or learning are standing even when phrased as one ask).
If the ask names a date or deadline: "DEADLINE: " + that date as YYYY-MM-DD, computed from today's date.
"GOAL: " + the instruction to hand the worker: one or two sentences, imperative, concrete, self-contained. For a standing need, the goal is the FIRST pass only.
If the work is honestly two or more distinct pieces that cannot ride one goal: GOAL carries the first piece, then one "STEP: " line per later piece (at most three), each a self-contained instruction in order. Most asks are one piece — never pad.
"WHY: " + one short sentence the owner reads to judge your plan. If KIND is none, WHY says what this ask actually needs instead.
Prefer new over repo unless the ask plainly continues that workspace's own work. Never invent facts the ask does not carry.`

// Plan is vera's answer to one ask: the shape of the work before any
// of it exists.
type Plan struct {
	Kind     string `json:"kind"`               // repo | new | ask | none
	Where    string `json:"where,omitempty"`    // kind repo: the offered path
	Home     string `json:"home,omitempty"`     // kind new: code | life
	Name     string `json:"name,omitempty"`     // kind new: workspace dir name
	Cadence  string `json:"cadence"`            // once | standing
	Deadline string `json:"deadline,omitempty"` // YYYY-MM-DD, only if spoken
	Goal     string `json:"goal,omitempty"`
	Why      string `json:"why,omitempty"`
	Question string `json:"question,omitempty"` // kind ask: the one missing fact
	// Steps are the later pieces when the work is honestly more than
	// one: Goal is the first piece, each Step a card of its own.
	Steps []string `json:"steps,omitempty"`
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
	switch {
	case p.Kind == "",
		p.Kind == "ask" && p.Question == "",
		p.Kind != "none" && p.Kind != "ask" && p.Goal == "":
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
			case "repo", "new", "ask", "none":
				p.Kind = v
			}
		case "ASK":
			p.Question = rest
		case "STEP":
			if len(p.Steps) < 3 && rest != "" {
				p.Steps = append(p.Steps, rest)
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
