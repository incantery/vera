package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/incantery/mote/provider"
)

func TestAConversationRemembersItsOwnTurns(t *testing.T) {
	h := newHistory()
	h.remember("c1", "my name is Seth", "Nice to meet you, Seth.", nil, "m")
	h.remember("c1", "what is my name", "Seth.", nil, "m")

	got := h.recall("c1", "m")
	if len(got) != 4 {
		t.Fatalf("recalled %d turns, want 4", len(got))
	}
	if got[0].Role != "user" || got[1].Role != "assistant" {
		t.Fatalf("turns are out of order: %+v", got[:2])
	}
	// Conversations do not leak into each other.
	if len(h.recall("c2", "m")) != 0 {
		t.Fatal("a different conversation saw these turns")
	}
	// And no id at all means no conversation, which is what curl gets.
	if len(h.recall("", "m")) != 0 {
		t.Fatal("an empty id recalled something")
	}
}

func TestAFailedExchangeIsNotRemembered(t *testing.T) {
	h := newHistory()
	h.remember("c1", "a question", "", nil, "m") // the model never answered
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
		h.remember("c1", fmt.Sprintf("question %d", i), fmt.Sprintf("answer %d", i), nil, "m")
	}

	got := h.recall("c1", "m")
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
		h.remember("c1", long, long, nil, "m")
	}
	if chars := h.threads["c1"].chars(); chars > h.maxChars+2*len(long) {
		t.Fatalf("conversation grew to %d chars — every exchange resends all of it", chars)
	}
}

func TestAbandonedConversationsAreDropped(t *testing.T) {
	h := newHistory()
	h.idleFor = time.Millisecond
	h.remember("old", "hello", "hi", nil, "m")
	time.Sleep(5 * time.Millisecond)
	h.remember("new", "hello", "hi", nil, "m") // creating a thread sweeps

	if h.size("old") != 0 {
		t.Fatal("an abandoned conversation is still held")
	}
	if h.size("new") == 0 {
		t.Fatal("the sweep took the conversation that caused it")
	}
}

// The point of all of it: the second question can refer to the first.
func TestPriorTurnsReachTheModel(t *testing.T) {
	model := scripted(t, says("ok"), says("ok"), says("ok"))
	mind := &Mind{Provider: model, Model: "m",
		History: newHistory(), instruments: newInstruments()}
	swallow := func(Frame) error { return nil }

	if err := mind.think(context.Background(), Message{Text: "my name is Seth", Conversation: "c"}, swallow); err != nil {
		t.Fatal(err)
	}
	if err := mind.think(context.Background(), Message{Text: "what is my name", Conversation: "c"}, swallow); err != nil {
		t.Fatal(err)
	}

	if model.rounds() != 2 {
		t.Fatalf("made %d calls", model.rounds())
	}
	// The system prompt is a field of the request now rather than the
	// first message, so a first call carries the question alone, and a
	// second carries both halves of the first exchange in front of it.
	if n := len(model.asked(0).Messages); n != 1 {
		t.Fatalf("first call carried %d messages, want 1", n)
	}
	if model.asked(0).System == "" {
		t.Fatal("the system prompt did not reach the model at all")
	}
	second := model.asked(1).Messages
	if len(second) != 3 {
		t.Fatalf("second call carried %d messages, want 3 — history did not reach the model", len(second))
	}
	if second[0].Text != "my name is Seth" || second[0].Role != provider.RoleUser {
		t.Fatalf("the second call did not carry the first question: %+v", second)
	}
	if second[1].Role != provider.RoleAssistant {
		t.Fatalf("the answer came back as %q, not an assistant turn", second[1].Role)
	}

	// A different conversation on the same model starts clean.
	if err := mind.think(context.Background(), Message{Text: "hello", Conversation: "other"}, swallow); err != nil {
		t.Fatal(err)
	}
	if n := len(model.asked(2).Messages); n != 1 {
		t.Fatalf("a new conversation inherited %d messages", n)
	}
}
