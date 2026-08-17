// The ignite system: the acting half of read-mode self-start. The
// steward thinks and marks (AutoStart on an inbox card); this system
// spends — each ignition is its own engine action, individually
// counted against the autonomy budget, so a steward that queues five
// cards cannot start five drives past the gate.
//
// The mark burns on the attempt, success or not: a failed compile
// must never become a per-tick billing loop. A board that still wants
// the card started will say so through the steward again.
package main

import "time"

type igniteSystem struct{ s *server }

func (igniteSystem) Name() string { return "ignite" }

func (r igniteSystem) Tick(w *World) []Action {
	var out []Action
	for _, t := range w.Tasks {
		if t.Col != "inbox" || t.AutoStart == "" {
			continue
		}
		id := t.ID
		out = append(out, Action{
			Key: "ignite/" + id, TaskID: id,
			Reason: "start the queued read-only analysis on " + id,
			Run:    func() { r.s.igniteQueued(id) },
		})
	}
	return out
}

// igniteQueued starts one queued card, re-checking against the store
// — the owner may have moved it since the mark landed.
func (s *server) igniteQueued(id string) {
	defer s.hub.notify()
	now := time.Now()
	t, err := s.tasks.get(id)
	if err != nil || t.Col != "inbox" || t.AutoStart == "" {
		return
	}
	// Burn the mark first; and whatever the mark claimed, autonomy
	// only ever takes the read tier.
	if _, err := s.tasks.mutate(id, func(t *task) error {
		t.AutoStart = ""
		return nil
	}); err != nil {
		return
	}
	s.igniteCard(id, "read", "the steward queued it", now)
}
