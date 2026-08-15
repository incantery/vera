package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/incantery/vera/drive"
	roostv1 "github.com/incantery/vera/gen/vera/v1"
)

// planWire is a fake completions endpoint that always bids the same
// fresh life workspace.
func planWire(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"content": "KIND: new\nHOME: life\nNAME: party-food\nCADENCE: once\nDEADLINE: 2026-08-28\nGOAL: Draft a menu and shopping list for the party.\nWHY: A one-off event deserves its own workspace.",
			}}},
		})
	}))
}

func TestPlanBidsAndJournals(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, dir)

	// No key: honest 409, not a fake plan.
	if _, serr := s.planCore(context.Background(), "handle the party food", time.Now()); serr == nil || serr.code != 409 {
		t.Fatalf("no llm must refuse with 409: %+v", serr)
	}

	srv := planWire(t)
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	s.planPath = filepath.Join(t.TempDir(), "plans.jsonl")

	rec, serr := s.planCore(context.Background(), "handle the party food", time.Now())
	if serr != nil {
		t.Fatalf("the bid must land: %v", serr.msg)
	}
	if rec.ID == "" || rec.Plan.Kind != "new" || rec.Plan.Name != "party-food" || rec.Plan.Deadline != "2026-08-28" {
		t.Fatalf("the whole plan comes back: %+v", rec)
	}
	b, err := os.ReadFile(s.planPath)
	if err != nil || !strings.Contains(string(b), "party-food") || !strings.Contains(string(b), planGen) {
		t.Fatalf("the bid is journaled with its generation: %v %s", err, b)
	}
}

func TestExecutePlanMakesTheWorkspaceAndRefusesNonsense(t *testing.T) {
	oldWorld := worldRoot
	defer func() { worldRoot = oldWorld }()
	worldRoot = t.TempDir()

	dir := t.TempDir()
	s := testServer(t, dir)
	srv := planWire(t)
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	s.planPath = filepath.Join(t.TempDir(), "plans.jsonl")
	s.claudeBin = "false" // the spawn's turn fails fast; the workspace and card are the test

	// The refusals: no goal, an unshown repo, a hostile name, kind none.
	if _, serr := s.executePlanCore(drive.Plan{Kind: "new", Name: "x"}, "", "", time.Now()); serr == nil || serr.code != 400 {
		t.Fatalf("a goalless plan must refuse 400: %+v", serr)
	}
	if _, serr := s.executePlanCore(drive.Plan{Kind: "repo", Where: "/nowhere", Goal: "g"}, "", "", time.Now()); serr == nil || serr.code != 400 {
		t.Fatalf("an unshown repo must refuse 400: %+v", serr)
	}
	if _, serr := s.executePlanCore(drive.Plan{Kind: "new", Home: "life", Name: "../evil", Goal: "g"}, "", "", time.Now()); serr == nil || serr.code != 400 {
		t.Fatalf("a hostile name must refuse 400: %+v", serr)
	}
	if _, serr := s.executePlanCore(drive.Plan{Kind: "none", Why: "needs a phone call", Goal: "g"}, "", "", time.Now()); serr == nil || serr.code != 409 || !strings.Contains(serr.msg, "phone call") {
		t.Fatalf("kind none must refuse with the why: %+v", serr)
	}

	// The nod: workspace born under the world's life home, git from
	// birth, the card carries vera's goal without a second compile.
	rec, serr := s.planCore(context.Background(), "handle the party food", time.Now())
	if serr != nil {
		t.Fatal(serr.msg)
	}
	tk, serr := s.executePlanCore(rec.Plan, rec.ID, "", time.Now())
	if serr != nil {
		t.Fatalf("the nod must execute: %v", serr.msg)
	}
	want := filepath.Join(worldRoot, "vera", "party-food")
	if tk.Workspace != want {
		t.Fatalf("a life workspace lands in the vera home: %s", tk.Workspace)
	}
	if _, err := os.Stat(filepath.Join(want, ".git")); err != nil {
		t.Fatal("git from birth: ", err)
	}
	if tk.Goal != rec.Plan.Goal || tk.GoalActor != "vera" || tk.Cadence != "once" || tk.Deadline != "2026-08-28" {
		t.Fatalf("the card carries the plan whole: %+v", tk)
	}
	if tk.Intent != "handle the party food" {
		t.Fatalf("the card remembers the owner's own words: %q", tk.Intent)
	}
	// The same ground twice is a refusal, not an overwrite.
	if _, serr := s.executePlanCore(rec.Plan, "", "", time.Now()); serr == nil || serr.code != 409 {
		t.Fatalf("an existing workspace must refuse 409: %+v", serr)
	}

	// A code plan lands under go/src.
	code := drive.Plan{Kind: "new", Home: "code", Name: "dirty-repos", Goal: "Build the tool.", Cadence: "once"}
	tk2, serr := s.executePlanCore(code, "", "", time.Now())
	if serr != nil {
		t.Fatal(serr.msg)
	}
	if tk2.Workspace != filepath.Join(worldRoot, "go", "src", "dirty-repos") {
		t.Fatalf("a code workspace lands under go/src: %s", tk2.Workspace)
	}
}

func TestPlanRPCsCarryTheBid(t *testing.T) {
	oldWorld := worldRoot
	defer func() { worldRoot = oldWorld }()
	worldRoot = t.TempDir()

	s := testServer(t, t.TempDir())
	srv := planWire(t)
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	s.planPath = filepath.Join(t.TempDir(), "plans.jsonl")
	s.claudeBin = "false"
	r := &veraRPC{s: s}

	resp, err := r.Plan(context.Background(), connect.NewRequest(&roostv1.PlanRequest{Text: "handle the party food"}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Msg.Id == "" || resp.Msg.Plan.Name != "party-food" {
		t.Fatalf("the RPC carries the whole bid: %+v", resp.Msg)
	}
	ex, err := r.ExecutePlan(context.Background(), connect.NewRequest(&roostv1.ExecutePlanRequest{
		Id: resp.Msg.Id, Plan: resp.Msg.Plan,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ex.Msg.TaskId, "T-") || !strings.HasSuffix(ex.Msg.Workspace, "party-food") {
		t.Fatalf("the nod answers with the card and the ground: %+v", ex.Msg)
	}
	if _, err := r.ExecutePlan(context.Background(), connect.NewRequest(&roostv1.ExecutePlanRequest{})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a nod without a plan is invalid: %v", err)
	}
}

func TestBoardDoesNotAdoptACardsOwnNewborn(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// A fresh spawn mid-run: the card claims the workspace, the worker
	// is a WORKING titled session there that has not yet been assigned.
	writeWorkingTranscript(t, dir, "newborn-ground", "worker-1", "Plan the party food", now)
	s := testServer(t, dir)
	tk := task{
		ID: "T-500", Title: "party food", Intent: "party food",
		Workspace: "/repo/newborn-ground",
		Col:       "progress", State: "in progress · a fresh agent is being born",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.tasks.write(tk); err != nil {
		t.Fatal(err)
	}
	tasks, _ := boardOf(t, s)
	if len(tasks) != 1 || tasks[0].ID != "T-500" {
		t.Fatalf("the newborn must not become a second card: %+v", tasks)
	}
}

func TestExecuteRefusesAnAskWithItsQuestion(t *testing.T) {
	s := testServer(t, t.TempDir())
	srv := planWire(t)
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	_, serr := s.executePlanCore(drive.Plan{Kind: "ask", Question: "Where does the site live?", Goal: "g"}, "", "", time.Now())
	if serr == nil || serr.code != 409 || !strings.Contains(serr.msg, "Where does the site live?") {
		t.Fatalf("an ask is not executable; the refusal carries the question: %+v", serr)
	}
}

func TestExecuteLaysStepCardsOnTheSameGround(t *testing.T) {
	oldWorld := worldRoot
	defer func() { worldRoot = oldWorld }()
	worldRoot = t.TempDir()
	s := testServer(t, t.TempDir())
	srv := planWire(t)
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	s.planPath = filepath.Join(t.TempDir(), "plans.jsonl")
	s.claudeBin = "false"

	p := drive.Plan{Kind: "new", Home: "code", Name: "pipeline", Cadence: "once",
		Goal:  "Build the collector.",
		Steps: []string{"Build the summarizer.", "Wire the delivery."}}
	first, serr := s.executePlanCore(p, "", "", time.Now())
	if serr != nil {
		t.Fatal(serr.msg)
	}
	all := s.tasks.list()
	if len(all) != 3 {
		t.Fatalf("one driving card and two planned steps: %d cards", len(all))
	}
	var steps int
	for _, tk := range all {
		if tk.ID == first.ID {
			continue
		}
		steps++
		if tk.Col != "inbox" || tk.Workspace != first.Workspace {
			t.Fatalf("a step waits as backlog on the same ground: %+v", tk)
		}
		if tk.Log[0].Actor != "vera" {
			t.Fatalf("the decomposition wears vera's name: %+v", tk.Log)
		}
	}
	if steps != 2 {
		t.Fatalf("both steps land: %d", steps)
	}
}

func TestAcceptanceChainsThePlannedSuccessor(t *testing.T) {
	oldWorld := worldRoot
	defer func() { worldRoot = oldWorld }()
	worldRoot = t.TempDir()
	s := testServer(t, t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "Do the next piece."}}},
		})
	}))
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	s.claudeBin = "false"

	ground := t.TempDir()
	now := time.Now()
	first := task{ID: "T-900", Title: "first", Intent: "first",
		Mode: "work", Workspace: ground, NextID: "T-901",
		Col: "waiting", State: "waiting for acceptance",
		ProposalKind: "done", Proposal: "Move to done",
		CreatedAt: now, UpdatedAt: now}
	step := task{ID: "T-901", Title: "wire the delivery", Intent: "wire the delivery",
		Workspace: ground, Col: "inbox", State: "inbox · planned",
		CreatedAt: now, UpdatedAt: now}
	if err := s.tasks.write(first); err != nil {
		t.Fatal(err)
	}
	if err := s.tasks.write(step); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/tasks/T-900/act", strings.NewReader(`{"action":"accept"}`))
	req.SetPathValue("tid", "T-900")
	s.handleTaskAct(rec, req)
	if rec.Code != 200 {
		t.Fatalf("accept refused: %s", rec.Body.String())
	}
	got, err := s.tasks.get("T-901")
	if err != nil {
		t.Fatal(err)
	}
	if got.Col != "progress" || got.Goal != "Do the next piece." || got.GoalActor != "vera" {
		t.Fatalf("the acceptance is the nod that starts the successor: %+v", got)
	}

	// A successor the human already moved is left exactly alone.
	parked := task{ID: "T-902", Title: "second", Intent: "second",
		Mode: "work", Workspace: ground, NextID: "T-903",
		Col: "waiting", ProposalKind: "done", Proposal: "Move to done",
		CreatedAt: now, UpdatedAt: now}
	moved := task{ID: "T-903", Title: "moved", Intent: "moved",
		Workspace: ground, Col: "dropped", CreatedAt: now, UpdatedAt: now}
	s.tasks.write(parked)
	s.tasks.write(moved)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/tasks/T-902/act", strings.NewReader(`{"action":"accept"}`))
	req.SetPathValue("tid", "T-902")
	s.handleTaskAct(rec, req)
	if got, _ := s.tasks.get("T-903"); got.Col != "dropped" {
		t.Fatalf("a human-moved successor stays where the human put it: %+v", got)
	}
}
