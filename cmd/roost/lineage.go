// The lineage: which fork currently speaks for which agent.
//
// Every headless turn forks — that is what keeps live terminals
// untouched — but a human does not want to manage forks, they want ONE
// agent with one history. So the server remembers each agent's family:
// the root is the agent's identity (the id in the URL, forever), the
// head is the newest fork (where the next turn resumes and where the
// full history lives, since a fork's transcript replays everything it
// continued). Forks are hidden from the index; the root wears the
// head's state.
//
// Journaled to one append-only jsonl so identity survives a restart —
// losing it would not corrupt anything, but every past fork would
// reappear as a stranger.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type lineage struct {
	mu   sync.Mutex
	path string            // "" = remember only while running
	head map[string]string // root -> newest fork
	root map[string]string // any fork -> its root
}

type lineageLine struct {
	Root string `json:"root"`
	Head string `json:"head"`
}

// openLineage loads the journal, tolerantly: an unreadable file is an
// empty memory, an unparseable line is skipped. Replay order rebuilds
// the newest head per root.
func openLineage(path string) *lineage {
	ln := &lineage{path: path, head: map[string]string{}, root: map[string]string{}}
	if path == "" {
		return ln
	}
	f, err := os.Open(path)
	if err != nil {
		return ln
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 1<<20)
	for sc.Scan() {
		var l lineageLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Root == "" || l.Head == "" {
			continue
		}
		ln.head[l.Root] = l.Head
		ln.root[l.Head] = l.Root
	}
	return ln
}

// rootOf names the agent a session belongs to: itself, unless it is a
// known fork.
func (ln *lineage) rootOf(id string) string {
	ln.mu.Lock()
	defer ln.mu.Unlock()
	if r, ok := ln.root[id]; ok {
		return r
	}
	return id
}

// headOf names where the agent's conversation now lives.
func (ln *lineage) headOf(id string) string {
	ln.mu.Lock()
	defer ln.mu.Unlock()
	r := id
	if rr, ok := ln.root[id]; ok {
		r = rr
	}
	if h, ok := ln.head[r]; ok {
		return h
	}
	return r
}

func (ln *lineage) isFork(id string) bool {
	ln.mu.Lock()
	defer ln.mu.Unlock()
	_, ok := ln.root[id]
	return ok
}

// advance records a new fork as an agent's head. Appended before it
// matters: a crash right after the turn must not orphan the fork.
func (ln *lineage) advance(id, fork string) {
	if fork == "" || fork == id {
		return
	}
	ln.mu.Lock()
	defer ln.mu.Unlock()
	r := id
	if rr, ok := ln.root[id]; ok {
		r = rr
	}
	if r == fork {
		return
	}
	ln.head[r] = fork
	ln.root[fork] = r
	if ln.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(ln.path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(ln.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(lineageLine{Root: r, Head: fork})
	f.Write(append(b, '\n'))
}

// defaultLineagePath is the journal's home, rook's state-dir
// conventions: $XDG_STATE_HOME/rook/roost-lineage.jsonl.
func defaultLineagePath() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			return ""
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "rook", "roost-lineage.jsonl")
}
