package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// serveLAN starts a transport on a port nobody else is using and hands
// back its base URL.
// lanUnderTest is the most recent transport serveLAN started, for tests
// that need to reach behind the wire.
var lanUnderTest *lanTransport

func serveLAN(t *testing.T, h Handler) (string, Identity) {
	t.Helper()
	id := Identity{Peer: "peer-under-test", Secret: "s3cret", Name: "test-mac"}
	lan := newLAN("127.0.0.1:0", id)
	lanUnderTest = lan

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = lan.Serve(ctx, h) }()

	// Wait for the listener rather than sleeping a guess.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lan.mu.Lock()
		port := lan.port
		lan.mu.Unlock()
		if port != "" {
			return "http://127.0.0.1:" + port, id
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("listener never came up")
	return "", id
}

func TestSayRefusesWithoutTheSecret(t *testing.T) {
	base, _ := serveLAN(t, echo)

	for _, header := range []string{"", "Bearer", "Bearer wrong"} {
		req, _ := http.NewRequest("POST", base+"/say", strings.NewReader(`{"text":"hello"}`))
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Authorization %q got %d, want 401", header, res.StatusCode)
		}
	}
}

func TestSayStreamsFramesAndTerminates(t *testing.T) {
	base, id := serveLAN(t, echo)

	req, _ := http.NewRequest("POST", base+"/say", strings.NewReader(`{"text":"good morning"}`))
	req.Header.Set("Authorization", "Bearer "+id.Secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if got := res.Header.Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("Content-Type %q — the phone splits on newlines and needs to be told so", got)
	}

	var said strings.Builder
	var frames int
	var terminated bool
	scan := bufio.NewScanner(res.Body)
	for scan.Scan() {
		var f Frame
		if err := json.Unmarshal(scan.Bytes(), &f); err != nil {
			t.Fatalf("frame %q is not JSON: %v", scan.Text(), err)
		}
		frames++
		if terminated {
			t.Fatal("a frame arrived after the stream said it was done")
		}
		said.WriteString(f.Delta)
		if f.Done || f.Error != "" {
			terminated = true
		}
	}
	if !terminated {
		// A truncated stream with no terminal frame is the one shape
		// the phone cannot tell apart from a hung connection.
		t.Fatal("stream ended without a terminal frame")
	}
	if frames < 3 {
		t.Fatalf("got %d frames — a one-shot answer is not a stream", frames)
	}
	if !strings.Contains(said.String(), "good morning") {
		t.Fatalf("reassembled reply was %q", said.String())
	}
}

func TestEmptyMessageIsNotAnError(t *testing.T) {
	var got []Frame
	err := echo(context.Background(), Message{Text: "   "}, func(f Frame) error {
		got = append(got, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Done {
		t.Fatalf("silence should end the exchange cleanly, got %+v", got)
	}
}

// Pairing hands out the secret, so it is for the person at the machine.
func TestPairingSurfaceIsLoopbackOnly(t *testing.T) {
	guarded := loopbackOnly(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("the secret"))
	})

	cases := map[string]int{
		"127.0.0.1:5555":    http.StatusOK,
		"[::1]:5555":        http.StatusOK,
		"192.168.1.44:5555": http.StatusForbidden,
		"10.0.0.9:5555":     http.StatusForbidden,
	}
	for remote, want := range cases {
		req := httptest.NewRequest("GET", "/pair.json", nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		guarded(rec, req)
		if rec.Code != want {
			t.Fatalf("from %s got %d, want %d", remote, rec.Code, want)
		}
	}
}

// A phone holding several hints needs to know which one is this Mac
// before it spends a secret on any of them.
func TestPingNamesTheMachineWithoutTheSecret(t *testing.T) {
	base, id := serveLAN(t, echo)

	res, err := http.Get(base + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["peer"] != id.Peer {
		t.Fatalf("ping said peer %q, want %q", got["peer"], id.Peer)
	}
	if strings.Contains(strings.Join(values(got), " "), id.Secret) {
		t.Fatal("ping leaked the secret to an unauthenticated caller")
	}
}

func values(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
