package fleet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A spawn carries the pictures into the brief the pane runs, and onto
// the record — a task reopened tomorrow has to be handed the same
// evidence, and the store is the only thing that outlives the process.
func TestSpawnKeepsAndBriefsTheImages(t *testing.T) {
	r := newRepo(t)
	m := newFakeMux()
	store := NewStore(filepath.Join(t.TempDir(), "fleet"))
	f := New(m, store)
	f.Harness = []string{"fake-agent"}
	f.Trust = func(string, string) error { return nil }

	shot := "/state/vera/images/c/aa.png"
	task, err := f.Spawn(context.Background(), Request{
		Project: r.Root, Name: "fix", Brief: "the header overlaps", Images: []string{shot},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Images) != 1 || task.Images[0] != shot {
		t.Fatalf("images on the record: %v", task.Images)
	}
	// On disk, so a task reopened tomorrow is handed the same picture.
	again, err := store.Load(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Images) != 1 || again.Images[0] != shot {
		t.Fatalf("images did not survive the store: %v", again.Images)
	}
	argv := m.argv[task.Pane]
	if len(argv) != 2 {
		t.Fatalf("command shape: %q", argv)
	}
	script, err := os.ReadFile(argv[1])
	if err != nil {
		t.Fatal(err)
	}
	if run := string(script); !strings.Contains(run, shot) {
		t.Errorf("the brief the pane runs never names the picture:\n%s", run)
	}
}

// A picture can be the answer to what an agent asked. It is typed into
// the room the words are, because a pane carries text and the agent on
// the other end reads files.
func TestAnswerCanCarryAPicture(t *testing.T) {
	r := newRepo(t)
	m := newFakeMux()
	store := NewStore(filepath.Join(t.TempDir(), "fleet"))
	f := New(m, store)
	f.Harness = []string{"fake-agent"}
	f.Trust = func(string, string) error { return nil }
	ctx := context.Background()

	task, err := f.Spawn(ctx, Request{Project: r.Root, Name: "ask", Brief: "b"})
	if err != nil {
		t.Fatal(err)
	}
	m.typed = nil
	if err := f.Answer(ctx, task.ID, "here is what I see", "/state/vera/images/c/bb.png"); err != nil {
		t.Fatal(err)
	}
	typed := strings.Join(m.typed, "")
	if !strings.Contains(typed, "here is what I see") || !strings.Contains(typed, "/state/vera/images/c/bb.png") {
		t.Fatalf("what was typed into the room: %q", typed)
	}
	// Typed, not handed over: a newline in what is SENT is a Return,
	// and a Return in the middle sends half the answer and leaves the
	// rest sitting in the box. (The one at the end is the Enter the
	// fake records separately, which is the send.)
	if strings.Contains(m.typed[0], "\n") {
		t.Fatalf("a newline was typed into the pane: %q", m.typed[0])
	}
	// The log says what was typed, so the record and the pane agree.
	log, err := store.Statuses(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := log[len(log)-1]
	if last.Verb != Resolved || !strings.Contains(last.Text, "/state/vera/images/c/bb.png") {
		t.Fatalf("log line: %+v", last)
	}

	// An answer with no picture is exactly what it was.
	m.typed = nil
	if err := f.Answer(ctx, task.ID, "just words"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(m.typed, ""); got != "just words\n" {
		t.Fatalf("a plain answer was changed: %q", got)
	}
}
