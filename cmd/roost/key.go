// The key: the cheapest honest lock on the LAN door. Binding beyond
// loopback exposes the whole membrane — every transcript, the chat,
// the drives, your spend — so a non-loopback listener requires a
// bearer key on every /api call. Loopback stays keyless: localhost is
// already inside the house.
//
// The key is minted once into the state dir and printed as part of
// the URL at startup; the page stashes it and strips it from the
// address bar. This is a placeholder for rook-host's real pairing
// (QR + Ed25519 challenge, the link rail) if roost ever grows into
// it — a shared secret today, a trust ceremony when it matters.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func defaultKeyPath() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			return ""
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "rook", "roost-key")
}

// loadOrCreateKey mints the key on first run, 0600. An unreadable
// state dir means no key and therefore no LAN serving — refusing is
// better than serving the fleet unlocked.
func loadOrCreateKey(path string) (string, error) {
	if path == "" {
		return "", os.ErrNotExist
	}
	if b, err := os.ReadFile(path); err == nil {
		if k := strings.TrimSpace(string(b)); k != "" {
			return k, nil
		}
	}
	raw := make([]byte, 16)
	rand.Read(raw)
	k := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(k+"\n"), 0o600); err != nil {
		return "", err
	}
	return k, nil
}

// loopbackOnly: does this listen address stay inside the machine?
// Empty host ("":4770"), 0.0.0.0 and :: open every interface; a
// concrete address answers for itself.
func loopbackOnly(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requireKey guards /api/* with the bearer key; everything else (the
// SPA shell and its hashed assets, which carry no data) stays open so
// the keyed URL can bootstrap the page.
func requireKey(next http.Handler, key string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guarded := strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/roost.")
		if !guarded || authOK(r, key) {
			next.ServeHTTP(w, r)
			return
		}
		httpErr(w, 401, "key required — use the URL roost printed at startup")
	})
}

func authOK(r *http.Request, key string) bool {
	if h := r.Header.Get("Authorization"); strings.TrimPrefix(h, "Bearer ") == key && h != "" {
		return true
	}
	return r.URL.Query().Get("key") == key
}

// lanURLs names every address the page answers on: concrete IPv4s
// plus the mDNS hostname macOS already advertises — the answer to
// DHCP drift without a line of server code.
func lanURLs(port string) []string {
	var out []string
	if host, err := os.Hostname(); err == nil {
		short := strings.TrimSuffix(host, ".local")
		out = append(out, "http://"+short+".local:"+port)
	}
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range ifaces {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil {
			continue
		}
		out = append(out, "http://"+ipn.IP.String()+":"+port)
	}
	return out
}
