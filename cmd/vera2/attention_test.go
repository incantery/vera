package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func focused(device, name, bundle string, at time.Time) Observation {
	return Observation{Type: "app.focused", Device: device, At: at, App: &ObservedApp{Name: name, BundleID: bundle}}
}

func TestDescribeSaysWhatHasFocusAndWhatCameBefore(t *testing.T) {
	a := newAttention()
	t0 := time.Date(2026, 8, 20, 11, 52, 0, 0, time.UTC)
	a.Observe(focused("work-mac", "Chrome", "com.google.Chrome", t0))
	a.Observe(focused("work-mac", "Ghostty", "com.mitchellh.ghostty", t0.Add(time.Minute)))
	a.Observe(focused("work-mac", "Chrome", "com.google.Chrome", t0.Add(2*time.Minute)))
	a.Observe(focused("work-mac", "Ghostty", "com.mitchellh.ghostty", t0.Add(3*time.Minute)))

	got := a.Describe(t0.Add(5*time.Minute), "work-mac")
	for _, want := range []string{
		"On work-mac (the device they are speaking from)",
		"Ghostty has had focus for 2 minutes",
		"Before that: Chrome",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("description %q lacks %q", got, want)
		}
	}
	if strings.Count(got, "Chrome") != 1 || strings.Count(got, "Ghostty") != 1 {
		t.Fatalf("the history should not repeat apps: %q", got)
	}
}

func TestDescribeIsSilentAboutStaleDevices(t *testing.T) {
	a := newAttention()
	t0 := time.Now().Add(-time.Hour)
	a.Observe(focused("work-mac", "Ghostty", "com.mitchellh.ghostty", t0))
	if got := a.Describe(time.Now(), ""); got != "" {
		t.Fatalf("an hour-old focus was described as the present: %q", got)
	}
	a.Seen("work-mac", time.Now())
	if got := a.Describe(time.Now(), ""); !strings.Contains(got, "Ghostty") {
		t.Fatalf("a heartbeat should bring the device back: %q", got)
	}
}

func TestRefocusingTheSameAppKeepsTheSince(t *testing.T) {
	a := newAttention()
	t0 := time.Now().Add(-10 * time.Minute)
	a.Observe(focused("m", "Ghostty", "com.mitchellh.ghostty", t0))
	a.Observe(focused("m", "Ghostty", "com.mitchellh.ghostty", t0.Add(9*time.Minute)))
	if got := a.Describe(time.Now(), ""); !strings.Contains(got, "10 minutes") {
		t.Fatalf("want the original since, got %q", got)
	}
}

func TestObservationKeepsUnknownFields(t *testing.T) {
	var o Observation
	raw := `{"type":"editor.selection","device":"m","source":"neovim","file":"internal/server.go","selection":{"start_line":182,"end_line":190}}`
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	if o.Source != "neovim" || string(o.Fields["file"]) != `"internal/server.go"` || o.Fields["selection"] == nil {
		t.Fatalf("fields lost: %+v", o)
	}
	a := newAttention()
	a.Observe(o)
	_, integrations := a.Snapshot(time.Now(), 10)
	for _, i := range integrations {
		if i.Name == "neovim" && !i.Connected {
			t.Fatal("an integration that just spoke should read as connected")
		}
		if i.Name == "chrome" && i.Connected {
			t.Fatal("an integration that never spoke should not")
		}
	}
}

func TestObserveAndStatusOverTheWire(t *testing.T) {
	base, id := serveLAN(t, echo)
	post := func(body string, auth bool) int {
		req, _ := http.NewRequest("POST", base+"/observe", bytes.NewBufferString(body))
		if auth {
			req.Header.Set("Authorization", "Bearer "+id.Secret)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := post(`{"type":"app.focused"}`, false); got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated observe got %d", got)
	}
	if got := post(`{"nope":1}`, true); got != http.StatusBadRequest {
		t.Fatalf("an observation without a type got %d", got)
	}
	if got := post(`{"type":"app.focused","device":"work-mac","app":{"name":"Ghostty","bundle_id":"com.mitchellh.ghostty"}}`, true); got != http.StatusNoContent {
		t.Fatalf("observe got %d", got)
	}

	req, _ := http.NewRequest("GET", base+"/status?device=work-mac", nil)
	req.Header.Set("Authorization", "Bearer "+id.Secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var s Status
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.Name != "test-mac" || s.Version != version || len(s.Devices) != 1 {
		t.Fatalf("status %+v", s)
	}
	d := s.Devices[0]
	if d.Name != "work-mac" || !d.Fresh || d.Focus == nil || d.Focus.Name != "Ghostty" || len(d.Recent) != 1 {
		t.Fatalf("device %+v", d)
	}
	if len(s.Providers) == 0 || s.Providers[0].Name != "rook" || len(s.Integrations) != len(knownIntegrations) {
		t.Fatalf("providers %+v integrations %+v", s.Providers, s.Integrations)
	}
}

func TestDictateWithoutAMindReturnsTheRawWords(t *testing.T) {
	base, id := serveLAN(t, echo)
	req, _ := http.NewRequest("POST", base+"/dictate", strings.NewReader(`{"text":"  um hello there  ","app":{"name":"Ghostty"}}`))
	req.Header.Set("Authorization", "Bearer "+id.Secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var c Cleaned
	if err := json.NewDecoder(res.Body).Decode(&c); err != nil {
		t.Fatal(err)
	}
	if c.Text != "um hello there" || !c.Raw {
		t.Fatalf("got %+v, want the trimmed raw words marked raw", c)
	}
}

func TestTerminalFocusIsDescribedOnlyInsideATerminal(t *testing.T) {
	a := newAttention()
	now := time.Now()
	pane := &TerminalFocus{Session: "vera", Window: "1", Pane: "1", Command: "2.1.237", Title: "✳ Vera native macOS surface", Path: "/Users/x/vera", Agent: "claude-code"}
	a.Observe(Observation{Type: "terminal.focus", Device: "m", Source: "rook", At: now, Terminal: pane})

	a.Observe(focused("m", "Slack", "com.tinyspeck.slackmacgap", now))
	if got := a.Describe(now, "m"); strings.Contains(got, "Claude Code") {
		t.Fatalf("the pane is not where attention is when Slack has focus: %q", got)
	}
	a.Observe(focused("m", "Ghostty", "com.mitchellh.ghostty", now))
	got := a.Describe(now, "m")
	if !strings.Contains(got, `Inside it, rook shows Claude Code session "Vera native macOS surface" (vera:1)`) {
		t.Fatalf("want the pane described, got %q", got)
	}
	devices, integrations := a.Snapshot(now, 5)
	if devices[0].Terminal == nil || devices[0].Terminal.Agent != "claude-code" {
		t.Fatalf("status should carry the terminal: %+v", devices[0])
	}
	for _, i := range integrations {
		if i.Name == "rook" && !i.Connected {
			t.Fatal("rook spoke; it should read as connected")
		}
	}
}

func TestPickClientPrefersTheFocusedOne(t *testing.T) {
	out := "attached\tsethlowie\t100\nattached,focused\tvera\t50\nattached\ttmux\t200\n"
	if got := pickClient(out); got != "vera" {
		t.Fatalf("got %q", got)
	}
	out = "attached\tsethlowie\t100\nattached\ttmux\t200\n"
	if got := pickClient(out); got != "tmux" {
		t.Fatalf("without a focused flag the most recently active wins, got %q", got)
	}
}

func TestPokeIsLoopbackOnlyAndRingsTheRightBell(t *testing.T) {
	base, _ := serveLAN(t, echo)
	// serveLAN's transport is reachable only on 127.0.0.1, so the
	// loopback guard passes; what is tested is the routing.
	lanT := lanUnderTest
	rang := make(chan struct{}, 1)
	lanT.onPoke("rook", func() { rang <- struct{}{} })

	res, err := http.Post(base+"/poke/rook", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("poke got %d", res.StatusCode)
	}
	select {
	case <-rang:
	case <-time.After(time.Second):
		t.Fatal("the bell did not ring")
	}
	res, _ = http.Post(base+"/poke/nobody", "", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown bell got %d", res.StatusCode)
	}
}

func TestWatchPushesOnChange(t *testing.T) {
	base, id := serveLAN(t, echo)
	req, _ := http.NewRequest("GET", base+"/watch?device=work-mac", nil)
	req.Header.Set("Authorization", "Bearer "+id.Secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	dec := json.NewDecoder(res.Body)

	var first Status
	if err := dec.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if len(first.Devices) != 1 || first.Devices[0].Focus != nil {
		t.Fatalf("first snapshot should show the watcher's own device and no focus: %+v", first.Devices)
	}

	lanUnderTest.attention.Observe(focused("work-mac", "Ghostty", "com.mitchellh.ghostty", time.Now()))

	got := make(chan Status, 1)
	go func() {
		var s Status
		if dec.Decode(&s) == nil {
			got <- s
		}
	}()
	select {
	case s := <-got:
		if s.Devices[0].Focus == nil || s.Devices[0].Focus.Name != "Ghostty" {
			t.Fatalf("pushed snapshot lacks the change: %+v", s.Devices[0])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no push within two seconds of a change")
	}
}

func TestTypeRefusesWithoutATargetAndNamesThePane(t *testing.T) {
	base, id := serveLAN(t, echo)
	post := func(body string) (int, Typed) {
		req, _ := http.NewRequest("POST", base+"/type", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+id.Secret)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out Typed
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := post(`{"text":"hi"}`); code != http.StatusNotImplemented {
		t.Fatalf("without a provider got %d", code)
	}

	var typed []string
	lanUnderTest.typer = func(ctx context.Context, text string, enter, anywhere bool) (*TerminalFocus, error) {
		if text != "" {
			typed = append(typed, text)
		}
		if enter {
			typed = append(typed, "<enter>")
		}
		return &TerminalFocus{Session: "vera", Window: "1", Pane: "1", Agent: "claude-code"}, nil
	}
	code, out := post(`{"text":"  fix the race  ","clean":true}`)
	if code != 200 || out.Into == nil || out.Into.Session != "vera" || !out.Raw {
		t.Fatalf("got %d %+v — echo has no cleaner, so raw should be marked", code, out)
	}
	if len(typed) != 1 || typed[0] != "fix the race" {
		t.Fatalf("typed %q; Enter must not be pressed unless asked", typed)
	}
	post(`{"text":"","enter":true}`)
	if len(typed) != 2 || typed[1] != "<enter>" {
		t.Fatalf("an empty text with enter should press Enter alone: %q", typed)
	}

	lanUnderTest.typer = func(context.Context, string, bool, bool) (*TerminalFocus, error) { return nil, ErrNoTarget }
	if code, _ := post(`{"text":"hi"}`); code != http.StatusConflict {
		t.Fatalf("no target should be a conflict, got %d", code)
	}
}

// fakeSTT is a Transcriber under test control.
type fakeSTT struct {
	ready bool
	text  string
}

func (f *fakeSTT) Transcribe(context.Context, string) (string, error) { return f.text, nil }
func (f *fakeSTT) Status(context.Context) STTStatus {
	return STTStatus{Engine: "fake", UV: true, Installed: f.ready, ModelReady: f.ready, Ready: f.ready}
}
func (f *fakeSTT) Install(_ context.Context, progress func(string)) error {
	progress("installing")
	f.ready = true
	progress("Ready.")
	return nil
}

func TestTranscribeNeedsAReadyEngine(t *testing.T) {
	base, id := serveLAN(t, echo)
	stt := &fakeSTT{ready: false, text: "pull the pr down"}
	lanUnderTest.stt = stt

	post := func() (int, Transcribed) {
		req, _ := http.NewRequest("POST", base+"/transcribe", strings.NewReader("RIFFfake-audio"))
		req.Header.Set("Authorization", "Bearer "+id.Secret)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out Transcribed
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := post(); code != http.StatusServiceUnavailable {
		t.Fatalf("an unready engine should be 503, got %d", code)
	}
	stt.ready = true
	code, out := post()
	if code != 200 || out.Text != "pull the pr down" {
		t.Fatalf("got %d %q", code, out.Text)
	}
}

func TestSTTInstallStreamsProgressToDone(t *testing.T) {
	base, id := serveLAN(t, echo)
	lanUnderTest.stt = &fakeSTT{}
	req, _ := http.NewRequest("POST", base+"/stt/install", nil)
	req.Header.Set("Authorization", "Bearer "+id.Secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var steps int
	var done bool
	scan := bufio.NewScanner(res.Body)
	for scan.Scan() {
		var f Frame
		if json.Unmarshal(scan.Bytes(), &f) != nil {
			continue
		}
		if f.Status != "" {
			steps++
		}
		if f.Done {
			done = true
		}
		if f.Error != "" {
			t.Fatalf("install errored: %s", f.Error)
		}
	}
	if steps == 0 || !done {
		t.Fatalf("want progress then done, got steps=%d done=%v", steps, done)
	}
}

func TestFrecencyRanksRecentFirstAndMarksCurrent(t *testing.T) {
	a := newAttention()
	now := time.Now()
	// Equal frequency, different recency: Notes last week, Ghostty just
	// now. Recency must break the tie.
	for i := 0; i < 3; i++ {
		a.Observe(focused("m", "Notes", "com.apple.Notes", now.Add(-7*24*time.Hour)))
		a.Observe(focused("m", "Ghostty", "com.mitchellh.ghostty", now.Add(-time.Duration(i)*time.Minute)))
	}
	// A rook pane, fresh, carrying the coordinates a jump needs.
	pane := &TerminalFocus{Session: "vera", Window: "1", Pane: "1", Agent: "claude-code", Title: "the work"}
	a.Observe(Observation{Type: "terminal.focus", Device: "m", Source: "rook", At: now, Terminal: pane})

	ranked := a.Rank("m", now, 12)
	pos := map[string]int{}
	for i, r := range ranked {
		pos[r.Label] = i
	}
	if pos["Ghostty"] > pos["Notes"] {
		t.Fatalf("with equal frequency the recent app should rank first: %v", pos)
	}
	var foundPane bool
	for _, r := range ranked {
		if r.Kind == "pane" {
			foundPane = true
			if r.Terminal == nil || r.Terminal.Session != "vera" {
				t.Fatalf("a pane target must carry its coordinates: %+v", r)
			}
		}
	}
	if !foundPane {
		t.Fatal("the rook pane should be a jump target")
	}
	// The place in front of the person is marked, and the list does not
	// reshuffle to put it first.
	a.Observe(focused("m", "Ghostty", "com.mitchellh.ghostty", now))
	var marked bool
	for _, r := range a.Rank("m", now, 12) {
		if r.Label == "Ghostty" {
			marked = r.Current
		}
	}
	if !marked {
		t.Fatal("the current app should be marked")
	}
}
