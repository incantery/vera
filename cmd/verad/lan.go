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
	"github.com/incantery/vera/attach"
	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
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
	typer func(ctx context.Context, text string, enter, anywhere bool, at *TerminalFocus) (*TerminalFocus, error)
	goer  func(ctx context.Context, session, window, pane string) error
	// screener reads the visible rows of a pane — Vera's one eye into
	// the terminal for a phone that cannot see rook itself.
	screener func(ctx context.Context, at *TerminalFocus) ([]string, *TerminalFocus, error)
	// narrower reshapes a pane's window to the phone's width while it is
	// dictating, and restores it after — nil if no provider offers it.
	narrower func(ctx context.Context, at *TerminalFocus, cols int, want bool) error
	// agenter runs a coding agent's own command (compact, ...) in a pane.
	agenter func(ctx context.Context, action string, at *TerminalFocus) (*TerminalFocus, error)
	// commands is the downlink to the Mac's own app (Vera.app), which
	// carries out actions on other apps — a tap on the phone becomes a
	// keystroke on the desk.
	commands *cmdHub
	// stt transcribes audio the phone sends, and manages its own engine.
	stt Transcriber
	// fleet is the supervisor, when one is running.
	fleet *fleet.Fleet
	// machine hears THIS machine's own lifecycle — it slept, it woke,
	// the network came or went — so the fleet can stop reading eight
	// quiet hours as agents that stalled. Only this device's events
	// reach it: a phone in a tunnel is not a reason to pause work
	// running on the desk. Nil ignores them.
	machine func(cause string, away bool, at time.Time)
	// events is the durable record of what has been going on, across
	// both this machine's repositories. Nil mounts no routes and
	// records nothing.
	events *eventStream
	// todo is the person's own list, when there is a home to keep it
	// in. Nil mounts no routes: a daemon with nowhere to write is not
	// a daemon that pretends to remember.
	todo *home.List
	// hasMind says whether there is a model to talk to. Nothing about
	// the list depends on it — it decides only whether an answer
	// mentions that saying it in prose would have worked too.
	hasMind bool
	// answer carries a person's word on an ask back to the exchange
	// parked on it. Nil when Vera has no tools of her own to gate.
	answer func(ctx context.Context, id, choice string) error
	// servers is the profile's MCP servers and what they offer, for a
	// person asking what she can reach. Nil when she has no tools at
	// all — which is not the same as having no servers.
	servers func() Servers
	// picker is where a conversation's model is read and set. verad is
	// the single writer of it; a terminal asks rather than keeping an
	// idea of its own. Nil in echo mode, where there is no model.
	picker Picker

	mu   sync.Mutex
	port string
}

func newLAN(addr string, id Identity) *lanTransport {
	return &lanTransport{addr: addr, id: id, runs: newRuns(), attention: newAttention(), commands: newCmdHub(), since: time.Now()}
}

func (l *lanTransport) Name() string { return "lan" }

// bound waits until the listener is up, or ctx ends, or the wait runs
// out — false means it did not bind (yet).
func (l *lanTransport) bound(ctx context.Context, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		l.mu.Lock()
		port := l.port
		l.mu.Unlock()
		if port != "" {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

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
	mux.HandleFunc("POST /ask/{id}", l.answerAsk)
	mux.HandleFunc("POST /observe", l.observe)
	mux.HandleFunc("GET /status", l.status)
	mux.HandleFunc("GET /watch", l.watchStatus)
	mux.HandleFunc("POST /dictate", l.dictate)
	mux.HandleFunc("POST /type", l.typeInto)
	mux.HandleFunc("POST /goto", l.goTo)
	mux.HandleFunc("POST /screen", l.screen)
	mux.HandleFunc("POST /agent", l.agentCmd)
	mux.HandleFunc("POST /capabilities", l.capabilities)
	mux.HandleFunc("POST /do", l.do)
	mux.HandleFunc("GET /commands", l.watchCommands)
	mux.HandleFunc("POST /transcribe", l.transcribe)
	mux.HandleFunc("GET /stt", l.sttStatus)
	mux.HandleFunc("GET /mcp", l.mcpServers)
	mux.HandleFunc("GET /models", l.listModels)
	mux.HandleFunc("PUT /model", l.setDefaultModel)
	mux.HandleFunc("GET /conversations/{id}/model", l.conversationModel)
	mux.HandleFunc("POST /conversations/{id}/model", l.setConversationModel)
	mux.HandleFunc("POST /stt/install", l.sttInstall)
	mux.HandleFunc("POST /poke/{who}", loopbackOnly(l.pokeHandler))
	if l.fleet != nil {
		l.fleetRoutes(mux)
	}
	if l.todo != nil {
		l.todoRoutes(mux)
	}
	if l.events != nil {
		l.eventRoutes(mux)
	}
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

// maxSayBody is the ceiling on one message. It was a megabyte when a
// message was words; a pasted screenshot of a 5K display is several
// times that once base64 has grown it by a third, and attach caps a
// single picture at 16 MB and a message at eight of them. This is the
// envelope those two ceilings need, and it is still a bound: a paired
// device cannot hand this daemon an arbitrary amount of memory.
const maxSayBody = (attach.MaxImages*attach.MaxBytes)*4/3 + 1<<20

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
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSayBody)).Decode(&msg); err != nil {
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

// answerAsk carries one word — yes, no or always — back to a tool call
// that is waiting for it.
//
// It is a separate request rather than a frame back up the /say
// stream because /say is one-way by construction: the exchange is
// already streaming out, and the phone that reads it is not the only
// thing that may answer. A client that cannot ask a person should
// answer "no": silence is the one reply that leaves the exchange
// hanging.
func (l *lanTransport) answerAsk(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if l.answer == nil {
		http.Error(w, "nothing here asks", http.StatusNotFound)
		return
	}
	var body struct {
		Choice string `json:"choice"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		http.Error(w, "bad answer", http.StatusBadRequest)
		return
	}
	if err := l.answer(r.Context(), r.PathValue("id"), strings.TrimSpace(body.Choice)); err != nil {
		// A question that is no longer open and a word that is not one
		// of the three are both the caller's mistake, and both are
		// worth saying out loud rather than swallowing.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"answered": r.PathValue("id")})
}

// Picker is verad's answer to "which model is this conversation on",
// and the one way to change it. The Mind implements it.
type Picker interface {
	Pick(conversation string) (Resolution, error)
	Choose(conversation, model, effort string) (Resolution, error)
	Models(conversation string) ModelsAnswer
	SetDefault(model, effort string) (Resolution, error)
}

// listModels is every model this daemon can reach, and what is in
// force. It is a table rather than a call to the vendor — see
// models.go — and it is the whole of what the `/model` picker draws,
// so a terminal never has to know which models exist.
//
// The conversation is optional: with one, the answer says what that
// conversation chose for itself, which is the row the picker ticks.
func (l *lanTransport) listModels(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if l.picker == nil {
		http.Error(w, "no model: this verad is echoing", http.StatusNotFound)
		return
	}
	writeJSON(w, l.picker.Models(r.URL.Query().Get("conversation")))
}

// setDefaultModel moves the daemon's own model and keeps it — Enter in
// either picker. It outranks the profile and not --model, so the answer
// is what is actually in force rather than what was asked for: a
// daemon started with --model says so by answering with the flag's.
//
// The two fields are two toggles: a field left empty is one nobody said
// anything about, so the effort card sends a model of "" and moves
// nothing else. Both empty forgets the saved choice.
func (l *lanTransport) setDefaultModel(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if l.picker == nil {
		http.Error(w, "no model: this verad is echoing", http.StatusNotFound)
		return
	}
	var body struct {
		Model  string `json:"model"`
		Effort string `json:"effort"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	res, err := l.picker.SetDefault(body.Model, body.Effort)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, res)
}

// conversationModel says what is in force for a conversation, and
// where each half of it came from. A terminal draws its status line
// from this rather than remembering what it last set: verad is the
// authority, and a `vera say -m` from another window changes nothing
// here but a phone that assumed otherwise would be lying on screen.
func (l *lanTransport) conversationModel(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if l.picker == nil {
		http.Error(w, "no model: this verad is echoing", http.StatusNotFound)
		return
	}
	res, err := l.picker.Pick(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, res)
}

// setConversationModel is `/model opus` and `/effort high` — the same
// route, because they are the same choice seen from two ends. Each
// field named replaces its own and leaves the other alone; both empty
// puts the conversation back on the daemon's own.
func (l *lanTransport) setConversationModel(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if l.picker == nil {
		http.Error(w, "no model: this verad is echoing", http.StatusNotFound)
		return
	}
	var body struct {
		Model  string `json:"model"`
		Effort string `json:"effort"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	res, err := l.picker.Choose(r.PathValue("id"), body.Model, body.Effort)
	if err != nil {
		// A model nobody can reach is the caller's mistake and worth
		// saying out loud: the alternative is a conversation quietly
		// answering on a different model than the one asked for.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, res)
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
	l.noteLifecycle(o)
	w.WriteHeader(http.StatusNoContent)
}

// noteLifecycle passes this machine's own sleep and network events on
// to whoever supervises the work running on it.
//
// The sleep event itself rarely arrives at the moment it happened —
// the machine is on its way out and there is no time to be heard — so
// the Mac app sends it again on the way back, stamped with when the lid
// actually shut. Both orders end with the same span on the record.
func (l *lanTransport) noteLifecycle(o Observation) {
	if l.machine == nil || o.Device != l.id.Name {
		return
	}
	at := o.At
	if at.IsZero() {
		at = time.Now()
	}
	switch o.Type {
	case "device.slept":
		l.machine(fleet.CauseSleep, true, at)
	case "device.woke":
		l.machine(fleet.CauseSleep, false, at)
	case "device.offline":
		l.machine(fleet.CauseOffline, true, at)
	case "device.online":
		l.machine(fleet.CauseOffline, false, at)
	}
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
		// actually on screen. The host is on THIS Mac's record, not the
		// phone's — the phone has no terminal of its own.
		if host := l.attention.TerminalHost(l.id.Name); host != nil {
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

// Screen is a request for the visible contents of a pane. A nil
// Terminal means "whatever the Mac is looking at".
type Screen struct {
	Terminal *TerminalFocus `json:"terminal,omitempty"`
	Device   string         `json:"device,omitempty"`
	// Mobile asks the Mac to hold this pane's window at the phone's
	// width while the phone keeps polling; Cols is that width. The
	// window reflows on the desk too — one window, one size.
	Mobile bool `json:"mobile,omitempty"`
	Cols   int  `json:"cols,omitempty"`
}

// Screened is the pane as it looks right now.
type Screened struct {
	Lines []string       `json:"lines"`
	Into  *TerminalFocus `json:"into,omitempty"`
}

// screen is Vera's one eye into the terminal from the phone: the Mac
// reads the visible rows of a pane and ships them back. Read-only — it
// types nothing and moves nothing, so any pane is fair game, agent or
// shell. The phone polls this while a pane view is open, to see a reply
// forming or the comment it is about to answer.
func (l *lanTransport) screen(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if l.screener == nil {
		http.Error(w, "no provider can read a pane", http.StatusNotImplemented)
		return
	}
	var s Screen
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&s); err != nil {
		http.Error(w, "bad screen", http.StatusBadRequest)
		return
	}
	// Shape the window first, so the rows we read back are already laid
	// out for the phone. Best-effort: a pane that will not resize still
	// reads fine, just wider.
	if l.narrower != nil {
		_ = l.narrower(r.Context(), s.Terminal, s.Cols, s.Mobile)
	}
	lines, into, err := l.screener(r.Context(), s.Terminal)
	switch {
	case errors.Is(err, ErrNoTarget):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "could not read the pane: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, Screened{Lines: lines, Into: into})
}

// AgentCmd asks a coding agent to run one of its own commands — compact,
// for now — in a pane it is running.
type AgentCmd struct {
	Action   string         `json:"action"`
	Terminal *TerminalFocus `json:"terminal,omitempty"`
	Device   string         `json:"device,omitempty"`
}

// agentCmd is the phone's buttons for a Claude Code session: it sends
// the agent one of its own commands as keystrokes. Refused on anything
// that is not a recognised agent — these are not shell commands.
func (l *lanTransport) agentCmd(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if l.agenter == nil {
		http.Error(w, "no provider can run an agent command", http.StatusNotImplemented)
		return
	}
	var a AgentCmd
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&a); err != nil {
		http.Error(w, "bad agent command", http.StatusBadRequest)
		return
	}
	into, err := l.agenter(r.Context(), a.Action, a.Terminal)
	switch {
	case errors.Is(err, ErrNoTarget):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, ErrNotAgent):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "agent command failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, Typed{Into: into})
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
	Anywhere bool `json:"anywhere,omitempty"`
	// Terminal, when set, is the exact pane to type into, regardless of
	// what has focus — the phone choosing a specific window.
	Terminal *TerminalFocus `json:"terminal,omitempty"`
	Device   string         `json:"device,omitempty"`
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
	into, err := l.typer(r.Context(), out.Text, t.Enter, t.Anywhere, t.Terminal)
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

// mcpServers is what `vera mcp` prints: the servers this profile
// declared, whether they answered, and every tool under the name the
// model sees and a policy rule has to be written against.
//
// Authed like everything else, and a GET because it changes nothing.
// It is asked of verad rather than read off disk because the file
// says what was declared and only a connected server says what it
// actually offers.
func (l *lanTransport) mcpServers(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if l.servers == nil {
		writeJSON(w, Servers{List: []ServerInfo{}})
		return
	}
	writeJSON(w, l.servers())
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
