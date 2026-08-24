// The terminal adapter: what is inside the terminal.
//
// To the focus tracker, Ghostty is opaque — a window with a name. The
// multiplexer inside it can say which session, which pane, and whether
// the thing in that pane is a coding agent. This file is Vera's side of
// that conversation. It does not know what the multiplexer is: that is
// mux.Mux, tmux today (rook's server), rook's own socket when it
// answers. What lives here is what is Vera's regardless — recognising
// an agent, keeping the phone's dictation from running words together,
// leasing a pane to the phone's width and giving it back — and turning
// mux events into observations like any other, `terminal.focus`, so the
// model, /status and the Mac app learn about it through the same door
// an editor or a browser will use.
package main

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/mux"
)

// TerminalFocus is the pane the person is looking at inside the mux.
type TerminalFocus struct {
	Session string `json:"session"`
	Window  string `json:"window"`
	Pane    string `json:"pane"`
	// Command is the foreground program, as the mux reports it.
	Command string `json:"command,omitempty"`
	Title   string `json:"title,omitempty"`
	Path    string `json:"path,omitempty"`
	// Agent names a coding agent when one is recognised in the pane:
	// "claude-code" today. Empty means an ordinary shell or program.
	Agent string `json:"agent,omitempty"`
}

func (t TerminalFocus) equal(o *TerminalFocus) bool {
	return o != nil && t == *o
}

func (t TerminalFocus) id() mux.ID {
	return mux.ID{Session: t.Session, Window: t.Window, Pane: t.Pane}
}

// claudeProcess: Claude Code's process reports its version as its name.
var claudeProcess = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// focusOf is where a pane becomes something Vera has an opinion about:
// which program is a coding agent.
func focusOf(p *mux.Pane) TerminalFocus {
	f := TerminalFocus{Session: p.ID.Session, Window: p.ID.Window, Pane: p.ID.Pane, Command: p.Command, Title: p.Title, Path: p.Path}
	if f.Command == "claude" || claudeProcess.MatchString(f.Command) || strings.HasPrefix(f.Title, "✳") {
		f.Agent = "claude-code"
	}
	return f
}

// Describe is the phrase for the model and the Mac app.
func (t TerminalFocus) Describe() string {
	where := t.Session + ":" + t.Window
	switch {
	case t.Agent == "claude-code":
		title := strings.TrimSpace(strings.TrimPrefix(t.Title, "✳"))
		if title == "" {
			return "a Claude Code session (" + where + ")"
		}
		return "Claude Code session \"" + title + "\" (" + where + ")"
	case t.Command != "":
		return t.Command + " in " + shortPath(t.Path) + " (" + where + ")"
	default:
		return "a shell in " + shortPath(t.Path) + " (" + where + ")"
	}
}

func shortPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 && i < len(p)-1 {
		return p[i+1:]
	}
	return p
}

// terminal is Vera's stateful view over a mux.
type terminal struct {
	m      mux.Mux
	device string

	mu   sync.Mutex
	last *TerminalFocus

	// typedAt / typedInto: the last typing, so the next one into the
	// same pane within a minute begins with a space rather than
	// running into it.
	typedAt   time.Time
	typedInto string

	// mob is the pane the phone has narrowed to its own width while
	// dictating, or zero. mobAt is bumped on every poll; if it goes
	// stale the run loop restores the pane, so a dropped phone can never
	// strand the desk at phone width.
	mob     mux.ID
	mobCols int
	mobAt   time.Time
}

func newTerminal(m mux.Mux, device string) *terminal {
	return &terminal{m: m, device: device}
}

// Focus is the pane currently in front of the person, or nil.
func (w *terminal) Focus() *TerminalFocus {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.last == nil {
		return nil
	}
	f := *w.last
	return &f
}

// ErrNoTarget: nothing to type into.
var ErrNoTarget = errors.New("the terminal is not showing a pane to type into")

// ErrNotAgent: the pane is a shell or program, not a coding agent, and
// the caller did not say that was fine.
var ErrNotAgent = errors.New("the focused pane is not a coding agent session")

// GoTo brings a pane to the front within the mux. Activating the
// terminal app itself is the caller's job.
func (w *terminal) GoTo(ctx context.Context, session, window, pane string) error {
	return w.m.GoTo(ctx, mux.ID{Session: session, Window: window, Pane: pane})
}

func (w *terminal) target(at *TerminalFocus) *TerminalFocus {
	if at != nil {
		return at
	}
	return w.Focus()
}

// Type is the terminal.type capability: put text into the focused pane
// as if typed, and press Enter only if asked. Type-only is the default
// on purpose — words typed into an agent can be read before they are
// sent; words sent cannot be unsent.
func (w *terminal) Type(ctx context.Context, text string, enter, anywhere bool, at *TerminalFocus) (*TerminalFocus, error) {
	// An explicit pane is a deliberate choice — type there whatever it
	// runs, and do not move the person's focus to it. No target means
	// "wherever they are looking", which must be a coding agent unless
	// they said otherwise.
	target := w.target(at)
	if target == nil {
		return nil, ErrNoTarget
	}
	if target.Agent == "" && !anywhere && at == nil {
		return target, ErrNotAgent
	}
	pane := target.id()
	if text != "" {
		w.mu.Lock()
		if w.typedInto == pane.String() && time.Since(w.typedAt) < time.Minute && !strings.HasPrefix(text, " ") {
			text = " " + text
		}
		w.typedAt, w.typedInto = time.Now(), pane.String()
		w.mu.Unlock()
		if err := w.m.Send(ctx, pane, text); err != nil {
			return target, err
		}
	}
	if enter {
		if err := w.m.Enter(ctx, pane); err != nil {
			return target, err
		}
		// Sent: the next words start a new line, not a continuation.
		w.mu.Lock()
		w.typedInto = ""
		w.mu.Unlock()
	}
	return target, nil
}

// Capture reads the visible rows of a pane as plain text. It is Vera's
// one eye into the terminal from afar: the phone cannot see the mux, so
// the Mac reads the pane and ships the lines. Nothing is typed and
// nothing moves, so a snapshot is safe against any pane.
func (w *terminal) Capture(ctx context.Context, at *TerminalFocus) ([]string, *TerminalFocus, error) {
	target := w.target(at)
	if target == nil {
		return nil, nil, ErrNoTarget
	}
	lines, err := w.m.Capture(ctx, target.id())
	return lines, target, err
}

// Mobile narrows a pane to the phone's width while dictating, or
// restores it. want=true is sent on every /screen poll while the view is
// open; each call bumps a deadline the run loop watches, so silence
// restores the desk. want=false leaves at once.
func (w *terminal) Mobile(ctx context.Context, at *TerminalFocus, cols int, want bool) error {
	if !want {
		return w.unmobile(ctx)
	}
	target := w.target(at)
	if target == nil {
		return ErrNoTarget
	}
	id := target.id()
	if cols < 20 || cols > 300 {
		cols = 52
	}
	w.mu.Lock()
	cur, curCols := w.mob, w.mobCols
	w.mu.Unlock()
	if cur == id && curCols == cols {
		w.mu.Lock()
		w.mobAt = time.Now()
		w.mu.Unlock()
		return nil
	}
	if !cur.Zero() && cur != id {
		_ = w.m.Widen(ctx, cur) // the phone moved to another pane
	}
	if err := w.m.Narrow(ctx, id, cols); err != nil {
		return err
	}
	w.mu.Lock()
	w.mob, w.mobCols, w.mobAt = id, cols, time.Now()
	w.mu.Unlock()
	return nil
}

func (w *terminal) unmobile(ctx context.Context) error {
	w.mu.Lock()
	id := w.mob
	w.mob, w.mobCols = mux.ID{}, 0
	w.mu.Unlock()
	if id.Zero() {
		return nil
	}
	return w.m.Widen(ctx, id)
}

func (w *terminal) mobileStale(ctx context.Context) {
	w.mu.Lock()
	stale := !w.mob.Zero() && time.Since(w.mobAt) > 4*time.Second
	w.mu.Unlock()
	if stale {
		_ = w.unmobile(ctx)
	}
}

// Agent runs a coding agent's own command in a pane — the phone's
// buttons for the session it is looking at. Only a recognised agent is a
// valid target: "/compact" is meaningless in a shell. Unlike Type,
// nothing is ever prepended — a command must begin with its slash.
func (w *terminal) Agent(ctx context.Context, action string, at *TerminalFocus) (*TerminalFocus, error) {
	target := w.target(at)
	if target == nil {
		return nil, ErrNoTarget
	}
	if target.Agent != "claude-code" {
		return target, ErrNotAgent
	}
	var keys string
	switch action {
	case "compact":
		keys = "/compact"
	default:
		return target, errors.New("unknown agent action " + action)
	}
	id := target.id()
	if err := w.m.Send(ctx, id, keys); err != nil {
		return target, err
	}
	if err := w.m.Enter(ctx, id); err != nil {
		return target, err
	}
	// A command was sent; the next dictation starts a fresh line.
	w.mu.Lock()
	w.typedInto = ""
	w.mu.Unlock()
	return target, nil
}

// Poke is the doorbell from the mux's hooks.
func (w *terminal) Poke() { w.m.Poke() }

// run turns mux events into observations until ctx ends. A change of
// focus becomes one observation; a mux that is not running is a fact
// reported once.
func (w *terminal) run(ctx context.Context, observe func(Observation)) {
	// Never leave the desk at phone width because the process is going.
	defer func() {
		rc, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = w.unmobile(rc)
	}()
	stale := time.NewTicker(time.Second)
	defer stale.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-stale.C:
				w.mobileStale(ctx)
			}
		}
	}()
	err := w.m.Watch(ctx, func(ev mux.Event) {
		switch ev.Kind {
		case mux.Gone:
			slog.Info("terminal: mux unavailable", "mux", w.m.Name())
		case mux.FocusChanged:
			if ev.Pane == nil {
				w.mu.Lock()
				had := w.last != nil
				w.last = nil
				w.mu.Unlock()
				if had {
					observe(Observation{Type: "terminal.unfocused", Device: w.device, Source: "rook", At: ev.At})
				}
				return
			}
			f := focusOf(ev.Pane)
			w.mu.Lock()
			changed := !f.equal(w.last)
			w.last = &f
			w.mu.Unlock()
			if changed {
				observe(Observation{Type: "terminal.focus", Device: w.device, Source: "rook", At: ev.At, Terminal: &f})
			}
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("terminal: watch ended", "error", err.Error())
	}
}
