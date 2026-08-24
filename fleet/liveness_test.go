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
	for _, s := range []State{Running, Quiet, Held, Closed} {
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
