package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	roostv1 "github.com/incantery/vera/gen/vera/v1"
)

// exploreWorld stands up a world with a small directory tree: a git
// repo, a plain dir, a hidden dir, and a loose file.
func exploreWorld(t *testing.T) string {
	t.Helper()
	w := t.TempDir()
	for _, d := range []string{"repo-a/.git", "notes", ".secret"} {
		if err := os.MkdirAll(filepath.Join(w, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(w, "loose.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestExplorerStaysInsideTheFence(t *testing.T) {
	oldWorld := worldRoot
	defer func() { worldRoot = oldWorld }()
	worldRoot = exploreWorld(t)
	s := testServer(t, t.TempDir())

	v, serr := s.listDirs("", time.Now())
	if serr != nil {
		t.Fatal(serr.msg)
	}
	if v.Root != worldRoot || v.Path != worldRoot || v.Parent != "" {
		t.Fatalf("the root browses as itself: %+v", v)
	}
	// Directories only, dotfiles skipped, git marked.
	if len(v.Dirs) != 2 || v.Dirs[0].Name != "notes" || v.Dirs[1].Name != "repo-a" {
		t.Fatalf("two visible dirs, sorted: %+v", v.Dirs)
	}
	if !v.Dirs[1].Git || v.Dirs[0].Git {
		t.Fatalf("git marks the repo and only the repo: %+v", v.Dirs)
	}

	// Descend, then the parent points home.
	v2, serr := s.listDirs(v.Dirs[1].Cwd, time.Now())
	if serr != nil || v2.Parent != worldRoot || !v2.Git {
		t.Fatalf("descending keeps its bearings: %+v %v", v2, serr)
	}

	// Escapes are refused, relative or absolute.
	if _, serr := s.listDirs("../..", time.Now()); serr == nil || serr.code != 400 {
		t.Fatalf("a relative escape must refuse: %+v", serr)
	}
	if _, serr := s.listDirs("/etc", time.Now()); serr == nil || serr.code != 400 {
		t.Fatalf("an absolute escape must refuse: %+v", serr)
	}
}

// stubClaude writes an executable that answers like claude -p:
// one JSON envelope naming the session it became.
func stubClaude(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\necho '{\"type\":\"result\",\"result\":\"hello from the newborn\",\"session_id\":\"sess-born-1\",\"total_cost_usd\":0.01}'\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStartSessionBirthsAndRefuses(t *testing.T) {
	oldWorld := worldRoot
	defer func() { worldRoot = oldWorld }()
	worldRoot = exploreWorld(t)
	s := testServer(t, t.TempDir())
	s.claudeBin = stubClaude(t)

	// The refusals: silence, ground outside the fence, ground that is
	// not a directory, a perm outside the vocabulary.
	if _, serr := s.startSession(worldRoot, "  ", ""); serr == nil || serr.code != 400 {
		t.Fatalf("a session is born with words: %+v", serr)
	}
	if _, serr := s.startSession("/etc", "hi", ""); serr == nil || serr.code != 400 {
		t.Fatalf("outside the fence must refuse: %+v", serr)
	}
	if _, serr := s.startSession(filepath.Join(worldRoot, "loose.txt"), "hi", ""); serr == nil || serr.code != 404 {
		t.Fatalf("a file is not ground: %+v", serr)
	}
	if _, serr := s.startSession(worldRoot, "hi", "yolo"); serr == nil || serr.code != 400 {
		t.Fatalf("perm keeps its closed set: %+v", serr)
	}

	// The birth: ticket now, name shortly.
	id, serr := s.startSession(filepath.Join(worldRoot, "repo-a"), "what is this repo?", "read")
	if serr != nil {
		t.Fatal(serr.msg)
	}
	var j *birthJob
	for range 100 {
		j, serr = s.birth(id)
		if serr != nil {
			t.Fatal(serr.msg)
		}
		if j.Status != "thinking" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if j.Status != "born" || j.Root != "sess-born-1" {
		t.Fatalf("the ticket ends in a name: %+v", j)
	}
	if s.spend["sess-born-1"] == nil || s.spend["sess-born-1"].ClaudeUSD == 0 {
		t.Fatal("the first turn's cost lands on the newborn")
	}
	if _, serr := s.birth("stranger"); serr == nil || serr.code != 404 {
		t.Fatalf("an unknown ticket is 404: %+v", serr)
	}
}

func TestExplorerRPCsCarryTheWholeLoop(t *testing.T) {
	oldWorld := worldRoot
	defer func() { worldRoot = oldWorld }()
	worldRoot = exploreWorld(t)
	s := testServer(t, t.TempDir())
	s.claudeBin = stubClaude(t)
	r := &veraRPC{s: s}

	b, err := r.Browse(context.Background(), connect.NewRequest(&roostv1.BrowseRequest{}))
	if err != nil || len(b.Msg.Dirs) != 2 || !b.Msg.Dirs[1].Git {
		t.Fatalf("browse carries the view: %+v %v", b, err)
	}
	st, err := r.StartSession(context.Background(), connect.NewRequest(&roostv1.StartSessionRequest{
		Cwd: b.Msg.Dirs[1].Cwd, Text: "hello", Perm: "read",
	}))
	if err != nil || st.Msg.BirthId == "" {
		t.Fatalf("the ticket rides the wire: %+v %v", st, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		bi, err := r.Birth(context.Background(), connect.NewRequest(&roostv1.BirthRequest{Id: st.Msg.BirthId}))
		if err != nil {
			t.Fatal(err)
		}
		if bi.Msg.Status == "born" {
			if bi.Msg.Root != "sess-born-1" {
				t.Fatalf("the birth names the session: %+v", bi.Msg)
			}
			break
		}
		if bi.Msg.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("the birth must land: %+v", bi.Msg)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := r.Browse(context.Background(), connect.NewRequest(&roostv1.BrowseRequest{Path: "/etc"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("an escape is invalid on the wire too: %v", err)
	}
}
