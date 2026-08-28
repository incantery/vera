// Which model each conversation is on, on disk.
//
// It is here rather than in the terminal because verad is the single
// writer of everything a conversation is: the phone, the chat and `vera
// say` all reach the same daemon, and a `/model` typed in one of them
// has to mean the same thing in the next. It survives a restart for the
// same reason the journal does — a person who chose opus this morning
// did not choose it for this process.
//
// One small JSON file, rewritten whole. There are tens of these, not
// millions, and a file a person can read and delete is worth more here
// than an efficient one.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// maxPicks bounds the file. A phone that reinstalls mints a new
// conversation id every launch, so this is a real bound, not a
// theoretical one; the oldest choices go first.
const maxPicks = 500

// Picks is the per-conversation model, kept.
type Picks struct {
	Path string

	mu     sync.Mutex
	loaded bool
	byConv map[string]storedPick
}

type storedPick struct {
	Pick
	At time.Time `json:"at"`
}

// Get is what this conversation was told to use, if anything.
func (s *Picks) Get(conversation string) (Pick, bool) {
	if s == nil || conversation == "" {
		return Pick{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	p, ok := s.byConv[conversation]
	if !ok || p.empty() {
		return Pick{}, false
	}
	return p.Pick, true
}

// Set records a conversation's choice; an empty pick forgets it.
func (s *Picks) Set(conversation string, p Pick) error {
	if s == nil || conversation == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	if p.empty() {
		delete(s.byConv, conversation)
	} else {
		s.byConv[conversation] = storedPick{Pick: p, At: time.Now()}
		s.evict()
	}
	return s.save()
}

// load reads the file once. A file that will not parse is treated as
// empty: a conversation on the wrong model is a smaller loss than a
// daemon that will not answer.
func (s *Picks) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.byConv = map[string]storedPick{}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return
	}
	var got map[string]storedPick
	if json.Unmarshal(b, &got) == nil && got != nil {
		s.byConv = got
	}
}

// evict drops the stalest choices once there are too many. Called
// under the lock, on the way to adding one.
func (s *Picks) evict() {
	if len(s.byConv) <= maxPicks {
		return
	}
	type aged struct {
		id string
		at time.Time
	}
	all := make([]aged, 0, len(s.byConv))
	for id, p := range s.byConv {
		all = append(all, aged{id, p.At})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	for _, a := range all[:len(s.byConv)-maxPicks] {
		delete(s.byConv, a.id)
	}
}

// save writes the whole file through a temporary one: a truncated
// write that lost power would lose every conversation's model, and
// rename is the cheapest way not to.
func (s *Picks) save() error {
	if s.Path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.byConv, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}
