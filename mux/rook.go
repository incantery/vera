package mux

import (
	"bufio"
	"context"
	"encoding/binary"
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
// The wire is rook's placeholder protocol (mux/src/proto.zig): one
// type byte, a little-endian length, a payload; the structured cell
// protocol replaces it. This file is written to be thrown away with
// it — a thin client, no cleverness, one connection per question.
//
// What rook gives that tmux could not: the block table is PUSHED on
// change, so Watch is an event stream rather than a poll; and a block
// client can hold a resize lease, so the phone can narrow a pane
// without reflowing the desk. What it does not give yet — which pane
// has the person, a per-block activity pulse, a plain-text snapshot,
// a spawn that carries a command — is noted at each verb, and each
// is a small ask on rook's side rather than a workaround here.
type Rook struct {
	// Sock is the engine's socket; empty means $ROOK_MUX_SOCK or the
	// default beside rook's state.
	Sock string
	// Every is how often Watch re-asks for the table between pushes.
	Every time.Duration

	poke chan struct{}

	mu     sync.Mutex
	leases map[ID]net.Conn // Narrow holds a connection per leased block
}

// rook's message kinds (proto.zig).
const (
	c2sAttach      byte = 1
	c2sStdin       byte = 2
	c2sResize      byte = 3
	c2sDetach      byte = 4
	c2sSession     byte = 9
	c2sBlocks      byte = 10
	c2sAttachBlock byte = 11
	c2sBlockCmd    byte = 12

	s2cDraw         byte = 1
	s2cExit         byte = 2
	s2cStatsText    byte = 3
	s2cBlocksText   byte = 4
	s2cBlockCreated byte = 5
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
// passes. Frames of other kinds are dropped — a control connection is
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

// --- the block table ----------------------------------------------------

// block is one row of rook's table: id, place (workspace:window, or
// "global:pin" / "<ws>:pin"), foreground program, size, cwd.
type block struct {
	id   uint32
	ws   string
	slot string
	fg   string
	cols int
	rows int
	cwd  string
}

func parseBlocks(text string) []block {
	var out []block
	for _, line := range strings.Split(text, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) < 5 {
			continue
		}
		id, err := strconv.ParseUint(f[0], 10, 32)
		if err != nil {
			continue
		}
		ws, slot, _ := strings.Cut(f[1], ":")
		b := block{id: uint32(id), ws: ws, slot: slot, fg: f[2], cwd: f[4]}
		if c, r, ok := strings.Cut(f[3], "x"); ok {
			b.cols, _ = strconv.Atoi(c)
			b.rows, _ = strconv.Atoi(r)
		}
		out = append(out, b)
	}
	return out
}

// pane maps a block to the neutral shape. Window is the block's slot
// (a window index or "pin"); Pane is the block id, which is the stable
// part — rook renumbers windows, never blocks. Active is zero: rook
// does not report a pulse yet.
func (b block) pane() Pane {
	return Pane{ID: ID{Session: b.ws, Window: b.slot, Pane: strconv.FormatUint(uint64(b.id), 10)}, Command: b.fg, Path: b.cwd}
}

func blockID(id ID) (uint32, bool) {
	n, err := strconv.ParseUint(id.Pane, 10, 32)
	return uint32(n), err == nil
}

func (r *Rook) blocks(ctx context.Context) ([]block, error) {
	c, err := r.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err := send(c, c2sBlocks, nil); err != nil {
		return nil, ErrUnavailable
	}
	f, err := waitFor(c, bufio.NewReader(c), s2cBlocksText, 2*time.Second)
	if err != nil {
		return nil, ErrUnavailable
	}
	return parseBlocks(string(f.payload)), nil
}

func (r *Rook) List(ctx context.Context) ([]Pane, error) {
	bs, err := r.blocks(ctx)
	if err != nil {
		return nil, err
	}
	panes := make([]Pane, 0, len(bs))
	for _, b := range bs {
		panes = append(panes, b.pane())
	}
	return panes, nil
}

func (r *Rook) find(ctx context.Context, id ID) (block, error) {
	want, ok := blockID(id)
	if !ok {
		return block{}, ErrNoPane
	}
	bs, err := r.blocks(ctx)
	if err != nil {
		return block{}, err
	}
	for _, b := range bs {
		if b.id == want {
			return b, nil
		}
	}
	return block{}, ErrNoPane
}

func (r *Rook) Get(ctx context.Context, id ID) (*Pane, error) {
	b, err := r.find(ctx, id)
	if err != nil {
		return nil, err
	}
	p := b.pane()
	return &p, nil
}

// Focus: rook does not yet tell a client which block has the person.
// The ask is one s2c event; until then there is no focus to report.
func (r *Rook) Focus(ctx context.Context) (*Pane, error) {
	if _, err := r.blocks(ctx); err != nil {
		return nil, err
	}
	return nil, ErrNoFocus
}

// --- a connection attached to one block ---------------------------------

// attach opens a block client on id. flags: 1 takes the resize lease
// (with cols×rows), 2 asks for scrollback backfill. The reply is a
// snapshot draw frame, returned so Capture can read it; other callers
// discard it.
func (r *Rook) attach(ctx context.Context, id ID, cols, rows int, flags byte) (net.Conn, *bufio.Reader, []byte, error) {
	bid, ok := blockID(id)
	if !ok {
		return nil, nil, nil, ErrNoPane
	}
	c, err := r.dial(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	var p [9]byte
	binary.LittleEndian.PutUint32(p[0:], bid)
	binary.LittleEndian.PutUint16(p[4:], uint16(cols))
	binary.LittleEndian.PutUint16(p[6:], uint16(rows))
	p[8] = flags
	if err := send(c, c2sAttachBlock, p[:]); err != nil {
		c.Close()
		return nil, nil, nil, ErrUnavailable
	}
	br := bufio.NewReader(c)
	f, err := waitFor(c, br, s2cDraw, 2*time.Second)
	if err != nil {
		c.Close()
		if strings.Contains(err.Error(), "no such block") {
			return nil, nil, nil, ErrNoPane
		}
		return nil, nil, nil, err
	}
	return c, br, f.payload, nil
}

func (r *Rook) Send(ctx context.Context, id ID, text string) error {
	if text == "" {
		return nil
	}
	c, _, _, err := r.attach(ctx, id, 0, 0, 0)
	if err != nil {
		return err
	}
	defer c.Close()
	return send(c, c2sStdin, []byte(text))
}

func (r *Rook) Enter(ctx context.Context, id ID) error {
	c, _, _, err := r.attach(ctx, id, 0, 0, 0)
	if err != nil {
		return err
	}
	defer c.Close()
	return send(c, c2sStdin, []byte("\r"))
}

// Capture attaches, takes the snapshot rook sends every block client
// on arrival, and reads the rows back out of it. The ask on rook's
// side is a plain-text flag; until then the frame is decoded here.
func (r *Rook) Capture(ctx context.Context, id ID) ([]string, error) {
	b, err := r.find(ctx, id)
	if err != nil {
		return nil, err
	}
	c, _, snap, err := r.attach(ctx, id, 0, 0, 0)
	if err != nil {
		return nil, err
	}
	c.Close()
	lines := rowsOf(snap, b.rows)
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

func (r *Rook) Kill(ctx context.Context, id ID) error {
	c, _, _, err := r.attach(ctx, id, 0, 0, 0)
	if err != nil {
		return err
	}
	defer c.Close()
	return send(c, c2sBlockCmd, []byte{'x'})
}

// GoTo switches to the block's workspace. rook has no verb yet to
// focus one block within it.
func (r *Rook) GoTo(ctx context.Context, id ID) error {
	c, err := r.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return send(c, c2sSession, append([]byte{'s'}, id.Session...))
}

// Spawn: a new workspace when the session is unknown, else a new
// window in it. rook starts a shell; the command, if any, is typed
// into it — which leaves a prompt behind when the program exits
// rather than a dead pane.
//
// Caveat, until rook grows a quiet form: creating a workspace makes it
// current, which moves the person's view. A new window does not.
func (r *Rook) Spawn(ctx context.Context, s Spawn) (*Pane, error) {
	if s.Session == "" {
		return nil, errors.New("spawn needs a session")
	}
	before, err := r.blocks(ctx)
	if err != nil {
		return nil, err
	}
	var in []block
	for _, b := range before {
		if b.ws == s.Session && b.slot != "pin" {
			in = append(in, b)
		}
	}
	var created *block
	if len(in) > 0 {
		// A window beside the existing ones: attach to any block in
		// the workspace and ask for 'c'. rook opens it in that block's
		// cwd; Dir is applied by the shell below.
		c, br, _, err := r.attach(ctx, in[0].pane().ID, 0, 0, 0)
		if err != nil {
			return nil, err
		}
		if err := send(c, c2sBlockCmd, []byte{'c'}); err != nil {
			c.Close()
			return nil, ErrUnavailable
		}
		f, err := waitFor(c, br, s2cBlockCreated, 2*time.Second)
		c.Close()
		if err != nil || len(f.payload) < 4 {
			return nil, fmt.Errorf("rook: no reply to new window")
		}
		id := binary.LittleEndian.Uint32(f.payload)
		created, err = r.await(ctx, func(b block) bool { return b.id == id })
		if err != nil {
			return nil, err
		}
	} else {
		c, err := r.dial(ctx)
		if err != nil {
			return nil, err
		}
		payload := append([]byte{'n'}, s.Session...)
		if s.Dir != "" {
			payload = append(append(payload, '\t'), s.Dir...)
		}
		err = send(c, c2sSession, payload)
		c.Close()
		if err != nil {
			return nil, ErrUnavailable
		}
		known := map[uint32]bool{}
		for _, b := range before {
			known[b.id] = true
		}
		created, err = r.await(ctx, func(b block) bool { return b.ws == s.Session && b.slot != "pin" && !known[b.id] })
		if err != nil {
			return nil, err
		}
	}
	p := created.pane()
	if len(s.Command) > 0 || len(in) > 0 && s.Dir != "" {
		line := ""
		if s.Dir != "" {
			line += "cd " + shellQuote(s.Dir) + " && "
		}
		for _, e := range s.Env {
			line += "export " + shellQuote(e) + " && "
		}
		if len(s.Command) > 0 {
			line += shellJoin(s.Command)
		} else {
			line += "clear"
		}
		// The shell may not have read its prompt yet; the pty queues
		// input, so this is safe — but give it a beat so the line is
		// not painted over by the shell's startup.
		time.Sleep(150 * time.Millisecond)
		if err := r.Send(ctx, p.ID, " "+line); err != nil { // leading space: out of history
			return &p, err
		}
		if err := r.Enter(ctx, p.ID); err != nil {
			return &p, err
		}
	}
	return &p, nil
}

// await polls the table briefly for a block matching pred.
func (r *Rook) await(ctx context.Context, pred func(block) bool) (*block, error) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		bs, err := r.blocks(ctx)
		if err != nil {
			return nil, err
		}
		for _, b := range bs {
			if pred(b) {
				return &b, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil, errors.New("rook: the new pane did not appear")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Narrow takes the resize lease on the block and HOLDS the connection:
// the lease is the connection, and the geometry returns to the desk
// when it drops. This is the thing tmux could not do — the desk never
// reflows.
func (r *Rook) Narrow(ctx context.Context, id ID, cols int) error {
	if cols < 20 || cols > 300 {
		cols = 52
	}
	b, err := r.find(ctx, id)
	if err != nil {
		return err
	}
	rows := b.rows
	if rows == 0 {
		rows = 40
	}
	r.mu.Lock()
	old := r.leases[id]
	r.mu.Unlock()
	if old != nil {
		// Same lease, new width: resize on the held connection.
		var g [4]byte
		binary.LittleEndian.PutUint16(g[0:], uint16(cols))
		binary.LittleEndian.PutUint16(g[2:], uint16(rows))
		return send(old, c2sResize, g[:])
	}
	c, _, _, err := r.attach(ctx, id, cols, rows, 1)
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

// Watch holds one connection with the table subscription open and
// turns each push into events. rook pushes on structural change and
// on foreground/cwd drift; a re-ask on the ticker covers a missed one.
// Focus events never come — rook does not send them yet.
func (r *Rook) Watch(ctx context.Context, fn func(Event)) error {
	every := r.Every
	if every == 0 {
		every = 5 * time.Second
	}
	last := map[ID]Pane{}
	gone := false
	for {
		err := r.watchOnce(ctx, every, last, &gone, fn)
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

func (r *Rook) watchOnce(ctx context.Context, every time.Duration, last map[ID]Pane, gone *bool, fn func(Event)) error {
	c, err := r.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := send(c, c2sBlocks, nil); err != nil {
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
			if err := send(c, c2sBlocks, nil); err != nil {
				return err
			}
		case <-r.poke:
			if err := send(c, c2sBlocks, nil); err != nil {
				return err
			}
		case f := <-frames:
			if f.kind != s2cBlocksText {
				continue
			}
			now := time.Now()
			seen := map[ID]Pane{}
			for _, b := range parseBlocks(string(f.payload)) {
				p := b.pane()
				seen[p.ID] = p
				if old, ok := last[p.ID]; !ok || old != p {
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
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// --- reading rows out of a snapshot -------------------------------------

// rowsOf turns rook's snapshot frame — clear, home, then each row
// painted with cursor moves and SGR — back into plain rows. It knows
// exactly as much VT as that frame uses: CUP, SGR and the other CSI
// sequences (skipped), OSC (skipped), CR/LF, and printable text.
func rowsOf(frame []byte, rows int) []string {
	if rows <= 0 {
		rows = 200
	}
	grid := make([][]rune, rows)
	x, y := 0, 0
	put := func(r rune) {
		if y >= rows {
			return
		}
		for len(grid[y]) <= x {
			grid[y] = append(grid[y], ' ')
		}
		grid[y][x] = r
		x++
	}
	s := []rune(string(frame))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[':
			// CSI: parameters, then a final byte in 0x40–0x7e.
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				return finish(grid)
			}
			params := string(s[i+2 : j])
			switch s[j] {
			case 'H', 'f':
				row, col := 1, 1
				if a, b, ok := strings.Cut(params, ";"); ok {
					row, _ = strconv.Atoi(a)
					col, _ = strconv.Atoi(b)
				} else if params != "" {
					row, _ = strconv.Atoi(params)
				}
				if row < 1 {
					row = 1
				}
				if col < 1 {
					col = 1
				}
				y, x = row-1, col-1
			case 'J':
				if params == "" || params == "2" {
					for k := range grid {
						grid[k] = nil
					}
				}
			case 'K':
				if y < rows && x < len(grid[y]) {
					grid[y] = grid[y][:x]
				}
			case 'C':
				n, _ := strconv.Atoi(params)
				if n < 1 {
					n = 1
				}
				x += n
			}
			i = j
		case c == 0x1b && i+1 < len(s) && s[i+1] == ']':
			// OSC: to BEL or ST.
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j < len(s) && s[j] == 0x1b {
				j++
			}
			i = j
		case c == 0x1b:
			// Some other escape: skip it and its one following byte.
			i++
		case c == '\r':
			x = 0
		case c == '\n':
			y++
			x = 0
		case c < 0x20:
			// other control: ignore
		default:
			put(c)
		}
	}
	return finish(grid)
}

func finish(grid [][]rune) []string {
	out := make([]string, len(grid))
	for i, row := range grid {
		out[i] = strings.TrimRight(string(row), " ")
	}
	return out
}
