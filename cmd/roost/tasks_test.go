package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskCaptureIsGlobalUnassignedBacklog(t *testing.T) {
	st := &taskStore{dir: t.TempDir()}
	now := time.Now()
	a, err := st.capture("decide where the board lives", now)
	if err != nil || a.ID != "T-100" || a.Col != "inbox" || a.ProposalKind != "start" {
		t.Fatalf("a=%+v err=%v", a, err)
	}
	if a.Agent != "" {
		t.Fatalf("a captured task belongs to nobody yet: %q", a.Agent)
	}
	if b, _ := st.capture("second", now.Add(time.Second)); b.ID != "T-101" {
		t.Fatalf("numbering: %s", b.ID)
	}
	if len(a.Log) != 1 || a.Log[0].Actor != "human" {
		t.Fatalf("log: %+v", a.Log)
	}
}

func TestTaskAdoptCarriesTheAgentAndItsTitle(t *testing.T) {
	st := &taskStore{dir: t.TempDir()}
	a, err := st.adopt("root-1", "Create local CLI alternative to Rook host", "rook-cloud", time.Now())
	if err != nil || a.Agent != "root-1" || a.Col != "progress" {
		t.Fatalf("a=%+v err=%v", a, err)
	}
	if a.Title != "Create local CLI alternative to Rook host" {
		t.Fatalf("title: %q", a.Title)
	}
	if a.Log[0].Actor != "rook" {
		t.Fatalf("adoption is rook's act, on the record: %+v", a.Log)
	}
}

func TestTaskOrderingPinnedFirst(t *testing.T) {
	st := &taskStore{dir: t.TempDir()}
	now := time.Now()
	a, _ := st.capture("old one", now.Add(-time.Hour))
	st.capture("new one", now)
	st.mutate(a.ID, func(x *task) error { x.Pinned = true; return nil })
	if list := st.list(); list[0].ID != a.ID || !list[0].Pinned {
		t.Fatalf("pinned must lead: %+v", list)
	}
}

func TestTaskIDsAreFilenameShapedOrRefused(t *testing.T) {
	st := &taskStore{dir: t.TempDir()}
	if _, err := st.get("../T-1"); err == nil {
		t.Fatal("bad id accepted")
	}
}

// writeWorkingTranscript drops a session mid-tool-call with a fresh
// mtime — the scanner reads WORKING.
func writeWorkingTranscript(t *testing.T, dir, proj, id, title string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, proj)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := mtime.UTC().Format(time.RFC3339)
	lines := fmt.Sprintf(`{"type":"user","timestamp":%q,"cwd":"/repo/%s","aiTitle":%q,"message":{"role":"user","content":"go"}}
{"type":"assistant","timestamp":%q,"cwd":"/repo/%s","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"description":"running the suite"}}]}}
`, ts, proj, title, ts, proj)
	path := filepath.Join(p, id+".jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func boardOf(t *testing.T, s *server) (tasks []task, fleet map[string]int) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleTaskList(rec, httptest.NewRequest("GET", "/api/tasks", nil))
	var got struct {
		Tasks []task         `json:"tasks"`
		Fleet map[string]int `json:"fleet"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("board did not parse: %v — %s", err, rec.Body.String())
	}
	return got.Tasks, got.Fleet
}

func TestBoardAdoptsTheWorkingAgentOnceWithLiveStatus(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeWorkingTranscript(t, dir, "-repo-alpha", "live-1", "Create local CLI alternative to Rook host", now)
	writeTranscript(t, dir, "-repo-beta", "quiet-1", now.Add(-30*time.Minute)) // needs-you, not adopted
	s := testServer(t, dir)

	tasks, fleet := boardOf(t, s)
	if fleet["working"] != 1 || fleet["agents"] != 2 {
		t.Fatalf("fleet: %+v", fleet)
	}
	if len(tasks) != 1 {
		t.Fatalf("exactly the working agent's task, nothing for the quiet one: %+v", tasks)
	}
	got := tasks[0]
	if got.Title != "Create local CLI alternative to Rook host" || got.Agent != "live-1" || got.Col != "progress" {
		t.Fatalf("adopted card: %+v", got)
	}
	// The live overlay: the agent's present state and its tool line.
	if got.Live == nil || got.Live.State != "working" || got.Live.Dir != "-repo-alpha" {
		t.Fatalf("live: %+v", got.Live)
	}
	if got.Live.Now != "Bash — running the suite" {
		t.Fatalf("now line: %q", got.Live.Now)
	}

	// A second read adopts nothing new: one agent, one open task.
	tasks, _ = boardOf(t, s)
	if len(tasks) != 1 {
		t.Fatalf("adopted twice: %+v", tasks)
	}
	// And the overlay is derived, never persisted.
	onDisk, err := s.tasks.get(got.ID)
	if err != nil || onDisk.Live != nil {
		t.Fatalf("overlay leaked to disk: %+v err=%v", onDisk.Live, err)
	}
}

// writeUntitledWorkingTranscript is a probe-shaped session: working,
// but Claude never titled it (`claude /usage -p` leaves exactly this).
func writeUntitledWorkingTranscript(t *testing.T, dir, proj, id string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, proj)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := mtime.UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"cwd":"/repo/%s","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{}}]}}`, ts, proj)
	path := filepath.Join(p, id+".jsonl")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestBoardDoesNotAdoptUntitledProbes(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeUntitledWorkingTranscript(t, dir, "-Users-someone", "probe-1", now)
	s := testServer(t, dir)
	tasks, fleet := boardOf(t, s)
	if fleet["working"] != 1 {
		t.Fatalf("the probe still counts as a working session: %+v", fleet)
	}
	if len(tasks) != 0 {
		t.Fatalf("a probe must not become a task: %+v", tasks)
	}
}

func TestBoardBacklogStaysUnassignedBesideTheLiveTask(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeWorkingTranscript(t, dir, "-repo-alpha", "live-1", "the live work", now)
	s := testServer(t, dir)

	if _, err := s.tasks.capture("persist the digest cache", now); err != nil {
		t.Fatal(err)
	}
	tasks, _ := boardOf(t, s)
	if len(tasks) != 2 {
		t.Fatalf("tasks: %+v", tasks)
	}
	var backlog, live int
	for _, x := range tasks {
		if x.Agent == "" && x.Col == "inbox" && x.Live == nil {
			backlog++
		}
		if x.Agent == "live-1" && x.Col == "progress" {
			live++
		}
	}
	if backlog != 1 || live != 1 {
		t.Fatalf("backlog=%d live=%d — %+v", backlog, live, tasks)
	}
}

func TestJournalsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	spendPath := filepath.Join(dir, "spend.jsonl")
	digestPath := filepath.Join(dir, "digests.jsonl")

	first := testServer(t, t.TempDir())
	first.spendPath, first.digestPath = spendPath, digestPath
	first.addSpend("root-1", 1.25, 0.10)
	first.addSpend("root-1", 0.75, 0)
	first.addSpend("root-2", 0, 0.05)
	appendLine(digestPath, digestLine{Hash: "abc123", Headline: "It shipped.", Bullets: []string{"tag it"}, At: time.Now()})

	// A fresh process, same journals: the meters must not have forgotten.
	second := testServer(t, t.TempDir())
	second.spendPath, second.digestPath = spendPath, digestPath
	second.loadJournals()
	if sp := second.spend["root-1"]; sp == nil || sp.ClaudeUSD != 2.0 || sp.JudgeUSD != 0.10 {
		t.Fatalf("root-1 ledger: %+v", sp)
	}
	if sp := second.spend["root-2"]; sp == nil || sp.JudgeUSD != 0.05 {
		t.Fatalf("root-2 ledger: %+v", sp)
	}
	rec := second.digests["abc123"]
	if rec == nil || rec.State != "ready" || rec.Headline != "It shipped." {
		t.Fatalf("digest cache: %+v", rec)
	}
}

func TestRepoListOffersFleetDirsButNeverHome(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeWorkingTranscript(t, dir, "-repo-alpha", "live-1", "titled work", now)
	// An untitled session in a real repo still offers its directory —
	// titled-ness gates adoption, not geography.
	writeUntitledWorkingTranscript(t, dir, "-repo-beta", "quiet-1", now)
	s := testServer(t, dir)
	repos := repoList(s.boardSessions(now), "/repo/-repo-beta")
	// beta played the role of $HOME here and must be excluded.
	if len(repos) != 1 || repos[0]["cwd"] != "/repo/-repo-alpha" {
		t.Fatalf("repos: %+v", repos)
	}
	// And a fresh start may only name what was offered.
	if repoOffered(s.boardSessions(now), "/somewhere/else") != "" {
		t.Fatal("an unoffered directory must be refused")
	}
	if repoOffered(s.boardSessions(now), "/repo/-repo-alpha") == "" {
		t.Fatal("the offered directory must be accepted")
	}
}
