package main

import (
	"path/filepath"
	"testing"
)

// The rule this file exists to keep: an event vera emits about her own
// act is sourced by the card she moved, because she IS the actor and
// her word is the record.
func TestEmitSourcesItselfByTheCard(t *testing.T) {
	l := newEventLog("", nil)
	e := l.emit(evNodeOpened, "T-100", "T-101", "opened the review")
	if e.Seq != 1 {
		t.Fatalf("the first event is sequence 1: %+v", e)
	}
	if e.Src.Task != "T-101" {
		t.Fatalf("vera's own act is cited by the card it moved: %+v", e.Src)
	}
	if e.At.IsZero() {
		t.Fatal("every event is stamped")
	}
}

// A claim is a statement about a WORKER's output. Vera did not write
// those words, so she must say where she read them — a reporter
// without a citation is writing fiction.
func TestClaimCarriesTheForkAndTurn(t *testing.T) {
	l := newEventLog("", nil)
	e := l.claim(evFindingRaised, "T-100", "T-102", "the journal API leaks a lifetime",
		sourceRef{Task: "T-102", Fork: "abc123", Msg: 47, Run: "R-9"})
	if e.Src.Fork != "abc123" || e.Src.Msg != 47 {
		t.Fatalf("the claim points at the turn it was read from: %+v", e.Src)
	}
	// This is what makes Cockpit a click-through rather than a parallel
	// system: the fork is resumable, the message index is addressable.
	if e.Src.Run != "R-9" {
		t.Fatalf("and at the run that produced it: %+v", e.Src)
	}
}

// An event with nothing to check it against is refused rather than
// recorded. Silently keeping it is how a narrated stream becomes a
// very pretty liar.
func TestAnUnsourcedEventIsRefused(t *testing.T) {
	l := newEventLog("", nil)
	if e := l.record(event{Kind: evFindingRaised, Text: "something happened"}); e.Seq != 0 {
		t.Fatalf("no card, no citation, no event: %+v", e)
	}
	if e := l.record(event{Text: "no kind", Src: sourceRef{Task: "T-1"}}); e.Seq != 0 {
		t.Fatal("an event the page cannot render is not an event")
	}
	if l.unsourced != 2 {
		t.Fatalf("refusals are counted so the rule is enforced, not merely documented: %d", l.unsourced)
	}
	if len(l.since(0)) != 0 {
		t.Fatal("and nothing reached the stream")
	}
}

func TestSinceIsACursorAndForGoalIsAStory(t *testing.T) {
	l := newEventLog("", nil)
	l.emit(evGoalAccepted, "T-100", "T-100", "took the ask")
	mark := l.cursor()
	l.emit(evNodeOpened, "T-100", "T-101", "opened the review")
	l.emit(evNodeOpened, "T-200", "T-200", "a different goal entirely")

	fresh := l.since(mark)
	if len(fresh) != 2 {
		t.Fatalf("a cursor reads forward from where it stopped: %d", len(fresh))
	}
	if fresh[0].Seq <= mark {
		t.Fatal("oldest first, strictly after the cursor")
	}
	story := l.forGoal("T-100")
	if len(story) != 2 {
		t.Fatalf("one goal's whole story and nobody else's: %+v", story)
	}
}

// Renumbering from 1 after a restart would make two different events
// share an id, and every client holding a cursor would silently re-read
// the past as the present.
func TestSequenceSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	first := newEventLog(path, nil)
	first.emit(evGoalAccepted, "T-100", "T-100", "took the ask")
	first.emit(evPlanDrawn, "T-100", "T-100", "drew a 3-node graph")

	second := newEventLog(path, nil)
	if second.cursor() != 2 {
		t.Fatalf("the sequence is replayed, not restarted: %d", second.cursor())
	}
	e := second.emit(evNodeOpened, "T-100", "T-101", "opened the review")
	if e.Seq != 3 {
		t.Fatalf("the next event continues the count: %d", e.Seq)
	}
}

// The story is half the work view. A restart that kept only the counter
// would blank every goal's history in the UI while the journal on disk
// still held it — the work would look like it had never happened.
func TestTheStorySurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	first := newEventLog(path, nil)
	first.emit(evPlanDrawn, "T-100", "T-100", "drew a 3-node graph")
	first.claim(evFindingRaised, "T-100", "T-101", "the journal API leaks a lifetime",
		sourceRef{Task: "T-101", Fork: "abc123", Msg: 47})
	first.emit(evNodeOpened, "T-200", "T-200", "a different goal")

	second := newEventLog(path, nil)
	story := second.forGoal("T-100")
	if len(story) != 2 {
		t.Fatalf("the goal's whole story comes back: %+v", story)
	}
	// Including the citations — a story that survived without its
	// sources would be exactly the unverifiable narration the log exists
	// to prevent.
	if story[1].Src.Fork != "abc123" || story[1].Src.Msg != 47 {
		t.Fatalf("the source survives with it: %+v", story[1].Src)
	}
	if len(second.forGoal("T-200")) != 1 {
		t.Fatal("and every other goal's too")
	}
}

// A fixture need not carry a log to exercise the code that emits —
// the same courtesy the hub extends.
func TestANilLogIsSafe(t *testing.T) {
	var l *eventLog
	l.emit(evNodeOpened, "T-1", "T-1", "nothing to record into")
	if l.since(0) != nil || l.forGoal("T-1") != nil || l.cursor() != 0 {
		t.Fatal("a nil log reads empty rather than panicking")
	}
}
