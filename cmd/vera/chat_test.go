package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/tui"
	"github.com/incantery/vera/fleet"
)

// fakeVerad is enough of the wire to test the client and the terminal's
// agent: /say streams an echo with a tool round, /status answers,
// /fleet is whatever the test set, and the secret is checked.
type fakeVerad struct {
	url   string
	id    Identity
	mu    sync.Mutex
	views []fleet.View
	posts []string
}

func newFakeVerad(t *testing.T) *fakeVerad {
	t.Helper()
	f := &fakeVerad{id: Identity{Peer: "peer-under-test", Secret: "s3cret", Name: "test-mac"}}
	mux := http.NewServeMux()
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") == "Bearer "+f.id.Secret {
			return true
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	mux.HandleFunc("POST /say", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		var m Message
		_ = json.NewDecoder(r.Body).Decode(&m)
		enc := json.NewEncoder(w)
		_ = enc.Encode(Frame{Run: "r1"})
		_ = enc.Encode(Frame{Status: "thinking"})
		_ = enc.Encode(Frame{ToolCall: &ToolCallFrame{ID: "t1", Name: "fleet", Args: `{"verb":"tasks"}`}})
		_ = enc.Encode(Frame{ToolResult: &ToolResultFrame{ID: "t1", Result: "no tasks", DurationMs: 1500, CostUSD: 0.02}})
		for _, word := range strings.Fields("You said: " + m.Text) {
			_ = enc.Encode(Frame{Delta: word + " "})
		}
		_ = enc.Encode(Frame{Done: true})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(Status{Name: f.id.Name, Mind: "echo"})
	})
	mux.HandleFunc("GET /fleet", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(f.views)
	})
	mux.HandleFunc("POST /fleet/", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		f.mu.Lock()
		f.posts = append(f.posts, r.URL.Path)
		f.mu.Unlock()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func (f *fakeVerad) client() *chatClient {
	return &chatClient{base: f.url, secret: f.id.Secret, device: f.id.Name}
}

func (f *fakeVerad) setFleet(v ...fleet.View) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.views = v
}

func (f *fakeVerad) posted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...)
}

func TestChatClientSpeaksThePhonesWire(t *testing.T) {
	f := newFakeVerad(t)
	c := f.client()

	var got []Frame
	if err := c.say(context.Background(), "hello there", "conv-1", func(fr Frame) { got = append(got, fr) }); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, fr := range got {
		text.WriteString(fr.Delta)
	}
	if !strings.Contains(text.String(), "You said: hello there") || !got[len(got)-1].Done {
		t.Fatalf("frames: %+v", got)
	}

	s, err := c.status(context.Background())
	if err != nil || s.Name != "test-mac" {
		t.Fatalf("status: %v %+v", err, s)
	}

	bad := &chatClient{base: f.url, secret: "wrong", device: f.id.Name}
	if err := bad.say(context.Background(), "x", "c", func(Frame) {}); err == nil {
		t.Fatal("the secret is required")
	}
}

// The wire and the terminal meet in translate; everything else about a
// frame is plumbing. Test the translation on its own.
func TestFramesBecomeEvents(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    Frame
		want []agent.Event
	}{
		{"run alone says nothing", Frame{Run: "r1"}, nil},
		{"done is the stream's business", Frame{Done: true}, nil},
		{"status", Frame{Status: "reading the fleet"}, []agent.Event{agent.Status("reading the fleet")}},
		{"delta", Frame{Delta: "hel"}, []agent.Event{agent.Delta("hel")}},
		{"error", Frame{Error: "boom"}, []agent.Event{agent.Fail("boom")}},
		{
			"tool call",
			Frame{ToolCall: &ToolCallFrame{ID: "t1", Name: "fleet", Args: `{"verb":"tasks"}`}},
			[]agent.Event{agent.Call("t1", "fleet", `{"verb":"tasks"}`)},
		},
		{
			"tool result, with what it cost",
			Frame{ToolResult: &ToolResultFrame{ID: "t1", Result: "two tasks", DurationMs: 2500, CostUSD: 0.03}},
			[]agent.Event{agent.Result("t1", "two tasks", 2500*time.Millisecond, 0.03)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := translate(tc.f)
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("event %d: got %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestAgentStreamsAnExchange(t *testing.T) {
	f := newFakeVerad(t)
	a := veraAgent{f.client()}

	ch, err := a.Send(context.Background(), "chat-1", "hello there")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []agent.Kind
	var reply strings.Builder
	var call, result agent.Event
	for ev := range ch {
		kinds = append(kinds, ev.Kind)
		switch ev.Kind {
		case agent.KindDelta:
			reply.WriteString(ev.Text)
		case agent.KindToolCall:
			call = ev
		case agent.KindToolResult:
			result = ev
		}
	}
	if kinds[0] != agent.KindStatus {
		t.Errorf("first event: %v", kinds[0])
	}
	if kinds[len(kinds)-1] != agent.KindDone {
		t.Errorf("last event: %v", kinds)
	}
	if n := countKind(kinds, agent.KindDone); n != 1 {
		t.Errorf("exactly one done, got %d", n)
	}
	if !strings.Contains(reply.String(), "You said: hello there") {
		t.Errorf("reply: %q", reply.String())
	}
	if call.Name != "fleet" || result.ID != "t1" || result.Duration != 1500*time.Millisecond || result.Cost != 0.02 {
		t.Errorf("tool round: %+v %+v", call, result)
	}

	// A call that cannot start is an error, not a channel that carries one.
	bad := veraAgent{&chatClient{base: f.url, secret: "wrong", device: "x"}}
	if _, err := bad.Send(context.Background(), "chat-1", "hi"); err == nil {
		t.Fatal("a rejected /say should fail before the channel")
	}

	// Cancelling ends the stream.
	ctx, cancel := context.WithCancel(context.Background())
	ch, err = a.Send(ctx, "chat-1", "hello")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled exchange should close its channel")
	}
}

func countKind(kinds []agent.Kind, want agent.Kind) int {
	n := 0
	for _, k := range kinds {
		if k == want {
			n++
		}
	}
	return n
}

func TestSideItemsMapWhatIsBelieved(t *testing.T) {
	want := map[fleet.State]tui.State{
		fleet.Running:  tui.Working,
		fleet.Quiet:    tui.Working,
		fleet.Waiting:  tui.Idle,
		fleet.Stale:    tui.Idle,
		fleet.Held:     tui.Idle,
		fleet.Decision: tui.Blocked,
		fleet.Broken:   tui.Blocked,
		fleet.Finished: tui.Done,
		fleet.Gone:     tui.Failed,
	}
	for state, expect := range want {
		v := fleet.View{Task: &fleet.Task{ID: "a1", Brief: "Port the chat"}, State: state}
		items := sideItems([]fleet.View{v})
		if len(items) != 1 {
			t.Fatalf("%s: %d items", state, len(items))
		}
		if items[0].State != expect {
			t.Errorf("%s → %s, want %s", state, items[0].State, expect)
		}
		if items[0].ID != "a1" || items[0].Title != "Port the chat" {
			t.Errorf("%s: %+v", state, items[0])
		}
	}

	// Closed tasks are not on the rail, however they got there.
	closed := fleet.View{Task: &fleet.Task{ID: "b2", Brief: "Landed"}, State: fleet.Closed}
	if items := sideItems([]fleet.View{closed}); len(items) != 0 {
		t.Errorf("closed on the rail: %+v", items)
	}
	closed.State, closed.Task.Closed = fleet.Finished, true
	if items := sideItems([]fleet.View{closed}); len(items) != 0 {
		t.Errorf("a closed task in a finished state is still closed: %+v", items)
	}

	// The subtitle is what it last said.
	v := fleet.View{
		Task:   &fleet.Task{ID: "c3", Brief: "Do the thing. Then another."},
		State:  fleet.Running,
		Last:   &fleet.Status{Verb: fleet.Working, Text: "reading\nthe wire"},
		Unread: []fleet.Status{{Text: "one"}, {Text: "two"}},
	}
	got := sideItems([]fleet.View{v})[0]
	if got.Title != "Do the thing" || got.Subtitle != "+2 reading the wire" {
		t.Errorf("%+v", got)
	}
}

func TestNoticesWhenATaskNeedsYou(t *testing.T) {
	w := newFleetWatch(nil)
	now := time.Now()
	running := fleet.View{Task: &fleet.Task{ID: "a1", Brief: "Scout the panel"}, State: fleet.Running}

	if lines := w.absorb([]fleet.View{running}, now); len(lines) != 0 {
		t.Fatalf("first sighting is not news: %+v", lines)
	}
	quiet := running
	quiet.State = fleet.Quiet
	if lines := w.absorb([]fleet.View{quiet}, now); len(lines) != 0 {
		t.Fatalf("a change nobody has to act on is not news: %+v", lines)
	}

	done := running
	done.State = fleet.Finished
	done.Last = &fleet.Status{Verb: fleet.Done, Text: "report written"}
	done.Report = "# Findings"
	lines := w.absorb([]fleet.View{done}, now)
	if len(lines) != 1 || !strings.Contains(lines[0], "a1 finished") || !strings.Contains(lines[0], "report written") || !strings.Contains(lines[0], "/report a1") {
		t.Fatalf("lines: %+v", lines)
	}
	if lines := w.absorb([]fleet.View{done}, now); len(lines) != 0 {
		t.Fatalf("unchanged state is not news: %+v", lines)
	}

	landed := done
	landed.State = fleet.Closed
	lines = w.absorb([]fleet.View{landed}, now)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "✓ a1") || !strings.Contains(lines[0], "report written") {
		t.Fatalf("a landing says so: %+v", lines)
	}

	// The rail agrees: what closed is off it.
	if items := w.side(); len(items) != 0 {
		t.Errorf("rail: %+v", items)
	}
}

// texts runs a command's tea.Cmd and reports every message it produced,
// flattened, as strings. The terminal's own message types are private,
// so what they carry is read the only way an application can.
func texts(cmd tea.Cmd) []string {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []string
		for _, c := range msg {
			out = append(out, texts(c)...)
		}
		return out
	default:
		return []string{fmt.Sprintf("%v", msg)}
	}
}

func joined(cmd tea.Cmd) string { return strings.Join(texts(cmd), "\n") }

func TestCommandsSayWhatTheyNeed(t *testing.T) {
	f := newFakeVerad(t)
	s := &chatSession{c: f.client(), w: newFleetWatch(f.client()), conv: "chat-1"}

	for _, tc := range []struct{ line, want string }{
		{"answer", "/answer <id> <text>"},
		{"land", "/land <id>"},
		{"stop", "/stop <id> [force]"},
		{"seen", "/seen <id>"},
		{"resume", "/resume <id>"},
		{"report", "/report <id>"},
		{"start", "/start [@repo] <brief>"},
		{"scout", "/scout [@repo] <brief>"},
		{"nonsense", "unknown command /nonsense"},
	} {
		name, args, _ := strings.Cut(tc.line, " ")
		if got := joined(s.handle(name, args)); !strings.Contains(got, tc.want) {
			t.Errorf("/%s said %q, want %q", tc.line, got, tc.want)
		}
	}

	if _, ok := s.handle("quit", "")().(tea.QuitMsg); !ok {
		t.Error("/quit should quit")
	}

	// /new moves the conversation, and says which one it moved to.
	before := s.conversation()
	got := joined(s.handle("new", ""))
	if s.conversation() == before || !strings.Contains(got, s.conversation()) {
		t.Errorf("/new: %q, conversation %q", got, s.conversation())
	}
}

func TestCommandsReachVerad(t *testing.T) {
	f := newFakeVerad(t)
	f.setFleet(fleet.View{
		Task:   &fleet.Task{ID: "a1", Brief: "Port the chat"},
		State:  fleet.Finished,
		Report: "# Findings\n\nIt works.",
	})
	c := f.client()
	s := &chatSession{c: c, w: newFleetWatch(c), conv: "chat-1"}

	if got := joined(s.handle("tasks", "")); !strings.Contains(got, "a1") || !strings.Contains(got, "Port the chat") {
		t.Errorf("/tasks: %q", got)
	}

	// Reading a report shows it, and marks it seen.
	if got := joined(s.handle("report", "a1")); !strings.Contains(got, "# Findings") {
		t.Errorf("/report: %q", got)
	}
	if got := joined(s.handle("report", "zz")); !strings.Contains(got, "no task zz") {
		t.Errorf("/report of nothing: %q", got)
	}

	if got := joined(s.handle("answer", "a1 keep going")); !strings.Contains(got, "sent to a1") {
		t.Errorf("/answer: %q", got)
	}
	if got := joined(s.handle("stop", "a1 force")); !strings.Contains(got, "stopped a1") {
		t.Errorf("/stop: %q", got)
	}

	want := []string{"/fleet/a1/seen", "/fleet/a1/answer", "/fleet/a1/teardown"}
	if got := f.posted(); !equal(got, want) {
		t.Errorf("posted %v, want %v", got, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBeliefReadsLikeThePreface(t *testing.T) {
	since := time.Now().Add(-12 * time.Minute)
	md := beliefMarkdown(&Status{
		Name: "vera", Mind: "opus", RunsInFlight: 2,
		Devices: []DeviceStatus{{
			Name: "seths-mbp", Fresh: true,
			Focus:      &ObservedApp{Name: "Ghostty"},
			FocusSince: &since,
			Terminal:   &TerminalFocus{Session: "main", Window: "1", Command: "vim", Path: "/src/vera"},
		}},
		Integrations: []IntegrationStatus{{Name: "calendar", Connected: true}, {Name: "mail"}},
	}, "chat-1")
	for _, want := range []string{"vera", "opus", "2 run(s) in flight", "chat-1", "seths-mbp", "fresh", "Ghostty", "vim in vera", "connected: calendar"} {
		if !strings.Contains(md, want) {
			t.Errorf("belief is missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "mail") {
		t.Error("an integration that is not connected is not connected")
	}
}
