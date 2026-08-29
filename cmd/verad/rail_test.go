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
	spaces, agents := railFrames(repos, tasks, "/x/vera", panes)
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
	none, _ := railFrames(repos, tasks, "", panes[:1])
	if len(none.Params.Items) != 0 {
		t.Errorf("a task's room does not make its repo a space: %+v", none.Params.Items)
	}
	if len(agents.Params.Items) != 2 || agents.Params.Items[0].ID != "b2" {
		t.Fatalf("agents newest first, closed omitted: %+v", agents.Params.Items)
	}
	a1 := agents.Params.Items[1]
	if a1.Title != "Make the rail real" || a1.Subtitle != "needs you · rook" || a1.State != "blocked" || !a1.Current {
		t.Errorf("a1 row: %+v", a1)
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
