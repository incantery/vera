package main

import (
	"strings"
	"testing"
	"time"
)

const realOutput = `You are currently using your subscription to power your Claude Code usage

Current session: 3% used · resets Aug 19 at 12:19pm (America/New_York)
Current week (all models): 33% used · resets Aug 22 at 7:59am (America/New_York)
Current week (Fable): 53% used · resets Aug 22 at 7:59am (America/New_York)

What's contributing to your limits usage?
Approximate, based on local sessions on this machine — does not include other devices or claude.ai.

Last 24h · 289 requests · 9 sessions
  85% of your usage was at >150k context
`

func TestUsageParsesTheRealOutput(t *testing.T) {
	u, err := parseUsage(realOutput)
	if err != nil {
		t.Fatal(err)
	}
	if u.Session.Used != 0.03 {
		t.Errorf("session %v, want 0.03", u.Session.Used)
	}
	if u.Week.Used != 0.33 {
		t.Errorf("week %v, want 0.33", u.Week.Used)
	}
	// "all models" is the overall week, not a model called "all models".
	if _, wrong := u.ByModel["all models"]; wrong {
		t.Error("the overall week was filed as a model")
	}
	if got := u.ByModel["Fable"].Used; got != 0.53 {
		t.Errorf("Fable %v, want 0.53", got)
	}
	if u.Requests != 289 || u.Sessions != 9 {
		t.Errorf("last 24h read as %d requests / %d sessions", u.Requests, u.Sessions)
	}
	if !u.OnPlan {
		t.Error("a subscription went unnoticed, so the limits look inapplicable")
	}
	if u.Session.Resets.IsZero() {
		t.Error("no reset time was read")
	}
}

// The single most important behaviour here: this scrapes a
// human-readable report, and when the wording changes it must break
// loudly. A gauge quietly reading 0% is indistinguishable from a fresh
// week, and would be believed.
func TestUnreadableOutputIsAnErrorNotZero(t *testing.T) {
	for name, text := range map[string]string{
		"empty":     "",
		"reworded":  "Your plan is 33 percent consumed this week.",
		"an error":  "Error: not logged in",
		"unrelated": "Usage: claude [options]",
	} {
		if u, err := parseUsage(text); err == nil {
			t.Errorf("%s parsed instead of failing: %+v", name, u)
		}
	}
}

func TestPartialOutputStillCounts(t *testing.T) {
	// A session line alone is a real answer; the weekly ones only
	// appear on some plans.
	u, err := parseUsage("Current session: 12% used")
	if err != nil {
		t.Fatal(err)
	}
	if u.Session.Used != 0.12 {
		t.Fatalf("session %v", u.Session.Used)
	}
	if u.Week.Used != 0 {
		t.Fatalf("a week appeared from nowhere: %v", u.Week.Used)
	}
}

func TestResetTimesLandInTheFuture(t *testing.T) {
	u, err := parseUsage(realOutput)
	if err != nil {
		t.Fatal(err)
	}
	// The year is missing from the text, so it is inferred. Whatever
	// today is, a reset should never be read as long past — otherwise
	// every New Year the dashboard says everything is overdue.
	if d := time.Until(u.Week.Resets); d < -48*time.Hour {
		t.Errorf("reset resolved to %v, which is %v ago", u.Week.Resets, -d)
	}
}

func TestUsageReadsAsASentence(t *testing.T) {
	u, _ := parseUsage(realOutput)
	printed := u.String()
	for _, want := range []string{"session", "week", "Fable", "289 requests"} {
		if !strings.Contains(printed, want) {
			t.Errorf("printed usage never mentions %q:\n%s", want, printed)
		}
	}
}
