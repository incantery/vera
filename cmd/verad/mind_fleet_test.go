package main

import (
	"strings"
	"testing"
	"time"

	"github.com/incantery/vera/fleet"
)

func TestDescribeFleetSpeaksInTheirNouns(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	views := []fleet.View{
		{Task: &fleet.Task{ID: "a1", Project: "/x/vera", Branch: "feat", Brief: "Add dark mode. Use the tokens.", TurnEnded: now.Add(-7 * time.Minute)},
			State: fleet.Waiting, Unread: []fleet.Status{{Verb: fleet.Blocked, Text: "which palette?"}}},
		{Task: &fleet.Task{ID: "b2", Project: "/x/rook", Branch: "fix", Brief: "Fix the crash", Closed: true}, State: fleet.Closed},
		{Task: &fleet.Task{ID: "c3", Project: "/x/vera", Branch: "docs", Brief: "Write docs"}, State: fleet.Running},
	}
	got := describeFleet(views, now)
	for _, want := range []string{"Task a1 (vera, feat): Add dark mode — WAITING ON THEM for 7 minutes", "[blocked] which palette?", "Task c3", "working", "1 earlier task(s) are finished"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, never := range []string{"worktree", "pane", "incarnation", "b2"} {
		if strings.Contains(got, never) {
			t.Errorf("%q should not appear in:\n%s", never, got)
		}
	}
	if describeFleet(nil, now) != "No tasks have been started." {
		t.Error("empty")
	}
}

func TestFleetToolIsOfferedOnlyWithAFleet(t *testing.T) {
	m := &Mind{}
	var tools []map[string]any
	if m.Delegate != nil {
		tools = append(tools, m.Delegate.tool(m.Fleet != nil))
	}
	if m.Fleet != nil {
		tools = append(tools, fleetTool())
	}
	if len(tools) != 0 {
		t.Fatal("no delegate, no fleet: no tools")
	}
	fn := fleetTool()["function"].(map[string]any)
	if fn["name"] != "fleet" {
		t.Fatal(fn["name"])
	}
}

func TestDelegateReadsAsTheSmallToolBesideTheFleet(t *testing.T) {
	d := &Delegate{}
	alone := d.tool(false)["function"].(map[string]any)["description"].(string)
	beside := d.tool(true)["function"].(map[string]any)["description"].(string)
	if !strings.Contains(beside, "NOT in any repository") || !strings.Contains(beside, "fleet tool") {
		t.Errorf("beside a fleet, delegate must point repository work at the fleet:\n%s", beside)
	}
	if strings.Contains(alone, "fleet") {
		t.Error("without a fleet there is nothing to point at")
	}
}
