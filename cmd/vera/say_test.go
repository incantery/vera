package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The direct-mode policy names are a closed set; anything else is
// refused before a turn can start under a policy nobody chose.
func TestSayRefusesAnUnknownPolicy(t *testing.T) {
	s := testServer(t, t.TempDir())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agent/x/say",
		strings.NewReader(`{"text":"hi","direct":true,"perm":"sudo"}`))
	req.SetPathValue("id", "x")
	s.handleSay(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "perm") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// Direct mode types ahead: a send during a turn queues (bounded);
// the membrane path still answers busy, because phrasing against a
// moving transcript would invent context.
func TestDirectSaysQueueWhileATurnFlies(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "-repo-alpha", "sess-live", time.Now().Add(-time.Minute))
	s := testServer(t, dir)
	s.says["sess-live"] = &sayJob{Status: "thinking"}

	say := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/agent/sess-live/say", strings.NewReader(body))
		req.SetPathValue("id", "sess-live")
		s.handleSay(rec, req)
		return rec
	}
	for i := 0; i < 3; i++ {
		if rec := say(`{"text":"next","direct":true,"perm":"read"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), "queued") {
			t.Fatalf("queue %d: code=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	if rec := say(`{"text":"one too many","direct":true}`); rec.Code != 409 {
		t.Fatalf("the queue must bound: code=%d", rec.Code)
	}
	if rec := say(`{"text":"via membrane"}`); rec.Code != 409 {
		t.Fatalf("membrane sends must still answer busy: code=%d", rec.Code)
	}
	if n := len(s.queues["sess-live"]); n != 3 {
		t.Fatalf("queued: %d", n)
	}
}

// Interrupt with nothing in flight answers 409, not a panic and not a
// silent 200 that would lie about having stopped something.
func TestInterruptNeedsATurnInFlight(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "-repo-alpha", "sess-live", time.Now().Add(-time.Minute))
	s := testServer(t, dir)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agent/sess-live/interrupt", nil)
	req.SetPathValue("id", "sess-live")
	s.handleInterrupt(rec, req)
	if rec.Code != 409 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}
