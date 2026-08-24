// Attention: what Vera knows about where the person is looking.
//
// The Mac app, and later an editor or a browser, report OBSERVATIONS —
// "Ghostty has focus", "this file is open", "this PR is on screen". They
// are kept here per device, bounded, and recited to the model as plain
// facts about the present moment.
//
// The discipline this file exists to enforce is the difference between
// what is known and what is guessed. "Slack has focus" is known. "They
// are reading the #incidents channel" is not, unless Slack said so. So
// an observation is stored as it arrived, described only as far as it
// goes, and never elaborated. An app with no integration is opaque, and
// opaque is a valid thing to tell the model.
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Observation is one semantic event from one device. The envelope is
// deliberately small and the rest of the payload rides in Fields, so an
// editor can start reporting `editor.selection` before this file has
// heard of it.
type Observation struct {
	Type   string    `json:"type"`
	Device string    `json:"device,omitempty"`
	At     time.Time `json:"at"`

	// App is set on app.focused / app.unfocused.
	App *ObservedApp `json:"app,omitempty"`

	// Source names the integration that produced the event — "neovim",
	// "chrome", "rook" — for everything that is not the OS itself.
	Source string `json:"source,omitempty"`

	// Terminal is set on terminal.focus: what rook shows in the pane
	// that has focus.
	Terminal *TerminalFocus `json:"terminal,omitempty"`

	// Fields is everything else that was sent, kept verbatim.
	Fields map[string]json.RawMessage `json:"fields,omitempty"`
}

type ObservedApp struct {
	Name     string `json:"name"`
	BundleID string `json:"bundle_id,omitempty"`
}

// UnmarshalJSON keeps the whole payload: the known keys into the struct,
// the rest into Fields, so nothing a future integration sends is lost
// between arriving and being understood.
func (o *Observation) UnmarshalJSON(b []byte) error {
	type plain Observation
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	for _, known := range []string{"type", "device", "at", "app", "source", "terminal", "fields"} {
		delete(all, known)
	}
	if len(all) > 0 {
		p.Fields = all
	}
	*o = Observation(p)
	return nil
}

// device is one machine's slice of the present.
type device struct {
	Name       string
	LastSeen   time.Time
	Focus      *ObservedApp
	FocusSince time.Time
	// Terminal is what is inside the terminal, when rook says.
	Terminal *TerminalFocus
	// termHost is the macOS app the terminal runs in (Ghostty, say),
	// remembered so a jump to a pane can bring that app to the front.
	termHost *ObservedApp
	// Recent is newest-last, bounded.
	Recent []Observation
	// Integrations maps a source ("neovim") to the last time it spoke.
	Integrations map[string]time.Time
	// places is the frecency table: every app and pane the person has
	// been to, keyed by target id, scored by how often and how lately.
	places map[string]*place
}

// place is one thing the person has had in front of them — an app, or a
// rook pane — with the frequency-and-recency the ranking is built from.
// The name is zoxide's: frecency, frequency married to recency, so a
// thing visited often but not lately loses to a thing visited twice
// this minute.
type place struct {
	Key      string
	Kind     string // "app" or "pane"
	Label    string
	BundleID string         // apps
	Terminal *TerminalFocus // panes
	// rank rises by one each visit; the score decays it by age, so an
	// old favourite fades without being forgotten.
	rank float64
	last time.Time
}

// bump records a visit, the way zoxide does: +1 to rank, and the clock
// reset to now.
func (d *device) bump(key, kind, label string, at time.Time, mut func(*place)) {
	p := d.places[key]
	if p == nil {
		p = &place{Key: key, Kind: kind}
		d.places[key] = p
	}
	p.Label = label
	p.rank++
	if at.After(p.last) {
		p.last = at
	}
	if mut != nil {
		mut(p)
	}
}

// frecency is zoxide's decay: lately counts for far more than often.
func frecency(rank float64, age time.Duration) float64 {
	switch {
	case age < time.Hour:
		return rank * 4
	case age < 24*time.Hour:
		return rank * 2
	case age < 7*24*time.Hour:
		return rank * 0.5
	default:
		return rank * 0.25
	}
}

// Attention holds every device's observations.
type Attention struct {
	mu      sync.Mutex
	devices map[string]*device

	keep  int
	fresh time.Duration

	// watchers are told when anything changed; each channel holds one
	// pending signal, so a burst of observations is one wake-up.
	watchers map[chan struct{}]struct{}
}

// Watch returns a channel that receives a signal after every change,
// and a function to stop watching.
func (a *Attention) Watch() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	a.mu.Lock()
	if a.watchers == nil {
		a.watchers = map[chan struct{}]struct{}{}
	}
	a.watchers[ch] = struct{}{}
	a.mu.Unlock()
	return ch, func() {
		a.mu.Lock()
		delete(a.watchers, ch)
		a.mu.Unlock()
	}
}

// changed wakes the watchers. Called with the lock held.
func (a *Attention) changed() {
	for ch := range a.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func newAttention() *Attention {
	return &Attention{devices: map[string]*device{}, keep: 50, fresh: 10 * time.Minute}
}

// Observe records one event. An event with no timestamp is stamped now;
// an event with no device is attributed to "unknown" rather than dropped,
// so a misconfigured sender shows up as a problem you can see.
func (a *Attention) Observe(o Observation) {
	if o.At.IsZero() {
		o.At = time.Now()
	}
	if o.Device == "" {
		o.Device = "unknown"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.device(o.Device)
	if o.At.After(d.LastSeen) {
		d.LastSeen = o.At
	}
	switch o.Type {
	case "app.focused":
		if o.App != nil {
			same := d.Focus != nil && d.Focus.BundleID == o.App.BundleID && d.Focus.Name == o.App.Name
			if !same {
				d.Focus = o.App
				d.FocusSince = o.At
			}
			if isTerminal(o.App) {
				app := *o.App
				d.termHost = &app
			}
			bundle := o.App.BundleID
			if bundle == "" {
				bundle = o.App.Name
			}
			app := *o.App
			d.bump("app:"+bundle, "app", o.App.Name, o.At, func(p *place) {
				p.BundleID = app.BundleID
			})
		}
	case "app.unfocused":
		if o.App != nil && d.Focus != nil && d.Focus.BundleID == o.App.BundleID {
			d.Focus = nil
			d.FocusSince = time.Time{}
		}
	case "terminal.focus":
		d.Terminal = o.Terminal
		if o.Terminal != nil {
			t := *o.Terminal
			key := "pane:" + t.Session + ":" + t.Window + "." + t.Pane
			d.bump(key, "pane", t.Describe(), o.At, func(p *place) { p.Terminal = &t })
		}
	case "terminal.unfocused":
		d.Terminal = nil
	}
	if o.Source != "" {
		d.Integrations[o.Source] = o.At
	}
	d.Recent = append(d.Recent, o)
	if len(d.Recent) > a.keep {
		d.Recent = d.Recent[len(d.Recent)-a.keep:]
	}
	a.changed()
}

// Seen is a heartbeat: the device is still there, whether or not anything
// happened on it.
func (a *Attention) Seen(name string, at time.Time) {
	if name == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.device(name)
	if at.After(d.LastSeen) {
		d.LastSeen = at
	}
}

func (a *Attention) device(name string) *device {
	d := a.devices[name]
	if d == nil {
		d = &device{Name: name, Integrations: map[string]time.Time{}, places: map[string]*place{}}
		a.devices[name] = d
	}
	return d
}

// Describe is the paragraph the model reads. Empty when nothing is fresh,
// so a Mac that went quiet an hour ago is not described as if it were
// still in front of the person.
//
// It says what has focus and what had it just before — and nothing about
// what is inside those windows, because nothing here knows.
func (a *Attention) Describe(now time.Time, speaking string) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	names := make([]string, 0, len(a.devices))
	for name, d := range a.devices {
		if now.Sub(d.LastSeen) <= a.fresh {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}

	var b strings.Builder
	for _, name := range names {
		d := a.devices[name]
		b.WriteString("On ")
		b.WriteString(name)
		if name == speaking {
			b.WriteString(" (the device they are speaking from)")
		}
		if d.Focus == nil {
			// No Mac app reporting: the terminal is the only sense.
			// Rook knows the pane in front of them; say so, and say
			// that is all that is known.
			if d.Terminal != nil {
				b.WriteString(": in the terminal, rook shows " + d.Terminal.Describe() + ". No other application is reporting.")
			} else {
				b.WriteString(": no application has focus.")
			}
		} else {
			fmt.Fprintf(&b, ": %s has had focus for %s.", d.Focus.Name, roughly(now.Sub(d.FocusSince)))
			// Inside the terminal, only when the terminal is what they
			// are looking at — rook's pane is still there when Slack
			// has focus, but it is not where their attention is.
			if d.Terminal != nil && isTerminal(d.Focus) {
				b.WriteString(" Inside it, rook shows " + d.Terminal.Describe() + ".")
			}
		}
		if before := a.before(d); len(before) > 0 {
			b.WriteString(" Before that: " + strings.Join(before, ", ") + ".")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// TerminalHost is the macOS app rook runs inside on a device, if known —
// what a pane jump must bring to the front.
func (a *Attention) TerminalHost(device string) *ObservedApp {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.devices[device]; d != nil && d.termHost != nil {
		h := *d.termHost
		return &h
	}
	return nil
}

// TerminalPath is the working directory of the pane in front of the
// person on a device, or "" — what "start a task on this" means.
func (a *Attention) TerminalPath(device string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.devices[device]; d != nil && d.Terminal != nil {
		return d.Terminal.Path
	}
	return ""
}

// isTerminal says whether an app is the kind rook would be running in.
func isTerminal(app *ObservedApp) bool {
	id := strings.ToLower(app.BundleID + " " + app.Name)
	for _, hint := range []string{"ghostty", "terminal", "iterm", "rook", "kitty", "alacritty", "wezterm"} {
		if strings.Contains(id, hint) {
			return true
		}
	}
	return false
}

// before lists the apps that had focus before the current one, most
// recent first, without repeats, and only from the fresh window.
func (a *Attention) before(d *device) []string {
	var out []string
	seen := map[string]bool{}
	if d.Focus != nil {
		seen[d.Focus.BundleID+"/"+d.Focus.Name] = true
	}
	for i := len(d.Recent) - 1; i >= 0 && len(out) < 3; i-- {
		o := d.Recent[i]
		if o.Type != "app.focused" || o.App == nil {
			continue
		}
		key := o.App.BundleID + "/" + o.App.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, o.App.Name)
	}
	return out
}

// TargetStatus is one ranked place a person can jump to.
type TargetStatus struct {
	Key      string         `json:"key"`
	Kind     string         `json:"kind"` // "app" or "pane"
	Label    string         `json:"label"`
	Score    float64        `json:"score"`
	BundleID string         `json:"bundle_id,omitempty"`
	Terminal *TerminalFocus `json:"terminal,omitempty"`
	// Current is the place in front of the person right now.
	Current bool `json:"current,omitempty"`
}

// Rank is the frecency-ordered list of places on one device, best
// first. The place in front of the person is marked but not moved — a
// list that reshuffles under your thumb as focus changes is a list you
// cannot tap.
func (a *Attention) Rank(device string, now time.Time, limit int) []TargetStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.devices[device]
	if d == nil {
		return nil
	}
	currentApp := ""
	if d.Focus != nil {
		if d.Focus.BundleID != "" {
			currentApp = "app:" + d.Focus.BundleID
		} else {
			currentApp = "app:" + d.Focus.Name
		}
	}
	// The pane is only "current" when a terminal is actually in front of
	// the person — the pane persists behind Slack, but it is not where
	// their attention is.
	currentPane := ""
	if d.Terminal != nil && d.Focus != nil && isTerminal(d.Focus) {
		currentPane = "pane:" + d.Terminal.Session + ":" + d.Terminal.Window + "." + d.Terminal.Pane
	}
	out := make([]TargetStatus, 0, len(d.places))
	for _, p := range d.places {
		out = append(out, TargetStatus{
			Key:      p.Key,
			Kind:     p.Kind,
			Label:    p.Label,
			Score:    frecency(p.rank, now.Sub(p.last)),
			BundleID: p.BundleID,
			Terminal: p.Terminal,
			Current:  p.Key == currentApp || p.Key == currentPane,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Label < out[j].Label
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// DeviceStatus is a device as /status reports it.
type DeviceStatus struct {
	Name       string         `json:"name"`
	LastSeen   time.Time      `json:"last_seen"`
	Fresh      bool           `json:"fresh"`
	Focus      *ObservedApp   `json:"focus,omitempty"`
	FocusSince *time.Time     `json:"focus_since,omitempty"`
	Terminal   *TerminalFocus `json:"terminal,omitempty"`
	Recent     []Observation  `json:"recent"`
}

// IntegrationStatus is one known integration and whether it has spoken
// lately. The list is fixed so the Connections view can show what does
// not exist yet; "connected" is earned by reporting in, not declared.
type IntegrationStatus struct {
	Name      string     `json:"name"`
	Connected bool       `json:"connected"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
}

var knownIntegrations = []string{"rook", "neovim", "vscode", "chrome"}

// Snapshot is the read side, for /status.
func (a *Attention) Snapshot(now time.Time, recent int) ([]DeviceStatus, []IntegrationStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()

	var devices []DeviceStatus
	lastBySource := map[string]time.Time{}
	for _, d := range a.devices {
		s := DeviceStatus{
			Name:     d.Name,
			LastSeen: d.LastSeen,
			Fresh:    now.Sub(d.LastSeen) <= a.fresh,
			Focus:    d.Focus,
			Terminal: d.Terminal,
			Recent:   []Observation{},
		}
		if d.Focus != nil {
			since := d.FocusSince
			s.FocusSince = &since
		}
		if n := len(d.Recent); n > 0 {
			from := n - recent
			if from < 0 {
				from = 0
			}
			s.Recent = append(s.Recent, d.Recent[from:]...)
		}
		devices = append(devices, s)
		for src, at := range d.Integrations {
			if at.After(lastBySource[src]) {
				lastBySource[src] = at
			}
		}
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })

	var integrations []IntegrationStatus
	for _, name := range knownIntegrations {
		s := IntegrationStatus{Name: name}
		if at, ok := lastBySource[name]; ok {
			s.LastSeen = &at
			s.Connected = now.Sub(at) <= a.fresh
		}
		integrations = append(integrations, s)
	}
	return devices, integrations
}

// roughly renders a duration the way a person would say it.
func roughly(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return quantity(int(d.Minutes()), "minute", "minutes")
	default:
		return quantity(int(d.Hours()), "hour", "hours")
	}
}
