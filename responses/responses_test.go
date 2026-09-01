package responses

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/incantery/mote/provider"
	"github.com/incantery/mote/tool"
)

// endpoint is a /responses that records what it was asked and answers
// with the events a test names.
type endpoint struct {
	*httptest.Server
	body   map[string]any
	status int
	send   string
}

func serve(t *testing.T, events ...string) *endpoint {
	t.Helper()
	e := &endpoint{status: http.StatusOK, send: strings.Join(events, "")}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			t.Errorf("asked %s, not /responses", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &e.body); err != nil {
			t.Errorf("body: %v", err)
		}
		w.WriteHeader(e.status)
		_, _ = io.WriteString(w, e.send)
	}))
	t.Cleanup(e.Close)
	return e
}

// sse is one event as the wire writes it. Only the data line is read —
// every payload names its own type — but the event line is sent too,
// because that is what arrives and a reader that trips over it should
// trip over it here.
func sse(kind, payload string) string {
	return "event: " + kind + "\ndata: " + payload + "\n\n"
}

func run(t *testing.T, e *endpoint, req provider.Request) ([]provider.Event, provider.Usage, error) {
	t.Helper()
	var got []provider.Event
	used, err := New(e.URL, "sk-test").Stream(context.Background(), req, func(ev provider.Event) {
		got = append(got, ev)
	})
	return got, used, err
}

func completed(usage string) string {
	return sse("response.completed",
		`{"type":"response.completed","response":{"model":"gpt-5.6-luna-2026-08-01","status":"completed",`+usage+`}}`)
}

const someUsage = `"usage":{"input_tokens":1200,"input_tokens_details":{"cached_tokens":1000},"output_tokens":42}`

// What the whole wire exists for: the reasoning dial and function tools
// in the same request, which is the pair /chat/completions refuses.
func TestTheDialAndTheToolsTravelTogether(t *testing.T) {
	e := serve(t, completed(someUsage))
	_, _, err := run(t, e, provider.Request{
		Model:    "gpt-5.6-luna",
		System:   "be brief",
		Effort:   provider.EffortHigh,
		Messages: []provider.Message{provider.User("what changed?")},
		Tools: []tool.Definition{{Type: "function", Function: tool.Function{
			Name: "read", Description: "read a file",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := e.body["reasoning"].(map[string]any); got["effort"] != "high" || got["summary"] != "auto" {
		t.Errorf("reasoning: %v", got)
	}
	if e.body["instructions"] != "be brief" {
		t.Errorf("instructions: %v", e.body["instructions"])
	}
	// The tool is flat here: chat completions nests the name and the
	// schema under "function", and this endpoint 400s on that.
	tools := e.body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools: %v", tools)
	}
	first := tools[0].(map[string]any)
	if first["type"] != "function" || first["name"] != "read" || first["parameters"] == nil {
		t.Errorf("the tool is not in this endpoint's shape: %v", first)
	}
	if _, nested := first["function"]; nested {
		t.Error("the tool went out in the chat-completions shape")
	}
	// Nothing is left on somebody else's disk, and the reasoning comes
	// back in a form that can be handed over again.
	if e.body["store"] != false {
		t.Errorf("store: %v", e.body["store"])
	}
	if inc := e.body["include"].([]any); len(inc) != 1 || inc[0] != "reasoning.encrypted_content" {
		t.Errorf("include: %v", inc)
	}
}

// The dial's words, and the two cases where the request says nothing.
func TestWhatTheDialSends(t *testing.T) {
	for _, c := range []struct {
		effort  provider.Effort
		display provider.Display
		want    any
	}{
		{effort: "", want: nil},
		{effort: "none", want: map[string]any{"effort": "none"}},
		{effort: provider.EffortMedium, want: map[string]any{"effort": "medium", "summary": "auto"}},
		// There is no word above high here, and working slightly less
		// hard beats refusing the request.
		{effort: provider.EffortMax, want: map[string]any{"effort": "high", "summary": "auto"}},
		// Asked not to be shown the working: still thinking, still
		// signing it, nothing to read.
		{effort: provider.EffortHigh, display: provider.DisplayOmitted,
			want: map[string]any{"effort": "high"}},
	} {
		t.Run(string(c.effort)+"/"+string(c.display), func(t *testing.T) {
			e := serve(t, completed(someUsage))
			if _, _, err := run(t, e, provider.Request{
				Effort: c.effort, ThinkingDisplay: c.display,
				Messages: []provider.Message{provider.User("hello")},
			}); err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(e.body["reasoning"])
			want, _ := json.Marshal(c.want)
			if string(got) != string(want) {
				t.Errorf("reasoning: %s, want %s", got, want)
			}
		})
	}
}

// The stream, whole: words, the working, a tool call taken from the
// item that finished, and what it cost — with the cached tokens counted
// once rather than twice.
func TestTheStreamComesBackInOnePiece(t *testing.T) {
	e := serve(t,
		sse("response.output_text.delta", `{"type":"response.output_text.delta","delta":"Hel"}`),
		sse("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","delta":"thinking"}`),
		sse("response.output_text.delta", `{"type":"response.output_text.delta","delta":"lo."}`),
		sse("response.output_item.done",
			`{"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC"}}`),
		sse("response.output_item.done",
			`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"path\":\"a.txt\"}"}}`),
		completed(someUsage),
	)
	got, used, err := run(t, e, provider.Request{Effort: provider.EffortHigh,
		Messages: []provider.Message{provider.User("read a.txt")}})
	if err != nil {
		t.Fatal(err)
	}

	var said, thought strings.Builder
	var calls []provider.Call
	var kept json.RawMessage
	for _, ev := range got {
		switch ev.Kind {
		case provider.KindDelta:
			said.WriteString(ev.Text)
		case provider.KindThinking:
			thought.WriteString(ev.Text)
		case provider.KindToolCall:
			calls = append(calls, ev.Call)
		case provider.KindRaw:
			kept = ev.Raw
		}
	}
	if said.String() != "Hello." || thought.String() != "thinking" {
		t.Errorf("said %q, thought %q", said.String(), thought.String())
	}
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "read" {
		t.Fatalf("calls: %+v", calls)
	}
	if calls[0].Arguments != `{"path":"a.txt"}` {
		t.Errorf("arguments: %s", calls[0].Arguments)
	}
	// A tool result quotes call_id back, not the item's own id.
	if strings.Contains(string(kept), "call_1") {
		t.Errorf("the call went into the kept reasoning: %s", kept)
	}
	if !strings.Contains(string(kept), "ENC") || !strings.Contains(string(kept), "rs_1") {
		t.Errorf("kept: %s", kept)
	}
	// The raw record arrives last, once, after the calls it led to.
	if got[len(got)-1].Kind != provider.KindRaw {
		t.Errorf("the kept reasoning is not last: %v", got[len(got)-1].Kind)
	}

	if used.Input != 200 || used.CacheRead != 1000 || used.Output != 42 {
		t.Errorf("usage: %+v — input_tokens counts the cached ones, Usage says each once", used)
	}
	if used.Model != "gpt-5.6-luna-2026-08-01" || used.StopReason != "completed" {
		t.Errorf("usage: %+v", used)
	}
}

// The round trip that stops the second round from starting the thought
// again from nothing: what the turn kept goes back in front of the call
// it led to, byte for byte.
func TestTheKeptReasoningGoesBackInFrontOfTheCall(t *testing.T) {
	e := serve(t, completed(someUsage))
	was := provider.Assistant("one moment", provider.Call{ID: "call_1", Name: "read", Arguments: `{"path":"a.txt"}`})
	was.Raw = json.RawMessage(`[{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC"}]`)

	if _, _, err := run(t, e, provider.Request{Effort: provider.EffortHigh, Messages: []provider.Message{
		provider.User("read a.txt"),
		was,
		provider.Answer("call_1", "hello"),
	}}); err != nil {
		t.Fatal(err)
	}

	items := e.body["input"].([]any)
	var kinds []string
	for _, it := range items {
		kinds = append(kinds, it.(map[string]any)["type"].(string))
	}
	want := "message,reasoning,message,function_call,function_call_output"
	if strings.Join(kinds, ",") != want {
		t.Fatalf("input items: %v, want %s", kinds, want)
	}
	// Whole, and not rewritten: the id, the empty summary this endpoint
	// insists on, and the block only it can read.
	kept := items[1].(map[string]any)
	if kept["id"] != "rs_1" || kept["encrypted_content"] != "ENC" {
		t.Errorf("the kept reasoning was rewritten on the way out: %v", kept)
	}
	if _, ok := kept["summary"].([]any); !ok {
		t.Errorf("summary is required even when it is empty: %v", kept["summary"])
	}
	// A tool result quotes the call id.
	if got := items[4].(map[string]any); got["call_id"] != "call_1" || got["output"] != "hello" {
		t.Errorf("tool result: %v", got)
	}
}

// Raw from somebody else's provider, or a session file that was edited,
// costs the reasoning and not the turn.
func TestRawItCannotReadIsDroppedRatherThanSent(t *testing.T) {
	e := serve(t, completed(someUsage))
	was := provider.Assistant("hello")
	was.Raw = json.RawMessage(`[{"type":"thinking","signature":"anthropic's"}]`)

	if _, _, err := run(t, e, provider.Request{Messages: []provider.Message{was}}); err != nil {
		t.Fatal(err)
	}
	items := e.body["input"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["type"] != "message" {
		t.Errorf("input items: %v", items)
	}
}

// A refusal happened and was paid for, so it is an event and not
// Stream's error. A failure is the other way round.
func TestARefusalIsSaidAndAFailureStops(t *testing.T) {
	e := serve(t,
		sse("response.refusal.done", `{"type":"response.refusal.done","refusal":"I cannot help with that."}`),
		completed(someUsage))
	got, used, err := run(t, e, provider.Request{Messages: []provider.Message{provider.User("go")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != provider.KindError || got[0].Text != "I cannot help with that." {
		t.Fatalf("events: %+v", got)
	}
	if used.Output != 42 {
		t.Errorf("a refusal is still paid for: %+v", used)
	}

	e = serve(t, sse("response.failed",
		`{"type":"response.failed","response":{"status":"failed","error":{"message":"the server had a moment"}}}`))
	if _, _, err := run(t, e, provider.Request{Messages: []provider.Message{provider.User("go")}}); err == nil ||
		!strings.Contains(err.Error(), "the server had a moment") {
		t.Errorf("a failed response: %v", err)
	}
}

// What the endpoint said when it refused the request, in the error,
// because that sentence is the whole debugging session.
func TestTheEndpointsOwnWordsSurviveAnError(t *testing.T) {
	e := serve(t)
	e.status = http.StatusBadRequest
	e.send = `{"error":{"message":"Unsupported parameter: reasoning.effort"}}`
	_, _, err := run(t, e, provider.Request{Effort: provider.EffortHigh,
		Messages: []provider.Message{provider.User("go")}})
	if err == nil || !strings.Contains(err.Error(), "Unsupported parameter") || !strings.Contains(err.Error(), "400") {
		t.Errorf("error: %v", err)
	}
}

// An incomplete turn says why in the API's own word rather than in one
// invented here.
func TestAnIncompleteTurnSaysWhy(t *testing.T) {
	e := serve(t, sse("response.incomplete",
		`{"type":"response.incomplete","response":{"model":"gpt-5.6-luna","status":"incomplete",`+
			`"incomplete_details":{"reason":"max_output_tokens"},`+someUsage+`}}`))
	_, used, err := run(t, e, provider.Request{Messages: []provider.Message{provider.User("go")}})
	if err != nil {
		t.Fatal(err)
	}
	if used.StopReason != "max_output_tokens" {
		t.Errorf("stop reason: %q", used.StopReason)
	}
}
