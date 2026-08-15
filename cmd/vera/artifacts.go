// The artifact shelf: documents that belong to an agent, beside the
// conversation rather than buried in it. A conversation is a stream; a
// spec, a plan, a design prompt is a THING — it gets edited, not
// re-said. The shelf is where the membrane's outputs will eventually
// accumulate (a drive's findings, a generated design prompt); for now
// it is a plain place to create, read, and edit.
//
// Persistence is one JSON file per artifact under
// <state>/vera/vera-artifacts/<agent-root>/<id>.json — inspectable
// with cat, greppable, atomically replaced on save. No database; a
// document you can only reach through an API is a document held
// hostage.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type artifact struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// artifactMeta is the list row: everything but the content.
type artifactMeta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Bytes     int       `json:"bytes"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type artifactStore struct {
	dir string // "" disables the shelf
}

func defaultArtifactsDir() string {
	return statePath("vera-artifacts")
}

// fileID: ids reach the filesystem, so anything beyond [A-Za-z0-9._-]
// is refused outright — same rule session ids live under.
func fileID(id string) bool {
	if id == "" || strings.HasPrefix(id, ".") {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

var errBadID = errors.New("that id is not filename-shaped")

func (st *artifactStore) path(root, id string) (string, error) {
	if st.dir == "" {
		return "", errors.New("the artifact shelf is off (no state directory)")
	}
	if !fileID(root) || !fileID(id) {
		return "", errBadID
	}
	return filepath.Join(st.dir, root, id+".json"), nil
}

func (st *artifactStore) list(root string) []artifactMeta {
	if st.dir == "" || !fileID(root) {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(st.dir, root))
	if err != nil {
		return nil
	}
	var out []artifactMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		a, err := st.get(root, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, artifactMeta{ID: a.ID, Title: a.Title, Bytes: len(a.Content), UpdatedAt: a.UpdatedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (st *artifactStore) get(root, id string) (artifact, error) {
	path, err := st.path(root, id)
	if err != nil {
		return artifact{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return artifact{}, errors.New("that artifact is gone")
	}
	var a artifact
	if json.Unmarshal(b, &a) != nil {
		return artifact{}, errors.New("that artifact did not parse")
	}
	return a, nil
}

func (st *artifactStore) create(root, title, content string, now time.Time) (artifact, error) {
	id := make([]byte, 5)
	rand.Read(id)
	a := artifact{
		ID: hex.EncodeToString(id), Title: strings.TrimSpace(title),
		Content: content, CreatedAt: now, UpdatedAt: now,
	}
	if a.Title == "" {
		a.Title = "untitled"
	}
	return a, st.write(root, a)
}

func (st *artifactStore) update(root, id, title, content string, now time.Time) (artifact, error) {
	a, err := st.get(root, id)
	if err != nil {
		return artifact{}, err
	}
	if t := strings.TrimSpace(title); t != "" {
		a.Title = t
	}
	a.Content = content
	a.UpdatedAt = now
	return a, st.write(root, a)
}

func (st *artifactStore) delete(root, id string) error {
	path, err := st.path(root, id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return errors.New("that artifact is gone")
	}
	return nil
}

// ---- the routes ----
//
// All keyed by the agent ROOT: an artifact belongs to the agent, not
// to whichever fork happens to be its head this hour.

func (s *server) handleArtifactList(w http.ResponseWriter, r *http.Request) {
	root := s.ln.rootOf(r.PathValue("id"))
	list := s.shelf.list(root)
	if list == nil {
		list = []artifactMeta{}
	}
	writeJSON(w, map[string]any{"artifacts": list})
}

func (s *server) handleArtifactGet(w http.ResponseWriter, r *http.Request) {
	root := s.ln.rootOf(r.PathValue("id"))
	a, err := s.shelf.get(root, r.PathValue("aid"))
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	writeJSON(w, a)
}

func (s *server) handleArtifactCreate(w http.ResponseWriter, r *http.Request) {
	root := s.ln.rootOf(r.PathValue("id"))
	var req struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<22)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	a, err := s.shelf.create(root, req.Title, req.Content, time.Now())
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, a)
}

func (s *server) handleArtifactUpdate(w http.ResponseWriter, r *http.Request) {
	root := s.ln.rootOf(r.PathValue("id"))
	var req struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<22)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	a, err := s.shelf.update(root, r.PathValue("aid"), req.Title, req.Content, time.Now())
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	writeJSON(w, a)
}

func (s *server) handleArtifactDelete(w http.ResponseWriter, r *http.Request) {
	root := s.ln.rootOf(r.PathValue("id"))
	if err := s.shelf.delete(root, r.PathValue("aid")); err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"})
}

// write replaces the file atomically — an editor mid-poll on the other
// side must see the old document or the new one, never half.
func (st *artifactStore) write(root string, a artifact) error {
	path, err := st.path(root, a.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
