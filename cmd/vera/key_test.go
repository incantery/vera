package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestLoopbackOnlyReadsTheBindAddress(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:4770": true,
		"localhost:4770": true,
		"[::1]:4770":     true,
		":4770":          false,
		"0.0.0.0:4770":   false,
		"[::]:4770":      false,
		// A concrete LAN address is still beyond the machine.
		"192.168.4.23:4770": false,
	} {
		if loopbackOnly(addr) != want {
			t.Fatalf("loopbackOnly(%q) != %v", addr, want)
		}
	}
}

func TestKeyIsMintedOnceAndStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vera-key")
	k1, err := loadOrCreateKey(path)
	if err != nil || len(k1) != 32 {
		t.Fatalf("k1=%q err=%v", k1, err)
	}
	k2, err := loadOrCreateKey(path)
	if err != nil || k2 != k1 {
		t.Fatalf("the key must survive a restart: %q vs %q", k1, k2)
	}
}

func TestRequireKeyGuardsTheAPIAndOnlyTheAPI(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := requireKey(inner, "sekrit")
	try := func(path, bearer, query string) int {
		req := httptest.NewRequest("GET", path+query, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if try("/api/state", "", "") != 401 {
		t.Fatal("a bare API call must be refused")
	}
	if try("/api/state", "wrong", "") != 401 {
		t.Fatal("a wrong key must be refused")
	}
	if try("/api/state", "sekrit", "") != 200 {
		t.Fatal("the bearer key must open the door")
	}
	if try("/api/state", "", "?key=sekrit") != 200 {
		t.Fatal("the bootstrap ?key= must open the door")
	}
	if try("/", "", "") != 200 || try("/_app/x.js", "", "") != 200 {
		t.Fatal("the shell and its assets carry no data and stay open")
	}
}

func TestGuardMutationsRefusesCrossSiteWrites(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := guardMutations(inner)
	try := func(method, path string, hdr map[string]string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Host = "localhost:4770"
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	// A browser on another site: refused, key or no key.
	if try("POST", "/api/agent/x/say", map[string]string{"Origin": "https://evil.example"}) != 403 {
		t.Fatal("a cross-origin POST must be refused")
	}
	if try("POST", "/api/tasks", map[string]string{"Sec-Fetch-Site": "cross-site"}) != 403 {
		t.Fatal("a cross-site fetch must be refused")
	}
	if try("POST", "/api/tasks", map[string]string{"Origin": "null"}) != 403 {
		t.Fatal("a sandboxed origin must be refused")
	}
	// The house's own page, and non-browser clients: welcome.
	if try("POST", "/api/tasks", map[string]string{"Origin": "http://localhost:4770"}) != 200 {
		t.Fatal("the same origin must pass")
	}
	if try("POST", "/api/tasks", map[string]string{"Sec-Fetch-Site": "same-origin"}) != 200 {
		t.Fatal("a same-origin fetch must pass")
	}
	if try("POST", "/api/tasks", nil) != 200 {
		t.Fatal("curl sends no browser fingerprint and must pass")
	}
	// Reads are not mutations; the SPA shell is not the API.
	if try("GET", "/api/state", map[string]string{"Origin": "https://evil.example"}) != 200 {
		t.Fatal("cross-origin reads stay CORS's problem, not ours")
	}
	if try("POST", "/not-api", map[string]string{"Origin": "https://evil.example"}) != 200 {
		t.Fatal("only the api surface is guarded")
	}
}
