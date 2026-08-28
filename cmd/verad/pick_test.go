package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/incantery/mote/provider"
	"github.com/incantery/vera/journal"
)

// wiredTo is a Wires whose models are already built, so a test can ask
// for a model without a key or a socket.
func wiredTo(models map[string]string) *Wires {
	made := map[string]*madeWire{}
	for model, vendor := range models {
		made[model] = &madeWire{p: &scriptedModel{}, vendor: vendor}
	}
	return &Wires{made: made}
}

func mindFor(t *testing.T) *Mind {
	t.Helper()
	return &Mind{
		Provider:   &scriptedModel{},
		Vendor:     "openai",
		Model:      "gpt-5.6-luna",
		ModelFrom:  fromBuiltin,
		EffortFrom: fromBuiltin,
		BaseEffort: "high",
		Wires:      wiredTo(map[string]string{"claude-opus-5": "anthropic", "gpt-5-mini": "openai"}),
		Picks:      &Picks{Path: filepath.Join(t.TempDir(), "models.json")},
		History:    newHistory(),
	}
}

// The order the whole feature rests on: a conversation's own choice
// beats one message's, which beats the flag, which beats the profile,
// which beats the built-in default.
func TestMoreSpecificWins(t *testing.T) {
	m := mindFor(t)

	// Nothing said: the daemon's own.
	got, err := m.choose("c1", Pick{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5.6-luna" || got.ModelFrom != fromBuiltin {
		t.Fatalf("base model: %q from %q", got.Model, got.ModelFrom)
	}

	// One message.
	got, err = m.choose("c1", Pick{Model: "gpt-5-mini"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5-mini" || got.ModelFrom != fromMessage {
		t.Fatalf("per-message model: %q from %q", got.Model, got.ModelFrom)
	}

	// The conversation, which the next message does not undo.
	if _, err := m.Choose("c1", "claude-opus-5", "medium"); err != nil {
		t.Fatal(err)
	}
	got, err = m.choose("c1", Pick{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "claude-opus-5" || got.ModelFrom != fromConversation {
		t.Fatalf("per-conversation model: %q from %q", got.Model, got.ModelFrom)
	}
	if got.Effort != "medium" || got.EffortFrom != fromConversation {
		t.Fatalf("per-conversation effort: %q from %q", got.Effort, got.EffortFrom)
	}

	// A message is more specific still, for that one exchange.
	got, err = m.choose("c1", Pick{Model: "gpt-5-mini"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5-mini" || got.ModelFrom != fromMessage {
		t.Fatalf("a message should outrank the conversation: %q from %q", got.Model, got.ModelFrom)
	}

	// Another conversation was never told anything.
	got, err = m.choose("c2", Pick{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5.6-luna" {
		t.Fatalf("c1's choice leaked into c2: %q", got.Model)
	}
}

// The flag outranks the profile now — a profile is a default about
// what this agent is, and a flag is somebody typing.
func TestFlagOutranksProfile(t *testing.T) {
	own := &Hands{Model: "claude-opus-5"}
	if got, source := modelFor("gpt-5-mini", true, Pick{}, own); got != "gpt-5-mini" || source != fromFlag {
		t.Fatalf("--model did not win: %q (%s)", got, source)
	}
	got, source := modelFor("gpt-5.6-luna", false, Pick{}, own)
	if got != "claude-opus-5" || source != "profiles/supervisor/profile.md" {
		t.Fatalf("the profile should fill in: %q (%s)", got, source)
	}
	if got, source := modelFor("gpt-5.6-luna", false, Pick{}, nil); got != "gpt-5.6-luna" || source != fromBuiltin {
		t.Fatalf("with nothing else, the built-in default: %q (%s)", got, source)
	}
}

// The vendor's half. The OpenAI-compatible endpoint gets reasoning off
// and no effort unless somebody typed one; Anthropic gets the dial.
func TestVendorDecidesTheDial(t *testing.T) {
	effort, thinking := tune("openai", "high", false)
	if effort != "none" || thinking != provider.ThinkingOff {
		t.Fatalf("openai default: %q / %q", effort, thinking)
	}
	if effort, _ := tune("openai", "high", true); effort != "high" {
		t.Fatalf("an effort somebody typed should stand: %q", effort)
	}
	effort, thinking = tune("anthropic", "high", false)
	if effort != "high" || thinking != "" {
		t.Fatalf("anthropic: %q / %q", effort, thinking)
	}
}

// Reopening `vera chat -c <id>` is a new process asking verad the same
// question, so the answer has to be on disk.
func TestAConversationsModelSurvivesAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	m := mindFor(t)
	m.Picks = &Picks{Path: path}
	if _, err := m.Choose("chat-1", "claude-opus-5", "max"); err != nil {
		t.Fatal(err)
	}

	// A second daemon, the same file.
	again := mindFor(t)
	again.Picks = &Picks{Path: path}
	got, err := again.Pick("chat-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "claude-opus-5" || got.Effort != "max" {
		t.Fatalf("reopened on %q / %q", got.Model, got.Effort)
	}
	if got.ModelFrom != fromConversation {
		t.Fatalf("provenance lost: %q", got.ModelFrom)
	}

	// And clearing it puts the conversation back on the daemon's own.
	if _, err := again.Choose("chat-1", "", ""); err != nil {
		t.Fatal(err)
	}
	back, err := again.Pick("chat-1")
	if err != nil {
		t.Fatal(err)
	}
	if back.Model != "gpt-5.6-luna" || back.ModelFrom != fromBuiltin {
		t.Fatalf("clearing left %q from %q", back.Model, back.ModelFrom)
	}
}

// A model with no wire on this machine is an error on the way in.
// (Whether the far end knows the NAME is a different question, and
// only the far end can answer it — that comes back as a 404 on the
// first thing said.)
func TestAModelWithNoWireIsRefusedBeforeItIsKept(t *testing.T) {
	// No keys, no endpoint: nothing can be built for any name.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	m := mindFor(t)
	if _, err := m.Choose("c1", "no-such-model", ""); err == nil {
		t.Fatal("a model with no provider was accepted")
	}
	if _, ok := m.Picks.Get("c1"); ok {
		t.Fatal("the bad model was written down anyway")
	}
	if _, err := m.Choose("c1", "claude-opus-5", "sideways"); err == nil {
		t.Fatal("a nonsense effort was accepted")
	}
}

// A message names a model, and the journal says which one answered.
func TestOneMessageCanNameItsOwnModel(t *testing.T) {
	dir := t.TempDir()
	m := mindFor(t)
	m.Journal = &journal.Writer{Dir: dir}
	m.instruments = newInstruments()
	m.Provider = scripted(t, says("from luna"))
	m.Wires = wiredTo(map[string]string{"claude-opus-5": "anthropic"})
	m.Wires.made["claude-opus-5"].p = scripted(t, says("from opus"))

	err := m.think(context.Background(), Message{Text: "hello", Conversation: "c1", Model: "claude-opus-5", Effort: "medium"},
		func(Frame) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	entries := readJournal(t, dir, "c1")
	if len(entries) != 1 {
		t.Fatalf("wanted one exchange, got %d", len(entries))
	}
	if entries[0].Model != "claude-opus-5" || entries[0].Effort != "medium" {
		t.Fatalf("journal says %q at effort %q", entries[0].Model, entries[0].Effort)
	}
	if entries[0].Answered != "from opus" {
		t.Fatalf("the wrong model answered: %q", entries[0].Answered)
	}
}
