// The durable meters: the spend ledger and the digest cache, journaled
// to the state dir and replayed at startup. A meter that forgets on
// restart is a meter that lies — and with the door open to the LAN,
// other hands can spend. Digests re-billed after a restart are the
// same dishonesty in a smaller coin.
//
// Same discipline as everything else here: append-only jsonl, replayed
// never queried, unparseable lines skipped. Losing a journal costs
// history, not correctness.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func defaultSpendPath() string {
	return statePath("roost-spend.jsonl")
}

func defaultDigestPath() string {
	return statePath("roost-digests.jsonl")
}

func statePath(name string) string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			return ""
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "rook", name)
}

type spendLine struct {
	Root   string    `json:"root"`
	Claude float64   `json:"claude,omitempty"`
	Judge  float64   `json:"judge,omitempty"`
	At     time.Time `json:"at"`
}

type digestLine struct {
	Hash     string    `json:"hash"`
	Headline string    `json:"headline"`
	Bullets  []string  `json:"bullets,omitempty"`
	At       time.Time `json:"at"`
}

// appendLine writes one journal record; failures are dropped — the
// in-memory truth stands for this run, the journal only for the next.
func appendLine(path string, v any) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	f.Write(append(b, '\n'))
}

func eachLine(path string, f func([]byte)) {
	if path == "" {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 4096), 1<<20)
	for sc.Scan() {
		f(sc.Bytes())
	}
}

// loadJournals replays both meters into memory. Called once, before
// the listener — a request must never see a half-replayed ledger.
func (s *server) loadJournals() {
	eachLine(s.spendPath, func(b []byte) {
		var l spendLine
		if json.Unmarshal(b, &l) != nil || l.Root == "" {
			return
		}
		sp := s.spend[l.Root]
		if sp == nil {
			sp = &agentSpend{}
			s.spend[l.Root] = sp
		}
		sp.ClaudeUSD += l.Claude
		sp.JudgeUSD += l.Judge
	})
	eachLine(s.digestPath, func(b []byte) {
		var l digestLine
		if json.Unmarshal(b, &l) != nil || l.Hash == "" || l.Headline == "" {
			return
		}
		s.digests[l.Hash] = &digestRec{State: "ready", Headline: l.Headline, Bullets: l.Bullets}
	})
}
