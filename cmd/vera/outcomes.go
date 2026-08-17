// The outcome journal: what every finished node actually cost, and
// whether the owner kept it.
//
// The ladder measures routing against a corpus, and the corpus turned
// out to be the weak link — 312 cells and it could not separate the
// tiers on implementation, because a task cheap enough to write is one
// file with a contract in a comment and tests handed over. Real nodes
// are not that. They are several files, a repository that already
// exists, a sentence instead of a spec, and no tests until someone
// writes them.
//
// So this is the same experiment run on the real thing. Every node that
// reaches a terminal column writes one line: its kind, the model that
// was routed to it, what it cost, and — the part no corpus can
// provide — whether the OWNER accepted it. That is the bar a check
// command was only ever standing in for. A synthetic task passes when
// `go test` exits 0; a real node passes when the person who asked for
// it takes the work.
//
// Confounds ride along rather than being quietly dropped, because
// "accepted" hides how much it cost to get there: a node accepted after
// two auto-recoveries and an escalation is not the same evidence as one
// accepted first time, and a table that treats them the same would
// happily route a tier that technically succeeds while burning the
// owner's attention. Retries and escalations are on the line so a later
// reading can separate them.
//
// Append-only jsonl, replayed never queried, the same discipline as
// every other journal here. It outlives the card: a board the owner
// prunes still leaves its evidence behind.
package main

import (
	"encoding/json"
	"time"

	"github.com/incantery/vera/route"
)

// outcomeLine is one finished node. Deliberately flat and small — this
// is a measurement record, not a second copy of the card.
type outcomeLine struct {
	At   time.Time `json:"at"`
	Task string    `json:"task"`
	Goal string    `json:"goal,omitempty"` // the root the node belonged to
	Kind string    `json:"kind,omitempty"`
	// Model is what was actually routed, not what the table would say
	// now. The table moves; the record must not move with it, or a
	// retuning would silently rewrite its own evidence.
	Model string `json:"model,omitempty"`
	Mode  string `json:"mode,omitempty"`
	// Accepted is the owner's verdict: the node reached done. A dropped
	// node is a fail. Nothing else is either — an open card has not
	// finished having an opinion formed about it.
	Accepted bool    `json:"accepted"`
	CostUSD  float64 `json:"costUsd"`
	// What it took to get there. A node accepted after retries and
	// escalations passed, but expensively, and a later reading is
	// entitled to weigh that.
	Retries    int    `json:"retries,omitempty"`
	Escalated  bool   `json:"escalated,omitempty"`
	Runs       int    `json:"runs,omitempty"` // drives and says on the card
	StopReason string `json:"stopReason,omitempty"`
}

func defaultOutcomePath() string { return statePath("vera-outcomes.jsonl") }

// recordOutcome journals a node that just landed. Called at the two
// terminal transitions and nowhere else: accepted (done) and dropped.
// A card that merely moved back to waiting has not finished.
func (s *server) recordOutcome(t task, accepted bool, now time.Time) {
	if s.outcomePath == "" || t.Kind == "" {
		// No kind, nothing to say about routing. Cards born outside a
		// graph are the ordinary case and are not evidence.
		return
	}
	escalated := false
	for _, r := range t.Runs {
		if r.Outcome == "escalated" {
			escalated = true
		}
	}
	appendLine(s.outcomePath, outcomeLine{
		At: now, Task: t.ID, Goal: goalOf(t), Kind: nodeKind(t.Kind),
		Model: t.Model, Mode: t.Mode,
		Accepted: accepted, CostUSD: t.CostUSD,
		Retries: t.Retries, Escalated: escalated || t.StopReason == "escalated",
		Runs: len(t.Runs), StopReason: t.StopReason,
	})
}

// loadOutcomes replays the journal into the observations the verdict
// reads. An unparseable line is skipped rather than fatal — losing a
// line costs one data point, and refusing to read the file at all costs
// every other one.
func loadOutcomes(path string) []route.Observation {
	var out []route.Observation
	eachLine(path, func(b []byte) {
		var l outcomeLine
		if json.Unmarshal(b, &l) != nil || l.Kind == "" {
			return
		}
		out = append(out, route.Observation{
			Kind: l.Kind, Model: l.Model, Pass: l.Accepted, USD: l.CostUSD,
		})
	})
	return out
}
