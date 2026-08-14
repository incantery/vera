// Scratch workspaces: directories roost itself creates, offers, and
// deletes, so the web app can stage a place where nothing real is at
// stake without anyone touching a terminal. They live under ONE
// managed parent (~/roost-scratch) — creation and deletion are bounded
// there by construction, which is what makes a delete verb safe to
// expose to the page at all.
//
// Every scratch workspace is born with a SCRATCH.txt marker: it names
// the folder as roost's, and doubles as the standing "protected file"
// a demo can dare an agent to delete.
//
// A scratch workspace is NOT a sandbox. A drive there runs real tools
// under the real user; the isolation is social (nothing you care
// about lives there), not mechanical.
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const scratchMarker = "SCRATCH.txt"

const scratchMarkerText = `This scratch workspace was created by roost.
Nothing outside this folder depends on it; deleting the whole folder is
always safe. Agents driven here are asked to treat this file as
protected — deleting it requires explicit authorization from you.
`

func defaultScratchParent() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "roost-scratch")
}

type scratchStore struct {
	parent string // "" disables scratch workspaces
}

// list names the scratch workspaces that exist right now.
func (sc *scratchStore) list() []string {
	if sc.parent == "" {
		return nil
	}
	entries, err := os.ReadDir(sc.parent)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && fileID(e.Name()) {
			out = append(out, filepath.Join(sc.parent, e.Name()))
		}
	}
	return out
}

// has answers whether a cwd is one of ours — the offer check.
func (sc *scratchStore) has(cwd string) bool {
	for _, w := range sc.list() {
		if w == cwd {
			return true
		}
	}
	return false
}

func (sc *scratchStore) create(name string) (string, error) {
	if sc.parent == "" {
		return "", errors.New("scratch workspaces are off (no home directory)")
	}
	if !fileID(name) {
		return "", errBadID
	}
	path := filepath.Join(sc.parent, name)
	if _, err := os.Stat(path); err == nil {
		return "", errors.New("a scratch workspace named " + name + " already exists")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(path, scratchMarker), []byte(scratchMarkerText), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// remove deletes one scratch workspace, wholesale. Only names under
// the managed parent are reachable — fileID plus the fixed join is
// the fence.
func (sc *scratchStore) remove(name string) error {
	if sc.parent == "" || !fileID(name) {
		return errBadID
	}
	path := filepath.Join(sc.parent, name)
	if _, err := os.Stat(filepath.Join(path, scratchMarker)); err != nil {
		// No marker, no delete: roost only removes what roost made.
		return errors.New("that is not a roost scratch workspace")
	}
	return os.RemoveAll(path)
}

// ---- the routes ----

func (s *server) handleWorkspaceCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpErr(w, 400, "name the workspace")
		return
	}
	path, err := s.scratch.create(req.Name)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]string{"cwd": path, "dir": req.Name})
}

func (s *server) handleWorkspaceDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.scratch.remove(r.PathValue("name")); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "deleted", "at": time.Now().Format(time.RFC3339)})
}
