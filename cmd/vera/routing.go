// Routing: which model runs which piece of work.
//
// A human picks a model once. They set it at the start of a session
// and never pick again, because re-picking mid-task is friction nobody
// pays. That is the whole opening: vera picks per node, every node, and
// the gap between the cheapest and the strongest tier is roughly ten
// times the price per token. Routing is not a micro-optimization here;
// it is the difference between an orchestrator that costs more than the
// human it replaces and one that costs less.
//
// Two surfaces, one idea. Vera's OWN calls — the judge, the membrane,
// the planner, the steward — run against whatever OpenAI-compatible
// endpoint she was pointed at. The WORKERS are claude subprocesses.
// Neither knows the other's model names, so both route through the
// same three tiers and each resolves its own vocabulary.
//
// The tier table and the reasoning behind it live in package route,
// because the ladder measures that table and a second copy here would
// be free to drift from it. This file is the mechanics only.
//
// Nothing here is silent. Every routed choice lands on the card's log
// and in the goal's story, because a system that quietly downgrades the
// model behind your work has stopped being trustworthy even when it is
// right.
package main

import (
	"strings"

	"github.com/incantery/vera/drive"
	"github.com/incantery/vera/route"
)

// The tier table itself lives in the route package, so the ladder can
// measure the same table the product runs. What stays here is the
// mechanics: resolving a tier into a concrete model on each of the two
// endpoints, and making every choice visible.
type tier = route.Tier
type part = route.Part

const (
	tierCheap  = route.Cheap
	tierMid    = route.Mid
	tierStrong = route.Strong
)

const (
	partDigest  = route.PartDigest
	partExpand  = route.PartExpand
	partCompile = route.PartCompile
	partSuggest = route.PartSuggest
	partJudge   = route.PartJudge
	partSteward = route.PartSteward
	partPlan    = route.PartPlan
)

func tierOfPart(p part) tier   { return route.OfPart(p) }
func tierOfKind(k string) tier { return route.OfKind(k) }

// A router resolves tiers into the two model vocabularies. Both maps
// are complete by construction — newRouter fills every tier — so a
// lookup never falls through to an empty model name.
type router struct {
	off    bool
	worker map[tier]string // claude's vocabulary, for the subprocess
	vera   map[tier]string // the configured endpoint's, for vera's own calls
}

// newRouter builds the table. Vera's own parts default to the single
// configured model at EVERY tier — she cannot know the vocabulary of an
// arbitrary OpenAI-compatible endpoint, so the honest default is the
// model the owner already named, and the overrides are opt-in. That
// also means turning routing on changes what the workers cost and
// nothing about what vera's own calls cost, until you say otherwise.
func newRouter(model, cheap, strong string, off bool) *router {
	pick := func(override string) string {
		if strings.TrimSpace(override) != "" {
			return strings.TrimSpace(override)
		}
		return model
	}
	return &router{
		off: off,
		worker: map[tier]string{
			tierCheap:  route.WorkerAlias[tierCheap],
			tierMid:    route.WorkerAlias[tierMid],
			tierStrong: route.WorkerAlias[tierStrong],
		},
		vera: map[tier]string{
			tierCheap:  pick(cheap),
			tierMid:    model,
			tierStrong: pick(strong),
		},
	}
}

// forKind names the claude model a worker node earns. Empty means "say
// nothing" — the CLI's own default stands, which is what routing-off
// and an unknown router both mean.
func (r *router) forKind(kind string) string {
	if r == nil || r.off {
		return ""
	}
	return r.worker[tierOfKind(kind)]
}

// forPart names the model one of vera's own parts earns.
func (r *router) forPart(p part) string {
	if r == nil || r.off {
		return ""
	}
	return r.vera[tierOfPart(p)]
}

// llmFor is the wire for one of vera's parts: a copy of the configured
// LLM with this part's model swapped in. A copy because the meter is
// per-call and a shared Spend closure would bill the wrong ledger.
// Routing off, or a router that has nothing to say, leaves the
// configured model exactly as it was.
func (s *server) llmFor(p part) *drive.LLM {
	ll := *s.llm
	if name := s.route.forPart(p); name != "" {
		ll.Name = name
	}
	return &ll
}

// routeNote is the line a card wears so a routed choice is never
// invisible. A system that quietly picks a cheaper model behind your
// work has stopped being trustworthy even when the pick was right.
func routeNote(kind, model string) string {
	if model == "" {
		return ""
	}
	return "routed the " + nodeKind(kind) + " node to " + model +
		" (" + string(tierOfKind(kind)) + ")"
}
