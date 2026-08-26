package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/incantery/mote/provider"
)

// TestHandsLive puts the real model in front of the real tools, once,
// against a throwaway home. It is the one thing the scripted tests
// cannot tell you: whether a model handed eight tool definitions on
// this endpoint reaches for the right one and reads what it finds.
//
// Skipped without VERA_LIVE=1. It costs money and it talks to the
// network; nothing in CI should do either by accident.
func TestHandsLive(t *testing.T) {
	mind, root := liveMind(t)

	// A fact only a tool can find.
	if err := os.WriteFile(filepath.Join(root, "notes", "colour.md"),
		[]byte("The colour of the shed is heliotrope.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var answer strings.Builder
	var tools []string
	err := mind.think(ctx, Message{
		Text:         "What colour is the shed? It is written down in notes/colour.md in your home.",
		Conversation: "live",
	}, func(f Frame) error {
		if f.ToolCall != nil {
			tools = append(tools, f.ToolCall.Name+" "+f.ToolCall.Args)
		}
		if f.ToolResult != nil {
			t.Logf("result: %s", trim(f.ToolResult.Result, 300))
		}
		if f.Ask != nil {
			t.Errorf("reading her own home should not ask: %+v", *f.Ask)
			_ = mind.Hands.Answer(ctx, f.Ask.ID, "no")
		}
		answer.WriteString(f.Delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("tools: %v\nanswer: %s", tools, answer.String())
	if len(tools) == 0 {
		t.Fatal("the model never reached for a tool, so it cannot have read the file")
	}
	if !strings.Contains(strings.ToLower(answer.String()), "heliotrope") {
		t.Fatalf("answer was %q", answer.String())
	}
}

// The ask, against the real model: a write where the policy has
// nothing to say stops and puts the question, and a yes runs it.
func TestAskLive(t *testing.T) {
	mind, _ := liveMind(t)
	target := filepath.Join(t.TempDir(), "note-for-them.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	asked := make(chan string, 4)
	err := mind.think(ctx, Message{
		Text:         "Write the single word marmalade into the file " + target + ". Use your write tool; do not delegate.",
		Conversation: "live-ask",
	}, func(f Frame) error {
		if f.Ask != nil {
			asked <- f.Ask.Name
			// From a goroutine: the exchange is parked inside this
			// very callback's caller, so answering here would wait on
			// itself forever.
			go func(id string) { _ = mind.Hands.Answer(ctx, id, "yes") }(f.Ask.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case name := <-asked:
		if name != "write" {
			t.Fatalf("asked about %q", name)
		}
	default:
		t.Fatal("writing outside her home did not ask")
	}
	b, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(b), "marmalade") {
		t.Fatalf("the answered write did not land: %v %q", err, b)
	}
}

// Memory, curated by her rather than extracted from her: something
// worth keeping was said, so a file and an index line should appear.
// This is the behaviour the eval suite used to get from the
// extractor, and it is the one thing about milestone 5 that is a
// prompt question rather than a wiring question.
func TestMemoryLive(t *testing.T) {
	mind, root := liveMind(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := mind.think(ctx, Message{
		Text:         "I have gone vegetarian for good — no meat from now on. Worth remembering.",
		Conversation: "live-memory",
	}, func(Frame) error { return nil }); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(root, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("she was told something durable and kept none of it")
	}
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(root, "memory", e.Name()))
		t.Logf("%s:\n%s", e.Name(), b)
	}
	index, err := os.ReadFile(filepath.Join(root, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MEMORY.md:\n%s", index)
	if strings.TrimSpace(string(index)) == "" {
		t.Error("a fact with no index line is a fact nobody sees again")
	}
	// What she wrote has to reach the prompt, which is the wiring
	// claim and the one worth failing on.
	if !strings.Contains(strings.ToLower(mind.Memory.Recite()), "vegetarian") {
		t.Fatalf("what she wrote never reached the prompt:\n%s", mind.Memory.Recite())
	}

	// Whether she then USES it well is the eval suite's question, not
	// this one — so the next conversation is logged rather than
	// asserted on. A live test that fails on a model's wording is a
	// test that will fail for the wrong reason at midnight.
	var answer strings.Builder
	if err := mind.think(ctx, Message{
		Text:         "What should I order at a steakhouse?",
		Conversation: "live-memory-next",
	}, func(f Frame) error {
		answer.WriteString(f.Delta)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("next conversation: %s", answer.String())
}

// liveMind is a mind with hands, a throwaway home and the real model.
func liveMind(t *testing.T) (*Mind, string) {
	t.Helper()
	if os.Getenv("VERA_LIVE") != "1" {
		t.Skip("set VERA_LIVE=1 to spend real tokens on this")
	}
	model := os.Getenv("VERA_LIVE_MODEL")
	if model == "" {
		model = "gpt-5.6-luna"
	}
	// The real thing, chosen the way verad chooses it: the model name
	// and whatever keys this machine has decide the wire.
	p, err := provider.New(provider.Config{Model: model, OpenAIKey: findKey("")})
	if err != nil {
		t.Skip(err.Error())
	}
	root := filepath.Join(t.TempDir(), "vera")
	mind, _, _ := askingMindAt(t, root, nil)
	mind.Provider, mind.Model, mind.Vendor = p, model, vendorOf(p)
	mind.Memory = mind.Home.Memory()
	return mind, root
}
