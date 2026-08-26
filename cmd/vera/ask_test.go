package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/incantery/mote/agent"
)

// The terminal finds an answerer by type assertion, and an agent that
// is not one renders the card and says the question went nowhere. So
// the assertion is the test: without it the ask is decoration.
func TestTheChatIsAnAnswerer(t *testing.T) {
	var a agent.Agent = veraAgent{&chatClient{}}
	if _, ok := a.(agent.Answerer); !ok {
		t.Fatal("the chat cannot answer an ask, so every one of them would hang")
	}
}

// An ask frame becomes the event the terminal draws a card for, with
// everything the person needs to decide on it.
func TestAskFrameBecomesAnAskEvent(t *testing.T) {
	events := translate(Frame{Ask: &AskFrame{
		ID: "call_7", Name: "write", Args: `{"path":"/tmp/x"}`, Text: "nothing said otherwise"}})
	if len(events) != 1 {
		t.Fatalf("one frame became %d events", len(events))
	}
	ev := events[0]
	if ev.Kind != agent.KindAsk {
		t.Fatalf("kind %q", ev.Kind)
	}
	if ev.ID != "call_7" || ev.Name != "write" || ev.Args == "" || ev.Text == "" {
		t.Fatalf("%+v — a card with no arguments or no reason cannot be answered", ev)
	}
}

// And the answer goes back where verad is waiting for it: the call's
// own id, one of the three words, on the authed route.
func TestAnswerPostsToTheAskRoute(t *testing.T) {
	type got struct {
		path, auth, choice string
	}
	seen := make(chan got, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var b struct {
			Choice string `json:"choice"`
		}
		_ = json.Unmarshal(body, &b)
		seen <- got{r.URL.Path, r.Header.Get("Authorization"), b.Choice}
	}))
	defer srv.Close()

	a := veraAgent{&chatClient{base: srv.URL, secret: "s3cret"}}
	if err := a.Answer(context.Background(), "call_7", agent.Always); err != nil {
		t.Fatal(err)
	}
	g := <-seen
	if g.path != "/ask/call_7" {
		t.Fatalf("posted to %q", g.path)
	}
	if g.auth != "Bearer s3cret" {
		t.Fatalf("Authorization %q — an unauthed answer is a stranger's answer", g.auth)
	}
	if g.choice != "always" {
		t.Fatalf("choice %q", g.choice)
	}
}
