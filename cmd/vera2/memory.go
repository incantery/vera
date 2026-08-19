// Memory: what is still true tomorrow.
//
// History is what survives a turn. Memory is what survives a RESTART,
// and that is the whole difference — a fact worth keeping is one that
// is still true in a conversation that has not happened yet.
//
// Three decisions worth arguing with:
//
// Writing is asynchronous, reading is synchronous. Extraction is a
// second model call, and putting it in front of the reply would add its
// latency to every single exchange to serve a fact that is not needed
// until the NEXT one. So the reply goes out, and remembering happens
// behind it.
//
// Every fact goes in the prompt; nothing is retrieved. One person
// accumulates tens to low hundreds of durable facts, which is small
// enough to send in full. Embeddings and similarity search are the
// right answer at thousands and a way of looking busy at fifty — and
// retrieval fails in the worst possible way, by silently not finding
// the thing that mattered.
//
// Facts are REPLACED, not accumulated. Someone who moves from Denver to
// Austin has not become a person who lives in two places, and a memory
// that only ever appends turns into a pile of contradictions that the
// model then has to arbitrate on every turn.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Fact struct {
	ID      int       `json:"id"`
	Text    string    `json:"text"`
	Learned time.Time `json:"learned"`
	// Which conversation taught it — so a wrong fact can be traced back
	// to the exchange that produced it.
	From string `json:"from,omitempty"`
}

type Memory struct {
	mu    sync.Mutex
	path  string
	facts []Fact
	next  int

	// A ceiling, because extraction is automatic and anything automatic
	// accumulates. At the limit the oldest goes; a fact still true will
	// be learned again, and one that is not should not have survived.
	limit int
}

func newMemory(path string) *Memory {
	m := &Memory{path: path, limit: 120}
	m.load()
	return m
}

func (m *Memory) load() {
	b, err := os.ReadFile(m.path)
	if err != nil {
		return
	}
	var facts []Fact
	if json.Unmarshal(b, &facts) != nil {
		return
	}
	m.facts = facts
	for _, f := range facts {
		if f.ID >= m.next {
			m.next = f.ID + 1
		}
	}
}

// save writes through a temporary file and renames, so a crash midway
// leaves the previous memory rather than half of the new one.
func (m *Memory) save() error {
	if m.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m.facts, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *Memory) All() []Fact {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Fact, len(m.facts))
	copy(out, m.facts)
	return out
}

func (m *Memory) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.facts)
}

// Recite is what goes in the prompt. Numbered, because the extractor
// refers to facts by number when it supersedes one.
func (m *Memory) Recite() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.facts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, f := range m.facts {
		fmt.Fprintf(&b, "%d. %s\n", f.ID, f.Text)
	}
	return b.String()
}

// Revision is what the extractor decided.
type Revision struct {
	Add     []string      `json:"add"`
	Replace []Replacement `json:"replace"`
	Remove  []int         `json:"remove"`
}

type Replacement struct {
	ID   int    `json:"id"`
	With string `json:"with"`
}

func (r Revision) empty() bool {
	return len(r.Add) == 0 && len(r.Replace) == 0 && len(r.Remove) == 0
}

// Apply commits a revision. Replacement keeps the fact's id, so a
// corrected fact stays the same fact rather than becoming a new one
// with the old still sitting beside it.
func (m *Memory) Apply(r Revision, from string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	drop := map[int]bool{}
	for _, id := range r.Remove {
		drop[id] = true
	}
	for _, rep := range r.Replace {
		text := strings.TrimSpace(rep.With)
		if text == "" {
			drop[rep.ID] = true
			continue
		}
		found := false
		for i := range m.facts {
			if m.facts[i].ID == rep.ID {
				m.facts[i].Text = text
				m.facts[i].Learned = time.Now()
				m.facts[i].From = from
				found = true
				break
			}
		}
		// An id the model invented is treated as something new rather
		// than dropped, because the content is probably fine even when
		// the bookkeeping is not.
		if !found {
			r.Add = append(r.Add, text)
		}
	}

	if len(drop) > 0 {
		kept := m.facts[:0]
		for _, f := range m.facts {
			if !drop[f.ID] {
				kept = append(kept, f)
			}
		}
		m.facts = kept
	}

	for _, text := range r.Add {
		text = strings.TrimSpace(text)
		if text == "" || m.duplicate(text) {
			continue
		}
		m.facts = append(m.facts, Fact{ID: m.next, Text: text, Learned: time.Now(), From: from})
		m.next++
	}

	// Oldest out at the ceiling.
	if len(m.facts) > m.limit {
		sort.SliceStable(m.facts, func(i, j int) bool {
			return m.facts[i].Learned.Before(m.facts[j].Learned)
		})
		m.facts = m.facts[len(m.facts)-m.limit:]
	}

	_ = m.save()
}

// duplicate is a cheap exact-ish guard. The extractor is told not to
// repeat what it already knows, so this only catches the case where it
// does so anyway.
func (m *Memory) duplicate(text string) bool {
	norm := strings.ToLower(strings.TrimSpace(text))
	for _, f := range m.facts {
		if strings.ToLower(strings.TrimSpace(f.Text)) == norm {
			return true
		}
	}
	return false
}

func (m *Memory) Forget(ids ...int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	drop := map[int]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	before := len(m.facts)
	kept := m.facts[:0]
	for _, f := range m.facts {
		if !drop[f.ID] {
			kept = append(kept, f)
		}
	}
	m.facts = kept
	_ = m.save()
	return before - len(m.facts)
}

func (m *Memory) ForgetAll() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.facts)
	m.facts = nil
	_ = m.save()
	return n
}

func memoryPath(override string) string {
	if override != "" {
		return override
	}
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "vera2", "memory.json")
}
