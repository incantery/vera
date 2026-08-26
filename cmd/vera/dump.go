package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/incantery/vera/dump"
	"github.com/incantery/vera/home"
)

// `vera dump`: everything about what just happened, in a folder.
//
// This is how anyone reports Vera doing something wrong — and how
// Vera is looked at afterwards. It reads disk only, so it works when
// verad is the thing that is down.

const dumpUsage = `vera dump [flags] [conversation or task ids...]

  With nothing named: the most recent conversation, and every task it
  touched. An id can be a prefix. Writes a folder (and a .tar.gz) under
  ~/.local/state/vera/dumps, with secrets redacted.

  --since 2h        everything active in the last two hours
  --all             every conversation and task on record
  --note "..."      what went wrong, in your words, at the top of the README
  --out DIR         write here instead
  --no-tar          the folder only
`

func runDump(args []string) error {
	fs := flag.NewFlagSet("dump", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, dumpUsage) }
	since := fs.String("since", "", "")
	all := fs.Bool("all", false, "")
	note := fs.String("note", "", "")
	out := fs.String("out", "", "")
	noTar := fs.Bool("no-tar", false, "")
	// Ids and flags in any order: parse, peel a positional, parse on.
	var ids []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		args = fs.Args()
		if len(args) > 0 {
			ids = append(ids, args[0])
			args = args[1:]
		}
	}
	o := dump.Options{Out: *out, All: *all, Note: *note, Version: version, Tar: !*noTar, HomeDir: home.Path(veraHomeSetting())}
	if *since != "" {
		d, err := time.ParseDuration(*since)
		if err != nil {
			return fmt.Errorf("--since wants a duration like 2h or 45m")
		}
		o.Since = time.Now().Add(-d)
	}
	// Task ids are eight hex characters; anything else names a
	// conversation. Both are prefixes, so a short one is tried as both.
	for _, id := range ids {
		if looksLikeTask(id) {
			o.Tasks = append(o.Tasks, id)
		} else {
			o.Conversations = append(o.Conversations, id)
		}
	}
	res, err := dump.Build(o)
	if err != nil {
		return err
	}
	fmt.Println(describeDump(res))
	return nil
}

func looksLikeTask(id string) bool {
	if len(id) == 0 || len(id) > 8 {
		return false
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func describeDump(res dump.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", res.Dir)
	if res.Tarball != "" {
		fmt.Fprintf(&b, "%s  ← send this\n", res.Tarball)
	}
	fmt.Fprintf(&b, "%d file(s): %d conversation(s), %d task(s), %d Claude Code session(s), %d memory file(s)", res.Files, len(res.Conversations), len(res.Tasks), res.Sessions, res.Memories)
	if res.Sessions > 0 {
		if res.Priced {
			fmt.Fprintf(&b, " ≈ $%.2f", res.CostUSD)
		} else if res.CostUSD > 0 {
			fmt.Fprintf(&b, " ≈ $%.2f (some unpriced)", res.CostUSD)
		}
	}
	return b.String()
}
