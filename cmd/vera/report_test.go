package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportCountsTheWindowOffTheRecords(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	s.report = &reportStore{path: filepath.Join(t.TempDir(), "vera-report.json")}
	s.spendPath = filepath.Join(t.TempDir(), "spend.jsonl")
	now := time.Now()

	a, _ := s.tasks.capture("the work", now.Add(-2*time.Hour))
	s.tasks.mutate(a.ID, func(x *task) error {
		x.event("vera", "its schedule came due — compiled intent → goal, starting", now.Add(-2*time.Hour))
		x.event("vera", "auto-recovering (retry 1/2) — the run vanished", now.Add(-time.Hour))
		x.event("vera", "judged done, proposed acceptance", now.Add(-30*time.Minute))
		return nil
	})
	b, _ := s.tasks.capture("waiting one", now.Add(-3*time.Hour))
	s.tasks.mutate(b.ID, func(x *task) error {
		x.Col, x.State = "waiting", "waiting · escalated to you"
		x.Ask = "which database?"
		// An event outside the window must not count.
		x.event("vera", "steward: proposed done — old news", now.Add(-30*time.Hour))
		return nil
	})
	appendLine(s.spendPath, spendLine{Root: "r1", Claude: 1.25, Judge: 0.05, At: now.Add(-time.Hour)})
	appendLine(s.spendPath, spendLine{Root: "vera-steward", Judge: 0.01, At: now.Add(-time.Minute)})
	appendLine(s.spendPath, spendLine{Root: "r1", Claude: 9.99, At: now.Add(-48 * time.Hour)}) // outside

	text := s.renderReport(now.Add(-24*time.Hour), now)
	for _, want := range []string{
		"started 1", "judged done 1", "recovered 1", "steward moves 0",
		"$1.31", "workers $1.25", "vera $0.06",
		"Waiting on you", b.ID,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the report must say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "9.99") {
		t.Fatalf("spend outside the window must not count:\n%s", text)
	}
}

func TestReportSystemFiresDailyAndStamps(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.hub = newHub()
	s.report = &reportStore{path: filepath.Join(t.TempDir(), "vera-report.json")}
	now := time.Now()

	acts := reportSystem{s}.Tick(s.world(now))
	if len(acts) != 1 || !acts[0].Free {
		t.Fatalf("the first report is owed and free: %+v", acts)
	}
	acts[0].Run()
	last := s.report.last()
	if last.At.IsZero() || last.Text == "" {
		t.Fatalf("the report must be stored: %+v", last)
	}
	if !strings.Contains(last.Text, "quiet") {
		t.Fatalf("an empty day is a quiet day, said plainly: %q", last.Text)
	}

	// Inside the day: nothing owed. Past it: owed again.
	if acts := (reportSystem{s}).Tick(s.world(now.Add(time.Hour))); len(acts) != 0 {
		t.Fatalf("one report a day: %+v", acts)
	}
	if acts := (reportSystem{s}).Tick(s.world(last.At.Add(25 * time.Hour))); len(acts) != 1 {
		t.Fatalf("the next day owes the next report: %+v", acts)
	}
}

func TestReportSitsOutWithoutAStateDir(t *testing.T) {
	s := testServer(t, t.TempDir())
	s.report = &reportStore{}
	if acts := (reportSystem{s}).Tick(s.world(time.Now())); len(acts) != 0 {
		t.Fatalf("no state dir, no report: %+v", acts)
	}
}
