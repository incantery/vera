package usage

import (
	"testing"
	"time"
)

const report = `Claude Max subscription

Current session: 34% used · resets 6pm (America/Denver)
Current week (all models): 61% used · resets Aug 18, 9am
Current week (Fable): 48% used · resets Aug 18, 9am
`

func TestParseReadsAllThreeWindows(t *testing.T) {
	now := time.Now()
	u, ok := Parse(report, now)
	if !ok {
		t.Fatal("the report did not parse")
	}
	if u.SessionPct != 34 || u.SessionResets != "6pm" {
		t.Fatalf("session: %+v", u)
	}
	if u.WeekAllPct != 61 || u.WeekAllResets != "Aug 18, 9am" {
		t.Fatalf("week all: %+v", u)
	}
	if u.WeekModelName != "Fable" || u.WeekModelPct != 48 {
		t.Fatalf("week model: %+v", u)
	}
}

func TestParseAPIAccountIsAnHonestMiss(t *testing.T) {
	if _, ok := Parse("API usage this month: $12.40", time.Now()); ok {
		t.Fatal("an API-billed report must not parse as subscription")
	}
}
