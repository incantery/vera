package drive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSalvageSuggestTakesWhatCame(t *testing.T) {
	happened, now, replies := salvageSuggest(
		"HAPPENED: Fixed the flaky test and reran the suite.\n" +
			"NOW: All green; asks whether to commit.\n" +
			"1. Yes — commit it with a message naming the race you fixed.\n" +
			"2) Show me the diff first.\n" +
			"- Run the suite twice more before committing.\n" +
			"4. A fourth bid past the cap.\n")
	if happened != "Fixed the flaky test and reran the suite." {
		t.Fatalf("happened: %q", happened)
	}
	if now != "All green; asks whether to commit." {
		t.Fatalf("now: %q", now)
	}
	if len(replies) != 3 {
		t.Fatalf("three replies, capped: %v", replies)
	}
	if replies[0] != "Yes — commit it with a message naming the race you fixed." ||
		replies[1] != "Show me the diff first." ||
		replies[2] != "Run the suite twice more before committing." {
		t.Fatalf("replies in rank order: %v", replies)
	}
}

func TestSuggestReadsTheExchangeAndSalvages(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"content": "HAPPENED: Renamed the flag.\nNOW: Waiting on your call.\n1. Ship it.",
			}}},
		})
	}))
	defer srv.Close()
	m := &LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	happened, now, replies, err := m.Suggest(context.Background(),
		"rename the flag", "please rename --old to --new", "Done — renamed everywhere. Ship it?")
	if err != nil {
		t.Fatal(err)
	}
	if happened != "Renamed the flag." || now != "Waiting on your call." {
		t.Fatalf("digest lines: %q / %q", happened, now)
	}
	if len(replies) != 1 || replies[0] != "Ship it." {
		t.Fatalf("replies: %v", replies)
	}
	for _, want := range []string{"rename the flag", "please rename --old", "renamed everywhere"} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("the wire request must carry %q: %s", want, gotBody)
		}
	}
}

func TestSuggestRefusesAnUnusableAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "no numbered lines here"}}},
		})
	}))
	defer srv.Close()
	m := &LLM{Client: srv.Client(), Base: srv.URL, Name: "test-model"}
	if _, _, _, err := m.Suggest(context.Background(), "", "q", "r"); err == nil {
		t.Fatal("an answer with no replies is unusable")
	}
}
