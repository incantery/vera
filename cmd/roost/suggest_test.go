package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/incantery/rook-host/engine/drive"
	roostv1 "github.com/incantery/rook-host/engine/gen/roost/v1"
	"github.com/incantery/rook-host/engine/transcript"
)

// suggestWire is a fake completions endpoint that counts its calls —
// the cache's honesty is measured in HTTP requests.
func suggestWire(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"content": "HAPPENED: Finished the rename.\nNOW: Waiting on your verdict.\n1. Looks right — commit it.\n2. Show me the tests first.",
			}}},
		})
	}))
}

// writeExchange writes a transcript with a real user prompt and an
// assistant reply — the exchange the bid reads.
func writeExchange(t *testing.T, dir, proj, id string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, proj)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := mtime.UTC().Format(time.RFC3339)
	lines := fmt.Sprintf(
		`{"type":"user","timestamp":%q,"cwd":"/repo/x","message":{"role":"user","content":"rename the flag"}}`+"\n"+
			`{"type":"assistant","timestamp":%q,"cwd":"/repo/x","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Done — renamed everywhere. Commit?"}]}}`+"\n",
		ts, ts)
	if err := os.WriteFile(filepath.Join(p, id+".jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSuggestBidsOnceAndJournals(t *testing.T) {
	dir := t.TempDir()
	writeExchange(t, dir, "-repo-x", "sess-sugg", time.Now().Add(-time.Minute))
	var calls atomic.Int32
	srv := suggestWire(t, &calls)
	defer srv.Close()
	s := testServer(t, dir)
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	s.suggestPath = filepath.Join(t.TempDir(), "suggests.jsonl")

	rec, serr := s.agentSuggest("sess-sugg")
	if serr != nil {
		t.Fatalf("the bid must land: %v", serr.msg)
	}
	if rec.Happened == "" || rec.Now == "" || len(rec.Replies) != 2 {
		t.Fatalf("the whole bid comes back: %+v", rec)
	}
	if rec.Replies[0] != "Looks right — commit it." {
		t.Fatalf("rank order holds: %v", rec.Replies)
	}
	if _, serr := s.agentSuggest("sess-sugg"); serr != nil {
		t.Fatal("the second ask reads the cache")
	}
	if calls.Load() != 1 {
		t.Fatalf("one turn, one bill: %d wire calls", calls.Load())
	}

	// A restart replays the journal: same bid, still no second bill.
	s2 := testServer(t, dir)
	s2.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	s2.suggestPath = s.suggestPath
	s2.loadJournals()
	rec2, serr := s2.agentSuggest("sess-sugg")
	if serr != nil {
		t.Fatalf("the replayed bid must answer: %v", serr.msg)
	}
	if len(rec2.Replies) != 2 || rec2.Happened != rec.Happened {
		t.Fatalf("the journal carries the whole bid: %+v", rec2)
	}
	if calls.Load() != 1 {
		t.Fatalf("a restart must not re-bill: %d wire calls", calls.Load())
	}
}

func TestSuggestRefusalsAndTheRPC(t *testing.T) {
	dir := t.TempDir()
	writeExchange(t, dir, "-repo-x", "sess-sugg", time.Now().Add(-time.Minute))
	s := testServer(t, dir)

	// No key: honest 409, not a fake bid.
	if _, serr := s.agentSuggest("sess-sugg"); serr == nil || serr.code != 409 {
		t.Fatalf("no llm must refuse with 409: %+v", serr)
	}

	var calls atomic.Int32
	srv := suggestWire(t, &calls)
	defer srv.Close()
	s.llm = &drive.LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	if _, serr := s.agentSuggest("stranger"); serr == nil || serr.code != 404 {
		t.Fatalf("a gone agent must be 404: %+v", serr)
	}

	r := &roostRPC{s: s}
	resp, err := r.Suggest(context.Background(), connect.NewRequest(&roostv1.SuggestRequest{Id: "sess-sugg"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Msg.Replies) != 2 || resp.Msg.Happened == "" {
		t.Fatalf("the RPC carries the whole bid: %+v", resp.Msg)
	}
	if _, err := r.Suggest(context.Background(), connect.NewRequest(&roostv1.SuggestRequest{Id: "stranger"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("a gone agent must be NotFound: %v", err)
	}
}

func TestLastExchangeSkipsProselessTurns(t *testing.T) {
	prompt, reply := lastExchange([]transcript.Msg{
		{Role: "user", Text: "first ask"},
		{Role: "assistant", Text: "first answer"},
		{Role: "user", Text: "now do the thing"},
		{Role: "assistant", Text: "", Tools: 3}, // ended in tool calls, no prose
	})
	if prompt != "first ask" || reply != "first answer" {
		t.Fatalf("falls back to the last turn with words: %q / %q", prompt, reply)
	}
	if p, r := lastExchange(nil); p != "" || r != "" {
		t.Fatalf("an empty history has no exchange: %q %q", p, r)
	}
}
