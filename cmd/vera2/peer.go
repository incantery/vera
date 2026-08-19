// The peer transport: reaching the phone without the network's help.
//
// LAN works at home and fails in a hotel, because access points
// routinely isolate clients from each other at layer 2 — the phone and
// the Mac are on the same wifi and still cannot exchange a packet. That
// is not a thing more code on this side can fix; it needs a radio link
// that does not involve the access point at all, which on Apple
// hardware means AWDL, which means Network.framework, for which there
// is no Go.
//
// So a small Swift sidecar owns the radio and nothing else: it
// advertises the service, accepts peers, and copies bytes into a unix
// socket. Everything above that byte stream — framing, auth, the
// protocol — stays here, in one place, in one language.
//
// The protocol is NOT HTTP. The Transport interface was made
// message-shaped precisely so this implementation would not have to
// invent status codes and header parsing for a link that has neither.
// One request per connection, length-prefixed JSON both ways.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const peerService = "_vera._tcp"

// ask is what a phone sends: one request, then it listens.
type ask struct {
	Secret string  `json:"secret"`
	Op     string  `json:"op"` // "say" or "resume"
	Msg    Message `json:"message,omitempty"`
	Run    string  `json:"run,omitempty"`
	From   int     `json:"from,omitempty"`
}

type peerTransport struct {
	id      Identity
	runs    *Runs
	socket  string
	sidecar string

	// Closed once the radio is genuinely advertising, or carrying the
	// reason it is not. The banner should not claim peer-to-peer is on
	// before it is — that was the first bug this transport had.
	ready chan error
}

func newPeer(id Identity, socket, sidecar string) *peerTransport {
	return &peerTransport{
		id: id, runs: newRuns(),
		socket: shortSocket(socket), sidecar: sidecar,
		ready: make(chan error, 1),
	}
}

// shortSocket keeps the path inside the sockaddr_un limit — 104 bytes
// on macOS, and a silent "invalid argument" from bind when exceeded,
// which reads like anything except a length problem. The usual path is
// nowhere near it; a relocated state directory can be.
func shortSocket(path string) string {
	const limit = 100
	if len(path) <= limit {
		return path
	}
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(os.TempDir(), "vera2-"+hex.EncodeToString(sum[:6])+".sock")
}

func (p *peerTransport) Name() string { return "peer" }

// Hints is empty, and that is the point. A peer transport has no
// address to offer — which is exactly why the pairing code was built to
// carry an identity rather than one.
func (p *peerTransport) Hints() []string { return nil }

func (p *peerTransport) Serve(ctx context.Context, h Handler) error {
	if err := os.MkdirAll(filepath.Dir(p.socket), 0o700); err != nil {
		return err
	}
	// A socket left by a previous run would refuse to bind.
	_ = os.Remove(p.socket)

	listener, err := net.Listen("unix", p.socket)
	if err != nil {
		p.announceReady(err)
		return err
	}
	defer listener.Close()
	defer os.Remove(p.socket)
	// Only the sidecar, running as this user, ever connects.
	_ = os.Chmod(p.socket, 0o600)

	sidecar, err := p.startSidecar(ctx)
	p.announceReady(err)
	if err != nil {
		return err
	}
	defer func() {
		if sidecar.Process != nil {
			_ = sidecar.Process.Kill()
		}
	}()

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go p.answer(ctx, conn, h)
	}
}

// answer handles one peer connection: read the request, check the
// secret, then stream the run until it ends or the peer leaves.
func (p *peerTransport) answer(ctx context.Context, conn net.Conn, h Handler) {
	defer conn.Close()

	// A peer that connects and says nothing should not hold a goroutine
	// forever. Only the REQUEST is deadlined; the answer may take
	// minutes and sets its own terms.
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	raw, err := readFrame(conn)
	if err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	var request ask
	if json.Unmarshal(raw, &request) != nil {
		_ = writeFrame(conn, Frame{Error: "I couldn't read that."})
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Secret), []byte(p.id.Secret)) != 1 {
		// Same silence as the LAN door: a wrong secret and an unknown
		// peer get the same answer.
		_ = writeFrame(conn, Frame{Error: "unauthorized"})
		return
	}

	send := func(f Frame) error { return writeFrame(conn, f) }

	switch request.Op {
	case "resume":
		run := p.runs.find(request.Run)
		if run == nil {
			_ = send(Frame{Error: "That was too long ago — I'm not holding it any more."})
			return
		}
		_ = run.follow(watchCtx(ctx, conn), request.From, send)

	default: // "say"
		run := p.runs.start()
		work, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		go func() {
			defer cancel()
			defer run.finish()
			run.append(Frame{Run: run.ID})
			if err := h(work, request.Msg, func(f Frame) error {
				run.append(f)
				return nil
			}); err != nil {
				run.append(Frame{Error: err.Error()})
			}
		}()
		_ = run.follow(watchCtx(ctx, conn), 0, send)
	}
}

// watchCtx ends when the peer hangs up, so follow stops writing into a
// dead socket — without touching the run, which belongs to nobody.
func watchCtx(parent context.Context, conn net.Conn) context.Context {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		// A peer that has said its piece sends nothing more, so any
		// read returning is the connection ending.
		var scratch [1]byte
		_, _ = conn.Read(scratch[:])
		cancel()
	}()
	return ctx
}

// MARK: - Framing
//
// Four bytes of big-endian length, then JSON. Not newline-delimited
// like the LAN transport: that one is meant to be read with curl in a
// terminal, and this one will never be.

const maxFrame = 4 << 20

func writeFrame(w io.Writer, f Frame) error {
	payload, err := json.Marshal(f)
	if err != nil {
		return err
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func readFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n > maxFrame {
		return nil, errors.New("frame too large")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// MARK: - The sidecar process

func (p *peerTransport) startSidecar(ctx context.Context) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, p.sidecar,
		"--socket", p.socket,
		"--service", peerService,
		"--name", p.id.Name,
		"--peer", p.id.Peer,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the peer sidecar: %w", err)
	}

	// It prints one line when the radio is actually advertising. Until
	// then it is not listening and saying so would be a lie.
	ready := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := stdout.Read(buf)
		if err != nil || n == 0 {
			ready <- errors.New("the peer sidecar stopped before it was ready")
			return
		}
		ready <- nil
	}()

	select {
	case err := <-ready:
		if err != nil {
			return nil, err
		}
	case <-time.After(10 * time.Second):
		return nil, errors.New("the peer sidecar did not come up")
	}

	slog.Info("peer transport ready", "service", peerService, "peer", p.id.Peer)
	return cmd, nil
}

// MARK: - Building the sidecar

//go:embed peer/sidecar.swift
var sidecarSource string

// sidecarBinary compiles the sidecar on first use and caches it by the
// hash of its own source, so editing the Swift rebuilds it and nothing
// else does.
//
// Built rather than shipped: a checked-in binary would need signing,
// would drift from the source beside it, and would be the first thing
// to break on a new macOS. swiftc is already on any Mac that has
// Xcode, and the compile takes about a second, once.
func sidecarBinary() (string, error) {
	sum := sha256.Sum256([]byte(sidecarSource))
	name := "vera-peer-" + hex.EncodeToString(sum[:6])

	dir := filepath.Join(stateDir(), "vera2", "bin")
	binary := filepath.Join(dir, name)
	if _, err := os.Stat(binary); err == nil {
		return binary, nil
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		return "", errors.New("peer-to-peer needs swiftc, which comes with Xcode's command line tools")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	source := filepath.Join(dir, name+".swift")
	if err := os.WriteFile(source, []byte(sidecarSource), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(source)

	build := exec.Command("swiftc", "-O", source, "-o", binary)
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the peer sidecar: %s", trim(string(out), 400))
	}
	slog.Info("built the peer sidecar", "path", binary)
	return binary, nil
}

func stateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state")
}

func (p *peerTransport) announceReady(err error) {
	select {
	case p.ready <- err:
	default:
	}
}

// Ready waits briefly for the radio to come up and says what happened.
func (p *peerTransport) Ready(within time.Duration) string {
	select {
	case err := <-p.ready:
		if err != nil {
			return "unavailable — " + err.Error()
		}
		return "advertising " + peerService + " as " + p.id.Name
	case <-time.After(within):
		return "still starting"
	}
}
