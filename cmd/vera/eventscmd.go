package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/incantery/vera/events"
)

// `vera events`: what has been going on, in one place.
//
// Like `vera costs`, it reads files and asks nobody. That is not a
// convenience — it is the requirement. The commonest reader of this
// stream is a coding agent that has just been handed a repository and
// no context, and the second commonest is a person working out why the
// daemon is not running. Neither can be told to start it first.
const eventsUsage = `vera events [--since 24h] [--repo rook] [--kind task.] [-q words]

  --since   how far back: 24h, 7d, 2w, or "all" (default 24h)
  --repo    one repository: rook, vera — the heading it appears under
  --source  who reported it: fleet, vera, git, machine, rook
  --kind    what sort: task.decision, git.commit, or a prefix like "task."
  --task    one task's whole history, by id
  -q        a substring anywhere in the line
  -n        how many at most (default 200)
  --flat    one line each, newest first, no grouping — for a pipe
  --json    one JSON object per line — for a program
`

func runEvents(args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, eventsUsage) }
	since := fs.String("since", "24h", "")
	repo := fs.String("repo", "", "")
	source := fs.String("source", "", "")
	kind := fs.String("kind", "", "")
	task := fs.String("task", "", "")
	text := fs.String("q", "", "")
	limit := fs.Int("n", events.DefaultLimit, "")
	flat := fs.Bool("flat", false, "")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	window, err := events.ParseSince(*since)
	if err != nil {
		return err
	}
	q := events.Query{Repo: *repo, Source: *source, Kind: *kind, Task: *task, Text: *text, Limit: *limit}
	now := time.Now()
	if window > 0 {
		q.Since = now.Add(-window)
	}
	evs, err := events.Read(eventsDir(), q)
	if err != nil {
		return err
	}
	switch {
	case *asJSON:
		enc := json.NewEncoder(os.Stdout)
		for _, e := range evs {
			if err := enc.Encode(e); err != nil {
				return err
			}
		}
	case *flat:
		for _, e := range evs {
			fmt.Printf("%s  %-6s %-16s %s\n", e.At.Local().Format("2006-01-02 15:04"), where(e), e.Kind, e.Text)
		}
	default:
		fmt.Print(events.Summarize(evs, q.Since, now).Text())
	}
	return nil
}

// where is the repository column, with a stand-in so the columns line
// up for the events that belong to neither repository.
func where(e events.Event) string {
	if e.Repo == "" {
		return "—"
	}
	return e.Repo
}

// eventsDir is where the stream lives, and the one place that says so
// on this side of the wire.
func eventsDir() string { return filepath.Join(stateDir(), "events") }

// eventQueryFrom reads `/events 7d @rook blocked` — the same three
// answers as the flags, in the words a person types on one line. Order
// does not matter and none of them is required: a window is a duration,
// a repository wears an @, and everything else is what to look for.
func eventQueryFrom(spec string) (events.Query, error) {
	q := events.Query{Since: time.Now().Add(-defaultChatWindow)}
	var words []string
	for _, f := range strings.Fields(spec) {
		switch {
		case strings.HasPrefix(f, "@"):
			q.Repo = strings.TrimPrefix(f, "@")
		case looksLikeWindow(f):
			d, err := events.ParseSince(f)
			if err != nil {
				return q, err
			}
			if d == 0 {
				q.Since = time.Time{}
			} else {
				q.Since = time.Now().Add(-d)
			}
		default:
			words = append(words, f)
		}
	}
	q.Text = strings.Join(words, " ")
	return q, nil
}

// defaultChatWindow is a week rather than the CLI's day: the chat is
// where "what have we been doing" gets asked, and the terminal is
// where "what just happened" does.
const defaultChatWindow = 7 * 24 * time.Hour

// looksLikeWindow says whether a word was MEANT as a span. It reads
// the intent, not the spelling: "all", or anything starting with a
// digit. So searching for the word "day" is not mistaken for asking
// for one, and "/events 9z" is told it is not a window rather than
// quietly going off to look for the string "9z".
func looksLikeWindow(f string) bool {
	return f == "all" || (f != "" && f[0] >= '0' && f[0] <= '9')
}
