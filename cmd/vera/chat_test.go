package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeVerad is enough of the wire to test the client: /say streams an
// echo as ndjson frames, /status answers, /fleet is absent, and the
// secret is checked.
func fakeVerad(t *testing.T) (string, Identity) {
	t.Helper()
	id := Identity{Peer: "peer-under-test", Secret: "s3cret", Name: "test-mac"}
	mux := http.NewServeMux()
	authed := func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+id.Secret }
	mux.HandleFunc("POST /say", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		var m Message
		_ = json.NewDecoder(r.Body).Decode(&m)
		enc := json.NewEncoder(w)
		_ = enc.Encode(Frame{Run: "r1"})
		_ = enc.Encode(Frame{Status: "thinking"})
		for _, word := range strings.Fields("You said: " + m.Text) {
			_ = enc.Encode(Frame{Delta: word + " "})
		}
		_ = enc.Encode(Frame{Done: true})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(Status{Name: id.Name, Mind: "echo"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, id
}

func TestChatClientSpeaksThePhonesWire(t *testing.T) {
	base, id := fakeVerad(t)
	c := &chatClient{base: base, secret: id.Secret, device: id.Name}

	var got []Frame
	if err := c.say(context.Background(), "hello there", "conv-1", func(f Frame) { got = append(got, f) }); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, f := range got {
		text.WriteString(f.Delta)
	}
	if !strings.Contains(text.String(), "You said: hello there") || !got[len(got)-1].Done {
		t.Fatalf("frames: %+v", got)
	}

	s, err := c.status(context.Background())
	if err != nil || s.Name != "test-mac" {
		t.Fatalf("status: %v %+v", err, s)
	}

	// No fleet on the test server: the route is absent, and the client
	// says so rather than pretending.
	if _, err := c.tasks(context.Background()); err == nil {
		t.Fatal("tasks should fail without a fleet")
	}

	bad := &chatClient{base: base, secret: "wrong", device: id.Name}
	if err := bad.say(context.Background(), "x", "c", func(Frame) {}); err == nil {
		t.Fatal("the secret is required")
	}
}

// The model's Update/View are pure, so the TUI is testable without a
// terminal: feed it frames and keys, read what it would draw.
func TestChatModelStreamsAndRenders(t *testing.T) {
	m := newChatModel(&chatClient{}, false)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.Update(frameMsg{Frame{Status: "Working on it…"}})
	if !strings.Contains(m.View(), "Working on it…") {
		t.Error("status line should show while nothing is said")
	}
	m.Update(frameMsg{Frame{Delta: "Hello"}})
	m.Update(frameMsg{Frame{Delta: ", world"}})
	if v := m.View(); !strings.Contains(v, "Hello, world") || strings.Contains(v, "Working on it") {
		t.Errorf("a delta replaces the status:\n%s", v)
	}
	m.Update(sayDone{})
	if len(m.lines) != 1 || m.lines[0].who != "vera" || m.lines[0].text != "Hello, world" {
		t.Errorf("lines: %+v", m.lines)
	}

	m.input.SetValue("/help")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "/answer <id> <text>") {
		t.Error("help not shown")
	}
	m.input.SetValue("/nonsense")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "unknown command") {
		t.Error("unknown command not reported")
	}
	m.input.SetValue("/answer")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "/answer <id> <text>") {
		t.Error("usage not reported")
	}
}
