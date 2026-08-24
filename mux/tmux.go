package mux

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Tmux is the reference backend: a tmux server on its own socket.
//
// tmux is told, not left to guess: hooks ring a doorbell (PokeURL) the
// moment a pane, window or session changes, and Watch re-reads. The
// hook carries no data and needs no secret, because nothing it says is
// believed — the truth is always read back. Polling remains as a slow
// safety net for hooks that did not fire.
type Tmux struct {
	// Socket is the server name (tmux -L). Rook's is "rook".
	Socket string
	// PokeURL is what the hooks POST to; "" installs no hooks.
	PokeURL string
	// Every is the polling interval behind the hooks. Default 5s.
	Every time.Duration

	poke   chan struct{}
	hooked bool
}

// NewTmux returns a backend on the named socket.
func NewTmux(socket, pokeURL string) *Tmux {
	return &Tmux{Socket: socket, PokeURL: pokeURL, Every: 5 * time.Second, poke: make(chan struct{}, 1)}
}

func (t *Tmux) Name() string { return "tmux" }

// paneFormat is every field Pane needs, tab-separated. window_activity
// is per window in tmux — the closest thing to a pane pulse it keeps.
const paneFormat = "#{session_name}\t#{window_index}\t#{pane_index}\t#{pane_current_command}\t#{pane_title}\t#{pane_current_path}\t#{window_activity}\t#{pane_dead}"

func parsePane(line string) (Pane, bool) {
	f := strings.Split(strings.TrimRight(line, "\r\n"), "\t")
	if len(f) < 8 {
		return Pane{}, false
	}
	p := Pane{
		ID:      ID{Session: f[0], Window: f[1], Pane: f[2]},
		Command: f[3],
		Title:   f[4],
		Path:    f[5],
		Dead:    f[7] == "1",
	}
	if secs, err := strconv.ParseInt(f[6], 10, 64); err == nil && secs > 0 {
		p.Active = time.Unix(secs, 0)
	}
	return p, true
}

func (t *Tmux) Focus(ctx context.Context) (*Pane, error) {
	clients, err := t.run(ctx, "list-clients", "-F", "#{client_flags}\t#{session_name}\t#{client_activity}")
	if err != nil {
		return nil, err
	}
	session := pickClient(clients)
	if session == "" {
		return nil, ErrNoFocus
	}
	line, err := t.run(ctx, "display", "-p", "-t", session, paneFormat)
	if err != nil {
		return nil, err
	}
	p, ok := parsePane(line)
	if !ok {
		return nil, ErrNoFocus
	}
	return &p, nil
}

// pickClient prefers the client macOS says is focused, then the one most
// recently typed in. Several terminals may be attached; only one has the
// person.
func pickClient(out string) string {
	var best, bestAt string
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

func (t *Tmux) Get(ctx context.Context, id ID) (*Pane, error) {
	line, err := t.run(ctx, "display", "-p", "-t", id.String(), paneFormat)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return nil, err
		}
		return nil, ErrNoPane
	}
	// tmux resolves a target loosely — a window that does not exist
	// falls back to the current one — so the answer must name what was
	// asked for to count.
	p, ok := parsePane(line)
	if !ok || p.ID != id {
		return nil, ErrNoPane
	}
	return &p, nil
}

func (t *Tmux) List(ctx context.Context) ([]Pane, error) {
	out, err := t.run(ctx, "list-panes", "-a", "-F", paneFormat)
	if err != nil {
		return nil, err
	}
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		if p, ok := parsePane(line); ok {
			panes = append(panes, p)
		}
	}
	return panes, nil
}

func (t *Tmux) Spawn(ctx context.Context, s Spawn) (*Pane, error) {
	if s.Session == "" {
		return nil, errors.New("spawn needs a session")
	}
	// -d everywhere: nothing here may move the person's focus.
	args := []string{"new-session", "-d", "-P", "-F", paneFormat, "-s", s.Session}
	if _, err := t.run(ctx, "has-session", "-t", "="+s.Session); err == nil {
		args = []string{"new-window", "-d", "-P", "-F", paneFormat, "-t", s.Session + ":"}
	}
	if s.Name != "" {
		args = append(args, "-n", s.Name)
	}
	if s.Dir != "" {
		args = append(args, "-c", s.Dir)
	}
	for _, e := range s.Env {
		args = append(args, "-e", e)
	}
	if len(s.Command) > 0 {
		args = append(args, shellJoin(s.Command))
	}
	line, err := t.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	p, ok := parsePane(line)
	if !ok {
		return nil, fmt.Errorf("tmux: unexpected reply %q", strings.TrimSpace(line))
	}
	return &p, nil
}

// shellJoin quotes argv for tmux, which hands a window's command to
// the shell as one string.
func shellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

func (t *Tmux) Kill(ctx context.Context, id ID) error {
	_, err := t.run(ctx, "kill-pane", "-t", id.String())
	return err
}

func (t *Tmux) Send(ctx context.Context, id ID, text string) error {
	if text == "" {
		return nil
	}
	// -l: literal, so "Enter" in a sentence is the word, not the key.
	_, err := t.run(ctx, "send-keys", "-t", id.String(), "-l", text)
	return err
}

func (t *Tmux) Enter(ctx context.Context, id ID) error {
	_, err := t.run(ctx, "send-keys", "-t", id.String(), "Enter")
	return err
}

func (t *Tmux) Capture(ctx context.Context, id ID) ([]string, error) {
	// -p prints to stdout; the visible region only. No -J: a coding
	// agent draws boxes, and joining wrapped lines would break them.
	out, err := t.run(ctx, "capture-pane", "-p", "-t", id.String())
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Trailing blank lines are an agent's empty input area: a lot of
	// nothing to ship and scroll past.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

func (t *Tmux) GoTo(ctx context.Context, id ID) error {
	if _, err := t.run(ctx, "select-pane", "-t", id.String()); err != nil {
		return err
	}
	if _, err := t.run(ctx, "select-window", "-t", id.Session+":"+id.Window); err != nil {
		return err
	}
	// switch-client moves whichever client is focused to that session;
	// the selects above decide where it lands inside it.
	_, err := t.run(ctx, "switch-client", "-t", id.String())
	return err
}

// Narrow resizes the pane's whole window: tmux has one size per window,
// so the desk reflows too. -x auto-sets window-size manual.
func (t *Tmux) Narrow(ctx context.Context, id ID, cols int) error {
	if cols < 20 || cols > 300 {
		cols = 52
	}
	_, err := t.run(ctx, "resize-window", "-t", id.Session+":"+id.Window, "-x", strconv.Itoa(cols))
	return err
}

// Widen returns a window to following the desk: -A snaps it to the
// largest client, then unsetting the option lets it auto-follow again.
func (t *Tmux) Widen(ctx context.Context, id ID) error {
	win := id.Session + ":" + id.Window
	if _, err := t.run(ctx, "resize-window", "-t", win, "-A"); err != nil {
		return err
	}
	_, err := t.run(ctx, "set-option", "-uw", "-t", win, "window-size")
	return err
}

func (t *Tmux) Poke() {
	select {
	case t.poke <- struct{}{}:
	default:
	}
}

// hooks are the tmux events after which something may have changed.
// Slot 77 keeps clear of anything the person's own config set at 0.
var hooks = []string{
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
	"pane-died",
	"pane-exited",
}

const hookSlot = "[77]"

func (t *Tmux) installHooks(ctx context.Context) {
	if t.PokeURL == "" {
		return
	}
	cmd := `run-shell -b "curl -s -m 1 -X POST ` + t.PokeURL + ` >/dev/null 2>&1"`
	for _, h := range hooks {
		if _, err := t.run(ctx, "set-hook", "-g", h+hookSlot, cmd); err != nil {
			return // not running; the next read says so
		}
	}
	t.hooked = true
}

func (t *Tmux) removeHooks() {
	if !t.hooked {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, h := range hooks {
		_, _ = t.run(ctx, "set-hook", "-gu", h+hookSlot)
	}
	t.hooked = false
}

// Watch reads on every poke and every few seconds regardless, and
// reports what differs from the last read. A tmux that is not running
// is a fact reported once.
func (t *Tmux) Watch(ctx context.Context, fn func(Event)) error {
	t.installHooks(ctx)
	defer t.removeHooks()
	every := t.Every
	if every == 0 {
		every = 5 * time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()

	var lastFocus *Pane
	lastPanes := map[ID]Pane{}
	gone := false
	for {
		now := time.Now()
		panes, err := t.List(ctx)
		if err != nil {
			if !gone {
				slog.Info("mux: tmux unavailable", "socket", t.Socket, "error", err.Error())
				gone, t.hooked = true, false
				fn(Event{Kind: Gone, At: now})
				if lastFocus != nil {
					lastFocus = nil
					fn(Event{Kind: FocusChanged, At: now})
				}
				lastPanes = map[ID]Pane{}
			}
		} else {
			if gone || !t.hooked {
				// tmux came (back): the hooks died with the old server.
				t.installHooks(ctx)
				if gone {
					gone = false
					fn(Event{Kind: Back, At: now})
				}
			}
			seen := map[ID]Pane{}
			for _, p := range panes {
				seen[p.ID] = p
				if old, ok := lastPanes[p.ID]; !ok || old != p {
					if ok && p.Dead && !old.Dead {
						fn(Event{Kind: PaneExited, Pane: &p, At: now})
					} else {
						fn(Event{Kind: PaneChanged, Pane: &p, At: now})
					}
				}
			}
			for id, old := range lastPanes {
				if _, ok := seen[id]; !ok {
					fn(Event{Kind: PaneExited, Pane: &old, At: now})
				}
			}
			lastPanes = seen

			focus, ferr := t.Focus(ctx)
			switch {
			case ferr != nil && lastFocus != nil:
				lastFocus = nil
				fn(Event{Kind: FocusChanged, At: now})
			case ferr == nil && (lastFocus == nil || focus.ID != lastFocus.ID || focus.Command != lastFocus.Command || focus.Title != lastFocus.Title || focus.Path != lastFocus.Path):
				lastFocus = focus
				fn(Event{Kind: FocusChanged, Pane: focus, At: now})
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		case <-t.poke:
			// Let tmux finish the change the hook announced.
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func (t *Tmux) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append([]string{"-L", t.Socket}, args...)...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg := strings.TrimSpace(string(ee.Stderr))
			if strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting") {
				return "", ErrUnavailable
			}
			if msg != "" {
				return "", errors.New("tmux: " + msg)
			}
		}
		return "", err
	}
	return string(out), nil
}
