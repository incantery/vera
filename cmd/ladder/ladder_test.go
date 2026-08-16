package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/incantery/vera/drive"
)

func TestCorpusValidation(t *testing.T) {
	good := task{ID: "ok", Goal: "do it", Check: "true"}
	if err := good.validate(); err != nil {
		t.Fatalf("a sound task must validate: %v", err)
	}
	bad := []task{
		{ID: "no bar", Goal: "do it"},                                             // id has a space AND no bar
		{ID: "nobar", Goal: "do it"},                                              // no check, no expect
		{ID: "x", Goal: " "},                                                      // empty goal
		{ID: "x", Goal: "g", Check: "true", Mode: "vibes"},                        // unknown mode
		{ID: "x", Goal: "g", Check: "true", Files: map[string]string{"../a": ""}}, // escape
		{ID: "x", Goal: "g", Check: "true", Files: map[string]string{"/a": ""}},   // absolute
	}
	for i, tk := range bad {
		if err := tk.validate(); err == nil {
			t.Errorf("bad task %d validated: %+v", i, tk)
		}
	}
}

func TestExampleCorpusLoads(t *testing.T) {
	tasks, err := loadCorpus("corpus.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) < 4 {
		t.Fatalf("the example corpus shrank: %d tasks", len(tasks))
	}
}

func TestResultsJournalRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.jsonl")
	ok := true
	r1 := result{At: "2026-08-16T00:00:00Z", Task: "a", Model: "m", Arm: "bare", Rep: 1, Pass: true, CheckOK: &ok, ClaudeUSD: 0.10}
	r2 := result{At: "2026-08-16T00:01:00Z", Task: "a", Model: "m", Arm: "drive", Rep: 1, JudgeUSD: 0.02}
	for _, r := range []result{r1, r2} {
		if err := appendResult(path, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := loadResults(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].key() != "a|m|bare|1" || got[1].key() != "a|m|drive|1" {
		t.Fatalf("round trip broke: %+v", got)
	}
	if !got[0].Pass || got[0].CheckOK == nil || !*got[0].CheckOK {
		t.Fatalf("the bar's word was lost: %+v", got[0])
	}
	// A missing journal is an empty record, not an error.
	if rs, err := loadResults(filepath.Join(t.TempDir(), "absent.jsonl")); err != nil || rs != nil {
		t.Fatalf("missing journal: %v %v", rs, err)
	}
}

func TestTableArithmetic(t *testing.T) {
	results := []result{
		{Task: "a", Model: "sonnet", Arm: "drive", Rep: 1, Pass: true, ClaudeUSD: 0.30, JudgeUSD: 0.10, Secs: 10},
		{Task: "a", Model: "sonnet", Arm: "drive", Rep: 2, Pass: false, ClaudeUSD: 0.60, Secs: 30},
		{Task: "a", Model: "opus", Arm: "bare", Rep: 1, Pass: false, Err: "boom"},
	}
	var b strings.Builder
	writeTable(&b, results, false)
	out := b.String()
	// sonnet/drive: 2 runs, 1 pass, $1.00 total → $0.50/run, $1.00/pass.
	if !strings.Contains(out, "$0.50") || !strings.Contains(out, "$1.00") {
		t.Fatalf("the arithmetic drifted:\n%s", out)
	}
	// opus/bare: no passes → the per-pass column holds a dash, not a division by zero.
	if !strings.Contains(out, "—") {
		t.Fatalf("zero passes must print a dash:\n%s", out)
	}
}

// stubClaude mirrors drive's test helper: a fake binary echoing a
// canned envelope, so cells run the real exec path without tokens.
func stubClaude(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func newLab(t *testing.T, bin string) *lab {
	t.Helper()
	world := t.TempDir()
	l := &lab{
		world: world, runsDir: filepath.Join(world, "runs"),
		resultsPath: filepath.Join(world, "results.jsonl"),
		claudeBin:   bin, turns: 2, maxUSD: 5, timeout: time.Minute, total: 1,
	}
	if err := os.MkdirAll(l.runsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestRunCellBareSeedsChecksAndRecords(t *testing.T) {
	// The stub "works": it writes the file the check demands, proving
	// the check runs in the seeded run directory.
	bin := stubClaude(t, `echo done > out.txt
echo '{"type":"result","result":"the counter has a data race","session_id":"s1","total_cost_usd":0.05}'`)
	l := newLab(t, bin)
	c := cell{
		task: task{ID: "t1", Goal: "go", Mode: "read",
			Files:  map[string]string{"seed/hello.txt": "hi"},
			Check:  "test -f out.txt && test -f seed/hello.txt",
			Expect: []string{"data race"}},
		model: "claude-sonnet-5", arm: "bare", rep: 1,
	}
	l.runCell(context.Background(), c)
	got, err := loadResults(l.resultsPath)
	if err != nil || len(got) != 1 {
		t.Fatalf("one line expected: %v %v", got, err)
	}
	r := got[0]
	if !r.Pass || r.CheckOK == nil || !*r.CheckOK || r.ExpectOK == nil || !*r.ExpectOK {
		t.Fatalf("both bars held and the record must say so: %+v", r)
	}
	if r.ClaudeUSD != 0.05 || r.Session != "s1" || r.Turns != 1 {
		t.Fatalf("the envelope's word was lost: %+v", r)
	}
}

func TestRunCellFailsTheBarHonestly(t *testing.T) {
	bin := stubClaude(t, `echo '{"type":"result","result":"looks fine to me","session_id":"s1","total_cost_usd":0.01}'`)
	l := newLab(t, bin)
	c := cell{
		task:  task{ID: "t2", Goal: "go", Mode: "read", Expect: []string{"data race"}},
		model: "claude-haiku-4-5", arm: "bare", rep: 1,
	}
	l.runCell(context.Background(), c)
	got, _ := loadResults(l.resultsPath)
	if len(got) != 1 || got[0].Pass || got[0].ExpectOK == nil || *got[0].ExpectOK {
		t.Fatalf("a miss must be a recorded miss: %+v", got)
	}
}

// yesJudge says DONE the moment it is asked — the drive arm's plumbing
// under test, not the judge's taste.
type yesJudge struct{ spend func(float64) }

func (j *yesJudge) Judge(ctx context.Context, goal string, history []drive.Exchange) (drive.Verdict, error) {
	if j.spend != nil {
		j.spend(0.02)
	}
	return drive.Verdict{Done: true, Reason: "met"}, nil
}

func TestRunCellDriveMetersBothSides(t *testing.T) {
	bin := stubClaude(t, `echo '{"type":"result","result":"fixed","session_id":"s1","total_cost_usd":0.10}'`)
	l := newLab(t, bin)
	l.judge = func(spend func(float64)) drive.Judge { return &yesJudge{spend: spend} }
	c := cell{
		task:  task{ID: "t3", Goal: "go", Check: "true"},
		model: "claude-sonnet-5", arm: "drive", rep: 1,
	}
	l.runCell(context.Background(), c)
	got, _ := loadResults(l.resultsPath)
	if len(got) != 1 {
		t.Fatalf("one line expected: %+v", got)
	}
	r := got[0]
	if !r.Pass || !r.Done || r.ClaudeUSD != 0.10 || r.JudgeUSD != 0.02 || r.Turns != 1 {
		t.Fatalf("both meters and the verdict belong on the record: %+v", r)
	}
}

func TestFreshDirWipesTheLeftover(t *testing.T) {
	l := newLab(t, "claude-not-called")
	c := cell{task: task{ID: "t4", Goal: "g", Check: "true"}, model: "m", arm: "bare", rep: 1}
	dir, err := l.freshDir(c)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "stale.txt")
	if err := os.WriteFile(stale, []byte("old run"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir2, err := l.freshDir(c)
	if err != nil || dir2 != dir {
		t.Fatalf("the cell's dir must be stable: %v %v", dir2, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("a half-run leftover must be wiped")
	}
}
