// Package fleet is Vera provisioning rooms for work and watching them.
//
// The shape is firstmate's (github.com/kunchenguid/firstmate), which
// proved it across five multiplexers with forty shell scripts: one
// supervisor, many crewmates, each in its own worktree and its own
// pane; an append-only status log per task in a small verb vocabulary;
// a watcher that classifies liveness from evidence rather than from a
// pane looking busy; and a durable queue of the moments that need a
// person. Here it is a Go package with a process behind it instead of a
// bash loop, so the durability machinery firstmate needed to survive
// its own absence is not needed.
//
// The boundary this keeps is delegate.go's: Vera provisions the room —
// worktree, branch, pane, brief — and never does the work. The moment
// this package starts describing HOW a task should be done, it has
// become a coding agent, and it was never supposed to be one.
package fleet

import (
	"time"

	"github.com/incantery/vera/mux"
)

// Kind is what a task produces.
type Kind string

const (
	// Ship delivers a change: a branch to merge or a PR.
	Ship Kind = "ship"
	// Scout delivers a report and touches nothing.
	Scout Kind = "scout"
)

// Mode is who may land a ship task, and how.
type Mode string

const (
	// LocalOnly merges into the default branch in the main checkout.
	LocalOnly Mode = "local-only"
	// DirectPR opens a PR and stops; a person merges.
	DirectPR Mode = "direct-pr"
	// NoMistakes runs review gates before anything lands.
	NoMistakes Mode = "no-mistakes"
)

// Verb is the status vocabulary, firstmate's six words. A crewmate or
// Vera appends one line at a time; the log is never rewritten.
type Verb string

const (
	Working  Verb = "working"
	Paused   Verb = "paused"   // a declared external wait
	Blocked  Verb = "blocked"  // a decision is needed from a person
	Resolved Verb = "resolved" // the decision was answered
	Done     Verb = "done"
	Failed   Verb = "failed"
)

// Terminal says whether a verb ends the task.
func (v Verb) Terminal() bool { return v == Done || v == Failed }

// Status is one line of a task's log.
type Status struct {
	At   time.Time `json:"at"`
	Verb Verb      `json:"verb"`
	Text string    `json:"text,omitempty"`
	// By is who said it: "agent", "vera", "person".
	By string `json:"by,omitempty"`
}

// Task is one room and what was asked of it. It is the durable record;
// State is derived from evidence, never stored.
type Task struct {
	ID      string `json:"id"`
	Project string `json:"project"` // main checkout root
	Name    string `json:"name"`    // worktree short name = branch
	Kind    Kind   `json:"kind"`
	Mode    Mode   `json:"mode,omitempty"`
	Brief   string `json:"brief"`
	// Images are pictures the person handed over with the ask —
	// absolute paths to files Vera keeps (see package attach). The
	// brief names them and tells the agent to open them; the fleet
	// never reads them itself. Kept on the record because a task that
	// is resumed a day later has to be handed the same evidence.
	Images []string `json:"images,omitempty"`

	Worktree string `json:"worktree"` // path; the Project itself for scouts
	Branch   string `json:"branch,omitempty"`
	Session  string `json:"session"`
	Pane     mux.ID `json:"pane"`

	Spawned time.Time `json:"spawned"`
	// Incarnation changes every spawn of the same task so a stale hook
	// from a previous life is ignored.
	Incarnation string `json:"incarnation"`
	// Resumed is the last time Vera reopened the room, zero if never.
	Resumed time.Time `json:"resumed,omitempty"`
	// TurnEnded is the last time the agent's harness said its turn was
	// over (the Claude Code Stop hook). Zero until it does.
	TurnEnded time.Time `json:"turn_ended,omitempty"`
	// Closed: landed or torn down. Kept on disk for the record.
	Closed   bool      `json:"closed,omitempty"`
	ClosedAt time.Time `json:"closed_at,omitempty"`
	// PR is the URL when Mode opened one.
	PR string `json:"pr,omitempty"`
	// LandFailedAt and LandFailure: the last time Vera tried to land
	// this on its own and could not, and why. A `done` newer than the
	// failure is a reason to try again.
	LandFailedAt time.Time `json:"land_failed_at,omitempty"`
	LandFailure  string    `json:"land_failure,omitempty"`
}
