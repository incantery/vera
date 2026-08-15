// The explorer: the last reason to open a terminal. Browse the
// machine's directories from the board, pick one, and say the first
// word — a fresh claude session takes its breath there and the direct
// cockpit opens on it. The fence is the same as everything else
// vera touches: the home directory, or the world when one is up.
//
// A birth is a job, not a request: first turns take as long as they
// take, so the POST answers with a ticket and the page (or the phone)
// watches it until the session has a name.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/incantery/vera/drive"
)

// exploreRoot is the browse fence: the world when one is up, the
// home directory otherwise.
func exploreRoot() string {
	if worldRoot != "" {
		return worldRoot
	}
	return homeDir()
}

// underRoot resolves a requested path against the fence: the cleaned
// absolute path when it sits under root, or refusal. Relative paths
// resolve from the root itself.
func underRoot(root, p string) (string, bool) {
	if root == "" {
		return "", false
	}
	if p == "" {
		return root, true
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
		return p, true
	}
	return "", false
}

// dirEntry is one row of the explorer: a directory, whether it is a
// git repo, and whether the fleet already knows it.
type dirEntry struct {
	Name  string `json:"name"`
	Cwd   string `json:"cwd"`
	Git   bool   `json:"git,omitempty"`
	Known bool   `json:"known,omitempty"`
}

// browseView is one directory's answer: where you are, how to go up,
// and what is underneath.
type browseView struct {
	Root   string     `json:"root"`
	Path   string     `json:"path"`
	Parent string     `json:"parent,omitempty"`
	Git    bool       `json:"git,omitempty"`
	Dirs   []dirEntry `json:"dirs"`
}

// listDirs walks one level. Directories only, dotfiles skipped — the
// explorer finds workspaces, it is not a file manager.
func (s *server) listDirs(path string, now time.Time) (*browseView, *sayErr) {
	root := exploreRoot()
	p, ok := underRoot(root, path)
	if !ok {
		return nil, &sayErr{400, "the explorer stays under " + root}
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, &sayErr{404, "cannot read that directory"}
	}
	known := map[string]bool{}
	for _, live := range s.boardSessions(now) {
		known[live.Cwd] = true
	}
	v := &browseView{Root: root, Path: p, Dirs: []dirEntry{}}
	if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
		v.Git = true
	}
	if p != root {
		v.Parent = filepath.Dir(p)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		cwd := filepath.Join(p, e.Name())
		_, gerr := os.Stat(filepath.Join(cwd, ".git"))
		v.Dirs = append(v.Dirs, dirEntry{Name: e.Name(), Cwd: cwd, Git: gerr == nil, Known: known[cwd]})
	}
	sort.Slice(v.Dirs, func(i, j int) bool { return v.Dirs[i].Name < v.Dirs[j].Name })
	return v, nil
}

// birthJob is one session being born: the first turn in flight, then
// either the session's name or what went wrong.
type birthJob struct {
	Cwd    string    `json:"cwd"`
	Text   string    `json:"text"`
	Status string    `json:"status"` // thinking | born | failed
	Root   string    `json:"root,omitempty"`
	Err    string    `json:"err,omitempty"`
	At     time.Time `json:"at"`
}

// startSession births a fresh claude in cwd with the owner's first
// message, verbatim — the explorer is direct mode from the first
// word. Answers with the birth ticket.
func (s *server) startSession(cwd, text, perm string) (string, *sayErr) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", &sayErr{400, "a session is born with its first message — say something"}
	}
	root := exploreRoot()
	p, ok := underRoot(root, cwd)
	if !ok {
		return "", &sayErr{400, "the explorer stays under " + root}
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		return "", &sayErr{404, "that directory is gone"}
	}
	permTools, permMode, permErr := permPolicy(perm)
	if permErr != "" {
		return "", &sayErr{400, permErr}
	}
	idb := make([]byte, 4)
	rand.Read(idb)
	id := hex.EncodeToString(idb)
	job := &birthJob{Cwd: p, Text: text, Status: "thinking", At: time.Now()}
	s.mu.Lock()
	s.births[id] = job
	s.mu.Unlock()
	s.hub.notify()
	go func() {
		turner := &drive.Headless{Bin: s.claudeBin, Dir: p,
			AllowedTools: permTools, PermissionMode: permMode}
		turn, err := turner.StartTurn(context.Background(), text)
		if turn.SessionID != "" {
			s.addSpend(turn.SessionID, turn.CostUSD, 0)
		}
		s.mu.Lock()
		if err != nil {
			job.Status, job.Err = "failed", err.Error()
		} else if turn.SessionID == "" {
			job.Status, job.Err = "failed", "the turn landed but named no session"
		} else {
			job.Status, job.Root = "born", turn.SessionID
		}
		s.mu.Unlock()
		s.hub.notify()
	}()
	return id, nil
}

func (s *server) birth(id string) (*birthJob, *sayErr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.births[id]; j != nil {
		cp := *j
		return &cp, nil
	}
	return nil, &sayErr{404, "no such birth"}
}

// ---- the routes ----

func (s *server) handleDirs(w http.ResponseWriter, r *http.Request) {
	v, serr := s.listDirs(r.URL.Query().Get("path"), time.Now())
	if serr != nil {
		httpErr(w, serr.code, serr.msg)
		return
	}
	writeJSON(w, v)
}

func (s *server) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cwd  string `json:"cwd"`
		Text string `json:"text"`
		Perm string `json:"perm"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	id, serr := s.startSession(req.Cwd, req.Text, req.Perm)
	if serr != nil {
		httpErr(w, serr.code, serr.msg)
		return
	}
	writeJSON(w, map[string]string{"id": id})
}

func (s *server) handleBirth(w http.ResponseWriter, r *http.Request) {
	j, serr := s.birth(r.PathValue("id"))
	if serr != nil {
		httpErr(w, serr.code, serr.msg)
		return
	}
	writeJSON(w, j)
}
