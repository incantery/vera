package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/mux"
)

func TestRailFramesSpeakRooksVocabulary(t *testing.T) {
	now := time.Now()
	repos := []fleet.Repo{{Root: "/x/rook", Name: "rook"}, {Root: "/x/vera", Name: "vera"}}
	tasks := []fleet.View{
		{Task: &fleet.Task{ID: "a1", Project: "/x/rook", Brief: "Make the rail real. Then more.", Spawned: now.Add(-time.Hour)}, State: fleet.Waiting},
		{Task: &fleet.Task{ID: "b2", Project: "/x/rook", Brief: "Scout the feed", Spawned: now, Session: "rook--vera-b2"}, State: fleet.Running},
		{Task: &fleet.Task{ID: "c3", Project: "/x/vera", Brief: "Old", Closed: true}, State: fleet.Closed},
	}
	// Rook holds a workspace on each checkout — plus a task's room,
	// which is a sibling dir and claims nothing for the repo.
	panes := []mux.Pane{
		{ID: mux.ID{Session: "rook--vera-b2"}, Path: "/x/rook--vera-b2"},
		{ID: mux.ID{Session: "main"}, Path: "/x/rook/mux/src"},
		{ID: mux.ID{Session: "v"}, Path: "/x/vera"},
	}
	spaces, agents := railFrames(repos, tasks, "/x/vera", "v", panes)
	if spaces.Op != "items.push" || spaces.Params.Surface != "spaces" || len(spaces.Params.Items) != 2 {
		t.Fatalf("spaces: %+v", spaces)
	}
	rook, vera := spaces.Params.Items[0], spaces.Params.Items[1]
	if rook.State != "blocked" || rook.Subtitle != "2 tasks" || rook.Current || rook.Workspace != "main" {
		t.Errorf("rook row: %+v", rook)
	}
	if vera.State != "idle" || !vera.Current || vera.Subtitle != "" || vera.Workspace != "v" {
		t.Errorf("vera row (closed task does not count): %+v", vera)
	}
	// A repo with no workspace open is not a space.
	none, _ := railFrames(repos, tasks, "", "", panes[:1])
	if len(none.Params.Items) != 0 {
		t.Errorf("a task's room does not make its repo a space: %+v", none.Params.Items)
	}
	if len(agents.Params.Items) != 2 || agents.Params.Items[0].ID != "b2" {
		t.Fatalf("agents newest first, closed omitted: %+v", agents.Params.Items)
	}
	a1 := agents.Params.Items[1]
	if a1.Title != "Make the rail real" || a1.Subtitle != "needs you · rook" || a1.State != "blocked" {
		t.Errorf("a1 row: %+v", a1)
	}
	// Nobody is standing in an agent's room — the person is in `v`,
	// which is the vera checkout's own workspace — so no agent row is
	// current. Wanting the person is said in the state, not here.
	for _, it := range agents.Params.Items {
		if it.Current {
			t.Errorf("no agent row is current when the person is not in one: %+v", it)
		}
	}
	// The row claims the workspace its session runs in, in rook's
	// vocabulary, so rook drops the pane it found there instead of
	// listing the agent twice.
	if b2 := agents.Params.Items[0]; b2.Workspace != "rook--vera-b2" {
		t.Errorf("b2 claims its workspace: %+v", b2)
	}
	b, _ := json.Marshal(agents)
	if !strings.Contains(string(b), `"workspace":"rook--vera-b2"`) {
		t.Errorf("workspace rides the frame: %s", b)
	}
	if strings.Contains(string(b), "\n") {
		t.Error("a frame is one line")
	}
	for _, never := range []string{"pane", "worktree", "incarnation"} {
		if strings.Contains(string(b), never) {
			t.Errorf("%q leaked into the rail", never)
		}
	}
}

func TestRailResetPushesAgain(t *testing.T) {
	r := &rail{poke: make(chan struct{}, 1)}
	r.last = [2][]byte{[]byte("a"), []byte("b")}
	r.Reset()
	if r.last[0] != nil || r.last[1] != nil {
		t.Errorf("Reset keeps what was pushed: %q", r.last)
	}
	select {
	case <-r.poke:
	default:
		t.Error("Reset does not poke the publisher")
	}
}

// The click and the next push have to agree, or the panel looks inert:
// rook sends a click to the workspace the row names and moves its
// cursor there, and the next push puts the cursor back where the
// producer says `current` is. So `current` is the room the person is
// standing in — after a click, the row they clicked — and never a
// second thing said in the same field.
func TestAgentRowCurrentIsWhereThePersonStands(t *testing.T) {
	now := time.Now()
	repos := []fleet.Repo{{Root: "/x/rook", Name: "rook"}}
	tasks := []fleet.View{
		{Task: &fleet.Task{ID: "a1", Project: "/x/rook", Brief: "One", Spawned: now.Add(-time.Hour), Session: "rook--vera-a1"}, State: fleet.Waiting},
		{Task: &fleet.Task{ID: "b2", Project: "/x/rook", Brief: "Two", Spawned: now, Session: "rook--vera-b2"}, State: fleet.Running},
	}
	panes := []mux.Pane{
		{ID: mux.ID{Session: "rook--vera-a1"}, Path: "/x/rook--vera-a1"},
		{ID: mux.ID{Session: "rook--vera-b2"}, Path: "/x/rook--vera-b2"},
	}
	// The person clicked a1's row: rook went to that room, and the
	// push that follows has to leave the cursor there.
	_, agents := railFrames(repos, tasks, "/x/rook", "rook--vera-a1", panes)
	byID := map[string]railItem{}
	for _, it := range agents.Params.Items {
		byID[it.ID] = it
	}
	if !byID["a1"].Current {
		t.Errorf("the room the person is in is the current row: %+v", agents.Params.Items)
	}
	// a1 is blocked — it wants the person — and that is still said in
	// the state and the word, not by taking the cursor.
	if byID["b2"].Current {
		t.Errorf("only one row is current: %+v", agents.Params.Items)
	}
	if byID["a1"].State != "blocked" || byID["a1"].Subtitle != "needs you · rook" {
		t.Errorf("what wants the person is on the row: %+v", byID["a1"])
	}
	// Standing in the vera chat's own workspace is standing in no
	// agent's room.
	_, none := railFrames(repos, tasks, "", "main", panes)
	for _, it := range none.Params.Items {
		if it.Current {
			t.Errorf("no row is current from outside every room: %+v", it)
		}
	}
}

// A row names a workspace so that clicking it goes there. A room the
// mux no longer holds is not a place to go, and claiming it would also
// fold rook's own row for that name into a task that has left.
func TestAgentRowClaimsOnlyARoomThatIsStillThere(t *testing.T) {
	now := time.Now()
	repos := []fleet.Repo{{Root: "/x/rook", Name: "rook"}}
	tasks := []fleet.View{
		{Task: &fleet.Task{ID: "a1", Project: "/x/rook", Brief: "Here", Spawned: now, Session: "rook--vera-a1"}, State: fleet.Running},
		{Task: &fleet.Task{ID: "b2", Project: "/x/rook", Brief: "Gone", Spawned: now.Add(-time.Hour), Session: "rook--vera-b2"}, State: fleet.Gone},
	}
	panes := []mux.Pane{{ID: mux.ID{Session: "rook--vera-a1"}, Path: "/x/rook--vera-a1"}}
	_, agents := railFrames(repos, tasks, "", "", panes)
	byID := map[string]railItem{}
	for _, it := range agents.Params.Items {
		byID[it.ID] = it
	}
	if byID["a1"].Workspace != "rook--vera-a1" {
		t.Errorf("a room that is there is claimed: %+v", byID["a1"])
	}
	if byID["b2"].Workspace != "" {
		t.Errorf("a room that is gone is not claimed: %+v", byID["b2"])
	}
	if byID["b2"].Subtitle != "gone · rook" {
		t.Errorf("the row still says what became of it: %+v", byID["b2"])
	}
	// A mux that did not answer is not every room being gone: the
	// claims stand and the next push corrects them.
	_, blind := railFrames(repos, tasks, "", "", nil)
	for _, it := range blind.Params.Items {
		if it.Workspace == "" {
			t.Errorf("an empty pane table takes no claim away: %+v", it)
		}
	}
}
