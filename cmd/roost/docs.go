// Documentation on the shelf: the guide is exposed through the SAME
// mechanism and storage model as artifacts — one JSON document in the
// artifact store, under a dedicated "docs" namespace that no agent
// owns (fully separate from any agent's shelf and its drafts).
//
// The canonical source stays engine/GUIDE.md, and only there. At
// startup roost locates it from this file's own compile-time path —
// which resolves in a working checkout and in the module cache a
// `go run …@latest` build ran from — and mirrors it into the docs
// shelf when it differs. If the source cannot be found (a relocated
// binary), the shelf's last synced copy keeps serving: the reader
// never needs the repository.
package main

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	docsRoot   = "docs"
	guideDocID = "guide"
	guideTitle = "the step-by-step rook agent-guiding-Claude-Code testing guide"
)

// guideSourcePath names the canonical engine/GUIDE.md, resolved from
// this source file's location at compile time.
func guideSourcePath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "GUIDE.md"))
}

// syncGuide mirrors the canonical guide into the docs shelf, content
// preserved exactly. Quiet misses: an unreadable source leaves the
// last synced copy standing.
func (s *server) syncGuide() {
	src := guideSourcePath()
	if src == "" {
		return
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return
	}
	now := time.Now()
	if cur, err := s.shelf.get(docsRoot, guideDocID); err == nil {
		if cur.Content == string(b) {
			return // already current
		}
		cur.Content = string(b)
		cur.Title = guideTitle
		cur.UpdatedAt = now
		s.shelf.write(docsRoot, cur)
		return
	}
	s.shelf.write(docsRoot, artifact{
		ID: guideDocID, Title: guideTitle, Content: string(b),
		CreatedAt: now, UpdatedAt: now,
	})
}

// handleDocGet reads one document from the docs shelf — the artifact
// mechanism, pointed at the namespace documentation lives in.
func (s *server) handleDocGet(w http.ResponseWriter, r *http.Request) {
	a, err := s.shelf.get(docsRoot, r.PathValue("id"))
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	writeJSON(w, a)
}
