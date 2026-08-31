package home

import (
	"os"
	"strings"
	"testing"
)

func texts(items []Item) []string {
	var out []string
	for _, it := range items {
		box := " "
		if it.Done {
			box = "x"
		}
		out = append(out, box+" "+it.Text)
	}
	return out
}

func TestAnItemIsALineAndComesBackWhole(t *testing.T) {
	l := fresh(t).Todo()
	if _, _, err := l.Add("Call the bank about the mortgage", "chat"); err != nil {
		t.Fatal(err)
	}
	it, after, err := l.Add("Renew the passport", "vera")
	if err != nil {
		t.Fatal(err)
	}
	if it.N != 2 {
		t.Errorf("the added item came back numbered %d, want 2", it.N)
	}
	if got := texts(after); len(got) != 2 || got[1] != "  Renew the passport" {
		t.Fatalf("list is %q", got)
	}

	// Crossed off in place, and the numbers do not move under it.
	touched, after, err := l.Mark(true, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 1 || !touched[0].Done {
		t.Fatalf("marking touched %v", touched)
	}
	if got := texts(after); got[0] != "x Call the bank about the mortgage" || got[1] != "  Renew the passport" {
		t.Errorf("after done: %q", got)
	}
	if after[1].N != 2 {
		t.Errorf("numbers moved when one was crossed off: %d", after[1].N)
	}

	// And back again.
	if _, after, err = l.Mark(false, []int{1}); err != nil {
		t.Fatal(err)
	}
	if after[0].Done {
		t.Error("undo left it done")
	}
}

// The promise the format makes: anything that is not an item is left
// exactly where it was. A list that eats the note you wrote above it
// is a list you stop keeping things in.
func TestWhatYouWroteYourselfIsNeverRewritten(t *testing.T) {
	h := fresh(t)
	l := h.Todo()
	raw := "# todo\n\nMine, and I mean it.\n\n## this week\n\n- [ ] one\n- [x] two\n\n> a note at the bottom\n"
	if err := os.WriteFile(l.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.Add("three", "chat"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.Mark(true, []int{1}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(l.Path())
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"Mine, and I mean it.", "## this week", "> a note at the bottom"} {
		if !strings.Contains(got, want) {
			t.Errorf("the file lost %q:\n%s", want, got)
		}
	}
	// The new item goes after the last one, not after the paragraph.
	if i, j := strings.Index(got, "three"), strings.Index(got, "> a note"); i < 0 || i > j {
		t.Errorf("the new item did not land inside the list:\n%s", got)
	}
	if !strings.Contains(got, "- [x] one") {
		t.Errorf("one was not crossed off:\n%s", got)
	}
}

// A line somebody typed by hand, with no bookkeeping on it, is a whole
// item. That is the point of choosing a format people already write.
func TestAHandWrittenLineCounts(t *testing.T) {
	l := fresh(t).Todo()
	if err := os.WriteFile(l.Path(), []byte("* [ ]   ring mum\n  + [X] pay rent\n- [] not an item\n- [ ]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := l.All()
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(items); len(got) != 2 || got[0] != "  ring mum" || got[1] != "x pay rent" {
		t.Fatalf("read %q", got)
	}
	// And its bullet and indent survive being crossed off.
	if _, _, err := l.Mark(true, []int{1}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(l.Path())
	if !strings.Contains(string(b), "* [x] ring mum") || !strings.Contains(string(b), "  + [X] pay rent") {
		t.Errorf("the file's own shape was not kept:\n%s", b)
	}
}

func TestBookkeepingRidesInACommentAndIsNotTheItem(t *testing.T) {
	l := fresh(t).Todo()
	if _, _, err := l.Add("Renew the passport", "seths phone (cli)"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(l.Path())
	line := string(b)
	if !strings.Contains(line, "<!-- added=") || !strings.Contains(line, "from=seths-phone-cli") {
		t.Errorf("no bookkeeping on the line:\n%s", line)
	}
	items, _ := l.All()
	if items[0].Text != "Renew the passport" {
		t.Errorf("the comment leaked into the item: %q", items[0].Text)
	}
	if items[0].From != "seths-phone-cli" || items[0].Added.IsZero() {
		t.Errorf("bookkeeping did not come back: %+v", items[0])
	}
	// An item that IS a comment is not bookkeeping.
	if _, _, err := l.Add("<!-- why is this here -->", "chat"); err != nil {
		t.Fatal(err)
	}
	items, _ = l.All()
	if items[1].Text != "<!-- why is this here -->" {
		t.Errorf("an item that is a comment was eaten: %q", items[1].Text)
	}
}

func TestDroppingAndClearing(t *testing.T) {
	l := fresh(t).Todo()
	for _, s := range []string{"one", "two", "three"} {
		if _, _, err := l.Add(s, "chat"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := l.Mark(true, []int{1, 3}); err != nil {
		t.Fatal(err)
	}
	gone, after, err := l.Clear()
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 2 {
		t.Errorf("cleared %d, want 2", len(gone))
	}
	if got := texts(after); len(got) != 1 || got[0] != "  two" {
		t.Fatalf("after clear: %q", got)
	}
	if _, after, err = l.Drop([]int{1}); err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Errorf("drop left %q", texts(after))
	}
}

// Matching answers in tiers and stops at the first that has anything,
// so a reference that worked yesterday cannot quietly start meaning
// something else because a vaguer rule found more.
func TestMatchingAnswersInTiers(t *testing.T) {
	items := []Item{
		{N: 1, Text: "Call the bank about the mortgage"},
		{N: 2, Text: "Call the bank"},
		{N: 3, Text: "Renew the passport"},
	}
	for _, tc := range []struct {
		ref  string
		want []int
	}{
		{"2", []int{2}},
		{"9", nil},
		{"call the bank", []int{2}},      // exact beats prefix
		{"call", []int{1, 2}},            // prefix, both
		{"passport", []int{3}},           // substring
		{"bank mortgage", []int{1}},      // every word
		{"mortgage bank call", []int{1}}, // order does not matter
		{"nothing like this", nil},
		{"", nil},
	} {
		var got []int
		for _, it := range Match(items, tc.ref) {
			got = append(got, it.N)
		}
		if len(got) != len(tc.want) {
			t.Errorf("%q matched %v, want %v", tc.ref, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q matched %v, want %v", tc.ref, got, tc.want)
				break
			}
		}
	}
}

func TestAMissingFileIsAnEmptyListAndTheNextAddRemakesIt(t *testing.T) {
	l := fresh(t).Todo()
	if err := os.Remove(l.Path()); err != nil {
		t.Fatal(err)
	}
	items, err := l.All()
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if _, after, err := l.Add("back again", "chat"); err != nil || len(after) != 1 {
		t.Fatalf("after=%v err=%v", after, err)
	}
	b, _ := os.ReadFile(l.Path())
	if !strings.Contains(string(b), "# todo") {
		t.Errorf("the remade file has no words at the top:\n%s", b)
	}
}

func TestAddingNothingIsRefused(t *testing.T) {
	l := fresh(t).Todo()
	if _, _, err := l.Add("   ", "chat"); err == nil {
		t.Error("adding whitespace should say so")
	}
}
