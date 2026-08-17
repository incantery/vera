// The work view's rail: one goal's whole present, as choreography.
//
// The board answers "where is everything?". This answers the question
// a board cannot: what is happening to THIS piece of work, right now,
// and what changed while I was gone. Same underlying cards — a node is
// a card — read as a graph with a story attached instead of a column
// with a status.
//
// Two things it will not do. It will not report a percentage: agentic
// work is not deterministic enough for a number to mean anything, and
// a fake one is worse than none. And it will not narrate anything the
// event log did not record with a source — the story here is a
// projection of that log, not a second telling of it, so every line the
// page shows can be opened to the fork it was read from.
//
// The semantic state is derived here rather than in the page, so the
// phone and the web cannot disagree about what "Reviewing" means.
package main

import (
	"hash/fnv"
	"strconv"
	"time"

	verav1 "github.com/incantery/vera/gen/vera/v1"
	"github.com/incantery/vera/route"
)

// goalState reads the nodes and names what is happening, in the
// vocabulary the doc asks for — a state, not a pipeline stage. The
// order of these checks IS the priority: what needs a human outranks
// what is merely running, because the whole point of the view is to
// surface the one moment vera cannot get past on her own.
func goalState(nodes []task) (state, face string) {
	var (
		waiting, running, done, dropped, total int
		asking                                 *task
		building, reviewing, verifying         bool
	)
	for i := range nodes {
		n := &nodes[i]
		total++
		switch n.Col {
		case "waiting":
			waiting++
			if n.Ask != "" && asking == nil {
				asking = n
			}
		case "progress":
			running++
			switch nodeKind(n.Kind) {
			case kindReview, kindReconcile:
				reviewing = true
			case kindVerify:
				verifying = true
			default:
				building = true
			}
		case "done":
			done++
		case "dropped":
			dropped++
		}
	}
	switch {
	case total == 0:
		return "Empty", "No nodes yet."
	case asking != nil:
		return "Needs you", asking.Ask
	case waiting > 0 && running == 0:
		return "Ready for you", plural(waiting, "node") + " finished and waiting on your word."
	case building:
		return "Building", runningFace(running, reviewing || verifying)
	case reviewing:
		return "Reviewing", runningFace(running, false)
	case verifying:
		return "Verifying", runningFace(running, false)
	case done+dropped == total && done > 0:
		return "Ready", "Every node landed. " + plural(done, "node") + " accepted."
	case done+dropped == total:
		return "Closed", "Nothing left open."
	default:
		return "Waiting", plural(total-done-dropped, "node") + " not started."
	}
}

func runningFace(running int, alsoChecking bool) string {
	s := plural(running, "worker") + " on it"
	if alsoChecking {
		s += ", with a check running beside them"
	}
	return s + "."
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// goalView assembles one frame and its change hash. The hash covers
// every field the frame carries: a view that re-renders only when a
// field it does not show has moved is a view that goes stale in the
// fields it does.
func (s *server) goalView(id string) (*verav1.WatchGoalResponse, uint64) {
	now := time.Now()
	all := s.tasks.list()
	byID := make(map[string]task, len(all))
	for _, t := range all {
		byID[t.ID] = t
	}
	nodes := nodesOf(all, id)
	if len(nodes) == 0 {
		return &verav1.WatchGoalResponse{Id: id, State: "Gone",
			Face: "That goal has no cards — it may have been dropped."}, 0
	}
	// The live overlay comes from the same scan the board reads, so the
	// two surfaces cannot disagree about what a worker is doing.
	fleet := s.boardSessions(now)
	live := map[string]*taskLive{}
	for i := range nodes {
		if a := nodes[i].Agent; a != "" {
			if l := liveOverlay(fleet[a]); l != nil {
				live[nodes[i].ID] = l
			}
		}
	}

	state, face := goalState(nodes)
	resp := &verav1.WatchGoalResponse{Id: id, State: state, Face: face}
	if root, ok := byID[id]; ok {
		resp.Title = root.Title
	} else {
		resp.Title = nodes[0].Title
	}

	for _, n := range nodes {
		kind := nodeKind(n.Kind)
		gn := &verav1.GoalNode{
			Id: n.ID, Title: n.Title, Kind: kind, Col: n.Col, State: n.State,
			Face: n.Face, Deps: n.Deps, Model: n.Model, CostUsd: n.CostUSD,
			ReadOnly: readOnly(kind), Ask: n.Ask,
		}
		if t, ok := route.TierOfModel(n.Model); ok {
			gn.Tier = string(t)
		}
		if r := depsFor(n, byID); !r.Ready {
			gn.BlockedBy = append(append([]string{}, r.Blocked...), r.Dead...)
		}
		if l := live[n.ID]; l != nil {
			gn.LiveState, gn.LiveNow = l.State, l.Now
		}
		resp.Spend += n.CostUSD
		resp.Nodes = append(resp.Nodes, gn)
	}

	for _, e := range s.events.forGoal(id) {
		resp.Events = append(resp.Events, &verav1.GoalEvent{
			Seq: e.Seq, AtUnixMs: e.At.UnixMilli(), Kind: e.Kind,
			Node: e.Node, Text: e.Text,
			Src: &verav1.EventSource{Task: e.Src.Task, Run: e.Src.Run,
				Fork: e.Src.Fork, Msg: int32(e.Src.Msg), File: e.Src.File},
		})
		if e.Seq > resp.Cursor {
			resp.Cursor = e.Seq
		}
	}
	return resp, goalHash(resp)
}

// goalHash folds everything the frame shows. Missing a field here is
// not a performance choice, it is a staleness bug waiting for the one
// user who notices.
func goalHash(r *verav1.WatchGoalResponse) uint64 {
	h := fnv.New64a()
	w := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	w(r.Id, r.Title, r.State, r.Face, strconv.FormatFloat(r.Spend, 'f', 4, 64))
	for _, n := range r.Nodes {
		w(n.Id, n.Title, n.Kind, n.Col, n.State, n.Face, n.Model, n.Tier, n.Ask,
			n.LiveState, n.LiveNow, strconv.FormatFloat(n.CostUsd, 'f', 4, 64))
		w(n.Deps...)
		w(n.BlockedBy...)
	}
	// The events are append-only, so the newest sequence is the whole
	// story's fingerprint.
	w(strconv.FormatInt(r.Cursor, 10), strconv.Itoa(len(r.Events)))
	return h.Sum64()
}
