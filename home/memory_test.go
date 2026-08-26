package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fresh(t *testing.T) *Home {
	t.Helper()
	h, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestTheLayoutIsMadeOnce(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vera")
	h, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{Index, MemoryDir, ProjectsDir, NotesDir, ProfileDir} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s was not made: %v", want, err)
		}
	}
	// A directory about a person is not a directory for everyone.
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("home is %o, want 700", perm)
	}

	// Opening again keeps what is there rather than starting over.
	if err := os.WriteFile(filepath.Join(root, Index), []byte("- mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, Index))
	if string(b) != "- mine\n" {
		t.Errorf("re-opening rewrote the index: %q", b)
	}
	_ = h
}

// The round trip that matters: a fact written as a file comes back as
// the same fact, and the index names it.
func TestAFactIsAFileAndAnIndexLine(t *testing.T) {
	h := fresh(t)
	m := h.Memory()
	if err := m.Apply(Revision{Add: []Note{
		{Name: "lives-in-vienna", Type: TypeUser, Fact: "Lives in Vienna."},
		{Type: TypeFeedback, Fact: "Wants short answers, and no bullet points."},
	}}, "c1"); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(h.Root, MemoryDir, "lives-in-vienna.md"))
	if err != nil {
		t.Fatalf("the fact is not a file: %v", err)
	}
	for _, want := range []string{"name: lives-in-vienna", "type: user", "since: " + time.Now().Format("2006-01-02"), "from: c1", "\nLives in Vienna.\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the file does not carry %q:\n%s", want, b)
		}
	}

	// A missing name is made from the fact, so a model that forgets the
	// field still gets a file somebody can find.
	if _, err := os.Stat(filepath.Join(h.Root, MemoryDir, "wants-short-answers-and-no-bullet-points.md")); err != nil {
		t.Errorf("a nameless fact was not filed: %v", err)
	}

	index, err := os.ReadFile(filepath.Join(h.Root, Index))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "- [lives-in-vienna](memory/lives-in-vienna.md) — Lives in Vienna.") {
		t.Errorf("the index does not name the fact:\n%s", index)
	}
	// And the index is what the prompt gets.
	if m.Recite() != string(index) {
		t.Errorf("recited something other than the index:\n%q\n%q", m.Recite(), index)
	}

	// A second process reading the same directory believes the same
	// things: the files are the memory, not what is in this one's head.
	again, err := Open(h.Root)
	if err != nil {
		t.Fatal(err)
	}
	facts := again.Memory().All()
	if len(facts) != 2 || facts[0].Body != "Lives in Vienna." || facts[0].Type != TypeUser {
		t.Fatalf("a restart read back %+v", facts)
	}
	if facts[1].Type != TypeFeedback {
		t.Errorf("the type did not survive: %+v", facts[1])
	}
}

func TestCorrectionRewritesRatherThanAccumulates(t *testing.T) {
	h := fresh(t)
	m := h.Memory()
	m.Apply(Revision{Add: []Note{{Name: "lives-in-denver", Fact: "Lives in Denver."}}}, "c1")
	m.Apply(Revision{Update: []Note{{Name: "lives-in-denver", Fact: "Lives in Austin."}}}, "c2")

	facts := m.All()
	if len(facts) != 1 {
		t.Fatalf("a correction left %d facts; both beliefs now survive: %+v", len(facts), facts)
	}
	if facts[0].Body != "Lives in Austin." {
		t.Fatalf("the correction did not take: %q", facts[0].Body)
	}
	if facts[0].Name != "lives-in-denver" {
		t.Fatalf("a corrected fact became a different file: %q", facts[0].Name)
	}
	// And the same revision applied twice changes nothing.
	before, _ := os.ReadFile(filepath.Join(h.Root, Index))
	m.Apply(Revision{Update: []Note{{Name: "lives-in-denver", Fact: "Lives in Austin."}}}, "c3")
	after, _ := os.ReadFile(filepath.Join(h.Root, Index))
	if string(before) != string(after) || m.Count() != 1 {
		t.Errorf("applying twice was not idempotent: %q → %q", before, after)
	}
}

func TestUpdatingSomethingUnknownKeepsTheContent(t *testing.T) {
	m := fresh(t).Memory()
	m.Apply(Revision{Update: []Note{{Name: "plays-the-cello", Fact: "Plays the cello."}}}, "c1")
	facts := m.All()
	if len(facts) != 1 || facts[0].Body != "Plays the cello." {
		t.Fatalf("content was lost with the unknown slug: %+v", facts)
	}
}

func TestForgettingByNothingAndBySlug(t *testing.T) {
	h := fresh(t)
	m := h.Memory()
	m.Apply(Revision{Add: []Note{{Name: "owns-a-dog", Fact: "Owns a dog."}, {Name: "owns-a-boat", Fact: "Owns a boat."}}}, "c1")

	// Replaced by nothing is forgotten.
	m.Apply(Revision{Update: []Note{{Name: "owns-a-dog", Fact: "  "}}}, "c2")
	if _, err := os.Stat(filepath.Join(h.Root, MemoryDir, "owns-a-dog.md")); !os.IsNotExist(err) {
		t.Error("the file outlived the fact")
	}
	// A retraction takes the file and the index line together.
	if n := m.Forget("owns-a-boat"); n != 1 {
		t.Fatalf("forgot %d", n)
	}
	index, _ := os.ReadFile(filepath.Join(h.Root, Index))
	if strings.Contains(string(index), "owns-a-boat") {
		t.Errorf("a forgotten fact is still in the index:\n%s", index)
	}
	if m.Count() != 0 {
		t.Fatalf("%d facts left", m.Count())
	}
}

// A person with an editor is the ordinary case, not a race to defend
// against: what they wrote is what Vera knows on the next exchange.
func TestAHandWrittenFileIsAFact(t *testing.T) {
	h := fresh(t)
	path := filepath.Join(h.Root, MemoryDir, "keeps-bees.md")
	if err := os.WriteFile(path, []byte("Keeps bees, two hives, since 2024.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	facts := h.Memory().All()
	if len(facts) != 1 || facts[0].Name != "keeps-bees" {
		t.Fatalf("the hand-written file was not read: %+v", facts)
	}
	if !strings.Contains(h.Memory().Recite(), "Keeps bees") {
		t.Errorf("it did not reach the prompt: %q", h.Memory().Recite())
	}
	// Deleting one is forgetting it.
	os.Remove(path)
	if h.Memory().Count() != 0 {
		t.Error("a deleted file is still remembered")
	}
}

func TestMemoryStopsGrowing(t *testing.T) {
	h := fresh(t)
	m := h.Memory()
	m.limit = 5
	for i := range 20 {
		m.Apply(Revision{Add: []Note{{Fact: string(rune('a'+i)) + " fact"}}}, "c")
		time.Sleep(2 * time.Millisecond)
	}
	if n := m.Count(); n > 5 {
		t.Fatalf("memory grew to %d past a limit of 5", n)
	}
	// The newest survive; a fact still true will be learned again.
	facts := m.All()
	if last := facts[len(facts)-1].Body; last != "t fact" {
		t.Fatalf("the most recent fact was evicted; kept %q", last)
	}
	// And the files went with them, not just the index lines.
	entries, _ := os.ReadDir(filepath.Join(h.Root, MemoryDir))
	if len(entries) != 5 {
		t.Fatalf("%d files for 5 facts — eviction left orphans", len(entries))
	}
}

func TestReciteIsEmptyWhenNothingIsKnown(t *testing.T) {
	if got := fresh(t).Memory().Recite(); got != "" {
		t.Fatalf("an empty memory recited %q, which would go into every prompt", got)
	}
}

// The cap is the guard for the day the index is not small. It takes
// the oldest and says that it did — a memory that silently sends less
// than everything is a memory that lies.
func TestTheIndexIsCappedOutLoud(t *testing.T) {
	h := fresh(t)
	m := h.Memory()
	m.promptCap = 300
	for i := range 20 {
		m.Apply(Revision{Add: []Note{{Name: "fact-" + string(rune('a'+i)), Fact: "Something worth knowing, number " + string(rune('a'+i)) + "."}}}, "c")
	}
	said := m.Recite()
	if len(said) > 400 {
		t.Fatalf("the cap did not hold: %d bytes", len(said))
	}
	if !strings.HasPrefix(said, "(") || !strings.Contains(said, "left out") {
		t.Fatalf("the trim was silent:\n%s", said)
	}
	if !strings.Contains(said, "fact-t") {
		t.Errorf("the newest memory was trimmed instead of the oldest:\n%s", said)
	}
	if strings.Contains(said, "fact-a]") {
		t.Errorf("the oldest survived the trim:\n%s", said)
	}
	// The file on disk is never capped: it is what a person reads.
	index, _ := os.ReadFile(filepath.Join(h.Root, Index))
	if len(index) <= len(said) {
		t.Errorf("MEMORY.md was capped too; it should hold everything (%d bytes)", len(index))
	}
}

// A model naming a fact "../../etc/passwd" must land in memory/ as a
// slug and nowhere else.
func TestSlugsCannotEscapeTheDirectory(t *testing.T) {
	h := fresh(t)
	h.Memory().Apply(Revision{Add: []Note{{Name: "../../escaped", Fact: "Nope."}}}, "c")
	if _, err := os.Stat(filepath.Join(filepath.Dir(h.Root), "escaped.md")); err == nil {
		t.Fatal("a fact was written outside home")
	}
	facts := h.Memory().All()
	if len(facts) != 1 || strings.ContainsAny(facts[0].Name, "./") {
		t.Fatalf("slug survived unsanitised: %+v", facts)
	}
}

func TestMigrationFromMemoryJSON(t *testing.T) {
	h := fresh(t)
	jsonPath := filepath.Join(t.TempDir(), "memory.json")
	fixture := `[
  {"id": 0, "text": "Lives in Vienna.", "learned": "2026-03-04T10:00:00Z", "from": "conv-1"},
  {"id": 1, "text": "Is rebuilding Vera on mote.", "learned": "2026-08-01T09:00:00Z"},
  {"id": 2, "text": "   ", "learned": "2026-08-01T09:00:00Z"}
]`
	if err := os.WriteFile(jsonPath, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}

	n, err := h.Migrate(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("migrated %d facts, want 2 (the blank one is not a fact)", n)
	}
	b, err := os.ReadFile(filepath.Join(h.Root, MemoryDir, "lives-in-vienna.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The day it was learned is part of the fact, and a migration that
	// stamps everything with today throws away when Vera knew it.
	if !strings.Contains(string(b), "since: 2026-03-04") || !strings.Contains(string(b), "from: conv-1") {
		t.Errorf("provenance was lost:\n%s", b)
	}
	index, _ := os.ReadFile(filepath.Join(h.Root, Index))
	if !strings.Contains(string(index), "rebuilding-vera-on-mote") {
		t.Errorf("the index missed a migrated fact:\n%s", index)
	}

	// Once, and the json is kept — it is what she believed.
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Error("memory.json was left in place; the next start would migrate it again")
	}
	if _, err := os.Stat(jsonPath + ".migrated"); err != nil {
		t.Errorf("the old memory was not kept: %v", err)
	}
	if n, err := h.Migrate(jsonPath); err != nil || n != 0 {
		t.Errorf("a second migration did %d facts (%v)", n, err)
	}
}
