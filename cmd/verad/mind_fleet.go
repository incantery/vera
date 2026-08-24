// The fleet, as the mind reaches it.
//
// delegate is for a minute of work the person waits on. The fleet is
// for work that outlives the conversation: a room is opened, an agent
// is briefed, and the person walks away. What the model gets back from
// starting one is not a result but a receipt — and what it gets from
// asking is a picture in the person's nouns: this task is waiting on
// you, that one finished, this one has said nothing for twenty minutes.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/incantery/vera/fleet"
)

// fleetTool is the one tool with five verbs. One tool rather than five
// keeps the model's choice binary — answer, or reach for the fleet —
// and the verb is a detail it fills in after.
func fleetTool() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "fleet",
			"description": "The way to get work done on code or in a repository: start, check on, answer, land " +
				"or stop tasks. Each task is a separate Claude Code agent working in its own copy of a " +
				"repository, in its own terminal pane, and it keeps going after this conversation ends. Use " +
				"`start` for ANY work in a repository — inspecting, changing, investigating — and for anything " +
				"that will take more than a couple of minutes; `delegate` is only for a quick lookup they wait " +
				"on. Use `list` whenever they ask how things are going. " +
				"Use `answer` to pass their reply to a task that is waiting on them. `land` merges a finished " +
				"task; `stop` abandons one — only when they clearly said so.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type": "string",
						"enum": []string{"list", "start", "answer", "land", "stop"},
					},
					"brief": map[string]any{
						"type": "string",
						"description": "start: what to accomplish, in plain prose, with everything the agent " +
							"needs to act alone. Say the goal, not the steps.",
					},
					"project": map[string]any{
						"type": "string",
						"description": "start: a path inside the repository. Leave empty for the one in front " +
							"of them right now.",
					},
					"kind": map[string]any{
						"type":        "string",
						"enum":        []string{"ship", "scout"},
						"description": "start: ship changes code; scout only investigates and reports.",
					},
					"task": map[string]any{
						"type":        "string",
						"description": "answer/land/stop: the task id from list.",
					},
					"text": map[string]any{
						"type":        "string",
						"description": "answer: what to tell the task, in the person's words.",
					},
				},
				"required": []string{"action"},
			},
		},
	}
}

type fleetArgs struct {
	Action  string `json:"action"`
	Brief   string `json:"brief"`
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Task    string `json:"task"`
	Text    string `json:"text"`
}

// invokeFleet runs one fleet call and returns what the model is told.
// Like invoke, errors are text: a room that could not be opened is
// something the model should explain, not a failed exchange.
func (m *Mind) invokeFleet(ctx context.Context, conversation, device string, x *exchange, call toolCall, reply func(Frame) error) string {
	var args fleetArgs
	if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil || args.Action == "" {
		return "The request was not readable."
	}
	started := time.Now()
	ctx, rec := m.beginTool(ctx, conversation, call, args.Action)
	result, err := m.fleetAction(ctx, device, x, args, reply)
	m.endTool(ctx, rec, delegated{Result: result}, time.Since(started), err)
	slog.Info("fleet tool",
		"gen_ai.conversation.id", conversation,
		"action", args.Action,
		"task", args.Task,
		"brief", trim(args.Brief, 200),
		"took_ms", time.Since(started).Milliseconds(),
		"error", errText(err),
	)
	if err != nil {
		return "That could not be done: " + err.Error()
	}
	return result
}

func (m *Mind) fleetAction(ctx context.Context, device string, x *exchange, args fleetArgs, reply func(Frame) error) (string, error) {
	switch args.Action {
	case "list":
		views, err := m.Fleet.Tasks(ctx)
		if err != nil {
			return "", err
		}
		return describeFleet(views, time.Now()), nil

	case "start":
		if strings.TrimSpace(args.Brief) == "" {
			return "", fmt.Errorf("a task needs a brief: say what should be accomplished")
		}
		project := args.Project
		if project == "" && m.Attention != nil {
			project = m.Attention.TerminalPath(device)
		}
		if project == "" {
			return "", fmt.Errorf("no repository was named and none is in front of them; ask which project")
		}
		x.sign(ctx)
		_ = reply(Frame{Status: "Opening a room for that…"})
		t, err := m.Fleet.Spawn(ctx, fleet.Request{Project: project, Kind: fleet.Kind(args.Kind), Brief: args.Brief})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Started task %s in %s on branch %s. It is working now and will keep going after this conversation; "+
			"they can ask how it is going later. Tell them it has started, in a sentence.", t.ID, shortPath(t.Project), t.Branch), nil

	case "answer":
		if args.Task == "" || strings.TrimSpace(args.Text) == "" {
			return "", fmt.Errorf("answer needs a task id and the text to send")
		}
		if err := m.Fleet.Answer(ctx, args.Task, args.Text); err != nil {
			return "", err
		}
		return "Sent. The task has their answer and is working again.", nil

	case "land":
		if args.Task == "" {
			return "", fmt.Errorf("land needs a task id")
		}
		x.sign(ctx)
		_ = reply(Frame{Status: "Landing it…"})
		if err := m.Fleet.Land(ctx, args.Task); err != nil {
			return "", err
		}
		return "Landed: the task's work is merged and its room is closed.", nil

	case "stop":
		if args.Task == "" {
			return "", fmt.Errorf("stop needs a task id")
		}
		// Never force from here. Unlanded work is discarded only by a
		// person at the machine, and the refusal says why.
		if err := m.Fleet.Teardown(ctx, args.Task, false); err != nil {
			return "", err
		}
		return "Stopped and cleaned up.", nil
	}
	return "", fmt.Errorf("unknown action %q", args.Action)
}

// describeFleet is the picture, open tasks first, in the person's
// nouns. Internal words — worktree, pane, incarnation — do not appear.
func describeFleet(views []fleet.View, now time.Time) string {
	if len(views) == 0 {
		return "No tasks have been started."
	}
	var b strings.Builder
	shown := 0
	for _, v := range views {
		if v.Closed {
			continue
		}
		shown++
		fmt.Fprintf(&b, "Task %s (%s, %s): %s — %s.", v.ID, shortPath(v.Project), v.Branch, trim(firstSentence(v.Brief), 120), fleetPhrase(v, now))
		if len(v.Unread) > 0 {
			b.WriteString(" Since they last looked:")
			for _, s := range v.Unread {
				fmt.Fprintf(&b, " [%s] %s", s.Verb, trim(strings.TrimSpace(s.Text), 160))
			}
		}
		b.WriteString("\n")
	}
	closed := len(views) - shown
	if shown == 0 {
		return fmt.Sprintf("Nothing is running. %d earlier task(s) are finished.", closed)
	}
	if closed > 0 {
		fmt.Fprintf(&b, "%d earlier task(s) are finished and closed.\n", closed)
	}
	return strings.TrimRight(b.String(), "\n")
}

// fleetPhrase says what is believed, and for how long, in words a
// person would use.
func fleetPhrase(v fleet.View, now time.Time) string {
	age := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return " for " + roughDuration(now.Sub(t))
	}
	var lastAt time.Time
	if v.Last != nil {
		lastAt = v.Last.At
	}
	switch v.State {
	case fleet.Running:
		return "working"
	case fleet.Quiet:
		return "working, quiet for a bit"
	case fleet.Stale:
		return "has gone quiet" + age(lastAt) + " — worth a look"
	case fleet.Waiting:
		return "WAITING ON THEM" + age(v.TurnEnded) + "; it asked something and needs an answer"
	case fleet.Decision:
		return "BLOCKED on a decision from them"
	case fleet.Held:
		return "paused, waiting on something external"
	case fleet.Finished:
		return "FINISHED — ready to land"
	case fleet.Broken:
		return "FAILED; it gave up"
	case fleet.Gone:
		return "its terminal is gone; it may have been closed"
	default:
		return string(v.State)
	}
}

func roughDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i]
	}
	return s
}
