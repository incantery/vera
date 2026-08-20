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
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
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

	// attention is what the devices have reported; /observe writes it,
	// /status reads it, and the mind recites it.
	attention *Attention
	// how names the mind, for /status — the same phrase the startup
	// banner prints.
	how   string
	since time.Time
	// cleaner tidies dictation; nil (echo mode, no key) means the words
	// go back as they came.
	cleaner func(context.Context, Dictation) (Cleaned, error)
	// poke is the doorbell a local integration rings to say "look
	// again". Loopback-only, carries nothing, trusted for nothing.
	poke map[string]func()
	// typer is the terminal.type capability, when a provider offers it.
	typer func(ctx context.Context, text string, enter, anywhere bool) (*TerminalFocus, error)
	goer  func(ctx context.Context, session, window, pane string) error
	// stt transcribes audio the phone sends, and manages its own engine.
	stt Transcriber

	mu   sync.Mutex
	port string
}

func newLAN(addr string, id Identity) *lanTransport {
	return &lanTransport{addr: addr, id: id, runs: newRuns(), attention: newAttention(), since: time.Now()}
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
	mux.HandleFunc("POST /observe", l.observe)
	mux.HandleFunc("GET /status", l.status)
	mux.HandleFunc("GET /watch", l.watchStatus)
	mux.HandleFunc("POST /dictate", l.dictate)
	mux.HandleFunc("POST /type", l.typeInto)
	mux.HandleFunc("POST /goto", l.goTo)
	mux.HandleFunc("POST /transcribe", l.transcribe)
	mux.HandleFunc("GET /stt", l.sttStatus)
	mux.HandleFunc("POST /stt/install", l.sttInstall)
	mux.HandleFunc("POST /poke/{who}", loopbackOnly(l.pokeHandler))
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
	writeJSON(w, map[string]string{"peer": l.id.Peer, "name": l.id.Name, "version": version})
}

// observe takes one context event from a device. It is fire-and-forget
// on purpose: an observation is a fact about a moment that has already
// passed, and nothing useful can be said back about it.
func (l *lanTransport) observe(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var o Observation
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&o); err != nil || o.Type == "" {
		http.Error(w, "bad observation", http.StatusBadRequest)
		return
	}
	l.attention.Observe(o)
	w.WriteHeader(http.StatusNoContent)
}

func (l *lanTransport) pokeHandler(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	fn := l.poke[r.PathValue("who")]
	l.mu.Unlock()
	if fn == nil {
		http.Error(w, "nobody by that name", http.StatusNotFound)
		return
	}
	fn()
	w.WriteHeader(http.StatusNoContent)
}

// onPoke registers a doorbell.
func (l *lanTransport) onPoke(who string, fn func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.poke == nil {
		l.poke = map[string]func(){}
	}
	l.poke[who] = fn
}

// Goto asks the Mac to bring a place to the front — an app, or a rook
// pane (which also activates the app rook runs in).
type Goto struct {
	Kind     string         `json:"kind"` // "app" or "pane"
	BundleID string         `json:"bundle_id,omitempty"`
	Name     string         `json:"name,omitempty"`
	Terminal *TerminalFocus `json:"terminal,omitempty"`
	Device   string         `json:"device,omitempty"`
}

// goTo is Vera and rook moving the person on their own Mac: activate the
// app, and for a pane deep-link tmux to it. This is the phone's home
// screen reaching across to the desk.
func (l *lanTransport) goTo(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var g Goto
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&g); err != nil {
		http.Error(w, "bad goto", http.StatusBadRequest)
		return
	}
	switch g.Kind {
	case "pane":
		if g.Terminal == nil {
			http.Error(w, "a pane goto needs the pane", http.StatusBadRequest)
			return
		}
		// Bring the terminal app forward first, so the switched pane is
		// actually on screen.
		if host := l.attention.TerminalHost(g.Device); host != nil {
			_ = activateApp(r.Context(), host.BundleID, host.Name)
		}
		if l.goer == nil {
			http.Error(w, "no provider can move within the terminal", http.StatusNotImplemented)
			return
		}
		if err := l.goer(r.Context(), g.Terminal.Session, g.Terminal.Window, g.Terminal.Pane); err != nil {
			http.Error(w, "could not switch panes: "+err.Error(), http.StatusBadGateway)
			return
		}
	case "app":
		if err := activateApp(r.Context(), g.BundleID, g.Name); err != nil {
			http.Error(w, "could not bring that app forward: "+err.Error(), http.StatusBadGateway)
			return
		}
	default:
		http.Error(w, "goto needs a kind of app or pane", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Transcribed is what /transcribe returns.
type Transcribed struct {
	Text string `json:"text"`
}

// transcribe takes recorded audio and returns the words. The phone
// records; the Mac recognises. The body is the audio file, whatever
// format the phone wrote — ffmpeg normalises it.
func (l *lanTransport) transcribe(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if l.stt == nil {
		http.Error(w, "no speech-to-text engine", http.StatusNotImplemented)
		return
	}
	if !l.stt.Status(r.Context()).Ready {
		http.Error(w, "speech-to-text is not installed yet", http.StatusServiceUnavailable)
		return
	}
	f, err := os.CreateTemp("", "vera-audio-*")
	if err != nil {
		http.Error(w, "cannot buffer audio", http.StatusInternalServerError)
		return
	}
	defer os.Remove(f.Name())
	// 200 MB ceiling: a very long dictation is large, but not unbounded.
	if _, err := io.Copy(f, http.MaxBytesReader(w, r.Body, 200<<20)); err != nil {
		f.Close()
		http.Error(w, "bad audio", http.StatusBadRequest)
		return
	}
	f.Close()

	text, err := l.stt.Transcribe(r.Context(), f.Name())
	if err != nil {
		slog.Error("transcribe", "error", err.Error())
		http.Error(w, "transcription failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, Transcribed{Text: text})
}

// sttStatus is what the "download and install" surface reads.
func (l *lanTransport) sttStatus(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if l.stt == nil {
		writeJSON(w, STTStatus{Engine: "none"})
		return
	}
	writeJSON(w, l.stt.Status(r.Context()))
}

// sttInstall runs the install and streams its progress, one ndjson line
// per step, so the button that started it can show what is happening.
func (l *lanTransport) sttInstall(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if l.stt == nil {
		http.Error(w, "no speech-to-text engine", http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	emit := func(step string, done bool, errText string) {
		_ = enc.Encode(Frame{Status: step, Done: done, Error: errText})
		_ = rc.Flush()
	}
	// Its own context: an install outlives the request that asked, the
	// same way a run does — a phone that locks mid-download should not
	// abandon it.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	err := l.stt.Install(ctx, func(step string) { emit(step, false, "") })
	if err != nil {
		emit("", false, err.Error())
		return
	}
	emit("Ready.", true, "")
}

// Typing is a request to put words into the focused pane.
type Typing struct {
	Text string `json:"text"`
	// Clean runs the dictation pass first, with the pane as context.
	Clean bool `json:"clean,omitempty"`
	// Enter presses Enter after the text (or alone, with empty text).
	Enter bool `json:"enter,omitempty"`
	// Anywhere allows a pane that is not a coding agent.
	Anywhere bool   `json:"anywhere,omitempty"`
	Device   string `json:"device,omitempty"`
}

// Typed is what happened.
type Typed struct {
	Text  string         `json:"text"`
	Raw   bool           `json:"raw,omitempty"`
	Into  *TerminalFocus `json:"into,omitempty"`
	Enter bool           `json:"enter,omitempty"`
}

// typeInto is the phone's way into the terminal: the words are cleaned
// if asked, typed into the pane rook says is in front of the person,
// and sent only if the caller said so. The reply names the pane, so a
// phone can show where the words went.
func (l *lanTransport) typeInto(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var t Typing
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&t); err != nil {
		http.Error(w, "bad typing", http.StatusBadRequest)
		return
	}
	if l.typer == nil {
		http.Error(w, "no provider offers terminal.type", http.StatusNotImplemented)
		return
	}
	text := strings.TrimSpace(t.Text)
	out := Typed{Text: text, Enter: t.Enter}
	if t.Clean && text != "" && l.cleaner != nil {
		app := &ObservedApp{Name: "a terminal", BundleID: "terminal"}
		cleaned, err := l.cleaner(r.Context(), Dictation{Text: text, Device: t.Device, App: app})
		if err != nil {
			slog.Warn("typing cleanup failed, typing raw", "error", err.Error())
		}
		if cleaned.Text != "" {
			out.Text, out.Raw = cleaned.Text, cleaned.Raw
		}
	} else if t.Clean {
		out.Raw = true
	}
	into, err := l.typer(r.Context(), out.Text, t.Enter, t.Anywhere)
	out.Into = into
	switch {
	case errors.Is(err, ErrNoTarget):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, ErrNotAgent):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "typing failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, out)
}

// dictate cleans one utterance for the cursor. It never fails the
// caller: the worst answer is the raw text, marked as such.
func (l *lanTransport) dictate(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var d Dictation
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&d); err != nil {
		http.Error(w, "bad dictation", http.StatusBadRequest)
		return
	}
	if l.cleaner == nil {
		writeJSON(w, Cleaned{Text: strings.TrimSpace(d.Text), Raw: true})
		return
	}
	out, err := l.cleaner(r.Context(), d)
	if err != nil {
		slog.Warn("dictation cleanup failed, returning raw", "error", err.Error())
	}
	writeJSON(w, out)
}

// Status is what a device sees when it asks how Vera is. It doubles as
// the heartbeat: a device that polls it is a device that is still here.
type Status struct {
	Version      string              `json:"version"`
	Name         string              `json:"name"`
	Peer         string              `json:"peer"`
	Mind         string              `json:"mind"`
	Since        time.Time           `json:"since"`
	RunsInFlight int                 `json:"runs_in_flight"`
	Devices      []DeviceStatus      `json:"devices"`
	Providers    []ProviderStatus    `json:"providers"`
	Integrations []IntegrationStatus `json:"integrations"`
	// Targets is the frecency-ranked places on the device that asked,
	// best first — the phone's home screen.
	Targets []TargetStatus `json:"targets"`
}

func (l *lanTransport) status(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, l.snapshot(r.URL.Query().Get("device")))
}

// snapshot is the Status of this moment. Naming a device is the
// heartbeat for it.
func (l *lanTransport) snapshot(device string) Status {
	now := time.Now()
	l.attention.Seen(device, now)
	devices, integrations := l.attention.Snapshot(now, 20)
	if devices == nil {
		devices = []DeviceStatus{}
	}
	// The places are this Mac's, whoever is asking — a phone has none of
	// its own. The device param is the caller's heartbeat, not the
	// subject of the ranking.
	targets := l.attention.Rank(l.id.Name, now, 12)
	if targets == nil {
		targets = []TargetStatus{}
	}
	return Status{
		Version:      version,
		Name:         l.id.Name,
		Peer:         l.id.Peer,
		Mind:         l.how,
		Since:        l.since,
		RunsInFlight: l.runs.inFlight(),
		Devices:      devices,
		Providers:    detectProviders(),
		Integrations: integrations,
		Targets:      targets,
	}
}

// watchStatus pushes Status: once now, then after every change, and
// every fifteen seconds so both ends know the other is alive. A device
// holding this open is present; the app stops asking and starts being
// told, which is the difference between "a few seconds" and "now".
func (l *lanTransport) watchStatus(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	device := r.URL.Query().Get("device")
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)

	changes, stop := l.attention.Watch()
	defer stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	send := func() bool {
		if err := enc.Encode(l.snapshot(device)); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send() {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
		case <-changes:
			// Coalesce: a focus change is often two observations
			// (unfocused, focused) a millisecond apart.
			time.Sleep(30 * time.Millisecond)
			for len(changes) > 0 {
				<-changes
			}
		}
		if !send() {
			return
		}
	}
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
