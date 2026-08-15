package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A pasted image round-trips: stored under the agent's namespace,
// served back by name; junk that is not an image is refused.
func TestUploadStoresAndServesAnImage(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "-repo-alpha", "sess-live", time.Now().Add(-time.Minute))
	s := testServer(t, dir)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agent/sess-live/upload", bytes.NewReader(tinyPNG(t)))
	req.SetPathValue("id", "sess-live")
	s.handleUpload(rec, req)
	if rec.Code != 200 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var ans struct{ Path, Name string }
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil || !strings.HasSuffix(ans.Name, ".png") {
		t.Fatalf("answer: %s", rec.Body.String())
	}

	get := httptest.NewRecorder()
	greq := httptest.NewRequest("GET", "/api/agent/sess-live/uploads/"+ans.Name, nil)
	greq.SetPathValue("id", "sess-live")
	greq.SetPathValue("name", ans.Name)
	s.handleUploadGet(get, greq)
	if get.Code != 200 || get.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("serve: %d %s", get.Code, get.Header().Get("Content-Type"))
	}

	bad := httptest.NewRecorder()
	breq := httptest.NewRequest("POST", "/api/agent/sess-live/upload", strings.NewReader("not an image at all"))
	breq.SetPathValue("id", "sess-live")
	s.handleUpload(bad, breq)
	if bad.Code != 400 {
		t.Fatalf("junk must be refused: %d", bad.Code)
	}
}

// A say may only attach files vera itself stored for that agent —
// arbitrary paths are refused before any turn starts.
func TestSayRefusesForeignImagePaths(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "-repo-alpha", "sess-live", time.Now().Add(-time.Minute))
	s := testServer(t, dir)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agent/sess-live/say",
		strings.NewReader(`{"text":"look","direct":true,"images":["/etc/passwd"]}`))
	req.SetPathValue("id", "sess-live")
	s.handleSay(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "attachment") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWithImagesAppendsTheMarkers(t *testing.T) {
	got := withImages("fix it", []string{"/a/img-1.png", "/a/img-2.png"})
	if !strings.Contains(got, "[image attached: /a/img-1.png — read this file to see it]") ||
		!strings.Contains(got, "img-2.png") || !strings.HasPrefix(got, "fix it") {
		t.Fatalf("got: %q", got)
	}
	if withImages("bare", nil) != "bare" {
		t.Fatal("no images must mean no markers")
	}
}

// Orphaned attachments go with their agents: a directory whose newest
// file predates the scan window is unreachable and is removed; a live
// agent's images stay.
func TestPruneUploadsSweepsOnlyUnreachableAgents(t *testing.T) {
	s := testServer(t, t.TempDir())

	old := filepath.Join(s.uploads, "agent-old")
	live := filepath.Join(s.uploads, "agent-live")
	for _, d := range []string{old, live} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stale := time.Now().Add(-72 * time.Hour)
	for path, at := range map[string]time.Time{
		filepath.Join(old, "img-1.png"):  stale,
		filepath.Join(live, "img-2.png"): time.Now(),
	} {
		if err := os.WriteFile(path, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}

	s.pruneUploads(48 * time.Hour)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("the orphaned directory must be gone")
	}
	if _, err := os.Stat(filepath.Join(live, "img-2.png")); err != nil {
		t.Fatal("the live agent's image must stay")
	}
}
