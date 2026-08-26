// Memory: what is still true tomorrow, one file per fact.
//
// History is what survives a turn. Memory is what survives a RESTART,
// and that is the whole difference — a fact worth keeping is one that
// is still true in a conversation that has not happened yet.
//
// Three decisions worth arguing with, carried over from when this was
// a JSON array and still true:
//
// Every fact goes in the prompt; nothing is retrieved. One person
// accumulates tens to low hundreds of durable facts, which is small
// enough to send in full. Embeddings and similarity search are the
// right answer at thousands and a way of looking busy at fifty — and
// retrieval fails in the worst possible way, by silently not finding
// the thing that mattered. What goes in is the INDEX, one line each:
// the bodies are for a person, and shortly for Vera's own tools.
//
// Facts are REPLACED, not accumulated. Someone who moves from Denver
// to Austin has not become a person who lives in two places, and a
// memory that only ever appends turns into a pile of contradictions
// the model then arbitrates on every turn. The slug is what makes that
// mechanical: `lives-in-austin` written over `lives-in-denver` is an
// edit, and two files would have been a contradiction.
//
// And it is a directory, so somebody else may have changed it since
// the last look — a person with an editor today, Vera with tools soon.
// Every read checks and re-reads; every write derives the index from
// the files rather than trusting the copy in hand.
package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// The kinds, borrowed from the convention Claude Code uses for the
// same job, because a person moving between the two should not have to
// learn a second vocabulary.
const (
	TypeUser      = "user"      // who they are
	TypeFeedback  = "feedback"  // how they want to be worked with
	TypeProject   = "project"   // ongoing work and constraints
	TypeReference = "reference" // where something lives
)

// Fact is one thing Vera knows, and one file.
type Fact struct {
	Name        string    // the slug, which is also the file name
	Description string    // the one line that goes in the index
	Type        string    // user | feedback | project | reference
	Since       time.Time // the day it was learned
	From        string    // the conversation that taught it
	Body        string    // the fact itself, as prose
}

// Memory is the memory/ directory and the index over it.
type Memory struct {
	home  *Home
	limit int // a ceiling, because anything automatic accumulates
	// promptCap bounds what Recite hands to a prompt. The index is
	// small today and this is the guard for the day it is not.
	promptCap int

	facts []Fact
	stamp string // what the directory looked like when facts was read
}

// --- reading --------------------------------------------------------------

func (m *Memory) reload() error {
	dir := m.home.path(MemoryDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			m.facts, m.stamp = nil, ""
			return nil
		}
		return err
	}
	var facts []Fact
	var stamp strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(&stamp, "%s:%d:%d\n", e.Name(), info.Size(), info.ModTime().UnixNano())
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		f := parseFact(strings.TrimSuffix(e.Name(), ".md"), string(b))
		if f.Since.IsZero() {
			f.Since = info.ModTime()
		}
		facts = append(facts, f)
	}
	sortFacts(facts)
	m.facts, m.stamp = facts, stamp.String()
	return nil
}

// fresh re-reads only when the directory changed under us. A person
// with an editor is the ordinary case here, not a race to defend
// against.
func (m *Memory) fresh() {
	dir := m.home.path(MemoryDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		_ = m.reload()
		return
	}
	var stamp strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(&stamp, "%s:%d:%d\n", e.Name(), info.Size(), info.ModTime().UnixNano())
	}
	if stamp.String() != m.stamp {
		_ = m.reload()
	}
}

// Oldest first: the index reads as an accumulation, and a cap that
// trims takes the oldest, which is also what the ceiling evicts.
func sortFacts(facts []Fact) {
	sort.SliceStable(facts, func(i, j int) bool {
		if facts[i].Since.Equal(facts[j].Since) {
			return facts[i].Name < facts[j].Name
		}
		return facts[i].Since.Before(facts[j].Since)
	})
}

func (m *Memory) All() []Fact {
	m.home.mu.Lock()
	defer m.home.mu.Unlock()
	m.fresh()
	out := make([]Fact, len(m.facts))
	copy(out, m.facts)
	return out
}

func (m *Memory) Count() int {
	m.home.mu.Lock()
	defer m.home.mu.Unlock()
	m.fresh()
	return len(m.facts)
}

// Recite is what goes in the prompt: MEMORY.md, whole, up to the cap.
// Over the cap the oldest lines go and a note says so — silently
// sending less than everything is how a memory starts lying.
func (m *Memory) Recite() string {
	m.home.mu.Lock()
	defer m.home.mu.Unlock()
	m.fresh()
	return capIndex(renderIndex(m.facts), m.promptCap)
}

func capIndex(index string, limit int) string {
	if limit <= 0 || len(index) <= limit {
		return index
	}
	lines := strings.Split(strings.TrimRight(index, "\n"), "\n")
	kept, size := []string{}, 0
	for i := len(lines) - 1; i >= 0; i-- {
		size += len(lines[i]) + 1
		if size > limit {
			break
		}
		kept = append([]string{lines[i]}, kept...)
	}
	dropped := len(lines) - len(kept)
	if dropped <= 0 {
		return index
	}
	return fmt.Sprintf("(%d older %s left out — the memory files hold them.)\n", dropped, plural(dropped, "memory is", "memories are")) +
		strings.Join(kept, "\n") + "\n"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// --- the index ------------------------------------------------------------

// renderIndex builds MEMORY.md from the files. Derived, never
// accumulated: the files are the truth, so an index that was edited by
// hand, or written by an older version, heals on the next write.
func renderIndex(facts []Fact) string {
	var b strings.Builder
	for _, f := range facts {
		fmt.Fprintf(&b, "- [%s](%s/%s.md) — %s\n", f.Name, MemoryDir, f.Name, oneLine(f.Description))
	}
	return b.String()
}

func (m *Memory) writeIndex() error {
	return write(m.home.path(Index), renderIndex(m.facts))
}

// --- one file -------------------------------------------------------------

func renderFact(f Fact) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", f.Name)
	fmt.Fprintf(&b, "description: %s\n", oneLine(f.Description))
	fmt.Fprintf(&b, "type: %s\n", f.Type)
	fmt.Fprintf(&b, "since: %s\n", f.Since.Format("2006-01-02"))
	if f.From != "" {
		fmt.Fprintf(&b, "from: %s\n", f.From)
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(f.Body, "\n") + "\n")
	return b.String()
}

// parseFact is forgiving. A file a person wrote by hand with no front
// matter at all is still a fact — the name comes from the file and the
// description from the first line, which is what they meant.
func parseFact(name, raw string) Fact {
	f := Fact{Name: name, Type: TypeUser}
	body := strings.TrimLeft(raw, "\n")
	if rest, ok := strings.CutPrefix(body, "---\n"); ok {
		if head, tail, found := strings.Cut(rest, "\n---"); found {
			for _, line := range strings.Split(head, "\n") {
				key, val, ok := strings.Cut(strings.TrimSpace(line), ":")
				if !ok {
					continue
				}
				val = strings.TrimSpace(val)
				switch strings.ToLower(strings.TrimSpace(key)) {
				case "name":
					if s := slug(val); s != "" {
						f.Name = s
					}
				case "description":
					f.Description = val
				case "type":
					f.Type = kind(val)
				case "since":
					if t, err := time.Parse("2006-01-02", val); err == nil {
						f.Since = t
					}
				case "from":
					f.From = val
				}
			}
			body = strings.TrimPrefix(tail, "\n---")
		}
	}
	f.Body = strings.TrimSpace(body)
	if f.Description == "" {
		f.Description = oneLine(f.Body)
	}
	if f.Body == "" {
		f.Body = f.Description
	}
	return f
}

func kind(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case TypeFeedback:
		return TypeFeedback
	case TypeProject:
		return TypeProject
	case TypeReference:
		return TypeReference
	default:
		return TypeUser
	}
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) > 200 {
		s = strings.TrimSpace(s[:200]) + "…"
	}
	return s
}

// slug is the file name, and it is the only thing standing between a
// model's output and the filesystem — so it produces [a-z0-9-] and
// nothing else, ever.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) && r < unicode.MaxASCII || unicode.IsDigit(r) && r < unicode.MaxASCII:
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	// Long enough to read, short enough to live in a directory listing.
	if len(out) > 60 {
		out = out[:60]
		if i := strings.LastIndexByte(out, '-'); i > 20 {
			out = out[:i]
		}
		out = strings.Trim(out, "-")
	}
	return out
}

// --- writing --------------------------------------------------------------

// Note is one fact as the extractor writes it.
type Note struct {
	// Name is the slug it wants. Empty means "make one from the fact",
	// which is what a model that forgot the field meant anyway.
	Name        string `json:"name"`
	Type        string `json:"type"`
	Fact        string `json:"fact"`
	Description string `json:"description,omitempty"`
}

// Revision is what the extractor decided about one exchange.
type Revision struct {
	Add    []Note   `json:"add"`
	Update []Note   `json:"update"`
	Remove []string `json:"remove"`
}

func (r Revision) Empty() bool {
	return len(r.Add) == 0 && len(r.Update) == 0 && len(r.Remove) == 0
}

// Counts is what to say in a log line.
func (r Revision) Counts() (added, updated, removed int) {
	return len(r.Add), len(r.Update), len(r.Remove)
}

// Apply commits a revision. It is idempotent on purpose: the same
// revision applied twice writes the same files and leaves the same
// index, because everything is keyed by slug rather than appended.
func (m *Memory) Apply(r Revision, from string) error {
	m.home.mu.Lock()
	defer m.home.mu.Unlock()
	m.fresh()

	now := time.Now()
	for _, name := range r.Remove {
		m.drop(slug(name))
	}
	// An update whose slug is unknown is not thrown away. The model
	// invents ids and slugs; the content is usually fine even when the
	// bookkeeping is not.
	for _, n := range append(append([]Note{}, r.Update...), r.Add...) {
		m.put(n, from, now)
	}

	// Oldest out at the ceiling. A fact still true will be learned
	// again; one that is not should not have survived.
	for len(m.facts) > m.limit {
		m.drop(m.facts[0].Name)
	}

	sortFacts(m.facts)
	if err := m.writeIndex(); err != nil {
		return err
	}
	return m.reload()
}

// put writes one fact, keeping the day it was first learned if this is
// an edit of something already known.
func (m *Memory) put(n Note, from string, now time.Time) {
	body := strings.TrimSpace(n.Fact)
	name := slug(n.Name)
	if name == "" {
		name = slug(oneLine(body))
	}
	if name == "" {
		return
	}
	// A fact replaced by nothing is a fact forgotten.
	if body == "" {
		m.drop(name)
		return
	}
	f := Fact{
		Name:        name,
		Description: oneLine(firstNonEmpty(n.Description, body)),
		Type:        kind(n.Type),
		Since:       now,
		From:        from,
		Body:        body,
	}
	at := -1
	for i := range m.facts {
		if m.facts[i].Name == name {
			at = i
			break
		}
	}
	if at >= 0 {
		f.Since = m.facts[at].Since
		m.facts[at] = f
	} else {
		m.facts = append(m.facts, f)
	}
	if err := write(m.home.path(MemoryDir, name+".md"), renderFact(f)); err != nil {
		return
	}
}

func (m *Memory) drop(name string) bool {
	if name == "" {
		return false
	}
	kept := m.facts[:0]
	found := false
	for _, f := range m.facts {
		if f.Name == name {
			found = true
			continue
		}
		kept = append(kept, f)
	}
	m.facts = kept
	if err := os.Remove(m.home.path(MemoryDir, name+".md")); err == nil {
		found = true
	}
	return found
}

// Forget removes facts by slug and says how many went.
func (m *Memory) Forget(names ...string) int {
	m.home.mu.Lock()
	defer m.home.mu.Unlock()
	m.fresh()
	n := 0
	for _, name := range names {
		if m.drop(slug(name)) {
			n++
		}
	}
	_ = m.writeIndex()
	_ = m.reload()
	return n
}

func (m *Memory) ForgetAll() int {
	m.home.mu.Lock()
	defer m.home.mu.Unlock()
	m.fresh()
	n := 0
	for _, f := range append([]Fact{}, m.facts...) {
		if m.drop(f.Name) {
			n++
		}
	}
	_ = m.writeIndex()
	_ = m.reload()
	return n
}

// --- migration ------------------------------------------------------------

// oldFact is memory.json's shape, from before this was a directory.
type oldFact struct {
	ID      int       `json:"id"`
	Text    string    `json:"text"`
	Learned time.Time `json:"learned"`
	From    string    `json:"from,omitempty"`
}

// Migrate folds an old memory.json into the home, once, and renames it
// to .migrated so a second start does not do it again. A fact whose
// slug is already a file is left alone: the file is newer than the
// json by definition.
//
// The json is kept rather than deleted. It is what she believed, and
// the day the migration turns out to have mangled something is the day
// somebody will want it.
func (h *Home) Migrate(jsonPath string) (int, error) {
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		return 0, nil // nothing to migrate is the ordinary case
	}
	var old []oldFact
	if err := json.Unmarshal(b, &old); err != nil {
		return 0, fmt.Errorf("reading %s: %w", jsonPath, err)
	}

	h.mu.Lock()
	m := h.mem
	m.fresh()
	n := 0
	for _, o := range old {
		body := strings.TrimSpace(o.Text)
		if body == "" {
			continue
		}
		name := slug(oneLine(body))
		if name == "" || m.has(name) {
			continue
		}
		f := Fact{
			Name:        name,
			Description: oneLine(body),
			Type:        TypeUser,
			Since:       o.Learned,
			From:        o.From,
			Body:        body,
		}
		if f.Since.IsZero() {
			f.Since = time.Now()
		}
		if err := write(m.home.path(MemoryDir, name+".md"), renderFact(f)); err != nil {
			h.mu.Unlock()
			return n, err
		}
		m.facts = append(m.facts, f)
		n++
	}
	sortFacts(m.facts)
	err = m.writeIndex()
	if err == nil {
		err = m.reload()
	}
	h.mu.Unlock()
	if err != nil {
		return n, err
	}
	if err := os.Rename(jsonPath, jsonPath+".migrated"); err != nil {
		return n, fmt.Errorf("renaming %s: %w", jsonPath, err)
	}
	return n, nil
}

func (m *Memory) has(name string) bool {
	for _, f := range m.facts {
		if f.Name == name {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
