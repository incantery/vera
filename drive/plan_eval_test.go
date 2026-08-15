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
		name:  "unnamed-mechanics-tool",
		ask:   "I need a little tool that tells me which of my git repos have uncommitted changes",
		repos: evalFleet, kinds: []string{"new"}, homes: []string{"code"},
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
		name:  "flaky-test-in-vera",
		ask:   "fix the flaky test in the vera repo",
		repos: evalFleet, kinds: []string{"repo"},
		where:    "/w/go/src/github.com/incantery/vera",
		cadences: []string{"once"}, goalMust: []string{"flaky"},
	},
	{
		name:  "dark-mode-blog",
		ask:   "add dark mode to my blog",
		repos: evalFleet, kinds: []string{"repo"}, where: "/w/projects/blog",
		cadences: []string{"once"}, goalMust: []string{"dark"},
	},
	{
		name:  "stale-readme",
		ask:   "the readme in vera is way out of date",
		repos: evalFleet, kinds: []string{"repo"},
		where:    "/w/go/src/github.com/incantery/vera",
		cadences: []string{"once"}, goalMust: []string{"readme"},
	},
	{
		name:  "branch-cleanup",
		ask:   "clean up the stale branches in rook-cloud",
		repos: evalFleet, kinds: []string{"repo"},
		where:    "/w/go/src/github.com/incantery/rook-cloud",
		cadences: []string{"once"}, goalMust: []string{"branch"},
	},
	{
		name:  "empty-fleet-still-plans",
		ask:   "build me a url shortener",
		repos: nil, kinds: []string{"new"}, homes: []string{"code"},
		cadences: []string{"once"}, goalMust: []string{"shortener"},
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
		name:  "ebike-research",
		ask:   "research the best e-bike under $2000 for a tall rider",
		repos: evalFleet, kinds: []string{"new"}, homes: []string{"life"},
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
	// ---- no directory of files can hold it ----
	{
		name:  "trivia-is-not-work",
		ask:   "what's the capital of france?",
		repos: evalFleet, kinds: []string{"none"},
	},
	{
		name:  "dentist-arguable",
		ask:   "remind me to call my dentist tomorrow",
		repos: evalFleet, kinds: []string{"none", "new"}, homes: []string{"life"},
		cadences: []string{"once"},
	},
	{
		name:  "plants-arguable",
		ask:   "I keep forgetting to water my plants",
		repos: evalFleet, kinds: []string{"none", "new"}, homes: []string{"life"},
		cadences: []string{"standing", "once"},
	},
}

// TestPlanCorpus runs every row once against the production model and
// reports each miss; the run fails if any row misses, and the log
// carries every plan verbatim so a red run reads as a finding, not a
// mystery.
func TestPlanCorpus(t *testing.T) {
	m := evalLLM(t)
	const today = "2026-08-14" // fixed: relative dates must be computable
	pass := 0
	for _, c := range planCorpus {
		p, err := m.Plan(context.Background(), c.ask, c.repos, today)
		if err != nil {
			t.Errorf("%s: the wire failed: %v", c.name, err)
			continue
		}
		t.Logf("%s → %+v", c.name, p)
		ok := true
		miss := func(format string, args ...any) {
			ok = false
			t.Errorf(c.name+": "+format, args...)
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
		if p.Kind != "none" {
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
		if ok {
			pass++
		}
	}
	t.Logf("corpus: %d/%d rows hold", pass, len(planCorpus))
}
