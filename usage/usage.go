// Package usage answers: how much of the Claude subscription is spent?
// The claude CLI is the ONLY honest source — it knows the account's
// rate-limit windows; transcripts do not.
//
// The file is rook's own usage.json, shared on purpose: a running rook
// terminal collects the same numbers to the same path, so whichever
// process is alive does the collecting and the other reads fresh — the
// more rook you run, the less anyone shells out.
package usage

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Usage is the parsed shape of the CLI's report: subscription
// rate-limit percentages, per window.
type Usage struct {
	Mode          string `json:"mode"` // "subscription"
	SessionPct    int    `json:"sessionPct"`
	SessionResets string `json:"sessionResets,omitempty"`
	WeekAllPct    int    `json:"weekAllPct"`
	WeekAllResets string `json:"weekAllResets,omitempty"`
	// The weekly per-model window (the CLI names the model, e.g. "Fable").
	WeekModelName   string    `json:"weekModelName,omitempty"`
	WeekModelPct    int       `json:"weekModelPct"`
	WeekModelResets string    `json:"weekModelResets,omitempty"`
	At              time.Time `json:"at"`
}

// SharedPath is rook's usage file: $XDG_STATE_HOME/rook/usage.json.
func SharedPath() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "rook", "usage.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "rook", "usage.json")
}

var (
	reSession   = regexp.MustCompile(`Current session:\s*(\d+)% used(?: · resets ([^\n(]+))?`)
	reWeekAll   = regexp.MustCompile(`Current week \(all models\):\s*(\d+)% used(?: · resets ([^\n(]+))?`)
	reWeekModel = regexp.MustCompile(`Current week \(([^)]+)\):\s*(\d+)% used(?: · resets ([^\n(]+))?`)
)

// Parse reads `claude /usage -p` output. Absence of the subscription
// banner (an API-billed account prints a different report) is an
// honest miss, not an error.
func Parse(out string, now time.Time) (Usage, bool) {
	u := Usage{Mode: "subscription", At: now}
	m := reSession.FindStringSubmatch(out)
	if m == nil {
		return Usage{}, false
	}
	u.SessionPct = atoi(m[1])
	u.SessionResets = strings.TrimSpace(m[2])
	if m := reWeekAll.FindStringSubmatch(out); m != nil {
		u.WeekAllPct = atoi(m[1])
		u.WeekAllResets = strings.TrimSpace(m[2])
	}
	for _, m := range reWeekModel.FindAllStringSubmatch(out, -1) {
		if strings.EqualFold(m[1], "all models") {
			continue
		}
		u.WeekModelName = m[1]
		u.WeekModelPct = atoi(m[2])
		u.WeekModelResets = strings.TrimSpace(m[3])
		break
	}
	return u, true
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// Collector keeps the latest report: the shared file's word when it is
// fresh, its own `claude /usage -p` when it is not. Failures are quiet
// misses — the readers see nil and the page simply shows no usage.
type Collector struct {
	Bin    string        // the claude binary; "" = from PATH
	Path   string        // the shared file; "" = SharedPath()
	Every  time.Duration // collection cadence; default 5m
	MaxAge time.Duration // older than this = a dead collector's last words; default 15m

	mu  sync.Mutex
	cur *Usage
}

func (c *Collector) path() string {
	if c.Path != "" {
		return c.Path
	}
	return SharedPath()
}

func (c *Collector) every() time.Duration {
	if c.Every > 0 {
		return c.Every
	}
	return 5 * time.Minute
}

func (c *Collector) maxAge() time.Duration {
	if c.MaxAge > 0 {
		return c.MaxAge
	}
	return 15 * time.Minute
}

// Latest is the freshest report known, or nil.
func (c *Collector) Latest() *Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cur == nil || time.Since(c.cur.At) > c.maxAge() {
		return nil
	}
	u := *c.cur
	return &u
}

// Loop runs until the process dies.
func (c *Collector) Loop() {
	for {
		c.collect()
		time.Sleep(c.every())
	}
}

func (c *Collector) collect() {
	now := time.Now()
	// Someone else's fresh collection (a running rook) beats shelling out.
	if u := readFile(c.path(), now, c.every()+time.Minute); u != nil {
		c.mu.Lock()
		c.cur = u
		c.mu.Unlock()
		return
	}
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "/usage", "-p").Output()
	if err != nil {
		return
	}
	u, ok := Parse(string(out), now)
	if !ok {
		return
	}
	c.mu.Lock()
	c.cur = &u
	c.mu.Unlock()
	writeFile(c.path(), u)
}

func readFile(path string, now time.Time, maxAge time.Duration) *Usage {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var u Usage
	if json.Unmarshal(b, &u) != nil || u.Mode == "" || now.Sub(u.At) > maxAge {
		return nil
	}
	return &u
}

// writeFile replaces the shared file atomically — rook's readers may
// be mid-read on the other side.
func writeFile(path string, u Usage) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	b, err := json.Marshal(u)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, path)
	}
}
