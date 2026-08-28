package price

import (
	"math"
	"testing"
)

func TestForMatchesTheLongestFamily(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  float64 // input rate, which is enough to say which row won
		ok    bool
	}{
		{"claude-opus-5", 5, true},
		{"claude-sonnet-5", 3, true},
		{"CLAUDE-HAIKU-4-5-20251001", 1, true},
		{"gpt-5.6-luna", 0.20, true},
		{"gpt-5-mini", 0.25, true},
		{"gpt-5-nano", 0.05, true},
		{"gpt-5", 1.25, true},
		{"gpt-4o-mini", 0.15, true},
		{"llama-3-70b", 0, false},
		{"", 0, false},
	} {
		p, ok := For(tc.model)
		if ok != tc.ok || p.Input != tc.want {
			t.Errorf("For(%q) = %v %v, want %v %v", tc.model, p.Input, ok, tc.want, tc.ok)
		}
	}
}

func TestCostChargesCacheTokensSeparatelyWhenItCan(t *testing.T) {
	// Anthropic rows know their cache rates: a million cache reads on
	// opus is $0.50, not $5.
	usd, ok := Of("claude-opus-5", Tokens{CacheRead: 1_000_000})
	if !ok || math.Abs(usd-0.5) > 1e-9 {
		t.Fatalf("cache read: %v %v", usd, ok)
	}
	// A row with no cache rates charges them at the input rate rather
	// than inventing a discount.
	usd, ok = Of("gpt-5", Tokens{CacheRead: 1_000_000})
	if !ok || math.Abs(usd-1.25) > 1e-9 {
		t.Fatalf("gpt cache read: %v %v", usd, ok)
	}
	// The whole turn, added up.
	usd, _ = Of("claude-opus-5", Tokens{Input: 1000, CacheWrite: 2000, CacheRead: 100_000, Output: 500})
	want := (1000*5 + 2000*6.25 + 100_000*0.5 + 500*25) / 1e6
	if math.Abs(usd-want) > 1e-9 {
		t.Fatalf("turn: got %v want %v", usd, want)
	}
}

func TestAnUnknownModelIsNotAZeroBill(t *testing.T) {
	usd, ok := Of("some-local-model", Tokens{Input: 1_000_000, Output: 1_000_000})
	if ok || usd != 0 {
		t.Fatalf("got %v %v, want 0 false", usd, ok)
	}
}

func TestEnvOverridesAndExtends(t *testing.T) {
	t.Setenv(Env, "some-local-model=1/2, opus=10/10/10/10 ,bogus,haiku=nope")
	p, ok := For("some-local-model-7b")
	if !ok || p.Input != 1 || p.Output != 2 {
		t.Fatalf("added family: %+v %v", p, ok)
	}
	if p, _ := For("claude-opus-5"); p.Input != 10 {
		t.Fatalf("override: %+v", p)
	}
	// A bad entry is named, and does not take the good ones with it.
	if p, _ := For("claude-haiku-4-5"); p.Input != 1 {
		t.Fatalf("a bad entry replaced a good default: %+v", p)
	}
	_, bad := Parse("bogus,haiku=nope,opus=1/2")
	if len(bad) != 2 {
		t.Fatalf("bad entries: %v", bad)
	}
}

func TestTheTableFollowsTheEnvironment(t *testing.T) {
	if p, _ := For("claude-opus-5"); p.Input != 5 {
		t.Fatalf("default: %+v", p)
	}
	t.Setenv(Env, "opus=1/1/1/1")
	if p, _ := For("claude-opus-5"); p.Input != 1 {
		t.Fatalf("after setting: %+v", p)
	}
	t.Setenv(Env, "")
	if p, _ := For("claude-opus-5"); p.Input != 5 {
		t.Fatalf("after clearing: %+v", p)
	}
}

func TestUSDKeepsSmallNumbersVisible(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{{0, "$0.0000"}, {0.0038, "$0.0038"}, {0.5, "$0.5000"}, {1, "$1.00"}, {12.345, "$12.35"}} {
		if got := USD(tc.in); got != tc.want {
			t.Errorf("USD(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
