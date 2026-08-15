//go:build eval

// The planning corpus: ~20 asks in the owner's own words, each with
// the shape a good plan must take. This is the wall the plan prompt
// is measured against — run it before and after any planSysPrompt
// change (and bump planGen when the prompt moves):
//
//	go test -tags eval -run Plan ./drive/ -v
//
// Assertions are mechanical (the shape fields are a closed
// vocabulary); goal quality is checked as constraint survival — the
// facts the ask carried must still be in the goal a worker receives.
// Arguable rows accept every defensible shape and exist to catch
// drift, not to litigate taste. One model call per row, low effort:
// a full run costs a few cents.
package drive

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

type planCase struct {
	name     string
	ask      string
	repos    []string // the offered fleet, verbatim
	kinds    []string // accepted kinds
	where    string   // when repo is accepted: the path it must choose
	homes    []string // when new is accepted: accepted homes (nil = any)
	cadences []string // accepted cadences
	deadline string   // "" none expected · "*" required · else exact
	goalMust []string // must survive into the goal (case-insensitive)
	noSteps  bool     // a plainly single-piece ask: steps would be padding
}

// The standing fleet most rows see: two code repos and a blog.
var evalFleet = []string{
	"/w/go/src/github.com/incantery/vera",
	"/w/go/src/github.com/incantery/rook-cloud",
	"/w/projects/blog",
}

var planCorpus = []planCase{
	// ---- the ladder's own rungs ----
	{
		name:    "unnamed-mechanics-tool",
		noSteps: true,
		ask:     "I need a little tool that tells me which of my git repos have uncommitted changes",
		repos:   evalFleet, kinds: []string{"new"}, homes: []string{"code"},
		cadences: []string{"once"}, goalMust: []string{"uncommitted"},
	},
	{
		name:  "meal-prep-standing",
		ask:   "I need to figure out meal prep",
		repos: evalFleet, kinds: []string{"new"}, homes: []string{"life"},
		cadences: []string{"standing"}, goalMust: []string{"meal"},
	},
	{
		name:  "party-deadline",
		ask:   "I'm supposed to handle food for a birthday party in two weeks. About 15 people, two of them vegetarian.",
		repos: evalFleet, kinds: []string{"new"}, homes: []string{"life"},
		cadences: []string{"once"}, deadline: "2026-08-28",
		goalMust: []string{"15", "vegetarian"},
	},
	{
		name:  "homelab-watch",
		ask:   "I want to keep an eye on whether my homelab services are up",
		repos: evalFleet, kinds: []string{"new"}, homes: []string{"code"},
		cadences: []string{"standing", "once"}, goalMust: []string{"homelab"},
	},
	{
		name:  "spend-summary-arguable",
		ask:   "It bugs me that I can never tell how much I'm spending across my claude sessions in a week",
		repos: evalFleet, kinds: []string{"new", "repo"},
		where: "/w/go/src/github.com/incantery/vera", homes: []string{"code"},
		cadences: []string{"once", "standing"}, goalMust: []string{"spend"},
	},
	// ---- continuing offered ground ----
	{
		name:    "flaky-test-in-vera",
		noSteps: true,
		ask:     "fix the flaky test in the vera repo",
		repos:   evalFleet, kinds: []string{"repo"},
		where:    "/w/go/src/github.com/incantery/vera",
		cadences: []string{"once"}, goalMust: []string{"flaky"},
	},
	{
		name:    "dark-mode-blog",
		noSteps: true,
		ask:     "add dark mode to my blog",
		repos:   evalFleet, kinds: []string{"repo"}, where: "/w/projects/blog",
		cadences: []string{"once"}, goalMust: []string{"dark"},
	},
	{
		name:    "stale-readme",
		noSteps: true,
		ask:     "the readme in vera is way out of date",
		repos:   evalFleet, kinds: []string{"repo"},
		where:    "/w/go/src/github.com/incantery/vera",
		cadences: []string{"once"}, goalMust: []string{"readme"},
	},
	{
		name:    "branch-cleanup",
		noSteps: true,
		ask:     "clean up the stale branches in rook-cloud",
		repos:   evalFleet, kinds: []string{"repo"},
		where:    "/w/go/src/github.com/incantery/rook-cloud",
		cadences: []string{"once"}, goalMust: []string{"branch"},
	},
	{
		name:  "empty-fleet-still-plans",
		ask:   "build me a url shortener",
		repos: nil, kinds: []string{"new"}, homes: []string{"code"},
		cadences: []string{"once"}, goalMust: []string{"shorten"},
	},
	// ---- life, not code ----
	{
		name:  "learn-spanish",
		ask:   "I want to learn spanish",
		repos: evalFleet, kinds: []string{"new"}, homes: []string{"life"},
		cadences: []string{"standing"}, goalMust: []string{"spanish"},
	},
	{
		name:  "portland-trip",
		ask:   "plan a weekend trip to portland for sometime next month",
		repos: evalFleet, kinds: []string{"new"}, homes: []string{"life"},
		cadences: []string{"once"}, goalMust: []string{"portland"},
	},
	{
		name:  "weight-tracking",
		ask:   "I want to start tracking my weight and workouts",
		repos: evalFleet, kinds: []string{"new"}, homes: []string{"life"},
		cadences: []string{"standing"}, goalMust: []string{"weight"},
	},
	{
		name:    "ebike-research",
		noSteps: true,
		ask:     "research the best e-bike under $2000 for a tall rider",
		repos:   evalFleet, kinds: []string{"new"}, homes: []string{"life"},
		cadences: []string{"once"}, goalMust: []string{"000", "tall"},
	},
	{
		name:  "taxes-next-april",
		ask:   "I need to get my taxes together, they're due april 15",
		repos: evalFleet, kinds: []string{"new"}, homes: []string{"life"},
		cadences: []string{"once"}, deadline: "2027-04-15",
		goalMust: []string{"tax"},
	},
	// ---- the artifact recurs, the work ends ----
	{
		name:  "nightly-backup-script",
		ask:   "write a script that backs up my photos folder to s3 every night",
		repos: evalFleet, kinds: []string{"new"}, homes: []string{"code"},
		cadences: []string{"once", "standing"}, goalMust: []string{"s3", "photo"},
	},
	{
		name:  "morning-agent-summary",
		ask:   "I want a short morning summary of what my agents did overnight",
		repos: evalFleet, kinds: []string{"new", "repo"}, homes: []string{"code", "life"},
		where:    "/w/go/src/github.com/incantery/vera",
		cadences: []string{"standing", "once"}, goalMust: []string{"summary"},
	},
	// ---- the plan cannot be shaped without one answer ----
	{
		// The blog in the fleet is a defensible read of "my personal
		// site" — the nod is the confirmation gate for guesses like
		// that. Asking is equally right. Inventing a workspace is not.
		name:  "personal-site-ask",
		ask:   "I'd like my personal site to stop embarrassing me",
		repos: evalFleet, kinds: []string{"ask", "repo"},
		where: "/w/projects/blog", cadences: []string{"once", "standing"},
	},
	{
		name:  "ambiguous-app-ask",
		ask:   "get my app deployed somewhere cheap",
		repos: evalFleet, kinds: []string{"ask"},
	},
	// ---- no directory of files can hold it ----
	{
		name:  "trivia-is-not-work",
		ask:   "what's the capital of france?",
		repos: evalFleet, kinds: []string{"none"},
	},
	{
		// A reminder is honestly none (vera cannot fire one), but a
		// dated life note or a clarifying ask are defensible reads —
		// and "tomorrow" is a spoken date, not an invented one.
		name:  "dentist-arguable",
		ask:   "remind me to call my dentist tomorrow",
		repos: evalFleet, kinds: []string{"none", "new", "ask"}, homes: []string{"life"},
		cadences: []string{"once"}, deadline: "?",
	},
	{
		name:  "plants-arguable",
		ask:   "I keep forgetting to water my plants",
		repos: evalFleet, kinds: []string{"none", "new"}, homes: []string{"life"},
		cadences: []string{"standing", "once"},
	},
}

// TestPlanCorpus runs every row against the production model and
// reports each miss; the log carries every plan verbatim so a red run
// reads as a finding, not a mystery. A row that misses gets ONE fresh
// retry — arguable edges sample differently run to run, and the wall
// is for prompt regressions, not sampling noise. A row that misses
// twice fails the run, with both plans on the record.
func TestPlanCorpus(t *testing.T) {
	m := evalLLM(t)
	const today = "2026-08-14" // fixed: relative dates must be computable
	pass := 0
	for _, c := range planCorpus {
		p, misses, err := checkPlanRow(m, c, today)
		if err != nil {
			t.Errorf("%s: the wire failed: %v", c.name, err)
			continue
		}
		t.Logf("%s → %+v", c.name, p)
		if len(misses) > 0 {
			p2, misses2, err := checkPlanRow(m, c, today)
			if err != nil {
				t.Errorf("%s: the retry wire failed: %v", c.name, err)
				continue
			}
			if len(misses2) > 0 {
				for _, miss := range misses {
					t.Errorf("%s (first): %s", c.name, miss)
				}
				for _, miss := range misses2 {
					t.Errorf("%s (retry): %s", c.name, miss)
				}
				continue
			}
			t.Logf("%s held on retry → %+v", c.name, p2)
		}
		pass++
	}
	t.Logf("corpus: %d/%d rows hold", pass, len(planCorpus))
}

// checkPlanRow runs one row once and names every miss.
func checkPlanRow(m *LLM, c planCase, today string) (Plan, []string, error) {
	p, err := m.Plan(context.Background(), c.ask, c.repos, today)
	if err != nil {
		return Plan{}, nil, err
	}
	var misses []string
	miss := func(format string, args ...any) {
		misses = append(misses, fmt.Sprintf(format, args...))
	}
	if !slices.Contains(c.kinds, p.Kind) {
		miss("kind %q, accepted %v", p.Kind, c.kinds)
	}
	if p.Kind == "repo" && c.where != "" && p.Where != c.where {
		miss("where %q, wanted %q", p.Where, c.where)
	}
	if p.Kind == "new" {
		if len(c.homes) > 0 && !slices.Contains(c.homes, p.Home) {
			miss("home %q, accepted %v", p.Home, c.homes)
		}
		if p.Name == "" {
			miss("a new workspace needs a name")
		}
	}
	if p.Kind == "new" || p.Kind == "repo" {
		if len(c.cadences) > 0 && !slices.Contains(c.cadences, p.Cadence) {
			miss("cadence %q, accepted %v", p.Cadence, c.cadences)
		}
		switch c.deadline {
		case "":
			// A deadline the ask never spoke is invention.
			if p.Deadline != "" {
				miss("invented deadline %q", p.Deadline)
			}
		case "*":
			if p.Deadline == "" {
				miss("the ask names a date; the plan must carry one")
			}
		case "?": // any answer is defensible
		default:
			if p.Deadline != c.deadline {
				miss("deadline %q, wanted %q", p.Deadline, c.deadline)
			}
		}
		goal := strings.ToLower(p.Goal)
		for _, want := range c.goalMust {
			if !strings.Contains(goal, strings.ToLower(want)) {
				miss("the goal lost %q: %q", want, p.Goal)
			}
		}
	}
	if p.Kind == "none" && p.Why == "" {
		miss("a none plan stands on its why")
	}
	if p.Kind == "ask" && p.Question == "" {
		miss("an ask stands on its question")
	}
	if c.noSteps && len(p.Steps) > 0 {
		miss("a single-piece ask got padded with steps: %v", p.Steps)
	}
	return p, misses, nil
}
