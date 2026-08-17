// The work graph: a goal decomposed into nodes that know what they
// wait on.
//
// Until now a plan was a chain — each card naming the one card after
// it, every link paid for at a human acceptance boundary. A chain
// cannot say the thing the work actually needs to say: that a review
// and a security pass both read the SAME implementation and run
// beside each other, that a verification waits on both, that a
// reconcile exists only when two nodes disagree. So the link becomes a
// set: a node names its dependencies, and the shape stops being a line.
//
// A node IS a card. That is the whole trick — cards already carry a
// log, ground, a mode, a budget, an assignment and a live overlay, and
// a node needs exactly those. Nothing new is stored; a card gains a
// kind and a list of what it waits on.
//
// Two tiers of readiness, and the distinction is vera's existing
// safety model rather than a new one:
//
//   - A dependency that reached "done" was ACCEPTED by the owner. It
//     clears anything.
//   - A dependency sitting in "waiting" has LANDED but not been judged.
//     It clears only read-only dependents — reviews and verifications,
//     which cannot mutate anything and whose worst case is bounded
//     spend.
//
// That second tier is what makes the review pattern work. The reviews
// run while the owner is still deciding, so their findings reach the
// decision instead of arriving after it. Anything that could WRITE
// still waits for the nod.
package main

import (
	"sort"
	"strings"
	"time"

	"github.com/incantery/vera/route"
	"github.com/incantery/vera/transcript"
)

// The node kinds and the read-only rule live in the route package —
// the same table the ladder measures, so the product and its
// measurement can never disagree about what a kind is worth.
const (
	kindImplement   = route.KindImplement
	kindInvestigate = route.KindInvestigate
	kindReview      = route.KindReview
	kindVerify      = route.KindVerify
	kindReconcile   = route.KindReconcile
)

func nodeKind(s string) string { return route.NormalizeKind(s) }

// readOnly answers whether a node may open without the owner. It is a
// question about the KIND, never about what a model proposed — a
// planner that labels a writing task "review" gets read-mode tools and
// discovers it cannot write, which is the safe direction to be wrong in.
func readOnly(kind string) bool { return route.ReadOnly(kind) }

// modeFor is the tool policy a kind earns. Read kinds cannot mutate
// whatever the card asked for, and that pin is what makes automatic
// opening defensible — but "cannot mutate" is not the same as "has no
// tools". A verify node's whole job is to run the build and the tests
// and say what happened, so it gets the check policy: the project's own
// commands, no edits. Everything else that reads gets no tools at all.
func modeFor(kind, asked string) string {
	if nodeKind(kind) == kindVerify {
		return "check"
	}
	if readOnly(kind) {
		return "read"
	}
	if asked == "read" {
		return "read"
	}
	return "work"
}

// readiness is one node's answer to "may I run yet, and if not, who
// am I waiting on".
type readiness struct {
	Ready   bool
	Blocked []string // dependency ids not yet cleared
	Dead    []string // dependencies that were dropped; this can never run
}

// depsFor computes readiness against a snapshot. Missing dependencies
// — a card the owner deleted — are treated as dead rather than
// cleared: silently promoting an orphan to runnable is how a graph
// starts lying about what it verified.
func depsFor(t task, byID map[string]task) readiness {
	var r readiness
	wantAccepted := !readOnly(t.Kind)
	for _, id := range t.Deps {
		d, ok := byID[id]
		if !ok || d.Col == "dropped" {
			r.Dead = append(r.Dead, id)
			continue
		}
		switch d.Col {
		case "done":
			// Accepted by the owner: clears anything.
		case "waiting":
			// Landed, not yet judged. Clears read-only dependents only —
			// and never one that stopped on an error, since a review of a
			// crashed run reviews nothing.
			if wantAccepted || d.StopErr != "" {
				r.Blocked = append(r.Blocked, id)
			}
		default:
			r.Blocked = append(r.Blocked, id)
		}
	}
	r.Ready = len(r.Blocked) == 0 && len(r.Dead) == 0
	return r
}

// goalOf names the graph a card belongs to. Root is set when a plan
// lays the graph down; a card born outside one is its own goal, which
// keeps every card renderable by the same work view.
func goalOf(t task) string {
	if t.Root != "" {
		return t.Root
	}
	return t.ID
}

// nodesOf collects one goal's cards in a stable order — dependencies
// before dependents where the graph says so, id order otherwise. The
// work view draws from this, so the order must not shuffle between
// two reads of an unchanged board.
func nodesOf(tasks []task, goal string) []task {
	var out []task
	for _, t := range tasks {
		if goalOf(t) == goal {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := len(out[i].Deps), len(out[j].Deps)
		if di != dj {
			return di < dj
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// ---- the system ----

// graphSystem opens the nodes whose moment came. Both tiers end in
// the same act — ignite the card — because both already carry the
// owner's permission, just from different places: a read-only node
// carries it from its kind, and a writing node carries it from the
// acceptance that cleared its last dependency. That second one is not
// a new liberty; it is exactly what the chain did when accepting a
// card started the card it named. The engine's dedupe key is the node
// itself, so a tick that fires twice opens it once.
type graphSystem struct{ s *server }

func (graphSystem) Name() string { return "graph" }

func (g graphSystem) Tick(w *World) []Action {
	byID := make(map[string]task, len(w.Tasks))
	for _, t := range w.Tasks {
		byID[t.ID] = t
	}
	var out []Action
	for _, t := range w.Tasks {
		if t.Col != "inbox" || len(t.Deps) == 0 || t.Workspace == "" {
			continue
		}
		r := depsFor(t, byID)
		if len(r.Dead) > 0 {
			id, dead := t.ID, strings.Join(r.Dead, ", ")
			out = append(out, Action{
				Key: "graph/dead/" + id, TaskID: id, Free: true,
				Reason: "a dependency is gone; this node can never run",
				Run:    func() { g.s.strandNode(id, dead) },
			})
			continue
		}
		if !r.Ready {
			continue
		}
		id, kind := t.ID, nodeKind(t.Kind)
		out = append(out, Action{
			Key: "graph/open/" + id, TaskID: id,
			Reason: "dependencies cleared — open the " + kind + " node",
			Run:    func() { g.s.openNode(id, kind) },
		})
	}
	return out
}

// openNode starts a node whose dependencies cleared. Every guard is
// re-checked against the store: the world it was proposed from is a
// tick old and the owner may have moved first. The mode comes from
// the kind, not from the card — a review runs read-only even if the
// plan that wrote it asked for teeth.
func (s *server) openNode(id, kind string) {
	defer s.hub.notify()
	now := time.Now()
	t, err := s.tasks.get(id)
	if err != nil || t.Col != "inbox" {
		return
	}
	// The kind rides in from the tick for the log line, but the card is
	// the authority: if it changed since, the fresh one decides the
	// tools. Trusting the argument here is how a node that grew teeth
	// gets opened read-only, or worse, the other way around.
	kind = nodeKind(t.Kind)
	byID := map[string]task{}
	for _, x := range s.tasks.list() {
		byID[x.ID] = x
	}
	if !depsFor(t, byID).Ready {
		return
	}
	why := "its dependencies cleared"
	if !readOnly(kind) {
		why = "the piece it waited on was accepted"
	}
	s.events.emit(evNodeOpened, goalOf(t), id,
		"Opening the "+kind+" node — "+why+": "+transcript.Snip(t.Intent, 80))
	s.igniteCard(id, modeFor(kind, t.Mode), why, now)
}

// strandNode records a node that can never run because something it
// waited on was dropped. Saying so is the whole job: a card that waits
// forever without explanation is the failure mode this prevents.
func (s *server) strandNode(id, dead string) {
	defer s.hub.notify()
	now := time.Now()
	t, err := s.tasks.mutate(id, func(t *task) error {
		if t.Col != "inbox" {
			return errBadID
		}
		t.Col, t.State = "dropped", "dropped · a dependency is gone"
		t.Face = "Cannot run: it waited on " + dead + ", which is gone."
		t.clearProposal()
		t.event("vera", "stranded — it waited on "+dead+", which is gone", now)
		return nil
	})
	if err != nil {
		return
	}
	s.events.emit(evNodeLanded, goalOf(t), id,
		"Stranded: it waited on "+dead+", which is gone.")
}
