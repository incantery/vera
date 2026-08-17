// The engine: vera's own heartbeat, game-engine shaped. Every tick
// the world is read ONCE into a snapshot, a fixed roster of systems
// reads that same snapshot in order, and each system answers with
// intents — it never mutates anything itself. The engine executes the
// intents: deduped by key so a tick cannot double-fire what the last
// tick started, and rate-limited so autonomy has a ceiling a runaway
// system cannot argue with.
//
// Systems are stateless between ticks; whatever a system wants to
// remember (retry counts, last-fired times) lives in the durable
// records — the card, the schedule entry — where it is auditable and
// survives a restart for free. The engine accelerates everything
// between human decisions; it never crosses one. Acceptance stays
// yours.
package main

import (
	"sync"
	"time"

	"github.com/incantery/vera/transcript"
)

// World is one tick's honest snapshot. Nothing in it is live: every
// system reads the same world, and a system that acted last tick sees
// the consequences here, not in its own memory.
type World struct {
	Now      time.Time
	Tasks    []task
	Fleet    map[string]*transcript.Session // boardSessions' answer: vera's own
	Runs     []run                          // drives, in flight and landed
	Schedule []schedEntry
}

// A System reads the world and says what should happen. Proposing is
// free; acting is the engine's, behind the budget.
type System interface {
	Name() string
	Tick(w *World) []Action
}

// An Action is one intent with a dedupe handle. Key names the work,
// not the attempt ("recover/T-3/1"): an action already in flight
// under the same key is not launched twice. Free actions are pure
// bookkeeping (no LLM, no spend) and bypass the rate gate. Budgeted
// actions spend real money but from an authorization the owner set
// explicitly (a card's autopilot budget) — that budget is the gate,
// so the hourly one steps aside.
type Action struct {
	Key      string
	TaskID   string // the card the action belongs to, for the audit log; "" = none
	Reason   string // one honest line for the log
	Free     bool
	Budgeted bool
	Run      func()
}

type engine struct {
	s       *server
	systems []System
	every   time.Duration // the tick; pokes from the hub arrive between
	perHour int           // spending actions allowed per rolling hour; 0 = none

	mu       sync.Mutex
	inflight map[string]bool
	fired    []time.Time          // spending launches inside the window
	denied   map[string]time.Time // key -> when its denial was last logged
}

func newEngine(s *server, systems []System, perHour int) *engine {
	return &engine{
		s: s, systems: systems, every: 15 * time.Second, perHour: perHour,
		inflight: map[string]bool{}, denied: map[string]time.Time{},
	}
}

// loop is the heartbeat: a tick on the clock, plus a poke whenever
// the hub says the world changed — so recovery starts seconds after a
// run lands, not at the next scheduled beat. Ticking twice is safe by
// construction: systems are idempotent over the same world and the
// key dedupe absorbs the rest.
func (e *engine) loop() {
	poke, cancel := e.s.hub.subscribe()
	defer cancel()
	tick := time.NewTicker(e.every)
	defer tick.Stop()
	for {
		e.tick(time.Now())
		select {
		case <-poke:
		case <-tick.C:
		}
	}
}

func (e *engine) tick(now time.Time) {
	w := e.s.world(now)
	for _, sys := range e.systems {
		for _, a := range sys.Tick(w) {
			e.launch(a, now)
		}
	}
}

// launch runs one action if its key is idle and the budget allows.
// A denied action is told to the card once an hour, not every tick —
// the log is a record, not a metronome.
func (e *engine) launch(a Action, now time.Time) {
	if a.Run == nil || a.Key == "" {
		return
	}
	e.mu.Lock()
	if e.inflight[a.Key] {
		e.mu.Unlock()
		return
	}
	if !a.Free && !a.Budgeted {
		keep := e.fired[:0]
		for _, at := range e.fired {
			if now.Sub(at) < time.Hour {
				keep = append(keep, at)
			}
		}
		e.fired = keep
		if len(e.fired) >= e.perHour {
			lastTold := e.denied[a.Key]
			e.denied[a.Key] = now
			e.mu.Unlock()
			if a.TaskID != "" && now.Sub(lastTold) >= time.Hour {
				e.s.tasks.mutate(a.TaskID, func(t *task) error {
					t.event("vera", "wanted to act ("+a.Reason+") but the autonomy budget is spent this hour", now)
					return nil
				})
			}
			return
		}
		e.fired = append(e.fired, now)
	}
	e.inflight[a.Key] = true
	e.mu.Unlock()
	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.inflight, a.Key)
			e.mu.Unlock()
		}()
		a.Run()
	}()
}

// hygieneSystem is the old prune sweeper wearing the engine's shape:
// uploads outlive their agents and the usage collector's probe
// transcripts outlive their harvest, so both are swept at start and
// every few hours. The sweep is mechanical (free) and its cadence is
// the one clock this file keeps outside the records — a timestamp is
// not state worth a journal.
type hygieneSystem struct {
	s      *server
	dir    string // the transcripts dir the probes land in
	home   string
	window time.Duration

	mu   sync.Mutex
	last time.Time
}

func (*hygieneSystem) Name() string { return "hygiene" }

func (h *hygieneSystem) Tick(w *World) []Action {
	h.mu.Lock()
	due := w.Now.Sub(h.last) >= 6*time.Hour
	if due {
		h.last = w.Now
	}
	h.mu.Unlock()
	if !due {
		return nil
	}
	return []Action{{
		Key: "hygiene/sweep", Free: true, Reason: "sweep stale uploads and probe transcripts",
		Run: func() {
			h.s.pruneUploads(h.window)
			pruneProbes(h.dir, h.home, time.Now())
		},
	}}
}

// world reads the snapshot the systems share.
func (s *server) world(now time.Time) *World {
	w := &World{Now: now, Tasks: s.tasks.list(), Fleet: s.boardSessions(now)}
	s.mu.Lock()
	for _, r := range s.runs {
		w.Runs = append(w.Runs, *r)
	}
	s.mu.Unlock()
	if s.sched != nil {
		w.Schedule = s.sched.list()
	}
	return w
}
