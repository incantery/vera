package main

import (
	"strings"
	"testing"

	"github.com/incantery/mote/tui"
	"github.com/incantery/vera/home"
)

func todoSession(t *testing.T) (*fakeVerad, *chatSession) {
	t.Helper()
	f := newFakeVerad(t)
	c := f.client()
	return f, &chatSession{c: c, w: newFleetWatch(c), conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}
}

// The line is sent whole. Nothing about what it means is decided here,
// so what the terminal must get right is that it did not touch it.
func TestTodoSendsTheLineWholeAndDrawsWhatCameBack(t *testing.T) {
	f, s := todoSession(t)
	f.answerTodo(func(line string) TodoAnswer {
		if line == "" {
			return TodoAnswer{Verb: "list", Path: "/home/vera/TODO.md", Items: []home.Item{
				{N: 1, Text: "Call the bank"},
				{N: 2, Text: "File the return", Done: true},
			}}
		}
		return TodoAnswer{Verb: "add", Said: "3. " + line, Items: []home.Item{
			{N: 1, Text: "Call the bank"}, {N: 3, Text: line},
		}}
	})

	// A list is a block, because a list is what you asked to see.
	got := joined(s.handle("todo", ""))
	for _, want := range []string{"Call the bank", "~~File the return~~", "/home/vera/TODO.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("/todo is missing %q:\n%s", want, got)
		}
	}

	// A change is one line, with what is left on the end of it.
	got = joined(s.handle("todo", "done 4 things, keep the commas"))
	if !strings.Contains(got, "3. done 4 things, keep the commas") {
		t.Errorf("/todo <item>: %q", got)
	}
	if !strings.Contains(got, "2 left") {
		t.Errorf("the answer does not say what is left: %q", got)
	}
	if lines := f.todoLines(); len(lines) != 2 || lines[1] != "done 4 things, keep the commas" {
		t.Errorf("verad was told %q — the terminal edited the line", lines)
	}
}

// A question is a card, not an error. Choosing a row sends the exact
// line that row carried, so choosing and typing are the same act.
func TestAnAmbiguousTodoPutsTheCandidatesUp(t *testing.T) {
	f, s := todoSession(t)
	f.answerTodo(func(line string) TodoAnswer {
		if line == "done 2" {
			return TodoAnswer{Verb: "done", Said: "crossed off 2. Call the dentist",
				Items: []home.Item{{N: 1, Text: "Call the bank"}, {N: 2, Text: "Call the dentist", Done: true}}}
		}
		return TodoAnswer{
			Verb:     "done",
			Question: `"call" matches 2 of them — which?`,
			Prose:    "or just tell her",
			Items:    []home.Item{{N: 1, Text: "Call the bank"}, {N: 2, Text: "Call the dentist"}},
			Choices: []TodoChoice{
				{Label: "Call the bank", Line: "done 1"},
				{Label: "Call the dentist", Line: "done 2"},
			},
		}
	})

	msg := s.handle("todo", "done call")()
	pick, ok := msg.(tui.Pick)
	if !ok {
		t.Fatalf("an ambiguous /todo gave %T, want a card", msg)
	}
	if len(pick.Items) != 2 || pick.Items[1].Label != "Call the dentist" {
		t.Fatalf("card rows: %+v", pick.Items)
	}
	if !strings.Contains(pick.Text, "matches 2") || !strings.Contains(pick.Text, "tell her") {
		t.Errorf("the card does not say why it is asking: %q", pick.Text)
	}

	// Cancelling leaves nothing behind — nothing had happened yet.
	if cmd := pick.OnPick(tui.PickChoice{Cancelled: true}); cmd != nil {
		t.Error("cancelling the card did something")
	}
	if got := joined(pick.OnPick(tui.PickChoice{Item: 1})); !strings.Contains(got, "crossed off 2") {
		t.Errorf("choosing the second row: %q", got)
	}
	if lines := f.todoLines(); len(lines) != 2 || lines[1] != "done 2" {
		t.Errorf("choosing sent %q, want the line the row carried", lines)
	}
}

func TestTodoSaysWhenVeradWillNot(t *testing.T) {
	_, s := todoSession(t)
	s.c = &chatClient{base: "http://127.0.0.1:1", secret: "x", device: "d"}
	if got := joined(s.handle("todo", "")); !strings.Contains(got, "todo:") {
		t.Errorf("an unreachable verad said %q", got)
	}
}

// The count on the end of a change is the whole point of putting one
// there: it answers "and now what".
func TestOpenCountSaysWhatIsLeft(t *testing.T) {
	for _, tc := range []struct {
		items []home.Item
		want  string
	}{
		{nil, ""},
		{[]home.Item{{N: 1, Text: "a"}}, "1 left"},
		{[]home.Item{{N: 1, Text: "a"}, {N: 2, Text: "b"}}, "2 left"},
		{[]home.Item{{N: 1, Text: "a", Done: true}}, "nothing left"},
	} {
		if got := openCount(tc.items); got != tc.want {
			t.Errorf("%d items: %q, want %q", len(tc.items), got, tc.want)
		}
	}
}

// /help has to name it, or nobody types it.
func TestTodoIsInTheCommandList(t *testing.T) {
	var found *tui.Command
	for i := range chatCommands {
		if chatCommands[i].Name == "todo" {
			found = &chatCommands[i]
		}
	}
	if found == nil {
		t.Fatal("/todo is not in the command list")
	}
	if !strings.Contains(found.Help, "done") {
		t.Errorf("the help does not say how to cross one off: %q", found.Help)
	}
}
