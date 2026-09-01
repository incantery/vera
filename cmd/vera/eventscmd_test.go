package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/incantery/vera/events"
)

// writeEvents puts a stream where `vera events` will look for it, the
// way verad does — through the same state directory both sides
// compute, which is the half of this that is worth a test.
func writeEvents(t *testing.T, evs ...events.Event) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	l := &events.Log{Dir: eventsDir()}
	if err := l.Append(evs...); err != nil {
		t.Fatal(err)
	}
}

// capture runs f with stdout redirected, and hands back what it wrote.
func capture(t *testing.T, f func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	ferr := f()
	os.Stdout = old
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	if ferr != nil {
		t.Fatal(ferr)
	}
	return string(out)
}

func sample() []events.Event {
	now := time.Now()
	return []events.Event{
		{At: now.Add(-2 * time.Hour), Repo: "rook", Source: "git", Kind: "git.commit", Text: "tabs: the ordinal sits (abc1234)"},
		{At: now.Add(-time.Hour), Repo: "vera", Source: "fleet", Kind: "task.decision", Task: "T-1", Text: "alpha is blocked on a decision"},
		{At: now.Add(-30 * time.Minute), Source: "machine", Kind: "machine.away", Text: "this machine went away (sleep)"},
	}
}

func TestEventsCommandGroupsByRepository(t *testing.T) {
	writeEvents(t, sample()...)
	out := capture(t, func() error { return runEvents(nil) })
	for _, want := range []string{"what happened", "rook", "vera", "elsewhere", "alpha is blocked", "tabs: the ordinal sits"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in:\n%s", want, out)
		}
	}
}

func TestEventsCommandFilters(t *testing.T) {
	writeEvents(t, sample()...)
	for _, c := range []struct {
		args    []string
		want    string
		absent  string
		nLines  int
		flatten bool
	}{
		{args: []string{"--repo", "rook", "--flat"}, want: "tabs: the ordinal sits", absent: "alpha is blocked", nLines: 1},
		{args: []string{"--kind", "task.", "--flat"}, want: "alpha is blocked", absent: "ordinal", nLines: 1},
		{args: []string{"--task", "T-1", "--flat"}, want: "alpha is blocked", absent: "ordinal", nLines: 1},
		{args: []string{"--source", "machine", "--flat"}, want: "went away", absent: "ordinal", nLines: 1},
		{args: []string{"-q", "ordinal", "--flat"}, want: "ordinal", absent: "alpha", nLines: 1},
		{args: []string{"--flat"}, want: "ordinal", nLines: 3},
		{args: []string{"--flat", "-n", "2"}, want: "went away", nLines: 2},
	} {
		out := capture(t, func() error { return runEvents(c.args) })
		if !strings.Contains(out, c.want) {
			t.Fatalf("%v: want %q in:\n%s", c.args, c.want, out)
		}
		if c.absent != "" && strings.Contains(out, c.absent) {
			t.Fatalf("%v: did not want %q in:\n%s", c.args, c.absent, out)
		}
		if n := len(strings.Split(strings.TrimSpace(out), "\n")); n != c.nLines {
			t.Fatalf("%v: want %d lines, got %d:\n%s", c.args, c.nLines, n, out)
		}
	}
}

func TestEventsCommandJSONIsOnePerLine(t *testing.T) {
	writeEvents(t, sample()...)
	out := capture(t, func() error { return runEvents([]string{"--json"}) })
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("want three lines, got %d:\n%s", len(lines), out)
	}
	for _, line := range lines {
		var e events.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if e.Kind == "" || e.Text == "" {
			t.Fatalf("want a whole event per line, got %+v", e)
		}
	}
}

func TestEventsCommandWindow(t *testing.T) {
	writeEvents(t, events.Event{At: time.Now().Add(-8 * 24 * time.Hour), Repo: "vera", Source: "git", Kind: "git.commit", Text: "last week (0000000)"})
	if out := capture(t, func() error { return runEvents([]string{"--flat"}) }); strings.Contains(out, "last week") {
		t.Fatalf("want a day by default, got:\n%s", out)
	}
	if out := capture(t, func() error { return runEvents([]string{"--flat", "--since", "2w"}) }); !strings.Contains(out, "last week") {
		t.Fatalf("want the wider window to reach it, got:\n%s", out)
	}
	if err := runEvents([]string{"--since", "soon"}); err == nil {
		t.Fatal("want a window nobody can parse refused")
	}
}

func TestEventsCommandSaysNothingHappenedOutLoud(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out := capture(t, func() error { return runEvents(nil) })
	if !strings.Contains(out, "Nothing was recorded") {
		t.Fatalf("want an empty history said out loud, got:\n%s", out)
	}
}

// Both sides of the wire have to agree on where the stream is: verad
// writes under its own state directory and this reads under its own,
// and they are the same path or nothing works.
func TestEventsDirIsWhereVeradWrites(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	if got, want := eventsDir(), filepath.Join(root, "vera", "events"); got != want {
		t.Fatalf("want %s, got %s", want, got)
	}
}

func TestEventQueryFromOneLine(t *testing.T) {
	for _, c := range []struct {
		spec  string
		repo  string
		text  string
		since time.Duration // 0 means the default week; -1 means none
	}{
		{spec: "", since: defaultChatWindow},
		{spec: "@rook", repo: "rook", since: defaultChatWindow},
		{spec: "7d @rook blocked", repo: "rook", text: "blocked", since: 7 * 24 * time.Hour},
		{spec: "blocked @rook 24h", repo: "rook", text: "blocked", since: 24 * time.Hour},
		{spec: "which database", text: "which database", since: defaultChatWindow},
		{spec: "all", since: -1},
	} {
		q, err := eventQueryFrom(c.spec)
		if err != nil {
			t.Fatalf("%q: %v", c.spec, err)
		}
		if q.Repo != c.repo || q.Text != c.text {
			t.Fatalf("%q: want repo %q text %q, got %+v", c.spec, c.repo, c.text, q)
		}
		switch {
		case c.since == -1:
			if !q.Since.IsZero() {
				t.Fatalf("%q: want no window, got %v", c.spec, q.Since)
			}
		default:
			if d := time.Since(q.Since); d < c.since-time.Minute || d > c.since+time.Minute {
				t.Fatalf("%q: want a window of %v, got %v", c.spec, c.since, d)
			}
		}
	}
	if _, err := eventQueryFrom("9z"); err == nil {
		t.Fatal("want a window nobody can parse refused")
	}
}

// A word that starts with a letter is a search term even when it looks
// like a unit: /events day is looking for the word.
func TestLooksLikeWindowReadsTheIntent(t *testing.T) {
	for _, yes := range []string{"all", "7d", "24h", "90m", "2w", "9z"} {
		if !looksLikeWindow(yes) {
			t.Fatalf("want %q read as an attempt at a window", yes)
		}
	}
	for _, no := range []string{"", "day", "days", "blocked", "@rook", "d7"} {
		if looksLikeWindow(no) {
			t.Fatalf("want %q read as a search term", no)
		}
	}
}
