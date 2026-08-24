// What was said a moment ago.
//
// Not memory. Memory is the thing that knows you across months and is
// the hard problem; this is the much smaller one of a conversation
// remembering its own last few turns, which is what makes "what about
// the second one?" mean anything.
//
// It lives on the MAC rather than the phone. Two reasons, and the
// second is the real one: the phone would have to resend the whole
// conversation on every exchange, and — when memory does arrive — the
// thing that remembers you has to be the thing that persists, and that
// is not a phone you might replace.
//
// It is held in memory and dies with the process. A conversation is a
// session and a restart ends it, which is the honest description of
// what this is and stops it from being mistaken for the memory system
// it is not.
package main

import (
	"sync"
	"time"
)

type turn struct {
	Role    string // "user" or "assistant"
	Content string
}

// History is every live conversation, bounded on all three axes that
// can run away: how long one gets, how many there are, and how long a
// forgotten one lingers.
type History struct {
	mu      sync.Mutex
	threads map[string]*thread

	// A window, because an unbounded conversation is an unbounded bill
	// and a slowly rising latency — the whole thing is resent every
	// single exchange.
	maxTurns int
	maxChars int
	idleFor  time.Duration
	maxLive  int
}

type thread struct {
	turns   []turn
	touched time.Time
}

func newHistory() *History {
	return &History{
		threads:  map[string]*thread{},
		maxTurns: 20, // ten exchanges
		maxChars: 24_000,
		idleFor:  6 * time.Hour,
		maxLive:  200,
	}
}

// recall returns the conversation so far. An empty id means no
// conversation — a caller with curl gets the stateless behaviour, which
// is a useful thing to be able to ask for.
func (h *History) recall(id string) []turn {
	if id == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.threads[id]
	if !ok {
		return nil
	}
	t.touched = time.Now()
	// A copy: the caller is about to build a request out of this while
	// another exchange may be appending to it.
	out := make([]turn, len(t.turns))
	copy(out, t.turns)
	return out
}

// remember records one completed exchange. Both halves or neither —
// a user turn stored without its answer would leave two user messages
// in a row, and the model reads that as the person repeating
// themselves.
func (h *History) remember(id, said, answered string) {
	if id == "" || said == "" || answered == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	t, ok := h.threads[id]
	if !ok {
		h.evict()
		t = &thread{}
		h.threads[id] = t
	}
	t.turns = append(t.turns, turn{"user", said}, turn{"assistant", answered})
	t.touched = time.Now()
	t.trim(h.maxTurns, h.maxChars)
}

// forget drops one conversation outright.
func (h *History) forget(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.threads, id)
}

func (h *History) size(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t, ok := h.threads[id]; ok {
		return len(t.turns)
	}
	return 0
}

// trim keeps the tail. Dropping from the front loses the oldest
// context, which is the right thing to lose — and turns come off in
// PAIRS, because a conversation starting with an answer to a question
// that is no longer there reads as a non sequitur.
func (t *thread) trim(maxTurns, maxChars int) {
	for len(t.turns) > maxTurns {
		t.turns = t.turns[2:]
	}
	for len(t.turns) > 2 && t.chars() > maxChars {
		t.turns = t.turns[2:]
	}
}

func (t *thread) chars() int {
	n := 0
	for _, turn := range t.turns {
		n += len(turn.Content)
	}
	return n
}

// evict clears out the abandoned. Called under the lock, on the way to
// creating a new thread — the only moment the map can grow.
func (h *History) evict() {
	cutoff := time.Now().Add(-h.idleFor)
	for id, t := range h.threads {
		if t.touched.Before(cutoff) {
			delete(h.threads, id)
		}
	}
	// Still too many: drop the stalest until there is room. A phone
	// that reinstalls mints a new id every launch, so this is a real
	// bound rather than a theoretical one.
	for len(h.threads) >= h.maxLive {
		var oldest string
		var when time.Time
		for id, t := range h.threads {
			if oldest == "" || t.touched.Before(when) {
				oldest, when = id, t.touched
			}
		}
		delete(h.threads, oldest)
	}
}
