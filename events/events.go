// Package events is the answer to "what has been going on".
//
// Vera already writes down a great deal. The journal has every
// exchange with the model; the fleet store has every task and its
// status log; the attention model has where the person was looking;
// rook publishes a state snapshot; git holds the commits. All of it is
// true and none of it is one thing you can read. A person coming back
// on Monday — or, much more often here, a fresh agent with no context
// at all — has to open five stores in three formats across two
// repositories and reconstruct a week from them.
//
// This is that one thing. One append-only stream of the moments that
// were significant enough to remember, each a single past-tense line
// with enough keys on it to filter by: which repository it was about,
// who reported it, what kind of moment it was, which task it belonged
// to. It is deliberately an INDEX, not a second copy of the record —
// an event says a task went to a decision, and the fleet store still
// holds what the decision was. Anything that wants the detail follows
// the keys back to the store that owns it.
//
// # Where it lives, and why it is a text file
//
// One JSON object per line, in files named for the UTC day:
//
//	~/.local/state/vera/events/2026-09-01.jsonl
//
// The stream is written by a daemon that gets killed, read by a CLI
// that must work when that daemon is down, and — the deciding case —
// read by coding agents who will reach for `grep` before they reach
// for anything else. A JSONL day-shard is greppable, appends
// atomically under the size of a pipe write, loses at most its last
// line to a crash, and is pruned by deleting a file. A database would
// buy indexes this does not need: the stream is human-rate — a few
// hundred lines on a busy day — and every question asked of it is
// "the last N, in a window, matching a couple of exact keys".
//
// It is the same shape as the journal next door on purpose. Two record
// formats in one state directory is one too many.
package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Event is one thing that happened, in one line.
//
// The fields are the ones every question is asked along. Everything
// else goes in Fields, as strings: an event is read by people and by
// models, and a nested object in a line meant to be grepped is a
// structure nobody can see. Depth belongs in the store the event
// points at.
type Event struct {
	At time.Time `json:"at"`
	// Repo is the short name of the repository this is about — "vera",
	// "rook" — or empty when it is about neither. It is the one key
	// that makes a two-repository history readable as two histories.
	Repo string `json:"repo,omitempty"`
	// Source is who reported it: "fleet", "vera", "git", "machine",
	// "rook", or the name an outside publisher gave itself.
	Source string `json:"source"`
	// Kind is what sort of moment it was, dotted from general to
	// specific: "task.decision", "git.commit", "rook.gone". The prefix
	// is the filter people actually type.
	Kind string `json:"kind"`
	// Text is the whole event in one past-tense line, written to be
	// read on its own with no other column visible.
	Text string `json:"text"`
	// Subject is what it was about in the source's own naming — a
	// commit sha, a workspace name, a conversation id.
	Subject string `json:"subject,omitempty"`
	// Task is the fleet task id, when the moment belonged to one; it
	// is how a whole task's history comes back in one query.
	Task string `json:"task,omitempty"`
	// Project is the repository root on disk. Repo is what you filter
	// on; this is what you cd to.
	Project string `json:"project,omitempty"`
	// Fields is everything the source wanted kept and this struct has
	// no opinion about.
	Fields map[string]string `json:"fields,omitempty"`
}

// Valid says whether an event carries the three things without which
// it cannot be read back: who said it, what kind of thing it was, and
// what happened. A line missing any of them is noise in a stream whose
// only job is to be read.
func (e Event) Valid() error {
	switch {
	case strings.TrimSpace(e.Source) == "":
		return errors.New("an event needs a source")
	case strings.TrimSpace(e.Kind) == "":
		return errors.New("an event needs a kind")
	case strings.TrimSpace(e.Text) == "":
		return errors.New("an event needs a line of text")
	}
	return nil
}

// maxText bounds one line. An event is a headline; a paragraph in the
// stream is a paragraph nobody reads and a file nobody greps.
const maxText = 500

// Log appends to the stream. The zero value needs only Dir.
type Log struct {
	// Dir is the directory of day-shards; it is created on first write.
	Dir string
	// Keep is how many days of shards survive; zero keeps 90. Pruning
	// happens when the day rolls over, not on every append.
	Keep int
	// Now is the clock, for tests.
	Now func() time.Time

	mu     sync.Mutex
	shard  string // the file the last append went to
	pruned bool
}

// DefaultKeep is a quarter: long enough that "what happened last
// month" is answerable, short enough that the directory stays a
// directory a person can list.
const DefaultKeep = 90

func (l *Log) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Append writes events in order. An event with no timestamp is stamped
// now; one that is not Valid is refused rather than written, because a
// line that cannot be read is worse than a line that is missing.
//
// A failure to write is returned but is never worth failing a caller
// over: nothing in Vera should stop working because its diary did.
func (l *Log) Append(evs ...Event) error {
	if l == nil || len(evs) == 0 {
		return nil
	}
	now := l.now()
	lines := make([][]byte, 0, len(evs))
	var day string
	for _, e := range evs {
		if e.At.IsZero() {
			e.At = now
		}
		if err := e.Valid(); err != nil {
			return err
		}
		e.Text = trim(e.Text, maxText)
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		lines = append(lines, append(b, '\n'))
		day = shardOf(e.At)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return err
	}
	// Every event in one call shares a day in practice; the shard is
	// chosen per event so a batch that straddles midnight still lands
	// in the right files.
	var firstErr error
	for i, e := range evs {
		at := e.At
		if at.IsZero() {
			at = now
		}
		if err := l.appendTo(shardOf(at), lines[i]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if day != "" && day != l.shard {
		l.shard, l.pruned = day, false
	}
	if !l.pruned {
		l.pruned = true
		_ = l.prune(now)
	}
	return firstErr
}

func (l *Log) appendTo(day string, line []byte) error {
	f, err := os.OpenFile(filepath.Join(l.Dir, day+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(line)
	return err
}

// prune removes shards older than Keep days. A file is the unit, so
// this is a directory listing and a handful of unlinks.
func (l *Log) prune(now time.Time) error {
	keep := l.Keep
	if keep <= 0 {
		keep = DefaultKeep
	}
	cutoff := now.UTC().AddDate(0, 0, -keep).Format(shardLayout)
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		return err
	}
	for _, d := range entries {
		day, ok := shardDay(d.Name())
		if !ok || day >= cutoff {
			continue
		}
		_ = os.Remove(filepath.Join(l.Dir, d.Name()))
	}
	return nil
}

const shardLayout = "2006-01-02"

func shardOf(t time.Time) string { return t.UTC().Format(shardLayout) }

// shardDay reads a file name back into its day, and says no to
// anything else in the directory — cursors live there too.
func shardDay(name string) (string, bool) {
	day := strings.TrimSuffix(name, ".jsonl")
	if day == name {
		return "", false
	}
	if _, err := time.Parse(shardLayout, day); err != nil {
		return "", false
	}
	return day, true
}

// trim flattens a line and bounds it, in bytes, ellipsis included: the
// cap is there so a shard stays greppable, and an ellipsis that pushed
// the line past it would defeat the point.
func trim(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	const ellipsis = "…"
	cut := n - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + ellipsis
}

// ParseSince reads the window a person types: 7d, 24h, 90m, 2w, or
// "all" for no window at all.
func ParseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "all" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") || strings.HasSuffix(s, "w") {
		unit := 24 * time.Hour
		if strings.HasSuffix(s, "w") {
			unit = 7 * 24 * time.Hour
		}
		n, err := strconv.ParseFloat(strings.TrimRight(s, "dw"), 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("--since %q: a number and d, h, m or s", s)
		}
		return time.Duration(n * float64(unit)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("--since %q: a number and d, h, m or s", s)
	}
	return d, nil
}

// sortNewestFirst is the order everything in this package hands back:
// the question is always about the recent past.
func sortNewestFirst(evs []Event) {
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].At.After(evs[j].At) })
}
