package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/incantery/vera/drive"
)

// marathonCard lays a budgeted card parked on a rollable stop, with a
// live agent to continue on.
func marathonCard(t *testing.T, s *server, dir, stopReason string, budget, spent float64) task {
	t.Helper()
	now := time.Now()
	writeTranscript(t, dir, "-repo-alpha", "agent-1", now.Add(-time.Minute))
	a, _ := s.tasks.capture("deep review", now)
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Col, x.State = "waiting", "waiting · escalated to you"
		x.Agent, x.Goal, x.Mode = "agent-1", "review the surface", "read"
		x.BudgetUSD, x.CostUSD = budget, spent
		x.StopReason = stopReason
		x.Ask = "Should I prioritize mobile or desktop?"
		x.clearProposal()
		return nil
	})
	forceUpdatedAt(t, s, a.ID, now.Add(-time.Minute))
	got, _ := s.tasks.get(a.ID)
	return got
}

func TestDriverProposesOnlyRollableStopsWithBudgetLeft(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, dir)
	roll := marathonCard(t, s, dir, "escalated", 30, 4)
	spent := marathonCard(t, s, dir, "turns", 30, 30)
	circling := marathonCard(t, s, dir, "circling", 30, 4)
	errored := marathonCard(t, s, dir, "error", 30, 4)
	plain := marathonCard(t, s, dir, "escalated", 0, 4) // no budget: not autopilot

	acts := driverSystem{s}.Tick(s.world(time.Now()))
	ids := map[string]bool{}
	for _, a := range acts {
		if !a.Budgeted {
			t.Fatalf("a driver action rides the card's budget, not the hourly gate: %+v", a)
		}
		ids[a.TaskID] = true
	}
	if !ids[roll.ID] {
		t.Fatalf("the rollable card must be proposed: %+v", ids)
	}
	// The spent card IS proposed — its action parks it for good.
	if !ids[spent.ID] {
		t.Fatalf("the spent card must be parked by the driver: %+v", ids)
	}
	for id, why := range map[string]string{
		circling.ID: "circling stays stopped",
		errored.ID:  "errors belong to recover",
		plain.ID:    "no budget, no autopilot",
	} {
		if ids[id] {
			t.Fatalf("%s must not be driven (%s)", id, why)
		}
	}
}

func TestDriverContinuesAndTheBudgetShrinksTheRunCap(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, dir)
	s.hub = newHub()
	s.claudeBin = "false"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "irrelevant"}}},
		})
	}))
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	a := marathonCard(t, s, dir, "escalated", 30, 4)

	s.driveOn(a.ID)
	got, _ := s.tasks.get(a.ID)
	if got.Col != "progress" || !strings.Contains(got.State, "autopilot") {
		t.Fatalf("the card must roll forward: %+v", got)
	}
	var rolled bool
	for _, ev := range got.Log {
		if strings.Contains(ev.Text, "autopilot: continuing past a escalated stop") {
			rolled = true
		}
	}
	if !rolled {
		t.Fatalf("the roll must be on the record: %+v", got.Log)
	}
}

func TestDriverParksForGoodWhenTheAuthorizationIsSpent(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, dir)
	s.hub = newHub()
	s.llm = &drive.LLM{}
	a := marathonCard(t, s, dir, "turns", 20, 19.8)

	s.driveOn(a.ID)
	got, _ := s.tasks.get(a.ID)
	if got.Col != "waiting" || got.State != budgetSpentState {
		t.Fatalf("the spent card parks: %+v", got)
	}
	if !strings.Contains(got.Ask, "Raise it, take the wheel, or close") {
		t.Fatalf("the park asks the owner plainly: %q", got.Ask)
	}
	// Parked is terminal: the driver proposes nothing more for it.
	if acts := (driverSystem{s}).Tick(s.world(time.Now().Add(time.Minute))); len(acts) != 0 {
		t.Fatalf("a parked card is the owner's: %+v", acts)
	}
}

func TestDriverRespectsTheOwnerMovingFirst(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, dir)
	s.hub = newHub()
	s.llm = &drive.LLM{}
	a := marathonCard(t, s, dir, "escalated", 30, 4)
	s.tasks.mutate(a.ID, func(x *task) error { x.Col = "dropped"; return nil })
	s.driveOn(a.ID)
	got, _ := s.tasks.get(a.ID)
	if got.Col != "dropped" {
		t.Fatalf("a moved card is the owner's: %+v", got)
	}
}

func TestStewardDoesNotSteerAnAutopilotCard(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, dir)
	a := marathonCard(t, s, dir, "escalated", 30, 4)
	now := time.Now()
	if s.applyStewardMove(drive.StewardMove{Verb: "answer", Task: a.ID, Why: "draft"}, now) {
		t.Fatal("the driver owns the card while the budget lasts")
	}
	if s.applyStewardMove(drive.StewardMove{Verb: "done", Task: a.ID, Why: "w"}, now) {
		t.Fatal("no steward done on a driven card")
	}
	if !s.applyStewardMove(drive.StewardMove{Verb: "note", Task: a.ID, Why: "observing is fine."}, now) {
		t.Fatal("a note is still welcome")
	}
}

func TestStartAcceptsTheBudgetAndForcesRead(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, dir)
	s.hub = newHub()
	s.claudeBin = "false"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "the goal"}}},
		})
	}))
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	writeTranscript(t, dir, "-repo-alpha", "agent-1", time.Now().Add(-time.Minute))
	s.ln.advance("agent-1", "fork-1") // claimed: currentAgent can find it

	a, _ := s.tasks.capture("deep review", time.Now())
	req := httptest.NewRequest("POST", "/api/tasks/"+a.ID+"/start",
		strings.NewReader(`{"mode":"work","budgetUsd":25,"agentId":"agent-1"}`))
	req.SetPathValue("tid", a.ID)
	rec := httptest.NewRecorder()
	s.handleTaskStart(rec, req)
	if rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := s.tasks.get(a.ID)
	if got.BudgetUSD != 25 || got.Mode != "read" {
		t.Fatalf("budgeted and forced read: %+v", got)
	}
	var authorized bool
	for _, ev := range got.Log {
		if ev.Actor == "human" && strings.Contains(ev.Text, "autopilot: authorized $25.00") {
			authorized = true
		}
	}
	if !authorized {
		t.Fatalf("the authorization is the human's, on the record: %+v", got.Log)
	}

	// The cap refuses a blank check.
	b, _ := s.tasks.capture("too rich", time.Now())
	req = httptest.NewRequest("POST", "/api/tasks/"+b.ID+"/start",
		strings.NewReader(fmt.Sprintf(`{"budgetUsd":%d}`, 500)))
	req.SetPathValue("tid", b.ID)
	rec = httptest.NewRecorder()
	s.handleTaskStart(rec, req)
	if rec.Code != 400 {
		t.Fatalf("a $500 authorization must refuse: %d", rec.Code)
	}
}
