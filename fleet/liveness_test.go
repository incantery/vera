package fleet

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	th := Thresholds{Quiet: time.Minute, Stale: 10 * time.Minute}
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	cases := []struct {
		name string
		e    Evidence
		want State
	}{
		{"closed wins", Evidence{Now: now, Closed: true, PaneAlive: true}, Closed},
		{"no pane", Evidence{Now: now}, Gone},
		{"no pane but done: at rest, not gone", Evidence{Now: now, Last: &Status{Verb: Done}}, Finished},
		{"no pane but failed", Evidence{Now: now, Last: &Status{Verb: Failed}}, Broken},
		{"pane is a bare shell: the agent is gone", Evidence{Now: now, PaneAlive: true, AgentAlive: false, PaneActive: ago(time.Second)}, Gone},
		{"agent said done", Evidence{Now: now, PaneAlive: true, AgentAlive: true, Last: &Status{Verb: Done}}, Finished},
		{"agent said failed", Evidence{Now: now, PaneAlive: true, AgentAlive: true, Last: &Status{Verb: Failed}}, Broken},
		{"agent blocked", Evidence{Now: now, PaneAlive: true, AgentAlive: true, Last: &Status{Verb: Blocked}}, Decision},
		{"agent paused", Evidence{Now: now, PaneAlive: true, AgentAlive: true, Last: &Status{Verb: Paused}}, Held},
		{"resolved falls through to evidence", Evidence{Now: now, PaneAlive: true, AgentAlive: true, Last: &Status{Verb: Resolved}, PaneActive: ago(5 * time.Second)}, Running},
		{"pane just active", Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(10 * time.Second)}, Running},
		{"pane quiet but files moving", Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(5 * time.Minute), LastWrite: ago(20 * time.Second)}, Running},
		{"quiet", Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(3 * time.Minute)}, Quiet},
		{"stale", Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(30 * time.Minute)}, Stale},
		{"turn ended, nothing since", Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(2 * time.Minute), TurnEnded: ago(time.Minute)}, Waiting},
		{"turn ended but active since", Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(10 * time.Second), TurnEnded: ago(time.Minute)}, Running},
		{"turn ended, writes since", Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(5 * time.Minute), LastWrite: ago(10 * time.Second), TurnEnded: ago(time.Minute)}, Running},
		{"nothing known yet", Evidence{Now: now, PaneAlive: true, AgentAlive: true}, Quiet},

		// The machine, not the agent. A lid shut at midnight leaves
		// every task in the fleet perfectly silent, and none of that
		// silence is about the agents.
		{"just woke, nothing since: interrupted, not stale",
			Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(8 * time.Hour),
				Machine: Machine{Cause: CauseSleep, Went: ago(8 * time.Hour), Back: ago(30 * time.Second), Down: 8*time.Hour - 30*time.Second}},
			Interrupted},
		{"awake five minutes after eight hours asleep: five minutes of silence, not eight hours",
			Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(8 * time.Hour),
				Machine: Machine{Cause: CauseSleep, Went: ago(8 * time.Hour), Back: ago(5 * time.Minute), Down: 8*time.Hour - 5*time.Minute}},
			Quiet},
		{"awake half an hour and still nothing: worth a look after all",
			Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(8 * time.Hour),
				Machine: Machine{Cause: CauseSleep, Went: ago(8 * time.Hour), Back: ago(30 * time.Minute), Down: 7*time.Hour + 30*time.Minute}},
			Stale},
		{"still asleep",
			Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(2 * time.Hour),
				Machine: Machine{Away: true, Cause: CauseSleep, Went: ago(2 * time.Hour), Down: 2 * time.Hour}},
			Interrupted},
		{"offline right now",
			Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(20 * time.Minute),
				Machine: Machine{Away: true, Cause: CauseOffline, Went: ago(20 * time.Minute), Down: 20 * time.Minute}},
			Interrupted},
		{"back, and it stirred since: running",
			Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(10 * time.Second),
				Machine: Machine{Cause: CauseSleep, Went: ago(time.Hour), Back: ago(time.Minute), Down: 59 * time.Minute}},
			Running},
		{"the machine ended the turn: interrupted, not waiting on a person",
			Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(2 * time.Hour), TurnEnded: ago(2 * time.Hour),
				Machine: Machine{Cause: CauseSleep, Went: ago(2*time.Hour + time.Minute), Back: ago(30 * time.Second), Down: 2 * time.Hour}},
			Interrupted},
		{"a turn the machine ended, long since back: quiet, and never a question",
			Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(2 * time.Hour), TurnEnded: ago(2 * time.Hour),
				Machine: Machine{Cause: CauseSleep, Went: ago(2*time.Hour + time.Minute), Back: ago(5 * time.Minute), Down: 1*time.Hour + 55*time.Minute}},
			Quiet},
		{"the agent ended its turn before the machine left: still waiting on a person",
			Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(3 * time.Hour), TurnEnded: ago(3 * time.Hour),
				Machine: Machine{Cause: CauseSleep, Went: ago(2 * time.Hour), Back: ago(time.Minute), Down: 2 * time.Hour}},
			Waiting},
		{"the agent's own word outranks the machine",
			Evidence{Now: now, PaneAlive: true, AgentAlive: true, Last: &Status{Verb: Blocked}, PaneActive: ago(2 * time.Hour),
				Machine: Machine{Away: true, Cause: CauseSleep, Went: ago(2 * time.Hour)}},
			Decision},
		{"an absence long since over changes nothing",
			Evidence{Now: now, PaneAlive: true, AgentAlive: true, PaneActive: ago(30 * time.Minute),
				Machine: Machine{Cause: CauseSleep, Went: ago(20 * time.Hour), Back: ago(12 * time.Hour)}},
			Stale},
	}
	for _, c := range cases {
		if got := Classify(c.e, th); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func TestActionable(t *testing.T) {
	for _, s := range []State{Waiting, Stale, Decision, Finished, Broken, Gone} {
		if !s.Actionable() {
			t.Errorf("%s should be actionable", s)
		}
	}
	for _, s := range []State{Running, Quiet, Held, Closed, Interrupted} {
		if s.Actionable() {
			t.Errorf("%s should not be actionable", s)
		}
	}
}

func TestNewestWrite(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	mk := func(rel string, at time.Time) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
		os.Chtimes(p, at, at)
	}
	mk("a.go", old)
	mk("sub/b.go", old.Add(10*time.Minute))
	mk("node_modules/x/c.js", time.Now()) // pruned
	mk(".git/index", time.Now())          // pruned
	mk("d1/d2/d3/d4/deep.go", time.Now()) // beyond depth 3

	got := NewestWrite(dir, time.Time{}, 3, time.Second)
	want := old.Add(10 * time.Minute)
	if got.Sub(want).Abs() > time.Second {
		t.Errorf("newest = %v, want %v", got, want)
	}

	// Early exit: once something newer than `since` is seen, the answer
	// is "yes, lately" and the exact value need not be the true newest.
	got = NewestWrite(dir, old.Add(5*time.Minute), 3, time.Second)
	if !got.After(old.Add(5 * time.Minute)) {
		t.Errorf("expected a write after since, got %v", got)
	}
}
