// The routing readout, ladder-side: turn cells into observations and
// let the route package grade them.
//
// The grading itself moved to route/evidence.go once the board could
// produce evidence too. Two verdict implementations would be free to
// disagree, and the one that disagreed would be whichever one nobody
// was looking at.
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/incantery/vera/route"
)

// observations flattens the journal into what the verdict reads. A cell
// with no kind, or a model that is not a tier alias, is dropped there
// rather than guessed at.
func observations(results []result) []route.Observation {
	out := make([]route.Observation, 0, len(results))
	for _, r := range results {
		out = append(out, route.Observation{
			Kind: r.Kind, Model: r.Model, Pass: r.Pass,
			USD: r.ClaudeUSD + r.JudgeUSD,
		})
	}
	return out
}

func writeRoutingTable(w io.Writer, results []result) {
	route.WriteTable(w, observations(results),
		"no routing evidence yet — the corpus needs tasks tagged with a kind,\n"+
			"run at the tier aliases (-route runs all three).")
}

// boardLine is vera's outcome journal shape, restated here rather than
// imported: cmd/vera is a main package, and a measuring tool that could
// not read a record without linking the thing that wrote it would be a
// worse tool. The fields it needs are few and stable.
type boardLine struct {
	Kind     string  `json:"kind"`
	Model    string  `json:"model"`
	Accepted bool    `json:"accepted"`
	CostUSD  float64 `json:"costUsd"`
}

// loadBoardOutcomes replays vera's outcome journal. A missing file is
// an empty record, not an error — a board that has finished no nodes
// has nothing to say and should say so rather than fail.
func loadBoardOutcomes(path string) ([]route.Observation, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []route.Observation
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var l boardLine
		if json.Unmarshal([]byte(line), &l) != nil {
			continue // one lost data point beats refusing to read the rest
		}
		out = append(out, route.Observation{
			Kind: l.Kind, Model: l.Model, Pass: l.Accepted, USD: l.CostUSD,
		})
	}
	return out, sc.Err()
}
