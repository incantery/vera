// The list over the wire, and in her hands.
//
// A task is an agent Vera opened a room for. An item on the list is
// neither an agent nor a room — it is a thing the PERSON has to do,
// and most of them are not about code at all. Keeping them apart is
// the whole reason this is a second noun: `/tasks` is what is being
// worked on, `/todo` is what is not done yet.
//
// Two layers, and the order between them is the design:
//
// The deterministic one always goes first and always wins. `todo`,
// `todo done 3`, `todo drop passport` are parsed here, by hand, with
// no model anywhere in the path. They work on a laptop with no key,
// they cost nothing, they take a millisecond, and the same words do
// the same thing today and next year. A to-do list that sometimes
// paraphrases what you typed is not a to-do list.
//
// The agent layer is what is there when a mind is: the same list, as
// one of her tools. "mark the bank one done and remind me to book the
// dentist" is prose, and prose is what a model is for — it reaches
// exactly the same file through exactly the same package. So the
// command never gets slower or less predictable to buy the fluency,
// and the fluency is there anyway for anyone who wants it.
//
// Between the two sits the one thing neither can skip: when what was
// typed matches more than one item, or none, nothing is guessed. The
// candidates come back as a question with the exact line that answers
// it beside each one — a follow-up, deterministic, no model required.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/incantery/mote/tool"
	"github.com/incantery/vera/home"
)

// TodoRequest is one typed line, whole. The daemon parses it rather
// than the client, so the terminal, the CLI and the phone cannot
// disagree about what `done` means.
type TodoRequest struct {
	Line string `json:"line"`
	// From is who is writing — a device name, "chat", "vera". It goes
	// on the item so a list read next month says where each thing
	// came from.
	From string `json:"from,omitempty"`
}

// TodoAnswer is the list after whatever happened, plus a line about
// what that was. Said and Question are never both set: either it was
// done, or it is being asked about.
type TodoAnswer struct {
	// Verb is what the line turned out to mean — list, all, add,
	// done, undo, drop, clear. The client renders a list differently
	// from a change, and verad is the one that parsed the line, so it
	// is the one that says which happened.
	Verb     string       `json:"verb,omitempty"`
	Said     string       `json:"said,omitempty"`
	Items    []home.Item  `json:"items"`
	Path     string       `json:"path,omitempty"`
	Question string       `json:"question,omitempty"`
	Choices  []TodoChoice `json:"choices,omitempty"`
	// Prose is a nudge to say it in words instead, present only when
	// there is a mind to say it to. The client shows it under a
	// question; a client that has never heard of it loses nothing.
	Prose string `json:"prose,omitempty"`
}

// TodoChoice is one candidate and the exact line that picks it. The
// line rather than an index: a client that shows the choice a week
// later, or a person reading the JSON, both end up sending something
// the parser already understands.
type TodoChoice struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Line   string `json:"line"`
}

// todoRoutes are mounted when there is a home to keep the list in.
func (l *lanTransport) todoRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /todo", l.todoList)
	mux.HandleFunc("POST /todo", l.todoDo)
}

func (l *lanTransport) todoList(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	items, err := l.todo.All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, TodoAnswer{Verb: "list", Items: nonNil(items), Path: l.todo.Path()})
}

func (l *lanTransport) todoDo(w http.ResponseWriter, r *http.Request) {
	if !l.authed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req TodoRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ans, err := doTodo(l.todo, req, l.hasMind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, ans)
}

// plural and firstNonEmpty are the two-line helpers this file needs
// and the package did not have. Kept here rather than in a utilities
// file nobody opens.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func nonNil(items []home.Item) []home.Item {
	if items == nil {
		return []home.Item{}
	}
	return items
}

// --- the grammar ----------------------------------------------------------

// The verbs, and every word that means one. They are short because
// they are typed after a slash and a space, in the middle of doing
// something else; they are aliased because a person who types `did`
// and gets an item called "did the washing" has been punished for
// speaking English.
var todoVerbs = map[string]string{
	"done": "done", "did": "done", "x": "done", "finish": "done", "finished": "done", "complete": "done",
	"undo": "undo", "open": "undo", "reopen": "undo", "undone": "undo",
	"drop": "drop", "rm": "drop", "remove": "drop", "delete": "drop", "forget": "drop",
	"clear": "clear", "tidy": "clear",
	"all": "all", "list": "list", "ls": "list",
}

// todoIntent is what a line meant.
type todoIntent struct {
	Verb string   // list, all, add, done, undo, drop, clear
	Text string   // add: the item, verbatim
	Refs []string // done/undo/drop: what to act on
}

// parseTodo reads a line. Everything that is not a verb is an item,
// which is the right default by a distance: the common case for a
// to-do list is putting something ON it, and a line misread as an add
// leaves a stray item somebody deletes, while a line misread as a
// verb loses what they were trying to write down.
func parseTodo(line string) todoIntent {
	line = strings.TrimSpace(line)
	if line == "" {
		return todoIntent{Verb: "list"}
	}
	head, rest, _ := strings.Cut(line, " ")
	verb, ok := todoVerbs[strings.ToLower(head)]
	if !ok {
		return todoIntent{Verb: "add", Text: line}
	}
	rest = strings.TrimSpace(rest)
	switch verb {
	case "list", "all", "clear":
		// A word after them is not an argument, it is somebody who
		// meant to write it down. `todo all the shopping` is an item.
		if rest != "" {
			return todoIntent{Verb: "add", Text: line}
		}
		return todoIntent{Verb: verb}
	}
	if rest == "" {
		return todoIntent{Verb: verb}
	}
	return todoIntent{Verb: verb, Refs: splitRefs(rest)}
}

// splitRefs cuts what follows a verb into the things it names. Commas
// separate always; bare words are one reference unless every one of
// them is a number, because `done 1 3` is two items and `done call the
// bank` is one.
func splitRefs(rest string) []string {
	if strings.Contains(rest, ",") {
		var out []string
		for _, part := range strings.Split(rest, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	fields := strings.Fields(rest)
	for _, f := range fields {
		if _, err := strconv.Atoi(f); err != nil {
			return []string{rest}
		}
	}
	return fields
}

// --- doing it -------------------------------------------------------------

// doTodo is the whole command, and it is the same function the wire,
// the tool and the tests call. mind says whether there is a model to
// suggest talking to; it changes nothing about what happens.
func doTodo(l *home.List, req TodoRequest, mind bool) (TodoAnswer, error) {
	if l == nil {
		return TodoAnswer{}, errors.New("there is no home to keep a list in")
	}
	in := parseTodo(req.Line)
	items, err := l.All()
	if err != nil {
		return TodoAnswer{}, err
	}
	ans := TodoAnswer{Verb: in.Verb, Path: l.Path()}

	switch in.Verb {
	case "list", "all":
		ans.Items = nonNil(items)
		ans.Said = countSaid(items)
		return ans, nil

	case "add":
		it, after, err := l.Add(in.Text, firstNonEmpty(req.From, "chat"))
		if err != nil {
			return TodoAnswer{}, err
		}
		ans.Items, ans.Said = nonNil(after), fmt.Sprintf("%d. %s", it.N, it.Text)
		return ans, nil

	case "clear":
		gone, after, err := l.Clear()
		if err != nil {
			return TodoAnswer{}, err
		}
		ans.Items = nonNil(after)
		ans.Said = fmt.Sprintf("cleared %d crossed-off %s", len(gone), plural(len(gone), "item"))
		return ans, nil
	}

	// The rest act on items, so they need to know which. Each verb
	// looks in the half of the list it can act on: `done` cannot
	// finish something already finished, and offering it as a
	// candidate would be offering a mistake.
	pool, empty := todoPool(in.Verb, items)
	if len(pool) == 0 {
		ans.Items, ans.Said = nonNil(items), empty
		return ans, nil
	}
	if len(in.Refs) == 0 {
		ans.Items = nonNil(items)
		ans.Question = "which one? — " + todoVerbHelp(in.Verb)
		ans.Choices, ans.Prose = todoChoices(in.Verb, pool), proseNudge(mind)
		return ans, nil
	}

	var ns []int
	for _, ref := range in.Refs {
		found := home.Match(pool, ref)
		if len(found) == 1 {
			ns = append(ns, found[0].N)
			continue
		}
		// Nothing is done when anything is unclear. Half a change is
		// worse than none here: the person cannot see which half.
		ans.Items = nonNil(items)
		if len(found) == 0 {
			// It may not be missing so much as on the wrong side of
			// the line: `done 2` where 2 is already crossed off is a
			// person who has lost track, not a person who mistyped,
			// and telling them that is the answer rather than a
			// question with the whole list attached.
			if elsewhere := home.Match(items, ref); len(elsewhere) == 1 {
				ans.Said = alreadySaid(in.Verb, elsewhere[0])
				return ans, nil
			}
			ans.Question = fmt.Sprintf("nothing to %s matches %q — did you mean one of these?", todoAct(in.Verb), ref)
			ans.Choices = todoChoices(in.Verb, pool)
		} else {
			ans.Question = fmt.Sprintf("%q matches %d of them — which?", ref, len(found))
			ans.Choices = todoChoices(in.Verb, found)
		}
		ans.Prose = proseNudge(mind)
		return ans, nil
	}

	var touched, after []home.Item
	switch in.Verb {
	case "done":
		touched, after, err = l.Mark(true, ns)
	case "undo":
		touched, after, err = l.Mark(false, ns)
	case "drop":
		touched, after, err = l.Drop(ns)
	}
	if err != nil {
		return TodoAnswer{}, err
	}
	ans.Items, ans.Said = nonNil(after), saidAbout(in.Verb, touched)
	return ans, nil
}

// todoPool is the half of the list a verb may act on, and what to say
// when it is empty.
func todoPool(verb string, items []home.Item) ([]home.Item, string) {
	switch verb {
	case "done":
		return home.Remaining(items), "nothing left to cross off"
	case "undo":
		return home.Crossed(items), "nothing has been crossed off"
	}
	return items, "the list is empty"
}

// todoAct is the verb as a sentence uses it. "nothing to done
// matches" is not English, and the person reading it is mid-task.
func todoAct(verb string) string {
	switch verb {
	case "done":
		return "cross off"
	case "undo":
		return "put back"
	}
	return "drop"
}

// alreadySaid is for a reference that names a real item the verb
// cannot touch, which is almost always one already in the state being
// asked for.
func alreadySaid(verb string, it home.Item) string {
	was := "already crossed off"
	if verb == "undo" {
		was = "not crossed off"
	}
	return fmt.Sprintf("%d. %s is %s", it.N, it.Text, was)
}

func todoVerbHelp(verb string) string {
	switch verb {
	case "done":
		return "cross one off by its number or a word from it"
	case "undo":
		return "put one back by its number or a word from it"
	}
	return "drop one by its number or a word from it"
}

// todoChoices offers the candidates with the line that picks each one.
// Nine at most: a question with thirty answers is not a question.
func todoChoices(verb string, pool []home.Item) []TodoChoice {
	var out []TodoChoice
	for _, it := range pool {
		if len(out) == 9 {
			break
		}
		c := TodoChoice{Label: it.Text, Line: fmt.Sprintf("%s %d", verb, it.N)}
		if it.From != "" {
			c.Detail = "from " + it.From
		}
		out = append(out, c)
	}
	return out
}

// proseNudge is the one place the command mentions the model, and it
// mentions it as an alternative rather than reaching for it.
func proseNudge(mind bool) string {
	if !mind {
		return ""
	}
	return "or just tell her — \"mark the bank one done\" reaches the same list"
}

func saidAbout(verb string, touched []home.Item) string {
	if len(touched) == 0 {
		return "nothing changed"
	}
	word := map[string]string{"done": "crossed off", "undo": "put back", "drop": "dropped"}[verb]
	if len(touched) == 1 {
		return word + " " + strconv.Itoa(touched[0].N) + ". " + touched[0].Text
	}
	return fmt.Sprintf("%s %d %s", word, len(touched), plural(len(touched), "item"))
}

func countSaid(items []home.Item) string {
	open := len(home.Remaining(items))
	if len(items) == 0 {
		return "nothing on the list"
	}
	if open == 0 {
		return "all done"
	}
	return fmt.Sprintf("%d %s to do", open, plural(open, "thing"))
}

// --- her hands ------------------------------------------------------------

// TodoTool is the list as a model reaches it. It exists so that the
// fluent path and the exact path are the same list: what she writes
// here is what `vera todo` prints, byte for byte, because both are
// this file.
//
// The description works hard to keep it away from the fleet. The two
// nouns are close enough in English that a model with both will reach
// for the wrong one unless told plainly which is which, and a "call
// the dentist" that opens a Claude Code agent in a repository is a
// funnier failure to read about than to have.
type TodoTool struct {
	// List is the file. Nil is not registered.
	List *home.List
}

func (t *TodoTool) Name() string { return "todo" }

func (t *TodoTool) Description() string { return todoDescription }

const todoDescription = "The person's own to-do list — the things THEY have to do, most of which are not " +
	"about code at all: call the bank, book a flight, buy a present. Use `list` when they ask what is on " +
	"it or what they should be doing; `add` when they say they need to do something, or ask to be " +
	"reminded of it; `done` when they say they have done one; `drop` when it turns out not to be a thing. " +
	"This is NOT the fleet: the fleet is agents working in repositories, and anything that means writing " +
	"or reading code goes there instead. Add what they said, in their words — an item is a note to " +
	"themselves, not a brief for you. If it is too vague to be useful next week, ask them what they mean " +
	"before writing it down."

func (t *TodoTool) Schema() json.RawMessage { return json.RawMessage(todoSchema) }

const todoSchema = `{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["list", "add", "done", "undo", "drop"]
    },
    "text": {
      "type": "string",
      "description": "add: the thing to do, in their own words, one line."
    },
    "item": {
      "type": "string",
      "description": "done/undo/drop: which one — the number from list, or enough words from it to be unambiguous."
    }
  },
  "required": ["action"]
}`

type todoArgs struct {
	Action string `json:"action"`
	Text   string `json:"text"`
	Item   string `json:"item"`
}

// Scope keeps an "always" to the verb that was asked about. Somebody
// who said always to `add` has not said she may drop things.
func (t *TodoTool) Scope(args json.RawMessage) string {
	var a todoArgs
	if json.Unmarshal(args, &a) != nil {
		return ""
	}
	return a.Action
}

func (t *TodoTool) Run(ctx context.Context, args json.RawMessage, h tool.Handle) (tool.Result, error) {
	if t.List == nil {
		return tool.Result{}, errors.New("there is no home to keep a list in")
	}
	var a todoArgs
	if json.Unmarshal(args, &a) != nil || a.Action == "" {
		return tool.Result{}, errors.New("the request was not readable")
	}
	line := ""
	switch a.Action {
	case "list":
		line = "all"
	case "add":
		if strings.TrimSpace(a.Text) == "" {
			return tool.Result{}, errors.New("an item needs words: say what has to be done")
		}
		// Through the same parser, but never through its verbs: an
		// item that starts with the word "done" is still an item.
		it, after, err := t.List.Add(a.Text, "vera")
		if err != nil {
			return tool.Result{}, err
		}
		return tool.Result{Text: fmt.Sprintf("Added %d. %s\n\n%s", it.N, it.Text, todoForModel(after))}, nil
	default:
		if strings.TrimSpace(a.Item) == "" {
			return tool.Result{}, fmt.Errorf("say which item to %s — the number from list, or words from it", a.Action)
		}
		line = a.Action + " " + a.Item
	}
	ans, err := doTodo(t.List, TodoRequest{Line: line, From: "vera"}, true)
	if err != nil {
		return tool.Result{}, err
	}
	if ans.Question != "" {
		// The model is told the question rather than being made to
		// guess. It has the person right there and can simply ask.
		var b strings.Builder
		b.WriteString(ans.Question + "\n")
		for _, c := range ans.Choices {
			b.WriteString("- " + c.Line + " — " + c.Label + "\n")
		}
		return tool.Result{Text: b.String()}, nil
	}
	return tool.Result{Text: ans.Said + "\n\n" + todoForModel(ans.Items)}, nil
}

// todoForModel is the list without the ceremony a screen wants.
func todoForModel(items []home.Item) string {
	if len(items) == 0 {
		return "The list is empty."
	}
	var b strings.Builder
	b.WriteString("The list now:\n")
	for _, it := range items {
		box := "[ ]"
		if it.Done {
			box = "[x]"
		}
		fmt.Fprintf(&b, "%d. %s %s\n", it.N, box, it.Text)
	}
	return b.String()
}
