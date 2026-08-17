// The driver: the marathon tier's engine system. A card the owner
// gave an autopilot budget runs in the same short, judged bursts as
// any drive — but when a burst stops for a rollable reason (the turn
// budget, the per-run spend cap, a routine escalation), the driver
// continues it instead of waiting for a tap, until the card's spend
// meets the owner's dollar authorization. The budget IS the human
// boundary: circling still parks (more money does not fix a
// conversation going nowhere), machinery errors still belong to
// recover, DONE still becomes a proposal only the owner accepts, and
// work mode never qualifies — autopilot is read-only by construction.
package main

import (
	"fmt"
	"time"
)

// driverSettle is how long a stopped autopilot card rests before the
// next burst: long enough for the landing to finish writing, short
// enough that an hours-long review never idles meaningfully.
const driverSettle = 30 * time.Second

// budgetSpentState marks a card whose authorization ran out — the
// driver's terminal parking spot, and its own guard against
// re-proposing the park forever.
const budgetSpentState = "waiting · autopilot budget spent"

// rollable names the stops autopilot may roll through.
func rollable(reason string) bool {
	return reason == "turns" || reason == "spend-cap" || reason == "escalated"
}

type driverSystem struct{ s *server }

func (driverSystem) Name() string { return "driver" }

func (r driverSystem) Tick(w *World) []Action {
	live := map[string]bool{}
	for _, rn := range w.Runs {
		if !rn.Finished && rn.TaskID != "" {
			live[rn.TaskID] = true
		}
	}
	var out []Action
	for _, t := range w.Tasks {
		if t.BudgetUSD <= 0 || t.Col != "waiting" || t.State == budgetSpentState {
			continue
		}
		if !rollable(t.StopReason) || live[t.ID] || w.Now.Sub(t.UpdatedAt) < driverSettle {
			continue
		}
		id := t.ID
		out = append(out, Action{
			Key:    fmt.Sprintf("driver/%s/%d", id, len(t.Runs)),
			TaskID: id, Budgeted: true,
			Reason: fmt.Sprintf("autopilot: continue (spent $%.2f of $%.2f)", t.CostUSD, t.BudgetUSD),
			Run:    func() { r.s.driveOn(id) },
		})
	}
	return out
}

// driveOn rolls one autopilot card forward — or parks it for good
// when the authorization is spent. Every guard re-checks the store:
// the owner may have moved first, and their move always wins.
func (s *server) driveOn(id string) {
	defer s.hub.notify()
	if s.llm == nil {
		return
	}
	now := time.Now()
	t, err := s.tasks.get(id)
	if err != nil || t.BudgetUSD <= 0 || t.Col != "waiting" || !rollable(t.StopReason) {
		return
	}
	goal := t.Goal
	if goal == "" {
		goal = t.Intent
	}
	if goal == "" || t.Agent == "" {
		return
	}
	// The authorization check, against real metered spend. A sliver
	// under a dollar is not worth a burst that cannot finish a turn.
	if t.BudgetUSD-t.CostUSD < 0.5 {
		s.tasks.mutate(id, func(t *task) error {
			t.State = budgetSpentState
			t.Ask = fmt.Sprintf("The $%.2f autopilot budget is spent ($%.2f used) without a DONE. Raise it, take the wheel, or close the card?", t.BudgetUSD, t.CostUSD)
			t.Face = "Autopilot ran its authorization out; the log holds everything it found."
			t.event("vera", fmt.Sprintf("autopilot: budget spent — $%.2f of $%.2f used, parking for the owner", t.CostUSD, t.BudgetUSD), now)
			return nil
		})
		return
	}
	root, head := s.resolveAgent(t.Agent, now)
	if head == nil {
		return // recover's problem, or the owner's
	}
	s.mu.Lock()
	busy := s.drivingLocked(root)
	s.mu.Unlock()
	if busy {
		return
	}
	// The continuation prompt: a routine escalation gets the standing
	// answer the owner pre-authorized by setting a budget; the other
	// stops just keep going.
	reply := "Continue toward the goal."
	if t.StopReason == "escalated" {
		reply = "I'm not available to answer right now — use your best judgment, choose the option that serves the goal, and log open questions in your final summary instead of waiting on me. Continue."
	}
	stop := t.StopReason
	nt, err := s.tasks.mutate(id, func(t *task) error {
		t.Col = "progress"
		t.State = fmt.Sprintf("in progress · autopilot ($%.2f of $%.2f spent)", t.CostUSD, t.BudgetUSD)
		t.Ask = ""
		t.Face = "Autopilot is driving; the budget is the boundary."
		t.clearProposal()
		t.event("vera", fmt.Sprintf("autopilot: continuing past a %s stop ($%.2f of $%.2f spent)", stop, t.CostUSD, t.BudgetUSD), now)
		return nil
	})
	if err != nil {
		return
	}
	s.startTaskDrive(root, head, id, goal, "read", reply, nt.Exchanges)
}
