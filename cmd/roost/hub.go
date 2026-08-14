// The hub: one broadcast of "something changed, look again". The
// transcript watcher feeds it from disk; the server's own mutations
// (says landing, queues draining, digests finishing) feed it
// directly. Subscribers get a coalesced poke and recompute their own
// view — the hub carries no payload, so it can never carry a stale
// one.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type hub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func newHub() *hub {
	return &hub{subs: map[chan struct{}]struct{}{}}
}

// subscribe returns a poke channel (capacity 1 — pokes coalesce) and
// its cancel.
func (h *hub) subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// notify pokes every subscriber without blocking on any. Safe on a
// nil hub so test fixtures need not carry one.
func (h *hub) notify() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default: // a poke is already waiting; one is plenty
		}
	}
}

// watch follows the transcript tree: every project directory under
// dir, plus new ones as they appear. Writes to .jsonl files debounce
// into pokes. Errors degrade to silence — subscribers keep their own
// slow tick as the safety net.
func (h *hub) watch(dir string) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer w.Close()
	_ = w.Add(dir)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				_ = w.Add(filepath.Join(dir, e.Name()))
			}
		}
	}
	var timer *time.Timer
	h.mu.Lock()
	pending := false
	h.mu.Unlock()
	fire := func() {
		h.mu.Lock()
		pending = false
		h.mu.Unlock()
		h.notify()
	}
	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					_ = w.Add(ev.Name)
					continue
				}
			}
			if !strings.HasSuffix(ev.Name, ".jsonl") {
				continue
			}
			h.mu.Lock()
			if !pending {
				pending = true
				timer = time.AfterFunc(120*time.Millisecond, fire)
				_ = timer
			}
			h.mu.Unlock()
		case _, ok := <-w.Errors:
			if !ok {
				return
			}
		}
	}
}
