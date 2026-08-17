// The schedule: work that starts because its time came, not because
// someone was at the keyboard. An entry names ground (a workspace),
// an intent, and a when — one-shot ("at") or recurring ("every") —
// and the engine's schedule system turns a due entry into a board
// card and starts it, exactly the way an accepted chain step starts.
// The card is the record; the entry just says when to make one.
//
// Persistence follows the registry's pattern: one JSON file, written
// whole and atomically — the schedule is small and the truth is the
// file.
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/transcript"
)

type schedEntry struct {
	ID        string    `json:"id"` // S-<n>
	Title     string    `json:"title,omitempty"`
	Intent    string    `json:"intent"`
	Workspace string    `json:"workspace"`
	Mode      string    `json:"mode,omitempty"`  // read | work
	At        time.Time `json:"at"`              // when it is (first) due
	Every     string    `json:"every,omitempty"` // Go duration ("24h"); "" = once
	LastRun   time.Time `json:"lastRun"`
	LastTask  string    `json:"lastTask,omitempty"`
	Paused    bool      `json:"paused,omitempty"`
	PausedWhy string    `json:"pausedWhy,omitempty"` // why the engine stopped it
	CreatedAt time.Time `json:"createdAt"`
}

// due answers whether the entry owes a firing right now. A one-shot
// that has fired never owes again; a recurring entry owes when its
// interval has passed since the last firing.
func (e *schedEntry) due(now time.Time) bool {
	if e.Paused {
		return false
	}
	if e.LastRun.IsZero() {
		return !now.Before(e.At)
	}
	d, err := time.ParseDuration(e.Every)
	if err != nil || d <= 0 {
		return false
	}
	return !now.Before(e.LastRun.Add(d))
}

type schedStore struct {
	path string // "" disables the schedule
	mu   sync.Mutex
}

func defaultSchedulePath() string {
	return statePath("vera-schedule.json")
}

func (st *schedStore) load() []schedEntry {
	if st.path == "" {
		return nil
	}
	b, err := os.ReadFile(st.path)
	if err != nil {
		return nil
	}
	var out []schedEntry
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func (st *schedStore) save(entries []schedEntry) error {
	if st.path == "" {
		return errors.New("the schedule is off (no state directory)")
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(entries, "", "  ")
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

func (st *schedStore) list() []schedEntry {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := st.load()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (st *schedStore) add(e schedEntry) (schedEntry, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	entries := st.load()
	max := 0
	for _, x := range entries {
		if n, err := strconv.Atoi(strings.TrimPrefix(x.ID, "S-")); err == nil && n > max {
			max = n
		}
	}
	e.ID = "S-" + strconv.Itoa(max+1)
	entries = append(entries, e)
	return e, st.save(entries)
}

func (st *schedStore) remove(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	entries := st.load()
	keep := entries[:0]
	for _, x := range entries {
		if x.ID != id {
			keep = append(keep, x)
		}
	}
	if len(keep) == len(entries) {
		return errors.New("no such entry")
	}
	return st.save(keep)
}

// mutate rewrites one entry under the lock; the whole file is the
// transaction.
func (st *schedStore) mutate(id string, f func(*schedEntry)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	entries := st.load()
	for i := range entries {
		if entries[i].ID == id {
			f(&entries[i])
			return st.save(entries)
		}
	}
	return errors.New("no such entry")
}

// ---- the system ----

type scheduleSystem struct{ s *server }

func (scheduleSystem) Name() string { return "schedule" }

func (r scheduleSystem) Tick(w *World) []Action {
	var out []Action
	for _, e := range w.Schedule {
		if !e.due(w.Now) {
			continue
		}
		id := e.ID
		// The key carries the firing's epoch (the last run it follows),
		// so one due moment launches once however many ticks see it.
		out = append(out, Action{
			Key:    "sched/" + id + "/" + strconv.FormatInt(e.LastRun.Unix(), 10),
			Reason: "scheduled work is due (" + id + ")",
			Run:    func() { r.s.fireSchedule(id) },
		})
	}
	return out
}

// fireSchedule turns one due entry into a card and starts it. The
// entry is re-checked under the store — the proposing world is a tick
// old — and LastRun is stamped before the start so a slow compile
// cannot double-fire.
func (s *server) fireSchedule(id string) {
	defer s.hub.notify()
	now := time.Now()
	var fire *schedEntry
	for _, e := range s.sched.list() {
		if e.ID == id {
			e := e
			fire = &e
			break
		}
	}
	if fire == nil || !fire.due(now) {
		return
	}
	if _, err := os.Stat(fire.Workspace); err != nil {
		// The ground is gone; the entry pauses and says so, and the
		// schedule page offers the resume once the workspace is back.
		s.sched.mutate(id, func(e *schedEntry) {
			e.Paused, e.PausedWhy, e.LastRun = true, "workspace missing at fire time: "+e.Workspace, now
		})
		return
	}
	title := fire.Title
	if title == "" {
		title = transcript.Snip(fire.Intent, 90)
	}
	mode := fire.Mode
	if mode != "work" {
		mode = "read"
	}
	t := task{
		Title: title, Intent: fire.Intent,
		Workspace: fire.Workspace, Mode: mode,
		Col: "inbox", State: "inbox · scheduled",
		Face:      "Born of schedule " + id + " — its time came.",
		CreatedAt: now, UpdatedAt: now,
	}
	t.event("vera", "born of schedule "+id+" ("+scheduleWhen(fire)+")", now)
	t, err := s.tasks.create(t)
	if err != nil {
		return
	}
	s.sched.mutate(id, func(e *schedEntry) { e.LastRun, e.LastTask = now, t.ID })
	if s.llm == nil {
		s.tasks.mutate(t.ID, func(t *task) error {
			t.event("vera", "no vera-agent key — captured, not started", now)
			return nil
		})
		return
	}
	s.igniteCard(t.ID, mode, "its schedule came due", now)
}

func scheduleWhen(e *schedEntry) string {
	if e.Every != "" {
		return "every " + e.Every
	}
	return "once, at " + e.At.Format(time.RFC3339)
}

// ---- the routes ----

func (s *server) handleScheduleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"entries": s.sched.list()})
}

func (s *server) handleScheduleAdd(w http.ResponseWriter, r *http.Request) {
	defer s.hub.notify()
	var req struct {
		Title     string `json:"title"`
		Intent    string `json:"intent"`
		Workspace string `json:"workspace"`
		Mode      string `json:"mode"`
		At        string `json:"at"`    // RFC3339; "" with every = first firing after one interval
		Every     string `json:"every"` // Go duration ("24h"); "" = once
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	req.Intent = strings.TrimSpace(req.Intent)
	if req.Intent == "" {
		httpErr(w, 400, "say what needs doing")
		return
	}
	if req.Workspace == "" {
		httpErr(w, 400, "name the ground (workspace) the work runs on")
		return
	}
	if _, err := os.Stat(req.Workspace); err != nil {
		httpErr(w, 400, "that workspace does not exist")
		return
	}
	now := time.Now()
	e := schedEntry{
		Title: strings.TrimSpace(req.Title), Intent: req.Intent,
		Workspace: req.Workspace, Mode: req.Mode, Every: req.Every, CreatedAt: now,
	}
	if req.Every != "" {
		if d, err := time.ParseDuration(req.Every); err != nil || d < time.Minute {
			httpErr(w, 400, `"every" wants a Go duration of at least a minute, like "30m" or "24h"`)
			return
		}
	}
	switch {
	case req.At != "":
		at, err := time.Parse(time.RFC3339, req.At)
		if err != nil {
			httpErr(w, 400, `"at" wants RFC3339, like "2026-08-17T09:00:00Z"`)
			return
		}
		e.At = at
	case req.Every != "":
		d, _ := time.ParseDuration(req.Every)
		e.At = now.Add(d)
	default:
		httpErr(w, 400, `say when: "at" (RFC3339), "every" (duration), or both`)
		return
	}
	e, err := s.sched.add(e)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, e)
}

func (s *server) handleScheduleRemove(w http.ResponseWriter, r *http.Request) {
	defer s.hub.notify()
	if err := s.sched.remove(r.PathValue("id")); err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleScheduleResume un-pauses an entry the engine stopped. A
// one-shot re-arms whole (LastRun cleared, so a past "at" fires
// promptly — the work was owed); a recurring entry keeps its clock
// and fires one interval after the pause stamp.
func (s *server) handleScheduleResume(w http.ResponseWriter, r *http.Request) {
	defer s.hub.notify()
	err := s.sched.mutate(r.PathValue("id"), func(e *schedEntry) {
		e.Paused, e.PausedWhy = false, ""
		if e.Every == "" {
			e.LastRun = time.Time{}
		}
	})
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
