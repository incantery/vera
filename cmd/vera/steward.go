// The steward system: the engine's one thinking pass. Every tick the
// mechanical systems keep the board truthful; on a slower clock, and
// only when the board has actually changed, this one hands the whole
// board to the vera agent and asks what an attentive manager would
// say. What comes back is advice — DONE and START land as proposals
// the owner accepts or declines, NOTE lands as a line on the card's
// log — and the guards here decide which advice survives. The steward
// spends real money, so it rides the engine's rate gate, waits out a
// cooldown, and fingerprints the board so an unchanged wall of cards
// is never billed twice.
package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/drive"
	"github.com/incantery/vera/transcript"
)

// stewardCooldown is the least quiet between two steward reads of a
// merely-changed board. stewardFastLane is the shorter wait when a
// card newly enters "waiting on the owner" — an escalation should
// meet its drafted answer in minutes, not at the next half hour.
const (
	stewardCooldown = 30 * time.Minute
	stewardFastLane = 3 * time.Minute
)

type stewardSystem struct {
	s *server

	mu        sync.Mutex
	lastFP    uint64
	lastNeeds uint64
	lastAt    time.Time
}

func (*stewardSystem) Name() string { return "steward" }

func (st *stewardSystem) Tick(w *World) []Action {
	if st.s.llm == nil {
		return nil
	}
	board, fp := stewardBoard(w)
	if board == "" {
		return nil
	}
	needs := needsFingerprint(w)
	st.mu.Lock()
	cool := stewardCooldown
	if needs != st.lastNeeds {
		cool = stewardFastLane
	}
	quiet := w.Now.Sub(st.lastAt) >= cool
	fresh := fp != st.lastFP
	st.mu.Unlock()
	if !quiet || !fresh {
		return nil
	}
	return []Action{{
		Key:    fmt.Sprintf("steward/%x", fp),
		Reason: "read the board and propose next moves",
		Run:    func() { st.read(board, fp, needs) },
	}}
}

// read is one steward pass: stamp the clock first (a failed read
// still spent the attempt — retrying every tick would be laps), ask,
// guard, apply. It answers with how many moves survived the guards —
// the scheduled path ignores it; the owner's "look now" reads it back.
func (st *stewardSystem) read(board string, fp, needs uint64) (int, error) {
	st.mu.Lock()
	st.lastFP, st.lastNeeds, st.lastAt = fp, needs, time.Now()
	st.mu.Unlock()
	ll := *st.s.llmFor(partSteward)
	ll.Spend = func(c float64) { st.s.addSpend("vera-steward", 0, c) }
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	moves, err := ll.Steward(ctx, board)
	if err != nil {
		return 0, err // the next changed board asks again; silence over spam
	}
	now := time.Now()
	applied := 0
	for _, mv := range moves {
		if st.s.applyStewardMove(mv, now) {
			applied++
		}
	}
	if applied > 0 {
		st.s.hub.notify()
	}
	return applied, nil
}

// lookNow is the owner's finger on the steward: read the board this
// second, cooldown and fingerprint be damned — the tap IS the
// authorization, so it spends outside the autonomy budget. The clocks
// still stamp, so the scheduled pass does not repeat the look.
func (st *stewardSystem) lookNow() (int, error) {
	w := st.s.world(time.Now())
	board, fp := stewardBoard(w)
	if board == "" {
		return 0, nil // an empty board is already stewarded
	}
	return st.read(board, fp, needsFingerprint(w))
}

// stewardBoard renders the open board for the steward's eyes and
// fingerprints what matters. The text carries ages for judgment; the
// fingerprint buckets them (6h) so the mere passing of minutes never
// looks like news. An empty board renders empty: nothing to steward.
func stewardBoard(w *World) (string, uint64) {
	open := make([]task, 0, len(w.Tasks))
	for _, t := range w.Tasks {
		if t.open() {
			open = append(open, t)
		}
	}
	if len(open) == 0 {
		return "", 0
	}
	sort.Slice(open, func(i, j int) bool { return open[i].ID < open[j].ID })
	var text, key strings.Builder
	text.WriteString("The board:\n")
	for _, t := range open {
		liveState := "no agent"
		if t.Agent != "" {
			liveState = "agent gone from the window"
			if live := w.Fleet[t.Agent]; live != nil {
				liveState = "agent " + string(live.State)
			}
		}
		age := w.Now.Sub(t.UpdatedAt)
		fmt.Fprintf(&text, "\n%s [%s] %q — %s, last moved %s ago",
			t.ID, t.State, t.Title, liveState, transcript.RelAge(age))
		// The card's substance, not just its shape: the goal it runs
		// toward, what the worker last said, the tail of its log — the
		// evidence a DONE must cite and an ANSWER must draw on.
		if t.Goal != "" {
			fmt.Fprintf(&text, "\n  goal: %s", transcript.Snip(t.Goal, 160))
		}
		if n := len(t.Exchanges); n > 0 {
			fmt.Fprintf(&text, "\n  the worker last said: %s", transcript.Snip(t.Exchanges[n-1].Reply, 280))
		}
		for _, ev := range logTail(t.Log, 2) {
			fmt.Fprintf(&text, "\n  log: %s — %s", ev.Actor, transcript.Snip(ev.Text, 110))
		}
		if t.Ask != "" {
			fmt.Fprintf(&text, "\n  waiting on the owner: %s", transcript.Snip(t.Ask, 200))
		}
		if t.Proposal != "" {
			fmt.Fprintf(&text, "\n  proposal already pending: %s", t.Proposal)
		}
		if t.Retries > 0 {
			fmt.Fprintf(&text, "\n  auto-recovered %d time(s)", t.Retries)
		}
		if t.Deadline != "" {
			fmt.Fprintf(&text, "\n  deadline: %s", t.Deadline)
		}
		if t.Cadence == "standing" {
			text.WriteString("\n  a standing need — it recurs")
		}
		if t.BudgetUSD > 0 {
			fmt.Fprintf(&text, "\n  autopilot: $%.2f of $%.2f spent — the driver continues it; do not steer", t.CostUSD, t.BudgetUSD)
		}
		fmt.Fprintf(&key, "%s|%s|%s|%s|%s|%d|%d\n",
			t.ID, t.Col, t.State, t.Ask, t.ProposalKind, t.Retries, int(age/(6*time.Hour)))
	}
	h := fnv.New64a()
	h.Write([]byte(key.String()))
	return text.String(), h.Sum64()
}

// logTail: the last n log entries, oldest first.
func logTail(log []taskEvent, n int) []taskEvent {
	if len(log) <= n {
		return log
	}
	return log[len(log)-n:]
}

// needsFingerprint names the set of cards waiting on the owner with
// nothing yet proposed — the moments proactivity is FOR. When this
// set changes, the steward gets the fast lane instead of the full
// cooldown: an escalation should meet its drafted answer in minutes.
func needsFingerprint(w *World) uint64 {
	h := fnv.New64a()
	for _, t := range w.Tasks {
		if t.Col == "waiting" && t.Ask != "" && t.Proposal == "" {
			h.Write([]byte(t.ID + "|"))
		}
	}
	return h.Sum64()
}

// handleStewardLook is the refresh button's rail: the owner asks vera
// to go check right now. Synchronous on purpose — the answer ("I
// moved 2 things" / "nothing to move") is the point of pressing it.
func (s *server) handleStewardLook(w http.ResponseWriter, r *http.Request) {
	if s.llm == nil {
		httpErr(w, 409, s.notice)
		return
	}
	if s.steward == nil {
		httpErr(w, 409, "the steward is not running")
		return
	}
	applied, err := s.steward.lookNow()
	if err != nil {
		httpErr(w, 502, "the look failed: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"applied": applied})
}

// applyStewardMove is the guardhouse: advice enters, only what the
// board's own rules allow comes out the other side. A refusal is
// silent — bad advice costs nothing and teaches nothing by failing
// loudly.
func (s *server) applyStewardMove(mv drive.StewardMove, now time.Time) bool {
	t, err := s.tasks.get(mv.Task)
	if err != nil || !t.open() {
		return false
	}
	// An autopilot card belongs to the driver while its budget lasts:
	// the steward may observe (NOTE) but not steer — a parked proposal
	// would only be cleared by the next burst anyway.
	if t.BudgetUSD > 0 && t.CostUSD < t.BudgetUSD && mv.Verb != "note" {
		return false
	}
	switch mv.Verb {
	case "done":
		// Only work that ran can be read as finished, and a pending
		// proposal or a run in flight means this moment is spoken for.
		if t.Col == "inbox" || t.Proposal != "" {
			return false
		}
		s.mu.Lock()
		busy := false
		for _, r := range s.runs {
			if r.TaskID == t.ID && !r.Finished {
				busy = true
			}
		}
		s.mu.Unlock()
		if busy {
			return false
		}
		why := mv.Why
		if why == "" {
			why = "the card's state reads as finished."
		}
		_, err := s.tasks.mutate(t.ID, func(t *task) error {
			t.Proposal, t.ProposalWhy, t.ProposalKind = "Move to done",
				"The steward reads this as finished: "+why+" Irreversible — yours to confirm.", "done"
			t.event("vera", "steward: proposed done — "+transcript.Snip(why, 100), now)
			return nil
		})
		return err == nil
	case "start":
		if t.Col != "inbox" || t.Proposal != "" || t.AutoStart != "" {
			return false
		}
		why := mv.Why
		if why == "" {
			why = "its moment has come."
		}
		// A card that already names registered ground may start itself —
		// read mode only: analysis cannot mutate, so the worst case is
		// bounded spend, and the ignite system pays it from the same
		// autonomy budget. A card with no named ground stays a proposal;
		// picking ground is the owner's.
		if t.Workspace != "" && s.registeredGround(t.Workspace) {
			if _, statErr := os.Stat(t.Workspace); statErr == nil {
				_, err := s.tasks.mutate(t.ID, func(t *task) error {
					t.AutoStart = "read"
					t.State = "inbox · queued by the steward"
					t.event("vera", "steward: queued for read-only start — "+transcript.Snip(why, 100), now)
					return nil
				})
				return err == nil
			}
		}
		_, err := s.tasks.mutate(t.ID, func(t *task) error {
			t.Proposal, t.ProposalWhy, t.ProposalKind = "Start this next",
				"The steward proposes it: "+why, "start"
			t.event("vera", "steward: proposed starting — "+transcript.Snip(why, 100), now)
			return nil
		})
		return err == nil
	case "answer":
		// A drafted reply parks on the card as a proposal; nothing is
		// sent until the owner taps it, and the card shows the exact
		// words first. Only waiting cards with a live ask and an agent
		// to continue take one.
		if t.Col != "waiting" || t.Ask == "" || t.Proposal != "" || t.Agent == "" || mv.Why == "" {
			return false
		}
		_, err := s.tasks.mutate(t.ID, func(t *task) error {
			t.Proposal, t.ProposalKind = "Send Vera's reply", "reply"
			t.ProposalWhy = "Vera drafted an answer to the ask — send it as written, or answer yourself."
			t.ProposalText = mv.Why
			t.event("vera", "steward: drafted a reply — "+transcript.Snip(mv.Why, 100), now)
			return nil
		})
		return err == nil
	case "note":
		if mv.Why == "" {
			return false
		}
		// One steward observation per card per day: the log is a
		// record, and the same worry restated hourly is noise.
		for _, ev := range t.Log {
			if strings.HasPrefix(ev.Text, "steward: ") && now.Sub(ev.At) < 24*time.Hour {
				return false
			}
		}
		_, err := s.tasks.mutate(t.ID, func(t *task) error {
			t.event("vera", "steward: "+transcript.Snip(mv.Why, 160), now)
			return nil
		})
		return err == nil
	}
	return false
}
