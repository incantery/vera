package fleet

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/incantery/vera/mux"
)

// Projects is what repositories the person has open — read from the
// panes in their multiplexer, resolved to repository roots. It is how
// the mind learns that "the rook repo" is a place with a path, rather
// than defaulting every task to whatever pane happens to have focus.
type Projects struct {
	Mux mux.Mux
	// Every bounds how often the mux is asked; between asks the last
	// answer stands.
	Every time.Duration

	mu   sync.Mutex
	at   time.Time
	last []Repo
}

// Known lists the repositories with a pane open, by name, main
// checkouts only (a worktree resolves to its repo). Empty when the
// mux is away.
func (p *Projects) Known(ctx context.Context) []Repo {
	p.mu.Lock()
	defer p.mu.Unlock()
	every := p.Every
	if every == 0 {
		every = 10 * time.Second
	}
	if time.Since(p.at) < every {
		return p.last
	}
	p.at = time.Now()
	panes, err := p.Mux.List(ctx)
	if err != nil {
		return p.last
	}
	seen := map[string]Repo{}
	for _, pane := range panes {
		if pane.Path == "" {
			continue
		}
		r, err := FindRepo(pane.Path)
		if err != nil {
			continue
		}
		seen[r.Root] = r
	}
	out := make([]Repo, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	p.last = out
	return out
}
