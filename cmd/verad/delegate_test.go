package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/incantery/mote/tool"
)

// The status line is the only thing a person sees for what may be
// minutes, so it has to read like a sentence rather than a log entry.
func TestDelegatingReadsLikeASentence(t *testing.T) {
	cases := map[string]string{
		"Create a file called notes.txt. Then tell me.": "create a file called notes.txt",
		"Look up the train times to Vienna":             "look up the train times to vienna",
	}
	for task, want := range cases {
		got := delegating(task)
		if !strings.HasPrefix(got, "Working on it — ") {
			t.Errorf("status did not lead with the wait: %q", got)
		}
		if !strings.Contains(strings.ToLower(got), want) {
			t.Errorf("status lost the task: %q", got)
		}
		// Exactly one ellipsis, at the end — the cut and the "still
		// going" are the same mark, not two.
		if strings.Count(got, "…") != 1 || !strings.HasSuffix(got, "…") {
			t.Errorf("ellipsis is wrong in %q", got)
		}
	}

	if got := delegating("   "); got != "Working on it…" {
		t.Errorf("an empty task produced %q", got)
	}
}

func TestLongTasksAreCutOnce(t *testing.T) {
	got := delegating("Do " + strings.Repeat("something very elaborate ", 20))
	if len(got) > 130 {
		t.Errorf("status is %d characters; it has to fit a phone: %q", len(got), got)
	}
	if strings.Count(got, "…") != 1 {
		t.Errorf("doubled ellipsis: %q", got)
	}
}

// The workspace is a floor, not a fence — but the floor has to exist.
func TestWorkspaceDefaultsSomewhereOfItsOwn(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/state")
	if got := workspacePath(""); got != "/tmp/state/vera/workspace" {
		t.Fatalf("default workspace is %q", got)
	}
	if got := workspacePath("/somewhere/else"); got != "/somewhere/else" {
		t.Fatalf("an explicit workspace was ignored: %q", got)
	}
}

// The description is the entire basis on which the model decides
// between answering and delegating.
func TestTheToolDescribesWhenToUseIt(t *testing.T) {
	d := &DelegateTool{Delegate: &Delegate{}}
	if d.Name() != "delegate" {
		t.Fatalf("tool is named %q", d.Name())
	}
	desc := d.Description()
	for _, needed := range []string{"Do not use it", "files", "commands"} {
		if !strings.Contains(desc, needed) {
			t.Errorf("the description never mentions %q, so the model has to guess", needed)
		}
	}
}

// What the delegate needs beyond the model's prose arrives on the
// Handle, and what it reached leaves on the Result.
//
// The Claude Code it hands to is not on PATH here, so the run fails —
// which is the interesting half: the status line was already said,
// and the Meta comes back anyway, because a run that was killed still
// spent what it spent.
func TestTheDelegateSpeaksAndReportsThroughTheHandle(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	d := &DelegateTool{Delegate: &Delegate{Workspace: t.TempDir(), Timeout: 5 * time.Second}}

	var said []string
	res, err := d.Run(context.Background(),
		json.RawMessage(`{"task":"Look up the train times to Vienna"}`),
		tool.Handle{
			Status: func(text string) { said = append(said, text) },
			Values: map[string]any{keyConversation: "c1", tool.Device: "phone"},
		})
	if err == nil {
		t.Fatal("a delegate with no Claude Code on PATH succeeded")
	}
	if len(said) != 1 || !strings.Contains(strings.ToLower(said[0]), "train times") {
		t.Errorf("what the person saw while it worked: %v", said)
	}
	if res.Meta == nil {
		t.Error("a failed delegation reported nothing about what it spent")
	}
	if _, ok := res.Meta[tool.MetaCost]; !ok {
		t.Errorf("no cost on the record: %v", res.Meta)
	}

	// A tool with nobody to hand to says so rather than pretending.
	if _, err := (&DelegateTool{}).Run(context.Background(), json.RawMessage(`{"task":"x"}`), tool.Handle{}); err == nil {
		t.Error("a delegate with no Claude Code behind it succeeded")
	}
	// And the zero Handle works, which is what lets a tool never check.
	if _, err := d.Run(context.Background(), json.RawMessage(`{"task":""}`), tool.Handle{}); err == nil {
		t.Error("an empty task succeeded")
	}
}
