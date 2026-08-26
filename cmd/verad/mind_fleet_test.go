package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/incantery/mote/tool"
	"github.com/incantery/vera/fleet"
)

func TestDescribeFleetSpeaksInTheirNouns(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	views := []fleet.View{
		{Task: &fleet.Task{ID: "a1", Project: "/x/vera", Branch: "feat", Brief: "Add dark mode. Use the tokens.", TurnEnded: now.Add(-7 * time.Minute)},
			State: fleet.Waiting, Unread: []fleet.Status{{Verb: fleet.Blocked, Text: "which palette?"}}},
		{Task: &fleet.Task{ID: "b2", Project: "/x/rook", Branch: "fix", Brief: "Fix the crash", Closed: true}, State: fleet.Closed},
		{Task: &fleet.Task{ID: "c3", Project: "/x/vera", Branch: "docs", Brief: "Write docs"}, State: fleet.Running},
		{Task: &fleet.Task{ID: "d4", Project: "/x/rook", Brief: "Scout the rail"}, State: fleet.Finished, Report: "# Findings\n\nIt is a placeholder in chrome.zig.", Unread: []fleet.Status{{Verb: fleet.Done, Text: "report written"}}},
	}
	got := describeFleet(views, now)
	for _, want := range []string{"Task a1 (vera, feat): Add dark mode — WAITING ON THEM for 7 minutes", "[blocked] which palette?", "Task c3", "working", "Task d4 (rook): Scout the rail", "1 earlier task(s) are finished", "Its report:\n# Findings", "placeholder in chrome.zig"} {
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

// The fleet and the delegate are registered rather than assembled per
// exchange, so what "she has a fleet" means is what is in the
// registry — and a Vera with no multiplexer has nothing to register.
func TestFleetIsRegisteredOnlyWithAFleet(t *testing.T) {
	h, _, _ := newHands(t)
	if _, ok := h.Tool("fleet"); ok {
		t.Fatal("no fleet was adopted, so there should be no fleet tool")
	}
	if err := h.Adopt(&FleetTool{Fleet: &fleet.Fleet{}}); err != nil {
		t.Fatal(err)
	}
	tl, ok := h.Tool("fleet")
	if !ok {
		t.Fatal("the fleet tool did not survive being adopted")
	}
	if tl.Name() != "fleet" {
		t.Fatalf("tool is named %q", tl.Name())
	}
}

// Handing work away is what she should reach for before doing it
// herself, and the order the model reads them in is the order they
// are listed.
func TestHerOwnToolsComeFirst(t *testing.T) {
	h, _, _ := newHands(t)
	profile := h.Names()
	if err := h.Adopt(&DelegateTool{Delegate: &Delegate{}}, &FleetTool{Fleet: &fleet.Fleet{}}); err != nil {
		t.Fatal(err)
	}
	got := h.Names()
	if len(got) != len(profile)+2 || got[0] != "delegate" || got[1] != "fleet" {
		t.Fatalf("adopted tools are not in front: %v", got)
	}
	// And the definitions the model is sent are in the same order.
	defs := h.Definitions()
	for i, want := range []string{"delegate", "fleet"} {
		fn := defs[i]["function"].(map[string]any)
		if fn["name"] != want {
			t.Fatalf("definition %d is %v, want %s", i, fn["name"], want)
		}
	}
}

func TestDelegateReadsAsTheSmallToolBesideTheFleet(t *testing.T) {
	alone := (&DelegateTool{}).Description()
	beside := (&DelegateTool{WithFleet: true}).Description()
	if !strings.Contains(beside, "NOT in any repository") || !strings.Contains(beside, "fleet tool") {
		t.Errorf("beside a fleet, delegate must point repository work at the fleet:\n%s", beside)
	}
	if strings.Contains(alone, "fleet") {
		t.Error("without a fleet there is nothing to point at")
	}
}

// The fleet's verbs are not the same question. Everything it does adds
// or reports, except stop, which throws away work somebody did.
func TestStoppingATaskIsAskedAbout(t *testing.T) {
	h, _, project := newHands(t)
	if err := h.Adopt(&DelegateTool{Delegate: &Delegate{}}, &FleetTool{Fleet: &fleet.Fleet{}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args any
		want tool.Decision
	}{
		{map[string]any{"action": "list"}, tool.Allow},
		{map[string]any{"action": "start", "brief": "fix it", "project": project}, tool.Allow},
		{map[string]any{"action": "land", "task": "a1"}, tool.Allow},
		{map[string]any{"action": "stop", "task": "a1"}, tool.Ask},
	}
	for _, c := range cases {
		if v := decide(t, h, "conv", "fleet", c.args); v.Decision != c.want {
			t.Errorf("fleet %v → %s (%s: %s), want %s", c.args, v.Decision, v.Rule, v.Reason, c.want)
		}
	}
	// And a delegation never stops to ask: it is the thing she is for.
	if v := decide(t, h, "conv", "delegate", map[string]any{"task": "what time is it"}); v.Decision != tool.Allow {
		t.Errorf("delegate → %s (%s)", v.Decision, v.Rule)
	}
}

// A start says which repository it is about, so a rule can be written
// about where work may be started. Nothing else the fleet does names a
// path at all.
func TestOnlyAStartNamesAPath(t *testing.T) {
	f := &FleetTool{}
	if got := f.Paths([]byte(`{"action":"start","project":"/src/rook"}`)); len(got) != 1 || got[0] != "/src/rook" {
		t.Errorf("start's paths are %v", got)
	}
	if got := f.Paths([]byte(`{"action":"start"}`)); got != nil {
		t.Errorf("a start with no project named a path anyway: %v", got)
	}
	if got := f.Paths([]byte(`{"action":"stop","task":"a1"}`)); got != nil {
		t.Errorf("stop named a path: %v", got)
	}
	if got := f.Command([]byte(`{"action":"stop","task":"a1"}`)); got != "stop" {
		t.Errorf("the verb a rule keys on is %q", got)
	}
	if got := f.Command([]byte(`not json`)); got != "" {
		t.Errorf("unreadable arguments produced a command: %q", got)
	}
}

// The definitions are the one thing about a tool the model ever sees,
// and these two moved from hand-written maps into the registry. Byte
// for byte, or the move changed the agent.
func TestDefinitionsAreUnchanged(t *testing.T) {
	reg := tool.NewRegistry(
		&DelegateTool{Delegate: &Delegate{}, WithFleet: true},
		&FleetTool{Fleet: &fleet.Fleet{}},
	)
	defs := definitionMaps(reg.Definitions())
	golden := []string{"delegate.json", "fleet.json"}
	for i, name := range golden {
		want, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(defs[i])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != strings.TrimRight(string(want), "\n") {
			t.Errorf("%s changed:\n got %s\nwant %s", name, got, want)
		}
	}

	// And the delegate on its own, which is what a Vera with no
	// multiplexer sends.
	alone := definitionMaps(tool.NewRegistry(&DelegateTool{Delegate: &Delegate{}}).Definitions())
	want, err := os.ReadFile(filepath.Join("testdata", "delegate_alone.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(alone[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.TrimRight(string(want), "\n") {
		t.Errorf("delegate_alone.json changed:\n got %s\nwant %s", got, want)
	}
}
