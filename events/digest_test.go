package events

import (
	"strings"
	"testing"
	"time"
)

func TestSummarizeGroupsByRepoBusiestFirst(t *testing.T) {
	now := at(3, 12)
	evs := []Event{
		{At: at(3, 9), Repo: "vera", Source: "git", Kind: "git.commit", Text: "one"},
		{At: at(3, 10), Repo: "vera", Source: "git", Kind: "git.commit", Text: "two"},
		{At: at(2, 10), Repo: "vera", Source: "git", Kind: "git.commit", Text: "three"},
		{At: at(3, 11), Repo: "rook", Source: "git", Kind: "git.commit", Text: "four"},
		{At: at(3, 8), Source: "machine", Kind: "machine.away", Text: "this machine went away (sleep)"},
	}
	d := Summarize(evs, at(1, 0), now)
	if d.Total != 5 {
		t.Fatalf("want 5 total, got %d", d.Total)
	}
	if len(d.Repos) != 3 {
		t.Fatalf("want three groups, got %d", len(d.Repos))
	}
	if d.Repos[0].Repo != "vera" || d.Repos[1].Repo != "rook" {
		t.Fatalf("want the busiest repository first, got %q then %q", d.Repos[0].Repo, d.Repos[1].Repo)
	}
	if last := d.Repos[2]; last.Repo != "" || last.Name() != "elsewhere" {
		t.Fatalf("want the unnamed group last and called elsewhere, got %+v", last)
	}
	vera := d.Repos[0]
	if len(vera.Days) != 2 {
		t.Fatalf("want two days in vera, got %d", len(vera.Days))
	}
	if vera.Days[0].Day <= vera.Days[1].Day {
		t.Fatalf("want the newest day first, got %v", vera.Days)
	}
	if got := vera.Days[0].Events[0].Text; got != "two" {
		t.Fatalf("want the newest event first within a day, got %q", got)
	}
}

func TestDigestTextAndMarkdownCarryEveryLine(t *testing.T) {
	evs := []Event{
		{At: at(3, 9), Repo: "vera", Source: "fleet", Kind: "task.finished", Text: "alpha said it is done"},
		{At: at(3, 10), Repo: "rook", Source: "git", Kind: "git.commit", Text: "tabs: the ordinal sits (abc1234)"},
	}
	d := Summarize(evs, at(1, 0), at(3, 12))
	for _, out := range []string{d.Text(), d.Markdown()} {
		for _, want := range []string{"alpha said it is done", "tabs: the ordinal sits", "vera", "rook", "task.finished"} {
			if !strings.Contains(out, want) {
				t.Fatalf("want %q in\n%s", want, out)
			}
		}
	}
}

func TestDigestSaysWhenNothingHappened(t *testing.T) {
	d := Summarize(nil, at(1, 0), at(3, 12))
	if !strings.Contains(d.Text(), "Nothing was recorded") {
		t.Fatalf("want an empty window said out loud, got %q", d.Text())
	}
}

func TestDayHeadingSpeaksLikeAPerson(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.Local)
	for _, c := range []struct{ day, want string }{
		{"2026-09-03", "today"},
		{"2026-09-02", "yesterday"},
		{"2026-08-20", "Thu 20 Aug"},
	} {
		if got := dayHeading(c.day, now); got != c.want {
			t.Fatalf("dayHeading(%q) = %q, want %q", c.day, got, c.want)
		}
	}
	// Inside the week it is the weekday's name, whatever that is.
	if got := dayHeading("2026-08-31", now); got != "Monday" {
		t.Fatalf("want a weekday name inside the week, got %q", got)
	}
}
