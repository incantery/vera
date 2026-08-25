package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// `vera say`: one exchange, no screen. For scripts, for other agents,
// for driving Vera from a pane while building her: the reply streams
// to stdout, status lines go to stderr, and the conversation id is
// stable so the next call continues it.
const sayUsage = `vera say [-c conversation] <text>   (or text on stdin)

  -c   conversation id (default "cli"; pass a new one to start over)
`

func runSay(args []string) error {
	fs := flag.NewFlagSet("say", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, sayUsage) }
	conv := fs.String("c", "cli", "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	text := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if text == "" {
		b, _ := readAll(os.Stdin)
		text = strings.TrimSpace(string(b))
	}
	if text == "" {
		return errors.New("nothing to say")
	}
	base, err := ensure()
	if err != nil {
		return err
	}
	id, err := loadIdentity(identityPath())
	if err != nil {
		return fmt.Errorf("no identity yet — run verad once: %w", err)
	}
	c := &chatClient{base: base, secret: id.Secret, device: id.Name + " (cli)"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	wrote := false
	err = c.say(ctx, text, *conv, func(f Frame) {
		switch {
		case f.Delta != "":
			fmt.Print(f.Delta)
			wrote = true
		case f.Status != "":
			fmt.Fprintln(os.Stderr, "· "+f.Status)
		case f.Error != "":
			fmt.Fprintln(os.Stderr, "✗ "+f.Error)
		}
	})
	if wrote {
		fmt.Println()
	}
	return err
}

func readAll(f *os.File) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			return out, nil
		}
	}
}
