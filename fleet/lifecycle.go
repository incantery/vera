// The machine's own lifecycle: when it was not there to work.
//
// Every state in liveness.go is read from silence — a pane that has
// produced nothing, a worktree nothing has been written to, a turn that
// ended and was never picked up. All of that is evidence about an agent
// only while the machine underneath it was running. A Mac that slept at
// midnight and woke at nine has eight hours of perfect silence on every
// task in the fleet, and none of it means anything about the agents.
//
// So the fleet keeps a second, much smaller record: the spans this
// machine spent away. Two things put spans in it. The Mac app says so —
// it hears NSWorkspace's sleep and wake, and the network coming and
// going, and reports them like any other observation. And failing that,
// the supervisor counts: a heartbeat that arrives an hour after the one
// before it is an hour the process was suspended, which is the only way
// a program can notice its own absence.
//
// What comes out is a Machine — the small set of facts Classify needs
// to tell "the agent is stuck" from "the lid was shut".
package fleet

import (
	"sync"
	"time"
)

// The causes a machine is away for. Sleep is the machine not running at
// all; offline is the machine running with nothing to reach, which for
// an agent whose every turn is an API call is nearly the same thing.
const (
	CauseSleep   = "sleep"
	CauseOffline = "offline"
)

// Away is one span the machine could not work through. To is zero while
// it is still going on.
type Away struct {
	Cause string    `json:"cause"`
	From  time.Time `json:"from"`
	To    time.Time `json:"to,omitzero"`
}

// Open says whether the machine is still in this absence.
func (a Away) Open() bool { return a.To.IsZero() }

// Lifecycle is the record of those spans. Its zero value works and
// records nothing until something is reported to it.
type Lifecycle struct {
	// Tolerance is how late a heartbeat may be before the gap it left
	// is read as the machine having been suspended. Zero means two
	// minutes: the supervisor's cadence is fifteen seconds, so this is
	// eight missed beats — far more than a slow mux or a loaded
	// machine costs, and far less than the ten minutes it takes for a
	// quiet task to become one worth looking at. Real sleeps are
	// reported precisely by the Mac app; this only has to catch the
	// ones nobody reported.
	Tolerance time.Duration
	// Keep bounds the record; zero keeps 32. Nothing older matters —
	// a task is judged over the window since it last did something,
	// and a task that has done nothing for thirty absences is a task
	// with a bigger problem than the lid.
	Keep int

	mu sync.Mutex
	// spans is oldest first; the last may be open.
	spans []Away
	// causes is what is currently keeping the machine away. Sleep and
	// no-network arrive together and leave separately, so an absence
	// ends when the last of its causes does, not the first.
	causes map[string]bool
	beat   time.Time
	// back is called when the machine comes back, so the supervisor
	// can look at once rather than at the end of its cadence.
	back func()
}

// OnBack sets what runs when the machine returns.
func (l *Lifecycle) OnBack(fn func()) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.back = fn
}

func (l *Lifecycle) tolerance() time.Duration {
	if l.Tolerance > 0 {
		return l.Tolerance
	}
	return 2 * time.Minute
}

func (l *Lifecycle) keep() int {
	if l.Keep > 0 {
		return l.Keep
	}
	return 32
}

// Went records the machine going away for a reason. A second reason
// during the same absence joins it rather than starting another; sleep
// outranks offline as the name of what happened, because a machine that
// is asleep is not a machine with a network problem.
func (l *Lifecycle) Went(cause string, at time.Time) {
	if l == nil {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.causes == nil {
		l.causes = map[string]bool{}
	}
	l.causes[cause] = true
	if s := l.open(); s != nil {
		if cause == CauseSleep {
			s.Cause = CauseSleep
		}
		if at.Before(s.From) {
			s.From = at
		}
		return
	}
	l.spans = append(l.spans, Away{Cause: cause, From: at})
	l.trim()
}

// Came records one reason ending. The absence ends when the last of
// them does.
func (l *Lifecycle) Came(cause string, at time.Time) {
	if l == nil {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	l.mu.Lock()
	delete(l.causes, cause)
	s := l.open()
	if s == nil || len(l.causes) > 0 {
		l.mu.Unlock()
		return
	}
	if at.Before(s.From) {
		at = s.From
	}
	s.To = at
	// The clock the heartbeat counts against restarts here: this gap
	// is now on the record, and must not be found a second time.
	l.beat = at
	back := l.back
	l.mu.Unlock()
	if back != nil {
		back()
	}
}

// Beat is the supervisor's heartbeat, and the fallback detector: a beat
// that arrives far later than the one before it is time the process was
// not running. It is deliberately dumber than the Mac app's report —
// it cannot say why, only that the machine was gone — and it is the
// only detector that works when no app is reporting at all.
func (l *Lifecycle) Beat(now time.Time) {
	if l == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	last := l.beat
	l.beat = now
	// The first beat establishes the clock; it says nothing about
	// what came before the process started.
	if last.IsZero() || l.open() != nil {
		return
	}
	if now.Sub(last) <= l.tolerance() {
		return
	}
	l.spans = append(l.spans, Away{Cause: CauseSleep, From: last, To: now})
	l.trim()
}

// Awake says whether the machine is here right now.
func (l *Lifecycle) Awake() bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.open() == nil
}

// open is the absence in progress, nil if none. Called with the lock.
func (l *Lifecycle) open() *Away {
	if n := len(l.spans); n > 0 && l.spans[n-1].Open() {
		return &l.spans[n-1]
	}
	return nil
}

func (l *Lifecycle) trim() {
	if k := l.keep(); len(l.spans) > k {
		l.spans = l.spans[len(l.spans)-k:]
	}
}

// Since is the machine as it was across [from, now]: how much of that
// window it spent away, and when it last went and came back.
func (l *Lifecycle) Since(from, now time.Time) Machine {
	if l == nil {
		return Machine{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var m Machine
	for _, s := range l.spans {
		to := s.To
		if s.Open() {
			to = now
		}
		// The last absence is the one Classify reasons about, whether
		// or not it overlaps the window: a turn that ended before the
		// machine went away is a turn that ended for its own reasons.
		if !s.From.Before(m.Went) {
			m.Went, m.Back, m.Cause, m.Away = s.From, s.To, s.Cause, s.Open()
		}
		if to.After(from) && s.From.Before(now) {
			start := s.From
			if start.Before(from) {
				start = from
			}
			if to.After(now) {
				to = now
			}
			if to.After(start) {
				m.Down += to.Sub(start)
			}
		}
	}
	return m
}

// Spans is the record, for anything that wants to show it.
func (l *Lifecycle) Spans() []Away {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Away(nil), l.spans...)
}

// Machine is what the machine itself was doing across the window a task
// is being judged over. Its zero value is a machine that has been here
// the whole time, which is what everything believed before this file
// existed.
type Machine struct {
	// Away: it is not here now.
	Away bool `json:"away,omitempty"`
	// Cause is why it went, the last time it did.
	Cause string `json:"cause,omitempty"`
	// Went and Back are the last absence. Back is zero while Away.
	Went time.Time `json:"went,omitzero"`
	Back time.Time `json:"back,omitzero"`
	// Down is how much of the window was spent away.
	Down time.Duration `json:"down,omitempty"`
}

// Interrupted says whether the machine, rather than the agent, explains
// why nothing has happened since `since`. It is true while the machine
// is away, and for a grace period after it comes back — long enough for
// an agent to have drawn a character if it was ever going to.
func (m Machine) Interrupted(since, now time.Time, grace time.Duration) bool {
	if m.Away {
		return true
	}
	if m.Back.IsZero() || since.After(m.Back) {
		return false
	}
	return now.Sub(m.Back) < grace
}

// Cut says whether the machine went out from under something that
// happened at `t` — an ended turn, in practice: did it end inside the
// absence? A turn that ended before the machine left is a question the
// agent asked and a person still owes an answer to. A turn that ended
// while the machine was going is the machine ending it, and calling
// that "waiting on you" is inventing a question nobody asked.
func (m Machine) Cut(t time.Time) bool {
	if m.Went.IsZero() || t.Before(m.Went) {
		return false
	}
	if m.Away {
		return true
	}
	return !m.Back.IsZero() && !t.After(m.Back)
}

// Why is the absence in the words a person uses about it.
func (m Machine) Why() string {
	switch m.Cause {
	case CauseSleep:
		if m.Away {
			return "the machine is asleep"
		}
		return "the machine was asleep"
	case CauseOffline:
		if m.Away {
			return "the machine has no network"
		}
		return "the machine had no network"
	}
	if m.Away {
		return "the machine is away"
	}
	return "the machine was away"
}
