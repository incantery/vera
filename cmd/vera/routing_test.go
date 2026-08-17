package main

import (
	"strings"
	"testing"

	"github.com/incantery/vera/drive"
)

// The routing table's one real trap, pinned: a bounded ANSWER is not a
// bounded consequence. The judge returns a single word and is the most
// expensive place in vera to be wrong — a false DONE ships broken work,
// a false ESCALATE burns a human interrupt. Routing it by output size
// would be the most expensive saving in the system.
func TestTheJudgeIsNotCheapDespiteItsTinyAnswer(t *testing.T) {
	if tierOfPart(partJudge) == tierCheap {
		t.Fatal("the judge gates a whole rerun and every escalation; it does not run on the cheapest tier")
	}
	if tierOfPart(partDigest) != tierCheap || tierOfPart(partExpand) != tierCheap {
		t.Fatal("the membrane runs on every turn and is bounded and structured — that is the cheap floor")
	}
	// Drawn once per goal, paid for by every node under it.
	if tierOfPart(partPlan) != tierStrong {
		t.Fatal("planning is the one part where spending more up front is reliably cheaper")
	}
}

func TestWorkersAreTieredByBlastRadius(t *testing.T) {
	if tierOfKind(kindImplement) != tierStrong || tierOfKind(kindReconcile) != tierStrong {
		t.Fatal("the work everything else checks, and the ruling on a disagreement, are not places to save")
	}
	if tierOfKind(kindReview) != tierMid || tierOfKind(kindInvestigate) != tierMid {
		t.Fatal("contained judgment sits at mid")
	}
	// The commands do the work; the model reads their output.
	if tierOfKind(kindVerify) != tierCheap {
		t.Fatal("a verify runs the build and reports")
	}
	// A card from before kinds existed is an implementation, and must
	// not be quietly downgraded because its Kind field is empty.
	if tierOfKind("") != tierStrong {
		t.Fatal("an unkinded card routes as the implementation it is")
	}
}

// Worker tiers are claude ALIASES, not pinned ids: an alias resolves
// against whatever the account can serve, a pinned id fails outright on
// a subscription without access to that model.
func TestWorkerRoutingUsesAliasesNotPinnedIDs(t *testing.T) {
	r := newRouter("gpt-5.6-luna", "", "", false)
	for _, kind := range []string{kindImplement, kindReview, kindVerify} {
		got := r.forKind(kind)
		if got == "" {
			t.Fatalf("%s routes somewhere", kind)
		}
		if strings.Contains(got, "-4-") || strings.Contains(got, "-5-") || strings.Contains(got, "claude-") {
			t.Fatalf("%s routed to a pinned id (%q); an account without it would simply fail", kind, got)
		}
	}
	if r.forKind(kindVerify) == r.forKind(kindImplement) {
		t.Fatal("if every tier resolves to the same model there is no routing")
	}
}

// Vera cannot know the vocabulary of an arbitrary OpenAI-compatible
// endpoint, so the honest default is the model the owner already named
// — turning routing on must not silently repoint her own calls.
func TestVerasOwnPartsDefaultToTheConfiguredModel(t *testing.T) {
	r := newRouter("gpt-5.6-luna", "", "", false)
	for _, p := range []part{partDigest, partJudge, partPlan, partSteward} {
		if got := r.forPart(p); got != "gpt-5.6-luna" {
			t.Fatalf("%s defaults to the configured model, got %q", p, got)
		}
	}
	// The overrides are how you split them, and they only move the tier
	// they name.
	r = newRouter("gpt-5.6-luna", "gpt-5-nano", "gpt-5", false)
	if r.forPart(partDigest) != "gpt-5-nano" {
		t.Fatal("--model-cheap moves the per-turn parts")
	}
	if r.forPart(partPlan) != "gpt-5" {
		t.Fatal("--model-strong moves planning")
	}
	if r.forPart(partJudge) != "gpt-5.6-luna" {
		t.Fatal("mid stays on --model — it was never overridable and should not move by accident")
	}
}

// One flag puts everything back on one model. Empty means "say
// nothing", so the CLI's own default stands for workers.
func TestRoutingOffSaysNothing(t *testing.T) {
	r := newRouter("gpt-5.6-luna", "gpt-5-nano", "gpt-5", true)
	if r.forKind(kindImplement) != "" || r.forPart(partPlan) != "" {
		t.Fatal("--no-route names no model anywhere")
	}
	var nilRouter *router
	if nilRouter.forKind(kindImplement) != "" || nilRouter.forPart(partPlan) != "" {
		t.Fatal("a fixture without a router routes nothing rather than panicking")
	}
}

func TestLLMForSwapsTheModelAndNothingElse(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.llm = &drive.LLM{Base: "http://example", Key: "k", Name: "gpt-5.6-luna", Effort: "low"}
	s.route = newRouter("gpt-5.6-luna", "gpt-5-nano", "", false)

	got := s.llmFor(partDigest)
	if got.Name != "gpt-5-nano" {
		t.Fatalf("the part decides the model: %q", got.Name)
	}
	if got.Base != "http://example" || got.Key != "k" || got.Effort != "low" {
		t.Fatalf("everything else rides along untouched: %+v", got)
	}
	// A copy, because the meter is per-call and a shared Spend closure
	// would bill the wrong ledger.
	if got == s.llm {
		t.Fatal("llmFor hands back a copy, never the shared wire")
	}
	if s.llm.Name != "gpt-5.6-luna" {
		t.Fatal("and the configured wire is left exactly as it was")
	}
}

// A system that quietly picks a cheaper model behind your work has
// stopped being trustworthy even when the pick was right.
func TestARoutedChoiceIsNeverInvisible(t *testing.T) {
	note := routeNote(kindVerify, "haiku")
	if !strings.Contains(note, "haiku") || !strings.Contains(note, string(tierCheap)) {
		t.Fatalf("the note names the model and the tier: %q", note)
	}
	if !strings.Contains(note, kindVerify) {
		t.Fatalf("and what it was routing: %q", note)
	}
	if routeNote(kindVerify, "") != "" {
		t.Fatal("nothing routed, nothing claimed")
	}
}
