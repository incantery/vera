// Image attachments: the browser pastes, roost stores, the worker
// READS. No new protocol — a saved file plus one marker line in the
// prompt, and claude's own Read tool (granted in every policy tier)
// renders the image. The file is the truth; the transcript records
// the path.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const uploadMax = 12 << 20 // bytes; a pasted screenshot, not a photo library

var uploadExt = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
}

func defaultUploadsDir() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, _ := os.UserHomeDir()
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "rook", "roost-uploads")
}

// uploadDir is one agent's attachment directory, namespaced by root
// so serving by basename can never cross agents.
func (s *server) uploadDir(root string) string {
	return filepath.Join(s.uploads, root)
}

// handleUpload stores one pasted image: raw bytes in, sniffed for
// type, bounded, saved under the agent's namespace. The answer is the
// absolute path the prompt will carry and the name the UI serves back.
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	root, head := s.resolveAgent(r.PathValue("id"), now)
	if head == nil {
		httpErr(w, 404, "that agent is gone from the window")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, uploadMax))
	if err != nil {
		httpErr(w, 400, "the image is too large — 12MB is the ceiling")
		return
	}
	kind := http.DetectContentType(body)
	ext, ok := uploadExt[kind]
	if !ok {
		httpErr(w, 400, "not an image roost knows ("+kind+") — png, jpeg, gif, webp")
		return
	}
	dir := s.uploadDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		httpErr(w, 500, "could not make the upload directory")
		return
	}
	name := fmt.Sprintf("img-%d%s", now.UnixNano(), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		httpErr(w, 500, "could not save the image")
		return
	}
	writeJSON(w, map[string]string{"path": path, "name": name})
}

// handleUploadGet serves a stored attachment back to the UI, by
// basename only — the agent's namespace is the whole universe.
func (s *server) handleUploadGet(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	root, head := s.resolveAgent(r.PathValue("id"), now)
	if head == nil {
		httpErr(w, 404, "that agent is gone from the window")
		return
	}
	name := r.PathValue("name")
	if name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		httpErr(w, 400, "just the name")
		return
	}
	http.ServeFile(w, r, filepath.Join(s.uploadDir(root), name))
}

// checkImages validates say attachments: every path must be a file
// roost itself stored for THIS agent — anything else is refused, not
// guessed at.
func (s *server) checkImages(root string, images []string) string {
	if len(images) > 4 {
		return "four images per message is plenty"
	}
	dir := s.uploadDir(root)
	for _, p := range images {
		clean := filepath.Clean(p)
		if filepath.Dir(clean) != dir {
			return "an attachment must be one roost stored for this agent"
		}
		if _, err := os.Stat(clean); err != nil {
			return "an attachment has gone missing from disk"
		}
	}
	return ""
}

// withImages appends the attachment markers the worker acts on: one
// line per image, each naming the file to read. The marker format is
// also what the UI recognizes to render thumbnails in the stream.
func withImages(text string, images []string) string {
	if len(images) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	for _, p := range images {
		b.WriteString("\n\n[image attached: " + p + " — read this file to see it]")
	}
	return b.String()
}
