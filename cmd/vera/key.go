// The key: the cheapest honest lock on the LAN door. Binding beyond
// loopback exposes the whole membrane — every transcript, the chat,
// the drives, your spend — so a non-loopback listener requires a
// bearer key on every /api call. Loopback stays keyless: localhost is
// already inside the house.
//
// The key is minted once into the state dir and printed as part of
// the URL at startup; the page stashes it and strips it from the
// address bar. This is a placeholder for vera-host's real pairing
// (QR + Ed25519 challenge, the link rail) if vera ever grows into
// it — a shared secret today, a trust ceremony when it matters.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func defaultKeyPath() string {
	return statePath("vera-key")
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
		guarded := strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/vera.")
		if !guarded || authOK(r, key) {
			next.ServeHTTP(w, r)
			return
		}
		httpErr(w, 401, "key required — use the URL vera printed at startup")
	})
}

func authOK(r *http.Request, key string) bool {
	// Constant-time both ways: a timing oracle on the key is a door
	// that picks its own lock.
	if h := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); h != "" &&
		subtle.ConstantTimeCompare([]byte(h), []byte(key)) == 1 {
		return true
	}
	q := r.URL.Query().Get("key")
	return q != "" && subtle.ConstantTimeCompare([]byte(q), []byte(key)) == 1
}

// guardMutations is the cross-site door: browsers volunteer where a
// request came from (Origin on cross-origin sends, Sec-Fetch-Site on
// everything modern), and a mutation whose provenance is another site
// is refused — even on loopback, where there is no key. Without this,
// any web page the user visits can POST tools into their sessions:
// localhost is inside the house, but the browser brings visitors.
// Non-browser clients (curl, scripts) send neither header and pass.
func guardMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutating := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		guarded := strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/vera.")
		if mutating && guarded && !sameOriginish(r) {
			httpErr(w, 403, "cross-site mutations are refused")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOriginish(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	if o := r.Header.Get("Origin"); o == "null" {
		return false // sandboxed iframes and data: pages are not the house
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true // no browser fingerprint, same page, or typed URL
	}
	return false
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
