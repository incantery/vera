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
