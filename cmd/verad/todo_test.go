package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/incantery/mote/tool"
	"github.com/incantery/vera/home"
)

func newList(t *testing.T) *home.List {
	t.Helper()
	h, err := home.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return h.Todo()
}

// say runs one typed line and fails the test if it could not.
func say(t *testing.T, l *home.List, line string) TodoAnswer {
	t.Helper()
	ans, err := doTodo(l, TodoRequest{Line: line, From: "chat"}, false)
	if err != nil {
		t.Fatalf("/todo %s: %v", line, err)
	}
	return ans
}

// The default is add, and it has to be: a line misread as an item
// leaves something to delete, a line misread as a verb loses what
// somebody was writing down.
func TestTheGrammarIsAVerbOrAnItem(t *testing.T) {
	for _, tc := range []struct {
		line string
		want todoIntent
	}{
		{"", todoIntent{Verb: "list"}},
		{"all", todoIntent{Verb: "all"}},
		{"clear", todoIntent{Verb: "clear"}},
		{"buy milk", todoIntent{Verb: "add", Text: "buy milk"}},
		{"done 3", todoIntent{Verb: "done", Refs: []string{"3"}}},
		{"did 1 2 5", todoIntent{Verb: "done", Refs: []string{"1", "2", "5"}}},
		{"done call the bank", todoIntent{Verb: "done", Refs: []string{"call the bank"}}},
		{"drop passport, milk", todoIntent{Verb: "drop", Refs: []string{"passport", "milk"}}},
		{"undo 2", todoIntent{Verb: "undo", Refs: []string{"2"}}},
		{"done", todoIntent{Verb: "done"}},
		// A verb with prose after it that is not a reference to
		// anything is still that verb — but a word after `all` or
		// `clear`, which take none, is somebody writing an item.
		{"all the shopping", todoIntent{Verb: "add", Text: "all the shopping"}},
		{"clear the garage", todoIntent{Verb: "add", Text: "clear the garage"}},
	} {
		got := parseTodo(tc.line)
		if got.Verb != tc.want.Verb || got.Text != tc.want.Text || strings.Join(got.Refs, "|") != strings.Join(tc.want.Refs, "|") {
			t.Errorf("%q parsed as %+v, want %+v", tc.line, got, tc.want)
		}
	}
}

func TestAddingListingAndCrossingOff(t *testing.T) {
	l := newList(t)
	if got := say(t, l, "").Said; got != "nothing on the list" {
		t.Errorf("empty list says %q", got)
	}
	if got := say(t, l, "Call the bank about the mortgage").Said; !strings.Contains(got, "1. Call the bank") {
		t.Errorf("add said %q", got)
	}
	say(t, l, "Renew the passport")
	if got := say(t, l, "").Said; got != "2 things to do" {
		t.Errorf("list says %q", got)
	}

	// A word from the item is as good as its number.
	ans := say(t, l, "done passport")
	if !strings.Contains(ans.Said, "crossed off 2") {
		t.Errorf("done said %q", ans.Said)
	}
	if !ans.Items[1].Done {
		t.Error("the item is not done")
	}
	if got := say(t, l, "").Said; got != "1 thing to do" {
		t.Errorf("after done: %q", got)
	}

	// And it will not cross off something already crossed off, which
	// is why the pool a verb looks in is half the list.
	if got := say(t, l, "done passport"); got.Question == "" {
		t.Errorf("a done item was offered to done again: %+v", got)
	}
	if got := say(t, l, "undo passport").Said; !strings.Contains(got, "put back") {
		t.Errorf("undo said %q", got)
	}
}

// Nothing is guessed. Ambiguity comes back as a question with the
// exact line that answers it beside each candidate — no model in the
// path, which is the point.
func TestAmbiguityIsAQuestionAndNothingHappens(t *testing.T) {
	l := newList(t)
	say(t, l, "Call the bank")
	say(t, l, "Call the dentist")

	ans := say(t, l, "done call")
	if ans.Question == "" || len(ans.Choices) != 2 {
		t.Fatalf("expected a question with two candidates, got %+v", ans)
	}
	if ans.Said != "" {
		t.Errorf("something happened as well as asking: %q", ans.Said)
	}
	for _, it := range ans.Items {
		if it.Done {
			t.Fatal("an item was crossed off while the question was being asked")
		}
	}
	// The line beside a candidate is a line the parser understands.
	if got := say(t, l, ans.Choices[1].Line).Said; !strings.Contains(got, "Call the dentist") {
		t.Errorf("the offered line did not do what it offered: %q", got)
	}

	// Nothing matching is the same shape of answer, with the whole
	// pool as candidates rather than an error a person has to re-read.
	miss := say(t, l, "done aardvark")
	if miss.Question == "" || len(miss.Choices) == 0 {
		t.Errorf("a miss should ask: %+v", miss)
	}
	if !strings.Contains(miss.Question, "aardvark") {
		t.Errorf("the question does not say what did not match: %q", miss.Question)
	}
}

// A change is all or nothing. Half of a `done 1, aardvark` is worse
// than none of it, because the person cannot see which half.
func TestAnUnclearReferenceStopsTheWholeChange(t *testing.T) {
	l := newList(t)
	say(t, l, "one")
	say(t, l, "two")
	ans := say(t, l, "done one, aardvark")
	if ans.Question == "" {
		t.Fatalf("expected a question: %+v", ans)
	}
	for _, it := range ans.Items {
		if it.Done {
			t.Errorf("item %d was crossed off anyway", it.N)
		}
	}
}

// The nudge towards prose is the only place the command mentions the
// model, and it is absent when there is none to mention.
func TestProseIsOfferedOnlyWhenThereIsAMindToSayItTo(t *testing.T) {
	l := newList(t)
	say(t, l, "Call the bank")
	say(t, l, "Call the dentist")
	quiet, _ := doTodo(l, TodoRequest{Line: "done call"}, false)
	loud, _ := doTodo(l, TodoRequest{Line: "done call"}, true)
	if quiet.Prose != "" {
		t.Errorf("a Vera with no model suggested talking to one: %q", quiet.Prose)
	}
	if loud.Prose == "" {
		t.Error("a Vera with a model did not mention it")
	}
}

func TestClearSweepsWhatIsCrossedOff(t *testing.T) {
	l := newList(t)
	say(t, l, "one")
	say(t, l, "two")
	say(t, l, "done 1")
	ans := say(t, l, "clear")
	if !strings.Contains(ans.Said, "cleared 1") {
		t.Errorf("clear said %q", ans.Said)
	}
	if len(ans.Items) != 1 || ans.Items[0].Text != "two" {
		t.Errorf("after clear: %+v", ans.Items)
	}
}

func TestTheMarkdownPutsWhatIsLeftFirst(t *testing.T) {
	l := newList(t)
	say(t, l, "one")
	say(t, l, "two")
	ans := say(t, l, "done 1")
	md := TodoMarkdown(ans.Items, l.Path(), false)
	if i, j := strings.Index(md, "two"), strings.Index(md, "~~one~~"); i < 0 || j < 0 || i > j {
		t.Errorf("what is left is not first:\n%s", md)
	}
	if !strings.Contains(md, l.Path()) {
		t.Errorf("the markdown does not say where the file is:\n%s", md)
	}
	if empty := TodoMarkdown(nil, l.Path(), false); !strings.Contains(empty, "Nothing on the list") {
		t.Errorf("empty: %s", empty)
	}
}

// --- the tool -------------------------------------------------------------

func runTodoTool(t *testing.T, l *home.List, args string) (string, error) {
	t.Helper()
	res, err := (&TodoTool{List: l}).Run(context.Background(), json.RawMessage(args), tool.Handle{})
	return res.Text, err
}

// The fluent path and the exact path are the same file. That is the
// whole claim the tool makes, so it is the thing to test.
func TestTheToolAndTheCommandAreTheSameList(t *testing.T) {
	l := newList(t)
	if _, err := runTodoTool(t, l, `{"action":"add","text":"Book the dentist"}`); err != nil {
		t.Fatal(err)
	}
	items, err := l.All()
	if err != nil || len(items) != 1 || items[0].Text != "Book the dentist" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if items[0].From != "vera" {
		t.Errorf("the item does not say she wrote it: %q", items[0].From)
	}
	if got := say(t, l, "").Said; got != "1 thing to do" {
		t.Errorf("the command cannot see what the tool wrote: %q", got)
	}
	out, err := runTodoTool(t, l, `{"action":"done","item":"dentist"}`)
	if err != nil || !strings.Contains(out, "crossed off") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	// And an ambiguous one hands the model the question rather than a
	// guess — she has the person right there.
	say(t, l, "Call the bank")
	say(t, l, "Call the dentist")
	out, err = runTodoTool(t, l, `{"action":"done","item":"call"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "matches 2") || !strings.Contains(out, "Call the bank") {
		t.Errorf("the tool did not hand back the question: %q", out)
	}
}

// An item that begins with a verb is still an item when it arrives
// through the tool: the model said add, and add is not a guess.
func TestAnItemThatBeginsWithAVerbSurvivesTheTool(t *testing.T) {
	l := newList(t)
	if _, err := runTodoTool(t, l, `{"action":"add","text":"done with the accountant by Friday"}`); err != nil {
		t.Fatal(err)
	}
	items, _ := l.All()
	if len(items) != 1 || items[0].Text != "done with the accountant by Friday" {
		t.Fatalf("items=%+v", items)
	}
}

func TestTheToolSaysWhatItNeeds(t *testing.T) {
	l := newList(t)
	for _, tc := range []struct{ args, want string }{
		{`{"action":"add"}`, "an item needs words"},
		{`{"action":"done"}`, "say which item"},
		{`{}`, "not readable"},
	} {
		if _, err := runTodoTool(t, l, tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s gave %v, want %q", tc.args, err, tc.want)
		}
	}
	if _, err := (&TodoTool{}).Run(context.Background(), json.RawMessage(`{"action":"list"}`), tool.Handle{}); err == nil {
		t.Error("a tool with no list should say so")
	}
	// An "always" reaches the verb and no further.
	if got := (&TodoTool{}).Scope(json.RawMessage(`{"action":"drop"}`)); got != "drop" {
		t.Errorf("scope is %q", got)
	}
}

// The two nouns are close enough in English that the descriptions
// have to keep them apart, or "call the dentist" opens a Claude Code
// agent in a repository.
func TestTheToolSaysItIsNotTheFleet(t *testing.T) {
	d := (&TodoTool{}).Description()
	for _, want := range []string{"NOT the fleet", "code"} {
		if !strings.Contains(d, want) {
			t.Errorf("the description does not say %q:\n%s", want, d)
		}
	}
	var schema map[string]any
	if err := json.Unmarshal((&TodoTool{}).Schema(), &schema); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
}
