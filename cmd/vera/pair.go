// Pairing: a phone and a machine learning each other's names once.
//
// The pairing code carries an IDENTITY and a SECRET, and only mentions
// addresses as a hint. That is the whole design decision here. A code
// containing "192.168.1.20:4780" pairs a phone to a network rather than
// to a machine, and every coffee shop, every hotel, and every switch to
// peer-to-peer becomes a re-pair. A code containing a peer id and a
// shared secret is still true a year later on a transport that had not
// been written when it was scanned.
//
// The secret is a shared bearer token, which is the same honest
// placeholder cmd/vera's key.go describes: good against the other
// guests on the hotel wifi, not against someone who has already read
// your disk. The trust ceremony it deserves — Ed25519, a challenge, a
// short pairing window — is worth doing when there is something behind
// the door worth taking.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Identity is this machine, as a peer. It survives restarts, address
// changes and transports; it is written once and then only read.
type Identity struct {
	Peer   string `json:"peer"`
	Secret string `json:"secret"`
	Name   string `json:"name"`
}

// Pairing is what the QR code contains.
type Pairing struct {
	V      int      `json:"v"`
	Peer   string   `json:"peer"`
	Secret string   `json:"secret"`
	Name   string   `json:"name"`
	Hints  []string `json:"hints,omitempty"`
}

func loadOrCreateIdentity(path string) (Identity, error) {
	if b, err := os.ReadFile(path); err == nil {
		var id Identity
		if json.Unmarshal(b, &id) == nil && id.Peer != "" && id.Secret != "" {
			return id, nil
		}
	}
	id := Identity{Peer: token(8), Secret: token(16), Name: machineName()}
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return Identity{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Identity{}, err
	}
	// 0600: the secret is the door.
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return Identity{}, err
	}
	return id, nil
}

func (id Identity) pairing(hints []string) Pairing {
	return Pairing{V: 1, Peer: id.Peer, Secret: id.Secret, Name: id.Name, Hints: hints}
}

func token(n int) string {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand failing is not a thing to paper over with a
		// weaker secret; the caller's alternative is not starting.
		panic("vera: no entropy: " + err.Error())
	}
	return hex.EncodeToString(raw)
}

// machineName is what the phone will call this Mac in a sentence, so
// the bare hostname is better than a fully-qualified one: "seths-mbp",
// not "seths-mbp.lan".
func machineName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "this Mac"
	}
	if dot := strings.IndexByte(h, '.'); dot > 0 {
		h = h[:dot]
	}
	return h
}

// lanHints enumerates the addresses a phone on the same network could
// actually reach, newest-looking first is not a thing we can know, so
// they go out in interface order and the phone tries them all.
//
// Loopback is excluded on purpose: it is reachable from the Mac and
// from nothing else, and a phone that tries it wastes a timeout.
func lanHints(port string) []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil {
			// IPv6 on a LAN is real but a phone reaching a Mac over it
			// is rare enough that offering it first would mostly buy
			// failed connections. When peer-to-peer lands this whole
			// function stops mattering.
			continue
		}
		out = append(out, net.JoinHostPort(ip.String(), port))
	}
	return out
}
