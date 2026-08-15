// The workspace registry: durable, named, described ground. The
// fleet's offer list was transient (sessions age out of the window)
// and anonymous (a path is thin identity) — so the planner, shown
// three plausible repos, had to ask which one was which. A bookmark
// answers that once: a name, a one-line note, and a path, offered to
// the planner as ground it can trust and to the board as a place
// work can always start.
//
// Two ways in: the owner bookmarks a directory from the explorer, and
// every workspace vera creates by plan registers itself with the
// plan's own why as its note.
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type bookmark struct {
	Name string    `json:"name"`
	Cwd  string    `json:"cwd"`
	Note string    `json:"note,omitempty"`
	At   time.Time `json:"at"`
}

// bookmarkStore is one JSON file of bookmarks, written whole and
// atomically — the registry is small and the truth is the file.
type bookmarkStore struct {
	path string // "" disables the registry
	mu   sync.Mutex
}

func defaultBookmarkPath() string {
	return statePath("vera-workspaces.json")
}

func (b *bookmarkStore) load() map[string]bookmark {
	out := map[string]bookmark{}
	if b.path == "" {
		return out
	}
	raw, err := os.ReadFile(b.path)
	if err != nil {
		return out
	}
	json.Unmarshal(raw, &out)
	return out
}

func (b *bookmarkStore) save(m map[string]bookmark) error {
	if b.path == "" {
		return errors.New("the registry is off (no state directory)")
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}

// list answers the bookmarks sorted by name.
func (b *bookmarkStore) list() []bookmark {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.load()
	out := make([]bookmark, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// add registers (or re-registers — the newest description wins) one
// directory under a name.
func (b *bookmarkStore) add(name, cwd, note string) error {
	name = strings.TrimSpace(name)
	if !fileID(name) {
		return errors.New("a bookmark name is short and filename-shaped")
	}
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return errors.New("that directory is gone")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.load()
	m[name] = bookmark{Name: name, Cwd: cwd, Note: strings.TrimSpace(note), At: time.Now()}
	return b.save(m)
}

func (b *bookmarkStore) remove(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.load()
	if _, ok := m[name]; !ok {
		return errors.New("no bookmark by that name")
	}
	delete(m, name)
	return b.save(m)
}

// noteFor answers the bookmark describing cwd, if any.
func (b *bookmarkStore) noteFor(cwd string) (bookmark, bool) {
	for _, bm := range b.list() {
		if bm.Cwd == cwd {
			return bm, true
		}
	}
	return bookmark{}, false
}

// ---- the routes ----

func (s *server) handleBookmarkAdd(w http.ResponseWriter, r *http.Request) {
	defer s.hub.notify()
	var req struct {
		Name string `json:"name"`
		Cwd  string `json:"cwd"`
		Note string `json:"note"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	// The same fence the explorer walks.
	p, ok := underRoot(exploreRoot(), req.Cwd)
	if !ok {
		httpErr(w, 400, "bookmarks stay under "+exploreRoot())
		return
	}
	if err := s.marks.add(req.Name, p, req.Note); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "bookmarked", "cwd": p})
}

func (s *server) handleBookmarkRemove(w http.ResponseWriter, r *http.Request) {
	defer s.hub.notify()
	if err := s.marks.remove(r.PathValue("name")); err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "removed"})
}
