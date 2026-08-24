package main

import (
	"path/filepath"
	"testing"
	"time"
)

func fresh(t *testing.T) *Memory {
	t.Helper()
	return newMemory(filepath.Join(t.TempDir(), "memory.json"))
}

func TestMemorySurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.json")

	first := newMemory(path)
	first.Apply(Revision{Add: []string{"Lives in Vienna."}}, "c1")

	// The whole difference between memory and history.
	second := newMemory(path)
	facts := second.All()
	if len(facts) != 1 || facts[0].Text != "Lives in Vienna." {
		t.Fatalf("memory did not survive: %+v", facts)
	}
	// And ids keep counting from where they were, so a fact learned
	// after a restart cannot collide with one learned before it.
	second.Apply(Revision{Add: []string{"Dislikes overnight flights."}}, "c2")
	got := second.All()
	if got[0].ID == got[1].ID {
		t.Fatalf("two facts share id %d", got[0].ID)
	}
}

func TestCorrectionReplacesRatherThanAccumulates(t *testing.T) {
	m := fresh(t)
	m.Apply(Revision{Add: []string{"Lives in Denver."}}, "c1")
	id := m.All()[0].ID

	m.Apply(Revision{Replace: []Replacement{{ID: id, With: "Lives in Austin."}}}, "c2")

	facts := m.All()
	if len(facts) != 1 {
		t.Fatalf("a correction left %d facts; both beliefs now survive: %+v", len(facts), facts)
	}
	if facts[0].Text != "Lives in Austin." {
		t.Fatalf("the correction did not take: %q", facts[0].Text)
	}
	if facts[0].ID != id {
		t.Fatal("a corrected fact became a different fact")
	}
}

func TestReplacingWithNothingForgets(t *testing.T) {
	m := fresh(t)
	m.Apply(Revision{Add: []string{"Owns a dog."}}, "c1")
	id := m.All()[0].ID
	m.Apply(Revision{Replace: []Replacement{{ID: id, With: "  "}}}, "c2")
	if n := m.Count(); n != 0 {
		t.Fatalf("replacing with nothing left %d facts", n)
	}
}

// The model sometimes cites an id that no longer exists. The content is
// usually fine even when the bookkeeping is not, so it should not be
// thrown away.
func TestReplacingSomethingUnknownKeepsTheContent(t *testing.T) {
	m := fresh(t)
	m.Apply(Revision{Replace: []Replacement{{ID: 99, With: "Plays the cello."}}}, "c1")
	facts := m.All()
	if len(facts) != 1 || facts[0].Text != "Plays the cello." {
		t.Fatalf("content was lost with the bad id: %+v", facts)
	}
}

func TestTheSameFactIsNotKeptTwice(t *testing.T) {
	m := fresh(t)
	m.Apply(Revision{Add: []string{"Vegetarian."}}, "c1")
	m.Apply(Revision{Add: []string{"  vegetarian.  "}}, "c2")
	if n := m.Count(); n != 1 {
		t.Fatalf("the same fact is held %d times", n)
	}
}

func TestMemoryStopsGrowing(t *testing.T) {
	m := fresh(t)
	m.limit = 5
	for i := range 20 {
		m.Apply(Revision{Add: []string{string(rune('a'+i)) + " fact"}}, "c")
		time.Sleep(time.Millisecond)
	}
	if n := m.Count(); n > 5 {
		t.Fatalf("memory grew to %d past a limit of 5", n)
	}
	// The newest survive; a fact still true will be learned again.
	last := m.All()[m.Count()-1].Text
	if last != "t fact" {
		t.Fatalf("the most recent fact was evicted; kept %q", last)
	}
}

func TestReciteIsEmptyWhenNothingIsKnown(t *testing.T) {
	if got := fresh(t).Recite(); got != "" {
		t.Fatalf("an empty memory recited %q, which would go into every prompt", got)
	}
}

func TestForgetting(t *testing.T) {
	m := fresh(t)
	m.Apply(Revision{Add: []string{"one", "two", "three"}}, "c")
	ids := m.All()
	if n := m.Forget(ids[1].ID); n != 1 {
		t.Fatalf("forgot %d facts, want 1", n)
	}
	if m.Count() != 2 {
		t.Fatalf("%d facts left, want 2", m.Count())
	}
	if n := m.ForgetAll(); n != 2 {
		t.Fatalf("forget-all reported %d", n)
	}
	// And it is durable, not just in-process.
	if newMemory(m.path).Count() != 0 {
		t.Fatal("forgotten facts came back after a reload")
	}
}

// The extractor is a model, so its output arrives in whatever shape it
// feels like. Refusing fenced JSON would throw away good answers.
func TestRevisionParsing(t *testing.T) {
	good := map[string]string{
		"plain":            `{"add":["Lives in Vienna."]}`,
		"fenced":           "```json\n{\"add\":[\"Lives in Vienna.\"]}\n```",
		"fenced bare":      "```\n{\"add\":[\"Lives in Vienna.\"]}\n```",
		"with a preamble":  "Here is the revision:\n{\"add\":[\"Lives in Vienna.\"]}",
		"trailing chatter": `{"add":["Lives in Vienna."]} — nothing else changed.`,
	}
	for name, raw := range good {
		r, ok := parseRevision(raw)
		if !ok {
			t.Errorf("%s did not parse: %q", name, raw)
			continue
		}
		if len(r.Add) != 1 || r.Add[0] != "Lives in Vienna." {
			t.Errorf("%s parsed to %+v", name, r)
		}
	}

	// The common case: nothing worth keeping.
	empty, ok := parseRevision("{}")
	if !ok || !empty.empty() {
		t.Fatalf("an empty revision did not read as empty: %+v", empty)
	}

	if _, ok := parseRevision("I could not determine anything."); ok {
		t.Error("prose parsed as a revision")
	}
}
