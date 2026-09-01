package events

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Query is one question about the stream. Every field narrows; the
// zero value asks for everything, newest first.
type Query struct {
	// Since and Until bound the window. Zero means unbounded.
	Since time.Time
	Until time.Time
	// Repo, Source and Task match exactly, case-insensitively. Empty
	// does not filter.
	Repo   string
	Source string
	Task   string
	// Kind matches exactly, or as a prefix when it ends in a dot:
	// "task." is every task event, "task.decision" is one.
	Kind string
	// Text is a substring anywhere a person would look — the line, the
	// subject, the kind — case-insensitive.
	Text string
	// Limit bounds the answer; zero means DefaultLimit. The newest are
	// kept, because a truncated history is only useful from the end.
	Limit int
}

// DefaultLimit is a screenful and change: enough that "what happened"
// is answered without a pager, small enough to paste into a prompt.
const DefaultLimit = 200

func (q Query) limit() int {
	if q.Limit <= 0 {
		return DefaultLimit
	}
	return q.Limit
}

// match says whether one event answers the question.
func (q Query) match(e Event) bool {
	if !q.Since.IsZero() && e.At.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && e.At.After(q.Until) {
		return false
	}
	if !eqFold(q.Repo, e.Repo) || !eqFold(q.Source, e.Source) || !eqFold(q.Task, e.Task) {
		return false
	}
	if k := strings.TrimSpace(q.Kind); k != "" {
		if strings.HasSuffix(k, ".") {
			if !strings.HasPrefix(strings.ToLower(e.Kind), strings.ToLower(k)) {
				return false
			}
		} else if !strings.EqualFold(k, e.Kind) {
			return false
		}
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		hay := strings.ToLower(e.Text + " " + e.Subject + " " + e.Kind + " " + e.Repo)
		if !strings.Contains(hay, strings.ToLower(t)) {
			return false
		}
	}
	return true
}

func eqFold(want, got string) bool {
	want = strings.TrimSpace(want)
	return want == "" || strings.EqualFold(want, got)
}

// Read answers a query from the shards under dir, newest first. A
// directory that does not exist is an empty history, not an error: a
// machine that has never run verad has genuinely had nothing happen.
//
// Shards are read from the newest day backwards and stop as soon as
// the limit is filled, so asking for the last twenty things on a
// machine with a quarter of history opens one file.
func Read(dir string, q Query) ([]Event, error) {
	days, err := shards(dir)
	if err != nil {
		return nil, err
	}
	limit := q.limit()
	var out []Event
	for _, day := range days {
		if !q.Since.IsZero() && day < shardOf(q.Since) {
			break
		}
		if !q.Until.IsZero() && day > shardOf(q.Until) {
			continue
		}
		evs, err := readShard(filepath.Join(dir, day+".jsonl"), q)
		if err != nil {
			continue
		}
		out = append(out, evs...)
		if len(out) >= limit {
			// A shard is chronological but the stream across shards is
			// only nearly so — an event stamped by a phone in another
			// timezone, or a batch that straddled midnight. Sort what
			// is in hand before cutting.
			sortNewestFirst(out)
			return out[:limit], nil
		}
	}
	sortNewestFirst(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// readShard returns the matching events of one file, newest first. A
// line that does not parse is skipped: a half-written last line must
// not hide the day above it.
func readShard(path string, q Query) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if q.match(e) {
			out = append(out, e)
		}
	}
	sortNewestFirst(out)
	return out, sc.Err()
}

// shards lists the day files under dir, newest first.
func shards(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var days []string
	for _, d := range entries {
		if d.IsDir() {
			continue
		}
		if day, ok := shardDay(d.Name()); ok {
			days = append(days, day)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	return days, nil
}

// Repos is every repository the stream has heard of in a window,
// commonest first. It is what a caller with no idea what to ask for
// asks first.
func Repos(dir string, since time.Time) ([]string, error) {
	evs, err := Read(dir, Query{Since: since, Limit: 100000})
	if err != nil {
		return nil, err
	}
	count := map[string]int{}
	for _, e := range evs {
		if e.Repo != "" {
			count[e.Repo]++
		}
	}
	out := make([]string, 0, len(count))
	for r := range count {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if count[out[i]] != count[out[j]] {
			return count[out[i]] > count[out[j]]
		}
		return out[i] < out[j]
	})
	return out, nil
}
