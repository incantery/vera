package events

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Digest is the stream rendered for reading rather than for filtering:
// grouped by repository, then by day, newest first.
//
// The grouping is the whole point. A flat reverse-chronological list of
// two repositories interleaved is exactly as hard to read as the five
// stores this package replaced — you spend the first pass sorting rook
// from vera in your head. Grouped, "what happened in rook this week" is
// a heading, and that is nearly always the question.
type Digest struct {
	// Since is the window asked for; zero means all of it.
	Since time.Time
	Now   time.Time
	Total int
	Repos []RepoDigest
}

// RepoDigest is one repository's slice, or the one holding everything
// that belonged to no repository.
type RepoDigest struct {
	// Repo is the short name; empty is the "elsewhere" group.
	Repo  string
	Count int
	Days  []DayDigest
}

// DayDigest is one calendar day of one repository, newest first.
type DayDigest struct {
	Day    string // "2026-09-01"
	Events []Event
}

// Name is the heading a repository is drawn under.
func (r RepoDigest) Name() string {
	if r.Repo == "" {
		return "elsewhere"
	}
	return r.Repo
}

// Summarize groups events for reading. The input need not be sorted.
func Summarize(evs []Event, since, now time.Time) Digest {
	if now.IsZero() {
		now = time.Now()
	}
	d := Digest{Since: since, Now: now, Total: len(evs)}
	byRepo := map[string][]Event{}
	for _, e := range evs {
		byRepo[e.Repo] = append(byRepo[e.Repo], e)
	}
	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	// Busiest repository first, named ones before the unnamed group:
	// "elsewhere" is a footnote however many lines are in it.
	sort.Slice(repos, func(i, j int) bool {
		a, b := repos[i], repos[j]
		if (a == "") != (b == "") {
			return b == ""
		}
		if len(byRepo[a]) != len(byRepo[b]) {
			return len(byRepo[a]) > len(byRepo[b])
		}
		return a < b
	})
	for _, r := range repos {
		list := byRepo[r]
		sortNewestFirst(list)
		rd := RepoDigest{Repo: r, Count: len(list)}
		for _, e := range list {
			day := e.At.Local().Format(shardLayout)
			if n := len(rd.Days); n > 0 && rd.Days[n-1].Day == day {
				rd.Days[n-1].Events = append(rd.Days[n-1].Events, e)
				continue
			}
			rd.Days = append(rd.Days, DayDigest{Day: day, Events: []Event{e}})
		}
		d.Repos = append(d.Repos, rd)
	}
	return d
}

// Text is the digest for a terminal and for a model's context: no
// markdown, no colour, one event per line, aligned enough to skim.
func (d Digest) Text() string {
	var b strings.Builder
	b.WriteString(d.headline() + "\n")
	if d.Total == 0 {
		b.WriteString("\nNothing was recorded in that window.\n")
		return b.String()
	}
	for _, r := range d.Repos {
		fmt.Fprintf(&b, "\n%s (%d)\n", r.Name(), r.Count)
		for _, day := range r.Days {
			fmt.Fprintf(&b, "  %s\n", dayHeading(day.Day, d.Now))
			for _, e := range day.Events {
				fmt.Fprintf(&b, "    %s  %-16s %s\n", e.At.Local().Format("15:04"), e.Kind, e.Text)
			}
		}
	}
	return b.String()
}

// Markdown is the same digest on a screen that renders it — the chat's
// /events, and anywhere else a card is drawn.
func (d Digest) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**\n", d.headline())
	if d.Total == 0 {
		b.WriteString("\nNothing was recorded in that window.\n")
		return b.String()
	}
	for _, r := range d.Repos {
		fmt.Fprintf(&b, "\n### %s · %d\n", r.Name(), r.Count)
		for _, day := range r.Days {
			fmt.Fprintf(&b, "\n%s\n\n", dayHeading(day.Day, d.Now))
			for _, e := range day.Events {
				fmt.Fprintf(&b, "- `%s` %s — %s\n", e.At.Local().Format("15:04"), e.Kind, e.Text)
			}
		}
	}
	return b.String()
}

func (d Digest) headline() string {
	when := "all of it"
	if !d.Since.IsZero() {
		when = "the last " + humanSpan(d.Now.Sub(d.Since))
	}
	return fmt.Sprintf("what happened — %s, %s", when, plural(d.Total, "event"))
}

// dayHeading names a day the way a person would say it out loud.
func dayHeading(day string, now time.Time) string {
	t, err := time.ParseInLocation(shardLayout, day, time.Local)
	if err != nil {
		return day
	}
	// Local midnight, not Truncate: truncation is against the epoch in
	// UTC, so anywhere but UTC it cuts the day in the wrong place and
	// "today" becomes "yesterday" for half the afternoon.
	today := midnight(now.Local())
	switch days := int(today.Sub(midnight(t)).Hours()/24 + 0.5); {
	case days == 0:
		return "today"
	case days == 1:
		return "yesterday"
	case days < 7:
		return t.Format("Monday")
	default:
		return t.Format("Mon 2 Jan")
	}
}

func midnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func humanSpan(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return plural(int(d.Hours()/24+0.5), "day")
	case d >= 2*time.Hour:
		return plural(int(d.Hours()+0.5), "hour")
	case d >= time.Minute:
		return plural(int(d.Minutes()+0.5), "minute")
	default:
		return "moment"
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
