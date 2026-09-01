// The side rail: what Vera draws in rook.
//
// rook's rail is two lists, spaces and agents, and rook paints
// whatever it is pushed — it decides nothing about what the rows
// mean (rook's docs/surfaces.md; the branch that made it so was a
// fleet task). This is the producer: the fleet, in rows. Spaces are
// the repositories Vera knows, coloured by the state of the work in
// them; agents are the open tasks. It is pushed when something
// changed and never otherwise, because a producer at its own cadence
// must not cost the glass a repaint.
//
// Two fields are rook's rather than ours, and both are about a click.
// `workspace` is where the row's agent lives, and a click on the row
// goes there — so a row names a room only while the mux still holds
// it. `current` is where the cursor sits, which rook hands back to the
// producer on the next push: it means the row the person is ON, so
// that a click and the push that follows it agree. Neither is a place
// to say a task wants attention — that is the row's state and its
// word.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/mux"
)

// railItem is one row, in rook's vocabulary. State is one of
// working, idle, blocked, done, failed; an unknown one draws plain.
type railItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	State    string `json:"state,omitempty"`
	Current  bool   `json:"current,omitempty"`
	// Workspace is the rook workspace the row's agent runs in — rook's
	// own vocabulary, and the one identity the two sides share. It is
	// how this row claims the session rook can see there, so the rail
	// does not list one agent twice: once as the task, once as the
	// pane rook found running it. Title and ID are ours; rook cannot
	// resolve either against its pane table.
	Workspace string `json:"workspace,omitempty"`
}

type railFrame struct {
	V      int        `json:"v"`
	Op     string     `json:"op"`
	Params railParams `json:"params"`
}

type railParams struct {
	Surface string     `json:"surface"`
	Items   []railItem `json:"items"`
}

// railState maps a task's state to a rail colour.
func railState(s fleet.State) string {
	switch s {
	case fleet.Running, fleet.Quiet:
		return "working"
	case fleet.Waiting, fleet.Decision, fleet.Stale:
		return "blocked"
	case fleet.Interrupted:
		// Deliberately not "blocked": the row must not read as
		// something the person has to answer.
		return "idle"
	case fleet.Finished:
		return "done"
	case fleet.Broken:
		return "failed"
	default:
		return "idle"
	}
}

// railFrames builds the two frames from what the fleet believes.
// focused is the repo root in front of the person and here is the
// workspace they are standing in — the two halves of `current`, which
// on either panel means the row the person is ON and nothing else.
// panes is the mux's pane table: a repo is a *space* only when a
// workspace is open in it, and the row names that workspace so rook
// folds its own row for it in rather than listing both. Rook lists
// every workspace it holds by itself; a repo with nothing open is not
// a space, it is something the picker could open.
func railFrames(repos []fleet.Repo, tasks []fleet.View, focused, here string, panes []mux.Pane) (spaces, agents railFrame) {
	// Spaces: every known repo; its state is the loudest of its open
	// tasks' — blocked beats working beats done beats idle.
	rank := map[string]int{"idle": 0, "done": 1, "working": 2, "blocked": 3, "failed": 3}
	byRepo := map[string]string{}
	open := 0
	for _, t := range tasks {
		if t.Closed {
			continue
		}
		open++
		s := railState(t.State)
		if rank[s] > rank[byRepo[t.Project]] {
			byRepo[t.Project] = s
		}
	}
	spaces = railFrame{V: 1, Op: "items.push", Params: railParams{Surface: "spaces", Items: []railItem{}}}
	for _, r := range repos {
		ws := workspaceIn(panes, r.Root)
		if ws == "" {
			continue
		}
		state := byRepo[r.Root]
		if state == "" {
			state = "idle"
		}
		sub := ""
		n := 0
		for _, t := range tasks {
			if !t.Closed && t.Project == r.Root {
				n++
			}
		}
		if n == 1 {
			sub = "1 task"
		} else if n > 1 {
			sub = itoa(n) + " tasks"
		}
		spaces.Params.Items = append(spaces.Params.Items, railItem{ID: r.Root, Title: r.Name, Subtitle: sub, State: state, Current: r.Root == focused, Workspace: ws})
	}

	// Agents: one row per open task, newest first — what it is doing,
	// in the person's nouns.
	agents = railFrame{V: 1, Op: "items.push", Params: railParams{Surface: "agents", Items: []railItem{}}}
	var openTasks []fleet.View
	for _, t := range tasks {
		if !t.Closed {
			openTasks = append(openTasks, t)
		}
	}
	sort.SliceStable(openTasks, func(i, j int) bool { return openTasks[i].Spawned.After(openTasks[j].Spawned) })
	stood := false
	for _, t := range openTasks {
		title := trim(firstSentence(t.Brief), 28)
		sub := railWord(t.State) + " · " + shortPath(t.Project)
		// The row is current when the person is standing in its room,
		// and one row at most: `current` is where the cursor sits, so
		// two of them is rook picking. It used to mark every task that
		// wanted the person, which is a different thing said in the
		// wrong field — and it made the panel look inert, because a
		// click moves rook's cursor and the next push puts it back
		// where the producer said. Now the click and the next push
		// agree: rook goes to that agent's room, and the row for that
		// room is the one Vera calls current. What wants the person is
		// on the row already, in its state and its word.
		current := false
		if !stood && here != "" && t.Session == here {
			current, stood = true, true
		}
		agents.Params.Items = append(agents.Params.Items, railItem{ID: t.ID, Title: title, Subtitle: sub, State: railState(t.State), Current: current, Workspace: claim(panes, t.Session)})
	}
	return spaces, agents
}

// claim is the workspace a row may name: the task's room, when the mux
// still holds it. A click goes to the workspace the row names, so a
// name for a room that is gone — the engine was restarted under it,
// the person closed it — is a row that does nothing when clicked, and
// a claim that would fold rook's own row for that name into a task
// that is no longer there. An empty pane table is the mux not
// answering rather than every room being gone: claim the room then,
// and let the next push correct it.
func claim(panes []mux.Pane, session string) string {
	if session == "" || len(panes) == 0 {
		return session
	}
	for _, p := range panes {
		if p.ID.Session == session {
			return session
		}
	}
	return ""
}

// workspaceIn is the workspace holding a pane whose cwd is the repo's
// main checkout (or inside it), or "". A task's worktree is a sibling
// directory, not inside the root, so a task's room never stands in
// for the repo's own space. The first such workspace in pane order
// wins; two workspaces on one checkout is a person's choice rook
// still lists in full.
func workspaceIn(panes []mux.Pane, root string) string {
	for _, p := range panes {
		if p.Path == root || strings.HasPrefix(p.Path, root+"/") {
			return p.ID.Session
		}
	}
	return ""
}

// railWord is the short word for a state, for a row's subtitle.
func railWord(s fleet.State) string {
	switch s {
	case fleet.Running, fleet.Quiet:
		return "working"
	case fleet.Waiting:
		return "needs you"
	case fleet.Decision:
		return "decision"
	case fleet.Stale:
		return "quiet"
	case fleet.Held:
		return "paused"
	case fleet.Interrupted:
		return "interrupted"
	case fleet.Finished:
		return "done"
	case fleet.Broken:
		return "failed"
	case fleet.Gone:
		return "gone"
	default:
		return string(s)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// rail is the publisher. It runs until ctx ends, pushing when the
// frames differ from the last ones pushed, on the fleet's poke and on
// a slow tick.
type rail struct {
	side     mux.Sider
	fleet    *fleet.Fleet
	projects *fleet.Projects
	// focus is where the person is: the repo root in front of them and
	// the workspace they are standing in, either of which may be "".
	focus  func() (root, workspace string)
	panes  func(context.Context) []mux.Pane
	poke   chan struct{}
	mu     sync.Mutex // guards last: Reset comes from the mux watcher
	last   [2][]byte
	warned bool
}

func newRail(side mux.Sider, f *fleet.Fleet, p *fleet.Projects, focus func() (root, workspace string), panes func(context.Context) []mux.Pane) *rail {
	return &rail{side: side, fleet: f, projects: p, focus: focus, panes: panes, poke: make(chan struct{}, 1)}
}

// Reset forgets what was last pushed, so the next push sends both
// frames whether or not they changed. The engine holds the rail in
// memory: after a restart it has nothing, and a producer that only
// pushes on change would leave it blank until the fleet moved.
func (r *rail) Reset() {
	r.mu.Lock()
	r.last = [2][]byte{}
	r.mu.Unlock()
	r.Poke()
}

func (r *rail) Poke() {
	select {
	case r.poke <- struct{}{}:
	default:
	}
}

func (r *rail) run(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		r.push(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-r.poke:
			time.Sleep(200 * time.Millisecond) // let a burst settle
		}
	}
}

func (r *rail) push(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tasks, err := r.fleet.Tasks(ctx)
	if err != nil {
		return
	}
	var repos []fleet.Repo
	if r.projects != nil {
		repos = r.projects.Known(ctx)
	}
	focused, here := "", ""
	if r.focus != nil {
		focused, here = r.focus()
	}
	var panes []mux.Pane
	if r.panes != nil {
		panes = r.panes(ctx)
	}
	spaces, agents := railFrames(repos, tasks, focused, here, panes)
	for i, f := range []railFrame{spaces, agents} {
		b, err := json.Marshal(f)
		if err != nil {
			continue
		}
		r.mu.Lock()
		same := bytes.Equal(b, r.last[i])
		r.mu.Unlock()
		if same {
			continue
		}
		if err := r.side.Side(ctx, b); err != nil {
			// Once per failure, not once per tick: a rail rook refuses
			// is a bug worth a line, not a log worth a scroll.
			if !r.warned {
				slog.Warn("rail: push refused", "surface", f.Params.Surface, "error", err.Error())
				r.warned = true
			}
			continue
		}
		r.warned = false
		r.mu.Lock()
		r.last[i] = b
		r.mu.Unlock()
	}
}
