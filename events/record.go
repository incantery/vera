package events

import (
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// The writers' side: one place where a thing that happened becomes a
// line somebody will read months later.
//
// The phrasing lives here rather than at each call site, and that is
// the point of the file. A stream written by six callers in six voices
// reads like six streams; the reason "what happened" is answerable in
// one pass is that every line is the same shape — past tense, subject
// first, no jargon that is not also on the screen the person saw.

// Recorder is the Log with the little context every writer shares. It
// never fails a caller: nothing in Vera should stop working because
// her diary could not be written, so a broken stream is a log line and
// nothing else.
type Recorder struct {
	// Log is where events go; nil records nothing, which is what a
	// test and a --no-events run both want.
	Log *Log
	// RepoOf names the repository a path belongs to. Nil leaves Repo as
	// whatever the caller set. A worktree resolves to its main
	// checkout's name, so a task's events file under the repository
	// they are about rather than under the branch they ran on.
	RepoOf func(path string) string
}

// Record fills in what it can and appends. An event with a Project but
// no Repo gets its repository named.
func (r *Recorder) Record(evs ...Event) {
	if r == nil || r.Log == nil {
		return
	}
	for i := range evs {
		if evs[i].Repo == "" && evs[i].Project != "" && r.RepoOf != nil {
			evs[i].Repo = r.RepoOf(evs[i].Project)
		}
	}
	if err := r.Log.Append(evs...); err != nil {
		slog.Warn("events: not written", "err", err)
	}
}

// Task is a change in what Vera believes about one task. The state
// names are the fleet's; the sentences are for a person who has never
// read fleet/liveness.go.
//
// Not every belief change is worth a line. Task is told about all of
// them and keeps the ones that would be news in a week — a task
// starting, asking, finishing, breaking, being closed. Running and
// quiet are the weather.
func Task(id, name, project, brief, state, prev string, at time.Time) (Event, bool) {
	said, keep := taskSaying[state]
	if !keep {
		return Event{}, false
	}
	where := name
	if where == "" {
		where = id
	}
	e := Event{
		At:      at,
		Source:  "fleet",
		Kind:    "task." + state,
		Subject: where,
		Task:    id,
		Project: project,
		Text:    where + " " + said,
		Fields:  map[string]string{"state": state},
	}
	if prev != "" {
		e.Fields["prev"] = prev
	}
	if b := trim(brief, 160); b != "" {
		e.Text += " — " + b
	}
	return e, true
}

// taskSaying is every state that earns a line, and what the line says.
// A state absent from this table is deliberately not recorded: the
// stream is what mattered, and a task going quiet for four minutes and
// coming back did not.
var taskSaying = map[string]string{
	"waiting":     "ended its turn and is waiting on you",
	"decision":    "is blocked on a decision",
	"held":        "paused on something outside it",
	"stale":       "has gone quiet long enough to be worth a look",
	"finished":    "said it is done",
	"broken":      "said it failed",
	"gone":        "lost its pane",
	"closed":      "was closed",
	"interrupted": "was interrupted — the machine went away under it",
}

// Spawned is a task being opened. It is not a belief change, so the
// supervisor never reports it and it has to be said at the door.
func Spawned(id, name, project, brief string, kind string, at time.Time) Event {
	return Event{
		At:      at,
		Source:  "fleet",
		Kind:    "task.spawned",
		Subject: name,
		Task:    id,
		Project: project,
		Text:    "started " + kindWord(kind) + " " + name + " — " + trim(brief, 200),
		Fields:  map[string]string{"kind": kind},
	}
}

// Landed is a task's branch going home, or failing to.
func Landed(id, name, project, how string, err string, at time.Time) Event {
	e := Event{
		At:      at,
		Source:  "fleet",
		Kind:    "task.landed",
		Subject: name,
		Task:    id,
		Project: project,
		Text:    "landed " + name,
	}
	if how != "" {
		e.Fields = map[string]string{"how": how}
		e.Text += " (" + how + ")"
	}
	if err != "" {
		e.Kind = "task.land-failed"
		e.Text = "could not land " + name + ": " + trim(err, 200)
	}
	return e
}

func kindWord(kind string) string {
	if kind == "scout" {
		return "a scout"
	}
	return "a task"
}

// Said is a status line a task's agent wrote about itself. These are
// the words the agent chose, kept verbatim: an agent saying "blocked
// on which database to use" is the most useful line in the stream, and
// paraphrasing it would throw away the only part that was not derived.
func Said(id, name, project, verb, text, by string, at time.Time) (Event, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Event{}, false
	}
	who := by
	if who == "" {
		who = "agent"
	}
	label := name
	if label == "" {
		label = id
	}
	return Event{
		At:      at,
		Source:  "fleet",
		Kind:    "task.said",
		Subject: label,
		Task:    id,
		Project: project,
		Text:    label + " (" + verb + ", " + who + "): " + text,
		Fields:  map[string]string{"verb": verb, "by": who},
	}, true
}

// Exchange is one thing said to Vera and what came back. The stream
// keeps the question and the shape of the answer, never the answer
// itself: the journal next door has the whole thing, and duplicating
// it here would make the file too big to grep and too private to
// paste.
func Exchange(conversation, device, model, said string, tools int, failed string, at time.Time) Event {
	e := Event{
		At:      at,
		Source:  "vera",
		Kind:    "vera.asked",
		Subject: conversation,
		Text:    "asked: " + trim(said, 200),
		Fields:  map[string]string{"model": model},
	}
	if device != "" {
		e.Fields["device"] = device
	}
	if tools > 0 {
		e.Fields["tools"] = strconv.Itoa(tools)
	}
	if failed != "" {
		e.Kind = "vera.failed"
		e.Text = "failed to answer \"" + trim(said, 120) + "\": " + trim(failed, 160)
	}
	return e
}

// Machine is this Mac being away and coming back. It is in the stream
// because it is the only thing that explains a silent night: without
// it, eight hours in which nothing happened reads as eight hours in
// which everything stalled.
func Machine(cause string, away bool, at time.Time) Event {
	word, kind := "came back", "machine.back"
	if away {
		word, kind = "went away", "machine.away"
	}
	return Event{
		At:     at,
		Source: "machine",
		Kind:   kind,
		Text:   "this machine " + word + " (" + cause + ")",
		Fields: map[string]string{"cause": cause},
	}
}

// Rook is the terminal engine itself going and returning. Vera notices
// because every pane she is watching disappears at once, and a reader
// of the stream needs to know that was rook restarting rather than
// nine agents dying.
func Rook(kind, text, subject string, at time.Time) Event {
	return Event{
		At:      at,
		Source:  "rook",
		Kind:    kind,
		Subject: subject,
		Text:    text,
	}
}
