// `vera sessions`: the conversations the chat left behind.
//
// The chat writes every exchange to a file of its own under the state
// directory — verad's journal is its record of the model, this is the
// terminal's record of the screen. This verb is what you read before
// `vera chat -c <id>` picks one back up. It reads disk only, so it
// works when verad is the thing that is down.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/incantery/mote/session"
)

func runSessions(args []string) error {
	fs := flag.NewFlagSet("vera sessions", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", "", "where the conversations live")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d := *dir
	if d == "" {
		d = chatSessionDir()
	}
	list, err := session.List(d)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Printf("no conversations in %s\n", d)
		return nil
	}
	return writeSessions(os.Stdout, d, list, time.Now())
}

func writeSessions(w io.Writer, dir string, list []session.Info, now time.Time) error {
	fmt.Fprintln(w, dir)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "id\tturns\tcost\tstarted\tlast")
	for _, it := range list {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", it.ID, it.Turns, cost(it.Cost),
			it.Started.Local().Format("2006-01-02 15:04"), ago(it.Last, now))
	}
	return tw.Flush()
}

// sessionLines is the same listing as the transcript shows it, for
// /sessions — with a mark on the one being said to, which /new moves.
func sessionLines(list []session.Info, current string, now time.Time) string {
	var b strings.Builder
	for _, it := range list {
		parts := []string{fmt.Sprintf("%d %s", it.Turns, plural(it.Turns, "turn"))}
		if c := cost(it.Cost); c != "" {
			parts = append(parts, c)
		}
		if a := ago(it.Last, now); a != "" {
			parts = append(parts, a)
		}
		mark := ""
		if it.ID == current {
			mark = "  ← this one"
		}
		fmt.Fprintf(&b, "%s  %s%s\n", it.ID, strings.Join(parts, " · "), mark)
	}
	return strings.TrimRight(b.String(), "\n")
}

func cost(usd float64) string {
	if usd <= 0 {
		return ""
	}
	return fmt.Sprintf("$%.4f", usd)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// ago is how long since something happened, in the largest unit that
// still says something true.
func ago(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
