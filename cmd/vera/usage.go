// What is left of the subscription.
//
// This is the one number about Claude Code that telemetry cannot give
// you, and the one that can actually stop you working. On a
// subscription the dollar figures — claude_code.cost.usage, and the
// total_cost_usd this logs per delegation — are NOTIONAL: what those
// tokens would have cost on the API, which is not what you pay. What
// you can actually run out of is a percentage of a weekly limit, and it
// exists nowhere except the text of `claude /usage`.
//
// So this scrapes it. That makes it the one fragile thing in here: a
// human-readable report is not a contract and will change wording
// eventually. It is therefore written to FAIL rather than to cope — a
// parse that finds nothing is an error and says so, because a gauge
// quietly reporting 0% is worse than no gauge at all.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Limit struct {
	// Ratio of the limit consumed, 0–1.
	Used float64
	// When the window rolls over. Zero if it could not be read.
	Resets time.Time
}

type Usage struct {
	Session   Limit
	Week      Limit
	ByModel   map[string]Limit
	Requests  int
	Sessions  int
	OnPlan    bool
	ScrapedAt time.Time
}

var (
	sessionLine = regexp.MustCompile(`(?i)^current session:\s*(\d+)%\s*used`)
	weekLine    = regexp.MustCompile(`(?i)^current week \(([^)]+)\):\s*(\d+)%\s*used`)
	last24Line  = regexp.MustCompile(`(?i)last 24h.*?(\d+)\s+requests.*?(\d+)\s+sessions`)
	resetsPart  = regexp.MustCompile(`(?i)resets\s+(.+?)\s*(?:\(([^)]+)\))?\s*$`)
)

// scrapeUsage asks Claude Code what is left. `-p "/usage"` runs the
// slash command non-interactively; it costs no session and barely a
// request, which is what makes polling it reasonable.
func scrapeUsage(ctx context.Context) (Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "claude", "-p", "/usage").Output()
	if err != nil {
		var detail string
		if ee, ok := err.(*exec.ExitError); ok {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		if detail == "" {
			detail = err.Error()
		}
		return Usage{}, fmt.Errorf("asking claude for usage: %s", trim(detail, 200))
	}
	return parseUsage(string(out))
}

func parseUsage(text string) (Usage, error) {
	u := Usage{ByModel: map[string]Limit{}, ScrapedAt: time.Now()}
	found := false

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		u.OnPlan = u.OnPlan || strings.Contains(strings.ToLower(line), "using your subscription")

		if m := sessionLine.FindStringSubmatch(line); m != nil {
			u.Session = limitFrom(m[1], line)
			found = true
			continue
		}
		if m := weekLine.FindStringSubmatch(line); m != nil {
			which, pct := strings.TrimSpace(m[1]), m[2]
			if strings.EqualFold(which, "all models") {
				u.Week = limitFrom(pct, line)
			} else {
				u.ByModel[which] = limitFrom(pct, line)
			}
			found = true
			continue
		}
		if m := last24Line.FindStringSubmatch(line); m != nil {
			u.Requests, _ = strconv.Atoi(m[1])
			u.Sessions, _ = strconv.Atoi(m[2])
			found = true
		}
	}

	if !found {
		// The wording changed, or claude said something else entirely.
		// Reporting zeroes here would look exactly like a fresh week.
		return Usage{}, fmt.Errorf("could not read any limits from /usage — the format has probably changed: %s", trim(text, 200))
	}
	return u, nil
}

func limitFrom(percent, line string) Limit {
	n, _ := strconv.Atoi(percent)
	return Limit{Used: float64(n) / 100, Resets: parseReset(line)}
}

// parseReset reads "resets Aug 22 at 7:59am (America/New_York)". The
// year is absent, so it is inferred — and a date that lands in the past
// is next year's, which is the only way this behaves sanely between
// Christmas and New Year.
func parseReset(line string) time.Time {
	m := resetsPart.FindStringSubmatch(line)
	if m == nil {
		return time.Time{}
	}
	when := strings.TrimSpace(m[1])
	loc := time.Local
	if len(m) > 2 && m[2] != "" {
		if l, err := time.LoadLocation(strings.TrimSpace(m[2])); err == nil {
			loc = l
		}
	}
	now := time.Now().In(loc)
	for _, layout := range []string{"Jan 2 at 3:04pm", "Jan 2 at 3:04PM", "Jan 2 at 15:04"} {
		t, err := time.ParseInLocation(layout, when, loc)
		if err != nil {
			continue
		}
		t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
		if t.Before(now.Add(-24 * time.Hour)) {
			t = t.AddDate(1, 0, 0)
		}
		return t
	}
	return time.Time{}
}

func (u Usage) String() string {
	var b strings.Builder
	if !u.OnPlan {
		b.WriteString("not on a subscription — these limits may not apply\n")
	}
	fmt.Fprintf(&b, "session      %3.0f%%%s\n", u.Session.Used*100, resetsIn(u.Session.Resets))
	fmt.Fprintf(&b, "week         %3.0f%%%s\n", u.Week.Used*100, resetsIn(u.Week.Resets))
	for model, l := range u.ByModel {
		fmt.Fprintf(&b, "week (%s)%s%3.0f%%%s\n", model,
			strings.Repeat(" ", max(1, 7-len(model))), l.Used*100, resetsIn(l.Resets))
	}
	fmt.Fprintf(&b, "last 24h     %d requests, %d sessions", u.Requests, u.Sessions)
	return b.String()
}

func resetsIn(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Until(t)
	if d < 0 {
		return "  (due to reset)"
	}
	if d < time.Hour {
		return fmt.Sprintf("  (resets in %dm)", int(d.Minutes()))
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("  (resets in %dh)", int(d.Hours()))
	}
	return fmt.Sprintf("  (resets in %dd)", int(d.Hours()/24))
}
