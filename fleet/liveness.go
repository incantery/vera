package fleet

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State is what Vera currently believes about a task. It is computed
// from evidence every time it is asked for; nothing stores it.
type State string

const (
	// Running: the agent is producing output or writing files.
	Running State = "running"
	// Quiet: nothing for a while, but not long enough to worry.
	Quiet State = "quiet"
	// Stale: nothing for long enough that a person should look.
	Stale State = "stale"
	// Waiting: the agent ended its turn and nobody has acted since —
	// it is waiting on a person.
	Waiting State = "waiting"
	// Held: the agent declared a pause on something external.
	Held State = "held"
	// Decision: the agent said it is blocked on a decision.
	Decision State = "decision"
	// Finished / Broken: the agent's own last word.
	Finished State = "finished"
	Broken   State = "broken"
	// Gone: the pane is not there.
	Gone State = "gone"
	// Closed: Vera landed or tore the task down.
	Closed State = "closed"
)

// Actionable says whether a state is a reason to wake a person.
func (s State) Actionable() bool {
	switch s {
	case Waiting, Stale, Decision, Finished, Broken, Gone:
		return true
	}
	return false
}

// Evidence is everything liveness is read from. Each field is a fact
// with a source; Classify is the only place they are weighed together.
type Evidence struct {
	Now time.Time
	// PaneAlive: the mux still has the pane and its process.
	PaneAlive bool
	// AgentAlive: the pane's foreground program is the agent, not the
	// shell it was started from. A pane whose agent has exited shows
	// a prompt: alive to the mux, gone for our purposes.
	AgentAlive bool
	// PaneActive: the last time the pane produced output.
	PaneActive time.Time
	// LastWrite: the newest mtime under the worktree. A pane that is
	// quiet while files change is an agent writing source, then tests,
	// then docs behind a static screen — that is liveness, not a stall.
	// firstmate's one big lesson.
	LastWrite time.Time
	// TurnEnded: the harness's own word that the agent stopped.
	TurnEnded time.Time
	// Last is the newest status line, nil if none.
	Last   *Status
	Closed bool
}

// Thresholds are how long quiet is quiet, and how long it is stale.
type Thresholds struct {
	Quiet time.Duration
	Stale time.Duration
}

// DefaultThresholds: a coding agent thinks for a minute routinely and
// for ten rarely.
var DefaultThresholds = Thresholds{Quiet: 90 * time.Second, Stale: 10 * time.Minute}

// Classify turns evidence into a state. Order matters: a person's
// decision outranks the agent's word, which outranks the harness's,
// which outranks what the screen looks like.
func Classify(e Evidence, th Thresholds) State {
	if e.Closed {
		return Closed
	}
	if !e.PaneAlive || !e.AgentAlive {
		return Gone
	}
	if e.Last != nil {
		switch e.Last.Verb {
		case Done:
			return Finished
		case Failed:
			return Broken
		case Blocked:
			return Decision
		case Paused:
			return Held
		}
	}
	latest := e.PaneActive
	if e.LastWrite.After(latest) {
		latest = e.LastWrite
	}
	// The harness said the turn ended and nothing has happened since:
	// the agent is waiting on somebody. A later pulse means a person
	// (or Vera) already answered and it is running again.
	if !e.TurnEnded.IsZero() && !latest.After(e.TurnEnded) {
		return Waiting
	}
	if latest.IsZero() {
		return Quiet
	}
	idle := e.Now.Sub(latest)
	switch {
	case idle < th.Quiet:
		return Running
	case idle < th.Stale:
		return Quiet
	default:
		return Stale
	}
}

// pruned are directories no agent's work lands in and every repo has
// thousands of files under.
var pruned = map[string]bool{".git": true, "node_modules": true, ".venv": true, "target": true, "dist": true, "build": true, "zig-out": true, ".zig-cache": true}

// NewestWrite is the write-evidence scan: the newest mtime under dir,
// bounded by depth and by time so a huge tree costs a bounded amount
// per poll. It answers "did anything change lately", so it can stop
// early once it has seen something newer than `since`.
func NewestWrite(dir string, since time.Time, maxDepth int, budget time.Duration) time.Time {
	deadline := time.Now().Add(budget)
	var newest time.Time
	root := filepath.Clean(dir)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if time.Now().After(deadline) || (!since.IsZero() && newest.After(since)) {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if p != root && (pruned[d.Name()] || depth(root, p) >= maxDepth) {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest
}

func depth(root, p string) int {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(os.PathSeparator)) + 1
}

// shells are what a pane shows when the agent in it has exited.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "": true}

// IsShell says whether a foreground program name is a shell.
func IsShell(command string) bool { return shells[command] }
