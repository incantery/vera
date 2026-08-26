// The fleet, as the mind reaches it.
//
// delegate is for a minute of work the person waits on. The fleet is
// for work that outlives the conversation: a room is opened, an agent
// is briefed, and the person walks away. What the model gets back from
// starting one is not a result but a receipt — and what it gets from
// asking is a picture in the person's nouns: this task is waiting on
// you, that one finished, this one has said nothing for twenty minutes.
//
// It is one of mote's tools, in the same registry as read and write
// and decided by the same policy, so that there is one path from a
// call to a result and one place a person can say what is allowed.
//
// What it needs beyond the model's arguments — which device asked,
// which repository is in front of them, somewhere to put a line while
// a room opens — arrives on the Handle the harness lends it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/incantery/mote/tool"
	"github.com/incantery/vera/fleet"
)

// FleetTool is the one tool with six verbs. One tool rather than six
// keeps the model's choice binary — answer, or reach for the fleet —
// and the verb is a detail it fills in after.
type FleetTool struct {
	// Fleet is the rooms themselves. Nil is not registered: a Vera
	// with no multiplexer has no fleet to offer.
	Fleet *fleet.Fleet
}

func (t *FleetTool) Name() string { return "fleet" }

func (t *FleetTool) Description() string { return fleetDescription }

const fleetDescription = "The way to get work done on code or in a repository: start, check on, answer, land " +
	"or stop tasks. Each task is a separate Claude Code agent working in its own copy of a " +
	"repository, in its own terminal pane, and it keeps going after this conversation ends. Use " +
	"`start` for ANY work in a repository — inspecting, changing, investigating — and for anything " +
	"that will take more than a couple of minutes; `delegate` is only for a quick lookup they wait " +
	"on. Use `list` whenever they ask how things are going. " +
	"Use `answer` to pass their reply to a task that is waiting on them. Vera lands a task by " +
	"itself when it says done; `land` is only for landing early or retrying after a landing " +
	"failed. `stop` abandons one — only when they clearly said so."

// Schema is written out rather than built from a map because it is
// the one thing about a tool the model ever sees, and a literal is
// what a person reviewing it can read.
func (t *FleetTool) Schema() json.RawMessage { return json.RawMessage(fleetSchema) }

const fleetSchema = `{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["list", "start", "answer", "resume", "land", "stop"]
    },
    "brief": {
      "type": "string",
      "description": "start: what to accomplish, in plain prose, with everything the agent needs to act alone. Say the goal, not the steps."
    },
    "project": {
      "type": "string",
      "description": "start: the repository, by the name or path listed in the prompt. Leave empty only when they mean the one in front of them."
    },
    "kind": {
      "type": "string",
      "enum": ["ship", "scout"],
      "description": "start: ship changes code; scout only investigates and reports."
    },
    "task": {
      "type": "string",
      "description": "answer/land/stop: the task id from list."
    },
    "text": {
      "type": "string",
      "description": "answer: what to tell the task, in the person's words."
    }
  },
  "required": ["action"]
}`

type fleetArgs struct {
	Action  string `json:"action"`
	Brief   string `json:"brief"`
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Task    string `json:"task"`
	Text    string `json:"text"`
}

// Paths is the repository a start is about, when it named one. The
// policy matches a path against the profile's globs, so a rule can
// say where work may be started without knowing that the fleet calls
// it `project`.
func (t *FleetTool) Paths(args json.RawMessage) []string {
	var a fleetArgs
	if json.Unmarshal(args, &a) != nil || a.Action != "start" {
		return nil
	}
	if strings.TrimSpace(a.Project) == "" {
		return nil
	}
	return []string{a.Project}
}

// Scope is how far an "always" about one of these calls reaches: the
// verb, and only the verb.
//
// The fleet is one tool with six of them, and they are not one
// question. A person who says always to a `list` has said the fleet
// may report on itself; they have not said it may abandon a task.
// Saying so here rather than letting the Gate guess is what keeps
// those apart, and the grant reads back as "fleet stop".
//
// A rule that decides BEFORE anybody is asked keys on the same
// argument a different way — `when = { action = "stop" }` — and that
// is the policy's half of this. See policyRules.
func (t *FleetTool) Scope(args json.RawMessage) string {
	var a fleetArgs
	if json.Unmarshal(args, &a) != nil {
		return ""
	}
	return a.Action
}

// Run does one fleet call and says what the model is told.
//
// An error is the call not happening: a room that could not be
// opened, a verb with nothing to act on. Everything that did happen
// comes back as text, including a picture of tasks that says several
// of them are stuck.
func (t *FleetTool) Run(ctx context.Context, args json.RawMessage, h tool.Handle) (tool.Result, error) {
	if t.Fleet == nil {
		return tool.Result{}, errors.New("there is no fleet: no multiplexer to open a room in")
	}
	var a fleetArgs
	if json.Unmarshal(args, &a) != nil || a.Action == "" {
		return tool.Result{}, errors.New("the request was not readable")
	}
	// The task this call is about, for the harness's record. It is set
	// before the work so that a round which fails still says which
	// task it failed on, and a start overwrites it with the id of the
	// task it opened.
	meta := map[string]any{}
	if a.Task != "" {
		meta[tool.MetaTask] = a.Task
	}
	text, err := t.act(ctx, h, meta, a)
	if err != nil {
		return tool.Result{Meta: meta}, err
	}
	return tool.Result{Text: text, Meta: meta}, nil
}

func (t *FleetTool) act(ctx context.Context, h tool.Handle, meta map[string]any, args fleetArgs) (string, error) {
	switch args.Action {
	case "list":
		views, err := t.Fleet.Tasks(ctx)
		if err != nil {
			return "", err
		}
		// Told to the model is told to the person: what was unread is
		// now seen, and a scout whose report was seen can close.
		for _, v := range views {
			if len(v.Unread) > 0 {
				_ = t.Fleet.Seen(v.ID)
			}
		}
		return describeFleet(views, time.Now()), nil

	case "start":
		if strings.TrimSpace(args.Brief) == "" {
			return "", fmt.Errorf("a task needs a brief: say what should be accomplished")
		}
		project := args.Project
		if project == "" {
			// The repository in front of them, which the harness knows
			// and the model did not say.
			project = h.Value(tool.Cwd)
		}
		if project == "" {
			return "", fmt.Errorf("no repository was named and none is in front of them; ask which project")
		}
		h.Say("Opening a room for that…")
		task, err := t.Fleet.Spawn(ctx, fleet.Request{Project: project, Kind: fleet.Kind(args.Kind), Brief: args.Brief})
		if err != nil {
			return "", err
		}
		meta[tool.MetaTask] = task.ID
		where := "in " + shortPath(task.Project)
		if task.Branch != "" {
			where += " on branch " + task.Branch
		}
		return fmt.Sprintf("Started task %s %s. It is working now and will keep going after this conversation; "+
			"they can ask how it is going later. Tell them it has started, in a sentence.", task.ID, where), nil

	case "answer":
		if args.Task == "" || strings.TrimSpace(args.Text) == "" {
			return "", fmt.Errorf("answer needs a task id and the text to send")
		}
		if err := t.Fleet.Answer(ctx, args.Task, args.Text); err != nil {
			return "", err
		}
		return "Sent. The task has their answer and is working again.", nil

	case "resume":
		if args.Task == "" {
			return "", fmt.Errorf("resume needs a task id")
		}
		h.Say("Picking it back up…")
		task, err := t.Fleet.Resume(ctx, args.Task)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Resumed task %s in %s; it is working again where it left off.", task.ID, shortPath(task.Project)), nil

	case "land":
		if args.Task == "" {
			return "", fmt.Errorf("land needs a task id")
		}
		h.Say("Landing it…")
		if err := t.Fleet.Land(ctx, args.Task); err != nil {
			return "", err
		}
		return "Landed: the task's work is merged and its room is closed.", nil

	case "stop":
		if args.Task == "" {
			return "", fmt.Errorf("stop needs a task id")
		}
		// Never force from here. Unlanded work is discarded only by a
		// person at the machine, and the refusal says why.
		if err := t.Fleet.Teardown(ctx, args.Task, false); err != nil {
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
		where := shortPath(v.Project)
		if v.Branch != "" {
			where += ", " + v.Branch
		}
		fmt.Fprintf(&b, "Task %s (%s): %s — %s.", v.ID, where, trim(firstSentence(v.Brief), 120), fleetPhrase(v, now))
		if len(v.Unread) > 0 {
			b.WriteString(" Since they last looked:")
			for _, s := range v.Unread {
				fmt.Fprintf(&b, " [%s] %s", s.Verb, trim(strings.TrimSpace(s.Text), 160))
			}
		}
		b.WriteString("\n")
		// The report is the deliverable; when the task is at rest and
		// the person has not seen it, it is what they are asking for.
		if v.Report != "" && (v.State == fleet.Finished || v.State == fleet.Broken || v.State == fleet.Decision) && len(v.Unread) > 0 {
			b.WriteString("Its report:\n" + trim(v.Report, 3000) + "\n")
		}
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
		return "its terminal is gone (the multiplexer was restarted?) — its work is intact; `resume` picks it up where it left off"
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
