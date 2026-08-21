package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentitySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")

	first, err := loadOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("identity changed across a restart: %+v then %+v", first, second)
	}
	if first.Peer == "" || first.Secret == "" {
		t.Fatal("an identity with no peer or no secret is not an identity")
	}

	// The secret is the door; the file it lives in should say so.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("identity file is %v, want 0600", perm)
	}
}

func TestTwoMachinesAreNotTheSameMachine(t *testing.T) {
	a, _ := loadOrCreateIdentity(filepath.Join(t.TempDir(), "id.json"))
	b, _ := loadOrCreateIdentity(filepath.Join(t.TempDir(), "id.json"))
	if a.Peer == b.Peer || a.Secret == b.Secret {
		t.Fatal("two machines minted the same identity")
	}
}

// The point of the whole pairing design: a code that is still true when
// the address it was minted on is gone.
func TestPairingIdentifiesTheMachineNotTheNetwork(t *testing.T) {
	id, _ := loadOrCreateIdentity(filepath.Join(t.TempDir(), "id.json"))

	atHome := id.pairing([]string{"192.168.1.20:4780"})
	atAHotel := id.pairing([]string{"10.55.3.9:4780"})

	if atHome.Peer != atAHotel.Peer || atHome.Secret != atAHotel.Secret {
		t.Fatal("the machine's identity moved when its address did")
	}

	// And a code with no address at all is still a valid pairing —
	// which is what the peer-to-peer transport will hand over.
	none := id.pairing(nil)
	b, err := json.Marshal(none)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "hints") {
		t.Fatalf("an address-free pairing still mentions addresses: %s", b)
	}
	if none.V == 0 {
		t.Fatal("pairing carries no version; the phone will not know how to read a later one")
	}
}

func TestLANHintsSkipLoopback(t *testing.T) {
	for _, hint := range lanHints("4780") {
		if strings.HasPrefix(hint, "127.") || strings.HasPrefix(hint, "[::1]") {
			t.Fatalf("offered a phone an address only this Mac can reach: %s", hint)
		}
		if !strings.HasSuffix(hint, ":4780") {
			t.Fatalf("hint %q does not name the port that is actually listening", hint)
		}
	}
}
