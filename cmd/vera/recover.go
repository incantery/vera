// Reconcile and recover: the two systems that make a run's death
// survivable without a human standing by.
//
// Reconcile keeps the board truthful: a card claiming "turn in
// flight" whose run does not exist (vera restarted mid-drive) is
// folded back to waiting, wearing a transient stop record. It runs
// first in the roster so recover reads a world that is already honest.
//
// Recover restarts the machinery deaths: a card stopped by something
// transient (a killed process, the clock, the wire) is retried — at
// most twice, with backoff, and never after the owner has touched the
// card, since a reply or a restart resets the budget. Everything the
// system remembers lives on the card itself; a vera restart forgets
// nothing.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/incantery/vera/transcript"
)

// maxAutoRetries bounds what recovery may spend on one card before
// the failure honestly belongs to the owner.
const maxAutoRetries = 2

// reconcileGrace is how stale a runless in-flight card must be before
// it is folded: long enough that a card mutated moments before its
// run registers is never mistaken for an orphan.
const reconcileGrace = 90 * time.Second

type reconcileSystem struct{ s *server }

func (reconcileSystem) Name() string { return "reconcile" }

func (r reconcileSystem) Tick(w *World) []Action {
	live := map[string]bool{}
	for _, rn := range w.Runs {
		if !rn.Finished && rn.TaskID != "" {
			live[rn.TaskID] = true
		}
	}
	var out []Action
	for _, t := range w.Tasks {
		// Drive-shaped progress cards only: adopted cards mirror a live
		// session and close themselves; a card without a goal was never
		// started by a run.
		if t.Col != "progress" || t.Adopted || t.Goal == "" {
			continue
		}
		if live[t.ID] || w.Now.Sub(t.UpdatedAt) < reconcileGrace {
			continue
		}
		id := t.ID
		out = append(out, Action{
			Key: "reconcile/" + id, TaskID: id, Free: true,
			Reason: "fold an orphaned in-flight card back to waiting",
			Run:    func() { r.s.reconcileTask(id) },
		})
	}
	return out
}

// reconcileTask folds one orphan: the card said a run was in flight,
// no run exists, so the card stops claiming it. The stop record is
// transient — a vanished run is machinery, and recover takes it from
// here.
func (s *server) reconcileTask(id string) {
	defer s.hub.notify()
	now := time.Now()
	s.tasks.mutate(id, func(t *task) error {
		if t.Col != "progress" {
			return errors.New("already moved")
		}
		t.Col, t.State = "waiting", "waiting · the run stopped"
		t.StopErr, t.StopTransient = "the run vanished — vera restarted while it was in flight", true
		t.Ask = "The run vanished (vera restarted mid-flight). Vera will retry; you can also start it yourself."
		t.Face = "The run vanished; recovering."
		t.event("vera", "the card claimed a run in flight but none exists — folded back to waiting", now)
		return nil
	})
}

type recoverSystem struct{ s *server }

func (recoverSystem) Name() string { return "recover" }

func (r recoverSystem) Tick(w *World) []Action {
	var out []Action
	for _, t := range w.Tasks {
		if t.Col != "waiting" || !t.StopTransient || t.StopErr == "" || t.Retries >= maxAutoRetries {
			continue
		}
		// Backoff by attempt: 30s before the first retry, 2m before the
		// second — counted from the card's last movement, which is when
		// the stop landed.
		wait := 30 * time.Second
		if t.Retries > 0 {
			wait = 2 * time.Minute
		}
		if w.Now.Sub(t.UpdatedAt) < wait {
			continue
		}
		id, attempt := t.ID, t.Retries+1
		out = append(out, Action{
			Key:    fmt.Sprintf("recover/%s/%d", id, attempt),
			TaskID: id,
			Reason: fmt.Sprintf("retry %d/%d after a transient stop", attempt, maxAutoRetries),
			Run:    func() { r.s.recoverTask(id) },
		})
	}
	return out
}

// recoverTask restarts one transiently-stopped card. Every guard is
// re-checked against the store — the world the action was proposed
// from is a tick old, and the owner may have moved first.
func (s *server) recoverTask(id string) {
	defer s.hub.notify()
	if s.llm == nil {
		return
	}
	now := time.Now()
	t, err := s.tasks.get(id)
	if err != nil || t.Col != "waiting" || !t.StopTransient || t.Retries >= maxAutoRetries {
		return
	}
	goal := t.Goal
	if goal == "" {
		goal = t.Intent
	}
	if goal == "" {
		return
	}
	stopErr := t.StopErr
	attempt := t.Retries + 1
	restart := func(t *task) error {
		t.Retries++
		t.Col, t.State = "progress", fmt.Sprintf("in progress · auto-recovering (retry %d of %d)", attempt, maxAutoRetries)
		t.Face = "The run died of machinery, not judgment. Vera started it again."
		t.Ask = ""
		t.event("vera", fmt.Sprintf("auto-recovering (retry %d/%d) — %s", attempt, maxAutoRetries, transcript.Snip(stopErr, 80)), now)
		return nil
	}

	if t.Agent == "" {
		// The death came before the birth: no agent exists yet, so the
		// recovery is the fresh-spawn path again, on the same ground.
		if t.Workspace == "" {
			return
		}
		if _, err := os.Stat(t.Workspace); err != nil {
			s.tasks.mutate(id, func(t *task) error {
				t.StopTransient = false
				t.event("vera", "cannot auto-recover — the workspace is gone: "+t.Workspace, now)
				return nil
			})
			return
		}
		nt, err := s.tasks.mutate(id, restart)
		if err != nil {
			return
		}
		s.spawnFresh(nt, nt.Workspace, nt.Mode, goal, 0)
		return
	}

	root, head := s.resolveAgent(t.Agent, now)
	if head == nil {
		s.tasks.mutate(id, func(t *task) error {
			t.StopTransient = false
			t.event("vera", "cannot auto-recover — the agent is gone from the window", now)
			return nil
		})
		return
	}
	s.mu.Lock()
	busy := s.drivingLocked(root)
	s.mu.Unlock()
	if busy {
		return
	}
	nt, err := s.tasks.mutate(id, restart)
	if err != nil {
		return
	}
	// With exchanges on the record the drive continues; the worker is
	// told plainly why it is being spoken to again. Without any, the
	// drive simply opens with the goal.
	reply := ""
	if len(nt.Exchanges) > 0 {
		reply = "The previous run stopped mid-turn from a machinery failure (" +
			transcript.Snip(stopErr, 120) + "). Pick up where you left off and continue toward the goal."
	}
	s.startTaskDrive(root, head, id, goal, nt.Mode, reply, nt.Exchanges)
}
