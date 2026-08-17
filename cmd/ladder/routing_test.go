package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/vera/route"
)

// The routing corpus is the instrument. If it rots — a task loses its
// kind, a kind is misspelled, a bar goes missing — the verdict keeps
// printing and quietly means nothing.
func TestRoutingCorpusIsWellFormedAndFullyKinded(t *testing.T) {
	for _, path := range []string{"corpus.routing.json", "corpus-hard.json"} {
		tasks, err := loadCorpus(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, task := range tasks {
			if task.Kind == "" {
				t.Fatalf("%s: %s carries no kind and would sit out the verdict", path, task.ID)
			}
			if !knownKind(task.Kind) {
				t.Fatalf("%s: %s has an unknown kind %q", path, task.ID, task.Kind)
			}
		}
	}
	// The easy corpus is the one that claims per-kind coverage; two
	// tasks is the floor for a verdict to be about the KIND rather than
	// about one task.
	tasks, _ := loadCorpus("corpus.routing.json")
	seen := map[string]int{}
	for _, task := range tasks {
		seen[task.Kind]++
	}
	for _, want := range []string{route.KindVerify, route.KindReview, route.KindInvestigate, route.KindImplement} {
		if seen[want] < 2 {
			t.Fatalf("%s has %d task(s); a kind judged on one task is judging that task", want, seen[want])
		}
	}
}

// A misspelled kind must be refused rather than normalized: folding it
// to "implement" would file a review task under the strongest tier and
// make the verdict a lie.
func TestAMisspelledKindIsRefusedNotNormalized(t *testing.T) {
	task := task{ID: "x", Goal: "do it", Kind: "reviw", Check: "true"}
	err := task.validate()
	if err == nil {
		t.Fatal("a typo'd kind is refused")
	}
	if !strings.Contains(err.Error(), "reviw") {
		t.Fatalf("and the error names it: %v", err)
	}
	if route.NormalizeKind("reviw") != route.KindImplement {
		t.Fatal("precondition: NormalizeKind would have silently folded it — that is the trap")
	}
}

func TestObservationsCarryTheWholeCost(t *testing.T) {
	obs := observations([]result{
		{Task: "t", Kind: route.KindReview, Model: "sonnet", Pass: true, ClaudeUSD: 0.10, JudgeUSD: 0.02},
	})
	if len(obs) != 1 {
		t.Fatalf("one cell, one observation: %+v", obs)
	}
	// Judge tokens are part of what a pass cost; dropping them would
	// flatter the supervised arm.
	if math.Abs(obs[0].USD-0.12) > 1e-9 {
		t.Fatalf("claude and judge spend both count: %v", obs[0].USD)
	}
}

// The board is the instrument the corpus could not be: a real node's
// bar is the owner accepting it, which is the judgment a check command
// was only ever standing in for.
func TestBoardOutcomesLoadFromVerasJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vera-outcomes.jsonl")
	os.WriteFile(path, []byte(strings.Join([]string{
		`{"at":"2026-08-17T10:00:00Z","task":"T-101","kind":"review","model":"sonnet","accepted":true,"costUsd":0.31}`,
		`{"at":"2026-08-17T10:05:00Z","task":"T-102","kind":"review","model":"haiku","accepted":false,"costUsd":0.04}`,
		`this line is not json and must not stop the read`,
		`{"at":"2026-08-17T10:09:00Z","task":"T-103","kind":"implement","model":"opus","accepted":true,"costUsd":1.20}`,
	}, "\n")+"\n"), 0o644)

	obs, err := loadBoardOutcomes(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 3 {
		t.Fatalf("one lost line beats refusing to read the rest: %+v", obs)
	}
	if obs[0].Kind != route.KindReview || obs[0].Model != "sonnet" || !obs[0].Pass || obs[0].USD != 0.31 {
		t.Fatalf("acceptance is the pass: %+v", obs[0])
	}
	if obs[1].Pass {
		t.Fatal("a dropped node is a fail")
	}
}

// A board that has finished no nodes has nothing to say and should say
// so rather than fail.
func TestAMissingBoardJournalIsAnEmptyRecord(t *testing.T) {
	obs, err := loadBoardOutcomes(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("a board with no history is not an error: %v", err)
	}
	if len(obs) != 0 {
		t.Fatal("and it is empty")
	}
}
