package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/incantery/vera/fleet"
)

func TestRailFramesSpeakRooksVocabulary(t *testing.T) {
	now := time.Now()
	repos := []fleet.Repo{{Root: "/x/rook", Name: "rook"}, {Root: "/x/vera", Name: "vera"}}
	tasks := []fleet.View{
		{Task: &fleet.Task{ID: "a1", Project: "/x/rook", Brief: "Make the rail real. Then more.", Spawned: now.Add(-time.Hour)}, State: fleet.Waiting},
		{Task: &fleet.Task{ID: "b2", Project: "/x/rook", Brief: "Scout the feed", Spawned: now}, State: fleet.Running},
		{Task: &fleet.Task{ID: "c3", Project: "/x/vera", Brief: "Old", Closed: true}, State: fleet.Closed},
	}
	spaces, agents := railFrames(repos, tasks, "/x/vera")
	if spaces.Op != "items.push" || spaces.Params.Surface != "spaces" || len(spaces.Params.Items) != 2 {
		t.Fatalf("spaces: %+v", spaces)
	}
	rook, vera := spaces.Params.Items[0], spaces.Params.Items[1]
	if rook.State != "blocked" || rook.Subtitle != "2 tasks" || rook.Current {
		t.Errorf("rook row: %+v", rook)
	}
	if vera.State != "idle" || !vera.Current || vera.Subtitle != "" {
		t.Errorf("vera row (closed task does not count): %+v", vera)
	}
	if len(agents.Params.Items) != 2 || agents.Params.Items[0].ID != "b2" {
		t.Fatalf("agents newest first, closed omitted: %+v", agents.Params.Items)
	}
	a1 := agents.Params.Items[1]
	if a1.Title != "Make the rail real" || a1.Subtitle != "needs you · rook" || a1.State != "blocked" || !a1.Current {
		t.Errorf("a1 row: %+v", a1)
	}
	b, _ := json.Marshal(agents)
	if strings.Contains(string(b), "\n") {
		t.Error("a frame is one line")
	}
	for _, never := range []string{"pane", "worktree", "incarnation"} {
		if strings.Contains(string(b), never) {
			t.Errorf("%q leaked into the rail", never)
		}
	}
}
