package main

import (
	"testing"
	"time"
)

func byID(ts ...task) map[string]task {
	m := map[string]task{}
	for _, t := range ts {
		m[t.ID] = t
	}
	return m
}

// The heart of the design: a dependency that LANDED clears a reader
// but not a writer. That is what lets a review run while the owner is
// still deciding, so its findings reach the decision instead of
// arriving after it.
func TestLandedWorkClearsReadersButNotWriters(t *testing.T) {
	landed := task{ID: "T-100", Col: "waiting"}
	world := byID(landed)

	review := task{ID: "T-101", Kind: kindReview, Deps: []string{"T-100"}}
	if r := depsFor(review, world); !r.Ready {
		t.Fatalf("a review reads what landed, without waiting for the nod: %+v", r)
	}

	next := task{ID: "T-102", Kind: kindImplement, Deps: []string{"T-100"}}
	r := depsFor(next, world)
	if r.Ready {
		t.Fatal("a writing node waits for the owner's acceptance, not merely for the work to land")
	}
	if len(r.Blocked) != 1 || r.Blocked[0] != "T-100" {
		t.Fatalf("and it says what it waits on: %+v", r)
	}
}

func TestAcceptanceClearsEverything(t *testing.T) {
	world := byID(task{ID: "T-100", Col: "done"})
	for _, kind := range []string{kindImplement, kindReview, kindVerify} {
		n := task{ID: "T-101", Kind: kind, Deps: []string{"T-100"}}
		if r := depsFor(n, world); !r.Ready {
			t.Fatalf("an accepted dependency clears a %s node: %+v", kind, r)
		}
	}
}

// Reviewing a crashed run reviews nothing — and would spend real money
// to say so.
func TestACrashedDependencyClearsNobody(t *testing.T) {
	world := byID(task{ID: "T-100", Col: "waiting", StopErr: "the process died"})
	n := task{ID: "T-101", Kind: kindReview, Deps: []string{"T-100"}}
	if r := depsFor(n, world); r.Ready {
		t.Fatal("a review does not open on a run that stopped on an error")
	}
}

// A dependency the owner deleted must not silently promote its
// dependent to runnable: a graph that does that starts lying about
// what it verified.
func TestAGoneDependencyStrandsRatherThanClears(t *testing.T) {
	world := byID(task{ID: "T-100", Col: "dropped"})
	n := task{ID: "T-101", Kind: kindReview, Deps: []string{"T-100", "T-999"}}
	r := depsFor(n, world)
	if r.Ready {
		t.Fatal("a dropped or missing dependency never clears")
	}
	if len(r.Dead) != 2 {
		t.Fatalf("both the dropped and the missing count as dead: %+v", r)
	}
}

func TestPartialReadinessWaits(t *testing.T) {
	world := byID(
		task{ID: "T-100", Col: "done"},
		task{ID: "T-101", Col: "progress"},
	)
	n := task{ID: "T-102", Kind: kindVerify, Deps: []string{"T-100", "T-101"}}
	r := depsFor(n, world)
	if r.Ready {
		t.Fatal("all dependencies, not any")
	}
	if len(r.Blocked) != 1 || r.Blocked[0] != "T-101" {
		t.Fatalf("it names only the one still running: %+v", r)
	}
}

// The kind decides the tools, never the card — a planner that labels a
// writing task "review" gets read mode and finds it cannot write,
// which is the safe direction to be wrong in.
func TestKindPinsTheToolPolicy(t *testing.T) {
	for _, kind := range []string{kindReview, kindInvestigate, kindReconcile} {
		if !readOnly(kind) || modeFor(kind, "work") != "read" {
			t.Fatalf("%s is read-only whatever was asked for", kind)
		}
	}
	// A verify cannot mutate either, but pure read mode refuses Bash —
	// so it would open, find it has no way to run the tests, and report
	// nothing. A verification that cannot verify is worse than none,
	// because it reports success either way.
	if !readOnly(kindVerify) {
		t.Fatal("a verify still cannot change a byte")
	}
	if modeFor(kindVerify, "work") != "check" {
		t.Fatal("a verify gets the project's own build and test commands")
	}
	tools := toolsFor("check")
	if len(tools) == 0 {
		t.Fatal("the check policy has to actually carry commands")
	}
	for _, tool := range tools {
		if tool == "Edit" || tool == "Write" || tool == "MultiEdit" {
			t.Fatalf("the check policy has no teeth: %q", tool)
		}
	}
	if modeFor(kindImplement, "work") != "work" {
		t.Fatal("an implementation keeps its teeth")
	}
	if modeFor(kindImplement, "read") != "read" {
		t.Fatal("an implementation the owner asked to keep read-only stays read-only")
	}
	// A card written before kinds existed is an implementation, which
	// is what every card was.
	if nodeKind("") != kindImplement || nodeKind("nonsense") != kindImplement {
		t.Fatal("an unknown kind falls back to implement")
	}
}

func TestGraphSystemOpensOnlyWhatIsReady(t *testing.T) {
	s := testServer(t, t.TempDir())
	now := time.Now()
	ground := t.TempDir()

	w := &World{Now: now, Tasks: []task{
		{ID: "T-100", Col: "waiting", Workspace: ground},
		{ID: "T-101", Kind: kindReview, Deps: []string{"T-100"}, Col: "inbox", Workspace: ground},
		{ID: "T-102", Kind: kindImplement, Deps: []string{"T-100"}, Col: "inbox", Workspace: ground},
		{ID: "T-103", Kind: kindVerify, Deps: []string{"T-404"}, Col: "inbox", Workspace: ground},
		// Not in a graph at all, and not this system's business.
		{ID: "T-104", Col: "inbox", Workspace: ground},
	}}

	keys := map[string]bool{}
	for _, a := range (graphSystem{s}).Tick(w) {
		keys[a.Key] = true
	}
	if !keys["graph/open/T-101"] {
		t.Fatal("the review's dependency landed; it opens")
	}
	if keys["graph/open/T-102"] {
		t.Fatal("the writing node waits for acceptance")
	}
	if !keys["graph/dead/T-103"] {
		t.Fatal("a node waiting on something that does not exist is stranded, not left waiting forever")
	}
	if keys["graph/open/T-104"] {
		t.Fatal("a card with no dependencies is not in a graph")
	}
}

func TestStrandedNodeSaysWhy(t *testing.T) {
	s := testServer(t, t.TempDir())
	now := time.Now()
	if err := s.tasks.write(task{ID: "T-200", Col: "inbox", Intent: "verify it",
		Kind: kindVerify, Deps: []string{"T-404"}, Root: "T-100",
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	s.strandNode("T-200", "T-404")
	got, err := s.tasks.get("T-200")
	if err != nil {
		t.Fatal(err)
	}
	if got.Col != "dropped" {
		t.Fatalf("a node that can never run does not sit in the inbox forever: %+v", got)
	}
	// Saying so is the whole job — a card that waits silently is the
	// failure this prevents.
	last := got.Log[len(got.Log)-1]
	if last.Actor != "vera" || last.Text == "" {
		t.Fatalf("the card carries the reason: %+v", got.Log)
	}
	if len(s.events.forGoal("T-100")) != 1 {
		t.Fatal("and the goal's story carries it too")
	}
}

func TestGoalGroupsTheNodes(t *testing.T) {
	all := []task{
		{ID: "T-100", Root: "T-100", CreatedAt: time.Unix(1, 0)},
		{ID: "T-101", Root: "T-100", Deps: []string{"T-100"}, CreatedAt: time.Unix(2, 0)},
		{ID: "T-102", Root: "T-100", Deps: []string{"T-100", "T-101"}, CreatedAt: time.Unix(3, 0)},
		{ID: "T-200", CreatedAt: time.Unix(4, 0)}, // its own goal
	}
	got := nodesOf(all, "T-100")
	if len(got) != 3 {
		t.Fatalf("one goal's nodes, and only those: %d", len(got))
	}
	// Fewer dependencies first, so the root leads and the page can play
	// the shape rather than sort a table.
	if got[0].ID != "T-100" || got[2].ID != "T-102" {
		t.Fatalf("stable order, root first: %+v", got)
	}
	if goalOf(all[3]) != "T-200" {
		t.Fatal("a card outside a plan is its own goal, so every card renders the same way")
	}
}
