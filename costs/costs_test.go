package costs

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/incantery/vera/journal"
)

// The fixture is small enough that every number in the assertions was
// worked out by hand from the price table, which is the point: a
// change to that table should fail here rather than quietly move
// somebody's report.
func fixture(t *testing.T, now time.Time) string {
	t.Helper()
	dir := t.TempDir()
	w := &journal.Writer{Dir: dir}
	write := func(e journal.Entry) {
		if err := w.Write(e); err != nil {
			t.Fatal(err)
		}
	}
	// Two exchanges on opus, one of them delegating.
	write(journal.Entry{
		At: now.Add(-2 * time.Hour), Conversation: "c1", Model: "claude-opus-5", Effort: "high",
		InputTokens: 12000, CacheReadTokens: 9000, CacheWriteTokens: 1000, OutputTokens: 800,
		FirstSignMs: 400,
		Rounds: []journal.Round{
			{Tool: "delegate", Session: "sess-1", CostUSD: 0.25},
			{Tool: "read"},
		},
	})
	write(journal.Entry{
		At: now.Add(-3 * time.Hour), Conversation: "c1", Model: "claude-opus-5", Effort: "high",
		InputTokens: 4000, OutputTokens: 200, FirstSignMs: 1200,
	})
	// One on the cheap model, in another conversation.
	write(journal.Entry{
		At: now.Add(-time.Hour), Conversation: "c2", Model: "gpt-5.6-luna", Effort: "none",
		InputTokens: 1000, OutputTokens: 100, FirstSignMs: 800,
	})
	// And one from last month, which a 7d window must not count.
	write(journal.Entry{
		At: now.Add(-30 * 24 * time.Hour), Conversation: "c2", Model: "claude-opus-5",
		InputTokens: 1_000_000, OutputTokens: 1_000_000, FirstSignMs: 50,
	})
	return dir
}

func about(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s: got %.9f, want %.9f", what, got, want)
	}
}

func TestByModelAddsUpTheWayTheTableSays(t *testing.T) {
	now := time.Now()
	rep, err := Build(Options{Dir: fixture(t, now), Since: 7 * 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Groups) != 2 {
		t.Fatalf("wanted a row per model, got %d: %+v", len(rep.Groups), rep.Groups)
	}
	// Most expensive first, and the effort is part of the row's name.
	opus := rep.Groups[0]
	if opus.Key != "claude-opus-5 · high" {
		t.Fatalf("first row is %q", opus.Key)
	}
	if opus.Exchanges != 2 {
		t.Errorf("exchanges: %d", opus.Exchanges)
	}
	// Input is the prompt MINUS what came from or went into the cache:
	// (12000-9000-1000) + 4000 = 6000.
	if opus.Tokens.Input != 6000 || opus.Tokens.CacheRead != 9000 || opus.Tokens.CacheWrite != 1000 || opus.Tokens.Output != 1000 {
		t.Errorf("tokens: %+v", opus.Tokens)
	}
	// opus is 5 / 6.25 / 0.5 / 25 per million:
	//   2000*5 + 1000*6.25 + 9000*0.5 + 800*25 = 40750  → $0.040750
	//   4000*5 +                        200*25 = 25000  → $0.025000
	about(t, "opus", opus.USD, 0.06575)
	if !opus.Priced {
		t.Error("opus should be priced")
	}
	// Two samples: the median is the faster one, the p90 the slower.
	if opus.MedianSignMs != 400 || opus.P90SignMs != 1200 {
		t.Errorf("first sign: p50 %d, p90 %d", opus.MedianSignMs, opus.P90SignMs)
	}
	if opus.ToolRounds != 2 || opus.PerExchange() != 1 {
		t.Errorf("tool rounds: %d (%.1f per exchange)", opus.ToolRounds, opus.PerExchange())
	}
	// The delegation reported its own cost; that is the fleet column,
	// and it is deliberately NOT in opus.USD.
	about(t, "delegated", opus.Fleet.USD, 0.25)

	luna := rep.Groups[1]
	// gpt-5.6-luna is 0.20 / 1.20: 1000*0.2 + 100*1.2 = 320 → $0.00032
	about(t, "luna", luna.USD, 0.00032)

	about(t, "total", rep.Total.USD, 0.06607)
	if rep.Total.Exchanges != 3 {
		t.Errorf("the month-old exchange should be outside a 7d window: %d exchanges", rep.Total.Exchanges)
	}

	text := rep.Text()
	for _, want := range []string{"claude-opus-5 · high", "$0.0658", "gpt-5.6-luna", "400ms / 1.2s", "$0.2500"} {
		if !strings.Contains(text, want) {
			t.Errorf("the table is missing %q:\n%s", want, text)
		}
	}
}

func TestByConversationAndByDay(t *testing.T) {
	now := time.Now()
	dir := fixture(t, now)

	byConv, err := Build(Options{Dir: dir, Since: 7 * 24 * time.Hour, By: ByConversation, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(byConv.Groups) != 2 || byConv.Groups[0].Key != "c1" || byConv.Groups[0].Exchanges != 2 {
		t.Fatalf("by conversation: %+v", byConv.Groups)
	}

	byDay, err := Build(Options{Dir: dir, Since: 7 * 24 * time.Hour, By: ByDay, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range byDay.Groups {
		if _, err := time.Parse("2006-01-02", g.Key); err != nil {
			t.Fatalf("a day row should be a date: %q", g.Key)
		}
	}

	if _, err := Build(Options{Dir: dir, By: "phase-of-the-moon"}); err == nil {
		t.Fatal("an unknown grouping should be refused")
	}
}

// A model nobody has a price for gets its tokens counted and no
// dollars invented, and the report says which model it was.
func TestAnUnpricedModelIsSaidOutLoud(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	w := &journal.Writer{Dir: dir}
	if err := w.Write(journal.Entry{
		At: now, Conversation: "c1", Model: "somebodys-own-model",
		InputTokens: 500, OutputTokens: 50, FirstSignMs: 100,
	}); err != nil {
		t.Fatal(err)
	}
	rep, err := Build(Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	g := rep.Groups[0]
	if g.Priced || g.USD != 0 {
		t.Errorf("an unknown model must not be given a price: priced=%v $%v", g.Priced, g.USD)
	}
	if g.Tokens.Input != 500 || g.Tokens.Output != 50 {
		t.Errorf("its tokens should still be counted: %+v", g.Tokens)
	}
	text := rep.Text()
	if !strings.Contains(text, "no price is known for somebodys-own-model") {
		t.Errorf("the report should say so:\n%s", text)
	}
	if !strings.Contains(text, "—") {
		t.Errorf("an unpriced row shows a dash, not $0.00:\n%s", text)
	}
}

func TestSinceIsReadTheWayPeopleTypeIt(t *testing.T) {
	for spec, want := range map[string]time.Duration{
		"7d":  7 * 24 * time.Hour,
		"2h":  2 * time.Hour,
		"90m": 90 * time.Minute,
		"1w":  7 * 24 * time.Hour,
		"all": 0,
		"":    0,
	} {
		got, err := ParseSince(spec)
		if err != nil {
			t.Fatalf("%q: %v", spec, err)
		}
		if got != want {
			t.Errorf("%q: got %s, want %s", spec, got, want)
		}
	}
	if _, err := ParseSince("last tuesday"); err == nil {
		t.Error("nonsense should be an error, not zero")
	}
}

// An empty journal is a report with nothing in it, not a failure.
func TestNothingJournaledIsNotAnError(t *testing.T) {
	rep, err := Build(Options{Dir: t.TempDir(), Since: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Groups) != 0 || !strings.Contains(rep.Text(), "no exchanges") {
		t.Errorf("empty: %+v\n%s", rep.Groups, rep.Text())
	}
}
