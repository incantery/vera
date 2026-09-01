package events

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func at(day, hour int) time.Time {
	return time.Date(2026, 9, day, hour, 0, 0, 0, time.UTC)
}

func newLog(t *testing.T) *Log {
	t.Helper()
	return &Log{Dir: t.TempDir()}
}

func TestAppendShardsByDay(t *testing.T) {
	l := newLog(t)
	if err := l.Append(
		Event{At: at(1, 9), Source: "fleet", Kind: "task.finished", Text: "a said it is done"},
		Event{At: at(2, 9), Source: "git", Kind: "git.commit", Text: "b (abc1234)"},
	); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"2026-09-01.jsonl", "2026-09-02.jsonl"} {
		if _, err := os.Stat(filepath.Join(l.Dir, name)); err != nil {
			t.Fatalf("want a shard %s: %v", name, err)
		}
	}
}

func TestAppendStampsAndRefuses(t *testing.T) {
	l := newLog(t)
	if err := l.Append(Event{Source: "vera", Kind: "vera.asked", Text: "asked: hello"}); err != nil {
		t.Fatal(err)
	}
	got, err := Read(l.Dir, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].At.IsZero() {
		t.Fatalf("want one stamped event, got %+v", got)
	}
	for _, bad := range []Event{
		{Kind: "k", Text: "t"},
		{Source: "s", Text: "t"},
		{Source: "s", Kind: "k"},
	} {
		if err := l.Append(bad); err == nil {
			t.Fatalf("want a refusal for %+v", bad)
		}
	}
}

func TestAppendTrimsLongText(t *testing.T) {
	l := newLog(t)
	if err := l.Append(Event{Source: "vera", Kind: "vera.asked", Text: strings.Repeat("x", maxText*2)}); err != nil {
		t.Fatal(err)
	}
	got, _ := Read(l.Dir, Query{})
	if len(got) != 1 || len(got[0].Text) > maxText {
		t.Fatalf("want the line trimmed to %d, got %d", maxText, len(got[0].Text))
	}
}

func TestReadNewestFirstAndLimited(t *testing.T) {
	l := newLog(t)
	for i := 1; i <= 5; i++ {
		if err := l.Append(Event{At: at(i, 9), Source: "git", Kind: "git.commit", Text: "commit " + string(rune('a'+i))}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Read(l.Dir, Query{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].At.After(got[i-1].At) {
			t.Fatalf("want newest first, got %v then %v", got[i-1].At, got[i].At)
		}
	}
	if !got[0].At.Equal(at(5, 9)) {
		t.Fatalf("want the newest kept, got %v", got[0].At)
	}
}

func TestReadFilters(t *testing.T) {
	l := newLog(t)
	err := l.Append(
		Event{At: at(1, 9), Repo: "rook", Source: "git", Kind: "git.commit", Text: "tabs: the ordinal sits against its chip"},
		Event{At: at(1, 10), Repo: "vera", Source: "fleet", Kind: "task.finished", Task: "T-1", Text: "alpha said it is done"},
		Event{At: at(1, 11), Repo: "vera", Source: "fleet", Kind: "task.decision", Task: "T-2", Text: "beta is blocked on a decision"},
	)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		q    Query
		want int
	}{
		{"repo", Query{Repo: "rook"}, 1},
		{"repo is case-insensitive", Query{Repo: "ROOK"}, 1},
		{"source", Query{Source: "fleet"}, 2},
		{"kind exactly", Query{Kind: "task.finished"}, 1},
		{"kind by prefix", Query{Kind: "task."}, 2},
		{"task", Query{Task: "T-2"}, 1},
		{"text", Query{Text: "ordinal"}, 1},
		{"text finds the kind too", Query{Text: "decision"}, 1},
		{"window", Query{Since: at(1, 10)}, 2},
		{"window with an end", Query{Since: at(1, 10), Until: at(1, 10)}, 1},
		{"nothing matches", Query{Repo: "mote"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Read(l.Dir, c.q)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != c.want {
				t.Fatalf("want %d, got %d: %+v", c.want, len(got), got)
			}
		})
	}
}

func TestReadMissingDirIsEmpty(t *testing.T) {
	got, err := Read(filepath.Join(t.TempDir(), "never"), Query{})
	if err != nil || len(got) != 0 {
		t.Fatalf("want an empty history and no error, got %v %v", got, err)
	}
}

func TestReadSkipsABrokenLine(t *testing.T) {
	l := newLog(t)
	if err := l.Append(Event{At: at(1, 9), Source: "git", Kind: "git.commit", Text: "good"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(l.Dir, "2026-09-01.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"at\":\"2026-09-01T\n")
	f.Close()
	got, err := Read(l.Dir, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "good" {
		t.Fatalf("want the sound line back, got %+v", got)
	}
}

func TestPruneDropsOldShards(t *testing.T) {
	l := &Log{Dir: t.TempDir(), Keep: 2, Now: func() time.Time { return at(1, 9) }}
	if err := l.Append(Event{At: at(1, 9).AddDate(0, 0, -10), Source: "git", Kind: "git.commit", Text: "ancient"}); err != nil {
		t.Fatal(err)
	}
	// The first append prunes on a fresh Log; the ancient shard is
	// written and then, on the next day's append, swept.
	l.Now = func() time.Time { return at(2, 9) }
	if err := l.Append(Event{At: at(2, 9), Source: "git", Kind: "git.commit", Text: "today"}); err != nil {
		t.Fatal(err)
	}
	got, err := Read(l.Dir, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "today" {
		t.Fatalf("want only the recent shard left, got %+v", got)
	}
}

func TestReposByBusiest(t *testing.T) {
	l := newLog(t)
	err := l.Append(
		Event{At: at(1, 9), Repo: "vera", Source: "git", Kind: "git.commit", Text: "one"},
		Event{At: at(1, 10), Repo: "vera", Source: "git", Kind: "git.commit", Text: "two"},
		Event{At: at(1, 11), Repo: "rook", Source: "git", Kind: "git.commit", Text: "three"},
		Event{At: at(1, 12), Source: "machine", Kind: "machine.away", Text: "this machine went away (sleep)"},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Repos(l.Dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "vera" || got[1] != "rook" {
		t.Fatalf("want [vera rook], got %v", got)
	}
}

func TestParseSince(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"all", 0},
		{"24h", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"90m", 90 * time.Minute},
	} {
		got, err := ParseSince(c.in)
		if err != nil || got != c.want {
			t.Fatalf("%q: want %v, got %v (%v)", c.in, c.want, got, err)
		}
	}
	if _, err := ParseSince("soon"); err == nil {
		t.Fatal("want an error for a window nobody can parse")
	}
}
