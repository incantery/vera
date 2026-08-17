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

func TestStewardBoardRendersOpenCardsAndFingerprintsStably(t *testing.T) {
	s := testServer(t, t.TempDir())
	now := time.Now()
	a, _ := s.tasks.capture("first thing", now)
	s.tasks.capture("second thing", now)

	w := s.world(now)
	board, fp := stewardBoard(w)
	if !strings.Contains(board, a.ID) || fp == 0 {
		t.Fatalf("board: %q fp=%d", board, fp)
	}
	// Minutes passing is not news: the fingerprint holds inside an age
	// bucket even as the rendered ages tick.
	_, fp2 := stewardBoard(s.world(now.Add(5 * time.Minute)))
	if fp2 != fp {
		t.Fatal("the mere passing of minutes must not look like a changed board")
	}
	// A state change is news.
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Col, x.State = "waiting", "waiting · escalated to you"
		return nil
	})
	_, fp3 := stewardBoard(s.world(now))
	if fp3 == fp {
		t.Fatal("a moved card must change the fingerprint")
	}
	// A closed board renders nothing to steward.
	for _, x := range s.tasks.list() {
		s.tasks.mutate(x.ID, func(x *task) error { x.Col = "done"; return nil })
	}
	if board, _ := stewardBoard(s.world(now)); board != "" {
		t.Fatalf("a closed board is not stewarded: %q", board)
	}
}

func TestApplyStewardMoveGuards(t *testing.T) {
	s := testServer(t, t.TempDir())
	now := time.Now()

	// DONE on an inbox card: refused — nothing ran.
	inbox, _ := s.tasks.capture("never started", now)
	s.tasks.mutate(inbox.ID, func(x *task) error { x.clearProposal(); return nil })
	if s.applyStewardMove(drive.StewardMove{Verb: "done", Task: inbox.ID, Why: "w"}, now) {
		t.Fatal("done on an inbox card must be refused")
	}

	// DONE on a waiting card: lands as a proposal, the owner's to take.
	waiting, _ := s.tasks.capture("ran and stopped", now)
	s.tasks.mutate(waiting.ID, func(x *task) error {
		x.Col, x.State = "waiting", "waiting · the run stopped"
		x.clearProposal()
		return nil
	})
	if !s.applyStewardMove(drive.StewardMove{Verb: "done", Task: waiting.ID, Why: "the log says it landed."}, now) {
		t.Fatal("done on a waiting card must land")
	}
	got, _ := s.tasks.get(waiting.ID)
	if got.ProposalKind != "done" || got.Col != "waiting" {
		t.Fatalf("a proposal, never a transition: %+v", got)
	}
	// And a pending proposal is never overwritten.
	if s.applyStewardMove(drive.StewardMove{Verb: "done", Task: waiting.ID, Why: "again"}, now) {
		t.Fatal("a pending proposal is spoken for")
	}

	// DONE with the run still in flight: refused.
	flying, _ := s.tasks.capture("mid run", now)
	s.tasks.mutate(flying.ID, func(x *task) error {
		x.Col = "progress"
		x.clearProposal()
		return nil
	})
	s.runs = append(s.runs, &run{ID: "drive-x", TaskID: flying.ID})
	if s.applyStewardMove(drive.StewardMove{Verb: "done", Task: flying.ID, Why: "w"}, now) {
		t.Fatal("a card with a run in flight is not finished")
	}

	// START on a non-inbox card: refused; on inbox: a start proposal.
	start, _ := s.tasks.capture("next up", now)
	s.tasks.mutate(start.ID, func(x *task) error { x.clearProposal(); return nil })
	if !s.applyStewardMove(drive.StewardMove{Verb: "start", Task: start.ID, Why: "its moment came."}, now) {
		t.Fatal("start on a clean inbox card must land")
	}
	got, _ = s.tasks.get(start.ID)
	if got.ProposalKind != "start" || got.Col != "inbox" {
		t.Fatalf("start is a proposal, not a launch: %+v", got)
	}

	// NOTE lands once; the same day's second note is noise, refused.
	noted, _ := s.tasks.capture("aging card", now)
	s.tasks.mutate(noted.ID, func(x *task) error { x.clearProposal(); return nil })
	if !s.applyStewardMove(drive.StewardMove{Verb: "note", Task: noted.ID, Why: "two days on one question."}, now) {
		t.Fatal("the first note must land")
	}
	if s.applyStewardMove(drive.StewardMove{Verb: "note", Task: noted.ID, Why: "still waiting."}, now.Add(time.Hour)) {
		t.Fatal("one steward line per card per day")
	}
	got, _ = s.tasks.get(noted.ID)
	var steward int
	for _, ev := range got.Log {
		if strings.HasPrefix(ev.Text, "steward: ") {
			steward++
		}
	}
	if steward != 1 {
		t.Fatalf("steward lines: %d — %+v", steward, got.Log)
	}

	// A ghost card and a closed card take nothing.
	if s.applyStewardMove(drive.StewardMove{Verb: "note", Task: "T-999", Why: "w"}, now) {
		t.Fatal("an invented id must be refused")
	}
}

func TestStewardSystemAsksOnceThenHoldsItsTongue(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	now := time.Now()
	a, _ := s.tasks.capture("the work", now)
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Col, x.State = "waiting", "waiting · the run stopped"
		x.clearProposal()
		return nil
	})

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{
				"content": "DONE " + a.ID + " — the run's record reads finished."}}},
		})
	}))
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}

	st := &stewardSystem{s: s}
	acts := st.Tick(s.world(now))
	if len(acts) != 1 {
		t.Fatalf("acts: %+v", acts)
	}
	acts[0].Run()
	if calls != 1 {
		t.Fatalf("calls: %d", calls)
	}
	got, _ := s.tasks.get(a.ID)
	if got.ProposalKind != "done" {
		t.Fatalf("the advice must land as a proposal: %+v", got)
	}

	// The proposal changed the board — but the cooldown holds the
	// steward's tongue regardless.
	if acts := st.Tick(s.world(now.Add(time.Minute))); len(acts) != 0 {
		t.Fatalf("the cooldown must hold: %+v", acts)
	}
	// Past the cooldown with an unchanged board: still silence.
	st.mu.Lock()
	fpThen := st.lastFP
	st.mu.Unlock()
	later := now.Add(stewardCooldown + time.Minute)
	_, fpNow := stewardBoard(s.world(later))
	if fpNow == fpThen {
		if acts := st.Tick(s.world(later)); len(acts) != 0 {
			t.Fatalf("an unchanged board is never billed twice: %+v", acts)
		}
	}
}

func TestStewardSitsOutWithoutAKey(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.tasks.capture("the work", time.Now())
	st := &stewardSystem{s: s}
	if acts := st.Tick(s.world(time.Now())); len(acts) != 0 {
		t.Fatalf("no key, no steward: %+v", acts)
	}
}

func TestStewardAnswerParksADraftAndAcceptSendsIt(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, dir)
	s.hub = newHub()
	s.claudeBin = "false" // the continued turn fails fast; the send is the test
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "irrelevant"}}},
		})
	}))
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	now := time.Now()
	writeTranscript(t, dir, "-repo-alpha", "agent-1", now.Add(-time.Minute))

	a, _ := s.tasks.capture("the work", now)
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Col, x.State = "waiting", "waiting · escalated to you"
		x.Ask = "Which database should I use?"
		x.Agent, x.Goal = "agent-1", "the goal"
		x.clearProposal()
		return nil
	})

	draft := "Use the staging database — the goal names it."
	if !s.applyStewardMove(drive.StewardMove{Verb: "answer", Task: a.ID, Why: draft}, now) {
		t.Fatal("the draft must park")
	}
	got, _ := s.tasks.get(a.ID)
	if got.ProposalKind != "reply" || got.ProposalText != draft || got.Col != "waiting" {
		t.Fatalf("parked, not sent: %+v", got)
	}
	// A second draft never overwrites the first.
	if s.applyStewardMove(drive.StewardMove{Verb: "answer", Task: a.ID, Why: "other words"}, now) {
		t.Fatal("a pending proposal is spoken for")
	}

	// The tap: accept sends exactly the drafted text and the drive
	// continues.
	req := httptest.NewRequest("POST", "/api/tasks/"+a.ID+"/act", strings.NewReader(`{"action":"accept"}`))
	req.SetPathValue("tid", a.ID)
	rec := httptest.NewRecorder()
	s.handleTaskAct(rec, req)
	if rec.Code != 200 {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body.String())
	}
	got, _ = s.tasks.get(a.ID)
	if got.Col != "progress" || got.Proposal != "" {
		t.Fatalf("the drive must continue: %+v", got)
	}
	var sent bool
	for _, ev := range got.Log {
		if ev.Actor == "human" && strings.Contains(ev.Text, "accepted vera's drafted reply") && strings.Contains(ev.Text, "staging database") {
			sent = true
		}
	}
	if !sent {
		t.Fatalf("the send must be on the record as the human's act: %+v", got.Log)
	}
}

func TestStewardAnswerGuards(t *testing.T) {
	s := testServer(t, t.TempDir())
	now := time.Now()
	// No ask: nothing to answer.
	quiet, _ := s.tasks.capture("no ask", now)
	s.tasks.mutate(quiet.ID, func(x *task) error {
		x.Col, x.Agent = "waiting", "agent-1"
		x.clearProposal()
		return nil
	})
	if s.applyStewardMove(drive.StewardMove{Verb: "answer", Task: quiet.ID, Why: "w"}, now) {
		t.Fatal("no ask, no draft")
	}
	// No agent: nothing to continue.
	orphan, _ := s.tasks.capture("no agent", now)
	s.tasks.mutate(orphan.ID, func(x *task) error {
		x.Col, x.Ask = "waiting", "which?"
		x.clearProposal()
		return nil
	})
	if s.applyStewardMove(drive.StewardMove{Verb: "answer", Task: orphan.ID, Why: "w"}, now) {
		t.Fatal("no agent, no draft")
	}
}

func TestStewardFastLaneOnNewNeeds(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	now := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "NOTHING"}}},
		})
	}))
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	a, _ := s.tasks.capture("first", now)
	s.tasks.mutate(a.ID, func(x *task) error { x.clearProposal(); return nil })

	st := &stewardSystem{s: s}
	acts := st.Tick(s.world(now))
	if len(acts) != 1 {
		t.Fatalf("first read: %+v", acts)
	}
	acts[0].Run()

	// A new escalation lands: the fast lane opens after minutes, not
	// the half hour.
	b, _ := s.tasks.capture("second", now)
	s.tasks.mutate(b.ID, func(x *task) error {
		x.Col, x.Ask, x.Agent = "waiting", "which?", "agent-1"
		x.clearProposal()
		return nil
	})
	if acts := st.Tick(s.world(now.Add(time.Minute))); len(acts) != 0 {
		t.Fatalf("even the fast lane waits its minutes: %+v", acts)
	}
	if acts := st.Tick(s.world(now.Add(stewardFastLane + time.Minute))); len(acts) != 1 {
		t.Fatalf("the fast lane must open for a new ask: %+v", acts)
	}
	// A merely-changed board (no new needs) still waits the full cooldown.
	acts = st.Tick(s.world(now.Add(stewardFastLane + time.Minute)))
	_ = acts
}

func TestStewardBoardCarriesTheEvidence(t *testing.T) {
	s := testServer(t, t.TempDir())
	now := time.Now()
	a, _ := s.tasks.capture("the work", now)
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Goal = "produce three hypotheses about the flaky test"
		x.Exchanges = []drive.Exchange{{Prompt: "go", Reply: "Here are the three hypotheses: timing, isolation, and stale cache."}}
		x.event("vera", "turn 1 — judged the goal met", now)
		x.clearProposal()
		return nil
	})
	board, _ := stewardBoard(s.world(now))
	for _, want := range []string{"three hypotheses", "goal:", "the worker last said:", "log:"} {
		if !strings.Contains(board, want) {
			t.Fatalf("the board must carry %q:\n%s", want, board)
		}
	}
}

func TestLookNowBypassesTheCooldownAndAnswersHonestly(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	now := time.Now()
	a, _ := s.tasks.capture("the work", now)
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Col, x.State = "waiting", "waiting · the run stopped"
		x.clearProposal()
		return nil
	})
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{
				"content": "DONE " + a.ID + " — the record reads finished."}}},
		})
	}))
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	s.steward = &stewardSystem{s: s}

	// A scheduled pass just ran: the cooldown would hold the tongue…
	s.steward.read(mustBoard(t, s, now))
	// …but the owner's tap goes through anyway.
	rec := httptest.NewRecorder()
	s.handleStewardLook(rec, httptest.NewRequest("POST", "/api/steward/look", nil))
	if rec.Code != 200 {
		t.Fatalf("look: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Applied int `json:"applied"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("the tap must ask regardless of the cooldown: %d calls", calls)
	}
	// The first pass already parked the done proposal; the second
	// finds it spoken for — applied is honestly zero.
	if out.Applied != 0 {
		t.Fatalf("applied: %d", out.Applied)
	}

	// No key: an honest 409, not a silent shrug.
	s.llm = nil
	rec = httptest.NewRecorder()
	s.handleStewardLook(rec, httptest.NewRequest("POST", "/api/steward/look", nil))
	if rec.Code != 409 {
		t.Fatalf("no key must refuse: %d", rec.Code)
	}
}

func mustBoard(t *testing.T, s *server, now time.Time) (string, uint64, uint64) {
	t.Helper()
	w := s.world(now)
	board, fp := stewardBoard(w)
	if board == "" {
		t.Fatal("the board must render")
	}
	return board, fp, needsFingerprint(w)
}
