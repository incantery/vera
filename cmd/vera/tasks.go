// The board: ONE global list of work, across every agent. A task is a
// unit of work with an append-only log; an agent is an ASSIGNMENT a
// task may carry, not a namespace tasks live inside. There is one
// board the way there is one team: the current agent's work is one
// in-progress card wearing that agent's live status, and everything
// discovered along the way is unassigned backlog until an agent is
// free to take it.
//
// Two ways a task is born:
//   - captured: the human says what needs doing; it lands in the inbox
//     unassigned, and vera spends nothing.
//   - adopted: an agent is WORKING right now with no open task assigned
//     — the work already exists, so the board says so: a progress card
//     titled with the agent's own title, on the record as adopted.
//
// Assigned cards wear the agent's live state (state, the membrane's
// now-line) as a DERIVED overlay computed at read time — the log is
// the truth about the workflow, the transcript is the truth about the
// moment, and neither is written into the other.
//
// Persistence follows the shelf's pattern: one JSON file per task
// under <state>/vera/vera-tasks/global/.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"crypto/rand"
	"encoding/hex"

	"github.com/incantery/vera/drive"
	"github.com/incantery/vera/transcript"
)

// globalBoard is the one namespace tasks live in. (Earlier builds kept
// a directory per agent; those directories are simply no longer read.)
const globalBoard = "global"

type taskEvent struct {
	At    time.Time `json:"at"`
	Actor string    `json:"actor"` // human | vera | worker
	Text  string    `json:"text"`
}

type taskRun struct {
	Kind    string  `json:"kind"` // drive | say
	Outcome string  `json:"outcome"`
	CostUSD float64 `json:"costUsd,omitempty"`
}

type task struct {
	ID     string `json:"id"` // T-<n>, global
	Title  string `json:"title"`
	Intent string `json:"intent"`
	// Agent is the ASSIGNMENT: the lineage root working this task, or
	// "" for backlog nobody has picked up.
	Agent string `json:"agent,omitempty"`
	// The goal the intent compiles to when the task starts — vera's
	// words, on the record beside the owner's.
	Goal      string `json:"goal,omitempty"`
	GoalActor string `json:"goalActor,omitempty"`

	Col   string `json:"col"`   // inbox | progress | waiting | done | dropped
	State string `json:"state"` // the human-readable line under the column
	Ask   string `json:"ask,omitempty"`
	Face  string `json:"face,omitempty"` // the card's one-line story

	Pinned bool `json:"pinned,omitempty"`

	// Adopted: the board created this card itself from a live working
	// session (not a human capture). Adopted cards close themselves
	// when their session goes quiet — the transcript is the record.
	Adopted bool `json:"adopted,omitempty"`

	// Vera proposes, the human disposes.
	Proposal     string `json:"proposal,omitempty"`
	ProposalWhy  string `json:"proposalWhy,omitempty"`
	ProposalKind string `json:"proposalKind,omitempty"` // "start" | "done" | "reply"
	// ProposalText is the payload a "reply" proposal carries: the
	// exact drafted answer that goes to the worker if the owner
	// accepts. Shown verbatim before the tap — nothing is sent that
	// was not seen.
	ProposalText string `json:"proposalText,omitempty"`

	Runs    []taskRun `json:"runs,omitempty"`
	CostUSD float64   `json:"costUsd,omitempty"`

	// The stop record: how the last run died, worn durably so the
	// engine's recover system can tell machinery failures (transient —
	// retry is sane) from judgment calls (the human's). Retries counts
	// automatic recoveries since the last human touch; a reply or a
	// restart from the owner resets the budget.
	StopErr       string `json:"stopErr,omitempty"`
	StopTransient bool   `json:"stopTransient,omitempty"`
	Retries       int    `json:"retries,omitempty"`
	// StopReason names how the last run ended, structurally: "error",
	// "escalated", "circling", "spend-cap", "turns", "done". The
	// driver reads it to know which stops autopilot may roll through.
	StopReason string `json:"stopReason,omitempty"`

	// BudgetUSD is the marathon tier: the owner's dollar authorization
	// for this card. While spend stays under it, the driver system
	// continues stopped runs on its own — routine escalations included
	// — so the card runs for hours with the budget as the only human
	// boundary. Circling and machinery errors still stop it; work mode
	// never qualifies (autopilot is read-only by construction).
	BudgetUSD float64 `json:"budgetUsd,omitempty"`

	// Workspace is where the task's runs execute — the assigned
	// agent's directory, recorded at start so cleanup knows the place
	// even after the agent scrolls out of the window.
	Workspace string `json:"workspace,omitempty"`
	// ScratchName is derived at read time: set when Workspace is a
	// vera-managed scratch dir that still exists, never persisted.
	ScratchName string `json:"scratchName,omitempty"`

	// Mode is the task's tool policy: "read" (default — print mode's
	// refusals stand) or "work" (edits and scoped build/test commands,
	// through claude's own permission system). Code-side sets, never
	// LLM-chosen.
	Mode string `json:"mode,omitempty"`
	// AutoStart marks an inbox card the engine may start on its own —
	// "read" is the only tier autonomy takes (analysis cannot mutate;
	// the worst case is bounded spend). The steward sets it, the
	// ignite system burns it on the attempt, and work mode stays
	// behind the owner's nod.
	AutoStart string `json:"autoStart,omitempty"`
	// Cadence and Deadline come from vera's plan when the card was born
	// of one: "once" | "standing", and YYYY-MM-DD if the ask named a
	// date. Standing is recorded before it is executable — vera cannot
	// yet return on its own; each pass starts from the card.
	Cadence  string `json:"cadence,omitempty"`
	Deadline string `json:"deadline,omitempty"`
	// NextID chains a plan's pieces: accepting this card as done is
	// the nod that starts the card it names. The chain spends only at
	// human acceptance boundaries.
	NextID string `json:"nextId,omitempty"`

	// The graph, when a plan laid one down. Kind is what the node is
	// FOR — implement, review, verify — and decides both whether vera
	// may open it unasked and which worker she reaches for. Deps names
	// what it waits on, which is the chain generalized: NextID could
	// only say "after this one", Deps can say "after all of these".
	// Root is the goal every node of one plan shares, so the work view
	// can draw a graph instead of filtering a table.
	//
	// A card with no Deps is not in a graph; a card with no Root is its
	// own goal. Both stay true of every card written before this
	// existed, which is why nothing needs migrating.
	Kind string   `json:"kind,omitempty"`
	Deps []string `json:"deps,omitempty"`
	Root string   `json:"root,omitempty"`
	// Model is what routing actually reached for, recorded when the
	// worker was born. Kept on the card rather than re-derived at read
	// time on purpose: the tier table moves as it learns, and evidence
	// that moved with it would be evidence rewriting its own past.
	Model string `json:"model,omitempty"`
	// Exchanges is the drive's own conversation, persisted so an
	// owner's reply can seed a continuation after any restart. Capped:
	// the transcript holds the full story, the task holds the working
	// set.
	Exchanges []drive.Exchange `json:"exchanges,omitempty"`

	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
	Log       []taskEvent `json:"log"`

	// The live overlay: the assigned agent's present, computed at read
	// time and never persisted (the json tags are for the wire).
	Live *taskLive `json:"live,omitempty"`
}

// taskLive is what the assigned agent is doing RIGHT NOW, from the
// same scan the rail reads.
type taskLive struct {
	Dir   string `json:"dir"`
	State string `json:"state"`
	Now   string `json:"now,omitempty"` // ⛭ tool — detail
}

func (t *task) event(actor, text string, at time.Time) {
	t.Log = append(t.Log, taskEvent{At: at, Actor: actor, Text: text})
	t.UpdatedAt = at
}

func (t *task) clearProposal() {
	t.Proposal, t.ProposalWhy, t.ProposalKind, t.ProposalText = "", "", "", ""
}

func (t *task) open() bool {
	return t.Col == "inbox" || t.Col == "progress" || t.Col == "waiting"
}

// maxExchanges bounds what a task file carries; the transcript is the
// full record.
const maxExchanges = 12

// workTools is the "work" mode's tool policy: edits plus the build-
// and-test commands a repo task needs. Deliberately no git mutation,
// no network, no package installs — those escalate.
var workTools = []string{
	"Edit", "Write", "MultiEdit",
	"Bash(go build:*)", "Bash(go test:*)", "Bash(go vet:*)", "Bash(gofmt:*)",
	"Bash(npm test:*)", "Bash(npm run build:*)", "Bash(make:*)",
}

// checkTools is "work" minus the teeth: the project's own build and
// test commands, no edits. It exists because a verify node pinned to
// pure read mode cannot run the tests it exists to run — print mode
// refuses gated tools, so the node would open, find it has no Bash, and
// report nothing. Three policies, not two.
//
// This is a real escalation over pure read — `go test` executes the
// repository's own code — and it is a narrower one than it looks: the
// node cannot change a byte, and it runs on ground the owner already
// pointed vera at. A verification that cannot verify is worse than no
// verification, because it reports success either way.
var checkTools = []string{
	"Bash(go build:*)", "Bash(go test:*)", "Bash(go vet:*)", "Bash(gofmt:*)",
	"Bash(npm test:*)", "Bash(npm run build:*)", "Bash(make:*)",
}

func toolsFor(mode string) []string {
	switch mode {
	case "work":
		return workTools
	case "check":
		return checkTools
	}
	return nil
}

type taskStore struct {
	dir string
	// One lock over the whole store: ids are max+1, adoption is
	// read-then-write, and the board mutates from HTTP handlers and
	// engine goroutines at once. The store is small; the lock is the
	// cheapest true thing.
	mu sync.Mutex
}

func defaultTasksDir() string {
	return statePath("vera-tasks")
}

func (st *taskStore) path(id string) (string, error) {
	if st.dir == "" {
		return "", errors.New("the board is off (no state directory)")
	}
	if !fileID(id) {
		return "", errBadID
	}
	return filepath.Join(st.dir, globalBoard, id+".json"), nil
}

func (st *taskStore) list() []task {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.listLocked()
}

func (st *taskStore) listLocked() []task {
	if st.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(st.dir, globalBoard))
	if err != nil {
		return nil
	}
	var out []task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		t, err := st.getLocked(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	// Pinned first, then recency — the draft's ordering rule.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func (st *taskStore) get(id string) (task, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.getLocked(id)
}

func (st *taskStore) getLocked(id string) (task, error) {
	path, err := st.path(id)
	if err != nil {
		return task{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return task{}, errors.New("that task is gone")
	}
	var t task
	if json.Unmarshal(b, &t) != nil {
		return task{}, errors.New("that task did not parse")
	}
	t.Live = nil // derived, never trusted from disk
	t.ScratchName = ""
	// Cards adopted before the flag existed still carry adopt's own
	// words; the flag is backfilled on read so they close like the rest.
	if !t.Adopted && strings.HasPrefix(t.Intent, "Adopted from the live session") {
		t.Adopted = true
	}
	return t, nil
}

// nextIDLocked numbers tasks globally: T-<max+1>, starting at T-100
// so ids read as ids and never as counts. Only meaningful under the
// lock — an id handed out unlocked is an id handed out twice.
func (st *taskStore) nextIDLocked() string {
	max := 99
	for _, t := range st.listLocked() {
		if n, err := strconv.Atoi(strings.TrimPrefix(t.ID, "T-")); err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("T-%d", max+1)
}

func (st *taskStore) write(t task) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.writeLocked(t)
}

// create assigns the next id and writes, atomically — the only safe
// way to mint a new card from outside the store.
func (st *taskStore) create(t task) (task, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	t.ID = st.nextIDLocked()
	return t, st.writeLocked(t)
}

func (st *taskStore) writeLocked(t task) error {
	path, err := st.path(t.ID)
	if err != nil {
		return err
	}
	t.Live, t.ScratchName = nil, "" // derived, never persisted
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// mutate runs f on one task under a fresh read and writes the result
// — one lock across the whole read-modify-write.
func (st *taskStore) mutate(id string, f func(*task) error) (task, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	t, err := st.getLocked(id)
	if err != nil {
		return task{}, err
	}
	if err := f(&t); err != nil {
		return task{}, err
	}
	return t, st.writeLocked(t)
}

// capture opens an UNASSIGNED task in the inbox — backlog for the
// next free agent. Vera spends nothing.
func (st *taskStore) capture(text string, now time.Time) (task, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	t := task{
		ID:    st.nextIDLocked(),
		Title: transcript.Snip(text, 90), Intent: text,
		Col: "inbox", State: "inbox · backlog",
		Face:         "Captured, unassigned. Vera has spent nothing on it yet.",
		Proposal:     "Start on the current agent",
		ProposalWhy:  "Nothing blocks it and no open task claims the same scope.",
		ProposalKind: "start",
		CreatedAt:    now, UpdatedAt: now,
	}
	t.event("human", "captured", now)
	return t, st.writeLocked(t)
}

// adopt records work that already exists: an agent is working right
// now with no open task assigned, so the board gains the in-progress
// card for it, titled with the agent's own title.
func (st *taskStore) adopt(root, title, dir string, now time.Time) (task, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	// Idempotent under the lock: two concurrent board reads deciding
	// to adopt the same agent must yield ONE card, not twins.
	for _, x := range st.listLocked() {
		if x.Agent == root && x.open() {
			return task{}, errors.New("already adopted")
		}
	}
	t := task{
		ID:    st.nextIDLocked(),
		Title: title,
		Intent: "Adopted from the live session in " + dir +
			" — the work this agent is already doing.",
		Agent:   root,
		Adopted: true,
		Col:     "progress", State: "in progress · live session",
		Face:      "The agent is working; the transcript is the record.",
		CreatedAt: now, UpdatedAt: now,
	}
	t.event("vera", "adopted from the live session ("+dir+")", now)
	return t, st.writeLocked(t)
}

// nearOpen names the merge-into candidate: the freshest live task a
// new capture could plausibly continue. Resolves toward NEW.
func nearOpen(tasks []task, excludeID string) *task {
	for i := range tasks {
		t := &tasks[i]
		if t.ID != excludeID && (t.Col == "progress" || t.Col == "waiting") {
			return t
		}
	}
	return nil
}

// ---- the board's view of the fleet ----

// boardSessions is the scan the board reads: non-fork roots wearing
// their head's live state, the same collapse the rail gets — filtered
// to the sessions vera can honestly claim. The machine is full of
// Claude sessions that are not vera's business; the board only sees
// its own: lineage family, task-assigned agents, and sessions working
// on registered ground. A sandbox world claims everything inside it —
// the world IS the claim.
func (s *server) boardSessions(now time.Time) map[string]*transcript.Session {
	scanned := s.sc.Scan(now)
	byID := map[string]*transcript.Session{}
	for i := range scanned {
		byID[scanned[i].ID] = &scanned[i]
	}
	claim := s.claimer()
	out := map[string]*transcript.Session{}
	for i := range scanned {
		t := &scanned[i]
		if s.ln.isFork(t.ID) || !claim(t) {
			continue
		}
		live := t
		if h := byID[s.ln.headOf(t.ID)]; h != nil {
			live = h
		}
		out[t.ID] = live
	}
	return out
}

// claimer builds this moment's ownership test, reading the ground
// registries once so the per-session check is cheap. Ownership is any
// of: vera's own lineage, an assignment already on a card, or a cwd
// under registered ground (a bookmark or a vera-made scratch
// workspace). Bookmarking a directory is how ground opts in.
func (s *server) claimer() func(*transcript.Session) bool {
	if worldRoot != "" {
		return func(*transcript.Session) bool { return true }
	}
	assigned := map[string]bool{}
	for _, t := range s.tasks.list() {
		if t.Agent != "" {
			assigned[t.Agent] = true
		}
	}
	var ground []string
	for _, b := range s.marks.list() {
		ground = append(ground, b.Cwd)
	}
	ground = append(ground, s.scratch.list()...)
	return func(t *transcript.Session) bool {
		return s.ln.knows(t.ID) || assigned[t.ID] || underAny(t.Cwd, ground)
	}
}

// registeredGround: is this a directory the owner has opted in — a
// bookmark, a vera scratch workspace, or anywhere inside a sandbox
// world? Autonomy only ever starts work on ground with a name.
func (s *server) registeredGround(cwd string) bool {
	if worldRoot != "" {
		return underAny(cwd, []string{worldRoot})
	}
	var ground []string
	for _, b := range s.marks.list() {
		ground = append(ground, b.Cwd)
	}
	ground = append(ground, s.scratch.list()...)
	return underAny(cwd, ground)
}

// underAny: is cwd one of the dirs, or inside one?
func underAny(cwd string, dirs []string) bool {
	for _, d := range dirs {
		if cwd == d || strings.HasPrefix(cwd, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// syncBoard adopts live work the board does not know yet: every
// WORKING agent with no assigned task — open OR closed — becomes one
// in-progress card. One agent, one card, ever: a session that works
// again after its card closed shows in the rail, not as a phantom
// duplicate on the board. Adopted cards close themselves when their
// session goes quiet.
func (s *server) syncBoard(tasks []task, fleet map[string]*transcript.Session, now time.Time) []task {
	assigned := map[string]bool{}
	// claimed marks the ground open cards are already working: a fresh
	// spawn takes its card's Agent only when the run ends, so mid-run
	// the newborn is a WORKING unassigned session in the card's own
	// workspace — that session is spoken for, not adoptable.
	claimed := map[string]bool{}
	for i := range tasks {
		if tasks[i].Agent != "" {
			assigned[tasks[i].Agent] = true
		}
		if tasks[i].open() && tasks[i].Workspace != "" {
			claimed[tasks[i].Workspace] = true
		}
	}
	for root, live := range fleet {
		// Adoption wants a WORKING agent with a conversation Claude
		// itself titled — an untitled "working" session is a probe (the
		// usage collector's `claude /usage -p` leaves those) or a
		// scratch run, not work the board should claim.
		if live.State != transcript.StateWorking || !live.Titled || assigned[root] || claimed[live.Cwd] {
			continue
		}
		if t, err := s.tasks.adopt(root, live.Title, filepath.Base(live.Cwd), now); err == nil {
			tasks = append([]task{t}, tasks...)
		}
	}
	// An adopted card mirrors a live session; when that session goes
	// quiet (idle ten minutes, or gone from the window entirely) the
	// mirror closes. needs-you and blocked? stay open — that work is
	// waiting on a human, not finished. Only cards still in progress
	// close themselves: a human who moved the card anywhere else took
	// over its workflow, and captured cards were never vera's to close.
	for i := range tasks {
		t := tasks[i]
		if !t.Adopted || t.Col != "progress" || t.Agent == "" {
			continue
		}
		if live := fleet[t.Agent]; live != nil && live.State != transcript.StateIdle {
			continue
		}
		nt, err := s.tasks.mutate(t.ID, func(x *task) error {
			if x.Col != "progress" {
				return errors.New("already moved")
			}
			x.Col = "done"
			x.State = "done · session went quiet"
			x.Face = "The session went quiet; the transcript is the record."
			x.clearProposal()
			x.event("vera", "the session went quiet — adopted card closed", now)
			return nil
		})
		if err == nil {
			tasks[i] = nt
		}
	}
	return tasks
}

// overlay dresses assigned tasks with their agent's present.
func overlay(tasks []task, fleet map[string]*transcript.Session) {
	for i := range tasks {
		t := &tasks[i]
		if t.Agent == "" || !t.open() {
			continue
		}
		live := fleet[t.Agent]
		if live == nil {
			continue
		}
		t.Live = liveOverlay(live)
	}
}

// liveOverlay is the assigned agent's present, as the board and the
// work view both read it. One function because two surfaces disagreeing
// about what a worker is doing right now is exactly the kind of drift
// nobody notices until it is confusing.
func liveOverlay(live *transcript.Session) *taskLive {
	if live == nil {
		return nil
	}
	l := &taskLive{Dir: filepath.Base(live.Cwd), State: string(live.State)}
	if live.ToolName != "" && (live.State == transcript.StateWorking || live.State == transcript.StateBlocked) {
		l.Now = live.ToolName
		if live.ToolDetail != "" {
			l.Now += " — " + live.ToolDetail
		}
	}
	return l
}

// ---- the routes ----

func (s *server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	b := s.boardData(time.Now())
	writeJSON(w, map[string]any{
		"tasks": b.tasks, "inflight": b.inflight, "spend": b.spend, "notice": s.notice,
		"fleet": map[string]int{"agents": b.agents, "working": b.working},
		"repos": b.repos,
	})
}

// boardPayload is one read of the whole board — what the REST list
// serves and what a WatchBoard frame carries.
type boardPayload struct {
	tasks    []task
	inflight int
	spend    float64
	agents   int
	working  int
	repos    []map[string]string
}

func (s *server) boardData(now time.Time) boardPayload {
	fleet := s.boardSessions(now)
	tasks := s.syncBoard(s.tasks.list(), fleet, now)
	if tasks == nil {
		tasks = []task{}
	}
	overlay(tasks, fleet)
	for i := range tasks {
		if tasks[i].Workspace != "" && s.scratch.has(tasks[i].Workspace) {
			tasks[i].ScratchName = filepath.Base(tasks[i].Workspace)
		}
	}

	working := 0
	for _, live := range fleet {
		if live.State == transcript.StateWorking {
			working++
		}
	}
	inflight := 0
	var spent float64
	s.mu.Lock()
	for _, rn := range s.runs {
		if !rn.Finished {
			inflight++
		}
	}
	for _, sp := range s.spend {
		spent += sp.ClaudeUSD + sp.JudgeUSD
	}
	s.mu.Unlock()
	return boardPayload{
		tasks: tasks, inflight: inflight, spend: spent,
		agents: len(fleet), working: working,
		repos: repoList(fleet, homeDir(), s.scratch.list(), s.marks.list()),
	}
}

// repoList names the directories a fresh agent could be born into —
// places the fleet has already shown, so the wire can never name a
// directory the machine did not first offer. The home directory is
// excluded: sessions there are usage probes and scratch, not repos.
func repoList(fleet map[string]*transcript.Session, home string, scratch []string, marks []bookmark) []map[string]string {
	seen := map[string]bool{}
	var out []map[string]string
	// Bookmarks first: the registry is the durable offer — named
	// ground that never ages out of the scan window.
	for _, bm := range marks {
		if bm.Cwd == "" || seen[bm.Cwd] {
			continue
		}
		seen[bm.Cwd] = true
		out = append(out, map[string]string{"dir": bm.Name, "cwd": bm.Cwd, "note": bm.Note, "bookmark": "yes"})
	}
	for _, live := range fleet {
		if live.Cwd == "" || live.Cwd == home || seen[live.Cwd] {
			continue
		}
		seen[live.Cwd] = true
		out = append(out, map[string]string{"dir": filepath.Base(live.Cwd), "cwd": live.Cwd})
	}
	// Scratch workspaces are offered even before any session exists in
	// them — vera made them, vera may staff them.
	for _, cwd := range scratch {
		if seen[cwd] {
			continue
		}
		seen[cwd] = true
		out = append(out, map[string]string{"dir": filepath.Base(cwd), "cwd": cwd, "scratch": "yes"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["dir"] < out[j]["dir"] })
	return out
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func (s *server) handleTaskCapture(w http.ResponseWriter, r *http.Request) {
	// A board mutation is a frame the watchers are owed.
	defer s.hub.notify()
	var req struct {
		Text string `json:"text"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		httpErr(w, 400, "say what needs doing")
		return
	}
	t, err := s.tasks.capture(req.Text, time.Now())
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	resp := map[string]any{"task": t}
	if near := nearOpen(s.tasks.list(), t.ID); near != nil {
		resp["near"] = map[string]string{"id": near.ID, "title": near.Title}
	}
	writeJSON(w, resp)
}

// handleTaskStart assigns an agent and drives: the task's own agent if
// it has one, the caller's choice if given, else the CURRENT agent —
// the one live session. Assignment is an event; nothing is implicit.
func (s *server) handleTaskStart(w http.ResponseWriter, r *http.Request) {
	// A board mutation is a frame the watchers are owed.
	defer s.hub.notify()
	if s.llm == nil {
		httpErr(w, 409, s.notice)
		return
	}
	var req struct {
		AgentID   string  `json:"agentId"`
		NewIn     string  `json:"newIn"`     // birth a fresh agent in this repo
		Mode      string  `json:"mode"`      // "" or "read" | "work"
		BudgetUSD float64 `json:"budgetUsd"` // > 0 = autopilot: read-only, driver continues runs until spent
	}
	// The body is optional; absence means "vera picks".
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&req)

	now := time.Now()
	t, err := s.tasks.get(r.PathValue("tid"))
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	if t.Col == "progress" {
		httpErr(w, 409, "already in progress")
		return
	}
	mode := req.Mode
	if mode != "work" {
		mode = "read"
	}
	// The marathon tier is read-only by construction: a budget large
	// enough to run for hours must not be a budget for unattended
	// edits. Cap what one card can be authorized for.
	if req.BudgetUSD > 0 {
		mode = "read"
		if req.BudgetUSD > 200 {
			httpErr(w, 400, "an autopilot budget caps at $200 per card")
			return
		}
		t, err = s.tasks.mutate(t.ID, func(t *task) error {
			t.BudgetUSD = req.BudgetUSD
			t.event("human", fmt.Sprintf("autopilot: authorized $%.2f — read-only; vera continues runs until spent, judged done, or circling", req.BudgetUSD), now)
			return nil
		})
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
	}
	if req.NewIn != "" {
		s.startTaskFresh(w, r, t, req.NewIn, mode, now)
		return
	}
	agentID := t.Agent
	if req.AgentID != "" {
		agentID = req.AgentID
	}
	if agentID == "" {
		agentID = s.currentAgent(now)
	}
	if agentID == "" {
		httpErr(w, 409, "no agent to start on — run claude somewhere first")
		return
	}
	root, head := s.resolveAgent(agentID, now)
	if head == nil {
		httpErr(w, 404, "that agent is gone from the window")
		return
	}
	s.mu.Lock()
	busy := s.drivingLocked(root)
	s.mu.Unlock()
	if busy {
		httpErr(w, 409, "that agent already has a run in flight — one task drives at a time")
		return
	}
	goal, err := s.rootLLM(root, partCompile).CompileGoal(r.Context(), t.Intent)
	if err != nil {
		httpErr(w, 502, "vera could not compile the goal: "+err.Error())
		return
	}
	dir := filepath.Base(head.Cwd)
	t, err = s.tasks.mutate(t.ID, func(t *task) error {
		t.Agent = root
		t.Mode = mode
		t.Workspace = head.Cwd
		t.Goal, t.GoalActor = goal, "vera"
		t.Col, t.State = "progress", "in progress · turn in flight"
		t.Face = "Started. The first turn is in flight."
		t.clearProposal()
		// The owner started it: fresh retry budget.
		t.Retries, t.StopErr, t.StopTransient = 0, "", false
		t.event("vera", "assigned to "+dir+" ("+shortID(root)+") · mode "+mode, now)
		t.event("vera", "compiled intent → drive goal, started against "+shortID(head.ID), now)
		return nil
	})
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	s.startTaskDrive(root, head, t.ID, goal, mode, "", nil)
	writeJSON(w, t)
}

// handleTaskReply is the escalation's return path: the owner answers a
// waiting card and the SAME drive continues — reply as the next
// prompt, the recorded exchanges as the seed, the judge still judging
// against the original goal.
func (s *server) handleTaskReply(w http.ResponseWriter, r *http.Request) {
	// A board mutation is a frame the watchers are owed.
	defer s.hub.notify()
	if s.llm == nil {
		httpErr(w, 409, s.notice)
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		httpErr(w, 400, "say something")
		return
	}
	t, serr := s.taskReply(r.PathValue("tid"), req.Text, "replied", time.Now())
	if serr != nil {
		httpErr(w, serr.code, serr.msg)
		return
	}
	writeJSON(w, t)
}

// taskReply is the transport-neutral core the REST reply and an
// accepted drafted answer both ride: validate, seed, continue the
// same drive. eventVerb names how the words arrived ("replied" —
// typed; "accepted vera's drafted reply" — one tap on the proposal).
func (s *server) taskReply(tid, text, eventVerb string, now time.Time) (task, *sayErr) {
	if s.llm == nil {
		return task{}, &sayErr{409, s.notice}
	}
	t, err := s.tasks.get(tid)
	if err != nil {
		return task{}, &sayErr{404, err.Error()}
	}
	if t.Col != "waiting" {
		return task{}, &sayErr{409, "only a waiting task takes a reply"}
	}
	if t.Agent == "" {
		return task{}, &sayErr{409, "this task has no agent yet — start it instead"}
	}
	root, head := s.resolveAgent(t.Agent, now)
	if head == nil {
		return task{}, &sayErr{404, "the task's agent is gone from the window"}
	}
	s.mu.Lock()
	busy := s.drivingLocked(root)
	s.mu.Unlock()
	if busy {
		return task{}, &sayErr{409, "that agent already has a run in flight"}
	}
	goal := t.Goal
	if goal == "" {
		goal = t.Intent
	}
	t, err = s.tasks.mutate(t.ID, func(t *task) error {
		t.Col, t.State = "progress", "in progress · continuing on your answer"
		t.Ask = ""
		t.Face = "Continuing: " + transcript.Snip(text, 100)
		t.clearProposal()
		// The owner touched the card: fresh retry budget.
		t.Retries, t.StopErr, t.StopTransient = 0, "", false
		t.event("human", eventVerb+" — "+transcript.Snip(text, 100), now)
		return nil
	})
	if err != nil {
		return task{}, &sayErr{500, err.Error()}
	}
	s.startTaskDrive(root, head, t.ID, goal, t.Mode, text, t.Exchanges)
	return t, nil
}

func shortID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

// startTaskFresh births a new agent for the task: a fresh headless
// claude in the chosen repo, the goal as its first breath. The repo
// must be one the fleet already showed — the wire can only name what
// the machine offered.
func (s *server) startTaskFresh(w http.ResponseWriter, r *http.Request, t task, newIn, mode string, now time.Time) {
	fleet := s.boardSessions(now)
	if s.repoOffered(fleet, newIn) == "" {
		httpErr(w, 400, "that directory is not one the fleet has shown")
		return
	}
	// Costs accrue against the newborn, whose name we learn when it
	// takes its first breath; until then the meter holds its coins.
	var judgeUSD float64
	var judgeMu sync.Mutex
	ll := *s.llmFor(partCompile)
	ll.Spend = func(c float64) { judgeMu.Lock(); judgeUSD += c; judgeMu.Unlock() }

	goal, err := ll.CompileGoal(r.Context(), t.Intent)
	if err != nil {
		httpErr(w, 502, "vera could not compile the goal: "+err.Error())
		return
	}
	judgeMu.Lock()
	held := judgeUSD
	judgeMu.Unlock()
	dir := filepath.Base(newIn)
	t, err = s.tasks.mutate(t.ID, func(t *task) error {
		t.Mode = mode
		t.Workspace = newIn
		t.Goal, t.GoalActor = goal, "vera"
		t.Col, t.State = "progress", "in progress · a fresh agent is being born"
		t.Face = "Starting a fresh agent in " + dir + "."
		t.clearProposal()
		t.event("vera", "compiled intent → drive goal; starting a fresh agent in "+dir, now)
		return nil
	})
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	s.spawnFresh(t, newIn, mode, goal, held)
	writeJSON(w, t)
}

// spawnFresh births a worker in newIn and drives it toward goal. The
// task must already wear its progress state; heldUSD is vera-spend
// paid on this task's behalf before the newborn had a name (compile,
// planning) — credited to it when it takes its first breath.
func (s *server) spawnFresh(t task, newIn, mode, goal string, heldUSD float64) {
	judgeUSD := heldUSD
	var judgeMu sync.Mutex
	ll := *s.llmFor(partJudge)
	dir := filepath.Base(newIn)

	ctx, cancel := context.WithCancel(context.Background())
	idb := make([]byte, 4)
	rand.Read(idb)
	rn := &run{
		ID:           "drive-" + hex.EncodeToString(idb),
		SessionTitle: "fresh agent in " + dir, Goal: goal, TaskID: t.ID,
		Status: "starting", At: time.Now(), cancel: cancel,
	}
	s.mu.Lock()
	s.runs = append([]*run{rn}, s.runs...)
	s.mu.Unlock()

	jl := ll // the run's own copy; same held meter
	jl.Spend = func(c float64) {
		judgeMu.Lock()
		judgeUSD += c
		judgeMu.Unlock()
		s.update(rn.ID, func(r *run) { r.JudgeUSD += c })
	}
	// The worker is matched to the node it runs: a verify reads command
	// output, an implement writes the code everything else checks. An
	// empty model says nothing and the CLI's own default stands.
	worker := s.route.forKind(t.Kind)
	turner := &drive.Headless{Bin: s.claudeBin, Dir: newIn, Model: worker,
		AllowedTools: toolsFor(mode)}
	if note := routeNote(t.Kind, worker); note != "" {
		s.tasks.mutate(t.ID, func(t *task) error {
			t.Model = worker
			t.event("vera", note, time.Now())
			return nil
		})
		s.events.emit(evNodeMoved, goalOf(t), t.ID, "Vera "+note+".")
	}
	loop := &drive.Loop{
		Turner:   turner,
		Judge:    &drive.LLMJudge{LLM: &jl},
		MaxTurns: s.turns,
		Progress: func(line string) { s.update(rn.ID, func(r *run) { r.Status = line; r.At = time.Now() }) },
		OnTurn:   s.auditTurn(t.ID),
	}
	if t.BudgetUSD > 0 {
		turner.Timeout = 30 * time.Minute
		if rem := t.BudgetUSD - t.CostUSD; rem < 5 {
			loop.MaxUSD = max(rem, 0.5)
		}
	}
	taskID := t.ID
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer cancel()
		res, err := loop.RunFresh(ctx, goal)
		judgeMu.Lock()
		held := judgeUSD
		judgeMu.Unlock()
		if res.Root != "" {
			s.ln.advance(res.Root, res.SessionID)
			s.addSpend(res.Root, res.CostUSD, held)
		}
		s.update(rn.ID, func(r *run) {
			r.Finished = true
			r.At = time.Now()
			r.SessionID = res.Root
			r.Turns = res.Turns
			r.ResumeID = res.SessionID
			r.ClaudeUSD = res.CostUSD
			if err != nil {
				r.Reason = err.Error()
				return
			}
			r.Done = res.Done
			r.Reason = res.Reason
		})
		// The newborn takes the assignment before the outcome is
		// folded, so the card names its agent however the run ended.
		if res.Root != "" {
			s.tasks.mutate(taskID, func(t *task) error {
				t.Agent = res.Root
				t.event("vera", "assigned to the newborn agent in "+dir+" ("+shortID(res.Root)+")", time.Now())
				return nil
			})
		}
		s.taskRunLanded(taskID, res, err, 0)
	}()
}

// repoOffered answers with the fleet cwd matching the ask, or "" —
// the same offer repoList makes, checked on the way back in.
func (s *server) repoOffered(fleet map[string]*transcript.Session, cwd string) string {
	for _, r := range repoList(fleet, homeDir(), s.scratch.list(), s.marks.list()) {
		if r["cwd"] == cwd {
			return cwd
		}
	}
	return ""
}

// currentAgent is the same freshest-lineage answer /api/state gives.
func (s *server) currentAgent(now time.Time) string {
	best, bestAt := "", time.Time{}
	for root, live := range s.boardSessions(now) {
		if live.Mtime.After(bestAt) {
			best, bestAt = root, live.Mtime
		}
	}
	return best
}

// auditTurn is the observability feed: every automatic decision the
// judge makes lands on the task's log as it is made, not just at the
// end — the board must be auditable while the machine is still moving.
func (s *server) auditTurn(taskID string) func(int, drive.Exchange, drive.Verdict) {
	return func(turn int, ex drive.Exchange, v drive.Verdict) {
		text := ""
		switch {
		case v.Done:
			text = fmt.Sprintf("turn %d — judged the goal met", turn)
		case v.Escalate:
			text = fmt.Sprintf("turn %d — escalating: %s", turn, transcript.Snip(v.Reason, 70))
		default:
			text = fmt.Sprintf("turn %d — answered the worker: %s", turn, transcript.Snip(v.Prompt, 70))
		}
		now := time.Now()
		s.tasks.mutate(taskID, func(t *task) error {
			t.event("vera", text, now)
			return nil
		})
	}
}

// startTaskDrive is the drive path the board rides: same loop, same
// judge, plus the task bookkeeping when it lands. A non-empty reply
// continues an escalated drive (seeded with the task's exchanges)
// instead of opening with the goal.
func (s *server) startTaskDrive(root string, head *transcript.Session, taskID, goal, mode, reply string, seed []drive.Exchange) {
	ctx, cancel := context.WithCancel(context.Background())
	idb := make([]byte, 4)
	rand.Read(idb)
	rn := &run{
		ID: "drive-" + hex.EncodeToString(idb), SessionID: root,
		SessionTitle: head.Title, Goal: goal, TaskID: taskID,
		Status: "starting", At: time.Now(), cancel: cancel,
	}
	s.mu.Lock()
	s.runs = append([]*run{rn}, s.runs...)
	s.mu.Unlock()

	ll := *s.llmFor(partJudge)
	ll.Spend = func(c float64) {
		s.update(rn.ID, func(r *run) { r.JudgeUSD += c })
		s.addSpend(root, 0, c)
	}
	// A continued drive routes by the same node kind the first turn
	// did: switching models mid-conversation would re-read the whole
	// history on a stranger and pay for the privilege.
	kind := ""
	if bt, err := s.tasks.get(taskID); err == nil {
		kind = bt.Kind
	}
	turner := &drive.Headless{Bin: s.claudeBin, Dir: head.Cwd,
		Model: s.route.forKind(kind), AllowedTools: toolsFor(mode)}
	loop := &drive.Loop{
		Turner:   turner,
		Judge:    &drive.LLMJudge{LLM: &ll},
		MaxTurns: s.turns,
		Progress: func(line string) { s.update(rn.ID, func(r *run) { r.Status = line; r.At = time.Now() }) },
		OnTurn:   s.auditTurn(taskID),
	}
	// An autopilot run never overshoots what remains of the owner's
	// budget: the per-run cap shrinks to the remainder. And marathon
	// turns get a marathon clock — a deep read of a codebase is
	// honestly longer than the 10m default.
	if bt, err := s.tasks.get(taskID); err == nil && bt.BudgetUSD > 0 {
		turner.Timeout = 30 * time.Minute
		if rem := bt.BudgetUSD - bt.CostUSD; rem < 5 {
			loop.MaxUSD = max(rem, 0.5)
		}
	}
	headID := head.ID
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer cancel()
		var res drive.Result
		var err error
		if reply != "" {
			res, err = loop.Continue(ctx, headID, goal, reply, seed)
		} else {
			res, err = loop.Run(ctx, headID, goal)
		}
		s.ln.advance(root, res.SessionID)
		s.addSpend(root, res.CostUSD, 0)
		s.update(rn.ID, func(r *run) {
			r.Finished = true
			r.At = time.Now()
			r.Turns = res.Turns
			r.ResumeID = res.SessionID
			r.ClaudeUSD = res.CostUSD
			if err != nil {
				r.Reason = err.Error()
				return
			}
			r.Done = res.Done
			r.Reason = res.Reason
		})
		s.taskRunLanded(taskID, res, err, len(seed))
	}()
}

// taskRunLanded folds a finished run back onto its task: done becomes
// a proposal (irreversible transitions are the human's), anything else
// becomes waiting with the reason as the ask. seedLen names how many
// of res.Turns were already on the task's record before this run.
func (s *server) taskRunLanded(taskID string, res drive.Result, runErr error, seedLen int) {
	now := time.Now()
	cost := res.CostUSD
	newTurns := res.Turns
	if seedLen <= len(newTurns) {
		newTurns = newTurns[seedLen:]
	}
	s.tasks.mutate(taskID, func(t *task) error {
		// The working set the next reply seeds from.
		t.Exchanges = append(t.Exchanges, newTurns...)
		if len(t.Exchanges) > maxExchanges {
			t.Exchanges = t.Exchanges[len(t.Exchanges)-maxExchanges:]
		}
		outcome := fmt.Sprintf("%d turns", len(newTurns))
		t.StopErr, t.StopTransient = "", false
		switch {
		case runErr != nil:
			t.StopReason = "error"
			outcome += ", stopped: " + transcript.Snip(runErr.Error(), 60)
			t.Col, t.State = "waiting", "waiting · the run stopped"
			t.Ask = "The run stopped: " + runErr.Error() + " — start again, or drop?"
			t.Face = "The run stopped before the goal was met."
			// The stop record: the engine reads this to decide whether
			// the death was machinery (retry) or judgment (yours).
			t.StopErr, t.StopTransient = runErr.Error(), drive.IsTransient(runErr)
			t.event("vera", "run stopped: "+transcript.Snip(runErr.Error(), 80), now)
		case res.Escalated:
			t.StopReason = "escalated"
			if res.Circled {
				t.StopReason = "circling"
			} else if res.Capped {
				t.StopReason = "spend-cap"
			}
			outcome += ", escalated"
			t.Col, t.State = "waiting", "waiting · escalated to you"
			t.Ask = res.Ask
			t.Face = "Vera escalated: " + transcript.Snip(res.Ask, 120)
			t.event("vera", "escalated — "+transcript.Snip(res.Ask, 100), now)
		case res.Done:
			t.StopReason = "done"
			outcome += ", judge said DONE"
			t.Col, t.State = "waiting", "waiting for acceptance"
			t.Ask = ""
			t.Face = "Judged done: " + transcript.Snip(res.Reason, 120)
			t.Proposal, t.ProposalWhy, t.ProposalKind = "Move to done",
				"Judge returned DONE: "+res.Reason+" Irreversible — yours to confirm.", "done"
			t.event("vera", "judged done, proposed acceptance", now)
		default:
			t.StopReason = "turns"
			outcome += ", budget spent"
			t.Col, t.State = "waiting", "waiting · budget spent"
			t.Ask = res.Reason + " — start another run, or drop?"
			t.Face = "The turn budget ran out before the judge was satisfied."
			t.event("vera", "budget spent without DONE", now)
		}
		if runErr == nil {
			// The machinery completed a whole run; a later transient
			// death starts from a fresh retry budget.
			t.Retries = 0
		}
		if n := len(res.Turns); n > 0 {
			t.event("worker", fmt.Sprintf("%d turn(s) landed", n), now)
		}
		t.Runs = append(t.Runs, taskRun{Kind: "drive", Outcome: outcome, CostUSD: cost})
		t.CostUSD += cost
		return nil
	})
}

// rearmStanding lays the next pass of a standing need: same intent,
// same ground, a fresh unassigned card in the inbox — spending
// nothing until its moment comes (the steward's queue, the owner's
// start, or a deadline the next plan names).
func (s *server) rearmStanding(prev task, now time.Time) {
	st := task{
		Title: prev.Title, Intent: prev.Intent,
		Workspace: prev.Workspace, Mode: prev.Mode, Cadence: "standing",
		Col: "inbox", State: "inbox · standing",
		Face:      "A standing need — the last pass (" + prev.ID + ") was accepted; this is the next.",
		CreatedAt: now, UpdatedAt: now,
	}
	st.event("vera", "standing need re-armed after "+prev.ID+" was accepted", now)
	s.tasks.create(st)
}

// handleTaskAct is the small verbs: accept, decline, drop, pin, merge.
func (s *server) handleTaskAct(w http.ResponseWriter, r *http.Request) {
	// A board mutation is a frame the watchers are owed.
	defer s.hub.notify()
	var req struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
		IntoID string `json:"intoId"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	now := time.Now()
	tid := r.PathValue("tid")
	switch req.Action {
	case "accept":
		t, err := s.tasks.get(tid)
		if err != nil {
			httpErr(w, 404, err.Error())
			return
		}
		if t.ProposalKind == "start" {
			s.handleTaskStart(w, r)
			return
		}
		// A drafted answer: accepting sends exactly the text the card
		// showed, and the same drive continues on it.
		if t.ProposalKind == "reply" {
			if strings.TrimSpace(t.ProposalText) == "" {
				httpErr(w, 409, "the drafted reply is empty — answer yourself instead")
				return
			}
			nt, serr := s.taskReply(tid, t.ProposalText, "accepted vera's drafted reply", now)
			if serr != nil {
				httpErr(w, serr.code, serr.msg)
				return
			}
			writeJSON(w, nt)
			return
		}
		if t.ProposalKind != "done" {
			httpErr(w, 409, "nothing proposed to accept")
			return
		}
		t, err = s.tasks.mutate(tid, func(t *task) error {
			t.Col, t.State = "done", "done · accepted by human"
			t.Face = "Accepted by you. Closed with the log intact."
			t.Ask = ""
			t.clearProposal()
			t.event("human", "accepted as done", now)
			return nil
		})
		if err == nil {
			s.recordOutcome(t, true, now)
		}
		// Accepting a plan-step card is the nod for the piece after it.
		if err == nil && t.NextID != "" {
			s.chainNext(t.NextID, t.Mode, now)
		}
		// A standing need outlives any one pass: acceptance closes THIS
		// card and lays the next, so the board keeps saying so.
		if err == nil && t.Cadence == "standing" {
			s.rearmStanding(t, now)
		}
		respond(w, t, err)
	case "decline":
		t, err := s.tasks.mutate(tid, func(t *task) error {
			t.clearProposal()
			t.event("human", "declined the proposal for now", now)
			return nil
		})
		respond(w, t, err)
	case "hold":
		// The veto on a queued self-start: the mark clears, nothing
		// spends, and the card stays exactly where it was. The steward
		// may argue again on a later pass; the owner may hold again.
		t, err := s.tasks.mutate(tid, func(t *task) error {
			if t.AutoStart == "" {
				return errors.New("nothing queued to hold")
			}
			t.AutoStart = ""
			t.State = "inbox · backlog"
			t.event("human", "held — vera's queued start canceled", now)
			return nil
		})
		respond(w, t, err)
	case "drop":
		reason := strings.TrimSpace(req.Reason)
		if reason == "" {
			reason = "closed without completion"
		}
		t, err := s.tasks.mutate(tid, func(t *task) error {
			t.Col, t.State = "dropped", "dropped · reason on the record"
			t.Face = "Dropped: " + reason
			t.Ask = ""
			t.clearProposal()
			t.event("human", "dropped — "+reason, now)
			return nil
		})
		if err == nil {
			s.recordOutcome(t, false, now)
		}
		respond(w, t, err)
	case "pin":
		t, err := s.tasks.mutate(tid, func(t *task) error {
			t.Pinned = !t.Pinned
			t.event("human", map[bool]string{true: "pinned", false: "unpinned"}[t.Pinned], now)
			return nil
		})
		respond(w, t, err)
	case "merge":
		src, err := s.tasks.get(tid)
		if err != nil {
			httpErr(w, 404, err.Error())
			return
		}
		if _, err := s.tasks.mutate(req.IntoID, func(t *task) error {
			t.Intent = t.Intent + "\n\n(merged from " + src.ID + ") " + src.Intent
			t.event("human", "absorbed "+src.ID+" — "+transcript.Snip(src.Intent, 80), now)
			return nil
		}); err != nil {
			httpErr(w, 404, "merge target: "+err.Error())
			return
		}
		t, err := s.tasks.mutate(tid, func(t *task) error {
			t.Col, t.State = "dropped", "merged into "+req.IntoID
			t.Face = "Merged into " + req.IntoID + "; its log carries the intent now."
			t.clearProposal()
			t.event("human", "merged into "+req.IntoID, now)
			return nil
		})
		respond(w, t, err)
	default:
		httpErr(w, 400, "no such action: "+req.Action)
	}
}

func respond(w http.ResponseWriter, t task, err error) {
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	writeJSON(w, t)
}
