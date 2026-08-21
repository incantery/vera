// The rook adapter: what is inside the terminal.
//
// To the focus tracker, Ghostty is opaque — a window with a name. Rook
// is what is running inside it, and rook can say which session, which
// pane, and whether the thing in that pane is a coding agent. This file
// is Vera's side of that conversation, and it is the ONLY place Vera
// knows how rook is put together. Today rook is a tmux server named
// "rook"; when the native rook's control socket is what runs, this file
// changes and nothing above it does.
//
// What it produces is an observation like any other — `terminal.focus`,
// source rook — so the model, /status and the Mac app learn about it
// through the same door an editor or a browser will use.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TerminalFocus is the pane the person is looking at inside rook.
type TerminalFocus struct {
	Session string `json:"session"`
	Window  string `json:"window"`
	Pane    string `json:"pane"`
	// Command is the foreground program, as tmux reports it.
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

// rookWatcher reads the rook tmux server and reports changes.
//
// It is told, not left to guess: tmux hooks ring a doorbell (Poke) the
// moment a pane, window or session changes, and the watcher re-reads.
// The hook carries no data and needs no secret, because nothing it
// says is believed — the truth is always read back from tmux. Polling
// remains as a slow safety net for hooks that did not fire.
type rookWatcher struct {
	socket string // tmux -L name
	device string
	every  time.Duration
	last   *TerminalFocus
	absent bool
	poke   chan struct{}
	hooked bool

	mu sync.Mutex // guards last for readers outside run

	// typedAt / typedInto: the last typing, so the next one into the
	// same pane within a minute begins with a space rather than
	// running into it.
	typedAt   time.Time
	typedInto string

	// mobWin is the window the phone has narrowed to its own width while
	// dictating, or "". mobAt is bumped on every poll; if it goes stale
	// the run loop restores the window, so a dropped phone can never
	// strand the desk at phone width. Guarded by mu.
	mobWin  string
	mobCols int
	mobAt   time.Time
}

// Focus is the pane currently in front of the person, or nil.
func (w *rookWatcher) Focus() *TerminalFocus {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.last == nil {
		return nil
	}
	f := *w.last
	return &f
}

// ErrNoTarget: nothing to type into.
var ErrNoTarget = errors.New("rook is not showing a pane to type into")

// ErrNotAgent: the pane is a shell or program, not a coding agent, and
// the caller did not say that was fine.
var ErrNotAgent = errors.New("the focused pane is not a coding agent session")

// GoTo brings a pane to the front within rook: select it, its window,
// and switch the focused client to it. Activating the terminal app
// itself is the caller's job — rook only moves within tmux.
func (w *rookWatcher) GoTo(ctx context.Context, session, window, pane string) error {
	target := session + ":" + window + "." + pane
	if _, err := w.tmux(ctx, "select-pane", "-t", target); err != nil {
		return err
	}
	if _, err := w.tmux(ctx, "select-window", "-t", session+":"+window); err != nil {
		return err
	}
	// switch-client moves whichever client is focused to that session;
	// select-window/pane above decide where it lands inside it.
	_, err := w.tmux(ctx, "switch-client", "-t", target)
	return err
}

// Type is the terminal.type capability: put text into the focused pane
// as if typed, and press Enter only if asked. Type-only is the default
// on purpose — words typed into an agent can be read before they are
// sent; words sent cannot be unsent.
func (w *rookWatcher) Type(ctx context.Context, text string, enter, anywhere bool, at *TerminalFocus) (*TerminalFocus, error) {
	// An explicit pane is a deliberate choice — type there whatever it
	// runs, and do not move the person's focus to it. No target means
	// "wherever they are looking", which must be a coding agent unless
	// they said otherwise.
	target := at
	explicit := at != nil
	if target == nil {
		target = w.Focus()
	}
	if target == nil {
		return nil, ErrNoTarget
	}
	if target.Agent == "" && !anywhere && !explicit {
		return target, ErrNotAgent
	}
	pane := target.Session + ":" + target.Window + "." + target.Pane
	if text != "" {
		w.mu.Lock()
		if w.typedInto == pane && time.Since(w.typedAt) < time.Minute && !strings.HasPrefix(text, " ") {
			text = " " + text
		}
		w.typedAt, w.typedInto = time.Now(), pane
		w.mu.Unlock()
		// -l: literal, so "Enter" in a sentence is the word, not the key.
		if _, err := w.tmux(ctx, "send-keys", "-t", pane, "-l", text); err != nil {
			return target, err
		}
	}
	if enter {
		if _, err := w.tmux(ctx, "send-keys", "-t", pane, "Enter"); err != nil {
			return target, err
		}
		// Sent: the next words start a new line, not a continuation.
		w.mu.Lock()
		w.typedInto = ""
		w.mu.Unlock()
	}
	return target, nil
}

// Capture reads the visible rows of a pane as plain text — the screen
// as it stands, not the scrollback. It is Vera's one eye into the
// terminal from afar: the phone cannot see rook, so the Mac reads the
// pane and ships the lines. Nothing is typed and nothing moves, so a
// snapshot is safe against any pane, agent or shell — a chosen pane, or
// whatever has focus when `at` is nil.
func (w *rookWatcher) Capture(ctx context.Context, at *TerminalFocus) ([]string, *TerminalFocus, error) {
	target := at
	if target == nil {
		target = w.Focus()
	}
	if target == nil {
		return nil, nil, ErrNoTarget
	}
	pane := target.Session + ":" + target.Window + "." + target.Pane
	// -p prints to stdout; the visible region only, which is the whole
	// point — this is a glance, not the whole history. No -J: a coding
	// agent draws boxes, and joining wrapped lines would break them.
	out, err := w.tmux(ctx, "capture-pane", "-p", "-t", pane)
	if err != nil {
		return nil, target, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Trim trailing blank lines — an agent's empty input area is a lot
	// of nothing to send down and to scroll past.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, target, nil
}

// Mobile narrows a pane's window to the phone's width while dictating,
// or restores it. The window is shared with the desktop terminal, so
// this reflows there too — inherent to one tmux window having one size,
// and the whole point: the phone sees the agent laid out for the phone.
//
// want=true is sent on every /screen poll while the view is open; each
// call bumps a deadline the run loop watches, so silence restores the
// desk. want=false leaves at once, for a snappy snap-back on close.
func (w *rookWatcher) Mobile(ctx context.Context, at *TerminalFocus, cols int, want bool) error {
	if !want {
		return w.unmobile(ctx)
	}
	target := at
	if target == nil {
		target = w.Focus()
	}
	if target == nil {
		return ErrNoTarget
	}
	win := target.Session + ":" + target.Window
	if cols < 20 || cols > 300 {
		cols = 52
	}
	w.mu.Lock()
	cur, curCols := w.mobWin, w.mobCols
	w.mu.Unlock()
	if cur == win && curCols == cols {
		w.mu.Lock()
		w.mobAt = time.Now()
		w.mu.Unlock()
		return nil
	}
	if cur != "" && cur != win {
		_ = w.restoreWin(ctx, cur) // the phone moved to another pane
	}
	// resize-window -x auto-sets window-size manual for the window.
	if _, err := w.tmux(ctx, "resize-window", "-t", win, "-x", strconv.Itoa(cols)); err != nil {
		return err
	}
	w.mu.Lock()
	w.mobWin, w.mobCols, w.mobAt = win, cols, time.Now()
	w.mu.Unlock()
	return nil
}

// unmobile restores whatever window we narrowed, if any. Safe to call
// when nothing is narrowed.
func (w *rookWatcher) unmobile(ctx context.Context) error {
	w.mu.Lock()
	win := w.mobWin
	w.mobWin, w.mobCols = "", 0
	w.mu.Unlock()
	if win == "" {
		return nil
	}
	return w.restoreWin(ctx, win)
}

// restoreWin returns a window to following the desktop again: -A snaps
// it to the largest (desktop) client's width, then unsetting the option
// lets it auto-follow that client from here on, as it did before.
func (w *rookWatcher) restoreWin(ctx context.Context, win string) error {
	if _, err := w.tmux(ctx, "resize-window", "-t", win, "-A"); err != nil {
		return err
	}
	_, err := w.tmux(ctx, "set-option", "-uw", "-t", win, "window-size")
	return err
}

// mobileStale restores the narrowed window if the phone has gone quiet.
// Called from the run loop; the deadline is a few polls of slack.
func (w *rookWatcher) mobileStale(ctx context.Context) {
	w.mu.Lock()
	stale := w.mobWin != "" && time.Since(w.mobAt) > 4*time.Second
	w.mu.Unlock()
	if stale {
		_ = w.unmobile(ctx)
	}
}

// Agent runs a coding agent's own command in a pane — the phone's
// buttons for the session it is looking at. Only a recognised agent is a
// valid target: "/compact" is meaningless in a shell, and firing it into
// one would just type a stray line. Unlike Type, nothing is ever
// prepended — a command must begin with its slash to be a command.
func (w *rookWatcher) Agent(ctx context.Context, action string, at *TerminalFocus) (*TerminalFocus, error) {
	target := at
	if target == nil {
		target = w.Focus()
	}
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
		return target, fmt.Errorf("unknown agent action %q", action)
	}
	pane := target.Session + ":" + target.Window + "." + target.Pane
	if _, err := w.tmux(ctx, "send-keys", "-t", pane, "-l", keys); err != nil {
		return target, err
	}
	if _, err := w.tmux(ctx, "send-keys", "-t", pane, "Enter"); err != nil {
		return target, err
	}
	// A command was sent; the next dictation starts a fresh line.
	w.mu.Lock()
	w.typedInto = ""
	w.mu.Unlock()
	return target, nil
}

func newRookWatcher(socket, device string) *rookWatcher {
	return &rookWatcher{socket: socket, device: device, every: 5 * time.Second, poke: make(chan struct{}, 1)}
}

// Poke asks for an immediate re-read. Safe from any goroutine; a poke
// while one is pending is folded into it.
func (w *rookWatcher) Poke() {
	select {
	case w.poke <- struct{}{}:
	default:
	}
}

// hooks are the tmux events after which focus may have moved. Slot 77
// keeps clear of anything the person's own config set at slot 0.
var rookHooks = []string{
	"client-session-changed",
	"session-window-changed",
	"window-pane-changed",
	"client-focus-in",
	"client-attached",
	"client-detached",
	"after-select-pane",
	"after-select-window",
	"after-kill-pane",
	"after-new-window",
}

const rookHookSlot = "[77]"

// installHooks points tmux at the doorbell. Idempotent; removed on exit.
func (w *rookWatcher) installHooks(ctx context.Context, pokeURL string) {
	cmd := `run-shell -b "curl -s -m 1 -X POST ` + pokeURL + ` >/dev/null 2>&1"`
	for _, h := range rookHooks {
		if _, err := w.tmux(ctx, "set-hook", "-g", h+rookHookSlot, cmd); err != nil {
			return // not running; the next read says so
		}
	}
	w.hooked = true
}

func (w *rookWatcher) removeHooks() {
	if !w.hooked {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, h := range rookHooks {
		_, _ = w.tmux(ctx, "set-hook", "-gu", h+rookHookSlot)
	}
}

// run reads until ctx ends — on every poke, and every few seconds
// regardless. A change becomes one observation; a rook that is not
// running is a fact reported once.
func (w *rookWatcher) run(ctx context.Context, pokeURL string, observe func(Observation)) {
	w.installHooks(ctx, pokeURL)
	defer w.removeHooks()
	// Never leave the desk at phone width because the process is going.
	defer func() {
		rc, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = w.unmobile(rc)
	}()
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		w.mobileStale(ctx)
		focus, err := w.read(ctx)
		switch {
		case err != nil:
			if !w.absent {
				slog.Info("rook: no terminal focus available", "error", err.Error())
				w.absent = true
				w.hooked = false
			}
			if w.last != nil {
				observe(Observation{Type: "terminal.unfocused", Device: w.device, Source: "rook", At: time.Now()})
				w.mu.Lock()
				w.last = nil
				w.mu.Unlock()
			}
		case !focus.equal(w.last):
			if w.absent || !w.hooked {
				// rook came (back): the hooks died with the old server.
				w.installHooks(ctx, pokeURL)
			}
			w.absent = false
			w.mu.Lock()
			w.last = &focus
			w.mu.Unlock()
			observe(Observation{Type: "terminal.focus", Device: w.device, Source: "rook", At: time.Now(), Terminal: &focus})
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.poke:
			// Let tmux finish the change the hook announced.
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// claudeProcess: Claude Code's process reports its version as its name.
var claudeProcess = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// read asks tmux which client has focus and what its active pane holds.
func (w *rookWatcher) read(ctx context.Context) (TerminalFocus, error) {
	clients, err := w.tmux(ctx, "list-clients", "-F", "#{client_flags}\t#{session_name}\t#{client_activity}")
	if err != nil {
		return TerminalFocus{}, err
	}
	session := pickClient(clients)
	if session == "" {
		return TerminalFocus{}, errNoClient
	}
	line, err := w.tmux(ctx, "display", "-p", "-t", session,
		"#{window_index}\t#{pane_index}\t#{pane_current_command}\t#{pane_title}\t#{pane_current_path}")
	if err != nil {
		return TerminalFocus{}, err
	}
	f := strings.Split(strings.TrimRight(line, "\n"), "\t")
	if len(f) < 5 {
		return TerminalFocus{}, errNoClient
	}
	focus := TerminalFocus{Session: session, Window: f[0], Pane: f[1], Command: f[2], Title: f[3], Path: f[4]}
	if focus.Command == "claude" || claudeProcess.MatchString(focus.Command) || strings.HasPrefix(focus.Title, "✳") {
		focus.Agent = "claude-code"
	}
	return focus, nil
}

type noClient struct{}

func (noClient) Error() string { return "no attached rook client" }

var errNoClient = noClient{}

// pickClient prefers the client macOS says is focused, then the one most
// recently typed in. Several terminals may be attached; only one has the
// person.
func pickClient(out string) string {
	var best string
	var bestAt string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		if strings.Contains(f[0], "focused") {
			return f[1]
		}
		if f[2] > bestAt {
			best, bestAt = f[1], f[2]
		}
	}
	return best
}

func (w *rookWatcher) tmux(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append([]string{"-L", w.socket}, args...)...).Output()
	return string(out), err
}
