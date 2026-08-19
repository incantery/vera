// The LAN transport: an HTTP listener, and the simplest thing that
// could possibly carry a conversation.
//
// Chosen first because it is honest about what it is — you can curl it,
// watch the frames arrive in a terminal, and tell instantly whether a
// silent phone is a transport problem or a handler problem. That is the
// whole reason it exists ahead of the peer-to-peer one: it is the
// known-good baseline the harder transport gets debugged against.
//
// It will not survive a hotel. Access points routinely isolate clients
// from each other at layer 2, and then this listener is running
// perfectly and reachable by nobody.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type lanTransport struct {
	addr   string
	id     Identity
	runs   *Runs
	server *http.Server

	mu   sync.Mutex
	port string
}

func newLAN(addr string, id Identity) *lanTransport {
	return &lanTransport{addr: addr, id: id, runs: newRuns()}
}

func (l *lanTransport) Name() string { return "lan" }

func (l *lanTransport) Hints() []string {
	l.mu.Lock()
	port := l.port
	l.mu.Unlock()
	if port == "" {
		_, port, _ = net.SplitHostPort(l.addr)
	}
	return lanHints(port)
}

func (l *lanTransport) Serve(ctx context.Context, h Handler) error {
	ln, err := net.Listen("tcp", l.addr)
	if err != nil {
		return err
	}
	// Ask the listener rather than the flag: ":0" is how the tests get
	// a port nobody else is on, and the pairing hints have to name the
	// port that actually happened.
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	l.mu.Lock()
	l.port = port
	l.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /say", l.say(h))
	mux.HandleFunc("GET /resume", l.resume)
	mux.HandleFunc("GET /ping", l.ping)
	mux.HandleFunc("GET /pair.json", loopbackOnly(l.pairJSON))
	mux.HandleFunc("GET /pair.png", loopbackOnly(l.pairPNG))
	mux.HandleFunc("GET /{$}", loopbackOnly(l.page))

	l.server = &http.Server{
		Handler: mux,
		// No read timeout: an exchange is a person talking, and a
		// person is slow. The write timeout is absent for the same
		// reason a streamed answer is — both would cut off a good
		// conversation to save a socket.
		ReadHeaderTimeout: 10 * time.Second,
	}

	done := make(chan error, 1)
	go func() { done <- l.server.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = l.server.Shutdown(shutdown)
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// say starts a run and shows it to you. The two are separable, which is
// the point: this connection ending does not end the work.
func (l *lanTransport) say(h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !l.authed(r) {
			// No detail. A wrong secret and an unknown peer are the
			// same answer, because telling them apart is a favour to
			// whoever is guessing.
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var msg Message
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&msg); err != nil {
			http.Error(w, "bad message", http.StatusBadRequest)
			return
		}

		run := l.runs.start()

		// Deliberately NOT r.Context(). The request is a viewer; the
		// work is not its property. A generous ceiling stands in for a
		// caller who may never come back, so nothing runs forever.
		work, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		go func() {
			defer cancel()
			defer run.finish()
			run.append(Frame{Run: run.ID})
			if err := h(work, msg, func(f Frame) error {
				run.append(f)
				return nil
			}); err != nil {
				run.append(Frame{Error: err.Error()})
			}
		}()

		l.watch(w, r, run, 0)
	}
}

// resume shows an existing run, starting after the frames the caller
// already has. This is how a phone that was in a pocket catches up
// without replaying an answer it already read.
func (l *lanTransport) resume(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	run := l.runs.find(r.URL.Query().Get("run"))
	if run == nil {
		// Either it never existed or it finished long enough ago to be
		// dropped. Both mean the same thing to the caller: stop waiting.
		http.Error(w, "no such run", http.StatusNotFound)
		return
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	if from < 0 {
		from = 0
	}
	l.watch(w, r, run, from)
}

// watch streams a run to one caller, newline-delimited so the terminal
// and the phone read it the same way.
func (l *lanTransport) watch(w http.ResponseWriter, r *http.Request, run *Run, from int) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vera-Run", run.ID)
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	_ = run.follow(r.Context(), from, func(f Frame) error {
		if err := enc.Encode(f); err != nil {
			return err
		}
		// Flush per frame or the whole point of streaming is lost in a
		// buffer: the phone should see the first words while the rest
		// is still being written.
		return rc.Flush()
	})
}

// ping lets a phone with several address hints find out which one is
// this machine without spending an exchange or a secret on it.
func (l *lanTransport) ping(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"peer": l.id.Peer, "name": l.id.Name})
}

func (l *lanTransport) authed(r *http.Request) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(l.id.Secret)) == 1
}

// loopbackOnly guards the pairing surface. Serving the QR to the LAN
// would hand the secret to exactly the people the secret exists to keep
// out; pairing is a thing you do at the machine.
func loopbackOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			http.Error(w, "pairing is only available on this machine", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
