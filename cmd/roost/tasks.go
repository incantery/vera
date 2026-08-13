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
//     unassigned, and rook spends nothing.
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
// under <state>/rook/roost-tasks/global/.
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
	"time"

	"crypto/rand"
	"encoding/hex"

	"github.com/incantery/rook-host/engine/drive"
	"github.com/incantery/rook-host/engine/transcript"
)

// globalBoard is the one namespace tasks live in. (Earlier builds kept
// a directory per agent; those directories are simply no longer read.)
const globalBoard = "global"

type taskEvent struct {
	At    time.Time `json:"at"`
	Actor string    `json:"actor"` // human | rook | worker
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
	// The goal the intent compiles to when the task starts — rook's
	// words, on the record beside the owner's.
	Goal      string `json:"goal,omitempty"`
	GoalActor string `json:"goalActor,omitempty"`

	Col   string `json:"col"`   // inbox | progress | waiting | done | dropped
	State string `json:"state"` // the human-readable line under the column
	Ask   string `json:"ask,omitempty"`
	Face  string `json:"face,omitempty"` // the card's one-line story

	Pinned bool `json:"pinned,omitempty"`

	// Rook proposes, the human disposes.
	Proposal     string `json:"proposal,omitempty"`
	ProposalWhy  string `json:"proposalWhy,omitempty"`
	ProposalKind string `json:"proposalKind,omitempty"` // "start" | "done"

	Runs    []taskRun `json:"runs,omitempty"`
	CostUSD float64   `json:"costUsd,omitempty"`

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
	t.Proposal, t.ProposalWhy, t.ProposalKind = "", "", ""
}

func (t *task) open() bool {
	return t.Col == "inbox" || t.Col == "progress" || t.Col == "waiting"
}

type taskStore struct {
	dir string
}

func defaultTasksDir() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			return ""
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "rook", "roost-tasks")
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
		t, err := st.get(strings.TrimSuffix(e.Name(), ".json"))
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
	t.Live = nil // the overlay never survives a write; never trust one from disk
	return t, nil
}

// nextID numbers tasks globally: T-<max+1>, starting at T-100 so ids
// read as ids and never as counts.
func (st *taskStore) nextID() string {
	max := 99
	for _, t := range st.list() {
		if n, err := strconv.Atoi(strings.TrimPrefix(t.ID, "T-")); err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("T-%d", max+1)
}

func (st *taskStore) write(t task) error {
	path, err := st.path(t.ID)
	if err != nil {
		return err
	}
	t.Live = nil // derived, never persisted
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

// mutate runs f on one task under a fresh read and writes the result.
func (st *taskStore) mutate(id string, f func(*task) error) (task, error) {
	t, err := st.get(id)
	if err != nil {
		return task{}, err
	}
	if err := f(&t); err != nil {
		return task{}, err
	}
	return t, st.write(t)
}

// capture opens an UNASSIGNED task in the inbox — backlog for the
// next free agent. Rook spends nothing.
func (st *taskStore) capture(text string, now time.Time) (task, error) {
	t := task{
		ID:    st.nextID(),
		Title: transcript.Snip(text, 90), Intent: text,
		Col: "inbox", State: "inbox · backlog",
		Face:         "Captured, unassigned. Rook has spent nothing on it yet.",
		Proposal:     "Start on the current agent",
		ProposalWhy:  "Nothing blocks it and no open task claims the same scope.",
		ProposalKind: "start",
		CreatedAt:    now, UpdatedAt: now,
	}
	t.event("human", "captured", now)
	return t, st.write(t)
}

// adopt records work that already exists: an agent is working right
// now with no open task assigned, so the board gains the in-progress
// card for it, titled with the agent's own title.
func (st *taskStore) adopt(root, title, dir string, now time.Time) (task, error) {
	t := task{
		ID:    st.nextID(),
		Title: title,
		Intent: "Adopted from the live session in " + dir +
			" — the work this agent is already doing.",
		Agent: root,
		Col:   "progress", State: "in progress · live session",
		Face:      "The agent is working; the transcript is the record.",
		CreatedAt: now, UpdatedAt: now,
	}
	t.event("rook", "adopted from the live session ("+dir+")", now)
	return t, st.write(t)
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
// their head's live state, the same collapse the rail gets.
func (s *server) boardSessions(now time.Time) map[string]*transcript.Session {
	scanned := s.sc.Scan(now)
	byID := map[string]*transcript.Session{}
	for i := range scanned {
		byID[scanned[i].ID] = &scanned[i]
	}
	out := map[string]*transcript.Session{}
	for i := range scanned {
		t := &scanned[i]
		if s.ln.isFork(t.ID) {
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

// syncBoard adopts live work the board does not know yet: every
// WORKING agent with no open assigned task becomes one in-progress
// card. One agent, one task — an agent already carrying an open task
// adopts nothing.
func (s *server) syncBoard(tasks []task, fleet map[string]*transcript.Session, now time.Time) []task {
	assigned := map[string]bool{}
	for i := range tasks {
		if tasks[i].open() && tasks[i].Agent != "" {
			assigned[tasks[i].Agent] = true
		}
	}
	for root, live := range fleet {
		// Adoption wants a WORKING agent with a conversation Claude
		// itself titled — an untitled "working" session is a probe (the
		// usage collector's `claude /usage -p` leaves those) or a
		// scratch run, not work the board should claim.
		if live.State != transcript.StateWorking || !live.Titled || assigned[root] {
			continue
		}
		if t, err := s.tasks.adopt(root, live.Title, filepath.Base(live.Cwd), now); err == nil {
			tasks = append([]task{t}, tasks...)
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
		l := &taskLive{Dir: filepath.Base(live.Cwd), State: string(live.State)}
		if live.ToolName != "" && (live.State == transcript.StateWorking || live.State == transcript.StateBlocked) {
			l.Now = live.ToolName
			if live.ToolDetail != "" {
				l.Now += " — " + live.ToolDetail
			}
		}
		t.Live = l
	}
}

// ---- the routes ----

func (s *server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	fleet := s.boardSessions(now)
	tasks := s.syncBoard(s.tasks.list(), fleet, now)
	if tasks == nil {
		tasks = []task{}
	}
	overlay(tasks, fleet)

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
	writeJSON(w, map[string]any{
		"tasks": tasks, "inflight": inflight, "spend": spent, "notice": s.notice,
		"fleet": map[string]int{"agents": len(fleet), "working": working},
	})
}

func (s *server) handleTaskCapture(w http.ResponseWriter, r *http.Request) {
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
	if s.llm == nil {
		httpErr(w, 409, s.notice)
		return
	}
	var req struct {
		AgentID string `json:"agentId"`
	}
	// The body is optional; absence means "rook picks".
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
	goal, err := s.rootLLM(root).CompileGoal(r.Context(), t.Intent)
	if err != nil {
		httpErr(w, 502, "rook could not compile the goal: "+err.Error())
		return
	}
	dir := filepath.Base(head.Cwd)
	t, err = s.tasks.mutate(t.ID, func(t *task) error {
		t.Agent = root
		t.Goal, t.GoalActor = goal, "rook"
		t.Col, t.State = "progress", "in progress · turn in flight"
		t.Face = "Started. The first turn is in flight."
		t.clearProposal()
		t.event("rook", "assigned to "+dir+" ("+shortID(root)+")", now)
		t.event("rook", "compiled intent → drive goal, started against "+shortID(head.ID), now)
		return nil
	})
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	s.startTaskDrive(root, head, t.ID, goal)
	writeJSON(w, t)
}

func shortID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
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

// startTaskDrive is the drive path the board rides: same loop, same
// judge, plus the task bookkeeping when it lands.
func (s *server) startTaskDrive(root string, head *transcript.Session, taskID, goal string) {
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

	ll := *s.llm
	ll.Spend = func(c float64) {
		s.update(rn.ID, func(r *run) { r.JudgeUSD += c })
		s.addSpend(root, 0, c)
	}
	loop := &drive.Loop{
		Turner:   &drive.Headless{Bin: s.claudeBin, Dir: head.Cwd},
		Judge:    &drive.LLMJudge{LLM: &ll},
		MaxTurns: s.turns,
		Progress: func(line string) { s.update(rn.ID, func(r *run) { r.Status = line; r.At = time.Now() }) },
	}
	headID := head.ID
	go func() {
		defer cancel()
		res, err := loop.Run(ctx, headID, goal)
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
		s.taskRunLanded(taskID, res, err)
	}()
}

// taskRunLanded folds a finished run back onto its task: done becomes
// a proposal (irreversible transitions are the human's), anything else
// becomes waiting with the reason as the ask.
func (s *server) taskRunLanded(taskID string, res drive.Result, runErr error) {
	now := time.Now()
	cost := res.CostUSD
	s.tasks.mutate(taskID, func(t *task) error {
		outcome := fmt.Sprintf("%d turns", len(res.Turns))
		switch {
		case runErr != nil:
			outcome += ", stopped: " + transcript.Snip(runErr.Error(), 60)
			t.Col, t.State = "waiting", "waiting · the run stopped"
			t.Ask = "The run stopped: " + runErr.Error() + " — start again, or drop?"
			t.Face = "The run stopped before the goal was met."
			t.event("rook", "run stopped: "+transcript.Snip(runErr.Error(), 80), now)
		case res.Done:
			outcome += ", judge said DONE"
			t.Col, t.State = "waiting", "waiting for acceptance"
			t.Ask = ""
			t.Face = "Judged done: " + transcript.Snip(res.Reason, 120)
			t.Proposal, t.ProposalWhy, t.ProposalKind = "Move to done",
				"Judge returned DONE: "+res.Reason+" Irreversible — yours to confirm.", "done"
			t.event("rook", "judged done, proposed acceptance", now)
		default:
			outcome += ", budget spent"
			t.Col, t.State = "waiting", "waiting · budget spent"
			t.Ask = res.Reason + " — start another run, or drop?"
			t.Face = "The turn budget ran out before the judge was satisfied."
			t.event("rook", "budget spent without DONE", now)
		}
		if n := len(res.Turns); n > 0 {
			t.event("worker", fmt.Sprintf("%d turn(s) landed", n), now)
		}
		t.Runs = append(t.Runs, taskRun{Kind: "drive", Outcome: outcome, CostUSD: cost})
		t.CostUSD += cost
		return nil
	})
}

// handleTaskAct is the small verbs: accept, decline, drop, pin, merge.
func (s *server) handleTaskAct(w http.ResponseWriter, r *http.Request) {
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
		respond(w, t, err)
	case "decline":
		t, err := s.tasks.mutate(tid, func(t *task) error {
			t.clearProposal()
			t.event("human", "declined the proposal for now", now)
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
