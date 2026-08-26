package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/incantery/mote/provider"
)

// What a model kept of how it reasoned goes back to it unread — once
// inside an exchange, in front of the tool call it led to, and again
// on the next exchange in the same conversation.
//
// It is opaque here on purpose: these tests never look inside a Raw,
// only at whether the same bytes came back to the same model.

// thinksAndCalls is a round in which the model reasons, asks for a
// tool, and hands out its record of the turn — which is the shape the
// Messages API is strict about.
func thinksAndCalls(t *testing.T, id, name string, args any, raw string) scriptRound {
	t.Helper()
	r := callsTool(t, id, name, args)
	r.events = append([]provider.Event{provider.Thought("hmm")}, r.events...)
	r.events = append(r.events, provider.Keeping(json.RawMessage(raw)))
	return r
}

// saysAndKeeps is an answer that also kept something.
func saysAndKeeps(text, raw string) scriptRound {
	r := says(text)
	r.events = append(r.events, provider.Keeping(json.RawMessage(raw)))
	return r
}

// Inside one exchange: the assistant turn that carries the tool call
// carries the reasoning that led to it. Without this the second round
// of every tool exchange against a thinking model is a 400.
func TestTheTurnThatCalledAToolHandsBackItsThinking(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	const kept = `[{"type":"thinking","signature":"sig-1"}]`
	mind, _, _ := askingMind(t,
		thinksAndCalls(t, "call_r", "read", map[string]any{"path": target}, kept),
		says("It says hello."))

	if err := mind.think(context.Background(), Message{Text: "what is in it", Conversation: "c"},
		func(Frame) error { return nil }); err != nil {
		t.Fatal(err)
	}

	model := mind.Provider.(*scriptedModel)
	if model.rounds() != 2 {
		t.Fatalf("the loop made %d calls", model.rounds())
	}
	second := model.asked(1).Messages
	var assistant *provider.Message
	for i := range second {
		if second[i].Role == provider.RoleAssistant {
			assistant = &second[i]
		}
	}
	if assistant == nil {
		t.Fatalf("the second round carried no assistant turn: %+v", second)
	}
	if len(assistant.Calls) == 0 {
		t.Fatalf("the assistant turn lost its tool call: %+v", assistant)
	}
	if string(assistant.Raw) != kept {
		t.Errorf("the turn went back without what it kept: %q", assistant.Raw)
	}
}

// Across exchanges: the conversation keeps it, and the next question
// carries it back in front of the answer it belongs to.
func TestTheNextExchangeCarriesWhatTheLastOneKept(t *testing.T) {
	const kept = `[{"type":"thinking","signature":"sig-2"}]`
	mind, _, _ := askingMind(t, saysAndKeeps("Nice to meet you.", kept), says("Seth."))
	swallow := func(Frame) error { return nil }

	for _, said := range []string{"my name is Seth", "what is my name"} {
		if err := mind.think(context.Background(), Message{Text: said, Conversation: "c"}, swallow); err != nil {
			t.Fatal(err)
		}
	}

	model := mind.Provider.(*scriptedModel)
	second := model.asked(1).Messages
	if len(second) != 3 {
		t.Fatalf("the second exchange carried %d messages: %+v", len(second), second)
	}
	if second[1].Role != provider.RoleAssistant {
		t.Fatalf("the kept turn is not the assistant's: %+v", second[1])
	}
	if string(second[1].Raw) != kept {
		t.Errorf("the conversation lost what the model kept: %q", second[1].Raw)
	}
}

// One model's record of its own reasoning is not another model's to
// read. The text survives a change of model; the Raw does not.
func TestAChangeOfModelLeavesTheThinkingBehind(t *testing.T) {
	h := newHistory()
	const kept = `[{"type":"thinking","signature":"sig-3"}]`
	h.remember("c", "hello", "hi", json.RawMessage(kept), "one")

	if got := h.recall("c", "one"); len(got) != 2 || string(got[1].Raw) != kept {
		t.Fatalf("the same model did not get its own record back: %+v", got)
	}
	got := h.recall("c", "two")
	if len(got) != 2 {
		t.Fatalf("a change of model lost the conversation: %+v", got)
	}
	if got[1].Content != "hi" {
		t.Errorf("what was said should survive a change of model: %q", got[1].Content)
	}
	if got[1].Raw != nil {
		t.Errorf("one model was handed another's reasoning: %q", got[1].Raw)
	}
	// And once it has changed, it stays changed: nothing kept for the
	// first model goes back to the second later either.
	h.remember("c", "again", "still here", nil, "two")
	for _, turn := range h.recall("c", "two") {
		if turn.Raw != nil {
			t.Errorf("the old model's record is still in the thread: %q", turn.Raw)
		}
	}
}

// --thinking-display is a dial of its own: it says whether the
// reasoning comes back to be read, not whether there is any.
func TestThinkingDisplayReachesTheRequest(t *testing.T) {
	mind, _, _ := askingMind(t, says("ok"))
	mind.ThinkingDisplay = provider.DisplayOmitted
	if err := mind.think(context.Background(), Message{Text: "hello", Conversation: "c"},
		func(Frame) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := mind.Provider.(*scriptedModel).asked(0).ThinkingDisplay; got != provider.DisplayOmitted {
		t.Errorf("the request asked for %q", got)
	}
}

// The flag is checked where somebody can read the answer, not at the
// socket where it is a 400 on the first exchange.
func TestThinkingDisplayIsCheckedAtStartup(t *testing.T) {
	for _, word := range []string{"", "summarized", "omitted"} {
		if _, err := displayFor(word); err != nil {
			t.Errorf("--thinking-display %q was refused: %v", word, err)
		}
	}
	if _, err := displayFor("summarised"); err == nil {
		t.Error("a word the API has never heard of was accepted")
	}
}
