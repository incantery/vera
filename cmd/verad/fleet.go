// The fleet over the wire.
//
// A task is a room Vera opened for a coding agent. The phone and the
// mind see the same thing: the list, with what Vera believes about each
// one — running, waiting on you, finished — and what has been said
// since you last looked. What comes back up is spoken in the person's
// nouns, not the machinery's: a task, a decision, a result. Never a
// worktree, a pane, a wake.
package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/incantery/vera/fleet"
)

// fleetRoutes are mounted by lan.Serve when a fleet exists.
func (l *lanTransport) fleetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /fleet", l.fleetList)
	mux.HandleFunc("POST /fleet", l.fleetSpawn)
	mux.HandleFunc("POST /fleet/{id}/answer", l.fleetAnswer)
	mux.HandleFunc("POST /fleet/{id}/land", l.fleetLand)
	mux.HandleFunc("POST /fleet/{id}/teardown", l.fleetTeardown)
	mux.HandleFunc("POST /fleet/{id}/seen", l.fleetSeen)
	// Loopback, no secret: the harness's Stop hook and the agent's own
	// status line. They ring a bell and say a word; the supervisor
	// re-reads the truth either way.
	mux.HandleFunc("POST /fleet/{id}/turn-ended", loopbackOnly(l.fleetTurnEnded))
	mux.HandleFunc("POST /fleet/{id}/status", loopbackOnly(l.fleetStatus))
}

func (l *lanTransport) fleetList(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	views, err := l.fleet.Tasks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if views == nil {
		views = []fleet.View{}
	}
	writeJSON(w, views)
}

func (l *lanTransport) fleetSpawn(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req fleet.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	t, err := l.fleet.Spawn(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, t)
}

func (l *lanTransport) fleetAnswer(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := l.fleet.Answer(r.Context(), r.PathValue("id"), body.Text); err != nil {
		http.Error(w, err.Error(), fleetStatusCode(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (l *lanTransport) fleetLand(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := l.fleet.Land(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), fleetStatusCode(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (l *lanTransport) fleetTeardown(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	force := r.URL.Query().Get("force") == "1"
	if err := l.fleet.Teardown(r.Context(), r.PathValue("id"), force); err != nil {
		http.Error(w, err.Error(), fleetStatusCode(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fleetSeen: the phone rendered the log this far. What follows is
// "what changed" next time.
func (l *lanTransport) fleetSeen(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	all, err := l.fleet.Store.Statuses(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := l.fleet.Store.Present(id, len(all)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (l *lanTransport) fleetTurnEnded(w http.ResponseWriter, r *http.Request) {
	if err := l.fleet.TurnEnded(r.PathValue("id"), r.URL.Query().Get("incarnation")); err != nil {
		http.Error(w, err.Error(), fleetStatusCode(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fleetStatus is the agent's own word: `curl -d '{"verb":"blocked",
// "text":"which API?"}' localhost:4780/fleet/ID/status`. The brief tells
// it the six verbs.
func (l *lanTransport) fleetStatus(w http.ResponseWriter, r *http.Request) {
	var st fleet.Status
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&st); err != nil || st.Verb == "" {
		http.Error(w, "bad status", http.StatusBadRequest)
		return
	}
	st.At, st.By = time.Now(), "agent"
	if err := l.fleet.Report(r.PathValue("id"), st); err != nil {
		http.Error(w, err.Error(), fleetStatusCode(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func fleetStatusCode(err error) int {
	if err == fleet.ErrNoTask {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

// fleetObservation turns a change of belief into the same kind of
// event the Mac app sends, so the mind's preface and /status carry it.
func fleetObservation(device string, ev fleet.Event) Observation {
	fields := map[string]json.RawMessage{}
	put := func(k string, v any) {
		if b, err := json.Marshal(v); err == nil {
			fields[k] = b
		}
	}
	put("task", ev.Task.ID)
	put("name", ev.Task.Name)
	put("project", shortPath(ev.Task.Project))
	put("state", string(ev.State))
	if ev.Prev != "" {
		put("prev", string(ev.Prev))
	}
	put("brief", trim(ev.Task.Brief, 200))
	return Observation{Type: "task." + string(ev.State), Device: device, Source: "fleet", At: ev.At, Fields: fields}
}
