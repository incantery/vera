// The event stream over the wire, and the sweep that fills it.
//
// Package events is the file format and the queries; this is verad's
// side of it — the recorder every reporter in this process writes to,
// the loop that goes looking for commits nobody reports, and the two
// doors on the outside.
//
// The doors are deliberately asymmetric. Reading is authed like the
// rest of the LAN surface, because the stream says what the person has
// been doing all week. Writing is loopback and no secret, exactly like
// the fleet's hooks: everything that publishes into it — rook, a
// Claude Code hook, a shell one-liner over `rook watch` — is a program
// on this Mac, and a secret those would have to carry is a secret that
// ends up in a dotfile.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/events"
	"github.com/incantery/vera/fleet"
)

// eventStream is the stream as verad holds it.
type eventStream struct {
	// Dir is the shard directory; `vera events` reads the same one
	// with no daemon in the way.
	Dir string
	// Rec is what everything in this process records through.
	Rec *events.Recorder
	// Repos says what to sweep for commits. Nil sweeps nothing, which
	// is the right answer for a verad with no fleet: it has no idea
	// what repositories exist.
	Repos func(ctx context.Context) []events.Repo
	// Every is the sweep cadence; zero means five minutes. Commits are
	// news for hours, so this is slow on purpose — the point is that
	// the stream is complete by the time anybody reads it, not that it
	// is complete within a second.
	Every time.Duration

	git *events.GitWatcher

	mu    sync.Mutex
	named map[string]string // repository root or worktree -> repo name
}

const sweepEvery = 5 * time.Minute

// newEventStream opens the stream under the state directory.
func newEventStream(dir string) *eventStream {
	s := &eventStream{Dir: dir, git: &events.GitWatcher{Dir: dir}, named: map[string]string{}}
	s.Rec = &events.Recorder{Log: &events.Log{Dir: dir}, RepoOf: s.repoOf}
	return s
}

// repoOf names the repository a path is in. A worktree resolves to its
// main checkout's name, so a task's events file under the repository
// they were about rather than under the branch they ran on. The answer
// is cached because it costs a git process and paths repeat all day.
func (s *eventStream) repoOf(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	s.mu.Lock()
	name, ok := s.named[path]
	s.mu.Unlock()
	if ok {
		return name
	}
	if r, err := fleet.FindRepo(path); err == nil {
		name = r.Name
	} else {
		name = filepath.Base(path)
	}
	s.mu.Lock()
	s.named[path] = name
	s.mu.Unlock()
	return name
}

// run sweeps for commits until ctx ends, starting with one sweep at
// once so a verad that has just come up is not an hour behind.
func (s *eventStream) run(ctx context.Context) {
	every := s.Every
	if every <= 0 {
		every = sweepEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *eventStream) sweep(ctx context.Context) {
	if s.Repos == nil {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	repos := s.Repos(sctx)
	if len(repos) == 0 {
		return
	}
	if evs := s.git.ScanAll(sctx, repos); len(evs) > 0 {
		s.Rec.Record(evs...)
		slog.Info("events: commits recorded", "n", len(evs))
	}
}

// projectRepos is Projects.Known in the shape the sweep wants.
func projectRepos(p *fleet.Projects) func(context.Context) []events.Repo {
	return func(ctx context.Context) []events.Repo {
		var out []events.Repo
		for _, r := range p.Known(ctx) {
			out = append(out, events.Repo{Name: r.Name, Root: r.Root})
		}
		return out
	}
}

// fleetEvents turns one thing the fleet noticed into the lines the
// stream should carry. Most kinds are one line; a state change that is
// only the weather — running, quiet — is none, and that judgement
// lives in package events beside the phrasing.
func fleetEvents(ev fleet.Event) []events.Event {
	t := ev.Task
	if t == nil {
		return nil
	}
	switch ev.Kind {
	case fleet.TaskSpawned:
		return []events.Event{events.Spawned(t.ID, t.Name, t.Project, t.Brief, string(t.Kind), ev.At)}
	case fleet.TaskLanded:
		return []events.Event{events.Landed(t.ID, t.Name, t.Project, landing(t), "", ev.At)}
	case fleet.LandFailed:
		return []events.Event{events.Landed(t.ID, t.Name, t.Project, landing(t), ev.Err, ev.At)}
	case fleet.TaskSaid:
		if ev.Said == nil {
			return nil
		}
		e, ok := events.Said(t.ID, t.Name, t.Project, string(ev.Said.Verb), ev.Said.Text, ev.Said.By, ev.At)
		if !ok {
			return nil
		}
		return []events.Event{e}
	case fleet.StateChanged:
		e, ok := events.Task(t.ID, t.Name, t.Project, t.Brief, string(ev.State), string(ev.Prev), ev.At)
		if !ok {
			return nil
		}
		return []events.Event{e}
	}
	return nil
}

// landing is how a task went home, in the person's word for it.
func landing(t *fleet.Task) string {
	switch {
	case t.Kind == fleet.Scout:
		return "closed"
	case t.Mode == fleet.DirectPR:
		return "as a PR"
	default:
		return "merged"
	}
}

// --- the wire -------------------------------------------------------------

// eventRoutes are mounted by lan.Serve when a stream exists.
func (l *lanTransport) eventRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /events", l.eventsList)
	mux.HandleFunc("POST /events", loopbackOnly(l.eventsPublish))
}

// defaultWindow is what `GET /events` means by "recently" when nobody
// says: a day, which is the span a person means by it when they come
// back to the machine.
const defaultWindow = 24 * time.Hour

func (l *lanTransport) eventsList(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	q := r.URL.Query()
	since := defaultWindow
	if s := q.Get("since"); s != "" {
		d, err := events.ParseSince(s)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		since = d
	}
	query := events.Query{
		Repo:   q.Get("repo"),
		Source: q.Get("source"),
		Kind:   q.Get("kind"),
		Task:   q.Get("task"),
		Text:   q.Get("q"),
	}
	if since > 0 {
		query.Since = time.Now().Add(-since)
	}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		query.Limit = n
	}
	evs, err := events.Read(l.events.Dir, query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch q.Get("format") {
	case "text", "markdown":
		d := events.Summarize(evs, query.Since, time.Now())
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if q.Get("format") == "markdown" {
			io.WriteString(w, d.Markdown())
			return
		}
		io.WriteString(w, d.Text())
	default:
		if evs == nil {
			evs = []events.Event{}
		}
		writeJSON(w, evs)
	}
}

// maxPublish bounds one publish. A publisher with more than this to
// say is writing a log, not an event stream.
const maxPublish = 256 << 10

// eventsPublish is the door for everything that is not verad: one JSON
// event, or a stream of them. Anything missing a source, a kind or a
// line of text is refused with the reason, because a publisher that is
// silently dropped writes into the void for months.
func (l *lanTransport) eventsPublish(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPublish))
	var in []events.Event
	for {
		var e events.Event
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			http.Error(w, "the body must be one JSON event, or one per line", http.StatusBadRequest)
			return
		}
		if err := e.Valid(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// A publisher names itself and says what happened; it does not
		// get to backdate. An "at" it sent is kept only when it is
		// plausible — inside the window this stream is about.
		if e.At.IsZero() || e.At.After(time.Now().Add(time.Minute)) {
			e.At = time.Now()
		}
		in = append(in, e)
	}
	if len(in) == 0 {
		http.Error(w, "nothing to record", http.StatusBadRequest)
		return
	}
	l.events.Rec.Record(in...)
	writeJSON(w, map[string]int{"recorded": len(in)})
}
