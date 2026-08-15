// The suggestion surface: when the worker's turn lands, the vera
// agent reads it and bids — what just happened, where we are, and one
// to three replies the human could send. The human clicking one is a
// send like any other (the say rail carries it); the human typing
// their own instead is the signal that vera's bid wasn't good enough.
// The journal keeps every bid served, so that comparison can be
// learned from later without having re-billed a single turn.
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/incantery/rook-host/engine/transcript"
)

// suggestGen salts the cache key: bump it when the suggest prompt
// changes, so old cached bids regenerate under the new prompt.
const suggestGen = "s1|"

// suggestRec is one bid: the digest of the turn plus ranked replies.
// done closes when the wire answers; waiters read Err before the rest.
type suggestRec struct {
	Happened string   `json:"happened"`
	Now      string   `json:"now"`
	Replies  []string `json:"replies"`
	Err      string   `json:"-"`
	done     chan struct{}
}

type suggestLine struct {
	Hash     string    `json:"hash"`
	Root     string    `json:"root"`
	Happened string    `json:"happened"`
	Now      string    `json:"now"`
	Replies  []string  `json:"replies"`
	At       time.Time `json:"at"`
}

// agentSuggest is the transport-neutral core: REST and the Connect
// RPC both call it. Blocking — the first caller for a turn pays the
// wire's latency, everyone else (and every refetch) gets the cache.
// Failures are returned but never cached: a flaky wire answer must
// not poison the turn until restart.
func (s *server) agentSuggest(id string) (*suggestRec, *sayErr) {
	now := time.Now()
	root, head := s.resolveAgent(id, now)
	if head == nil {
		return nil, &sayErr{404, "that agent is gone from the window"}
	}
	if s.llm == nil {
		return nil, &sayErr{409, "no vera-agent key — suggestions are off"}
	}
	prompt, reply := lastExchange(transcript.History(head.Path))
	if reply == "" {
		return nil, &sayErr{409, "no finished turn to read yet"}
	}
	h := textHash(suggestGen + prompt + "\x1f" + reply)
	s.mu.Lock()
	if rec := s.suggests[h]; rec != nil {
		s.mu.Unlock()
		<-rec.done
		if rec.Err != "" {
			return nil, &sayErr{502, "the vera agent did not answer: " + rec.Err}
		}
		return rec, nil
	}
	rec := &suggestRec{done: make(chan struct{})}
	s.suggests[h] = rec
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	happened, nowLine, replies, err := s.rootLLM(root).Suggest(ctx, head.Title, prompt, reply)
	s.mu.Lock()
	if err != nil {
		rec.Err = err.Error()
		delete(s.suggests, h) // retryable — the next ask calls again
	} else {
		rec.Happened, rec.Now, rec.Replies = happened, nowLine, replies
		appendLine(s.suggestPath, suggestLine{
			Hash: h, Root: root, Happened: happened, Now: nowLine, Replies: replies, At: time.Now(),
		})
	}
	s.mu.Unlock()
	close(rec.done)
	if rec.Err != "" {
		return nil, &sayErr{502, "the vera agent did not answer: " + rec.Err}
	}
	return rec, nil
}

// lastExchange walks back to the last assistant prose and the user
// prompt that provoked it. A turn that ended in tool calls with no
// prose falls through to the last turn that said something — a bid
// needs words to read.
func lastExchange(history []transcript.Msg) (prompt, reply string) {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" || history[i].Text == "" {
			continue
		}
		reply = history[i].Text
		for j := i - 1; j >= 0; j-- {
			if history[j].Role == "user" {
				prompt = history[j].Text
				break
			}
		}
		return prompt, reply
	}
	return "", ""
}

func (s *server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	rec, serr := s.agentSuggest(r.PathValue("id"))
	if serr != nil {
		httpErr(w, serr.code, serr.msg)
		return
	}
	writeJSON(w, rec)
}
