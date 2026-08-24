package main

import (
	"strings"
	"testing"
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
	d := &Delegate{}
	tool := d.tool()
	fn, ok := tool["function"].(map[string]any)
	if !ok {
		t.Fatal("the tool has no function")
	}
	if fn["name"] != "delegate" {
		t.Fatalf("tool is named %v", fn["name"])
	}
	desc, _ := fn["description"].(string)
	for _, needed := range []string{"Do not use it", "files", "commands"} {
		if !strings.Contains(desc, needed) {
			t.Errorf("the description never mentions %q, so the model has to guess", needed)
		}
	}
}
