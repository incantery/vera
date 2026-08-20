// The standing conversation with the owner. The thread lives here in
// the daemon — the popup that renders it is ephemeral by design — as
// a jsonl journal, so it survives relaunches and any face (terminal
// popup today, the phone tomorrow) shows the same conversation.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/drive"
)

type chatTurn struct {
	Role string    `json:"role"` // "owner" | "vera"
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

const chatKeep = 500 // turns held in memory and offered to faces

type chatStore struct {
	path  string // "" disables persistence, the thread still works
	mu    sync.Mutex
	turns []chatTurn
}

func defaultChatPath() string {
	return statePath("vera-chat.jsonl")
}

func newChatStore(path string) *chatStore {
	st := &chatStore{path: path}
	if path == "" {
		return st
	}
	f, err := os.Open(path)
	if err != nil {
		return st
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 1<<20)
	for sc.Scan() {
		var t chatTurn
		if json.Unmarshal(sc.Bytes(), &t) == nil && t.Text != "" {
			st.turns = append(st.turns, t)
		}
	}
	if len(st.turns) > chatKeep {
		st.turns = st.turns[len(st.turns)-chatKeep:]
	}
	return st
}

func (st *chatStore) add(role, text string, at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	turn := chatTurn{Role: role, Text: text, At: at}
	st.turns = append(st.turns, turn)
	if len(st.turns) > chatKeep {
		st.turns = st.turns[len(st.turns)-chatKeep:]
	}
	if st.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(st.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	line, _ := json.Marshal(turn)
	f.Write(append(line, '\n'))
}

func (st *chatStore) tail(n int) []chatTurn {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.turns) > n {
		return append([]chatTurn(nil), st.turns[len(st.turns)-n:]...)
	}
	return append([]chatTurn(nil), st.turns...)
}

func (s *server) handleChatGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.chat.tail(50))
}

func (s *server) handleChatPost(w http.ResponseWriter, r *http.Request) {
	if s.llm == nil {
		httpErr(w, 409, s.notice)
		return
	}
	var req struct {
		Text    string `json:"text"`
		Session string `json:"session"`
		Dir     string `json:"dir"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		httpErr(w, 400, "say something")
		return
	}
	now := time.Now()
	board, _ := stewardBoard(s.world(now))
	place := ""
	if req.Session != "" {
		place = "tmux session " + req.Session
	}
	if req.Dir != "" {
		if place != "" {
			place += ", "
		}
		place += "directory " + req.Dir
	}

	ll := *s.llmFor(partSteward)
	ll.Spend = func(c float64) { s.addSpend("vera-chat", 0, c) }
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	thread := make([]drive.ChatTurn, 0, 30)
	for _, t := range s.chat.tail(30) {
		thread = append(thread, drive.ChatTurn{Role: t.Role, Text: t.Text})
	}
	reply, moves, err := ll.Chat(ctx, board, place, thread, req.Text)
	if err != nil {
		httpErr(w, 502, err.Error())
		return
	}

	// The one verb: relay the owner's decision to a waiting card,
	// through the same core a typed reply rides. Failures are said in
	// the thread, not swallowed.
	var applied []string
	for _, mv := range moves {
		if mv.Verb != "answer" {
			continue
		}
		if _, serr := s.taskReply(mv.Task, mv.Why, "replied via chat", now); serr != nil {
			reply += fmt.Sprintf("\n(could not send to %s: %s)", mv.Task, serr.msg)
			continue
		}
		applied = append(applied, mv.Task)
	}
	if len(applied) > 0 {
		s.hub.notify()
	}

	s.chat.add("owner", req.Text, now)
	s.chat.add("vera", reply, time.Now())
	writeJSON(w, struct {
		Reply   string   `json:"reply"`
		Applied []string `json:"applied,omitempty"`
	}{reply, applied})
}
