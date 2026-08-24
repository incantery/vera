package main

import (
	"strings"
	"testing"

	"github.com/grafana/agento11y/go/agento11y/experiments"
)

func scoreOf(checks []check, name string) (bool, bool) {
	for _, c := range checks {
		if c.name == name {
			return c.ok, true
		}
	}
	return false, false
}

// The scorers are the thing everything else trusts, so they get graded
// too. A markdown detector that never fires would make the suite pass
// forever and mean nothing.
func TestMarkdownIsCaught(t *testing.T) {
	bad := map[string]string{
		"bullet":     "Here you go:\n- Schönbrunn\n- The Prater",
		"asterisk":   "Try these:\n* one\n* two",
		"numbered":   "In order:\n1. Vienna\n2. Graz",
		"heading":    "## Things to see\nSchönbrunn is lovely.",
		"bold":       "The tallest is **DC Tower 1**.",
		"code fence": "Run this:\n```\nvera --addr :4780\n```",
	}
	for name, text := range bad {
		checks := judge(experiments.TestCase{}, text)
		if ok, _ := scoreOf(checks, "no_markdown"); ok {
			t.Errorf("%s went undetected: %q", name, text)
		}
	}

	fine := []string{
		"Vienna.",
		"The tallest building in Vienna is the DC Tower 1, at 250 metres.",
		"I don't know — you haven't told me that.",
		"It costs 5-10 euros, depending on the day.", // a hyphen mid-sentence is not a bullet
	}
	for _, text := range fine {
		checks := judge(experiments.TestCase{}, text)
		if ok, _ := scoreOf(checks, "no_markdown"); !ok {
			t.Errorf("plain prose flagged as markdown: %q", text)
		}
	}
}

func TestBrevityHasALimit(t *testing.T) {
	short := judge(experiments.TestCase{}, "Vienna.")
	if ok, _ := scoreOf(short, "brief"); !ok {
		t.Error("a one-word answer was judged not brief")
	}
	long := judge(experiments.TestCase{}, strings.Repeat("word ", briefWords+10))
	if ok, _ := scoreOf(long, "brief"); ok {
		t.Errorf("%d words passed a %d-word limit", briefWords+10, briefWords)
	}
}

func TestExpectationsAreCaseInsensitive(t *testing.T) {
	c := experiments.TestCase{Expected: map[string]any{"contains": "Thursday"}}
	if ok, _ := scoreOf(judge(c, "You said thursday."), "contains"); !ok {
		t.Error("a lowercase match was missed")
	}
	if ok, _ := scoreOf(judge(c, "You said Friday."), "contains"); ok {
		t.Error("the wrong day was accepted")
	}

	// The isolation case: knowing the answer is the failure.
	a := experiments.TestCase{Expected: map[string]any{"absent": "Seth"}}
	if ok, _ := scoreOf(judge(a, "Your name is Seth."), "absent"); ok {
		t.Error("a fresh conversation knew something it was never told, and passed")
	}
	if ok, _ := scoreOf(judge(a, "I don't know your name."), "absent"); !ok {
		t.Error("not knowing was marked a failure")
	}
}

func TestEmptyRepliesFail(t *testing.T) {
	for _, text := range []string{"", "   ", "\n"} {
		if ok, _ := scoreOf(judge(experiments.TestCase{}, text), "answered"); ok {
			t.Errorf("an empty reply (%q) counted as answered", text)
		}
	}
}

func TestSuiteParses(t *testing.T) {
	suite, err := experiments.LoadSuite("evals/smoke.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(suite.TestCases) == 0 {
		t.Fatal("the suite has no cases")
	}
	for _, c := range suite.TestCases {
		in, err := decodeInput(c.Input)
		if err != nil {
			t.Fatalf("case %s: %v", c.TestCaseID, err)
		}
		if len(in.Turns) == 0 {
			t.Fatalf("case %s has no turns", c.TestCaseID)
		}
		for _, turn := range in.Turns {
			if strings.TrimSpace(turn.Say) == "" {
				t.Fatalf("case %s has an empty turn", c.TestCaseID)
			}
		}
	}
}
