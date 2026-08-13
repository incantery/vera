package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/incantery/rook-host/engine/transcript"
	"github.com/incantery/rook-host/engine/usage"
)

// writeTranscript drops one minimal session file: an ended turn, so
// the scanner reads a clean needs-you with the mtime the test sets.
func writeTranscript(t *testing.T, dir, proj, id string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, proj)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(
		`{"type":"assistant","timestamp":%q,"cwd":"/repo/%s","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`,
		mtime.UTC().Format(time.RFC3339), proj)
	path := filepath.Join(p, id+".jsonl")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

type stateResp struct {
	Sessions []wireSession `json:"sessions"`
	Current  string        `json:"current"`
}

func stateOf(t *testing.T, s *server) stateResp {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleState(rec, httptest.NewRequest("GET", "/api/state", nil))
	var got stateResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("state did not parse: %v — %s", err, rec.Body.String())
	}
	return got
}

func testServer(t *testing.T, dir string) *server {
	t.Helper()
	return &server{
		sc: &transcript.Scanner{
			Dir: dir, Window: 48 * time.Hour, Idle: 10 * time.Minute,
			Quiet: 60 * time.Second, Max: 50,
		},
		ln:      openLineage(""),
		uc:      &usage.Collector{}, // never started; Latest() honestly answers nil
		says:    map[string]*sayJob{},
		spend:   map[string]*agentSpend{},
		digests: map[string]*digestRec{},
		sent:    map[string]string{},
		tasks:   &taskStore{dir: t.TempDir()},
	}
}

func TestStateDiscoversSessionsAndNamesTheCurrent(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeTranscript(t, dir, "-repo-alpha", "sess-old", now.Add(-2*time.Hour))
	writeTranscript(t, dir, "-repo-beta", "sess-live", now.Add(-time.Minute))
	s := testServer(t, dir)

	got := stateOf(t, s)
	if len(got.Sessions) != 2 {
		t.Fatalf("sessions: %+v", got.Sessions)
	}
	// The freshest lineage is the current one — the session the human
	// is living in right now.
	if got.Current != "sess-live" {
		t.Fatalf("current: %q", got.Current)
	}
}

func TestStateHidesForksAndTheRootWearsTheHeadsFreshness(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// The root went quiet two hours ago; its fork (a chat turn) is the
	// freshest thing on the machine.
	writeTranscript(t, dir, "-repo-alpha", "root-1", now.Add(-2*time.Hour))
	writeTranscript(t, dir, "-repo-alpha", "fork-1", now.Add(-time.Minute))
	writeTranscript(t, dir, "-repo-beta", "bystander", now.Add(-time.Hour))
	s := testServer(t, dir)
	s.ln.advance("root-1", "fork-1")

	got := stateOf(t, s)
	ids := map[string]bool{}
	for _, ws := range got.Sessions {
		ids[ws.ID] = true
	}
	if ids["fork-1"] {
		t.Fatal("a fork must never be listed as its own agent")
	}
	if !ids["root-1"] || !ids["bystander"] {
		t.Fatalf("roots missing: %+v", ids)
	}
	// The current agent is the ROOT id, credited with the fork's
	// freshness — selection lands on the agent, not the plumbing.
	if got.Current != "root-1" {
		t.Fatalf("current: %q", got.Current)
	}
}

func TestStateSelectionSurvivesARestart(t *testing.T) {
	// The lineage journal is what keeps a fork adopted across process
	// lives; a forgotten journal would resurface forks as strangers.
	dir := t.TempDir()
	now := time.Now()
	writeTranscript(t, dir, "-repo-alpha", "root-1", now.Add(-2*time.Hour))
	writeTranscript(t, dir, "-repo-alpha", "fork-1", now.Add(-time.Minute))
	journal := filepath.Join(t.TempDir(), "lineage.jsonl")

	first := openLineage(journal)
	first.advance("root-1", "fork-1")

	s := testServer(t, dir)
	s.ln = openLineage(journal) // a fresh process, same journal
	got := stateOf(t, s)
	if got.Current != "root-1" || len(got.Sessions) != 1 {
		t.Fatalf("after restart: current=%q sessions=%d", got.Current, len(got.Sessions))
	}
}

func TestCommandsAreNeverExpanded(t *testing.T) {
	// A slash command must reach claude exactly as typed; the membrane
	// phrasing "/compact" into prose would turn a verb into a paragraph.
	for text, want := range map[string]bool{
		"/compact": true, "/clear": true, "fix the tests": false, " /not-a-command": false,
	} {
		if isCommand(text) != want {
			t.Fatalf("isCommand(%q) != %v", text, want)
		}
	}
}
