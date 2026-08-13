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
	path := filepath.Join(t.TempDir(), "roost-key")
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
