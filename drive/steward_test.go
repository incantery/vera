package drive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseStewardMovesSalvagesTheShape(t *testing.T) {
	moves, err := ParseStewardMoves(`DONE T-3 — the agent finished and went quiet.
START T-7 — its predecessor landed yesterday.
NOTE T-9 — this has waited two days on one question.
STOP T-1 — not a verb the vocabulary knows
commentary the model was told not to write`)
	if err != nil || len(moves) != 3 {
		t.Fatalf("moves=%+v err=%v", moves, err)
	}
	if moves[0].Verb != "done" || moves[0].Task != "T-3" || moves[0].Why == "" {
		t.Fatalf("first: %+v", moves[0])
	}
	if moves[1].Verb != "start" || moves[2].Verb != "note" {
		t.Fatalf("verbs: %+v", moves)
	}
}

func TestParseStewardMovesNothingIsClean(t *testing.T) {
	moves, err := ParseStewardMoves("NOTHING")
	if err != nil || len(moves) != 0 {
		t.Fatalf("moves=%+v err=%v", moves, err)
	}
	if _, err := ParseStewardMoves("the board seems fine to me!"); err == nil {
		t.Fatal("chatter with no moves and no NOTHING is a broken shape")
	}
}

func TestParseStewardMovesCapsAtThreeAndRefusesInventedIds(t *testing.T) {
	moves, err := ParseStewardMoves(`NOTE T-1 — a
NOTE T-2 — b
NOTE T-3 — c
NOTE T-4 — d`)
	if err != nil || len(moves) != 3 {
		t.Fatalf("moves=%+v err=%v", moves, err)
	}
	// An id with a space in it is not an id.
	moves, err = ParseStewardMoves("DONE two words — nope\nNOTE T-5 — fine")
	if err != nil || len(moves) != 1 || moves[0].Task != "T-5" {
		t.Fatalf("moves=%+v err=%v", moves, err)
	}
}

func TestStewardAsksOverTheWire(t *testing.T) {
	var sawBoard string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []chatMsg `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		sawBoard = req.Messages[1].Content
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "NOTE T-1 — aging in waiting."}}},
		})
	}))
	defer srv.Close()
	m := &LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	moves, err := m.Steward(context.Background(), "The board:\nT-1 [waiting] ...")
	if err != nil || len(moves) != 1 || moves[0].Verb != "note" {
		t.Fatalf("moves=%+v err=%v", moves, err)
	}
	if !strings.Contains(sawBoard, "The board:") {
		t.Fatalf("the board must ride the user message: %q", sawBoard)
	}
}

func TestParseStewardMovesAnswerCarriesTheDraft(t *testing.T) {
	moves, err := ParseStewardMoves("ANSWER T-4 — Yes — use the staging database; the goal names it.")
	if err != nil || len(moves) != 1 {
		t.Fatalf("moves=%+v err=%v", moves, err)
	}
	if moves[0].Verb != "answer" || moves[0].Task != "T-4" {
		t.Fatalf("move: %+v", moves[0])
	}
	// The draft survives intact past the first dash.
	if !strings.Contains(moves[0].Why, "staging database") || !strings.Contains(moves[0].Why, "the goal names it") {
		t.Fatalf("draft: %q", moves[0].Why)
	}
}
