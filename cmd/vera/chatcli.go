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
	fmt.Printf("%s · q closes%s\n\n", cliDim, cliOff)

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

	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("%syou ▸%s ", cliAccent, cliOff)
		if !in.Scan() {
			fmt.Println()
			return
		}
		// A stray Esc or arrow key must not become a message.
		text := strings.Map(func(r rune) rune {
			if r < 32 {
				return -1
			}
			return r
		}, in.Text())
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
