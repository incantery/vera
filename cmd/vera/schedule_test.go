package main

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScheduleDue(t *testing.T) {
	now := time.Now()
	for name, tc := range map[string]struct {
		e    schedEntry
		want bool
	}{
		"one-shot due":       {schedEntry{At: now.Add(-time.Minute)}, true},
		"one-shot not yet":   {schedEntry{At: now.Add(time.Minute)}, false},
		"one-shot fired":     {schedEntry{At: now.Add(-time.Hour), LastRun: now.Add(-time.Minute)}, false},
		"recurring due":      {schedEntry{At: now.Add(-2 * time.Hour), Every: "1h", LastRun: now.Add(-90 * time.Minute)}, true},
		"recurring resting":  {schedEntry{At: now.Add(-2 * time.Hour), Every: "1h", LastRun: now.Add(-time.Minute)}, false},
		"paused stays quiet": {schedEntry{At: now.Add(-time.Hour), Paused: true}, false},
		"bad every":          {schedEntry{At: now.Add(-time.Hour), Every: "sideways", LastRun: now.Add(-time.Hour)}, false},
	} {
		if got := tc.e.due(now); got != tc.want {
			t.Fatalf("%s: due=%v want %v", name, got, tc.want)
		}
	}
}

func TestScheduleStoreRoundTrips(t *testing.T) {
	st := &schedStore{path: filepath.Join(t.TempDir(), "vera-schedule.json")}
	a, err := st.add(schedEntry{Intent: "sweep the yard", Workspace: "/w", At: time.Now(), CreatedAt: time.Now()})
	if err != nil || a.ID != "S-1" {
		t.Fatalf("a=%+v err=%v", a, err)
	}
	b, _ := st.add(schedEntry{Intent: "second", Workspace: "/w", At: time.Now(), CreatedAt: time.Now()})
	if b.ID != "S-2" {
		t.Fatalf("numbering: %s", b.ID)
	}
	if err := st.remove(a.ID); err != nil {
		t.Fatal(err)
	}
	if got := st.list(); len(got) != 1 || got[0].ID != "S-2" {
		t.Fatalf("list: %+v", got)
	}
	if err := st.remove("S-9"); err == nil {
		t.Fatal("removing a ghost must refuse")
	}
}

func TestFireScheduleMakesTheCardAndStampsTheEntry(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	s.sched = &schedStore{path: filepath.Join(t.TempDir(), "vera-schedule.json")}
	ws := t.TempDir()
	e, _ := s.sched.add(schedEntry{
		Intent: "sweep the yard", Workspace: ws,
		At: time.Now().Add(-time.Minute), CreatedAt: time.Now(),
	})

	// No key: the card is captured, not started — and says so.
	s.fireSchedule(e.ID)

	var tasks []task
	for _, x := range s.tasks.list() {
		tasks = append(tasks, x)
	}
	if len(tasks) != 1 || tasks[0].Col != "inbox" || tasks[0].Intent != "sweep the yard" {
		t.Fatalf("tasks: %+v", tasks)
	}
	var said bool
	for _, ev := range tasks[0].Log {
		if strings.Contains(ev.Text, "no vera-agent key") {
			said = true
		}
	}
	if !said {
		t.Fatalf("the card must say why it did not start: %+v", tasks[0].Log)
	}
	got := s.sched.list()[0]
	if got.LastRun.IsZero() || got.LastTask != tasks[0].ID {
		t.Fatalf("the entry must remember its firing: %+v", got)
	}

	// Fired one-shots owe nothing more; the system proposes nothing.
	if acts := (scheduleSystem{s}).Tick(s.world(time.Now())); len(acts) != 0 {
		t.Fatalf("a fired one-shot must rest: %+v", acts)
	}
}

func TestScheduleSystemProposesTheDueFiring(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.sched = &schedStore{path: filepath.Join(t.TempDir(), "vera-schedule.json")}
	e, _ := s.sched.add(schedEntry{
		Intent: "nightly digest", Workspace: t.TempDir(),
		At: time.Now().Add(-time.Minute), CreatedAt: time.Now(),
	})
	acts := (scheduleSystem{s}).Tick(s.world(time.Now()))
	if len(acts) != 1 || !strings.HasPrefix(acts[0].Key, "sched/"+e.ID+"/") {
		t.Fatalf("acts: %+v", acts)
	}
}

func TestScheduleRoutesValidateAndListen(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	s.sched = &schedStore{path: filepath.Join(t.TempDir(), "vera-schedule.json")}
	ws := t.TempDir()

	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleScheduleAdd(rec, httptest.NewRequest("POST", "/api/schedule", strings.NewReader(body)))
		return rec
	}
	if rec := post(`{"intent":"","workspace":"`+ws+`","every":"1h"}`); rec.Code != 400 {
		t.Fatalf("an empty intent must refuse: %d", rec.Code)
	}
	if rec := post(`{"intent":"x","workspace":"/no/such/dir","every":"1h"}`); rec.Code != 400 {
		t.Fatalf("a missing workspace must refuse: %d", rec.Code)
	}
	if rec := post(`{"intent":"x","workspace":"`+ws+`"}`); rec.Code != 400 {
		t.Fatalf("no when must refuse: %d", rec.Code)
	}
	if rec := post(`{"intent":"x","workspace":"`+ws+`","every":"5s"}`); rec.Code != 400 {
		t.Fatalf("a sub-minute cadence must refuse: %d", rec.Code)
	}
	rec := post(`{"intent":"sweep","workspace":"` + ws + `","every":"1h"}`)
	if rec.Code != 200 {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	var e schedEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.ID == "" {
		t.Fatalf("entry: %+v err=%v", e, err)
	}
	// "every" with no "at": the first firing waits one interval.
	if e.At.Before(time.Now().Add(50 * time.Minute)) {
		t.Fatalf("first firing must wait an interval: %v", e.At)
	}

	rec = httptest.NewRecorder()
	s.handleScheduleList(rec, httptest.NewRequest("GET", "/api/schedule", nil))
	var got struct {
		Entries []schedEntry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Entries) != 1 {
		t.Fatalf("list: %+v err=%v", got, err)
	}

	req := httptest.NewRequest("DELETE", "/api/schedule/"+e.ID, nil)
	req.SetPathValue("id", e.ID)
	rec = httptest.NewRecorder()
	s.handleScheduleRemove(rec, req)
	if rec.Code != 200 || len(s.sched.list()) != 0 {
		t.Fatalf("remove: %d entries=%+v", rec.Code, s.sched.list())
	}
}
