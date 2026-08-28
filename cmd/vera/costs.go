package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/incantery/vera/costs"
	"github.com/incantery/vera/dump"
)

// `vera costs`: what the journal says every exchange cost, in one
// table. It reads files and asks nobody — verad does not have to be
// running, and a machine that has been switched off for a week can
// still answer what last week cost.
const costsUsage = `vera costs [--since 7d] [--by model|conversation|day]

  --since   how far back: 7d, 24h, 90m, or "all" (default 7d)
  --by      what a row is: model (with its effort), conversation, or day
`

func runCosts(args []string) error {
	fs := flag.NewFlagSet("costs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, costsUsage) }
	since := fs.String("since", "7d", "")
	by := fs.String("by", costs.ByModel, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	window, err := costs.ParseSince(*since)
	if err != nil {
		return err
	}
	rep, err := costs.Build(costOptions(window, *by))
	if err != nil {
		return err
	}
	fmt.Print(rep.Text())
	return nil
}

// costOptions is where everything lives, in one place, so the verb and
// the chat's /costs read exactly the same files.
func costOptions(since time.Duration, by string) costs.Options {
	return costs.Options{
		Dir:       filepath.Join(stateDir(), "conversations"),
		FleetDir:  filepath.Join(stateDir(), "fleet"),
		ClaudeDir: dump.ClaudeDir(),
		Since:     since,
		By:        by,
	}
}

// costOptionsFrom reads `/costs 24h by conversation` — the same two
// answers as the flags, in the words a person types on one line. Order
// does not matter and neither is required.
func costOptionsFrom(spec string) (costs.Options, error) {
	since, by := 7*24*time.Hour, costs.ByModel
	fields := strings.Fields(spec)
	for i := 0; i < len(fields); i++ {
		f := strings.TrimPrefix(strings.TrimPrefix(fields[i], "--"), "-")
		switch f {
		case "by":
			if i+1 >= len(fields) {
				return costs.Options{}, errors.New("by what? model, conversation or day")
			}
			i++
			by = fields[i]
		case "since":
			if i+1 >= len(fields) {
				return costs.Options{}, errors.New("since when? 7d, 24h, 90m or all")
			}
			i++
			d, err := costs.ParseSince(fields[i])
			if err != nil {
				return costs.Options{}, err
			}
			since = d
		case costs.ByModel, costs.ByConversation, costs.ByDay:
			by = f
		default:
			d, err := costs.ParseSince(f)
			if err != nil {
				return costs.Options{}, fmt.Errorf("%q is not a window or a grouping — try `/costs 24h by day`", fields[i])
			}
			since = d
		}
	}
	return costOptions(since, by), nil
}
