// The morning report: autonomy's account of itself. Once a day the
// engine writes down, mechanically and for free, what happened while
// nobody was watching — what started, what landed, what it recovered,
// what the steward said, what it all cost, and what is waiting on the
// owner. Every number comes from records that already exist (card
// logs, the spend journal, the board); the report only renders them.
// An engine that acts overnight and cannot account for itself reads
// as spooky; this is the antidote, on a file and an endpoint.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/transcript"
)

type report struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

type reportStore struct {
	path string // "" disables the report
	mu   sync.Mutex
}

func defaultReportPath() string {
	return statePath("vera-report.json")
}

func (st *reportStore) last() report {
	if st.path == "" {
		return report{}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	var r report
	if b, err := os.ReadFile(st.path); err == nil {
		json.Unmarshal(b, &r)
	}
	return r
}

func (st *reportStore) save(r report) error {
	if st.path == "" {
		return errors.New("the report is off (no state directory)")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

type reportSystem struct{ s *server }

func (reportSystem) Name() string { return "report" }

func (r reportSystem) Tick(w *World) []Action {
	if r.s.report == nil || r.s.report.path == "" {
		return nil
	}
	last := r.s.report.last()
	if !last.At.IsZero() && w.Now.Sub(last.At) < 24*time.Hour {
		return nil
	}
	// The day names the action, so one due day launches once however
	// many ticks see it.
	return []Action{{
		Key: "report/" + w.Now.Format("2006-01-02"), Free: true,
		Reason: "write the daily account of what the engine did",
		Run:    func() { r.s.composeReport() },
	}}
}

func (s *server) composeReport() {
	defer s.hub.notify()
	now := time.Now()
	since := s.report.last().At
	if since.IsZero() {
		since = now.Add(-24 * time.Hour)
	}
	text := s.renderReport(since, now)
	if err := s.report.save(report{At: now, Text: text}); err != nil {
		return
	}
	for line := range strings.SplitSeq(text, "\n") {
		fmt.Println("vera: " + line)
	}
}

// renderReport counts the window's events off the records they
// already live in. Text matching against event lines vera itself
// writes is honest here: the log is the record, and these are our own
// sentences.
func (s *server) renderReport(since, now time.Time) string {
	var started, judgedDone, accepted, recovered, stewardMoves, scheduled int
	var waiting []string
	for _, t := range s.tasks.list() {
		if t.open() && t.Col == "waiting" && t.Ask != "" {
			waiting = append(waiting, fmt.Sprintf("%s %q", t.ID, transcript.Snip(t.Title, 40)))
		}
		for _, ev := range t.Log {
			if !ev.At.After(since) || ev.At.After(now) {
				continue
			}
			switch {
			case strings.Contains(ev.Text, "compiled intent →"):
				started++
			case ev.Text == "judged done, proposed acceptance":
				judgedDone++
			case ev.Text == "accepted as done":
				accepted++
			case strings.Contains(ev.Text, "auto-recovering"):
				recovered++
			case strings.HasPrefix(ev.Text, "steward: "):
				stewardMoves++
			case strings.HasPrefix(ev.Text, "born of schedule"):
				scheduled++
			}
		}
	}
	workers, vera := s.spendWindow(since, now)

	span := "the last " + transcript.RelAge(now.Sub(since))
	var b strings.Builder
	if started+judgedDone+accepted+recovered+stewardMoves+scheduled == 0 {
		b.WriteString("A quiet stretch (" + span + "): nothing started, nothing stopped.")
	} else {
		fmt.Fprintf(&b, "Over %s: started %d (%d by schedule) · judged done %d · accepted %d · recovered %d · steward moves %d.",
			span, started, scheduled, judgedDone, accepted, recovered, stewardMoves)
	}
	if workers+vera > 0 {
		fmt.Fprintf(&b, " Spent $%.2f (workers $%.2f, vera $%.2f).", workers+vera, workers, vera)
	}
	if len(waiting) > 0 {
		if len(waiting) > 3 {
			waiting = append(waiting[:3], fmt.Sprintf("and %d more", len(waiting)-3))
		}
		b.WriteString("\nWaiting on you: " + strings.Join(waiting, ", ") + ".")
	}
	return b.String()
}

// spendWindow sums the spend journal inside the window: what claude
// metered for worker turns, and what vera's own calls cost.
func (s *server) spendWindow(since, now time.Time) (workers, vera float64) {
	if s.spendPath == "" {
		return 0, 0
	}
	f, err := os.Open(s.spendPath)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 1<<20)
	for sc.Scan() {
		var l spendLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.At.After(since) && !l.At.After(now) {
			workers += l.Claude
			vera += l.Judge
		}
	}
	return workers, vera
}

func (s *server) handleReport(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.report.last())
}
