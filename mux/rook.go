package mux

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rook is the backend for rook's own engine, over its unix socket.
//
// Rook is the single writer of its state; Vera replicates it and
// changes it only by issuing commands (rook's docs/surfaces.md). So
// this file reads one thing — the state feed, a JSON snapshot of
// everything the engine knows, pushed on change — and sends a handful
// of commands. The wire is rook's placeholder framing (proto.zig: one
// type byte, a little-endian length, a payload); the snapshot's
// schema is the part meant to last.
//
// What rook gives that tmux could not: focus and a per-pane activity
// stamp arrive as fields of the same snapshot rather than as hooks and
// heuristics; a block client can hold a resize lease, so the phone
// narrows a pane without reflowing the desk; and a workspace can be
// opened quietly, so starting work never moves the person.
type Rook struct {
	// Sock is the engine's socket; empty means $ROOK_MUX_SOCK or the
	// default beside rook's state.
	Sock string
	// Every is how often Watch re-asks for a fresh snapshot between
	// pushes — the pushed stream holds activity back to its own
	// cadence; a direct ask always carries it.
	Every time.Duration

	poke chan struct{}

	mu     sync.Mutex
	leases map[ID]net.Conn // Narrow holds a connection per leased block
}

// rook's message kinds (proto.zig).
const (
	c2sStdin       byte = 2
	c2sResize      byte = 3
	c2sSession     byte = 9
	c2sAttachBlock byte = 11
	c2sBlockCmd    byte = 12
	c2sState       byte = 13
	c2sCapture     byte = 14

	s2cDraw         byte = 1
	s2cExit         byte = 2
	s2cBlockCreated byte = 5
	s2cStateJSON    byte = 6
	s2cAck          byte = 7
	s2cText         byte = 8
)

// RookSock is where the engine listens.
func RookSock() string {
	if s := os.Getenv("ROOK_MUX_SOCK"); s != "" {
		return s
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "rook", "mux.sock")
}

func NewRook(sock string) *Rook {
	if sock == "" {
		sock = RookSock()
	}
	return &Rook{Sock: sock, Every: 5 * time.Second, poke: make(chan struct{}, 1), leases: map[ID]net.Conn{}}
}

func (r *Rook) Name() string { return "rook" }

// --- wire ---------------------------------------------------------------

func (r *Rook) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: 2 * time.Second}
	c, err := d.DialContext(ctx, "unix", r.Sock)
	if err != nil {
		return nil, ErrUnavailable
	}
	return c, nil
}

func send(c net.Conn, kind byte, payload []byte) error {
	hdr := [5]byte{kind}
	binary.LittleEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := c.Write(hdr[:]); err != nil {
		return err
	}
	_, err := c.Write(payload)
	return err
}

type frame struct {
	kind    byte
	payload []byte
}

func recv(br *bufio.Reader) (frame, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return frame{}, err
	}
	n := binary.LittleEndian.Uint32(hdr[1:])
	if n > 64<<20 {
		return frame{}, errors.New("rook: frame too large")
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(br, p); err != nil {
		return frame{}, err
	}
	return frame{hdr[0], p}, nil
}

// waitFor reads frames until one of kind arrives or the deadline
// passes. Others — acks, draws — are dropped: a control connection is
// not a viewer.
func waitFor(c net.Conn, br *bufio.Reader, kind byte, timeout time.Duration) (frame, error) {
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	defer c.SetReadDeadline(time.Time{})
	for {
		f, err := recv(br)
		if err != nil {
			return frame{}, err
		}
		if f.kind == s2cExit {
			return frame{}, errors.New("rook: " + strings.TrimSpace(string(f.payload)))
		}
		if f.kind == kind {
			return f, nil
		}
	}
}

// --- the state feed -----------------------------------------------------

// snapshot is the subset of rook's state JSON Vera reads. Unknown
// fields are skipped; the schema says readers accept newer.
type snapshot struct {
	Version int    `json:"rookMuxState"`
	Serial  uint64 `json:"serial"`
	Focus   struct {
		Pane uint32 `json:"pane"`
		Mode string `json:"mode"`
	} `json:"focus"`
	Workspaces []struct {
		Name    string `json:"name"`
		Current bool   `json:"current"`
		Windows []struct {
			Index  int             `json:"index"`
			Layout json.RawMessage `json:"layout"`
		} `json:"windows"`
		Pins []uint32 `json:"pins"`
	} `json:"workspaces"`
	Panes []struct {
		ID           uint32 `json:"id"`
		Program      string `json:"program"`
		Cwd          string `json:"cwd"`
		Cols         int    `json:"cols"`
		Rows         int    `json:"rows"`
		Exited       bool   `json:"exited"`
		LastOutputMs int64  `json:"lastOutputMs"`
	} `json:"panes"`
	Pins []struct {
		Pane  uint32 `json:"pane"`
		Scope string `json:"scope"`
	} `json:"pins"`
}

// leaves collects the pane ids in a layout tree, in order.
func leaves(layout json.RawMessage, out *[]uint32) {
	var n struct {
		Pane *uint32         `json:"pane"`
		A    json.RawMessage `json:"a"`
		B    json.RawMessage `json:"b"`
	}
	if json.Unmarshal(layout, &n) != nil {
		return
	}
	if n.Pane != nil {
		*out = append(*out, *n.Pane)
		return
	}
	if n.A != nil {
		leaves(n.A, out)
	}
	if n.B != nil {
		leaves(n.B, out)
	}
}

// panes turns a snapshot into the neutral shape. Window is the window
// index or "pin"; Pane is the block id, the stable part — rook
// renumbers windows, never panes.
func (s *snapshot) panes() ([]Pane, map[uint32]ID) {
	place := map[uint32]ID{}
	for _, ws := range s.Workspaces {
		for _, w := range ws.Windows {
			var ids []uint32
			leaves(w.Layout, &ids)
			for _, id := range ids {
				place[id] = ID{Session: ws.Name, Window: strconv.Itoa(w.Index), Pane: strconv.FormatUint(uint64(id), 10)}
			}
		}
		for _, id := range ws.Pins {
			place[id] = ID{Session: ws.Name, Window: "pin", Pane: strconv.FormatUint(uint64(id), 10)}
		}
	}
	for _, p := range s.Pins {
		if p.Scope == "global" {
			place[p.Pane] = ID{Session: "global", Window: "pin", Pane: strconv.FormatUint(uint64(p.Pane), 10)}
		}
	}
	out := make([]Pane, 0, len(s.Panes))
	for _, p := range s.Panes {
		id, ok := place[p.ID]
		if !ok {
			id = ID{Pane: strconv.FormatUint(uint64(p.ID), 10)}
		}
		pane := Pane{ID: id, Command: p.Program, Path: p.Cwd, Dead: p.Exited}
		if p.LastOutputMs > 0 {
			pane.Active = time.UnixMilli(p.LastOutputMs)
		}
		out = append(out, pane)
	}
	return out, place
}

func (s *snapshot) focus() (*Pane, error) {
	panes, _ := s.panes()
	want := strconv.FormatUint(uint64(s.Focus.Pane), 10)
	for i := range panes {
		if panes[i].ID.Pane == want {
			return &panes[i], nil
		}
	}
	return nil, ErrNoFocus
}

func (r *Rook) state(ctx context.Context) (*snapshot, error) {
	c, err := r.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err := send(c, c2sState, []byte{0}); err != nil {
		return nil, ErrUnavailable
	}
	f, err := waitFor(c, bufio.NewReader(c), s2cStateJSON, 2*time.Second)
	if err != nil {
		return nil, ErrUnavailable
	}
	var s snapshot
	if err := json.Unmarshal(f.payload, &s); err != nil {
		return nil, fmt.Errorf("rook: bad state: %w", err)
	}
	return &s, nil
}

func (r *Rook) List(ctx context.Context) ([]Pane, error) {
	s, err := r.state(ctx)
	if err != nil {
		return nil, err
	}
	panes, _ := s.panes()
	return panes, nil
}

func (r *Rook) Get(ctx context.Context, id ID) (*Pane, error) {
	s, err := r.state(ctx)
	if err != nil {
		return nil, err
	}
	panes, _ := s.panes()
	for i := range panes {
		if panes[i].ID.Pane == id.Pane {
			return &panes[i], nil
		}
	}
	return nil, ErrNoPane
}

func (r *Rook) Focus(ctx context.Context) (*Pane, error) {
	s, err := r.state(ctx)
	if err != nil {
		return nil, err
	}
	return s.focus()
}

func blockID(id ID) (uint32, bool) {
	n, err := strconv.ParseUint(id.Pane, 10, 32)
	return uint32(n), err == nil
}

// --- commands -----------------------------------------------------------

// attach opens a block client on id. flags: 1 takes the resize lease
// (with cols×rows), 2 asks for scrollback backfill. Rook answers with a
// snapshot draw and then the raw tee; a command connection ignores
// both.
func (r *Rook) attach(ctx context.Context, id ID, cols, rows int, flags byte) (net.Conn, *bufio.Reader, error) {
	bid, ok := blockID(id)
	if !ok {
		return nil, nil, ErrNoPane
	}
	c, err := r.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	var p [9]byte
	binary.LittleEndian.PutUint32(p[0:], bid)
	binary.LittleEndian.PutUint16(p[4:], uint16(cols))
	binary.LittleEndian.PutUint16(p[6:], uint16(rows))
	p[8] = flags
	if err := send(c, c2sAttachBlock, p[:]); err != nil {
		c.Close()
		return nil, nil, ErrUnavailable
	}
	br := bufio.NewReader(c)
	if _, err := waitFor(c, br, s2cDraw, 2*time.Second); err != nil {
		c.Close()
		if strings.Contains(err.Error(), "no such block") {
			return nil, nil, ErrNoPane
		}
		return nil, nil, err
	}
	return c, br, nil
}

func (r *Rook) Send(ctx context.Context, id ID, text string) error {
	if text == "" {
		return nil
	}
	c, _, err := r.attach(ctx, id, 0, 0, 0)
	if err != nil {
		return err
	}
	defer c.Close()
	return send(c, c2sStdin, []byte(text))
}

func (r *Rook) Enter(ctx context.Context, id ID) error {
	c, _, err := r.attach(ctx, id, 0, 0, 0)
	if err != nil {
		return err
	}
	defer c.Close()
	return send(c, c2sStdin, []byte("\r"))
}

// Capture asks for the viewport as text: rows joined by newlines,
// trailing blanks already trimmed, no styling.
func (r *Rook) Capture(ctx context.Context, id ID) ([]string, error) {
	bid, ok := blockID(id)
	if !ok {
		return nil, ErrNoPane
	}
	c, err := r.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var p [4]byte
	binary.LittleEndian.PutUint32(p[:], bid)
	if err := send(c, c2sCapture, p[:]); err != nil {
		return nil, ErrUnavailable
	}
	f, err := waitFor(c, bufio.NewReader(c), s2cText, 2*time.Second)
	if err != nil {
		// rook answers nothing for an unknown pane; a timeout is the
		// only signal, so tell the two apart by asking.
		if _, gerr := r.Get(ctx, id); gerr != nil {
			return nil, gerr
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(f.payload), "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

func (r *Rook) Kill(ctx context.Context, id ID) error {
	c, _, err := r.attach(ctx, id, 0, 0, 0)
	if err != nil {
		return err
	}
	defer c.Close()
	return send(c, c2sBlockCmd, []byte{'x'})
}

// GoTo switches to the pane's workspace. Focusing one pane within it
// is the next verb rook does not have.
func (r *Rook) GoTo(ctx context.Context, id ID) error {
	c, err := r.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return send(c, c2sSession, append([]byte{'s'}, id.Session...))
}

// Spawn opens a workspace quietly ('N': the view stays where it is)
// or a window in an existing one, and gets the new pane's id back as
// block_created. Rook starts a shell; the command is typed into it,
// which leaves a prompt behind when the program exits rather than a
// dead pane.
func (r *Rook) Spawn(ctx context.Context, s Spawn) (*Pane, error) {
	if s.Session == "" {
		return nil, errors.New("spawn needs a session")
	}
	snap, err := r.state(ctx)
	if err != nil {
		return nil, err
	}
	var existing []Pane
	panes, _ := snap.panes()
	for _, p := range panes {
		if p.ID.Session == s.Session && p.ID.Window != "pin" && !p.Dead {
			existing = append(existing, p)
		}
	}

	var c net.Conn
	var br *bufio.Reader
	if len(existing) > 0 {
		// A window beside the existing ones: any block in the
		// workspace can ask for 'c'. Rook opens it in that block's cwd;
		// Dir is applied by the shell below.
		c, br, err = r.attach(ctx, existing[0].ID, 0, 0, 0)
		if err != nil {
			return nil, err
		}
		err = send(c, c2sBlockCmd, []byte{'c'})
	} else {
		c, err = r.dial(ctx)
		if err != nil {
			return nil, err
		}
		br = bufio.NewReader(c)
		payload := append([]byte{'N'}, s.Session...)
		if s.Dir != "" {
			payload = append(append(payload, '\t'), s.Dir...)
		}
		err = send(c, c2sSession, payload)
	}
	if err != nil {
		c.Close()
		return nil, ErrUnavailable
	}
	f, err := waitFor(c, br, s2cBlockCreated, 2*time.Second)
	c.Close()
	if err != nil || len(f.payload) < 4 {
		return nil, errors.New("rook: no reply to spawn")
	}
	id := binary.LittleEndian.Uint32(f.payload)
	// The snapshot after the ack names the window it landed in.
	p, err := r.Get(ctx, ID{Pane: strconv.FormatUint(uint64(id), 10)})
	if err != nil {
		return nil, err
	}

	if len(s.Command) > 0 || (len(existing) > 0 && s.Dir != "") {
		var line strings.Builder
		if s.Dir != "" {
			line.WriteString("cd " + shellQuote(s.Dir) + " && ")
		}
		for _, e := range s.Env {
			line.WriteString("export " + shellQuote(e) + " && ")
		}
		if len(s.Command) > 0 {
			line.WriteString(shellJoin(s.Command))
		} else {
			line.WriteString("clear")
		}
		// The pty queues input before the shell reads it, so this is
		// safe at once; a beat keeps the line clear of the prompt's own
		// first paint. The leading space keeps it out of history.
		time.Sleep(150 * time.Millisecond)
		if err := r.Send(ctx, p.ID, " "+line.String()); err != nil {
			return p, err
		}
		if err := r.Enter(ctx, p.ID); err != nil {
			return p, err
		}
	}
	return p, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Narrow takes the resize lease on the block and HOLDS the connection:
// the lease is the connection, and the geometry returns to the desk
// when it drops. The desk never reflows.
func (r *Rook) Narrow(ctx context.Context, id ID, cols int) error {
	if cols < 20 || cols > 300 {
		cols = 52
	}
	p, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	rows := 40
	if s, err := r.state(ctx); err == nil {
		for _, sp := range s.Panes {
			if strconv.FormatUint(uint64(sp.ID), 10) == p.ID.Pane && sp.Rows > 0 {
				rows = sp.Rows
			}
		}
	}
	r.mu.Lock()
	old := r.leases[id]
	r.mu.Unlock()
	if old != nil {
		var g [4]byte
		binary.LittleEndian.PutUint16(g[0:], uint16(cols))
		binary.LittleEndian.PutUint16(g[2:], uint16(rows))
		return send(old, c2sResize, g[:])
	}
	c, _, err := r.attach(ctx, id, cols, rows, 1)
	if err != nil {
		return err
	}
	// Drain what rook streams to a viewer so the socket never fills.
	go func() { _, _ = io.Copy(io.Discard, c) }()
	r.mu.Lock()
	r.leases[id] = c
	r.mu.Unlock()
	return nil
}

func (r *Rook) Widen(ctx context.Context, id ID) error {
	r.mu.Lock()
	c := r.leases[id]
	delete(r.leases, id)
	r.mu.Unlock()
	if c != nil {
		return c.Close()
	}
	return nil
}

func (r *Rook) Poke() {
	select {
	case r.poke <- struct{}{}:
	default:
	}
}

// Watch subscribes to the feed and turns each snapshot into events:
// focus and pane changes, exits, and the mux going away and coming
// back. A ticker re-asks so activity — which the pushed stream holds
// back — stays fresh.
func (r *Rook) Watch(ctx context.Context, fn func(Event)) error {
	every := r.Every
	if every == 0 {
		every = 5 * time.Second
	}
	last := map[ID]Pane{}
	var lastFocus *Pane
	gone := false
	for {
		err := r.watchOnce(ctx, every, last, &lastFocus, &gone, fn)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !gone {
			slog.Info("mux: rook unavailable", "sock", r.Sock, "error", errText(err))
			gone = true
			now := time.Now()
			for _, p := range last {
				fn(Event{Kind: PaneExited, Pane: &p, At: now})
			}
			clear(last)
			if lastFocus != nil {
				lastFocus = nil
				fn(Event{Kind: FocusChanged, At: now})
			}
			fn(Event{Kind: Gone, At: now})
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		case <-r.poke:
		}
	}
}

func (r *Rook) watchOnce(ctx context.Context, every time.Duration, last map[ID]Pane, lastFocus **Pane, gone *bool, fn func(Event)) error {
	c, err := r.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := send(c, c2sState, []byte{1}); err != nil {
		return err
	}
	if *gone {
		*gone = false
		fn(Event{Kind: Back, At: time.Now()})
	}
	frames := make(chan frame, 8)
	errs := make(chan error, 1)
	go func() {
		br := bufio.NewReader(c)
		for {
			f, err := recv(br)
			if err != nil {
				errs <- err
				return
			}
			frames <- f
		}
	}()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			return err
		case <-tick.C:
			if err := send(c, c2sState, []byte{0}); err != nil {
				return err
			}
		case <-r.poke:
			if err := send(c, c2sState, []byte{0}); err != nil {
				return err
			}
		case f := <-frames:
			if f.kind != s2cStateJSON {
				continue
			}
			var s snapshot
			if json.Unmarshal(f.payload, &s) != nil {
				continue
			}
			now := time.Now()
			panes, _ := s.panes()
			seen := map[ID]Pane{}
			for _, p := range panes {
				seen[p.ID] = p
				old, ok := last[p.ID]
				switch {
				case ok && p.Dead && !old.Dead:
					fn(Event{Kind: PaneExited, Pane: &p, At: now})
				case !ok || old != p:
					fn(Event{Kind: PaneChanged, Pane: &p, At: now})
				}
			}
			for id, old := range last {
				if _, ok := seen[id]; !ok {
					fn(Event{Kind: PaneExited, Pane: &old, At: now})
				}
			}
			clear(last)
			for k, v := range seen {
				last[k] = v
			}
			focus, ferr := s.focus()
			switch {
			case ferr != nil && *lastFocus != nil:
				*lastFocus = nil
				fn(Event{Kind: FocusChanged, At: now})
			case ferr == nil && (*lastFocus == nil || focus.ID != (*lastFocus).ID || focus.Command != (*lastFocus).Command || focus.Path != (*lastFocus).Path):
				*lastFocus = focus
				fn(Event{Kind: FocusChanged, Pane: focus, At: now})
			}
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
