package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/mux"
)

// Projects is what repositories Vera knows about, by name: the ones
// with a pane open right now, the ones a task has ever run in, and
// the ones remembered from before. It is how "the rook repo" is a
// place with a path wherever the person happens to be standing and
// whatever happens to be open — Vera is not a program you run from
// inside a checkout.
type Projects struct {
	Mux mux.Mux
	// File persists what was learned; "" keeps it in memory only.
	File string
	// Every bounds how often the mux is asked; between asks the last
	// answer stands.
	Every time.Duration

	mu         sync.Mutex
	at         time.Time
	open       []Repo
	remembered map[string]Repo // by root
	loaded     bool
}

// Known lists every repository Vera knows, by name. Open ones first
// is not a thing — a name is a name; they are sorted.
func (p *Projects) Known(ctx context.Context) []Repo {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.load()
	p.refresh(ctx)
	seen := map[string]Repo{}
	for _, r := range p.open {
		seen[r.Root] = r
	}
	for root, r := range p.remembered {
		if _, err := os.Stat(root); err == nil {
			seen[root] = r
		}
	}
	out := make([]Repo, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Remember records a repository for good.
func (p *Projects) Remember(r Repo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.load()
	if _, ok := p.remembered[r.Root]; ok {
		return
	}
	p.remembered[r.Root] = r
	p.save()
}

// Resolve turns what a person or the mind said — a path, a name, a
// path inside a checkout — into a repository. A name matches the
// known list case-insensitively; a miss says what the names are.
func (p *Projects) Resolve(ctx context.Context, ref string) (Repo, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Repo{}, errors.New("no repository named")
	}
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "~") || strings.HasPrefix(ref, ".") {
		if strings.HasPrefix(ref, "~") {
			home, _ := os.UserHomeDir()
			ref = filepath.Join(home, ref[1:])
		}
		r, err := FindRepo(ref)
		if err != nil {
			return Repo{}, err
		}
		p.Remember(r)
		return r, nil
	}
	known := p.Known(ctx)
	want := strings.ToLower(ref)
	for _, r := range known {
		if strings.ToLower(r.Name) == want {
			return r, nil
		}
	}
	// A path that is not absolute: maybe it is one after all.
	if r, err := FindRepo(ref); err == nil {
		p.Remember(r)
		return r, nil
	}
	names := make([]string, 0, len(known))
	for _, r := range known {
		names = append(names, r.Name)
	}
	if len(names) == 0 {
		return Repo{}, fmt.Errorf("no repository called %q; none are known yet — give a path", ref)
	}
	return Repo{}, fmt.Errorf("no repository called %q; known: %s", ref, strings.Join(names, ", "))
}

func (p *Projects) refresh(ctx context.Context) {
	every := p.Every
	if every == 0 {
		every = 10 * time.Second
	}
	if p.Mux == nil || time.Since(p.at) < every {
		return
	}
	p.at = time.Now()
	panes, err := p.Mux.List(ctx)
	if err != nil {
		return
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
		// Seen once is remembered: a repo with a pane open today is a
		// repo the person works in.
		if _, ok := p.remembered[r.Root]; !ok {
			p.remembered[r.Root] = r
			p.save()
		}
	}
	p.open = p.open[:0]
	for _, r := range seen {
		p.open = append(p.open, r)
	}
}

func (p *Projects) load() {
	if p.loaded {
		return
	}
	p.loaded = true
	p.remembered = map[string]Repo{}
	if p.File == "" {
		return
	}
	b, err := os.ReadFile(p.File)
	if err != nil {
		return
	}
	var list []Repo
	if json.Unmarshal(b, &list) == nil {
		for _, r := range list {
			p.remembered[r.Root] = r
		}
	}
}

func (p *Projects) save() {
	if p.File == "" {
		return
	}
	list := make([]Repo, 0, len(p.remembered))
	for _, r := range p.remembered {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Root < list[j].Root })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p.File), 0o755)
	_ = os.WriteFile(p.File, b, 0o644)
}
