// A run: work that outlives the connection that asked for it.
//
// The bug this exists to fix is that a delegated task died when the
// phone went away — a lift, a locked screen, a lock-up of the wifi —
// and a ten-minute task is exactly the kind you walk away from. The
// cause was ownership: the work belonged to the HTTP request, so the
// request ending ended the work.
//
// So the ownership is inverted. The RUN is the thing that exists; a
// connection is a view onto it, and any number of them can come and go
// while it proceeds. Frames are kept rather than merely forwarded,
// which is what lets a phone that missed the middle of an answer pick
// it up from where it stopped watching instead of from nothing.
package main

import (
	"context"
	"sync"
	"time"
)

type Run struct {
	ID string

	mu       sync.Mutex
	frames   []Frame
	done     bool
	finished time.Time
	// Closed and replaced on every append. A closed channel is the
	// cheapest broadcast there is, and readers need no registration.
	changed chan struct{}
}

func newRun(id string) *Run {
	return &Run{ID: id, changed: make(chan struct{})}
}

func (r *Run) append(f Frame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.frames = append(r.frames, f)
	if f.Done || f.Error != "" {
		r.done = true
		r.finished = time.Now()
	}
	close(r.changed)
	r.changed = make(chan struct{})
}

// finish marks a run over even if the handler never sent a terminal
// frame — a panic, a cancelled context, a bug. A watcher waiting on a
// run that will never speak again is the one state worse than an error.
func (r *Run) finish() {
	r.mu.Lock()
	over := r.done
	r.mu.Unlock()
	if !over {
		r.append(Frame{Error: "That stopped without finishing."})
	}
}

// follow emits frames from `from` onward and returns when the run is
// over or ctx ends. Several callers may follow the same run at once,
// and a caller leaving does not disturb it.
func (r *Run) follow(ctx context.Context, from int, emit func(Frame) error) error {
	for {
		r.mu.Lock()
		var batch []Frame
		if from < len(r.frames) {
			batch = append(batch, r.frames[from:]...)
			from = len(r.frames)
		}
		done := r.done
		changed := r.changed
		r.mu.Unlock()

		for _, f := range batch {
			if err := emit(f); err != nil {
				return err
			}
		}
		if done {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			// The watcher left. The run does not care.
			return nil
		}
	}
}

// Runs holds what is in flight and what recently finished.
//
// Finished runs are kept for a while on purpose: the phone that asked
// may be in a pocket, and the answer should still be there when it
// comes back. They are not kept forever — this is a delivery buffer,
// not a transcript. The transcript is on the phone.
type Runs struct {
	mu   sync.Mutex
	runs map[string]*Run

	keep time.Duration
	max  int
}

func newRuns() *Runs {
	return &Runs{runs: map[string]*Run{}, keep: 30 * time.Minute, max: 64}
}

func (rs *Runs) start() *Run {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.evict()
	run := newRun(token(8))
	rs.runs[run.ID] = run
	return run
}

func (rs *Runs) find(id string) *Run {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.runs[id]
}

// evict drops finished runs past their keep window, then the oldest
// finished ones if there are still too many. A run still in flight is
// never dropped — that would be the original bug wearing a hat.
func (rs *Runs) evict() {
	cutoff := time.Now().Add(-rs.keep)
	for id, r := range rs.runs {
		r.mu.Lock()
		stale := r.done && r.finished.Before(cutoff)
		r.mu.Unlock()
		if stale {
			delete(rs.runs, id)
		}
	}
	for len(rs.runs) >= rs.max {
		var oldest string
		var when time.Time
		for id, r := range rs.runs {
			r.mu.Lock()
			done, at := r.done, r.finished
			r.mu.Unlock()
			if !done {
				continue
			}
			if oldest == "" || at.Before(when) {
				oldest, when = id, at
			}
		}
		if oldest == "" {
			return // everything is still running; let it be
		}
		delete(rs.runs, oldest)
	}
}

func (rs *Runs) inFlight() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	n := 0
	for _, r := range rs.runs {
		r.mu.Lock()
		if !r.done {
			n++
		}
		r.mu.Unlock()
	}
	return n
}
