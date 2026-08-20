// `vera chat` — the terminal face of the standing conversation, built
// to live inside rook's companion popup: renders the thread's tail,
// then a plain REPL. It holds no state; the daemon owns the thread,
// so dismissing the popup mid-thought costs nothing. ROOK_SESSION and
// ROOK_DIR (injected by rook at summon time) ride along with every
// message so vera answers from where the owner is standing.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	cliAccent = "\x1b[33;1m"
	cliDim    = "\x1b[90m"
	cliOff    = "\x1b[0m"
)

func chatMain(args []string) {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	addr := fs.String("addr", "localhost:4770", "the vera daemon's address")
	fs.Parse(args)

	base := "http://" + strings.TrimPrefix(*addr, "http://")
	key := ""
	if b, err := os.ReadFile(statePath("vera-key")); err == nil {
		key = strings.TrimSpace(string(b))
	}
	client := &http.Client{Timeout: 180 * time.Second}
	get := func(path string) (*http.Response, error) {
		u := base + path
		if key != "" {
			u += "?key=" + url.QueryEscape(key)
		}
		return client.Get(u)
	}

	fmt.Printf("\n  %s♜ vera%s", cliAccent, cliOff)
	if s := os.Getenv("ROOK_SESSION"); s != "" {
		fmt.Printf("%s · with you in %s%s", cliDim, s, cliOff)
	}
	fmt.Printf("%s · esc closes%s\n\n", cliDim, cliOff)

	if resp, err := get("/api/chat"); err == nil {
		var turns []chatTurn
		json.NewDecoder(resp.Body).Decode(&turns)
		resp.Body.Close()
		if len(turns) > 12 {
			turns = turns[len(turns)-12:]
		}
		for _, t := range turns {
			printTurn(t.Role, t.Text)
		}
	} else {
		fmt.Printf("  %svera is not answering at %s%s\n\n", cliDim, *addr, cliOff)
	}

	read := lineReader()
	for {
		fmt.Printf("%syou ▸%s ", cliAccent, cliOff)
		text, quit := read()
		if quit {
			fmt.Println()
			return
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if text == "q" || text == "exit" {
			return
		}
		body, _ := json.Marshal(map[string]string{
			"text":    text,
			"session": os.Getenv("ROOK_SESSION"),
			"dir":     os.Getenv("ROOK_DIR"),
		})
		u := base + "/api/chat"
		if key != "" {
			u += "?key=" + url.QueryEscape(key)
		}
		resp, err := client.Post(u, "application/json", bytes.NewReader(body))
		if err != nil {
			fmt.Printf("  %s%v%s\n", cliDim, err, cliOff)
			continue
		}
		var rep struct {
			Reply   string   `json:"reply"`
			Applied []string `json:"applied"`
			Error   string   `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&rep)
		resp.Body.Close()
		if rep.Error != "" {
			fmt.Printf("  %s%s%s\n", cliDim, rep.Error, cliOff)
			continue
		}
		printTurn("vera", rep.Reply)
		for _, id := range rep.Applied {
			fmt.Printf("  %s→ sent to %s%s\n", cliAccent, id, cliOff)
		}
	}
}

// lineReader picks the input discipline: on a real terminal, a raw
// reader where lone Esc closes instantly (the popup convention rook
// users live in — fzf taught the muscle memory); on a pipe, a plain
// scanner so scripts and tests keep working.
func lineReader() func() (string, bool) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		sc := bufio.NewScanner(os.Stdin)
		return func() (string, bool) {
			if !sc.Scan() {
				return "", true
			}
			return sc.Text(), false
		}
	}
	return readLineRaw
}

// stdinFeed is the one reader of stdin's bytes, started on first use:
// a blocking tty read cannot carry a deadline (SetReadDeadline is not
// supported there), so lone-Esc detection selects on this channel with
// a timer instead.
var stdinFeed chan byte

func stdinBytes() chan byte {
	if stdinFeed == nil {
		stdinFeed = make(chan byte, 64)
		go func() {
			buf := make([]byte, 1)
			for {
				n, err := os.Stdin.Read(buf)
				if n > 0 {
					stdinFeed <- buf[0]
				}
				if err != nil {
					close(stdinFeed)
					return
				}
			}
		}()
	}
	return stdinFeed
}

// readLineRaw is one line in raw mode: printable bytes echo, backspace
// erases, ctrl-u clears, enter submits; Esc alone, ctrl-c or ctrl-d
// quits. An escape SEQUENCE (arrow keys) is told from a lone Esc by a
// short wait, then swallowed.
func readLineRaw() (string, bool) {
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		sc := bufio.NewScanner(os.Stdin)
		if !sc.Scan() {
			return "", true
		}
		return sc.Text(), false
	}
	defer term.Restore(int(os.Stdin.Fd()), old)

	ch := stdinBytes()
	next := func(wait time.Duration) (byte, bool) {
		if wait == 0 {
			b, ok := <-ch
			return b, ok
		}
		select {
		case b, ok := <-ch:
			return b, ok
		case <-time.After(wait):
			return 0, false
		}
	}

	var line []byte
	for {
		b, ok := next(0)
		if !ok {
			return "", true
		}
		switch {
		case b == 0x1b: // Esc — or the start of a key's escape sequence
			seq, more := next(60 * time.Millisecond)
			if !more {
				fmt.Print("\r\n")
				return "", true // a lone Esc: the owner is done
			}
			// Swallow the rest of the sequence (CSI ends on 0x40-0x7e).
			if seq == '[' || seq == 'O' {
				for {
					b, more := next(60 * time.Millisecond)
					if !more || (b >= 0x40 && b <= 0x7e) {
						break
					}
				}
			}
		case b == 0x03 || b == 0x04: // ctrl-c / ctrl-d
			fmt.Print("\r\n")
			return "", true
		case b == '\r' || b == '\n':
			fmt.Print("\r\n")
			return string(line), false
		case b == 0x7f || b == 0x08: // backspace
			if len(line) > 0 {
				// Trim one rune; erase one cell (multibyte width is
				// approximated at one, good enough for a popup).
				_, size := utf8.DecodeLastRune(line)
				line = line[:len(line)-size]
				fmt.Print("\b \b")
			}
		case b == 0x15: // ctrl-u — clear the line
			for range utf8.RuneCount(line) {
				fmt.Print("\b \b")
			}
			line = line[:0]
		case b >= 0x20:
			line = append(line, b)
			os.Stdout.Write([]byte{b})
		}
	}
}

func printTurn(role, text string) {
	label, tint := "vera ▸", ""
	if role == "owner" {
		label, tint = "you ▸ ", cliDim
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	fmt.Printf("%s%s%s %s%s%s\n", cliDim, label, cliOff, tint, lines[0], cliOff)
	for _, l := range lines[1:] {
		fmt.Printf("       %s%s%s\n", tint, l, cliOff)
	}
	fmt.Println()
}
