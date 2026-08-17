package main

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/incantery/vera/drive"
)

// The scoping contract: the machine is full of Claude sessions that
// are not vera's; the board sees none of them until they are claimed
// — by lineage, by assignment, or by registered ground.
func TestBoardIgnoresSessionsOffVerasGround(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeWorkingTranscript(t, dir, "-repo-foreign", "stranger-1", "someone else's work", now)
	s := testServer(t, dir)

	tasks, fleet := boardOf(t, s)
	if fleet["agents"] != 0 || len(tasks) != 0 {
		t.Fatalf("an unclaimed session must be invisible: fleet=%+v tasks=%+v", fleet, tasks)
	}

	// Lineage claims it: vera drove this session once, it is family.
	s.ln.advance("stranger-1", "fork-1")
	tasks, fleet = boardOf(t, s)
	if fleet["agents"] != 1 || len(tasks) != 1 {
		t.Fatalf("a lineage-known session is vera's: fleet=%+v tasks=%+v", fleet, tasks)
	}
}

func TestAssignmentClaimsASessionWithoutGround(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeWorkingTranscript(t, dir, "-repo-foreign", "agent-1", "assigned work", now)
	s := testServer(t, dir)
	a, _ := s.tasks.capture("the work", now)
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Agent = "agent-1"
		x.Col = "progress"
		return nil
	})
	_, fleet := boardOf(t, s)
	if fleet["agents"] != 1 {
		t.Fatalf("a card's assignment is a claim: %+v", fleet)
	}
}

// ---- reconcile ----

func TestReconcileFoldsAnOrphanedInFlightCard(t *testing.T) {
	s := testServer(t, t.TempDir())
	now := time.Now()
	a, _ := s.tasks.capture("the work", now.Add(-5*time.Minute))
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Col, x.State = "progress", "in progress · turn in flight"
		x.Goal = "the goal"
		x.UpdatedAt = now.Add(-5 * time.Minute)
		return nil
	})
	// mutate stamps UpdatedAt through event() only; force the age.
	forceUpdatedAt(t, s, a.ID, now.Add(-5*time.Minute))

	sys := reconcileSystem{s}
	acts := sys.Tick(s.world(now))
	if len(acts) != 1 || acts[0].Key != "reconcile/"+a.ID || !acts[0].Free {
		t.Fatalf("acts: %+v", acts)
	}
	acts[0].Run()
	got, _ := s.tasks.get(a.ID)
	if got.Col != "waiting" || !got.StopTransient || got.StopErr == "" {
		t.Fatalf("the orphan must fold to a transient stop: %+v", got)
	}

	// A truthful card proposes nothing: the fold cleared the claim.
	if acts := sys.Tick(s.world(now)); len(acts) != 0 {
		t.Fatalf("reconcile must be idempotent: %+v", acts)
	}
}

func TestReconcileLeavesLiveRunsAndFreshCardsAlone(t *testing.T) {
	s := testServer(t, t.TempDir())
	now := time.Now()
	// A card whose run is honestly in flight.
	a, _ := s.tasks.capture("live one", now)
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Col, x.Goal = "progress", "the goal"
		return nil
	})
	forceUpdatedAt(t, s, a.ID, now.Add(-5*time.Minute))
	s.runs = append(s.runs, &run{ID: "drive-1", TaskID: a.ID})
	// A card mutated moments ago: its run may not have registered yet.
	b, _ := s.tasks.capture("fresh one", now)
	s.tasks.mutate(b.ID, func(x *task) error {
		x.Col, x.Goal = "progress", "the goal"
		return nil
	})
	// An adopted card: it mirrors a session, not a run.
	c, _ := s.tasks.capture("adopted one", now)
	s.tasks.mutate(c.ID, func(x *task) error {
		x.Col, x.Adopted, x.Goal = "progress", true, "the goal"
		return nil
	})
	forceUpdatedAt(t, s, c.ID, now.Add(-5*time.Minute))

	if acts := (reconcileSystem{s}).Tick(s.world(now)); len(acts) != 0 {
		t.Fatalf("nothing here is an orphan: %+v", acts)
	}
}

// forceUpdatedAt rewrites a card's UpdatedAt — tests need age and
// mutate() stamps now.
func forceUpdatedAt(t *testing.T, s *server, id string, at time.Time) {
	t.Helper()
	x, err := s.tasks.get(id)
	if err != nil {
		t.Fatal(err)
	}
	x.UpdatedAt = at
	if err := s.tasks.write(x); err != nil {
		t.Fatal(err)
	}
}

// ---- recover ----

func stoppedCard(t *testing.T, s *server, transientStop bool, retries int, age time.Duration) task {
	t.Helper()
	a, _ := s.tasks.capture("the work", time.Now().Add(-age))
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Col, x.State = "waiting", "waiting · the run stopped"
		x.Goal = "the goal"
		x.StopErr = "the turn ran past its 10m0s budget and was killed"
		x.StopTransient = transientStop
		x.Retries = retries
		return nil
	})
	forceUpdatedAt(t, s, a.ID, time.Now().Add(-age))
	got, _ := s.tasks.get(a.ID)
	return got
}

func TestRecoverProposesOnlyTransientStopsPastBackoff(t *testing.T) {
	s := testServer(t, t.TempDir())
	now := time.Now()
	fresh := stoppedCard(t, s, true, 0, 5*time.Second)    // too fresh: backoff
	due := stoppedCard(t, s, true, 0, time.Minute)        // due for retry 1
	second := stoppedCard(t, s, true, 1, time.Minute)     // retry 2 wants 2m
	spent := stoppedCard(t, s, true, maxAutoRetries, time.Hour)
	human := stoppedCard(t, s, false, 0, time.Hour) // a judgment call, the owner's

	acts := recoverSystem{s}.Tick(s.world(now))
	keys := map[string]bool{}
	for _, a := range acts {
		keys[a.Key] = true
	}
	if !keys["recover/"+due.ID+"/1"] {
		t.Fatalf("the due card must be proposed: %+v", keys)
	}
	for id, why := range map[string]string{
		fresh.ID:  "backoff not served",
		second.ID: "second retry wants two minutes",
		spent.ID:  "the retry budget is spent",
		human.ID:  "a non-transient stop is the owner's",
	} {
		for k := range keys {
			if strings.Contains(k, "/"+id+"/") {
				t.Fatalf("%s (%s) must not be proposed", id, why)
			}
		}
	}
	// The second retry becomes due once two minutes pass.
	acts = recoverSystem{s}.Tick(s.world(now.Add(2 * time.Minute)))
	found := false
	for _, a := range acts {
		if a.Key == "recover/"+second.ID+"/2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the second retry must come due: %+v", acts)
	}
}

func TestRecoverRestartsAFreshSpawnDeathAndCountsTheRetry(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.llm = &drive.LLM{} // armed; never reached — the turn fails first
	s.claudeBin = "false"
	ws := t.TempDir()
	a, _ := s.tasks.capture("the work", time.Now().Add(-time.Minute))
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Col, x.State = "waiting", "waiting · the run stopped"
		x.Goal, x.Workspace, x.Mode = "the goal", ws, "read"
		x.StopErr, x.StopTransient = "signal: killed", true
		return nil
	})

	s.recoverTask(a.ID)

	// The retry is on the record immediately; the doomed run lands
	// shortly after and folds back to a (non-transient) stop.
	got, _ := s.tasks.get(a.ID)
	if got.Retries != 1 {
		t.Fatalf("the retry must be counted: %+v", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ = s.tasks.get(a.ID)
		if got.Col == "waiting" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the doomed run never landed: %+v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got.StopTransient {
		t.Fatalf("exit 1 with no story is not transient: %+v", got)
	}
	var recovering bool
	for _, ev := range got.Log {
		if strings.Contains(ev.Text, "auto-recovering (retry 1/2)") {
			recovering = true
		}
	}
	if !recovering {
		t.Fatalf("the recovery must be on the log: %+v", got.Log)
	}
}

func TestRecoverStopsWhenTheOwnerMovedFirst(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.llm = &drive.LLM{}
	a := stoppedCard(t, s, true, 0, time.Minute)
	s.tasks.mutate(a.ID, func(x *task) error {
		x.Col = "dropped"
		return nil
	})
	s.recoverTask(a.ID)
	got, _ := s.tasks.get(a.ID)
	if got.Retries != 0 || got.Col != "dropped" {
		t.Fatalf("a moved card is the owner's: %+v", got)
	}
}

// ---- the engine's launch discipline ----

func TestEngineLaunchDedupesInFlightKeys(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	e := newEngine(s, nil, 10)
	var mu sync.Mutex
	ran := 0
	block := make(chan struct{})
	act := Action{Key: "k", Free: true, Run: func() {
		mu.Lock()
		ran++
		mu.Unlock()
		<-block
	}}
	now := time.Now()
	e.launch(act, now)
	e.launch(act, now) // in flight: must not double-fire
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	got := ran
	mu.Unlock()
	if got != 1 {
		t.Fatalf("ran %d times", got)
	}
	close(block)
}

func TestEngineRateLimitsSpendingButNotFreeActions(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	e := newEngine(s, nil, 1)
	var mu sync.Mutex
	ran := map[string]bool{}
	mk := func(key string, free bool) Action {
		return Action{Key: key, Free: free, Run: func() {
			mu.Lock()
			ran[key] = true
			mu.Unlock()
		}}
	}
	now := time.Now()
	e.launch(mk("spend-1", false), now)
	e.launch(mk("spend-2", false), now) // over budget
	e.launch(mk("free-1", true), now)   // free rides regardless
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		ok := ran["spend-1"] && ran["free-1"]
		mu.Unlock()
		if ok || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !ran["spend-1"] || !ran["free-1"] || ran["spend-2"] {
		t.Fatalf("ran: %+v", ran)
	}
}
