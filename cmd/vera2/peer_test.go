package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFramingSurvivesARoundTrip(t *testing.T) {
	var wire bytes.Buffer
	sent := []Frame{
		{Run: "r1"},
		{Status: "Working on it…"},
		{Delta: "Vienna"},
		{Delta: " is the capital."},
		{Done: true},
	}
	for _, f := range sent {
		if err := writeFrame(&wire, f); err != nil {
			t.Fatal(err)
		}
	}

	var got []Frame
	for range sent {
		payload, err := readFrame(&wire)
		if err != nil {
			t.Fatal(err)
		}
		var f Frame
		if err := json.Unmarshal(payload, &f); err != nil {
			t.Fatal(err)
		}
		got = append(got, f)
	}
	if len(got) != len(sent) || got[2].Delta != "Vienna" || !got[4].Done {
		t.Fatalf("round trip produced %+v", got)
	}
	if wire.Len() != 0 {
		t.Fatalf("%d bytes left over — the lengths do not line up", wire.Len())
	}
}

// Frames arrive in pieces over a real link, and a reader that assumed
// one read per frame would work locally and fail on the radio.
func TestAFrameSplitAcrossReadsStillParses(t *testing.T) {
	var whole bytes.Buffer
	_ = writeFrame(&whole, Frame{Delta: strings.Repeat("x", 5000)})

	drip := &dripReader{data: whole.Bytes(), at: 0, chunk: 7}
	payload, err := readFrame(drip)
	if err != nil {
		t.Fatal(err)
	}
	var f Frame
	_ = json.Unmarshal(payload, &f)
	if len(f.Delta) != 5000 {
		t.Fatalf("reassembled %d bytes", len(f.Delta))
	}
}

type dripReader struct {
	data  []byte
	at    int
	chunk int
}

func (d *dripReader) Read(p []byte) (int, error) {
	if d.at >= len(d.data) {
		return 0, os.ErrClosed
	}
	n := min(min(d.chunk, len(p)), len(d.data)-d.at)
	copy(p, d.data[d.at:d.at+n])
	d.at += n
	return n, nil
}

func TestAbsurdLengthsAreRefused(t *testing.T) {
	// 0xFFFFFFFF bytes of "payload" would be an allocation, not a frame.
	hostile := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	if _, err := readFrame(bytes.NewReader(hostile)); err == nil {
		t.Fatal("a four-gigabyte frame was accepted")
	}
}

// macOS caps a unix socket path near 104 bytes and reports "invalid
// argument" when exceeded, which reads like anything but a length
// problem.
func TestLongSocketPathsAreShortened(t *testing.T) {
	short := filepath.Join("/tmp", "vera2", "peer.sock")
	if got := shortSocket(short); got != short {
		t.Fatalf("a short path was rewritten to %q", got)
	}

	long := filepath.Join("/tmp", strings.Repeat("deeply-nested-directory/", 8), "peer.sock")
	got := shortSocket(long)
	if len(got) > 104 {
		t.Fatalf("shortened path is still %d bytes: %q", len(got), got)
	}
	if !strings.HasSuffix(got, ".sock") {
		t.Fatalf("shortened path lost its shape: %q", got)
	}
	// Stable, so a restart finds the same socket rather than leaking one.
	if shortSocket(long) != got {
		t.Fatal("shortening is not deterministic")
	}
}

// The pairing code has to work for a transport that has no addresses,
// which is the entire reason it carries an identity.
func TestPeerTransportOffersNoAddresses(t *testing.T) {
	p := newPeer(Identity{Peer: "abc", Secret: "s", Name: "mac"}, "/tmp/x.sock", "/bin/true")
	if hints := p.Hints(); len(hints) != 0 {
		t.Fatalf("the peer transport offered addresses: %v", hints)
	}
	if p.Name() != "peer" {
		t.Fatalf("named %q", p.Name())
	}
}
