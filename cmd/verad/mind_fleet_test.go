package main

import (
	"context"
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
		if defs[i].Function.Name != want {
			t.Fatalf("definition %d is %v, want %s", i, defs[i].Function.Name, want)
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
	if got := f.Scope([]byte(`{"action":"stop","task":"a1"}`)); got != "stop" {
		t.Errorf("the verb an \"always\" is about is %q", got)
	}
	if got := f.Scope([]byte(`not json`)); got != "" {
		t.Errorf("unreadable arguments produced a scope: %q", got)
	}
}

// definitionJSON is a definition as the golden files were written:
// through a map, so the schema's keys come out in the one order a
// comparison can rely on. The registry keeps a schema's own byte order
// and JSON objects have none, so normalising is what makes this a test
// about the definitions rather than about gofmt.
func definitionJSON(d tool.Definition) ([]byte, error) {
	var params any
	if len(d.Function.Parameters) > 0 {
		if err := json.Unmarshal(d.Function.Parameters, &params); err != nil {
			return nil, err
		}
	}
	return json.Marshal(map[string]any{
		"type": d.Type,
		"function": map[string]any{
			"name":        d.Function.Name,
			"description": d.Function.Description,
			"parameters":  params,
		},
	})
}

// The definitions are the one thing about a tool the model ever sees,
// and these two moved from hand-written maps into the registry. Byte
// for byte, or the move changed the agent.
func TestDefinitionsAreUnchanged(t *testing.T) {
	reg := tool.NewRegistry(
		&DelegateTool{Delegate: &Delegate{}, WithFleet: true},
		&FleetTool{Fleet: &fleet.Fleet{}},
	)
	defs := reg.Definitions()
	golden := []string{"delegate.json", "fleet.json"}
	for i, name := range golden {
		want, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := definitionJSON(defs[i])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != strings.TrimRight(string(want), "\n") {
			t.Errorf("%s changed:\n got %s\nwant %s", name, got, want)
		}
	}

	// And the delegate on its own, which is what a Vera with no
	// multiplexer sends.
	alone := tool.NewRegistry(&DelegateTool{Delegate: &Delegate{}}).Definitions()
	want, err := os.ReadFile(filepath.Join("testdata", "delegate_alone.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := definitionJSON(alone[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.TrimRight(string(want), "\n") {
		t.Errorf("delegate_alone.json changed:\n got %s\nwant %s", got, want)
	}
}

// The repository in front of them is the harness's to know, not the
// model's to say, and it arrives on the Handle. Without it a start
// with no project named has to ask which one.
func TestAStartWithNoProjectUsesWhatTheHandleKnows(t *testing.T) {
	f := &FleetTool{Fleet: &fleet.Fleet{}}
	args := json.RawMessage(`{"action":"start","brief":"fix it"}`)

	_, err := f.Run(context.Background(), args, tool.Handle{})
	if err == nil || !strings.Contains(err.Error(), "no repository was named") {
		t.Fatalf("a start with nothing in front of them said: %v", err)
	}

	// With a repository on the Handle it gets as far as trying to open
	// a room, which is a different failure — there is no multiplexer
	// in a test.
	said := make(chan string, 4)
	_, err = f.Run(context.Background(), args, tool.Handle{
		Status: func(text string) { said <- text },
		Values: map[string]any{tool.Cwd: "/src/rook"},
	})
	if err != nil && strings.Contains(err.Error(), "no repository was named") {
		t.Fatalf("the repository on the Handle was ignored: %v", err)
	}
	select {
	case line := <-said:
		if line != "Opening a room for that…" {
			t.Errorf("the harness said %q while it worked", line)
		}
	default:
		t.Error("nothing was said while a room was being opened")
	}
}

// A task id is what the call reached, and it is on the Result whether
// or not the call succeeded — a round that failed still says which
// task it failed on.
func TestTheFleetSaysWhichTaskItWasAbout(t *testing.T) {
	f := &FleetTool{Fleet: &fleet.Fleet{}}
	res, err := f.Run(context.Background(), json.RawMessage(`{"action":"land"}`), tool.Handle{})
	if err == nil {
		t.Fatal("a land with no task id succeeded")
	}
	if res.Meta[tool.MetaTask] != nil {
		t.Errorf("a call about no task named one: %v", res.Meta)
	}
	// An answer with no text does not reach the fleet either, and it
	// still says which task it was about.
	res, err = f.Run(context.Background(), json.RawMessage(`{"action":"answer","task":"a1"}`), tool.Handle{})
	if err == nil {
		t.Fatal("an answer with nothing to say succeeded")
	}
	if res.Meta[tool.MetaTask] != "a1" {
		t.Errorf("the task the call was about is %v", res.Meta[tool.MetaTask])
	}
}

// An "always" said to one verb is not an always said to another. The
// fleet says its Scope, so the Gate does not have to guess.
func TestAlwaysToOneVerbIsNotAlwaysToAnother(t *testing.T) {
	h, _, project := newHands(t)
	if err := h.Adopt(&FleetTool{Fleet: &fleet.Fleet{}}); err != nil {
		t.Fatal(err)
	}
	tl, _ := h.Tool("fleet")
	stop := tool.NewCall("call_1", tl, json.RawMessage(`{"action":"stop","task":"a1"}`))

	v, g := h.Decide("conv", stop)
	if v.Decision != tool.Ask {
		t.Fatalf("stopping a task was %s before anybody said always", v.Decision)
	}
	if got := g.Grant(stop).String(); got != "fleet stop" {
		t.Fatalf("an always about stopping would cover %q", got)
	}
	// Say it, the way the wire does, and stopping stops asking.
	h.Asking("call_1", g)
	go func() { _ = h.Answer(context.Background(), "call_1", tool.Always) }()
	if ok, alive := h.waitFor(context.Background(), g, stop); !ok || !alive {
		t.Fatal("the always was never taken")
	}
	if again, _ := h.Decide("conv", stop); again.Decision != tool.Allow {
		t.Errorf("stopping still asks after an always: %s", again.Decision)
	}
	// And a start, which was allowed anyway, did not become an always
	// about the fleet as a whole.
	start := tool.NewCall("call_2", tl, json.RawMessage(`{"action":"start","brief":"x","project":"`+project+`"}`))
	if got := g.Grant(start).String(); got != "fleet start" {
		t.Errorf("an always about starting would cover %q", got)
	}
}
