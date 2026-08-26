package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/incantery/vera/home"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// sse serves a canned server-sent-event stream the way the API does.
func sse(t *testing.T, chunks ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestStreamReassemblesTheAnswer(t *testing.T) {
	srv := sse(t,
		`{"model":"m-1","choices":[{"delta":{"content":"Hello"}}]}`,
		`{"model":"m-1","choices":[{"delta":{"content":", world"}}]}`,
		`{"model":"m-1","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":4}}`,
	)
	defer srv.Close()

	mind := &Mind{Client: srv.Client(), Base: srv.URL, Model: "m-1", instruments: newInstruments()}

	var got strings.Builder
	var used usage
	if _, err := mind.stream(context.Background(),
		[]chatMessage{{Role: "user", Content: "hi"}}, nil,
		func(d string) error {
			got.WriteString(d)
			return nil
		}, &used); err != nil {
		t.Fatal(err)
	}

	if got.String() != "Hello, world" {
		t.Fatalf("reassembled %q", got.String())
	}
	// Without stream_options the usage chunk never arrives, and the
	// cost of every exchange silently reads as zero.
	if used.Prompt != 11 || used.Completion != 4 {
		t.Fatalf("usage was %+v — token counts did not survive the stream", used)
	}
	if used.Model != "m-1" {
		t.Fatalf("response model was %q", used.Model)
	}
}

func TestUsageIsRequestedOrItNeverArrives(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		body = string(b[:n])
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	mind := &Mind{Client: srv.Client(), Base: srv.URL, Model: "m", instruments: newInstruments()}
	_, _ = mind.stream(context.Background(),
		[]chatMessage{{Role: "user", Content: "hi"}}, nil,
		func(string) error { return nil }, &usage{})

	if !strings.Contains(body, "include_usage") {
		t.Fatalf("the request does not ask for usage, so tokens will always read zero:\n%s", body)
	}
}

// The one that matters for a bill: a metric label is a time series
// forever, so a conversation id must never become one.
func TestMetricsCarryNoUnboundedLabels(t *testing.T) {
	reader := metric.NewManualReader()
	otel.SetMeterProvider(metric.NewMeterProvider(metric.WithReader(reader)))

	srv := sse(t,
		`{"model":"m","choices":[{"delta":{"content":"ok"}}]}`,
		`{"model":"m","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1}}`,
	)
	defer srv.Close()

	mind := &Mind{Client: srv.Client(), Base: srv.URL, Model: "m", History: newHistory(), instruments: newInstruments()}
	err := mind.think(context.Background(),
		Message{Text: "hello", Conversation: "conversation-that-is-unique-forever"},
		func(Frame) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}

	var found int
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			found++
			for _, attrs := range attributeSets(m) {
				for _, kv := range attrs {
					switch string(kv.Key) {
					case "gen_ai.conversation.id", "gen_ai.prompt", "gen_ai.completion",
						"gen_ai.input.messages", "gen_ai.output.messages":
						t.Fatalf("%s carries %s as a label — that is a new time series per conversation",
							m.Name, kv.Key)
					}
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("no metrics were recorded at all")
	}
}

// attributeSets pulls the label sets out of whichever aggregation the
// instrument happened to use.
type attributeKV = attribute.KeyValue

func kvs(in []attribute.KeyValue) []attributeKV { return in }

func attributeSets(m metricdata.Metrics) [][]attributeKV {
	var out [][]attributeKV
	switch data := m.Data.(type) {
	case metricdata.Histogram[float64]:
		for _, p := range data.DataPoints {
			out = append(out, kvs(p.Attributes.ToSlice()))
		}
	case metricdata.Histogram[int64]:
		for _, p := range data.DataPoints {
			out = append(out, kvs(p.Attributes.ToSlice()))
		}
	}
	return out
}

// What the prompt carries is MEMORY.md, whole, under the heading it
// has always had — the wording is what the evals were written
// against, and memory becoming a directory should not have moved it.
func TestThePromptCarriesTheIndex(t *testing.T) {
	place, err := home.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &Mind{Memory: place.Memory()}

	// Nothing known: no section at all, rather than an empty heading
	// the model has to interpret.
	if got := m.preface(); strings.Contains(got, "What you know about them") {
		t.Fatalf("an empty memory still added a section:\n%s", got)
	}

	place.Memory().Apply(home.Revision{Add: []home.Note{
		{Name: "lives-in-vienna", Type: home.TypeUser, Fact: "Lives in Vienna."},
	}}, "c1")

	got := m.preface()
	if !strings.Contains(got, "What you know about them, from earlier conversations:") {
		t.Fatalf("the heading changed:\n%s", got)
	}
	if !strings.Contains(got, "Lives in Vienna.") {
		t.Fatalf("the fact did not reach the prompt:\n%s", got)
	}
	if !strings.Contains(got, "Do not mention it, list it, or bring it up unprompted.") {
		t.Fatalf("the instruction not to perform its memory went missing:\n%s", got)
	}
	if !strings.HasPrefix(got, voice) {
		t.Fatal("memory displaced the system prompt rather than following it")
	}
}
