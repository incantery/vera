package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/incantery/vera/attach"
)

// `vera say`: one exchange, no screen. For scripts, for other agents,
// for driving Vera from a pane while building her: the reply streams
// to stdout, status lines go to stderr, and the conversation id is
// stable so the next call continues it.
const sayUsage = `vera say [-c conversation] [-m model] [-e effort] [-i image] <text>   (or text on stdin)

  -c   conversation id (default "cli"; pass a new one to start over)
  -m   model for THIS exchange only (claude-opus-5, gpt-5-mini, …)
  -e   how hard to think for this exchange: none, minimal, low, medium, high, xhigh, max
  -i   a picture to send with it (PNG, JPEG, GIF or WebP); repeat for more.
       Vera cannot see it — she hands the file to whichever agent she
       gives the work to, and that agent opens it.
`

// images is -i, once per picture. A repeated flag rather than a
// comma-separated list: file names contain commas, and a screenshot's
// name usually contains a space as well.
type images []string

func (i *images) String() string     { return strings.Join(*i, ", ") }
func (i *images) Set(v string) error { *i = append(*i, v); return nil }

func runSay(args []string) error {
	fs := flag.NewFlagSet("say", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, sayUsage) }
	conv := fs.String("c", "cli", "")
	model := fs.String("m", "", "")
	effort := fs.String("e", "", "")
	var pictures images
	fs.Var(&pictures, "i", "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	text := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if text == "" && len(pictures) == 0 {
		b, _ := readAll(os.Stdin)
		text = strings.TrimSpace(string(b))
	}
	// The bytes are read here, on this side of the wire, where the
	// file is already this process's to read. A picture that is not
	// there, or is not a picture, is said now rather than after an
	// exchange has been paid for.
	var attached []attach.Image
	for _, path := range pictures {
		im, err := attach.Read(path)
		if err != nil {
			return fmt.Errorf("-i %s: %w", path, err)
		}
		attached = append(attached, im)
	}
	// A picture on its own is a whole message — "look at this".
	if text == "" && len(attached) == 0 {
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
	var spent *UsageFrame
	err = c.say(ctx, Message{Text: text, Conversation: *conv, Model: *model, Effort: *effort, Images: attached}, func(f Frame) {
		if f.Usage != nil {
			spent = f.Usage
		}
		switch {
		case f.Ask != nil:
			// Nobody is watching this. A script that answered yes on a
			// person's behalf would be the worst possible client, and
			// one that answered nothing would park the exchange until
			// it timed out — so it says no, out loud, and the person
			// sees what was wanted the next time they read the output.
			fmt.Fprintf(os.Stderr, "? %s %s — %s\n  no (nobody is here to ask; say it in the chat if you meant it)\n",
				f.Ask.Name, trim(oneLine(f.Ask.Args), 160), f.Ask.Text)
			if err := c.answer(ctx, f.Ask.ID, "no"); err != nil {
				fmt.Fprintln(os.Stderr, "✗ could not answer: "+err.Error())
			}
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
	// On stderr with the status lines, not stdout: a script piping the
	// reply somewhere should get the reply and nothing else.
	if line := spent.line(); line != "" {
		fmt.Fprintln(os.Stderr, "· "+line)
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
