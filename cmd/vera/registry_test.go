package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/incantery/vera/drive"
)

func TestBookmarksAreDurableNamedGround(t *testing.T) {
	b := &bookmarkStore{path: filepath.Join(t.TempDir(), "vera-workspaces.json")}
	ground := t.TempDir()
	if err := b.add("vera", ground, "the vera product itself"); err != nil {
		t.Fatal(err)
	}
	if err := b.add("../evil", ground, "x"); err == nil {
		t.Fatal("a hostile name must refuse")
	}
	if err := b.add("gone", filepath.Join(ground, "missing"), "x"); err == nil {
		t.Fatal("a missing directory must refuse")
	}

	// A fresh store reads the same file: the registry survives restarts.
	b2 := &bookmarkStore{path: b.path}
	got := b2.list()
	if len(got) != 1 || got[0].Name != "vera" || got[0].Note != "the vera product itself" {
		t.Fatalf("the bookmark survives whole: %+v", got)
	}
	if bm, ok := b2.noteFor(ground); !ok || bm.Name != "vera" {
		t.Fatalf("noteFor finds the ground: %+v %v", bm, ok)
	}
	if err := b2.remove("vera"); err != nil {
		t.Fatal(err)
	}
	if len(b2.list()) != 0 {
		t.Fatal("removed means gone")
	}
}

func TestPlannerOffersCarryTheBookmarkNotes(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, dir)
	ground := t.TempDir()
	if err := s.marks.add("vera", ground, "the vera product itself (board, planner, explorer UI)"); err != nil {
		t.Fatal(err)
	}
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				// The model answers with the ANNOTATED line — the
				// trim must recover the pure path.
				"content": "KIND: repo\nWHERE: " + ground + " # vera: the vera product itself (board, planner, explorer UI)\nCADENCE: once\nGOAL: Write the todos.\nWHY: The note says this is vera.",
			}}},
		})
	}))
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	s.planPath = filepath.Join(t.TempDir(), "plans.jsonl")
	s.claudeBin = "false"

	rec, serr := s.planCore(context.Background(), "make some todos for vera", time.Now())
	if serr != nil {
		t.Fatal(serr.msg)
	}
	if !strings.Contains(gotBody, ground+" # vera: the vera product itself") {
		t.Fatalf("the offer must carry the note: %s", gotBody)
	}
	// The nod lands on the bookmarked ground even though no session
	// ever ran there — the registry is the durable offer.
	tk, serr := s.executePlanCore(rec.Plan, rec.ID, "", time.Now())
	if serr != nil {
		t.Fatalf("bookmarked ground is startable: %v", serr.msg)
	}
	if tk.Workspace != ground {
		t.Fatalf("the annotation is trimmed back to the path: %q", tk.Workspace)
	}
}

func TestPlanMadeWorkspacesSelfRegister(t *testing.T) {
	oldWorld := worldRoot
	defer func() { worldRoot = oldWorld }()
	worldRoot = t.TempDir()
	s := testServer(t, t.TempDir())
	srv := planWire(t)
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	s.planPath = filepath.Join(t.TempDir(), "plans.jsonl")
	s.claudeBin = "false"

	p := drive.Plan{Kind: "new", Home: "life", Name: "party-food", Cadence: "once",
		Goal: "Draft the menu.", Why: "A one-off event deserves its own workspace."}
	tk, serr := s.executePlanCore(p, "", "", time.Now())
	if serr != nil {
		t.Fatal(serr.msg)
	}
	bm, ok := s.marks.noteFor(tk.Workspace)
	if !ok || bm.Name != "party-food" || !strings.Contains(bm.Note, "one-off event") {
		t.Fatalf("ground vera makes, vera remembers: %+v %v", bm, ok)
	}
}

func TestBookmarkRoutesGuardTheFence(t *testing.T) {
	oldWorld := worldRoot
	defer func() { worldRoot = oldWorld }()
	worldRoot = t.TempDir()
	s := testServer(t, t.TempDir())
	if err := os.MkdirAll(filepath.Join(worldRoot, "repo-a"), 0o755); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.handleBookmarkAdd(rec, httptest.NewRequest("POST", "/api/bookmarks",
		strings.NewReader(`{"name":"a","cwd":"`+filepath.Join(worldRoot, "repo-a")+`","note":"n"}`)))
	if rec.Code != 200 {
		t.Fatalf("a fenced bookmark lands: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.handleBookmarkAdd(rec, httptest.NewRequest("POST", "/api/bookmarks",
		strings.NewReader(`{"name":"evil","cwd":"/etc","note":"n"}`)))
	if rec.Code != 400 {
		t.Fatalf("outside the fence must refuse: %s", rec.Body.String())
	}
	// The explorer view names marked ground and rides the registry.
	v, serr := s.listDirs(filepath.Join(worldRoot, "repo-a"), time.Now())
	if serr != nil || v.Marked != "a" || len(v.Bookmarks) != 1 {
		t.Fatalf("the view knows its name: %+v %v", v, serr)
	}
}
