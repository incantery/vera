// The semantic event log: what vera did, in words a human reads,
// with a pointer to the thing that actually happened.
//
// The board says where work IS; this says what MOVED. A card sitting
// in "progress" for ten minutes is one row in a table — the same ten
// minutes here is a plan accepted, three nodes opened, a review
// finding raised, an implementation revising. That difference is the
// whole product: the user should be able to look away and come back
// to a story rather than a status.
//
// The one rule this file exists to enforce: EVERY EVENT NAMES ITS
// SOURCE. An event vera emits about her own act is sourced by the
// card she mutated — she is the actor, so her word IS the record. An
// event that says something about a WORKER's output must name the
// fork and the turn it was read from, because there vera is a
// reporter, not the author, and a reporter without a citation is
// writing fiction. Cockpit is then not a parallel system but a
// click-through: every line here can be opened to the artifact under
// it.
//
// Same persistence discipline as the other journals: append-only
// jsonl, replayed at startup for the sequence, never queried. Losing
// it costs history, not correctness.
package main

import (
	"encoding/json"
	"sync"
	"time"
)

// The event vocabulary. Closed on purpose: a kind that is not in this
// list is a kind the page cannot render, and a stream whose verbs
// drift is a stream nobody can build a UI against.
const (
	// The goal's own arc.
	evGoalAccepted = "goal.accepted" // vera took the ask and shaped it
	evGoalReady    = "goal.ready"    // every node landed; it is the human's again

	// The graph.
	evPlanDrawn   = "plan.drawn"   // a work graph exists, with N nodes
	evNodePlanned = "node.planned" // a node is on the board, waiting on its deps
	evNodeOpened  = "node.opened"  // a node's dependencies cleared; it may run
	evNodeMoved   = "node.moved"   // a node changed its state
	evNodeLanded  = "node.landed"  // a node finished, one way or another

	// The workers. Everything below is a CLAIM about someone else's
	// output and may not be written without a fork to point at.
	evWorkerSpawned = "worker.spawned"
	evFindingRaised = "finding.raised" // a reviewer found something real
	evFindingClosed = "finding.closed"
	evApproachSplit = "approaches.split" // two nodes disagree; a third reads both

	// The boundary. The one event that means the machine stopped and
	// the human is the next actor.
	evNeedsHuman = "needs.human"
	evHumanRuled = "human.ruled"
)

// sourceRef is the citation: where the claim can be checked. Task is
// always set — it is the card the event belongs to, and vera's own
// acts are sourced by the mutation itself. Fork and Msg are what a
// claim about a worker needs: the session id a human can `claude
// --resume`, and the turn inside it.
type sourceRef struct {
	Task string `json:"task"`
	Run  string `json:"run,omitempty"`
	Fork string `json:"fork,omitempty"` // the claude session the words came from
	Msg  int    `json:"msg,omitempty"`  // index into that transcript
	File string `json:"file,omitempty"` // a journal line, when that is the artifact
}

// An event is one meaningful movement. Text is the whole human
// payload — one sentence, already compressed, written to be read on a
// phone at a glance. The page renders Text; Cockpit follows Src.
type event struct {
	Seq  int64     `json:"seq"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	Goal string    `json:"goal,omitempty"` // the root card this belongs to
	Node string    `json:"node,omitempty"` // the card it happened on
	Text string    `json:"text"`
	Src  sourceRef `json:"src"`
}

// eventLog is the append-only record plus a tail the page can read
// without touching disk. The ring is small on purpose: anything older
// than the tail is history, and history lives in the file.
type eventLog struct {
	path string // "" = remember only while running

	mu    sync.Mutex
	seq   int64
	ring  []event
	limit int

	hub *hub

	// unsourced counts events refused for naming no card. Production
	// never reads it; the test that asserts it stays zero is what
	// keeps the rule a rule instead of a paragraph.
	unsourced int
}

func defaultEventPath() string { return statePath("vera-events.jsonl") }

func newEventLog(path string, h *hub) *eventLog {
	l := &eventLog{path: path, limit: 512, hub: h}
	// Replay the tail into the ring, not just the sequence. The story is
	// half the work view, and a restart that kept only the counter would
	// blank every goal's history in the UI while the journal on disk
	// still held it — the work would look like it had never happened.
	//
	// The sequence matters for its own reason: renumbering from 1 would
	// make two different events share an id, and every client holding a
	// cursor would silently re-read the past as the present.
	eachLine(path, func(b []byte) {
		var e event
		if json.Unmarshal(b, &e) != nil || e.Kind == "" {
			return
		}
		if e.Seq > l.seq {
			l.seq = e.Seq
		}
		l.ring = append(l.ring, e)
		if len(l.ring) > l.limit {
			l.ring = l.ring[len(l.ring)-l.limit:]
		}
	})
	return l
}

// emit records a transition vera made herself. She is the actor, so
// the card she moved is the citation — there is nothing else to point
// at and nothing else needed.
func (l *eventLog) emit(kind, goal, node, text string) event {
	return l.record(event{Kind: kind, Goal: goal, Node: node, Text: text,
		Src: sourceRef{Task: node}})
}

// claim records something vera says about a WORKER's output — a
// finding, a disagreement, a spawn. The fork and turn are required
// because vera did not write those words and must say where they came
// from. A claim that cannot cite is a claim vera should not make.
func (l *eventLog) claim(kind, goal, node, text string, src sourceRef) event {
	if src.Task == "" {
		src.Task = node
	}
	return l.record(event{Kind: kind, Goal: goal, Node: node, Text: text, Src: src})
}

// record is safe on a nil log, the same courtesy the hub extends, so
// a fixture need not carry one to exercise the code that emits.
func (l *eventLog) record(e event) event {
	if l == nil {
		return event{}
	}
	if e.Kind == "" || e.Src.Task == "" {
		// Nothing to render, or nothing to check it against. Both are
		// programming errors and neither belongs in a stream the user is
		// invited to trust.
		l.mu.Lock()
		l.unsourced++
		l.mu.Unlock()
		return event{}
	}
	l.mu.Lock()
	l.seq++
	e.Seq = l.seq
	if e.At.IsZero() {
		e.At = time.Now()
	}
	l.ring = append(l.ring, e)
	if len(l.ring) > l.limit {
		l.ring = append(l.ring[:0], l.ring[len(l.ring)-l.limit:]...)
	}
	l.mu.Unlock()
	appendLine(l.path, e)
	l.hub.notify()
	return e
}

// since returns everything after a cursor, oldest first — the shape a
// stream wants. A cursor older than the ring gets the whole ring
// rather than a lie about there being nothing.
func (l *eventLog) since(seq int64) []event {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]event, 0, len(l.ring))
	for _, e := range l.ring {
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	return out
}

// forGoal is the work view's read: one goal's whole story, oldest
// first, so the page can play it as choreography rather than sort a
// table.
func (l *eventLog) forGoal(goal string) []event {
	if l == nil || goal == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []event
	for _, e := range l.ring {
		if e.Goal == goal {
			out = append(out, e)
		}
	}
	return out
}

// cursor is the newest sequence issued — what a client stores so its
// next read starts where this one stopped.
func (l *eventLog) cursor() int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seq
}
