package main

import "testing"

// `vera say` has no status bar to put a running total on, so the
// exchange's spend is one line at the end — on stderr, where the
// status lines go, so that a script piping the reply somewhere gets
// the reply and nothing else.
func TestSayPrintsWhatTheExchangeSpent(t *testing.T) {
	for _, tc := range []struct {
		name string
		u    *UsageFrame
		want string
	}{
		{"nothing said about it", nil, ""},
		{
			"priced",
			&UsageFrame{Model: "claude-opus-5", InputTokens: 12000, OutputTokens: 800, CacheReadTokens: 9000, CostUSD: 0.0395, Priced: true},
			"12000 in (9000 cached) · 800 out · $0.0395 at list prices",
		},
		{
			// Counted tokens, unknown dollars: say the tokens and say
			// plainly that nobody knows what they cost.
			"unpriced",
			&UsageFrame{Model: "some-local-model", InputTokens: 900, OutputTokens: 100},
			"900 in · 100 out · no price known for some-local-model",
		},
		{"no tokens and no price is nothing worth a line", &UsageFrame{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.u.line(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
