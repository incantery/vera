// Package mux is Vera's view of a terminal multiplexer.
//
// Vera decides what work exists and who is doing it; the multiplexer
// decides where that is drawn and how a person reaches it. This package
// is the line between them. It is written to what Vera WANTS from a
// mux — a pane is a place with a process, a cwd and a pulse, and the
// mux tells us when any of that changes — not to what tmux happens to
// offer. The tmux backend is lossy against this interface on purpose:
// it is the reference today, and the day rook's control socket answers
// the same questions with real events, nothing above this line moves.
//
// The interface is small because the supervisor above it only needs
// six things: find the pane the person is looking at, start a pane for
// an agent, put words into it, read its screen, bring it forward, and
// hear when something changed. Everything else is a backend detail.
package mux

import (
	"context"
	"errors"
	"time"
)

// ID names one pane. The three parts mirror how every multiplexer
// addresses a pane — a session (rook: workspace), a window (rook: tab),
// a pane — so the same ID round-trips through the phone, the mind and
// the Mac app unchanged.
type ID struct {
	Session string `json:"session"`
	Window  string `json:"window"`
	Pane    string `json:"pane"`
}

// String is the backend-neutral spelling: "session:window.pane".
// Backends whose pane ids are stable on their own (rook's blocks)
// still carry a window; it is informational there.
func (id ID) String() string { return id.Session + ":" + id.Window + "." + id.Pane }

// Zero says whether the ID names nothing.
func (id ID) Zero() bool { return id == ID{} }

// Pane is one place a process runs. Command, Title and Path are what
// the mux knows without looking inside; Active is the last moment the
// pane produced output, which is the pulse liveness is read from.
type Pane struct {
	ID      ID
	Command string // foreground program
	Title   string // what the program set, if anything
	Path    string // current working directory
	Active  time.Time
	// Dead is a pane whose process has exited but which the mux still
	// shows (tmux remain-on-exit). Zero Active and no Command is the
	// other way that shows up.
	Dead bool
}

// Spawn is a request for a new pane. Session is the session/workspace
// to put it in — created if absent. Name labels the window. Dir is the
// cwd. Command is argv; empty runs the person's shell. Env is appended
// to the pane's environment.
type Spawn struct {
	Session string
	Name    string
	Dir     string
	Command []string
	Env     []string
}

// Kind is what changed.
type Kind string

const (
	// FocusChanged: the pane in front of the person is a different one,
	// or there is none.
	FocusChanged Kind = "focus"
	// PaneChanged: something about a pane — its command, title, path,
	// or activity — is different. Backends that cannot say which pane
	// send it with a zero ID and the watcher re-reads everything.
	PaneChanged Kind = "pane"
	// PaneExited: the process in a pane ended.
	PaneExited Kind = "exited"
	// Gone: the mux itself is not reachable. Sent once per outage.
	Gone Kind = "gone"
	// Back: the mux is reachable again after Gone.
	Back Kind = "back"
)

// Event is one thing the mux said happened. Pane is set when known.
type Event struct {
	Kind Kind
	Pane *Pane
	At   time.Time
}

// Mux is a multiplexer as Vera sees it.
type Mux interface {
	// Name is the backend, for /status and logs: "tmux", "rook".
	Name() string

	// Focus is the pane in front of the person. ErrNoFocus when no
	// client is attached or nothing is selected.
	Focus(ctx context.Context) (*Pane, error)

	// Get reads one pane. ErrNoPane if it does not exist.
	Get(ctx context.Context, id ID) (*Pane, error)

	// List enumerates every pane the mux holds.
	List(ctx context.Context) ([]Pane, error)

	// Spawn starts a pane and returns it. The pane is NOT brought to the
	// front: a supervisor starting work must not steal the person's
	// place.
	Spawn(ctx context.Context, s Spawn) (*Pane, error)

	// Kill ends a pane and its process.
	Kill(ctx context.Context, id ID) error

	// Send types text into a pane literally — no key names, no
	// interpretation. Enter is a separate call on purpose: words typed
	// into an agent can be read before they are sent, and words sent
	// cannot be unsent.
	Send(ctx context.Context, id ID, text string) error

	// Enter presses Return in a pane.
	Enter(ctx context.Context, id ID) error

	// Capture reads the visible rows of a pane as plain text: the
	// screen as it stands, not the scrollback.
	Capture(ctx context.Context, id ID) ([]string, error)

	// GoTo brings a pane to the front within the mux: selects it, its
	// window, and switches the attached client to it. Activating the
	// terminal app around the mux is somebody else's job.
	GoTo(ctx context.Context, id ID) error

	// Narrow leases a pane's width to cols for a viewer that is not the
	// desk (the phone, dictating). Widen returns it. Backends that cannot
	// resize one viewer independently reflow the shared window, which is
	// what tmux does and what rook's resize lease exists to improve.
	Narrow(ctx context.Context, id ID, cols int) error
	Widen(ctx context.Context, id ID) error

	// Watch delivers events until ctx ends. The backend decides how it
	// learns — hooks, a socket stream, polling — and the caller treats
	// every event as a doorbell, never as the truth: re-read after.
	Watch(ctx context.Context, fn func(Event)) error

	// Poke asks for an immediate re-read, from any goroutine. A backend
	// that gets real events can ignore it.
	Poke()
}

var (
	// ErrNoFocus: nobody is looking at a pane.
	ErrNoFocus = errors.New("no focused pane")
	// ErrNoPane: the ID names nothing.
	ErrNoPane = errors.New("no such pane")
	// ErrUnavailable: the mux is not running or not reachable.
	ErrUnavailable = errors.New("multiplexer is not available")
)
