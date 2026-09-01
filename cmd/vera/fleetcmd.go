package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/incantery/vera/fleet"
)

// The fleet verbs, without a screen: the same calls the chat's slash
// commands make, for scripts and for an agent driving Vera. A brief
// arrives verbatim — no model paraphrases it on the way.
const fleetUsage = `vera tasks                                 every task and what is believed about it
vera task start [-scout] [-p repo] <brief|->   start a task (brief on stdin with -)
vera task answer <id> <text>               pass a reply to a task
vera task report [id]                      print its report (and mark it seen); no id takes the one waiting
vera task land <id> | stop <id> [-f] | resume <id> | seen <id>
`

func runFleet(verb string, args []string) error {
	base, err := ensure()
	if err != nil {
		return err
	}
	id, err := loadIdentity(identityPath())
	if err != nil {
		return err
	}
	c := &chatClient{base: base, secret: id.Secret, device: id.Name + " (cli)"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch verb {
	case "tasks":
		views, err := c.tasks(ctx)
		if err != nil {
			return err
		}
		if len(views) == 0 {
			fmt.Println("no tasks")
		}
		for _, v := range views {
			if v.Closed {
				continue
			}
			line := fmt.Sprintf("%s  %-9s %-5s %s — %s", v.ID, v.State, v.Kind, shortPath(v.Project), trim(firstSentence(v.Brief), 70))
			if v.Last != nil && v.Last.Text != "" {
				line += "\n          " + trim(v.Last.Text, 160)
			}
			fmt.Println(line)
		}
		return nil
	case "start":
		fs := flag.NewFlagSet("start", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		fs.Usage = func() { fmt.Fprint(os.Stderr, fleetUsage) }
		scout := fs.Bool("scout", false, "")
		project := fs.String("p", "", "")
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		brief := strings.TrimSpace(strings.Join(fs.Args(), " "))
		if brief == "-" {
			b, _ := readAll(os.Stdin)
			brief = strings.TrimSpace(string(b))
		}
		if brief == "" {
			return errors.New("a brief is needed")
		}
		kind := fleet.Ship
		if *scout {
			kind = fleet.Scout
		}
		if err := c.post(ctx, "/fleet", fleet.Request{Project: *project, Kind: kind, Brief: brief}); err != nil {
			return err
		}
		views, err := c.tasks(ctx)
		if err != nil {
			return err
		}
		if n := len(views); n > 0 {
			newest := views[0]
			for _, v := range views {
				if v.Spawned.After(newest.Spawned) {
					newest = v
				}
			}
			fmt.Printf("started %s (%s in %s)\n", newest.ID, newest.Kind, shortPath(newest.Project))
		}
		return nil
	case "answer":
		if len(args) < 2 {
			return errors.New("vera task answer <id> <text>")
		}
		return c.post(ctx, "/fleet/"+args[0]+"/answer", map[string]string{"text": strings.Join(args[1:], " ")})
	case "report":
		views, err := c.tasks(ctx)
		if err != nil {
			return err
		}
		// The same pick the chat makes: a prefix, or nothing at all
		// when one report is waiting. An ambiguous prefix used to take
		// whichever task came first, which is a coin toss dressed up
		// as an answer.
		want := ""
		if len(args) > 0 {
			want = args[0]
		}
		v, err := pickReport(views, want)
		if err != nil {
			return err
		}
		if v.Report == "" {
			return errors.New(v.ID + " has written no report yet")
		}
		// The report itself is stdout and nothing else, so it can be
		// piped into a file or a reader. Whose it is and what to do
		// about it go to stderr, where they are read and not saved.
		fmt.Fprintf(os.Stderr, "%s · %s · %s · %s\n", v.ID, v.Kind, v.State, shortPath(v.Project))
		fmt.Println(v.Report)
		if next := reportNext(v); next != "" {
			fmt.Fprintln(os.Stderr, plainText(next))
		}
		return c.post(ctx, "/fleet/"+v.ID+"/seen", nil)
	case "land", "resume", "seen":
		if len(args) < 1 {
			return fmt.Errorf("vera task %s <id>", verb)
		}
		return c.post(ctx, "/fleet/"+args[0]+"/"+verb, nil)
	case "stop":
		if len(args) < 1 {
			return errors.New("vera task stop <id> [-f]")
		}
		path := "/fleet/" + args[0] + "/teardown"
		if len(args) > 1 && (args[1] == "-f" || args[1] == "force") {
			path += "?force=1"
		}
		return c.post(ctx, path, nil)
	}
	return errors.New("unknown fleet verb " + verb)
}
