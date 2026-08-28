// Package costs answers "what have these models actually cost me".
//
// The journal already holds the only honest answer: one line per
// exchange, with the model, the effort, the tokens, how long until
// anything reached the screen, and every tool round it made. Nothing
// here measures anything — it reads that file and adds it up.
//
// Three things it deliberately does NOT do. It does not invent a price
// for a model the table does not know: those exchanges come back with
// tokens and no dollars, and the report says which models they were. It
// does not average latency, because the average of a first-sign time is
// a number nobody has ever waited — the median and the p90 are what a
// person recognises. And it does not put what a delegated agent spent
// into the exchange's own token count: Claude Code bills its own way,
// and folding the two together would make delegation look free.
//
// The dollars are API list prices from price/, which a subscription
// does not pay. The honest use of them is comparative: which model,
// which conversation, which day ran away.
package costs

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/incantery/vera/dump"
	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/journal"
	"github.com/incantery/vera/price"
)

// How the rows are grouped.
const (
	ByModel        = "model"
	ByConversation = "conversation"
	ByDay          = "day"
)

// Options is one question about the journal.
type Options struct {
	// Dir is the journal — one .jsonl per conversation.
	Dir string
	// Since is how far back to look; zero means everything.
	Since time.Duration
	// By is model, conversation or day. Empty means model.
	By string
	// Now is the clock, for a test that wants a fixed one.
	Now time.Time

	// FleetDir is where the fleet keeps its tasks, and ClaudeDir is
	// where Claude Code keeps its sessions. Both empty means the
	// report is Vera's own spend and says nothing about the agents she
	// started — which is the right answer for a machine that has
	// neither, and a lie on one that does, so `vera costs` fills them.
	FleetDir  string
	ClaudeDir string
}

// Group is one row: every exchange that shared a key.
type Group struct {
	Key       string
	Exchanges int
	Tokens    price.Tokens
	USD       float64
	// Priced is false when any exchange in the row ran on a model with
	// no price. USD is then a floor rather than a total.
	Priced   bool
	Unpriced []string // the models nobody had a price for

	// First sign is what the person actually waited: the moment
	// anything at all reached the screen, which for a delegating
	// exchange is a status line long before the first word.
	MedianSignMs int64
	P90SignMs    int64

	// ToolRounds is every tool call these exchanges made; per exchange
	// is the number worth reading.
	ToolRounds int

	// Fleet is what the agents these exchanges started spent, at the
	// same list prices. It is separate from USD on purpose.
	Fleet dump.Spend

	signs []int64
}

// PerExchange is tool rounds per exchange, the way the table shows it.
func (g Group) PerExchange() float64 {
	if g.Exchanges == 0 {
		return 0
	}
	return float64(g.ToolRounds) / float64(g.Exchanges)
}

// Report is the whole answer.
type Report struct {
	// From is the oldest exchange counted; Since is what was asked for.
	From   time.Time
	Since  time.Duration
	By     string
	Groups []Group
	Total  Group
	// Files is how many conversation files were read.
	Files int
}

// Build reads the journal and adds it up.
func Build(o Options) (Report, error) {
	if o.By == "" {
		o.By = ByModel
	}
	switch o.By {
	case ByModel, ByConversation, ByDay:
	default:
		return Report{}, fmt.Errorf("--by is model, conversation or day, not %q", o.By)
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	var cutoff time.Time
	if o.Since > 0 {
		cutoff = now.Add(-o.Since)
	}

	files, err := journal.List(o.Dir)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Since: o.Since, By: o.By}
	rep.Total.Priced = true
	groups := map[string]*Group{}
	spend := newSpender(o.FleetDir, o.ClaudeDir)

	for _, f := range files {
		entries, err := journal.Read(f.Path)
		if err != nil {
			continue
		}
		counted := false
		for _, e := range entries {
			if !cutoff.IsZero() && e.At.Before(cutoff) {
				continue
			}
			counted = true
			if rep.From.IsZero() || e.At.Before(rep.From) {
				rep.From = e.At
			}
			key := keyOf(e, o.By, f.Conversation)
			g := groups[key]
			if g == nil {
				g = &Group{Key: key, Priced: true}
				groups[key] = g
			}
			// Once per exchange, not once per row it lands in: the
			// spender counts a task's sessions the first time it is
			// asked and zero every time after, so asking it twice
			// would put the money in the row and not in the total.
			agents := spend.forEntry(e)
			fold(g, e, agents)
			fold(&rep.Total, e, agents)
		}
		if counted {
			rep.Files++
		}
	}

	rep.Groups = make([]Group, 0, len(groups))
	for _, g := range groups {
		g.settle()
		rep.Groups = append(rep.Groups, *g)
	}
	rep.Total.settle()
	sortGroups(rep.Groups, o.By)
	return rep, nil
}

func keyOf(e journal.Entry, by, conversation string) string {
	switch by {
	case ByConversation:
		if e.Conversation != "" {
			return e.Conversation
		}
		return conversation
	case ByDay:
		return e.At.Local().Format("2006-01-02")
	}
	// By model, and the effort with it: the same model at effort none
	// and effort high is two different bills and two different waits,
	// and a row that merged them would hide exactly what is being
	// compared.
	if e.Effort != "" {
		return e.Model + " · " + e.Effort
	}
	return e.Model
}

// fold adds one exchange to a row.
func fold(g *Group, e journal.Entry, agents dump.Spend) {
	g.Exchanges++
	t := tokensOf(e)
	g.Tokens.Add(t)
	usd, priced := price.Of(e.Model, t)
	g.USD += usd
	if !priced {
		g.Priced = false
		if !contains(g.Unpriced, e.Model) {
			g.Unpriced = append(g.Unpriced, e.Model)
		}
	}
	if e.FirstSignMs > 0 {
		g.signs = append(g.signs, e.FirstSignMs)
	}
	g.ToolRounds += len(e.Rounds)
	if g.Fleet.Sessions == 0 && g.Fleet.USD == 0 {
		g.Fleet.Priced = true
	}
	g.Fleet.USD += agents.USD
	g.Fleet.Sessions += agents.Sessions
	if !agents.Priced {
		g.Fleet.Priced = false
	}
}

// tokensOf puts the journal's counts back into the three that do not
// overlap: InputTokens is the WHOLE prompt, cache included, so the
// uncached part is what is left after taking the two cache counts out.
func tokensOf(e journal.Entry) price.Tokens {
	return price.Tokens{
		Input:      int64(max(e.InputTokens-e.CacheReadTokens-e.CacheWriteTokens, 0)),
		CacheWrite: int64(e.CacheWriteTokens),
		CacheRead:  int64(e.CacheReadTokens),
		Output:     int64(e.OutputTokens),
	}
}

func (g *Group) settle() {
	sort.Slice(g.signs, func(i, j int) bool { return g.signs[i] < g.signs[j] })
	g.MedianSignMs = quantile(g.signs, 0.5)
	g.P90SignMs = quantile(g.signs, 0.9)
	sort.Strings(g.Unpriced)
}

// quantile is the nearest-rank one: for eleven samples the p90 is a
// sample somebody actually waited, not an interpolation between two.
func quantile(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(q*float64(len(sorted))+0.5) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func sortGroups(g []Group, by string) {
	switch by {
	case ByDay:
		sort.Slice(g, func(i, j int) bool { return g[i].Key > g[j].Key }) // newest first
	default:
		// Most expensive first, and where nothing is priced, busiest.
		sort.Slice(g, func(i, j int) bool {
			if g[i].USD != g[j].USD {
				return g[i].USD > g[j].USD
			}
			if g[i].Exchanges != g[j].Exchanges {
				return g[i].Exchanges > g[j].Exchanges
			}
			return g[i].Key < g[j].Key
		})
	}
}

func contains(all []string, s string) bool {
	for _, v := range all {
		if v == s {
			return true
		}
	}
	return false
}

// --- what the agents behind the rounds spent -----------------------------

// spender turns a journal round into dollars, once per thing. A
// conversation asks after the same task twenty times and the task's
// sessions must not be counted twenty times, so both tasks and
// sessions are remembered as they are seen.
type spender struct {
	store     *fleet.Store
	claudeDir string
	tasks     map[string]dump.Spend
	sessions  map[string]dump.Spend
}

func newSpender(fleetDir, claudeDir string) *spender {
	s := &spender{claudeDir: claudeDir, tasks: map[string]dump.Spend{}, sessions: map[string]dump.Spend{}}
	if fleetDir != "" {
		s.store = fleet.NewStore(fleetDir)
	}
	return s
}

// forEntry is what the agents one exchange started spent, counted once
// each however many rows the exchange is folded into.
func (s *spender) forEntry(e journal.Entry) dump.Spend {
	out := dump.Spend{Priced: true}
	for _, r := range e.Rounds {
		got := s.forRound(r)
		out.USD += got.USD
		out.Sessions += got.Sessions
		if !got.Priced {
			out.Priced = false
		}
	}
	return out
}

// forRound is what this round's agent spent, counted once. A round
// that reports its own cost — a delegation does — is believed: it is
// the harness's own number and needs no file read at all.
func (s *spender) forRound(r journal.Round) dump.Spend {
	if r.CostUSD > 0 {
		if r.Session != "" {
			if _, seen := s.sessions[r.Session]; seen {
				return dump.Spend{Priced: true}
			}
			s.sessions[r.Session] = dump.Spend{USD: r.CostUSD, Priced: true, Sessions: 1}
		}
		return dump.Spend{USD: r.CostUSD, Priced: true, Sessions: 1}
	}
	if s.claudeDir == "" {
		return dump.Spend{Priced: true}
	}
	if r.Session != "" {
		if _, seen := s.sessions[r.Session]; seen {
			return dump.Spend{Priced: true}
		}
		got := dump.SessionSpend(s.claudeDir, r.Session)
		s.sessions[r.Session] = got
		return got
	}
	if r.Task != "" && s.store != nil {
		if _, seen := s.tasks[r.Task]; seen {
			return dump.Spend{Priced: true}
		}
		got := dump.Spend{Priced: true}
		if t, err := s.store.Load(r.Task); err == nil {
			got = dump.TaskSpend(s.claudeDir, t.Worktree, t.ID, t.Spawned.Add(-time.Minute))
		}
		s.tasks[r.Task] = got
		return got
	}
	return dump.Spend{Priced: true}
}

// --- reading it ----------------------------------------------------------

// Text is the table, for a terminal.
func (r Report) Text() string {
	var b strings.Builder
	b.WriteString(r.headline() + "\n\n")
	if len(r.Groups) == 0 {
		return b.String() + "no exchanges in that window\n"
	}
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	head := map[string]string{ByModel: "model", ByConversation: "conversation", ByDay: "day"}[r.By]
	fmt.Fprintln(w, head+"\texch\tinput\tcached\tout\t$\tsign p50/p90\ttools/exch\tfleet $")
	for _, g := range r.Groups {
		fmt.Fprintln(w, g.row())
	}
	if len(r.Groups) > 1 {
		total := r.Total
		total.Key = "total"
		fmt.Fprintln(w, total.row())
	}
	w.Flush()
	if note := r.unpricedNote(); note != "" {
		b.WriteString("\n" + note + "\n")
	}
	return b.String()
}

// Markdown is the same table for a screen that renders markdown. It is
// fenced rather than a markdown table on purpose: the columns are
// numbers and they should stay lined up.
func (r Report) Markdown() string {
	return "```\n" + strings.TrimRight(r.Text(), "\n") + "\n```"
}

func (g Group) row() string {
	// A dash rather than $0.0000: nobody spent nothing, so a zero here
	// means an exchange that never reached the model, or a model with
	// no price. Either way it is "not known", not "free".
	dollars := "—"
	switch {
	case g.USD > 0 && g.Priced:
		dollars = price.USD(g.USD)
	case g.USD > 0:
		dollars = "≥" + price.USD(g.USD)
	}
	fleet := "—"
	if g.Fleet.Sessions > 0 {
		fleet = price.USD(g.Fleet.USD)
		if !g.Fleet.Priced {
			fleet = "≥" + fleet
		}
	}
	return fmt.Sprintf("%s\t%d\t%s\t%s\t%s\t%s\t%s\t%.1f\t%s",
		g.Key, g.Exchanges,
		count(g.Tokens.Input), count(g.Tokens.CacheRead+g.Tokens.CacheWrite), count(g.Tokens.Output),
		dollars, millis(g.MedianSignMs)+" / "+millis(g.P90SignMs), g.PerExchange(), fleet)
}

func (r Report) headline() string {
	window := "all time"
	if r.Since > 0 {
		window = "the last " + Duration(r.Since)
	}
	line := fmt.Sprintf("%s · %s · by %s", window, quantity(r.Total.Exchanges, "exchange", "exchanges"), r.By)
	if !r.From.IsZero() {
		line += ", oldest " + r.From.Local().Format("2006-01-02 15:04")
	}
	return line
}

func (r Report) unpricedNote() string {
	var all []string
	for _, g := range r.Groups {
		for _, m := range g.Unpriced {
			if !contains(all, m) {
				all = append(all, m)
			}
		}
	}
	if len(all) == 0 {
		return ""
	}
	sort.Strings(all)
	return "no price is known for " + strings.Join(all, ", ") +
		" — their tokens are counted and their dollars are not (add one with $" + price.Env + ")"
}

func millis(ms int64) string {
	if ms <= 0 {
		return "—"
	}
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + "ms"
	}
	return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) + "s"
}

func count(n int64) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	case n >= 1_000:
		return strconv.FormatFloat(float64(n)/1e3, 'f', 1, 64) + "k"
	}
	return strconv.FormatInt(n, 10)
}

func quantity(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Duration is a window the way it was typed: 7d, 2h, 90m.
func Duration(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	case d%time.Hour == 0:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	case d%time.Minute == 0:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	}
	return d.String()
}

// ParseSince reads "7d", "2h", "90m", "30s". Go's own parser has no
// day, and a day is the unit people ask in.
func ParseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "all" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") || strings.HasSuffix(s, "w") {
		unit := 24 * time.Hour
		if strings.HasSuffix(s, "w") {
			unit = 7 * 24 * time.Hour
		}
		n, err := strconv.ParseFloat(strings.TrimRight(s, "dw"), 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("--since %q: a number and d, h, m or s", s)
		}
		return time.Duration(n * float64(unit)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("--since %q: a number and d, h, m or s", s)
	}
	return d, nil
}
