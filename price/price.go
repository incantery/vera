// Package price turns tokens into dollars.
//
// Two places in this repository already had to answer "what did that
// cost": `vera dump`, pricing whole Claude Code sessions after the
// fact, and now the chat's status line, pricing the turn you are
// watching. They must not disagree — a dump that says one number and a
// status line that says another is worse than neither — so the table
// lives here and both read it.
//
// What the numbers are NOT: a bill. They are API list prices. On a
// subscription nobody pays them, and the honest use of the figure is
// comparative: which turn was expensive, which task ran away. A model
// the table does not know gets tokens and no dollars rather than a
// guess.
//
// $VERA_PRICES adds to the table, or overrides it, without a rebuild:
//
//	VERA_PRICES="gpt-5.6-luna=0.20/1.20,opus=5/6.25/0.5/25"
//
// Each entry is family=input/output or
// family=input/cache-write/cache-read/output, in USD per million
// tokens. The family is matched against the model name as a substring,
// longest first.
package price

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Env is the variable that adds to, or overrides, the table.
const Env = "VERA_PRICES"

// Price is USD per million tokens.
//
// CacheWrite and CacheRead are zero for a family whose cache rates are
// not known separately; those tokens are then charged at Input, which
// is the conservative reading and never invents a discount.
type Price struct{ Input, CacheWrite, CacheRead, Output float64 }

func (p Price) cacheWrite() float64 {
	if p.CacheWrite == 0 {
		return p.Input
	}
	return p.CacheWrite
}

func (p Price) cacheRead() float64 {
	if p.CacheRead == 0 {
		return p.Input
	}
	return p.CacheRead
}

// Tokens is what a turn, a round or a whole session spent. Input is
// the part that was NOT served from or written to a cache: the three
// input counts do not overlap.
type Tokens struct{ Input, CacheWrite, CacheRead, Output int64 }

// Add folds another count in.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.CacheWrite += o.CacheWrite
	t.CacheRead += o.CacheRead
	t.Output += o.Output
}

// Cost is what these tokens would cost at p.
func (p Price) Cost(t Tokens) float64 {
	return (float64(t.Input)*p.Input +
		float64(t.CacheWrite)*p.cacheWrite() +
		float64(t.CacheRead)*p.cacheRead() +
		float64(t.Output)*p.Output) / 1e6
}

// defaults are API list prices by model family.
//
// The Anthropic rows are the ones `vera dump` has always used. The
// OpenAI rows are the ones the drive module already prices its own
// calls with; they say nothing about cache rates, so cached input is
// charged at the input rate. Nothing here is invented for the sake of
// having a number: a family with no row gets no dollars.
var defaults = map[string]Price{
	"opus":         {Input: 5, CacheWrite: 6.25, CacheRead: 0.5, Output: 25},
	"sonnet":       {Input: 3, CacheWrite: 3.75, CacheRead: 0.3, Output: 15},
	"haiku":        {Input: 1, CacheWrite: 1.25, CacheRead: 0.1, Output: 5},
	"gpt-5.6-luna": {Input: 0.20, Output: 1.20},
	"gpt-5-nano":   {Input: 0.05, Output: 0.40},
	"gpt-5-mini":   {Input: 0.25, Output: 2.00},
	"gpt-5":        {Input: 1.25, Output: 10.00},
	"gpt-4o-mini":  {Input: 0.15, Output: 0.60},
	"gpt-4o":       {Input: 2.50, Output: 10.00},
}

// Parse reads a $VERA_PRICES spec. Entries it cannot read come back in
// bad, named, rather than being dropped in silence — a typo in a price
// should be visible in the log, not in the bill.
func Parse(spec string) (map[string]Price, []string) {
	out := map[string]Price{}
	var bad []string
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		family, rates, ok := strings.Cut(entry, "=")
		family = strings.ToLower(strings.TrimSpace(family))
		if !ok || family == "" {
			bad = append(bad, entry)
			continue
		}
		p, err := parseRates(rates)
		if err != nil {
			bad = append(bad, entry)
			continue
		}
		out[family] = p
	}
	return out, bad
}

func parseRates(s string) (Price, error) {
	parts := strings.Split(strings.TrimSpace(s), "/")
	n := make([]float64, 0, 4)
	for _, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || v < 0 {
			return Price{}, fmt.Errorf("price %q", p)
		}
		n = append(n, v)
	}
	switch len(n) {
	case 2:
		return Price{Input: n[0], Output: n[1]}, nil
	case 4:
		return Price{Input: n[0], CacheWrite: n[1], CacheRead: n[2], Output: n[3]}, nil
	}
	return Price{}, fmt.Errorf("want in/out or in/write/read/out, got %d numbers", len(n))
}

// The table is rebuilt when the environment changes, so a test can set
// $VERA_PRICES and a long-lived daemon does not have to be restarted
// to be corrected.
var (
	mu     sync.Mutex
	cached map[string]Price
	from   string
	loaded bool
)

func table() map[string]Price {
	env := os.Getenv(Env)
	mu.Lock()
	defer mu.Unlock()
	if loaded && env == from {
		return cached
	}
	t := make(map[string]Price, len(defaults)+4)
	for k, v := range defaults {
		t[k] = v
	}
	over, _ := Parse(env)
	for k, v := range over {
		t[k] = v
	}
	cached, from, loaded = t, env, true
	return t
}

// For is the price of a model, and whether one is known. The longest
// matching family wins, so "gpt-5-mini" is not priced as "gpt-5".
func For(model string) (Price, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return Price{}, false
	}
	t := table()
	families := make([]string, 0, len(t))
	for family := range t {
		if strings.Contains(m, family) {
			families = append(families, family)
		}
	}
	if len(families) == 0 {
		return Price{}, false
	}
	sort.Slice(families, func(i, j int) bool { return len(families[i]) > len(families[j]) })
	return t[families[0]], true
}

// Of is what a model's tokens would cost, and whether it was priced at
// all. An unknown model is (0, false) — not a zero bill.
func Of(model string, t Tokens) (float64, bool) {
	p, ok := For(model)
	if !ok {
		return 0, false
	}
	return p.Cost(t), true
}

// USD is a dollar figure short enough for a status line: cents when
// there are cents to see, and four places when there are not, because
// a turn that cost $0.0038 should not read as $0.00.
func USD(usd float64) string {
	if usd >= 1 {
		return fmt.Sprintf("$%.2f", usd)
	}
	return fmt.Sprintf("$%.4f", usd)
}
