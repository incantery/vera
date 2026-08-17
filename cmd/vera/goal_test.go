package main

import (
	"strings"
	"testing"
	"time"
)

func node(id, kind, col string) task {
	return task{ID: id, Title: id, Kind: kind, Col: col, Root: "T-100",
		CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}
}

// The order of the state checks IS the priority: what needs a human
// outranks what is merely running, because surfacing the one moment
// vera cannot get past on her own is the whole point of the view.
func TestNeedsYouOutranksEverythingElseRunning(t *testing.T) {
	asking := node("T-101", kindImplement, "waiting")
	asking.Ask = "Should the persistence live in core or the plugin?"
	nodes := []task{
		node("T-100", kindImplement, "progress"), // a worker IS running
		asking,
		node("T-102", kindReview, "progress"),
	}
	state, face := goalState(nodes)
	if state != "Needs you" {
		t.Fatalf("a question outranks running work: %q", state)
	}
	// And the face is the actual question, not a count — the point is to
	// let the owner answer without opening anything.
	if face != asking.Ask {
		t.Fatalf("the face carries the question itself: %q", face)
	}
}

func TestStateNamesWhatIsActuallyHappening(t *testing.T) {
	cases := []struct {
		name  string
		nodes []task
		want  string
	}{
		{"building", []task{node("T-100", kindImplement, "progress")}, "Building"},
		{"reviewing", []task{node("T-100", kindReview, "progress")}, "Reviewing"},
		{"verifying", []task{node("T-100", kindVerify, "progress")}, "Verifying"},
		{"reconcile reads as reviewing", []task{node("T-100", kindReconcile, "progress")}, "Reviewing"},
		{"landed, unjudged", []task{node("T-100", kindImplement, "waiting")}, "Ready for you"},
		{"all accepted", []task{node("T-100", kindImplement, "done")}, "Ready"},
		{"nothing started", []task{node("T-100", kindImplement, "inbox")}, "Waiting"},
		{"all dropped", []task{node("T-100", kindImplement, "dropped")}, "Closed"},
		{"no nodes", nil, "Empty"},
	}
	for _, c := range cases {
		if got, _ := goalState(c.nodes); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Building wins over a review running beside it — the implementation is
// the thing the reviews are about — but the face says both are on.
func TestBuildingMentionsTheCheckRunningBeside(t *testing.T) {
	state, face := goalState([]task{
		node("T-100", kindImplement, "progress"),
		node("T-101", kindReview, "progress"),
	})
	if state != "Building" {
		t.Fatalf("the implementation is the headline: %q", state)
	}
	if !strings.Contains(face, "check running beside") {
		t.Fatalf("but the reader is told a check is running too: %q", face)
	}
	if !strings.Contains(face, "2 workers") {
		t.Fatalf("and how many are on it: %q", face)
	}
}

// No percentages, anywhere. Agentic work is not deterministic enough
// for a number to mean anything, and a confident fake one is worse than
// none at all.
func TestTheStateIsNeverANumber(t *testing.T) {
	for _, nodes := range [][]task{
		{node("T-100", kindImplement, "progress"), node("T-101", kindReview, "done")},
		{node("T-100", kindImplement, "done"), node("T-101", kindVerify, "inbox")},
	} {
		state, face := goalState(nodes)
		if strings.Contains(state+face, "%") {
			t.Fatalf("no percentage anywhere: %q / %q", state, face)
		}
	}
}

func TestGoalViewCarriesTheGraphAndTheStory(t *testing.T) {
	s := testServer(t, t.TempDir())
	now := time.Now()
	root := node("T-100", kindImplement, "waiting")
	root.Title = "Make the start screen useful"
	root.Model = "opus"
	root.CostUSD = 1.25
	rev := node("T-101", kindReview, "inbox")
	rev.Deps = []string{"T-100"}
	rev.Model = "sonnet"
	ver := node("T-102", kindVerify, "inbox")
	ver.Deps = []string{"T-999"} // gone
	for _, n := range []task{root, rev, ver} {
		if err := s.tasks.write(n); err != nil {
			t.Fatal(err)
		}
	}
	s.events.emit(evPlanDrawn, "T-100", "T-100", "Drew a 3-node graph.")
	s.events.claim(evFindingRaised, "T-100", "T-101", "The journal API leaks a lifetime.",
		sourceRef{Task: "T-101", Fork: "abc123def456", Msg: 47})
	s.events.emit(evNodeOpened, "T-200", "T-200", "a different goal entirely")

	v, sum := s.goalView("T-100")
	if sum == 0 {
		t.Fatal("a real goal hashes to something")
	}
	if v.Title != root.Title || v.State != "Ready for you" {
		t.Fatalf("the head is the root's title and the derived state: %+v", v)
	}
	if len(v.Nodes) != 3 {
		t.Fatalf("every node of the goal: %d", len(v.Nodes))
	}
	if v.Spend < 1.24 || v.Spend > 1.26 {
		t.Fatalf("spend sums the nodes: %v", v.Spend)
	}

	byID := map[string]int{}
	for i, n := range v.Nodes {
		byID[n.Id] = i
	}
	if got := v.Nodes[byID["T-101"]]; got.Tier != "mid" || !got.ReadOnly {
		t.Fatalf("a review is mid-tier and opens itself: %+v", got)
	}
	// A node waiting on something gone must say so rather than sit
	// silently — a card that waits forever without explanation is the
	// failure this surfaces.
	if got := v.Nodes[byID["T-102"]]; len(got.BlockedBy) != 1 || got.BlockedBy[0] != "T-999" {
		t.Fatalf("the dead dependency is named: %+v", got)
	}

	if len(v.Events) != 2 {
		t.Fatalf("this goal's story and nobody else's: %d", len(v.Events))
	}
	// The citation is the whole point: a claim about a worker names the
	// fork it was read from, and that fork is resumable.
	var finding *struct {
		fork string
		msg  int32
	}
	for _, e := range v.Events {
		if e.Kind == evFindingRaised {
			finding = &struct {
				fork string
				msg  int32
			}{e.Src.Fork, e.Src.Msg}
		}
	}
	if finding == nil || finding.fork != "abc123def456" || finding.msg != 47 {
		t.Fatalf("the finding carries its source: %+v", v.Events)
	}
	if v.Cursor == 0 {
		t.Fatal("the frame carries a cursor so the page can mark what is new")
	}
	_ = now
}

func TestAGoalWithNoCardsSaysSoRatherThanLookingEmpty(t *testing.T) {
	s := testServer(t, t.TempDir())
	v, _ := s.goalView("T-404")
	if v.State != "Gone" || v.Face == "" {
		t.Fatalf("a dropped goal explains itself: %+v", v)
	}
}

// A view that re-renders only when a field it does not show has moved
// is a view that goes stale in the fields it does.
func TestTheHashCoversWhatTheFrameShows(t *testing.T) {
	s := testServer(t, t.TempDir())
	base := node("T-100", kindImplement, "progress")
	if err := s.tasks.write(base); err != nil {
		t.Fatal(err)
	}
	_, before := s.goalView("T-100")

	for _, mut := range []struct {
		name string
		f    func(*task)
	}{
		{"state line", func(t *task) { t.State = "in progress · revising" }},
		{"the ask", func(t *task) { t.Ask = "which approach?" }},
		{"the routed model", func(t *task) { t.Model = "haiku" }},
		{"cost", func(t *task) { t.CostUSD = 0.42 }},
		{"the face", func(t *task) { t.Face = "a new one-line story" }},
	} {
		if _, err := s.tasks.mutate("T-100", func(t *task) error { mut.f(t); return nil }); err != nil {
			t.Fatal(err)
		}
		_, after := s.goalView("T-100")
		if after == before {
			t.Fatalf("%s moved and the hash did not — the view would go stale", mut.name)
		}
		before = after
	}

	// And an event landing is a new frame too: the story is half the view.
	s.events.emit(evNodeMoved, "T-100", "T-100", "something happened")
	if _, after := s.goalView("T-100"); after == before {
		t.Fatal("a new event must produce a new frame")
	}
}
