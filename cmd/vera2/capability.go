package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// What the phone can do with whatever is in front of the person.
//
// The phone is a generic renderer. It does not know what Xcode is, or
// Claude Code; it asks the Mac "what can I do here?" and draws whatever
// comes back — a row of buttons, and whether this thing takes text or
// has a screen worth watching. Teaching Vera a new app is teaching the
// MAC a new descriptor; the phone never changes. This is the plugin
// seam: rook is one provider (panes, coding agents), a table of keyboard
// shortcuts is another (any Mac app), and a bespoke provider can come
// later without the phone learning anything new.

// Action is one button. The phone shows title + icon and, on a tap,
// calls /do with the id.
type Action struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Icon        string `json:"icon,omitempty"` // an SF Symbol name
	Destructive bool   `json:"destructive,omitempty"`
}

// Capability is the whole surface for one target: the buttons it offers,
// whether it takes dictation/typing, and whether it has a live screen
// the phone should poll (/screen). Everything the panel draws comes from
// here.
type Capability struct {
	Label       string   `json:"label,omitempty"`
	Actions     []Action `json:"actions,omitempty"`
	Screen      bool     `json:"screen,omitempty"`
	AcceptsText bool     `json:"accepts_text,omitempty"`
}

// CapTarget is the thing the phone is pointed at — an app by bundle id,
// or a rook pane. It mirrors the home list's RankedTarget.
type CapTarget struct {
	Kind     string         `json:"kind"` // "app" | "pane"
	BundleID string         `json:"bundle_id,omitempty"`
	Name     string         `json:"name,omitempty"`
	Terminal *TerminalFocus `json:"terminal,omitempty"`
}

// DoCmd is a tap: run this action against this target.
type DoCmd struct {
	Action string    `json:"action"`
	Target CapTarget `json:"target"`
}

// --- Tier 0: declarative keyboard-shortcut profiles ---------------------
//
// The cheapest provider there is. A Mac app becomes controllable by
// listing its shortcuts here — no code. The executor is generic: the
// Mac's own app (Vera.app) brings the target forward and posts the key.

type keyAction struct {
	ID    string
	Title string
	Icon  string
	Key   string   // the character, e.g. "b"
	Mods  []string // "command" | "shift" | "option" | "control"
}

var appProfiles = map[string][]keyAction{
	"com.apple.dt.Xcode": {
		{"build", "Build", "hammer", "b", []string{"command"}},
		{"run", "Run", "play.fill", "r", []string{"command"}},
		{"stop", "Stop", "stop.fill", ".", []string{"command"}},
		{"test", "Test", "checkmark.diamond", "u", []string{"command"}},
		{"clean", "Clean", "trash", "k", []string{"command", "shift"}},
	},
	"com.google.Chrome": {
		{"reload", "Reload", "arrow.clockwise", "r", []string{"command"}},
		{"newtab", "New Tab", "plus", "t", []string{"command"}},
		{"back", "Back", "chevron.left", "[", []string{"command"}},
		{"forward", "Forward", "chevron.right", "]", []string{"command"}},
	},
}

// --- The command downlink ----------------------------------------------
//
// Actions on a GUI app run on the Mac, in Vera.app — the one process
// with the accessibility trust to drive other apps. Vera.app holds a
// /commands stream open the way it holds /watch open; the core publishes
// a Command onto it when the phone taps a button.

// Command is one thing for the Mac to do. Today: focus an app and press
// a shortcut.
type Command struct {
	Type     string   `json:"type"` // "keystroke"
	BundleID string   `json:"bundle_id,omitempty"`
	Name     string   `json:"name,omitempty"`
	Key      string   `json:"key,omitempty"`
	Mods     []string `json:"mods,omitempty"`
}

// cmdHub fans commands out to whoever is holding the /commands stream
// open for a device. There is normally exactly one subscriber (Vera.app
// on this Mac), but the shape allows none (app not running) or more.
type cmdHub struct {
	mu   sync.Mutex
	subs map[string]map[chan Command]struct{}
}

func newCmdHub() *cmdHub { return &cmdHub{subs: map[string]map[chan Command]struct{}{}} }

func (h *cmdHub) subscribe(device string) (<-chan Command, func()) {
	ch := make(chan Command, 8)
	h.mu.Lock()
	if h.subs[device] == nil {
		h.subs[device] = map[chan Command]struct{}{}
	}
	h.subs[device][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[device], ch)
		h.mu.Unlock()
	}
}

// publish delivers c to every subscriber for device and reports whether
// anyone was listening — a tap with no executor is a tap that did
// nothing, and the phone should hear about it.
func (h *cmdHub) publish(device string, c Command) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	subs := h.subs[device]
	if len(subs) == 0 {
		return false
	}
	sent := false
	for ch := range subs {
		select {
		case ch <- c:
			sent = true
		default:
			// A wedged subscriber does not block the tap.
		}
	}
	return sent
}

// --- Handlers -----------------------------------------------------------

// capabilities answers "what can I do here?" for one target. The phone
// asks on entering a panel, and draws itself from the reply.
func (l *lanTransport) capabilities(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var t CapTarget
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&t); err != nil {
		http.Error(w, "bad target", http.StatusBadRequest)
		return
	}
	writeJSON(w, l.describe(t))
}

// describe computes the capability surface for a target. This is where
// providers are chosen — by pane-vs-app and, for apps, by bundle id.
func (l *lanTransport) describe(t CapTarget) Capability {
	switch t.Kind {
	case "pane":
		// rook's surface: it takes text, it has a screen, and a
		// recognised coding agent adds its own commands.
		cap := Capability{Screen: true, AcceptsText: true}
		if t.Terminal != nil {
			cap.Label = t.Terminal.Describe()
			if t.Terminal.Agent == "claude-code" {
				cap.Actions = []Action{
					{ID: "compact", Title: "Compact", Icon: "rectangle.compress.vertical"},
				}
			}
		}
		return cap
	case "app":
		cap := Capability{Label: t.Name}
		for _, k := range appProfiles[t.BundleID] {
			cap.Actions = append(cap.Actions, Action{ID: k.ID, Title: k.Title, Icon: k.Icon})
		}
		return cap
	default:
		return Capability{}
	}
}

// do runs one action against one target. Pane actions go through the
// terminal provider (the same path as Compact always did); app actions
// become a keystroke the Mac's own app carries out.
func (l *lanTransport) do(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var d DoCmd
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&d); err != nil {
		http.Error(w, "bad command", http.StatusBadRequest)
		return
	}
	switch d.Target.Kind {
	case "pane":
		if l.agenter == nil {
			http.Error(w, "no provider can run that here", http.StatusNotImplemented)
			return
		}
		_, err := l.agenter(r.Context(), d.Action, d.Target.Terminal)
		switch {
		case errors.Is(err, ErrNoTarget), errors.Is(err, ErrNotAgent):
			http.Error(w, err.Error(), http.StatusConflict)
			return
		case err != nil:
			http.Error(w, "command failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "app":
		var ka *keyAction
		for i := range appProfiles[d.Target.BundleID] {
			if appProfiles[d.Target.BundleID][i].ID == d.Action {
				ka = &appProfiles[d.Target.BundleID][i]
				break
			}
		}
		if ka == nil {
			http.Error(w, "no such action for that app", http.StatusNotFound)
			return
		}
		cmd := Command{Type: "keystroke", BundleID: d.Target.BundleID, Name: d.Target.Name, Key: ka.Key, Mods: ka.Mods}
		if !l.commands.publish(l.id.Name, cmd) {
			http.Error(w, "the Mac's app isn't running to carry that out", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "a command needs a target", http.StatusBadRequest)
	}
}

// watchCommands is the downlink Vera.app holds open: the Mac's own app
// waiting to be told to drive another app. It streams Command frames the
// way /watch streams Status, with a heartbeat so both ends know the
// other is there.
func (l *lanTransport) watchCommands(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	device := r.URL.Query().Get("device")
	if device == "" {
		device = l.id.Name
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)

	cmds, stop := l.commands.subscribe(device)
	defer stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	// Flush the head now so the app knows the stream is live rather than
	// waiting on the first heartbeat.
	if _, err := w.Write([]byte("\n")); err != nil {
		return
	}
	if rc.Flush() != nil {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			// A blank line keeps the socket warm without a command.
			if _, err := w.Write([]byte("\n")); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		case c := <-cmds:
			if err := enc.Encode(c); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
	}
}
