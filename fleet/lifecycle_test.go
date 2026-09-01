package fleet

import (
	"testing"
	"time"
)

var epoch = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return epoch.Add(d) }

func TestLifecycleSpan(t *testing.T) {
	var l Lifecycle
	l.Went(CauseSleep, at(time.Hour))
	if l.Awake() {
		t.Fatal("asleep machine reads as awake")
	}
	m := l.Since(at(0), at(90*time.Minute))
	if !m.Away || m.Cause != CauseSleep || m.Down != 30*time.Minute {
		t.Fatalf("mid-absence: %+v", m)
	}
	l.Came(CauseSleep, at(3*time.Hour))
	if !l.Awake() {
		t.Fatal("woken machine reads as away")
	}
	m = l.Since(at(0), at(4*time.Hour))
	if m.Away || m.Down != 2*time.Hour || !m.Went.Equal(at(time.Hour)) || !m.Back.Equal(at(3*time.Hour)) {
		t.Fatalf("after: %+v", m)
	}
	// A window that starts inside the absence only counts its own part.
	if m := l.Since(at(2*time.Hour), at(4*time.Hour)); m.Down != time.Hour {
		t.Errorf("clipped window: down = %v, want 1h", m.Down)
	}
	// A window entirely after it counts none of it, but still knows
	// when the machine last came back.
	m = l.Since(at(3*time.Hour+time.Minute), at(4*time.Hour))
	if m.Down != 0 || !m.Back.Equal(at(3*time.Hour)) {
		t.Errorf("later window: %+v", m)
	}
}

func TestLifecycleCausesNest(t *testing.T) {
	var l Lifecycle
	// The network drops as the lid shuts, and comes back after it opens:
	// one absence, not two, and it is called sleep.
	l.Went(CauseOffline, at(0))
	l.Went(CauseSleep, at(time.Second))
	l.Came(CauseSleep, at(time.Hour))
	if l.Awake() {
		t.Fatal("the absence ended while the network was still down")
	}
	l.Came(CauseOffline, at(time.Hour+time.Minute))
	if !l.Awake() {
		t.Fatal("the absence outlived both of its causes")
	}
	spans := l.Spans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1: %+v", len(spans), spans)
	}
	if spans[0].Cause != CauseSleep {
		t.Errorf("cause = %q, want sleep — sleep is the bigger fact", spans[0].Cause)
	}
	if got := spans[0].To.Sub(spans[0].From); got != time.Hour+time.Minute {
		t.Errorf("span = %v, want 1h1m", got)
	}
}

func TestLifecycleBeatFindsSuspension(t *testing.T) {
	l := Lifecycle{Tolerance: time.Minute}
	l.Beat(at(0))
	l.Beat(at(15 * time.Second))
	l.Beat(at(30 * time.Second))
	if len(l.Spans()) != 0 {
		t.Fatalf("a kept cadence is not an absence: %+v", l.Spans())
	}
	// The lid shuts; nothing runs; the next beat is eight hours late.
	l.Beat(at(8 * time.Hour))
	spans := l.Spans()
	if len(spans) != 1 || spans[0].Cause != CauseSleep {
		t.Fatalf("late beat should record a sleep: %+v", spans)
	}
	if m := l.Since(at(0), at(8*time.Hour)); m.Down < 7*time.Hour {
		t.Errorf("down = %v, want most of the window", m.Down)
	}
	// The same gap must not be found twice.
	l.Beat(at(8*time.Hour + 15*time.Second))
	if len(l.Spans()) != 1 {
		t.Errorf("gap recorded twice: %+v", l.Spans())
	}
}

func TestLifecycleBeatDefersToTheApp(t *testing.T) {
	l := Lifecycle{Tolerance: time.Minute}
	l.Beat(at(0))
	// The app says the machine went and came back; the first beat after
	// that is late by hours of wall clock but none of it is news.
	l.Went(CauseSleep, at(time.Minute))
	l.Came(CauseSleep, at(8*time.Hour))
	l.Beat(at(8*time.Hour + 10*time.Second))
	if len(l.Spans()) != 1 {
		t.Fatalf("the beat invented a second absence: %+v", l.Spans())
	}
	if m := l.Since(at(0), at(8*time.Hour)); m.Down != 8*time.Hour-time.Minute {
		t.Errorf("down = %v, want the reported span", m.Down)
	}
}

func TestLifecycleOnBack(t *testing.T) {
	var l Lifecycle
	poked := 0
	l.OnBack(func() { poked++ })
	l.Went(CauseSleep, at(0))
	if poked != 0 {
		t.Fatal("poked on the way out")
	}
	l.Came(CauseSleep, at(time.Hour))
	if poked != 1 {
		t.Fatalf("poked %d times, want 1", poked)
	}
	// Nothing to come back from: no poke.
	l.Came(CauseSleep, at(2*time.Hour))
	if poked != 1 {
		t.Fatalf("poked %d times, want 1", poked)
	}
}

func TestLifecycleNil(t *testing.T) {
	var l *Lifecycle
	l.Went(CauseSleep, at(0))
	l.Came(CauseSleep, at(0))
	l.Beat(at(0))
	l.OnBack(func() {})
	if !l.Awake() || l.Spans() != nil || (l.Since(at(0), at(time.Hour)) != Machine{}) {
		t.Error("a fleet with no lifecycle should believe the machine never leaves")
	}
}

func TestMachineInterrupted(t *testing.T) {
	grace := 10 * time.Minute
	cases := []struct {
		name  string
		m     Machine
		since time.Time
		now   time.Time
		want  bool
	}{
		{"never away", Machine{}, at(0), at(time.Hour), false},
		{"away now", Machine{Away: true, Went: at(0)}, at(0), at(time.Hour), true},
		{"just back", Machine{Went: at(0), Back: at(time.Hour)}, at(0), at(time.Hour + time.Minute), true},
		{"back a while", Machine{Went: at(0), Back: at(time.Hour)}, at(0), at(2 * time.Hour), false},
		{"stirred since it came back", Machine{Went: at(0), Back: at(time.Hour)}, at(time.Hour + 30*time.Second), at(time.Hour + time.Minute), false},
	}
	for _, c := range cases {
		if got := c.m.Interrupted(c.since, c.now, grace); got != c.want {
			t.Errorf("%s: Interrupted = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMachineCut(t *testing.T) {
	cases := []struct {
		name string
		m    Machine
		turn time.Time
		want bool
	}{
		{"never away", Machine{}, at(time.Hour), false},
		{"the turn ended before the machine left", Machine{Went: at(time.Hour), Back: at(2 * time.Hour)}, at(30 * time.Minute), false},
		{"the machine went out from under it", Machine{Went: at(time.Hour), Back: at(2 * time.Hour)}, at(time.Hour + time.Second), true},
		{"the turn ended after it was back", Machine{Went: at(time.Hour), Back: at(2 * time.Hour)}, at(2*time.Hour + 30*time.Second), false},
		{"still away", Machine{Away: true, Went: at(time.Hour)}, at(time.Hour + time.Second), true},
	}
	for _, c := range cases {
		if got := c.m.Cut(c.turn); got != c.want {
			t.Errorf("%s: Cut = %v, want %v", c.name, got, c.want)
		}
	}
}

// The heartbeat notices the sleep first and the Mac app's report of the
// same sleep arrives a second later, once its streams are back. One
// absence, not two — two would discount the same eight hours twice, and
// a task that really did stall would read as busy for ever.
func TestLifecycleTheBeatAndTheAppAgreeOnOneAbsence(t *testing.T) {
	l := Lifecycle{Tolerance: time.Minute}
	l.Beat(at(0))
	// Nothing runs for eight hours; the first sweep back finds the gap.
	l.Beat(at(8 * time.Hour))
	// Then the app says what it was, with the moment it began.
	l.Went(CauseSleep, at(time.Minute))
	l.Came(CauseSleep, at(8*time.Hour+time.Second))

	spans := l.Spans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1: %+v", len(spans), spans)
	}
	// The span keeps the earlier of the two starts. The beat can only
	// say "nothing has run since the last one", which in the running
	// daemon is fifteen seconds before the lid shut — generous by a
	// cadence, and generous is the safe direction.
	if !spans[0].From.Equal(at(0)) || !spans[0].To.Equal(at(8*time.Hour+time.Second)) {
		t.Errorf("span %+v", spans[0])
	}
	m := l.Since(at(0), at(8*time.Hour+time.Minute))
	if m.Down != 8*time.Hour+time.Second {
		t.Fatalf("down = %v, want the one span — twice that is the same absence counted twice", m.Down)
	}
	if m.Away {
		t.Error("still away after the app said it came back")
	}
	// The minute since it came back is still the agent's own silence.
	if !m.Back.Equal(at(8*time.Hour + time.Second)) {
		t.Errorf("back = %v", m.Back)
	}
}
