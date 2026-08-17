// Evidence: reading a pile of finished work back as a verdict on the
// tier table.
//
// This started life inside the ladder, where the work was synthetic and
// the bar was a check command. It lives here because the ladder is not
// the only thing that produces evidence, and it turned out not to be
// the best one. A corpus cheap enough to write is too well-specified to
// separate the tiers on implementation: single file, contract in a
// comment, tests handed over. Real nodes are several files, an existing
// repository, a sentence instead of a spec, and no tests until someone
// writes them — which is the shape the question was actually about.
//
// So the input here is an Observation and nothing more: a kind, the
// model that ran it, whether it was good enough, and what it cost. A
// synthetic cell and a real board node both flatten to that, and both
// get graded by the same code — which is the point. Two verdict
// implementations would be free to disagree, and the one that disagreed
// would be whichever one nobody was looking at.
package route

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
)

// MinRuns is the floor below which a comparison is not reported as a
// finding. Deliberately blunt: this is not a significance test, it is a
// guard against reading a handful of coin flips as a trend.
const MinRuns = 8

// MaterialGap is how much worse a pass rate has to be before the
// difference is called rather than shrugged at, in percentage points.
// Below it, two tiers are a tie and the cheaper one wins on price.
const MaterialGap = 10

// An Observation is one finished piece of work, whatever produced it.
// Pass is the bar the producer applies — a check command in the lab, an
// owner's acceptance on the board. The second is the better bar: it is
// the judgment the first was only ever a proxy for.
type Observation struct {
	Kind  string
	Model string
	Pass  bool
	USD   float64
}

// Stats is one (kind, tier) cell's arithmetic.
type Stats struct {
	Runs, Passes int
	USD          float64
}

func (s *Stats) Rate() int {
	if s.Runs == 0 {
		return 0
	}
	return 100 * s.Passes / s.Runs
}

func (s *Stats) PerPass() string {
	if s.Passes == 0 {
		return "—"
	}
	return fmt.Sprintf("$%.2f", s.USD/float64(s.Passes))
}

// TierOfModel reverses the worker alias table. A model that is not one
// of the aliases — a pinned id, a local model, or nothing at all
// because routing was off — has no tier and sits out the verdict. It is
// still on the record; it just cannot answer this question.
func TierOfModel(model string) (Tier, bool) {
	for _, t := range Tiers {
		if WorkerAlias[t] == model {
			return t, true
		}
	}
	return "", false
}

// Group buckets observations into kind → tier → stats, dropping
// anything that cannot speak to routing: no kind, or a model with no
// tier. Neither is guessed at.
func Group(obs []Observation) map[string]map[Tier]*Stats {
	out := map[string]map[Tier]*Stats{}
	for _, o := range obs {
		if o.Kind == "" {
			continue
		}
		t, ok := TierOfModel(o.Model)
		if !ok {
			continue
		}
		if out[o.Kind] == nil {
			out[o.Kind] = map[Tier]*Stats{}
		}
		if out[o.Kind][t] == nil {
			out[o.Kind][t] = &Stats{}
		}
		c := out[o.Kind][t]
		c.Runs++
		if o.Pass {
			c.Passes++
		}
		c.USD += o.USD
	}
	return out
}

// WriteTable prints one block per kind: every tier that ran, the routed
// tier marked, and the verdict underneath.
func WriteTable(w io.Writer, obs []Observation, empty string) {
	cells := Group(obs)
	if len(cells) == 0 {
		fmt.Fprintln(w, empty)
		return
	}
	kinds := make([]string, 0, len(cells))
	for k := range cells {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	for _, kind := range kinds {
		routed := OfKind(kind)
		fmt.Fprintf(w, "\n%s — routing says %s\n", kind, routed)
		tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  tier\tmodel\truns\tpass\trate\t$/run\t$/pass\t")
		for _, t := range Tiers {
			c := cells[kind][t]
			if c == nil {
				continue
			}
			mark := "  "
			if t == routed {
				mark = "→ "
			}
			fmt.Fprintf(tw, "%s%s\t%s\t%d\t%d\t%d%%\t$%.2f\t%s\t\n",
				mark, t, WorkerAlias[t], c.Runs, c.Passes, c.Rate(),
				c.USD/float64(c.Runs), c.PerPass())
		}
		tw.Flush()
		fmt.Fprintln(w, "  "+Verdict(kind, routed, cells[kind]))
	}
}

// Verdict reads one kind's cells and says, in a sentence, whether the
// routing table earned its claim. It refuses to conclude from thin
// data, and it says which direction the table should move when it has
// something to say.
//
// Two ways the table can be wrong. Routed too cheap: the routed tier
// passes materially less often than a stronger one, so the saving is
// not real — it is being paid for in failures, and a failure costs a
// whole rerun plus the human who has to notice. Routed too rich: a
// cheaper tier matched it, which is the money the whole exercise exists
// to find.
//
// And one way the EXPERIMENT can be wrong, which matters more than
// either: not enough runs to tell. A verdict from three samples is a
// coin-flip wearing a lab coat. The point of measuring is to stop
// believing the table on somebody's say-so; replacing a guess with a
// confident-sounding number off four observations would be a worse lie
// than the guess was.
func Verdict(kind string, routed Tier, cells map[Tier]*Stats) string {
	here := cells[routed]
	if here == nil {
		return "verdict: the routed tier (" + string(routed) + ") has not been run — nothing to check."
	}
	thin := here.Runs < MinRuns
	var compared int
	for t, c := range cells {
		if t != routed && c.Runs >= MinRuns {
			compared++
		}
	}
	if thin || compared == 0 {
		return fmt.Sprintf(
			"verdict: too thin to call — %d runs at %s, %d other tier(s) with %d+ runs.",
			here.Runs, routed, compared, MinRuns)
	}
	for _, t := range Tiers {
		c := cells[t]
		if c == nil || c.Runs < MinRuns || !dearer(t, routed) {
			continue
		}
		if c.Rate()-here.Rate() >= MaterialGap {
			return fmt.Sprintf(
				"verdict: ROUTED TOO CHEAP — %s passes %d%% vs %s's %d%%. The saving is being paid for in failures; move %s up.",
				t, c.Rate(), routed, here.Rate(), kind)
		}
	}
	for _, t := range Tiers {
		c := cells[t]
		if c == nil || c.Runs < MinRuns || !dearer(routed, t) {
			continue
		}
		if here.Rate()-c.Rate() < MaterialGap {
			return fmt.Sprintf(
				"verdict: ROUTED TOO RICH — %s ties %s (%d%% vs %d%%) at %s per pass vs %s. Move %s down.",
				t, routed, c.Rate(), here.Rate(), c.PerPass(), here.PerPass(), kind)
		}
	}
	return fmt.Sprintf(
		"verdict: HOLDS — %s passes %d%% at %s per pass; nothing cheaper matches it and nothing dearer beats it by %d points.",
		routed, here.Rate(), here.PerPass(), MaterialGap)
}

// dearer answers whether a is a more expensive tier than b.
func dearer(a, b Tier) bool {
	rank := map[Tier]int{Cheap: 0, Mid: 1, Strong: 2}
	return rank[a] > rank[b]
}
