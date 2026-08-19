package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAConversationRemembersItsOwnTurns(t *testing.T) {
	h := newHistory()
	h.remember("c1", "my name is Seth", "Nice to meet you, Seth.")
	h.remember("c1", "what is my name", "Seth.")

	got := h.recall("c1")
	if len(got) != 4 {
		t.Fatalf("recalled %d turns, want 4", len(got))
	}
	if got[0].Role != "user" || got[1].Role != "assistant" {
		t.Fatalf("turns are out of order: %+v", got[:2])
	}
	// Conversations do not leak into each other.
	if len(h.recall("c2")) != 0 {
		t.Fatal("a different conversation saw these turns")
	}
	// And no id at all means no conversation, which is what curl gets.
	if len(h.recall("")) != 0 {
		t.Fatal("an empty id recalled something")
	}
}

func TestAFailedExchangeIsNotRemembered(t *testing.T) {
	h := newHistory()
	h.remember("c1", "a question", "") // the model never answered
	if n := h.size("c1"); n != 0 {
		t.Fatalf("stored %d turns for an exchange that failed", n)
	}
	// Otherwise the next call sends two user messages in a row, and the
	// model reads that as the person repeating themselves.
}

func TestTheWindowDropsWholeExchanges(t *testing.T) {
	h := newHistory()
	h.maxTurns = 4
	for i := range 5 {
		h.remember("c1", fmt.Sprintf("question %d", i), fmt.Sprintf("answer %d", i))
	}

	got := h.recall("c1")
	if len(got) != 4 {
		t.Fatalf("kept %d turns, want 4", len(got))
	}
	// The tail is what survives, and it still begins with a question.
	if got[0].Role != "user" {
		t.Fatalf("window starts on %q — an answer to a question that is gone", got[0].Role)
	}
	if !strings.Contains(got[len(got)-1].Content, "answer 4") {
		t.Fatalf("the most recent exchange was dropped instead of the oldest: %+v", got)
	}
}

func TestALongConversationStopsGrowing(t *testing.T) {
	h := newHistory()
	h.maxChars = 200
	long := strings.Repeat("x", 150)
	for range 10 {
		h.remember("c1", long, long)
	}
	if chars := h.threads["c1"].chars(); chars > h.maxChars+2*len(long) {
		t.Fatalf("conversation grew to %d chars — every exchange resends all of it", chars)
	}
}

func TestAbandonedConversationsAreDropped(t *testing.T) {
	h := newHistory()
	h.idleFor = time.Millisecond
	h.remember("old", "hello", "hi")
	time.Sleep(5 * time.Millisecond)
	h.remember("new", "hello", "hi") // creating a thread sweeps

	if h.size("old") != 0 {
		t.Fatal("an abandoned conversation is still held")
	}
	if h.size("new") == 0 {
		t.Fatal("the sweep took the conversation that caused it")
	}
}

// The point of all of it: the second question can refer to the first.
func TestPriorTurnsReachTheModel(t *testing.T) {
	var sent [][]map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]string `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent = append(sent, body.Messages)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	mind := &Mind{Client: srv.Client(), Base: srv.URL, Model: "m",
		History: newHistory(), instruments: newInstruments()}
	swallow := func(Frame) error { return nil }

	if err := mind.think(context.Background(), Message{Text: "my name is Seth", Conversation: "c"}, swallow); err != nil {
		t.Fatal(err)
	}
	if err := mind.think(context.Background(), Message{Text: "what is my name", Conversation: "c"}, swallow); err != nil {
		t.Fatal(err)
	}

	if len(sent) != 2 {
		t.Fatalf("made %d calls", len(sent))
	}
	// First call: system + the question. Second: system + both halves
	// of the first exchange + the new question.
	if len(sent[0]) != 2 {
		t.Fatalf("first call carried %d messages, want 2", len(sent[0]))
	}
	if len(sent[1]) != 4 {
		t.Fatalf("second call carried %d messages, want 4 — history did not reach the model", len(sent[1]))
	}
	if sent[1][1]["content"] != "my name is Seth" {
		t.Fatalf("the second call did not carry the first question: %+v", sent[1])
	}

	// A different conversation on the same server starts clean.
	if err := mind.think(context.Background(), Message{Text: "hello", Conversation: "other"}, swallow); err != nil {
		t.Fatal(err)
	}
	if len(sent[2]) != 2 {
		t.Fatalf("a new conversation inherited %d messages", len(sent[2]))
	}
}
