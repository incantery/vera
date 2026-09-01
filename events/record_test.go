package events

import (
	"strings"
	"testing"
)

func TestRecorderNamesTheRepositoryFromThePath(t *testing.T) {
	l := newLog(t)
	r := &Recorder{Log: l, RepoOf: func(path string) string {
		if strings.Contains(path, "rook") {
			return "rook"
		}
		return "vera"
	}}
	r.Record(Event{At: at(1, 9), Source: "fleet", Kind: "task.finished", Text: "done", Project: "/src/rook"})
	got, err := Read(l.Dir, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Repo != "rook" {
		t.Fatalf("want the repository filled in, got %+v", got)
	}
}

func TestRecorderWithNoLogIsSilentNotFatal(t *testing.T) {
	var r *Recorder
	r.Record(Event{Source: "fleet", Kind: "task.finished", Text: "done"})
	(&Recorder{}).Record(Event{Source: "fleet", Kind: "task.finished", Text: "done"})
}

func TestTaskKeepsOnlyTheStatesWorthRemembering(t *testing.T) {
	for _, state := range []string{"waiting", "decision", "finished", "broken", "closed", "interrupted", "held", "stale", "gone"} {
		if _, ok := Task("T-1", "alpha", "/src/vera", "do a thing", state, "running", at(1, 9)); !ok {
			t.Fatalf("want %q recorded", state)
		}
	}
	for _, state := range []string{"running", "quiet", "", "nonsense"} {
		if _, ok := Task("T-1", "alpha", "/src/vera", "b", state, "running", at(1, 9)); ok {
			t.Fatalf("want %q left out of the stream", state)
		}
	}
}

func TestTaskLineReadsAsASentence(t *testing.T) {
	e, ok := Task("T-1", "alpha", "/src/vera", "wire up the thing", "decision", "running", at(1, 9))
	if !ok {
		t.Fatal("want the event")
	}
	if e.Kind != "task.decision" || e.Task != "T-1" || e.Subject != "alpha" || e.Project != "/src/vera" {
		t.Fatalf("want the keys filled, got %+v", e)
	}
	if !strings.HasPrefix(e.Text, "alpha is blocked on a decision") || !strings.Contains(e.Text, "wire up the thing") {
		t.Fatalf("want a sentence with the brief on it, got %q", e.Text)
	}
	if e.Fields["prev"] != "running" {
		t.Fatalf("want the previous state kept, got %+v", e.Fields)
	}
	// A task with no name is still addressable by its id.
	nameless, _ := Task("T-2", "", "/src/vera", "", "finished", "", at(1, 9))
	if !strings.HasPrefix(nameless.Text, "T-2 ") {
		t.Fatalf("want the id to stand in for a missing name, got %q", nameless.Text)
	}
}

func TestSaidKeepsTheAgentsOwnWords(t *testing.T) {
	e, ok := Said("T-1", "alpha", "/src/vera", "blocked", "which database?", "agent", at(1, 9))
	if !ok {
		t.Fatal("want the event")
	}
	if !strings.Contains(e.Text, "which database?") || e.Fields["verb"] != "blocked" {
		t.Fatalf("want the agent's words verbatim, got %+v", e)
	}
	if _, ok := Said("T-1", "alpha", "/src/vera", "working", "   ", "", at(1, 9)); ok {
		t.Fatal("want a status with nothing in it left out")
	}
}

func TestExchangeKeepsTheQuestionNotTheAnswer(t *testing.T) {
	e := Exchange("c-1", "phone", "claude-opus-5", "what is on my list", 2, "", at(1, 9))
	if e.Kind != "vera.asked" || !strings.Contains(e.Text, "what is on my list") {
		t.Fatalf("want the question, got %+v", e)
	}
	if e.Fields["tools"] != "2" || e.Fields["model"] != "claude-opus-5" || e.Fields["device"] != "phone" {
		t.Fatalf("want the shape of the answer kept, got %+v", e.Fields)
	}
	bad := Exchange("c-1", "", "claude-opus-5", "what is on my list", 0, "the model timed out", at(1, 9))
	if bad.Kind != "vera.failed" || !strings.Contains(bad.Text, "timed out") {
		t.Fatalf("want a failure said as one, got %+v", bad)
	}
}

func TestSpawnedLandedAndMachineAreReadable(t *testing.T) {
	s := Spawned("T-1", "alpha", "/src/vera", "wire up the thing", "scout", at(1, 9))
	if s.Kind != "task.spawned" || !strings.Contains(s.Text, "a scout") || !strings.Contains(s.Text, "wire up the thing") {
		t.Fatalf("want the spawn said plainly, got %+v", s)
	}
	l := Landed("T-1", "alpha", "/src/vera", "merged", "", at(1, 9))
	if l.Kind != "task.landed" || !strings.Contains(l.Text, "merged") {
		t.Fatalf("want a landing, got %+v", l)
	}
	f := Landed("T-1", "alpha", "/src/vera", "merged", "the branch had conflicts", at(1, 9))
	if f.Kind != "task.land-failed" || !strings.Contains(f.Text, "conflicts") {
		t.Fatalf("want the failure and its reason, got %+v", f)
	}
	away := Machine("sleep", true, at(1, 9))
	back := Machine("sleep", false, at(1, 10))
	if away.Kind != "machine.away" || back.Kind != "machine.back" {
		t.Fatalf("want both halves of an absence, got %q and %q", away.Kind, back.Kind)
	}
}

func TestEveryConstructorProducesAValidEvent(t *testing.T) {
	task, _ := Task("T-1", "alpha", "/src/vera", "b", "finished", "", at(1, 9))
	said, _ := Said("T-1", "alpha", "/src/vera", "working", "on it", "agent", at(1, 9))
	for _, e := range []Event{
		task, said,
		Spawned("T-1", "alpha", "/src/vera", "b", "ship", at(1, 9)),
		Landed("T-1", "alpha", "/src/vera", "merged", "", at(1, 9)),
		Exchange("c-1", "phone", "m", "hello", 0, "", at(1, 9)),
		Machine("offline", true, at(1, 9)),
		Rook("rook.gone", "the terminal engine went away", "", at(1, 9)),
	} {
		if err := e.Valid(); err != nil {
			t.Fatalf("%+v: %v", e, err)
		}
	}
}
