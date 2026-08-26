package main

import (
	"context"
	"strings"
	"testing"

	"github.com/incantery/mote/provider"
)

// Dictation is the one call with a deadline the person can feel: the
// cursor waits 2.5 seconds and then types the raw words. So it asks
// for no reasoning — and for no effort either, because a model that
// takes both can refuse the pair.
func TestDictationAsksForNothingSlow(t *testing.T) {
	model := scripted(t, says("Send it Wednesday."))
	mind := &Mind{Provider: model, Model: "m", Effort: provider.EffortMax,
		Thinking: provider.ThinkingOff, instruments: newInstruments()}

	got, err := mind.clean(context.Background(), Dictation{
		Text: "um send it tuesday no wait wednesday",
		App:  &ObservedApp{Name: "Mail", BundleID: "com.apple.mail"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Raw || got.Text != "Send it Wednesday." {
		t.Fatalf("the cleaned text was %+v", got)
	}

	req := model.asked(0)
	if req.Thinking != provider.ThinkingOff {
		t.Errorf("dictation asked for thinking: %q", req.Thinking)
	}
	if req.Effort != "" {
		t.Errorf("dictation asked for effort %q, which a thinking-off model can refuse", req.Effort)
	}
	if len(req.Tools) != 0 {
		t.Errorf("the cursor has no tools to reach for: %+v", req.Tools)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != provider.RoleUser {
		t.Errorf("dictation carried a conversation: %+v", req.Messages)
	}
	// The application it is going into is a hint about register, and
	// it belongs in the prompt rather than in the words.
	if !strings.Contains(req.System, "Mail") {
		t.Errorf("where the cursor is did not reach the prompt:\n%s", req.System)
	}
}
