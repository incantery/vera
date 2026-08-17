package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/incantery/vera/drive"
)

// The steward's START on a card with registered ground queues a
// read-only self-start; without named ground it stays a proposal.
func TestStewardStartQueuesOnRegisteredGroundOnly(t *testing.T) {
	s := testServer(t, t.TempDir())
	now := time.Now()
	ws := t.TempDir()
	claimGround(t, s, ws)

	grounded, _ := s.tasks.capture("analyze the yard", now)
	s.tasks.mutate(grounded.ID, func(x *task) error {
		x.Workspace = ws
		x.clearProposal()
		return nil
	})
	if !s.applyStewardMove(drive.StewardMove{Verb: "start", Task: grounded.ID, Why: "its moment came."}, now) {
		t.Fatal("start on registered ground must land")
	}
	got, _ := s.tasks.get(grounded.ID)
	if got.AutoStart != "read" || got.Col != "inbox" || got.Proposal != "" {
		t.Fatalf("queued, not proposed, not yet started: %+v", got)
	}

	groundless, _ := s.tasks.capture("no ground named", now)
	s.tasks.mutate(groundless.ID, func(x *task) error { x.clearProposal(); return nil })
	if !s.applyStewardMove(drive.StewardMove{Verb: "start", Task: groundless.ID, Why: "w"}, now) {
		t.Fatal("start without ground still lands as a proposal")
	}
	got, _ = s.tasks.get(groundless.ID)
	if got.AutoStart != "" || got.ProposalKind != "start" {
		t.Fatalf("picking ground is the owner's: %+v", got)
	}

	// Foreign ground: never queued, only proposed.
	foreign, _ := s.tasks.capture("elsewhere", now)
	s.tasks.mutate(foreign.ID, func(x *task) error {
		x.Workspace = "/somewhere/unregistered"
		x.clearProposal()
		return nil
	})
	s.applyStewardMove(drive.StewardMove{Verb: "start", Task: foreign.ID, Why: "w"}, now)
	got, _ = s.tasks.get(foreign.ID)
	if got.AutoStart != "" {
		t.Fatalf("autonomy only starts on ground with a name: %+v", got)
	}
}

func TestIgniteSystemBurnsTheMarkAndStartsTheCard(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	s.claudeBin = "false" // the spawned turn fails fast; the ignition is the test
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "the compiled goal"}}},
		})
	}))
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}

	ws := t.TempDir()
	a, _ := s.tasks.capture("analyze the yard", time.Now())
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Workspace = ws
		x.AutoStart = "read"
		x.clearProposal()
		return nil
	})

	acts := igniteSystem{s}.Tick(s.world(time.Now()))
	if len(acts) != 1 || acts[0].Key != "ignite/"+a.ID || acts[0].Free {
		t.Fatalf("one spending ignition: %+v", acts)
	}
	acts[0].Run()
	got, _ := s.tasks.get(a.ID)
	if got.AutoStart != "" {
		t.Fatal("the mark burns on the attempt")
	}
	if got.Col != "progress" || got.Mode != "read" || got.Goal != "the compiled goal" {
		t.Fatalf("the card must be started in read mode: %+v", got)
	}
	// The mark burned: nothing left to propose.
	if acts := (igniteSystem{s}).Tick(s.world(time.Now())); len(acts) != 0 {
		t.Fatalf("no mark, no ignition: %+v", acts)
	}
}

func TestIgniteBurnsTheMarkEvenWhenTheStartFails(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	// No llm: igniteCard refuses silently — the mark must still burn,
	// or a broken wire becomes a per-tick billing loop the moment a
	// key appears.
	a, _ := s.tasks.capture("analyze", time.Now())
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Workspace = t.TempDir()
		x.AutoStart = "read"
		return nil
	})
	s.igniteQueued(a.ID)
	got, _ := s.tasks.get(a.ID)
	if got.AutoStart != "" || got.Col != "inbox" {
		t.Fatalf("burned but unstarted: %+v", got)
	}
}

func TestIgniteRespectsTheOwnerMovingFirst(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	a, _ := s.tasks.capture("analyze", time.Now())
	s.tasks.mutate(a.ID, func(x *task) error {
		x.AutoStart = "read"
		x.Col = "dropped"
		return nil
	})
	s.igniteQueued(a.ID)
	got, _ := s.tasks.get(a.ID)
	if got.Col != "dropped" {
		t.Fatalf("a moved card is the owner's: %+v", got)
	}
}

func TestAcceptingAStandingCardLaysTheNextPass(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	now := time.Now()
	a, _ := s.tasks.capture("water the plants", now)
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Cadence = "standing"
		x.Workspace = "/the/garden"
		x.Mode = "read"
		x.Col, x.State = "waiting", "waiting for acceptance"
		x.Proposal, x.ProposalKind = "Move to done", "done"
		return nil
	})

	req := httptest.NewRequest("POST", "/api/tasks/"+a.ID+"/act",
		strings.NewReader(`{"action":"accept"}`))
	req.SetPathValue("tid", a.ID)
	rec := httptest.NewRecorder()
	s.handleTaskAct(rec, req)
	if rec.Code != 200 {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body.String())
	}

	var closed, next *task
	for _, x := range s.tasks.list() {
		x := x
		switch {
		case x.ID == a.ID:
			closed = &x
		case x.Cadence == "standing" && x.Col == "inbox":
			next = &x
		}
	}
	if closed == nil || closed.Col != "done" {
		t.Fatalf("this pass must close: %+v", closed)
	}
	if next == nil || next.Intent != "water the plants" || next.Workspace != "/the/garden" || next.Agent != "" {
		t.Fatalf("the next pass must be laid, unassigned: %+v", next)
	}
	// The next pass spends nothing by itself: no auto-start mark.
	if next.AutoStart != "" {
		t.Fatalf("re-arming is free: %+v", next)
	}
}

func TestHoldVetoesAQueuedStart(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	a, _ := s.tasks.capture("queued work", time.Now())
	s.tasks.mutate(a.ID, func(x *task) error {
		x.AutoStart = "read"
		x.State = "inbox · queued by the steward"
		x.clearProposal()
		return nil
	})
	req := httptest.NewRequest("POST", "/api/tasks/"+a.ID+"/act", strings.NewReader(`{"action":"hold"}`))
	req.SetPathValue("tid", a.ID)
	rec := httptest.NewRecorder()
	s.handleTaskAct(rec, req)
	if rec.Code != 200 {
		t.Fatalf("hold: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := s.tasks.get(a.ID)
	if got.AutoStart != "" || got.Col != "inbox" {
		t.Fatalf("held, unstarted, still yours: %+v", got)
	}
	// The veto is on the record, and the ignite system sees nothing.
	if acts := (igniteSystem{s}).Tick(s.world(time.Now())); len(acts) != 0 {
		t.Fatalf("a held card must not ignite: %+v", acts)
	}
	// Holding twice is refused honestly.
	req = httptest.NewRequest("POST", "/api/tasks/"+a.ID+"/act", strings.NewReader(`{"action":"hold"}`))
	req.SetPathValue("tid", a.ID)
	rec = httptest.NewRecorder()
	s.handleTaskAct(rec, req)
	if rec.Code == 200 {
		t.Fatal("nothing queued — hold must refuse")
	}
}
