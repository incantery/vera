// The record and the table. Every finished cell is one JSONL line in
// <world>/results.jsonl — append-only, replayed never queried, the
// same discipline as vera's own journals. Resume is the absence of a
// line: rerunning the same matrix skips what is already on the record,
// so a Ctrl-C costs nothing but the cell it interrupted. A cell that
// errored is still on the record (an error is a data point); delete
// its line to run it again.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

type result struct {
	At    string `json:"at"` // RFC3339
	Task  string `json:"task"`
	Model string `json:"model"`
	Arm   string `json:"arm"` // bare | drive
	Rep   int    `json:"rep"`
	// Kind is the task's node kind, copied onto the record so the
	// routing table can be read back without the corpus that produced
	// it. Lines written before kinds existed carry none and sit out of
	// the routing verdict rather than being guessed at.
	Kind string `json:"kind,omitempty"`
	// Pass is the mechanical bar's word: every bar the task states,
	// met. The judge's own DONE rides separately in Done — the gap
	// between the two columns is the judge grading itself generously.
	Pass      bool    `json:"pass"`
	CheckOK   *bool   `json:"check_ok,omitempty"`  // nil = the task has no check command
	ExpectOK  *bool   `json:"expect_ok,omitempty"` // nil = the task expects no substrings
	Done      bool    `json:"done"`                // drive: the judge said DONE; bare: the turn landed
	Escalated bool    `json:"escalated,omitempty"`
	Turns     int     `json:"turns"`
	ClaudeUSD float64 `json:"claude_usd"`
	JudgeUSD  float64 `json:"judge_usd"`
	Secs      float64 `json:"secs"`
	Session   string  `json:"session,omitempty"` // the root fork — `claude --resume` reads the run
	Reason    string  `json:"reason,omitempty"`
	CheckOut  string  `json:"check_out,omitempty"` // the check's tail, kept on failure only
	Err       string  `json:"err,omitempty"`
}

func (r *result) key() string {
	return fmt.Sprintf("%s|%s|%s|%d", r.Task, r.Model, r.Arm, r.Rep)
}

// loadResults reads the journal, tolerant of a missing file (an empty
// record) and intolerant of a corrupt line (half a record is a lie).
func loadResults(path string) ([]result, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []result
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r result
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("%s line %d did not parse: %w", path, n, err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// appendResult journals one line. The caller holds the lab's lock; a
// single O_APPEND write keeps lines whole regardless.
func appendResult(path string, r result) error {
	buf, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(buf, '\n'))
	return err
}

// cellStats is one table row's arithmetic.
type cellStats struct {
	runs, passes, errs int
	usd, secs          float64 // summed; usd is claude + judge
}

func (c *cellStats) add(r result) {
	c.runs++
	if r.Pass {
		c.passes++
	}
	if r.Err != "" {
		c.errs++
	}
	c.usd += r.ClaudeUSD + r.JudgeUSD
	c.secs += r.Secs
}

// writeTable prints the experiment's answer: per model × arm (or per
// task × model × arm with byTask), how often the bar was met and what
// a success cost. $/pass is the honest metric — retries and judge
// tokens included, failures amortized onto the successes that have to
// pay for them.
func writeTable(w io.Writer, results []result, byTask bool) {
	if len(results) == 0 {
		fmt.Fprintln(w, "nothing on the record yet")
		return
	}
	type rowKey struct{ task, model, arm string }
	cells := map[rowKey]*cellStats{}
	for _, r := range results {
		k := rowKey{model: r.Model, arm: r.Arm}
		if byTask {
			k.task = r.Task
		}
		if cells[k] == nil {
			cells[k] = &cellStats{}
		}
		cells[k].add(r)
	}
	keys := make([]rowKey, 0, len(cells))
	for k := range cells {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.task != b.task {
			return a.task < b.task
		}
		if a.model != b.model {
			return a.model < b.model
		}
		return a.arm < b.arm
	})
	tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
	if byTask {
		fmt.Fprintln(tw, "task\tmodel\tarm\truns\tpass\trate\t$/run\t$/pass\tsecs\terrs")
	} else {
		fmt.Fprintln(tw, "model\tarm\truns\tpass\trate\t$/run\t$/pass\tsecs\terrs")
	}
	for _, k := range keys {
		c := cells[k]
		perPass := "—"
		if c.passes > 0 {
			perPass = fmt.Sprintf("$%.2f", c.usd/float64(c.passes))
		}
		row := fmt.Sprintf("%s\t%s\t%d\t%d\t%d%%\t$%.2f\t%s\t%.0f\t%d",
			k.model, k.arm, c.runs, c.passes, 100*c.passes/c.runs,
			c.usd/float64(c.runs), perPass, c.secs/float64(c.runs), c.errs)
		if byTask {
			row = k.task + "\t" + row
		}
		fmt.Fprintln(tw, row)
	}
	tw.Flush()
}
