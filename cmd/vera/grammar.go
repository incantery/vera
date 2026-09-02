// How the transcript reads.
//
// mote draws the screen; this decides what is written on it. The
// design reference (Claude Design project "TUI app Polish
// Discussion", turn 3) asks for one thing throughout, and it is the
// rule this file exists to keep: **one state per channel**.
//
//	◐ working    ◇ needs you    ○ quiet    ✓ done    × failed
//	● unread — a second channel, independent of all five
//
// Every state has its own shape, so the line reads with the colour
// off; red is failure only, so a task asking for a word is not
// dressed as a crash; and ● means unread and nothing else, so a task
// can be done AND unread in the same breath.
//
// Three grammars come out of that, and nothing else in the chat
// writes a line in any of them:
//
//	taskNotice   a task's lifecycle: glyph, what it is, what to do
//	failure      an error: what failed, what it changed, what next
//	toolSays     a tool receipt: the call as a sentence
package main

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/incantery/vera/fleet"
)

// The glyphs. They are here rather than inline so that the vocabulary
// is a list somebody can read, and so that rook's rail and this
// transcript can be checked against each other by eye.
const (
	glyphWorking  = "◐"
	glyphNeedsYou = "◇"
	glyphQuiet    = "○"
	glyphDone     = "✓"
	glyphFailed   = "×"
	glyphUnread   = "●"
)

// mark is the glyph and the short word for a state — the head of a
// notice, before the subject.
//
// Waiting, Decision and Held are all "needs you" and all ◇: from the
// person's side they are one thing (say something to it), and the
// difference between them belongs on the second line, where the
// detail is. Broken and Gone are the only two that get red's shape,
// because they are the only two where something went wrong.
func mark(v fleet.View) (glyph, word string) {
	switch v.State {
	case fleet.Running, fleet.Quiet:
		return glyphWorking, "working"
	case fleet.Waiting, fleet.Decision, fleet.Held:
		return glyphNeedsYou, "needs you"
	case fleet.Stale:
		return glyphQuiet, "has gone quiet"
	case fleet.Interrupted:
		return glyphQuiet, "interrupted"
	case fleet.Finished:
		if v.Kind == fleet.Scout {
			return glyphDone, "reported"
		}
		return glyphDone, "finished"
	case fleet.Broken:
		return glyphFailed, "failed"
	case fleet.Gone:
		return glyphFailed, "is gone"
	}
	return glyphQuiet, string(v.State)
}

// kindWord is what a task is called out loud. A scout is a scout — it
// is the word the person used to start it, and it says up front that
// there is nothing to land.
func kindWord(v fleet.View) string {
	if v.Kind == fleet.Scout {
		return "Scout"
	}
	return "Task"
}

// event is one lifecycle line in the design's grammar:
//
//	◇ Scout needs you · Investigate Vera's /effort
//	Luna has no effort control · /answer a3f2 <text>
//
// The head is the state and the subject; the second line is what is
// true right now and the one thing to do about it. mote draws a
// notice down a gutter with the continuation indented, so the two
// read as one block rather than as two events.
//
// Either half of the second line may be missing, and a task with
// neither gets one line. Nothing here is padded out to look complete.
func event(head, detail, next string) string {
	body := detail
	if next != "" {
		if body != "" {
			body += " · "
		}
		body += next
	}
	if body == "" {
		return head
	}
	return head + "\n" + body
}

// taskNotice is a change of state, as the transcript says it.
//
// The subject is the brief in the person's own words; the id appears
// only inside the command that acts on it, which is the one place it
// is worth retyping. That is the reference's rule for the status line
// ("no session id") applied where it came from: an id is not what
// anything is called, it is an argument.
func taskNotice(v fleet.View, waited string) string {
	glyph, word := mark(v)
	head := glyph + " " + kindWord(v) + " " + word + " · " + trim(firstSentence(v.Brief), 60)
	if unreadReport(v) {
		head += " " + glyphUnread
	}
	return event(head, taskDetail(v, waited), taskNext(v))
}

// unreadReport: something has been written here that nobody has read.
// It is a channel of its own — a scout can be done and unread at the
// same time, and the tick alone would say there is nothing here.
func unreadReport(v fleet.View) bool { return v.Report != "" && len(v.Unread) > 0 }

// taskDetail is the second line's first half: what is actually going
// on: the task's own last words where it left any, and what is true
// of the state where that adds something the words do not.
//
// Nothing here restates a command. "Ready to land" and "/land a1" are
// the same sentence twice, so the qualifier gives way to the verb —
// except when the task said nothing at all, where a line with only a
// command on it is a line that never says what happened.
func taskDetail(v fleet.View, waited string) string {
	var parts []string
	switch {
	case v.State == fleet.Decision && v.LandFailure != "":
		parts = append(parts, trim(oneLine(v.LandFailure), 120))
	case v.Last != nil && v.Last.Text != "":
		parts = append(parts, trim(oneLine(v.Last.Text), 120))
	}
	if q := qualifier(v, waited, len(parts) == 0); q != "" {
		parts = append(parts, q)
	}
	return strings.Join(parts, " · ")
}

// qualifier is what the state adds to what the task said. alone says
// the task said nothing, which is when a state that would otherwise
// keep quiet has to speak.
func qualifier(v fleet.View, waited string, alone bool) string {
	switch v.State {
	case fleet.Waiting:
		if waited != "" {
			return "waiting " + waited
		}
	case fleet.Held:
		return "paused on something outside"
	case fleet.Stale:
		return "worth a look"
	case fleet.Interrupted:
		return v.Machine.Why()
	case fleet.Gone:
		return "its terminal is gone"
	case fleet.Finished:
		switch {
		case v.Kind == fleet.Scout:
		case v.AutoLand:
			// Nobody is waiting for the person and there is no verb to
			// offer, so this is the one thing they need to hear.
			return "Vera is landing it"
		case alone:
			return "ready to land"
		}
	case fleet.Broken:
		if alone {
			return "it failed"
		}
	}
	return ""
}

// taskNext is the second line's second half: the one command that
// moves this task on from where it is. A task nobody has to do
// anything about gets none, and says nothing rather than offering a
// verb it has not earned.
func taskNext(v fleet.View) string {
	switch v.State {
	case fleet.Waiting, fleet.Decision, fleet.Held:
		return "/answer " + v.ID + " <text>"
	case fleet.Broken, fleet.Gone:
		return "/resume " + v.ID
	case fleet.Stale:
		return "/report " + v.ID
	case fleet.Finished:
		if v.Report != "" || v.Kind == fleet.Scout {
			return "/report " + v.ID
		}
		if v.AutoLand {
			return ""
		}
		return "/land " + v.ID
	}
	if v.Report != "" {
		return "/report " + v.ID
	}
	return ""
}

// --- errors -------------------------------------------------------------

// failure is what an error says, in the three parts the reference
// asks for: what failed, what that leaves unchanged, and the next
// thing to try.
//
//	Luna does not expose a reasoning-effort control
//	→ model and settings unchanged · /model shows what does
//
// The point of the second line is that a refusal is not a loss: after
// a command that would not run, the person needs to know the machine
// is where they left it, not just that they were told no. mote paints
// the glyph and the block; the words are the application's.
func failure(what, consequence, next string) string {
	rest := consequence
	if next != "" {
		if rest != "" {
			rest += " · "
		}
		rest += next
	}
	if rest == "" {
		return what
	}
	return what + "\n→ " + rest
}

// noDial is what a model with no reasoning control says, as an error
// rather than as a setting. "effort: none" reads as a dial turned
// down; the truth is that there is no dial, and the difference is
// what tells somebody to change model rather than to keep typing
// efforts at this one.
func noDial(model string) string {
	return failure(model+" does not expose a reasoning-effort control",
		"model and settings unchanged", "/model shows what does")
}

// --- tool receipts ------------------------------------------------------

// toolSays is the sentence a tool card reads as — the call as it
// would be said out loud, rather than the JSON it was made with:
//
//	▸ ✓ fleet · start scout vera "Investigate Vera's /effort" · 193ms
//
// Only the tools this terminal knows the shape of get one. Everything
// else returns "" and mote summarizes the arguments itself, which is
// the right answer for a tool the chat has never heard of.
func toolSays(name, args string) string {
	switch name {
	case "fleet":
		return fleetSays(args)
	case "delegate":
		var a struct {
			Task string `json:"task"`
		}
		if json.Unmarshal([]byte(args), &a) != nil {
			return ""
		}
		return trim(oneLine(a.Task), 90)
	}
	return ""
}

func fleetSays(args string) string {
	var a struct {
		Action  string `json:"action"`
		Brief   string `json:"brief"`
		Project string `json:"project"`
		Kind    string `json:"kind"`
		Task    string `json:"task"`
		Text    string `json:"text"`
	}
	if json.Unmarshal([]byte(args), &a) != nil || a.Action == "" {
		return ""
	}
	parts := []string{a.Action}
	switch a.Action {
	case "start":
		if a.Kind != "" {
			parts = append(parts, a.Kind)
		}
		if a.Project != "" {
			parts = append(parts, shortPath(a.Project))
		}
		if a.Brief != "" {
			parts = append(parts, strconv.Quote(trim(oneLine(a.Brief), 48)))
		}
	case "answer":
		if a.Task != "" {
			parts = append(parts, a.Task)
		}
		if a.Text != "" {
			parts = append(parts, strconv.Quote(trim(oneLine(a.Text), 48)))
		}
	default:
		if a.Task != "" {
			parts = append(parts, a.Task)
		}
	}
	return strings.Join(parts, " ")
}
