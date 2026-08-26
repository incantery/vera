package main

import (
	"strings"
	"testing"

	"github.com/incantery/vera/home"
)

// The extractor is a model, so its output arrives in whatever shape it
// feels like. Refusing fenced JSON would throw away good answers.
func TestRevisionParsing(t *testing.T) {
	good := map[string]string{
		"plain":            `{"add":[{"name":"lives-in-vienna","type":"user","fact":"Lives in Vienna."}]}`,
		"fenced":           "```json\n{\"add\":[{\"name\":\"lives-in-vienna\",\"fact\":\"Lives in Vienna.\"}]}\n```",
		"fenced bare":      "```\n{\"add\":[{\"name\":\"lives-in-vienna\",\"fact\":\"Lives in Vienna.\"}]}\n```",
		"with a preamble":  "Here is the revision:\n{\"add\":[{\"name\":\"lives-in-vienna\",\"fact\":\"Lives in Vienna.\"}]}",
		"trailing chatter": `{"add":[{"name":"lives-in-vienna","fact":"Lives in Vienna."}]} — nothing else changed.`,
	}
	for name, raw := range good {
		r, ok := parseRevision(raw)
		if !ok {
			t.Errorf("%s did not parse: %q", name, raw)
			continue
		}
		if len(r.Add) != 1 || r.Add[0].Fact != "Lives in Vienna." || r.Add[0].Name != "lives-in-vienna" {
			t.Errorf("%s parsed to %+v", name, r)
		}
	}

	// "replace" is what the old protocol called it and what a model
	// reaches for anyway; it means update.
	r, ok := parseRevision(`{"replace":[{"name":"lives-in-denver","fact":"Lives in Austin."}]}`)
	if !ok || len(r.Update) != 1 || r.Update[0].Fact != "Lives in Austin." {
		t.Fatalf("replace did not read as an update: %+v", r)
	}

	// The common case: nothing worth keeping.
	empty, ok := parseRevision("{}")
	if !ok || !empty.Empty() {
		t.Fatalf("an empty revision did not read as empty: %+v", empty)
	}

	if _, ok := parseRevision("I could not determine anything."); ok {
		t.Error("prose parsed as a revision")
	}
}

// The whole loop, without a model: what the extractor says becomes
// files, and those files are what the next prompt carries.
func TestARevisionBecomesWhatTheNextPromptKnows(t *testing.T) {
	place, err := home.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, ok := parseRevision(`{"add":[{"name":"prefers-short-answers","type":"feedback","fact":"Prefers short answers."}]}`)
	if !ok {
		t.Fatal("did not parse")
	}
	if err := place.Memory().Apply(r, "c1"); err != nil {
		t.Fatal(err)
	}
	mind := &Mind{Memory: place.Memory()}
	if got := mind.preface(); !strings.Contains(got, "Prefers short answers.") {
		t.Fatalf("what was learned did not reach the next prompt:\n%s", got)
	}
}
