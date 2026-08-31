// The list: what the person still has to do, one Markdown checklist.
//
// Memory is what is still true tomorrow. This is what is not done yet,
// and the difference matters — a fact is replaced when it changes, an
// item is CROSSED OFF, and the crossing off is the whole point of
// having written it down.
//
// It is one file rather than a directory because a list is read whole
// or not at all: you do not grep a to-do list, you look at it. And it
// is Markdown with `- [ ]` boxes because that is the checklist every
// editor, every previewer and every person already renders — the same
// bet home makes everywhere else, that a thing you can open and fix by
// hand beats a thing only the program can read.
//
// Two rules the code keeps and the format does not say out loud:
//
// Lines that are not items are LEFT ALONE, exactly where they are.
// MEMORY.md can be regenerated because the memory files are the truth
// and the index is derived; here the file is the only truth there is,
// so a heading you wrote, a blank line you left, a paragraph of
// context above a group — none of it is Vera's to throw away. Only
// item lines are ever rewritten, and only in place.
//
// And items keep their numbers. The number beside an item is its
// position in the FILE, done ones counted, so that `todo done 3` means
// the same thing five seconds after the list was printed as it did
// when it was printed. The display groups open before done; the
// numbering does not move with it.
package home

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Todo is the file. It sits beside MEMORY.md rather than under
// notes/, because it is the person's and notes/ is hers.
const Todo = "TODO.md"

// todoPreamble is written once, when there is no file. After that it
// is an ordinary non-item line and nothing rewrites it — including a
// person who deletes it.
const todoPreamble = `# todo

Yours and Vera's, the same list — ` + "`vera todo`" + `, ` + "`/todo`" + ` in the chat, or
just tell her. Edit it by hand whenever you like: a line beginning
` + "`- [ ]`" + ` is open and ` + "`- [x]`" + ` is done. Everything else in this file is
yours and is never rewritten.

`

// Item is one line of the list.
type Item struct {
	// N is its number: its position among the items in the file,
	// from 1, done ones counted. It is what a person types.
	N    int    `json:"n"`
	Text string `json:"text"`
	Done bool   `json:"done"`
	// Added and Did are days, not moments. A to-do list that knows
	// what minute you wrote something down is keeping a record of you
	// rather than a list for you.
	Added time.Time `json:"added,omitzero"`
	Did   time.Time `json:"did,omitzero"`
	// From is where it came from — "chat", "vera", a device name.
	From string `json:"from,omitempty"`

	// line is which line of the file it is, so a rewrite can change
	// that one and leave every other byte alone.
	line int
}

// List is the file, opened. Every call re-reads it: the person has an
// editor, Vera has tools, and the one thing a shared list must never
// do is write back what it read a minute ago.
type List struct{ home *Home }

// Todo is the list.
func (h *Home) Todo() *List { return &List{home: h} }

// Path is the file, for a message that has to say where it is.
func (l *List) Path() string { return l.home.path(Todo) }

// All is the list as it stands, in file order.
func (l *List) All() ([]Item, error) {
	l.home.mu.Lock()
	defer l.home.mu.Unlock()
	_, items, err := l.read()
	return items, err
}

// read returns the file's lines and the items in them. A missing file
// is an empty list, not an error: somebody deleted it, and the next
// thing added makes it again.
func (l *List) read() ([]string, []Item, error) {
	b, err := os.ReadFile(l.Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	var items []Item
	for i, line := range lines {
		it, ok := parseItem(line)
		if !ok {
			continue
		}
		it.line = i
		it.N = len(items) + 1
		items = append(items, it)
	}
	return lines, items, nil
}

// parseItem reads one line. It is forgiving on purpose — `- [ ]`,
// `* [x]`, any indent, any spacing — because the whole promise is that
// a line you typed yourself counts.
func parseItem(line string) (Item, bool) {
	s := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(s, "- ") && !strings.HasPrefix(s, "* ") && !strings.HasPrefix(s, "+ ") {
		return Item{}, false
	}
	s = strings.TrimLeft(s[2:], " ")
	if len(s) < 3 || s[0] != '[' || s[2] != ']' {
		return Item{}, false
	}
	var done bool
	switch s[1] {
	case ' ':
	case 'x', 'X':
		done = true
	default:
		return Item{}, false
	}
	it := Item{Done: done, Text: strings.TrimSpace(s[3:])}
	it.Text, it.Added, it.Did, it.From = splitNote(it.Text)
	if it.Text == "" {
		return Item{}, false
	}
	return it, true
}

// splitNote pulls Vera's bookkeeping off the end of an item. It rides
// in an HTML comment so that every Markdown renderer hides it and
// every text editor shows it, and a line without one is still a whole
// item — which is what a line a person typed looks like.
func splitNote(text string) (rest string, added, did time.Time, from string) {
	i := strings.LastIndex(text, "<!--")
	if i < 0 || !strings.HasSuffix(strings.TrimSpace(text), "-->") {
		return text, time.Time{}, time.Time{}, ""
	}
	note := strings.TrimSpace(text[i+4 : strings.LastIndex(text, "-->")])
	rest = strings.TrimSpace(text[:i])
	if rest == "" {
		// A comment and nothing else is not bookkeeping, it is the item.
		return text, time.Time{}, time.Time{}, ""
	}
	for _, field := range strings.Fields(note) {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "added":
			added, _ = time.Parse(dayFormat, val)
		case "did":
			did, _ = time.Parse(dayFormat, val)
		case "from":
			from = val
		}
	}
	return rest, added, did, from
}

const dayFormat = "2006-01-02"

// render writes one item back as a line, keeping whatever indent and
// bullet the file already used for it.
func renderItem(it Item, was string) string {
	indent, bullet := "", "-"
	if was != "" {
		trimmed := strings.TrimLeft(was, " \t")
		indent = was[:len(was)-len(trimmed)]
		if len(trimmed) > 0 {
			bullet = trimmed[:1]
		}
	}
	box := "[ ]"
	if it.Done {
		box = "[x]"
	}
	line := indent + bullet + " " + box + " " + it.Text
	if note := renderNote(it); note != "" {
		line += " <!-- " + note + " -->"
	}
	return line
}

func renderNote(it Item) string {
	var parts []string
	if !it.Added.IsZero() {
		parts = append(parts, "added="+it.Added.Format(dayFormat))
	}
	if it.Done && !it.Did.IsZero() {
		parts = append(parts, "did="+it.Did.Format(dayFormat))
	}
	if it.From != "" {
		parts = append(parts, "from="+strings.Join(strings.Fields(it.From), "-"))
	}
	return strings.Join(parts, " ")
}

// --- changing it ----------------------------------------------------------

// Add appends one item and hands back what the list looks like after.
// The text is taken verbatim: nothing paraphrases what somebody wrote
// down for themselves.
func (l *List) Add(text, from string) (Item, []Item, error) {
	text = oneLine(text)
	if text == "" {
		return Item{}, nil, fmt.Errorf("nothing to add")
	}
	l.home.mu.Lock()
	defer l.home.mu.Unlock()
	lines, items, err := l.read()
	if err != nil {
		return Item{}, nil, err
	}
	// Appended after the last item, so its number is the next one.
	it := Item{N: len(items) + 1, Text: text, Added: today(), From: from}
	// After the last item, so a file that ends in a paragraph of your
	// own keeps the list above it rather than growing into it.
	at := len(lines)
	if n := len(items); n > 0 {
		at = items[n-1].line + 1
	} else if len(lines) == 0 {
		lines = strings.Split(strings.TrimRight(todoPreamble, "\n"), "\n")
		at = len(lines)
	}
	lines = append(lines[:at], append([]string{renderItem(it, "")}, lines[at:]...)...)
	after, err := l.writeBack(lines)
	return it, after, err
}

// Mark crosses items off, or puts them back. It is one call rather
// than two because "done" and "not done" is one property, and a caller
// that has resolved which items it means should not care which way it
// is pushing them.
func (l *List) Mark(done bool, ns []int) ([]Item, []Item, error) {
	return l.change(ns, func(it *Item) {
		it.Done = done
		if done {
			it.Did = today()
		} else {
			it.Did = time.Time{}
		}
	})
}

// Drop removes items. Crossing off is the ordinary end of an item;
// this is for the ones that turned out not to be things at all.
func (l *List) Drop(ns []int) ([]Item, []Item, error) {
	return l.change(ns, nil)
}

// Clear drops every item already done — the tidy, once the crossing
// off has piled up.
func (l *List) Clear() ([]Item, []Item, error) {
	items, err := l.All()
	if err != nil {
		return nil, nil, err
	}
	var ns []int
	for _, it := range items {
		if it.Done {
			ns = append(ns, it.N)
		}
	}
	return l.Drop(ns)
}

// change applies fn to the numbered items, or removes them when fn is
// nil, and hands back what it touched and what the list looks like
// after. A number nothing answers to is skipped rather than refused:
// the caller resolved these, and half a change is better than none.
func (l *List) change(ns []int, fn func(*Item)) ([]Item, []Item, error) {
	l.home.mu.Lock()
	defer l.home.mu.Unlock()
	lines, items, err := l.read()
	if err != nil {
		return nil, nil, err
	}
	want := map[int]bool{}
	for _, n := range ns {
		want[n] = true
	}
	var touched []Item
	var drop []int
	for i := range items {
		if !want[items[i].N] {
			continue
		}
		if fn == nil {
			touched = append(touched, items[i])
			drop = append(drop, items[i].line)
			continue
		}
		fn(&items[i])
		lines[items[i].line] = renderItem(items[i], lines[items[i].line])
		touched = append(touched, items[i])
	}
	if len(drop) > 0 {
		sort.Sort(sort.Reverse(sort.IntSlice(drop)))
		for _, at := range drop {
			lines = append(lines[:at], lines[at+1:]...)
		}
	}
	if len(touched) == 0 {
		return nil, items, nil
	}
	after, err := l.writeBack(lines)
	return touched, after, err
}

// writeBack puts the lines down and reads what is now there, so a
// caller always answers from the file rather than from its own idea
// of what it just wrote.
func (l *List) writeBack(lines []string) ([]Item, error) {
	if err := write(l.Path(), strings.Join(lines, "\n")+"\n"); err != nil {
		return nil, err
	}
	_, items, err := l.read()
	return items, err
}

// today is the day, without the time of day.
func today() time.Time {
	y, m, d := time.Now().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// --- finding one ----------------------------------------------------------

// Match resolves what a person typed into the items they meant. It is
// deliberately a pure function over a list somebody else filtered, so
// that `done` can look only at open items and `undo` only at closed
// ones without this knowing either verb exists.
//
// It answers in tiers and stops at the first that has anything: a
// number, then the whole text, then a prefix, then a substring, then
// every word of the reference appearing somewhere in the item. That
// order is what makes it predictable — a reference that matched
// exactly yesterday cannot start matching something else today because
// a vaguer rule found more.
//
// More than one answer is not an error here. Ambiguity is a question,
// and the caller is the one with somewhere to ask it.
func Match(items []Item, ref string) []Item {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	if n, err := strconv.Atoi(ref); err == nil {
		for _, it := range items {
			if it.N == n {
				return []Item{it}
			}
		}
		return nil
	}
	low := strings.ToLower(ref)
	tiers := []func(text string) bool{
		func(t string) bool { return t == low },
		func(t string) bool { return strings.HasPrefix(t, low) },
		func(t string) bool { return strings.Contains(t, low) },
		func(t string) bool { return hasEveryWord(t, low) },
	}
	for _, fits := range tiers {
		var out []Item
		for _, it := range items {
			if fits(strings.ToLower(it.Text)) {
				out = append(out, it)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// hasEveryWord is the loosest tier: "bank mortgage" finds "Call the
// bank about the mortgage". Words of one letter are ignored, and a
// reference of nothing but those matches nothing.
func hasEveryWord(text, ref string) bool {
	words := 0
	for _, w := range strings.FieldsFunc(ref, notLetterOrDigit) {
		if len(w) < 2 {
			continue
		}
		words++
		if !strings.Contains(text, w) {
			return false
		}
	}
	return words > 0
}

func notLetterOrDigit(r rune) bool {
	return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
}

// Remaining is the items still to do. (Not Open — that is how a home
// is opened, and one of the two had to give way.)
func Remaining(items []Item) []Item { return filterDone(items, false) }

// Crossed is the ones already done.
func Crossed(items []Item) []Item { return filterDone(items, true) }

func filterDone(items []Item, done bool) []Item {
	var out []Item
	for _, it := range items {
		if it.Done == done {
			out = append(out, it)
		}
	}
	return out
}

// --- as a person reads it -------------------------------------------------

// TodoMarkdown is the list as a person reads it: what is left first,
// because that is what a list is for, then what was crossed off, which
// is there to be seen and then cleared. The numbers are the file's, so
// a number read here is a number that can be typed back.
func TodoMarkdown(items []Item, path string, all bool) string {
	open, done := Remaining(items), Crossed(items)
	var b strings.Builder
	if len(items) == 0 {
		return "Nothing on the list. `/todo <something>` puts it there.\n\n`" + path + "`"
	}
	for _, it := range open {
		fmt.Fprintf(&b, "%d. %s\n", it.N, it.Text)
	}
	if len(open) == 0 {
		b.WriteString("Nothing left to do.\n")
	}
	if len(done) > 0 {
		shown := done
		if !all && len(shown) > 3 {
			shown = shown[len(shown)-3:]
		}
		b.WriteString("\n")
		for _, it := range shown {
			fmt.Fprintf(&b, "%d. ~~%s~~\n", it.N, it.Text)
		}
		if n := len(done) - len(shown); n > 0 {
			fmt.Fprintf(&b, "\n%d more crossed off — `/todo all` shows them, `/todo clear` sweeps them.\n", n)
		}
	}
	fmt.Fprintf(&b, "\n`%s`", path)
	return b.String()
}
