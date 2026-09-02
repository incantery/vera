package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/incantery/mote/agent"
	"github.com/incantery/mote/session"
	"github.com/incantery/mote/tui"
	"github.com/incantery/vera/costs"
	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
	"github.com/incantery/vera/journal"
	"github.com/incantery/vera/price"
)

// fakeVerad is enough of the wire to test the client and the terminal's
// agent: /say streams an echo with a tool round, /status answers,
// /fleet is whatever the test set, and the secret is checked.
type fakeVerad struct {
	url    string
	id     Identity
	mu     sync.Mutex
	views  []fleet.View
	posts  []string
	said   []Message // every /say, whole, for a test that cares what rode on one
	model  Resolution
	saved  Resolution // what PUT /model has been told, merged
	chosen *InForce   // what this conversation chose, as /models reports it
	dflt   *InForce   // what PUT /model last saved
	// told is every line POST /todo received, and todoReply is what to
	// answer with. Nil answers an empty list.
	told      []string
	todoReply func(line string) TodoAnswer
	// refuse is what every POST /fleet/… answers with instead of
	// doing it, for a test about what a refused verb says.
	refuse string
}

func newFakeVerad(t *testing.T) *fakeVerad {
	t.Helper()
	f := &fakeVerad{
		id: Identity{Peer: "peer-under-test", Secret: "s3cret", Name: "test-mac"},
		model: Resolution{Model: "gpt-5.6-luna", Effort: "none", Provider: "openai",
			ModelFrom: "the built-in default", EffortFrom: "the built-in default"},
		saved: Resolution{Model: "gpt-5.6-luna", Effort: "none", Provider: "openai",
			ModelFrom: "the built-in default", EffortFrom: "the built-in default"},
	}
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
		f.mu.Lock()
		f.said = append(f.said, m)
		f.mu.Unlock()
		enc := json.NewEncoder(w)
		_ = enc.Encode(Frame{Run: "r1"})
		_ = enc.Encode(Frame{Status: "thinking"})
		_ = enc.Encode(Frame{ToolCall: &ToolCallFrame{ID: "t1", Name: "fleet", Args: `{"verb":"tasks"}`}})
		_ = enc.Encode(Frame{ToolResult: &ToolResultFrame{ID: "t1", Result: "no tasks", DurationMs: 1500, CostUSD: 0.02}})
		for _, word := range strings.Fields("You said: " + m.Text) {
			_ = enc.Encode(Frame{Delta: word + " "})
		}
		_ = enc.Encode(Frame{Done: true, Usage: &sayUsageFrame})
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
	mux.HandleFunc("GET /conversations/{id}/model", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(f.model)
	})
	mux.HandleFunc("GET /models", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		ans := ModelsAnswer{
			Default: InForce{Model: "gpt-5.6-luna", Effort: "none", From: "the built-in default"},
			Models: []ModelRow{
				{Name: "gpt-5.6-luna", Provider: "openai", Wire: "responses",
					Efforts: []string{"none", "low", "medium", "high"},
					Note:    "the dial, via the responses API", Priced: true},
				{Name: "gpt-5.6-terra", Provider: "openai", Efforts: []string{"none"},
					Note: "effort none only (chat completions)", Priced: true},
				{Name: "gpt-5", Provider: "openai", Efforts: []string{"none", "low", "medium", "high"}, Priced: true},
				{Name: "claude-opus-5", Provider: "anthropic", Efforts: []string{"low", "medium", "high", "max"}, Priced: true},
				{Name: "my-local-7b", Provider: "openai", Efforts: []string{"none"}},
			},
		}
		if f.dflt != nil {
			ans.Default = *f.dflt
		}
		if f.chosen != nil {
			ans.Conversation = f.chosen
		}
		_ = json.NewEncoder(w).Encode(ans)
	})
	mux.HandleFunc("PUT /model", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		var body struct{ Model, Effort string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.posts = append(f.posts, r.URL.Path)
		// The two halves merge, the way verad's do: a half left empty
		// is one nobody said anything about, not one being cleared.
		if body.Model != "" {
			f.saved.Model, f.saved.ModelFrom = body.Model, "the saved default"
		}
		if body.Effort != "" {
			f.saved.Effort, f.saved.EffortFrom = body.Effort, "the saved default"
		}
		f.saved.Provider = "anthropic"
		f.dflt = &InForce{Model: f.saved.Model, Effort: f.saved.Effort, From: "the saved default"}
		_ = json.NewEncoder(w).Encode(f.saved)
	})
	mux.HandleFunc("POST /conversations/{id}/model", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		var body struct{ Model, Effort string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.posts = append(f.posts, r.URL.Path)
		if body.Model == "no-such-model" {
			http.Error(w, "cannot reach no-such-model", http.StatusBadRequest)
			return
		}
		if body.Model != "" {
			f.model.Model, f.model.ModelFrom = body.Model, "this conversation"
		}
		if body.Effort != "" {
			f.model.Effort, f.model.EffortFrom = body.Effort, "this conversation"
		}
		f.model.Provider = "anthropic"
		f.chosen = &InForce{Model: f.model.Model, Effort: f.model.Effort}
		_ = json.NewEncoder(w).Encode(f.model)
	})
	// The list. The daemon parses the line; a fake only has to answer
	// like one, so a test can say "this is what verad decided" and
	// check what the screen did with it.
	mux.HandleFunc("POST /todo", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		var body struct{ Line, From string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.told = append(f.told, body.Line)
		fn := f.todoReply
		f.mu.Unlock()
		if fn == nil {
			fn = func(string) TodoAnswer { return TodoAnswer{Verb: "list", Items: []home.Item{}} }
		}
		_ = json.NewEncoder(w).Encode(fn(body.Line))
	})
	mux.HandleFunc("POST /fleet/", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		f.mu.Lock()
		f.posts = append(f.posts, r.URL.Path)
		why := f.refuse
		f.mu.Unlock()
		if why != "" {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": why})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

// sayUsageFrame is what the fake claims the exchange spent. The
// dollars are the price table's answer for those tokens on that model,
// worked out by hand so a change to the table shows up here as a
// failing test rather than as a silently different status line.
var sayUsageFrame = UsageFrame{
	Model: "claude-opus-5", InputTokens: 12000, OutputTokens: 800,
	CacheReadTokens: 9000, CostUSD: 0.0395, Priced: true,
}

func (f *fakeVerad) client() *chatClient {
	return &chatClient{base: f.url, secret: f.id.Secret, device: f.id.Name}
}

func (f *fakeVerad) setFleet(v ...fleet.View) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.views = v
}

// messages is every /say this fake received, in order.
func (f *fakeVerad) messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.said...)
}

// answerTodo makes the fake reply with fn, and returns every line it
// was told.
func (f *fakeVerad) answerTodo(fn func(line string) TodoAnswer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.todoReply = fn
}

func (f *fakeVerad) todoLines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.told...)
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
	if err := c.say(context.Background(), Message{Text: "hello there", Conversation: "conv-1"}, func(fr Frame) { got = append(got, fr) }); err != nil {
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
	if err := bad.say(context.Background(), Message{Text: "x", Conversation: "c"}, func(Frame) {}); err == nil {
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
			// Nothing sends one yet; the terminal is ready for it.
			"tool output, streamed into the open card",
			Frame{ToolOutput: &ToolOutputFrame{ID: "t1", Text: "reading the fleet\n"}},
			[]agent.Event{agent.Output("t1", "reading the fleet\n")},
		},
		{
			"tool result, with what it cost",
			Frame{ToolResult: &ToolResultFrame{ID: "t1", Result: "two tasks", DurationMs: 2500, CostUSD: 0.03}},
			[]agent.Event{agent.Result("t1", "two tasks", 2500*time.Millisecond, 0.03)},
		},
		{
			// It rides on the terminal frame, and the terminal frame is
			// Send's — finish is where it becomes an event.
			"usage is the stream's business too",
			Frame{Done: true, Usage: &sayUsageFrame},
			nil,
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

// What the turn spent becomes the one event that ends it, because that
// is where mote adds it to the status line's running total.
func TestTheDoneEventCarriesWhatTheTurnSpent(t *testing.T) {
	for _, tc := range []struct {
		name string
		u    *UsageFrame
		want agent.Event
	}{
		{
			// An older verad says nothing about usage. A turn that cost
			// an unknown amount must not read as a turn that was free.
			"no usage on the wire",
			nil,
			agent.Done(),
		},
		{
			"priced",
			&UsageFrame{Model: "claude-opus-5", InputTokens: 12000, OutputTokens: 800, CostUSD: 0.0395, Priced: true},
			agent.Spent(0.0395, 12000, 800),
		},
		{
			// Tokens are counted; dollars are not. A model with no price
			// gets its tokens on the screen and no dollar figure at all.
			"tokens known, price not",
			&UsageFrame{Model: "some-local-model", InputTokens: 900, OutputTokens: 100},
			agent.Spent(0, 900, 100),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := finish(tc.u); got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAgentStreamsAnExchange(t *testing.T) {
	f := newFakeVerad(t)
	a := veraAgent{c: f.client()}

	ch, err := a.Send(context.Background(), "chat-1", "hello there")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []agent.Kind
	var reply strings.Builder
	var call, result, last agent.Event
	for ev := range ch {
		kinds = append(kinds, ev.Kind)
		switch ev.Kind {
		case agent.KindDelta:
			reply.WriteString(ev.Text)
		case agent.KindToolCall:
			call = ev
		case agent.KindToolResult:
			result = ev
		case agent.KindDone:
			last = ev
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
	if last.Cost != sayUsageFrame.CostUSD || last.InputTokens != sayUsageFrame.InputTokens || last.OutputTokens != sayUsageFrame.OutputTokens {
		t.Errorf("done should carry the turn's spend: %+v", last)
	}

	// A call that cannot start is an error, not a channel that carries one.
	bad := veraAgent{c: &chatClient{base: f.url, secret: "wrong", device: "x"}}
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

	if evs := w.absorb([]fleet.View{running}, now); len(evs) != 0 {
		t.Fatalf("first sighting is not news: %+v", evs)
	}
	quiet := running
	quiet.State = fleet.Quiet
	if evs := w.absorb([]fleet.View{quiet}, now); len(evs) != 0 {
		t.Fatalf("a change nobody has to act on is not news: %+v", evs)
	}

	done := running
	done.State = fleet.Finished
	done.Last = &fleet.Status{Verb: fleet.Done, Text: "merged"}
	evs := w.absorb([]fleet.View{done}, now)
	// Landing is the person's here (AutoLand is off on this view), so
	// the verb it offers is the landing. The tick is the state, the
	// task's own last word is the detail, and /land is what to do —
	// one channel each.
	if len(evs) != 1 || !strings.Contains(evs[0].Text, "✓ Task finished · Scout the panel") ||
		!strings.Contains(evs[0].Text, "merged") || !strings.Contains(evs[0].Text, "/land a1") {
		t.Fatalf("events: %+v", evs)
	}
	if strings.Contains(evs[0].Text, "×") {
		t.Errorf("red is failure only: %q", evs[0].Text)
	}
	if evs := w.absorb([]fleet.View{done}, now); len(evs) != 0 {
		t.Fatalf("unchanged state is not news: %+v", evs)
	}

	landed := done
	landed.State = fleet.Closed
	landed.Report = "# What changed\n\nThe panel reads the cache now."
	evs = w.absorb([]fleet.View{landed}, now)
	if len(evs) != 1 || !strings.HasPrefix(evs[0].Text, "✓ Task landed · Scout the panel") ||
		!strings.Contains(evs[0].Text, "merged") {
		t.Fatalf("a landing says so: %+v", evs)
	}
	// It landed itself and closed itself; the summary it wrote on the
	// way out is the last thing the person can still read, so the line
	// that ends the task says where it is.
	if !strings.Contains(evs[0].Text, "/report a1") {
		t.Errorf("a landing with a report should point at it: %q", evs[0].Text)
	}

	// The rail agrees: what closed is off it.
	if items := w.side(); len(items) != 0 {
		t.Errorf("rail: %+v", items)
	}
}

// A ship task the supervisor lands by itself is never "ready to land":
// nobody is waiting for the person, and the next thing they hear is
// that it landed.
func TestAShipTaskVeraLandsHerselfDoesNotAskThemToLandIt(t *testing.T) {
	w := newFleetWatch(nil)
	now := time.Now()
	v := fleet.View{Task: &fleet.Task{ID: "b2", Kind: fleet.Ship, Brief: "Move the thing"}, State: fleet.Running, AutoLand: true}
	w.absorb([]fleet.View{v}, now)

	v.State = fleet.Finished
	evs := w.absorb([]fleet.View{v}, now)
	if len(evs) != 1 || strings.Contains(evs[0].Text, "ready to land") || !strings.Contains(evs[0].Text, "landing it") {
		t.Fatalf("events: %+v", evs)
	}

	// And a landing that failed says what went wrong, not "a decision".
	v.State = fleet.Decision
	v.LandFailure = "merge conflict in README"
	evs = w.absorb([]fleet.View{v}, now)
	// A landing that failed needs the person, and needing the person
	// is not a crash: ◇ and the failure's own words, never ×.
	if len(evs) != 1 || !strings.Contains(evs[0].Text, "◇ Task needs you") ||
		!strings.Contains(evs[0].Text, "merge conflict in README") ||
		!strings.Contains(evs[0].Text, "/answer b2") {
		t.Fatalf("a failed landing should say so: %+v", evs)
	}
	if strings.Contains(evs[0].Text, "×") {
		t.Errorf("attention is not failure: %q", evs[0].Text)
	}
}

// A scout that finishes has nothing to land. What it has is a report,
// and until somebody has read it the task needs them.
func TestAScoutReportsRatherThanLands(t *testing.T) {
	w := newFleetWatch(nil)
	now := time.Now()
	v := fleet.View{Task: &fleet.Task{ID: "1ea6a4b5", Kind: fleet.Scout, Brief: "Find out why it is slow"}, State: fleet.Running, AutoLand: true}
	w.absorb([]fleet.View{v}, now)

	v.State = fleet.Finished
	v.Report = "# Findings\n\nThe cache prefix changes every turn.\n"
	v.Unread = []fleet.Status{{Verb: fleet.Done, Text: "report written"}}
	evs := w.absorb([]fleet.View{v}, now)
	// Done AND unread, on one row and in two channels: the tick is
	// what became of it, ● is that nobody has read it yet.
	want := "✓ Scout reported · Find out why it is slow ●\nFindings · /report 1ea6a4b5"
	if len(evs) != 1 || evs[0].Text != want {
		t.Fatalf("notice:\n got %+v\nwant %q", evs, want)
	}

	// On the rail it is done AND it needs them: it did everything it
	// was asked, and a tick on its own would say there is nothing to
	// do here.
	item := sideItems([]fleet.View{v})[0]
	if !item.Needs || item.State != tui.Done || item.Subtitle != "report waiting" {
		t.Fatalf("rail row: %+v", item)
	}

	// Read is seen; then it closes, and it does not announce itself a
	// second time.
	v.Unread = nil
	if item := sideItems([]fleet.View{v})[0]; item.Needs || item.State != tui.Done {
		t.Fatalf("a report that was read is done and wants nothing: %+v", item)
	}
	v.State = fleet.Closed
	if evs := w.absorb([]fleet.View{v}, now); len(evs) != 0 {
		t.Fatalf("closing a scout that already reported is not news: %+v", evs)
	}
}

// Reading a report is the review, and the review is read -> decide.
// The pick has to survive a person who saw the id on screen and typed
// three characters of it, or none at all; and what comes back has to
// say whose report it is and what the next verb is.
func TestReadingAReport(t *testing.T) {
	scout := fleet.View{
		Task:   &fleet.Task{ID: "1ea6a4b5", Kind: fleet.Scout, Project: "/w/vera", Brief: "Find out why it is slow. It has been for weeks."},
		State:  fleet.Finished,
		Report: "# Findings\n\nThe cache prefix changes every turn.",
		Unread: []fleet.Status{{Verb: fleet.Done, Text: "report written"}}, AutoLand: true,
	}
	ship := fleet.View{
		Task:  &fleet.Task{ID: "1e00ffff", Kind: fleet.Ship, Project: "/w/vera", Branch: "vera-1e00", Brief: "Port the chat"},
		State: fleet.Running,
	}
	views := []fleet.View{ship, scout}

	// Nothing named: the one report that is waiting.
	if v, err := pickReport(views, ""); err != nil || v.ID != scout.ID {
		t.Errorf("bare /report picked %q (%v), want the waiting report", v.ID, err)
	}
	// A prefix is enough — the command line has always taken one.
	if v, err := pickReport(views, "1ea"); err != nil || v.ID != scout.ID {
		t.Errorf("prefix: %q (%v)", v.ID, err)
	}
	// A prefix that fits two names them instead of guessing.
	_, err := pickReport(views, "1e")
	if err == nil || !strings.Contains(err.Error(), "1e00ffff") || !strings.Contains(err.Error(), "1ea6a4b5") {
		t.Errorf("an ambiguous prefix should name the candidates: %v", err)
	}
	if _, err := pickReport(views, "zz"); err == nil || err.Error() != "no task zz" {
		t.Errorf("unknown id: %v", err)
	}
	// An exact id beats a prefix: a task whose id is another's prefix
	// is still reachable by typing all of it.
	short := fleet.View{Task: &fleet.Task{ID: "1e", Kind: fleet.Scout}, Report: "x"}
	if v, err := pickReport([]fleet.View{ship, short}, "1e"); err != nil || v.ID != "1e" {
		t.Errorf("exact id: %q (%v)", v.ID, err)
	}
	// Two waiting: named, not guessed between.
	ship.Report, ship.Unread, ship.State = "# Done\n\nmerged", []fleet.Status{{Verb: fleet.Done}}, fleet.Finished
	if _, err := pickReport([]fleet.View{ship, scout}, ""); err == nil || !strings.Contains(err.Error(), "1e00ffff") || !strings.Contains(err.Error(), "1ea6a4b5") {
		t.Errorf("two waiting: %v", err)
	}
	// A closed task is not waiting on anybody, but it is still there
	// to be re-read by name.
	closed := scout
	closed.Task = &fleet.Task{ID: "0ldc0de", Kind: fleet.Scout, Closed: true}
	closed.State = fleet.Closed
	if _, err := pickReport([]fleet.View{closed}, ""); err == nil || !strings.Contains(err.Error(), "no report is waiting") {
		t.Errorf("closed is not waiting: %v", err)
	}
	if v, err := pickReport([]fleet.View{closed}, "0ld"); err != nil || v.ID != "0ldc0de" {
		t.Errorf("re-reading a closed task: %q (%v)", v.ID, err)
	}

	// What is shown: whose it is, where it worked, the report, and the
	// one thing to do about it now.
	md := reportMarkdown(scout)
	for _, want := range []string{"1ea6a4b5", "scout", "finished", "vera", "Find out why it is slow", "cache prefix changes", "Nothing to land"} {
		if !strings.Contains(md, want) {
			t.Errorf("the report as shown is missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "/land 1ea6a4b5") {
		t.Errorf("a scout has nothing to land:\n%s", md)
	}
}

// The next verb is a claim about what happens now, so it says what is
// true of THIS task rather than one line for every report.
func TestWhatToDoAboutAReport(t *testing.T) {
	task := func(kind fleet.Kind) *fleet.Task {
		return &fleet.Task{ID: "a1", Kind: kind, Branch: "vera-a1"}
	}
	for _, tc := range []struct {
		name string
		v    fleet.View
		want string
	}{
		{"blocked", fleet.View{Task: task(fleet.Ship), State: fleet.Decision}, "blocked on you"},
		{"turn over", fleet.View{Task: task(fleet.Ship), State: fleet.Waiting}, "nobody has answered"},
		{"paused", fleet.View{Task: task(fleet.Ship), State: fleet.Held}, "paused on something outside"},
		{"failed", fleet.View{Task: task(fleet.Ship), State: fleet.Broken}, "/resume a1"},
		{"landing itself", fleet.View{Task: task(fleet.Ship), State: fleet.Finished, AutoLand: true}, "Vera is landing it"},
		{"waiting to land", fleet.View{Task: task(fleet.Ship), State: fleet.Finished}, "/land a1"},
		{"scout, closed for them", fleet.View{Task: task(fleet.Scout), State: fleet.Finished, AutoLand: true}, "Vera closes it"},
		{"scout, theirs to close", fleet.View{Task: task(fleet.Scout), State: fleet.Finished}, "/land a1"},
		{"still going", fleet.View{Task: task(fleet.Ship), State: fleet.Running}, "still going"},
	} {
		if got := reportNext(tc.v); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q, want %q in it", tc.name, got, tc.want)
		}
	}
	// A closed task has no next verb; the work is over.
	if got := reportNext(fleet.View{Task: task(fleet.Ship), State: fleet.Closed}); got != "" {
		t.Errorf("closed: %q", got)
	}
}

// Every notice about a task carries the task's id, so the terminal
// rewrites that task's line instead of stacking one per change — and
// the landing rewrites the same line one last time.
func TestNoticesAreAboutTheTask(t *testing.T) {
	w := newFleetWatch(nil)
	now := time.Now()
	view := func(id string, st fleet.State) fleet.View {
		return fleet.View{Task: &fleet.Task{ID: id, Brief: "Port the chat"}, State: st}
	}
	w.absorb([]fleet.View{view("a1", fleet.Running), view("b2", fleet.Running)}, now)

	var ids []string
	for _, st := range []fleet.State{fleet.Decision, fleet.Finished, fleet.Closed} {
		evs := w.absorb([]fleet.View{view("a1", st), view("b2", fleet.Running)}, now)
		if len(evs) != 1 {
			t.Fatalf("%s: %+v", st, evs)
		}
		if evs[0].Kind != agent.KindNotice {
			t.Errorf("%s: kind %q", st, evs[0].Kind)
		}
		ids = append(ids, evs[0].ID)
	}
	for _, id := range ids {
		if id != "a1" {
			t.Fatalf("one task, three states, ids %v — a notice is about the task", ids)
		}
	}
}

// Where Vera believes you are, or, when nobody has told her, what she
// is doing herself. It used to be the right of the status line and is
// `/debug`'s opening line now — the status line says what is true of
// this conversation, and this is true of the world.
func TestWhereYouAre(t *testing.T) {
	focused := &Status{
		RunsInFlight: 2,
		Devices: []DeviceStatus{
			{Name: "stale-mac", Focus: &ObservedApp{Name: "Xcode"}}, // not fresh: not where you are
			{Name: "seths-mbp", Fresh: true,
				Focus:    &ObservedApp{Name: "Ghostty"},
				Terminal: &TerminalFocus{Session: "main", Window: "3", Agent: "claude-code", Title: "✳ port the chat"}},
		},
	}
	got := whereYouAre(focused)
	if !strings.Contains(got, "Ghostty") || !strings.Contains(got, "port the chat") || !strings.Contains(got, "main:3") {
		t.Errorf("focused: %q", got)
	}
	if strings.Contains(got, "Xcode") {
		t.Errorf("a device that has gone quiet is not where you are: %q", got)
	}

	if got := whereYouAre(&Status{RunsInFlight: 2}); got != "2 runs in flight" {
		t.Errorf("nothing focused: %q", got)
	}
	if got := whereYouAre(&Status{RunsInFlight: 1}); got != "1 run in flight" {
		t.Errorf("one run: %q", got)
	}
	if got := whereYouAre(&Status{}); got != "" {
		t.Errorf("nothing to say is nothing said: %q", got)
	}
	if got := whereYouAre(nil); got != "" {
		t.Errorf("no status yet: %q", got)
	}

	// And it reaches the person through the belief block, which is
	// where it went when it came off the line.
	if got := beliefMarkdown(focused, "chat-1"); !strings.Contains(got, "Where you are: ") ||
		!strings.Contains(got, "Ghostty") {
		t.Errorf("/debug should open with where she thinks you are:\n%s", got)
	}
	if got := beliefMarkdown(&Status{}, "chat-1"); strings.Contains(got, "Where you are") {
		t.Errorf("nothing believed is nothing claimed:\n%s", got)
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
	s := &chatSession{c: f.client(), w: newFleetWatch(f.client()), conv: "chat-1",
		dir: t.TempDir(), open: &openSessions{}}

	for _, tc := range []struct{ line, want string }{
		{"answer", "/answer <id> <text>"},
		{"land", "/land <id>"},
		{"stop", "/stop <id> [force]"},
		{"seen", "/seen <id>"},
		{"resume", "/resume <id>"},
		{"report", "no report is waiting"},
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

	// /new moves the conversation and says which one it moved to. The
	// chat's own copy does not move here — the terminal says so when
	// the change lands, which the terminal test checks.
	before := s.conversation()
	got := joined(s.handle("new", ""))
	if !strings.Contains(got, "new conversation chat-") || strings.Contains(got, before) {
		t.Errorf("/new: %q", got)
	}
	if s.conversation() != before {
		t.Errorf("the chat guessed the id instead of being told: %q", s.conversation())
	}
}

func TestCommandsReachVerad(t *testing.T) {
	f := newFakeVerad(t)
	f.setFleet(fleet.View{
		Task:   &fleet.Task{ID: "a1", Kind: fleet.Ship, Brief: "Port the chat"},
		State:  fleet.Finished,
		Report: "# Findings\n\nIt works.",
		Unread: []fleet.Status{{Verb: fleet.Done, Text: "shipped"}},
	})
	c := f.client()
	s := &chatSession{c: c, w: newFleetWatch(c), conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}

	if got := joined(s.handle("tasks", "")); !strings.Contains(got, "a1") || !strings.Contains(got, "Port the chat") {
		t.Errorf("/tasks: %q", got)
	}

	// Reading a report shows it — with whose it is and what to do
	// about it — and marks it seen. A bare /report takes the one that
	// is waiting, which is why the notice can say "reported" and the
	// person can answer it with six characters.
	got := joined(s.handle("report", ""))
	if !strings.Contains(got, "# Findings") || !strings.Contains(got, "a1") || !strings.Contains(got, "/land a1") {
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

// deliver hands a command's messages to the model the way a running
// program would, unwrapping batches. It is the only way to drive the
// terminal from outside: the message types are the terminal's own.
func deliver(m tea.Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			deliver(m, c)
		}
	default:
		m.Update(msg)
	}
}

// The whole of it, once, without a terminal: the real options over a
// fake verad, drawn by mote with the colour turned off.
func TestTheTerminalDrawsWhatVeraGivesIt(t *testing.T) {
	outsideRook(t)
	f := newFakeVerad(t)
	c := f.client()
	f.setFleet(fleet.View{
		Task:   &fleet.Task{ID: "a1", Brief: "Port the chat onto mote"},
		State:  fleet.Decision,
		Last:   &fleet.Status{Verb: fleet.Blocked, Text: "one rail or two?"},
		Report: "# Findings\n\nIt works.",
	})
	w := newFleetWatch(c)
	w.poll(context.Background())
	w.absorbStatus(&Status{Name: "test-mac", RunsInFlight: 1})
	s := &chatSession{c: c, w: w, conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}
	w.conv = s.conversation
	w.pollModel(context.Background())

	m := tui.New(veraAgent{c: c}, headless(chatOptions(&Status{Name: "vera", Mind: "echo"}, s, nil, "say something")))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})

	// The status line: the model actually in use, and nothing else the
	// reference struck off it — no "· none" for a dial nobody turned,
	// no "(from profiles/…)", nothing about where the person is.
	view := screen(m)
	for _, want := range []string{"vera \u00b7 gpt-5.6-luna", "say something", "fleet", "a1", "Port the chat onto mote", "a1 \u00b7 blocked", "one rail o"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}

	deliver(m, s.handle("report", "a1"))
	if v := screen(m); !strings.Contains(v, "Findings") || !strings.Contains(v, "It works.") {
		t.Errorf("/report should print what it fetched:\n%s", v)
	}
	deliver(m, s.handle("start", ""))
	if v := screen(m); !strings.Contains(v, "/start [@repo] <brief>") {
		t.Errorf("a command that cannot run says how:\n%s", v)
	}
}

// headless is the chat's own options with the colour turned off, so a
// test can draw the real screen without a terminal.
func headless(opts tui.Options) tui.Options {
	pal := tui.DefaultPalette()
	pal.Markdown = "ascii"
	opts.Palette = &pal
	return opts
}

// drive runs the terminal the way a bubbletea program does: every
// command runs off the UI goroutine and its message comes back here,
// where Update is the only thing that touches the model. It returns
// when done says so — which is how a test waits for a whole exchange,
// events, tool round and all, to have landed.
func drive(t *testing.T, m *tui.Model, done func() bool, cmds ...tea.Cmd) {
	t.Helper()
	msgs := make(chan tea.Msg, 128)
	var start func(tea.Cmd)
	start = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			switch msg := c().(type) {
			case nil:
			case tea.BatchMsg:
				for _, b := range msg {
					start(b)
				}
			default:
				msgs <- msg
			}
		}()
	}
	for _, c := range cmds {
		start(c)
	}
	deadline := time.After(10 * time.Second)
	for !done() {
		select {
		case msg := <-msgs:
			_, next := m.Update(msg)
			start(next)
		case <-deadline:
			t.Fatal("the exchange never finished")
		}
	}
}

func say(m *tui.Model, text string) tea.Cmd {
	m.Update(tea.KeyPressMsg{Code: []rune(text)[0], Text: text})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return cmd
}

func openChat(t *testing.T, dir, id string) *session.Session {
	t.Helper()
	s, err := session.Open(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A conversation had in the chat is on disk when the chat is gone, and
// `vera chat -c` puts it back on the screen — the whole exchange, the
// tool round included, and the line that was typed to get it.
func TestAConversationOutlivesTheTerminal(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := chatSessionDir()
	if want := filepath.Join(os.Getenv("XDG_STATE_HOME"), "vera", "chat"); dir != want {
		t.Fatalf("conversations go beside verad's own files: %s, want %s", dir, want)
	}

	f := newFakeVerad(t)
	c := f.client()
	st := &Status{Name: "vera", Mind: "echo"}

	sess := openChat(t, dir, "chat-1")
	s := &chatSession{c: c, w: newFleetWatch(c), conv: "chat-1", dir: dir, open: &openSessions{list: []*session.Session{sess}}}
	m := tui.New(veraAgent{c: c}, headless(chatOptions(st, s, sess, chatGreeting(st, sess))))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	drive(t, m, func() bool { return len(sess.Turns()) == 1 }, m.Init(), say(m, "what is on the rail?"))
	deliver(m, say(m, "/tasks")) // a command line, typed, for the history
	live := screen(m)
	for _, want := range []string{"what is on the rail?", "You said:", "fleet"} {
		if !strings.Contains(live, want) {
			t.Fatalf("the live screen is missing %q:\n%s", want, live)
		}
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	// `vera chat -c chat-1`, in the next process.
	again := openChat(t, dir, "chat-1")
	if n := len(again.Turns()); n != 1 {
		t.Fatalf("%d turns on disk, want the one exchange", n)
	}
	s2 := &chatSession{c: c, w: newFleetWatch(c), conv: "chat-1", dir: dir, open: &openSessions{}}
	m2 := tui.New(veraAgent{c: c}, headless(chatOptions(st, s2, again, chatGreeting(st, again))))
	m2.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	view := screen(m2)
	for _, want := range []string{"Reopened", "chat-1", "1 turn", "what is on the rail?", "You said:", "fleet"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the rebuilt screen is missing %q:\n%s", want, view)
		}
	}
	// The input history came back with it: the up arrow finds the
	// last line that was sent, commands and all.
	m2.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if v := screen(m2); !strings.Contains(v, "/tasks") {
		t.Errorf("the up arrow found nothing:\n%s", v)
	}

	// And `vera sessions` lists it.
	list, err := session.List(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}
	var b strings.Builder
	if err := writeSessions(&b, dir, list, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "chat-1") || !strings.Contains(b.String(), dir) {
		t.Errorf("vera sessions:\n%s", b.String())
	}
	if got := sessionLines(list, "chat-1", time.Now()); !strings.Contains(got, "← this one") || !strings.Contains(got, "1 turn") {
		t.Errorf("/sessions: %q", got)
	}
}

// What the exchange cost reaches the screen: the turn's model spend
// plus what its tools reported, on the status line, and still there
// when the conversation is reopened in another process — the whole
// point of putting it on disk with the turn.
func TestTheStatusLineSaysWhatItCost(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := chatSessionDir()
	f := newFakeVerad(t)
	c := f.client()
	st := &Status{Name: "vera", Mind: "claude-opus-5"}

	sess := openChat(t, dir, "chat-1")
	s := &chatSession{c: c, w: newFleetWatch(c), conv: "chat-1", dir: dir, open: &openSessions{list: []*session.Session{sess}}}
	m := tui.New(veraAgent{c: c}, headless(chatOptions(st, s, sess, "hello")))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})

	// Nothing spent, nothing claimed: a fresh screen shows no money.
	if v := screen(m); strings.Contains(v, "$") {
		t.Fatalf("a screen with no exchange on it should say nothing about cost:\n%s", v)
	}

	drive(t, m, func() bool { return len(sess.Turns()) == 1 }, m.Init(), say(m, "what is on the rail?"))

	// The model's own spend and the tool round's, added up: $0.0395 for
	// the turn plus the $0.02 the fleet call reported.
	want := price.USD(sayUsageFrame.CostUSD + 0.02)
	view := screen(m)
	if !strings.Contains(view, want) {
		t.Errorf("the status line should show %s:\n%s", want, view)
	}
	if !strings.Contains(view, "12.8k tok") {
		t.Errorf("the status line should show the turn's tokens:\n%s", view)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopened, the total comes back with the turn.
	again := openChat(t, dir, "chat-1")
	s2 := &chatSession{c: c, w: newFleetWatch(c), conv: "chat-1", dir: dir, open: &openSessions{}}
	m2 := tui.New(veraAgent{c: c}, headless(chatOptions(st, s2, again, chatGreeting(st, again))))
	m2.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if v := screen(m2); !strings.Contains(v, want) {
		t.Errorf("a reopened conversation should still know what it cost:\n%s", v)
	}
}

// /new moves the conversation to a file of its own, and the chat's
// copy of the id is the terminal's answer, not a guess.
func TestNewMovesTheConversationAndItsFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := chatSessionDir()
	f := newFakeVerad(t)
	c := f.client()
	st := &Status{Name: "vera", Mind: "echo"}

	sess := openChat(t, dir, "chat-1")
	s := &chatSession{c: c, w: newFleetWatch(c), conv: "chat-1", dir: dir, open: &openSessions{list: []*session.Session{sess}}}
	m := tui.New(veraAgent{c: c}, headless(chatOptions(st, s, sess, "hello")))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	deliver(m, s.handle("new", ""))
	conv := m.Conversation()
	if conv == "chat-1" || !strings.HasPrefix(conv, "chat-") {
		t.Fatalf("the terminal is on %q", conv)
	}
	if s.conversation() != conv {
		t.Errorf("the chat was told %q, the terminal is on %q", s.conversation(), conv)
	}
	if n := len(s.open.list); n != 2 {
		t.Errorf("%d files open, want the old one and the new", n)
	}

	// The new conversation is written to its own file.
	drive(t, m, func() bool { return len(s.open.list[1].Turns()) == 1 }, m.Init(), say(m, "still here?"))
	if n := len(sess.Turns()); n != 0 {
		t.Errorf("the old conversation grew by %d", n)
	}
	list, err := session.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != conv {
		t.Fatalf("on disk: %+v", list) // chat-1 was never said to, so it left nothing
	}
}

// outsideRook is a test that wants the rail open: the chat starts it
// closed inside rook, and the tests are run from a terminal that may
// well be one.
func outsideRook(t *testing.T) {
	t.Helper()
	t.Setenv("ROOK_MUX_SOCK", "")
	t.Setenv("ROOK_SOCK", "")
}

// screen is the terminal's frame as plain text: v2 hands back a
// tea.View, and the tests read words, not colours.
func screen(m tea.Model) string { return ansi.Strip(m.View().Content) }

// /model <name> moves this conversation onto another one — the typed
// form, for when you know the answer and do not need the card. The
// terminal keeps no idea of its own: verad is the single writer, and
// the status line follows.
func TestModelCommandAsksVeradAndTheStatusLineFollows(t *testing.T) {
	outsideRook(t)
	f := newFakeVerad(t)
	c := f.client()
	w := newFleetWatch(c)
	s := &chatSession{c: c, w: w, conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}
	w.conv = s.conversation
	w.pollModel(context.Background())

	m := tui.New(veraAgent{c: c}, headless(chatOptions(&Status{Name: "vera"}, s, nil, "")))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})

	// What is in force is on the status line from the start, without
	// asking: the chat polls verad for it.
	if v := screen(m); !strings.Contains(v, "vera · gpt-5.6-luna") || strings.Contains(v, "· none") {
		t.Errorf("the status line should open on what is in force:\n%s", v)
	}

	// With an argument it moves, and says so with the provenance.
	deliver(m, s.handle("model", "claude-opus-5 high"))
	if v := screen(m); !strings.Contains(v, "claude-opus-5 · high") || !strings.Contains(v, "this conversation") {
		t.Errorf("/model <name> should move the conversation:\n%s", v)
	}
	if got := f.posted(); len(got) == 0 || got[len(got)-1] != "/conversations/chat-1/model" {
		t.Errorf("verad was not asked to change it: %v", got)
	}
	// And the status line is drawn from the same answer, without the
	// sentence about where it came from.
	if v := screen(m); !strings.Contains(v, "claude-opus-5 · high") || strings.Contains(v, "from profiles/") {
		t.Errorf("status line:\n%s", v)
	}

	// A model verad cannot reach is an error on the screen, not a
	// silent change to something else.
	deliver(m, s.handle("model", "no-such-model"))
	if v := screen(m); !strings.Contains(v, "cannot reach no-such-model") {
		t.Errorf("a refused model should be visible:\n%s", v)
	}
}

// /costs draws the same table `vera costs` prints, from the journal on
// disk and nothing else.
func TestCostsCommandReadsTheJournal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	w := &journal.Writer{Dir: filepath.Join(stateDir(), "conversations")}
	if err := w.Write(journal.Entry{
		At: time.Now(), Conversation: "c1", Model: "claude-opus-5", Effort: "high",
		InputTokens: 4000, OutputTokens: 200, FirstSignMs: 900,
	}); err != nil {
		t.Fatal(err)
	}

	o, err := costOptionsFrom("24h by model")
	if err != nil {
		t.Fatal(err)
	}
	if o.Since != 24*time.Hour || o.By != costs.ByModel {
		t.Fatalf("/costs 24h by model read as %+v", o)
	}
	rep, err := costs.Build(o)
	if err != nil {
		t.Fatal(err)
	}
	// 4000 input at $5/M plus 200 output at $25/M = $0.025.
	if !strings.Contains(rep.Text(), "claude-opus-5 · high") || !strings.Contains(rep.Text(), "$0.0250") {
		t.Errorf("the table:\n%s", rep.Text())
	}
	if !strings.HasPrefix(rep.Markdown(), "```") {
		t.Error("/costs shows a fenced block so the columns stay lined up")
	}
	if _, err := costOptionsFrom("last tuesday"); err == nil {
		t.Error("nonsense arguments should be an error, not a silent default")
	}
}

// Inside rook the rail is redundant: rook has an agents pane showing
// the same fleet. So it starts hidden, the greeting says so, and the
// toggle is still there — as a key and as a command, because a person
// who never presses F2 would otherwise never learn the rail exists.
func TestTheRailStartsHiddenInsideRook(t *testing.T) {
	t.Setenv("ROOK_MUX_SOCK", "")
	t.Setenv("ROOK_SOCK", "")
	if insideRook() {
		t.Fatal("no rook in the environment")
	}
	plain := chatGreeting(&Status{Name: "vera"}, emptySession(t))
	if strings.Contains(plain, "starts hidden") {
		t.Errorf("outside rook the rail is just there:\n%s", plain)
	}

	t.Setenv("ROOK_MUX_SOCK", "/tmp/rook.sock")
	if !insideRook() {
		t.Fatal("ROOK_MUX_SOCK should be enough")
	}
	inside := chatGreeting(&Status{Name: "vera"}, emptySession(t))
	for _, want := range []string{"starts hidden", "agents pane", "/rail", "F2"} {
		if !strings.Contains(inside, want) {
			t.Errorf("the greeting inside rook is missing %q:\n%s", want, inside)
		}
	}

	// mote is told at New — Options.SideClosed — rather than being sent
	// a ctrl+t as the program starts. Closed, not gone: /rail is the
	// same toggle and still brings it back.
	f := newFakeVerad(t)
	c := f.client()
	w := newFleetWatch(c)
	f.setFleet(fleet.View{Task: &fleet.Task{ID: "a1", Brief: "Port the chat onto mote"}, State: fleet.Running})
	w.poll(context.Background())
	s := &chatSession{c: c, w: w, conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}
	opts := chatOptions(&Status{Name: "vera"}, s, nil, "")
	if !opts.SideClosed {
		t.Error("inside rook the rail should start closed")
	}
	m := tui.New(veraAgent{c: c}, headless(opts))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	if strings.Contains(screen(m), "Port the chat onto mote") {
		t.Errorf("the rail should start hidden inside rook:\n%s", screen(m))
	}
	deliver(m, s.handle("rail", ""))
	if !strings.Contains(screen(m), "Port the chat onto mote") {
		t.Errorf("/rail did not bring it back:\n%s", screen(m))
	}
	deliver(m, s.handle("rail", ""))
	if strings.Contains(screen(m), "Port the chat onto mote") {
		t.Errorf("/rail did not hide it again:\n%s", screen(m))
	}

	// And outside rook there is nothing to be redundant with, so it is
	// simply there.
	t.Setenv("ROOK_MUX_SOCK", "")
	if chatOptions(&Status{Name: "vera"}, s, nil, "").SideClosed {
		t.Error("outside rook the rail is just there")
	}
}

func emptySession(t *testing.T) *session.Session {
	t.Helper()
	s, err := session.Open(t.TempDir(), "greeting")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- the model picker -----------------------------------------------------

// pickSession is a chat over a fake verad, ready to open the card.
func pickSession(t *testing.T) (*fakeVerad, *chatSession, *tui.Model) {
	t.Helper()
	outsideRook(t)
	f := newFakeVerad(t)
	c := f.client()
	w := newFleetWatch(c)
	s := &chatSession{c: c, w: w, conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}
	w.conv = s.conversation
	w.pollModel(context.Background())
	m := tui.New(veraAgent{c: c}, headless(chatOptions(&Status{Name: "vera"}, s, nil, "")))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	return f, s, m
}

// /model with no argument is the card: every model verad can reach,
// what each is reached by, what it will take, and a tick on the one
// this conversation is on. The effort is not on it — that is /effort.
func TestModelWithNoArgumentOpensTheCard(t *testing.T) {
	_, s, m := pickSession(t)
	deliver(m, s.handle("model", ""))

	v := screen(m)
	for _, want := range []string{
		"Select model",
		"gpt-5.6-terra", "effort none only", // the note the table exists for
		"gpt-5.6-luna", "the dial, via the responses API", // and the wire that lifted it
		"claude-opus-5", "via anthropic",
		"my-local-7b", "unpriced", // no price is said out loud
		"/effort sets how hard it thinks",      // the other toggle, named
		"Enter to make it Vera's default",      // the two scopes,
		"s this conversation", "Esc to cancel", // and the way out
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the card is missing %q:\n%s", want, v)
		}
	}
	// The tick is on the model in use, not on the first row.
	if !strings.Contains(v, "gpt-5.6-luna ✓") {
		t.Errorf("no tick on the model in use:\n%s", v)
	}
	// The effort dial used to live here. It does not any more: a model
	// card that restates the effort is a card that changes it by
	// accident every time you change model.
	if strings.Contains(v, "effort:") {
		t.Errorf("the model card still carries an effort dial:\n%s", v)
	}
}

// The two ways out are two scopes, and they are two different requests.
func TestThePickerPostsToTheRightEndpointForEachAction(t *testing.T) {
	f, s, m := pickSession(t)

	ans, err := f.client().models(context.Background(), "chat-1")
	if err != nil {
		t.Fatal(err)
	}
	p := s.modelPick(ans)
	opus := indexRow(t, ans.Models, "claude-opus-5")

	// `s` is this conversation and nothing else.
	deliver(m, p.OnPick(tui.PickChoice{Item: opus, Action: "s"}))
	if got := f.posted(); len(got) == 0 || got[len(got)-1] != "/conversations/chat-1/model" {
		t.Fatalf("s should move this conversation: %v", got)
	}
	if v := screen(m); !strings.Contains(v, "for this conversation") || !strings.Contains(v, "claude-opus-5") {
		t.Errorf("the note should say what changed and for what scope:\n%s", v)
	}
	// And the status line follows immediately, without waiting for a poll.
	if v := screen(m); !strings.Contains(v, "vera · claude-opus-5") {
		t.Errorf("the status line did not follow:\n%s", v)
	}

	// Enter is Vera herself, which is a different endpoint.
	deliver(m, p.OnPick(tui.PickChoice{Item: opus, Action: "enter"}))
	if got := f.posted(); len(got) == 0 || got[len(got)-1] != "/model" {
		t.Fatalf("Enter should move the daemon's own: %v", got)
	}
	if v := screen(m); !strings.Contains(v, "as Vera's default") {
		t.Errorf("the note should say the scope was the daemon:\n%s", v)
	}

	// Esc changes nothing and says nothing.
	before := len(f.posted())
	deliver(m, p.OnPick(tui.PickChoice{Item: opus, Cancelled: true}))
	if len(f.posted()) != before {
		t.Error("cancelling sent something")
	}
}

// Choosing a model says nothing about the effort: the request carries
// an empty one, which is verad's word for "leave it where it is".
func TestTheModelCardSendsNoEffort(t *testing.T) {
	f, s, m := pickSession(t)
	ans, err := f.client().models(context.Background(), "chat-1")
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.client().chooseModel(context.Background(), "chat-1", "claude-opus-5", "high")
	if err != nil {
		t.Fatal(err)
	}
	if res.Effort != "high" {
		t.Fatalf("setting both should set both: %+v", res)
	}
	// Now the card, which names a model and nothing else.
	p := s.modelPick(ans)
	deliver(m, p.OnPick(tui.PickChoice{Item: indexRow(t, ans.Models, "gpt-5"), Action: "s"}))
	again, err := f.client().model(context.Background(), "chat-1")
	if err != nil {
		t.Fatal(err)
	}
	if again.Model != "gpt-5" || again.Effort != "high" {
		t.Errorf("the model card moved the effort as well: %+v", again)
	}
}

// /effort is the toggle: three positions, whatever model is answering,
// with a tick on the one in force.
func TestEffortWithNoArgumentOpensTheToggle(t *testing.T) {
	_, s, m := pickSession(t)
	// The built-in default has no dial at all, so move onto one that
	// does first — the card is about the model in force.
	deliver(m, s.handle("model", "claude-opus-5 medium"))
	deliver(m, s.handle("effort", ""))

	v := screen(m)
	for _, want := range []string{
		"Reasoning effort", "claude-opus-5",
		"low", "medium", "high",
		"answers soonest", "thinks longest before answering",
		"Enter to make it Vera's default", "s this conversation", "Esc to cancel",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the toggle is missing %q:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "medium ✓") {
		t.Errorf("no tick on the effort in force:\n%s", v)
	}
	// Three positions, like Claude Code. opus takes max as well, and
	// the card says how to reach it rather than growing a fourth.
	if !strings.Contains(v, "/effort max") {
		t.Errorf("the way to an effort off the toggle is not said:\n%s", v)
	}
	if strings.Count(v, "max") != 1 {
		t.Errorf("the toggle grew a fourth position:\n%s", v)
	}
}

// A model with no dial gets the fact, not an empty card and not three
// options verad would refuse one at a time — and the fact is that the
// control is ABSENT, not that it is set to "none". Three parts:
// what cannot be done, what that leaves alone, what to do instead.
func TestEffortSaysSoWhenTheModelHasNoDial(t *testing.T) {
	f, s, m := pickSession(t)
	f.dflt = &InForce{Model: "gpt-5.6-terra", Effort: "none", From: "the built-in default"}
	deliver(m, s.handle("effort", ""))

	v := screen(m)
	if strings.Contains(v, "Reasoning effort") {
		t.Errorf("a card was drawn for a model with no dial:\n%s", v)
	}
	if !strings.Contains(v, "gpt-5.6-terra does not expose a reasoning-effort control") {
		t.Errorf("it should say the control is not there:\n%s", v)
	}
	if !strings.Contains(v, "model and settings unchanged") || !strings.Contains(v, "/model shows what does") {
		t.Errorf("a refusal says what it left alone and what to do:\n%s", v)
	}
	if strings.Contains(v, "it takes effort none") {
		t.Errorf("none is not a position of a dial this model has:\n%s", v)
	}
}

// And a model that does have one gets the card, which is the half that
// was wrong: luna is reached through the responses API, where the dial
// survives having tools in the request.
func TestEffortDrawsTheCardForAModelWithADial(t *testing.T) {
	_, s, m := pickSession(t)
	deliver(m, s.handle("effort", ""))

	v := screen(m)
	if !strings.Contains(v, "Reasoning effort") {
		t.Fatalf("no card for gpt-5.6-luna, which takes the dial:\n%s", v)
	}
	for _, want := range []string{"low", "medium", "high"} {
		if !strings.Contains(v, want) {
			t.Errorf("the card is missing %q:\n%s", want, v)
		}
	}
}

// The toggle is the two scopes the model card has, and it moves the
// effort without touching the model.
func TestTheEffortToggleMovesOnlyTheEffort(t *testing.T) {
	f, s, m := pickSession(t)
	deliver(m, s.handle("model", "claude-opus-5 low"))

	ans, err := f.client().models(context.Background(), "chat-1")
	if err != nil {
		t.Fatal(err)
	}
	dial := effortsFor(ans)
	if strings.Join(dial, ",") != "low,medium,high" {
		t.Fatalf("the toggle should be the three: %v", dial)
	}
	p := s.effortPick(ans, dial)

	deliver(m, p.OnPick(tui.PickChoice{Item: indexDial(t, dial, "high"), Action: "s"}))
	if got := f.posted(); len(got) == 0 || got[len(got)-1] != "/conversations/chat-1/model" {
		t.Fatalf("s should move this conversation: %v", got)
	}
	now, err := f.client().model(context.Background(), "chat-1")
	if err != nil {
		t.Fatal(err)
	}
	if now.Model != "claude-opus-5" || now.Effort != "high" {
		t.Errorf("the toggle should move the effort and nothing else: %+v", now)
	}
	if v := screen(m); !strings.Contains(v, "claude-opus-5 · high") {
		t.Errorf("the status line did not follow:\n%s", v)
	}

	// Enter is the daemon's own, and a different endpoint.
	deliver(m, p.OnPick(tui.PickChoice{Item: indexDial(t, dial, "low"), Action: "enter"}))
	if got := f.posted(); len(got) == 0 || got[len(got)-1] != "/model" {
		t.Fatalf("Enter should move the daemon's own: %v", got)
	}
	if v := screen(m); !strings.Contains(v, "as Vera's default") {
		t.Errorf("the note should say the scope was the daemon:\n%s", v)
	}
}

// /effort <level> is the typed form, and it moves this conversation the
// way /model <name> does.
func TestEffortTypedMovesThisConversation(t *testing.T) {
	f, s, m := pickSession(t)
	deliver(m, s.handle("model", "claude-opus-5"))
	deliver(m, s.handle("effort", "high"))

	if got := f.posted(); len(got) == 0 || got[len(got)-1] != "/conversations/chat-1/model" {
		t.Fatalf("verad was not asked to change it: %v", got)
	}
	if v := screen(m); !strings.Contains(v, "claude-opus-5 · high") || !strings.Contains(v, "this conversation") {
		t.Errorf("/effort <level> should move the conversation:\n%s", v)
	}
}

// A verad too old to list them can still say what this conversation is
// on, and that is worth more than an error about a missing route.
func TestAVeradWithNoModelsRouteStillAnswers(t *testing.T) {
	outsideRook(t)
	f := newFakeVerad(t)
	c := f.client()
	w := newFleetWatch(c)
	s := &chatSession{c: c, w: w, conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}
	w.conv = s.conversation
	w.pollModel(context.Background())
	m := tui.New(veraAgent{c: c}, headless(chatOptions(&Status{Name: "vera"}, s, nil, "")))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})

	// The fake's /models is there; the old one's is not.
	old := &chatClient{base: f.url + "/nowhere", secret: f.id.Secret, device: f.id.Name}
	s.c = old
	deliver(m, s.handle("model", ""))
	if v := screen(m); strings.Contains(v, "Select model") {
		t.Errorf("a card was drawn from nothing:\n%s", v)
	}
}

func indexRow(t *testing.T, rows []ModelRow, name string) int {
	t.Helper()
	for i, r := range rows {
		if r.Name == name {
			return i
		}
	}
	t.Fatalf("no row %q", name)
	return -1
}

func indexDial(t *testing.T, dial []string, effort string) int {
	t.Helper()
	for i, e := range dial {
		if e == effort {
			return i
		}
	}
	t.Fatalf("no %q on the dial %v", effort, dial)
	return -1
}

// The model can move without this terminal moving it — a `vera say -m`
// in another window, a `/model` on the phone. verad is the authority
// and the poll is how the status line hears about it, so what it hears
// goes back to mote as a message rather than waiting for somebody here
// to type.
func TestTheWatchPushesAModelThatMovedElsewhere(t *testing.T) {
	outsideRook(t)
	f := newFakeVerad(t)
	c := f.client()
	w := newFleetWatch(c)
	s := &chatSession{c: c, w: w, conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}
	w.conv = s.conversation
	w.pollModel(context.Background())
	m := tui.New(veraAgent{c: c}, headless(chatOptions(&Status{Name: "vera"}, s, nil, "")))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})

	var pushed []tea.Msg
	w.sendWith(func(msg tea.Msg) { pushed = append(pushed, msg) })

	// Nothing moved: nothing is said. A message per poll would redraw
	// the screen every few seconds for no reason.
	w.pollModel(context.Background())
	if len(pushed) != 0 {
		t.Fatalf("a poll that changed nothing pushed %d message(s)", len(pushed))
	}

	// Somebody else moved it.
	f.mu.Lock()
	f.model = Resolution{Model: "claude-opus-5", Effort: "max", Provider: "anthropic",
		ModelFrom: "this conversation", EffortFrom: "this conversation"}
	f.mu.Unlock()
	w.pollModel(context.Background())
	if len(pushed) != 1 {
		t.Fatalf("wanted one message, got %d", len(pushed))
	}
	for _, msg := range pushed {
		m.Update(msg)
	}
	if v := screen(m); !strings.Contains(v, "vera · claude-opus-5 · max") {
		t.Errorf("the status line did not follow verad:\n%s", v)
	}
}

// A backslash and then enter breaks the line in the chat's box rather
// than sending it. It is the newline that survives a terminal which
// reports alt+enter and shift+enter as a plain enter — the two-line
// question is one question, and the backslash was the instruction
// rather than part of it, so it is not in what verad is told.
func TestBackslashEnterIsANewlineNotASend(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := chatSessionDir()
	f := newFakeVerad(t)
	c := f.client()
	st := &Status{Name: "vera", Mind: "echo"}

	sess := openChat(t, dir, "chat-1")
	s := &chatSession{c: c, w: newFleetWatch(c), conv: "chat-1", dir: dir, open: &openSessions{list: []*session.Session{sess}}}
	m := tui.New(veraAgent{c: c}, headless(chatOptions(st, s, sess, "hello")))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	m.Update(tea.KeyPressMsg{Code: '\\', Text: "what is on the rail,\\"})
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("a backslash and enter should have broken the line, not started an exchange")
	}
	if n := len(sess.Turns()); n != 0 {
		t.Fatalf("nothing should have been sent yet, the session has %d turns", n)
	}

	// The second line, and the plain enter that does send.
	m.Update(tea.KeyPressMsg{Code: 'a', Text: "and what is blocked?"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	drive(t, m, func() bool { return len(sess.Turns()) == 1 }, m.Init(), cmd)

	want := "what is on the rail,\nand what is blocked?"
	if got := sess.Turns()[0].Said; got != want {
		t.Errorf("verad was told %q, want %q", got, want)
	}
}

// A fleet verb that would not run says the three things every refusal
// here says: what failed, what it left alone, and what to do. The
// middle one is the one that matters — a /land that failed and one
// that half-landed look identical from the keyboard.
func TestARefusedFleetVerbSaysTheTaskIsWhereItWas(t *testing.T) {
	outsideRook(t)
	f := newFakeVerad(t)
	f.refuse = "the branch has a conflict in README"
	c := f.client()
	w := newFleetWatch(c)
	s := &chatSession{c: c, w: w, conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}
	w.conv = s.conversation

	got := joined(s.handle("land", "a1"))
	for _, want := range []string{"conflict in README", "the task is where it was", "/tasks"} {
		if !strings.Contains(got, want) {
			t.Errorf("a refused /land is missing %q:\n%s", want, got)
		}
	}
}
