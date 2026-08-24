package mux

import (
	"testing"
	"time"
)

func TestParsePane(t *testing.T) {
	p, ok := parsePane("vera\t1\t0\tclaude\t✳ fixing the thing\t/Users/me/vera\t1756000000\t0\n")
	if !ok {
		t.Fatal("did not parse")
	}
	if p.ID != (ID{"vera", "1", "0"}) || p.Command != "claude" || p.Title != "✳ fixing the thing" || p.Path != "/Users/me/vera" {
		t.Errorf("got %+v", p)
	}
	if !p.Active.Equal(time.Unix(1756000000, 0)) || p.Dead {
		t.Errorf("activity/dead wrong: %+v", p)
	}
	if _, ok := parsePane("short\tline"); ok {
		t.Error("short line parsed")
	}
}

func TestPickClient(t *testing.T) {
	out := "\tone\t100\nfocused\ttwo\t50\n\tthree\t200\n"
	if got := pickClient(out); got != "two" {
		t.Errorf("focused flag should win, got %q", got)
	}
	out = "\tone\t100\n\tthree\t200\n"
	if got := pickClient(out); got != "three" {
		t.Errorf("most recent activity should win, got %q", got)
	}
	if got := pickClient(""); got != "" {
		t.Errorf("empty should be empty, got %q", got)
	}
}

func TestShellJoin(t *testing.T) {
	got := shellJoin([]string{"claude", "--settings", "/a b/c.json", "it's a brief"})
	want := `'claude' '--settings' '/a b/c.json' 'it'\''s a brief'`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestIDString(t *testing.T) {
	if s := (ID{"s", "2", "1"}).String(); s != "s:2.1" {
		t.Error(s)
	}
	if !(ID{}).Zero() || (ID{"s", "", ""}).Zero() {
		t.Error("Zero")
	}
}
