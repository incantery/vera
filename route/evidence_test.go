package route

import (
	"bytes"
	"strings"
	"testing"
)

func TestTierOfModelReversesTheAliasTable(t *testing.T) {
	for _, want := range Tiers {
		got, ok := TierOfModel(WorkerAlias[want])
		if !ok || got != want {
			t.Fatalf("%s round-trips, got %q ok=%v", want, got, ok)
		}
	}
	// A pinned id, a local model, or nothing at all because routing was
	// off: no tier, and sits out the verdict rather than being guessed
	// into one.
	for _, m := range []string{"claude-opus-5", "llama3", ""} {
		if _, ok := TierOfModel(m); ok {
			t.Fatalf("%q has no tier", m)
		}
	}
}

func TestGroupDropsWhatCannotSpeakToRouting(t *testing.T) {
	obs := []Observation{
		{Kind: KindVerify, Model: "haiku", Pass: true, USD: 0.02},
		{Kind: "", Model: "haiku", Pass: true, USD: 0.02},          // no kind
		{Kind: KindVerify, Model: "claude-opus-5", Pass: true},     // no tier
		{Kind: KindVerify, Model: "haiku", Pass: false, USD: 0.02}, // counts
	}
	got := Group(obs)
	c := got[KindVerify][Cheap]
	if c == nil || c.Runs != 2 || c.Passes != 1 {
		t.Fatalf("only the two that can answer: %+v", c)
	}
	if len(got) != 1 {
		t.Fatalf("nothing invented for the dropped rows: %+v", got)
	}
}

// The verdict must refuse to conclude from thin data. Replacing a guess
// with a confident number off four samples is a worse lie than the
// guess was.
func TestThinDataRefusesToConclude(t *testing.T) {
	cells := map[Tier]*Stats{
		Cheap:  {Runs: 3, Passes: 3, USD: 0.03},
		Strong: {Runs: 3, Passes: 1, USD: 0.60},
	}
	if v := Verdict(KindVerify, Cheap, cells); !strings.Contains(v, "too thin") {
		t.Fatalf("three runs is not a finding: %q", v)
	}
	// Even a lopsided comparison stays unreported when the routed tier
	// is thin.
	cells[Strong] = &Stats{Runs: 40, Passes: 40, USD: 8}
	if v := Verdict(KindVerify, Cheap, cells); !strings.Contains(v, "too thin") {
		t.Fatalf("the routed tier being thin is still thin: %q", v)
	}
}

func TestVerdictCallsRoutedTooCheap(t *testing.T) {
	cells := map[Tier]*Stats{
		Cheap:  {Runs: 10, Passes: 4, USD: 0.10},
		Strong: {Runs: 10, Passes: 10, USD: 5.00},
	}
	v := Verdict(KindVerify, Cheap, cells)
	if !strings.Contains(v, "ROUTED TOO CHEAP") {
		t.Fatalf("a saving paid for in failures is not a saving: %q", v)
	}
	if !strings.Contains(v, "move "+KindVerify+" up") {
		t.Fatalf("and it says which way to move: %q", v)
	}
}

// The failure mode the whole exercise exists to find.
func TestVerdictCallsRoutedTooRich(t *testing.T) {
	cells := map[Tier]*Stats{
		Cheap:  {Runs: 10, Passes: 9, USD: 0.10},
		Strong: {Runs: 10, Passes: 9, USD: 5.00},
	}
	v := Verdict(KindImplement, Strong, cells)
	if !strings.Contains(v, "ROUTED TOO RICH") {
		t.Fatalf("a cheaper tier that ties is money left on the table: %q", v)
	}
	if !strings.Contains(v, "Move "+KindImplement+" down") {
		t.Fatalf("and it says which way to move: %q", v)
	}
}

func TestVerdictHolds(t *testing.T) {
	cells := map[Tier]*Stats{
		Cheap:  {Runs: 10, Passes: 3, USD: 0.10},
		Mid:    {Runs: 10, Passes: 9, USD: 1.00},
		Strong: {Runs: 10, Passes: 9, USD: 5.00},
	}
	if v := Verdict(KindReview, Mid, cells); !strings.Contains(v, "HOLDS") {
		t.Fatalf("nothing cheaper matches and nothing dearer beats it: %q", v)
	}
}

func TestWriteTableMarksTheRoutedTier(t *testing.T) {
	var obs []Observation
	for i := 0; i < 10; i++ {
		obs = append(obs,
			Observation{Kind: KindVerify, Model: "haiku", Pass: true, USD: 0.01},
			Observation{Kind: KindVerify, Model: "opus", Pass: true, USD: 0.50})
	}
	var buf bytes.Buffer
	WriteTable(&buf, obs, "nothing")
	out := buf.String()
	if !strings.Contains(out, "→ "+string(Cheap)) {
		t.Fatalf("the routed tier is marked:\n%s", out)
	}
	if !strings.Contains(out, "verdict:") {
		t.Fatalf("a verdict is printed:\n%s", out)
	}
}

func TestEmptyEvidenceSaysSoPlainly(t *testing.T) {
	var buf bytes.Buffer
	WriteTable(&buf, []Observation{{Kind: "", Model: "haiku"}}, "nothing to go on")
	if !strings.Contains(buf.String(), "nothing to go on") {
		t.Fatalf("an empty verdict is stated, not implied: %q", buf.String())
	}
}

// The whole reason this lives in one place: a synthetic cell and a real
// board node flatten to the same Observation and are graded by the same
// code. Two implementations would be free to disagree, and the one that
// disagreed would be whichever nobody was looking at.
func TestLabAndBoardEvidenceGradeIdentically(t *testing.T) {
	mk := func(pass bool) []Observation {
		var out []Observation
		for i := 0; i < 10; i++ {
			out = append(out, Observation{Kind: KindReview, Model: "sonnet", Pass: pass, USD: 0.1})
			out = append(out, Observation{Kind: KindReview, Model: "haiku", Pass: false, USD: 0.01})
		}
		return out
	}
	var lab, board bytes.Buffer
	WriteTable(&lab, mk(true), "x")
	WriteTable(&board, mk(true), "x")
	if lab.String() != board.String() {
		t.Fatal("the same observations must grade the same however they were produced")
	}
}
