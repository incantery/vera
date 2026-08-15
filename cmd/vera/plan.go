// The planning surface: the owner tells vera what they need in plain
// words; vera answers with a plan — where the work should live, its
// cadence, and the goal a worker would be handed. The plan is a bid
// the owner nods at before anything exists; the nod-or-edit is
// journaled, the second learning substrate after suggest's pick-vs-own.
//
// Every workspace vera creates is a git repo, code or not: the review
// rail, the board, and the digests already work on git — a meal plan's
// diff reviews exactly like Go.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/incantery/vera/drive"
	"github.com/incantery/vera/transcript"
)

// planGen salts the plan journal so a prompt change reads as a new
// generation in later analysis. Bump on any planSysPrompt change.
const planGen = "p6|"

func defaultPlanPath() string {
	return statePath("vera-plans.jsonl")
}

// planRec is one served bid, held in memory so the nod can credit the
// planning spend to the newborn agent.
type planRec struct {
	ID   string     `json:"id"`
	Ask  string     `json:"ask"`
	Plan drive.Plan `json:"plan"`
	USD  float64    `json:"-"`
	Done bool       `json:"-"` // executed — a bid is spent once
}

// planLine is the journal shape: every bid served, and every bid the
// owner executed, on the record for the future picked-vs-reshaped
// comparison. Plans are moments, not caches — nothing replays.
type planLine struct {
	ID       string      `json:"id"`
	Gen      string      `json:"gen,omitempty"`
	Ask      string      `json:"ask,omitempty"`
	Plan     *drive.Plan `json:"plan,omitempty"`
	USD      float64     `json:"usd,omitempty"`
	Executed string      `json:"executed,omitempty"` // the task id the nod made
	At       time.Time   `json:"at"`
}

// planHome is where a new workspace lands: code under the go tree,
// everything else under the vera home. A world re-roots both.
func planHome(home string) string {
	base := homeDir()
	if worldRoot != "" {
		base = worldRoot
	}
	if home == "code" {
		return filepath.Join(base, "go", "src")
	}
	return filepath.Join(base, "vera")
}

// planCore asks vera for the shape of one piece of work. No side
// effects beyond the journal: the bid is the product.
func (s *server) planCore(ctx context.Context, text string, now time.Time) (*planRec, *sayErr) {
	if s.llm == nil {
		return nil, &sayErr{409, s.notice}
	}
	// Bookmarked ground carries its name and note into the offer —
	// identity is what lets the planner match work to a workspace
	// without asking which repo is which.
	var repos []string
	for _, r := range repoList(s.boardSessions(now), homeDir(), s.scratch.list(), s.marks.list()) {
		line := r["cwd"]
		if r["bookmark"] == "yes" {
			line += " # " + r["dir"]
			if r["note"] != "" {
				line += ": " + r["note"]
			}
		}
		repos = append(repos, line)
	}
	var usd float64
	ll := *s.llm
	ll.Spend = func(c float64) { usd += c }
	cctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	p, err := ll.Plan(cctx, text, repos, now.Format("2006-01-02"))
	if err != nil {
		return nil, &sayErr{502, "vera could not shape a plan: " + err.Error()}
	}
	idb := make([]byte, 4)
	rand.Read(idb)
	rec := &planRec{ID: hex.EncodeToString(idb), Ask: text, Plan: p, USD: usd}
	s.mu.Lock()
	s.plans[rec.ID] = rec
	s.mu.Unlock()
	appendLine(s.planPath, planLine{ID: rec.ID, Gen: planGen, Ask: text, Plan: &p, USD: usd, At: now})
	return rec, nil
}

// executePlanCore is the nod: make the workspace the plan named (or
// verify the one it chose), open the card, and birth the worker with
// the plan's own goal — no second compile, one bill.
func (s *server) executePlanCore(p drive.Plan, planID, mode string, now time.Time) (task, *sayErr) {
	if s.llm == nil {
		return task{}, &sayErr{409, s.notice}
	}
	if p.Goal == "" {
		return task{}, &sayErr{400, "a plan without a goal is not executable"}
	}
	if mode != "read" {
		mode = "work"
	}

	var dir string
	switch p.Kind {
	case "ask":
		q := p.Question
		if q == "" {
			q = "vera needs an answer before it can plan this"
		}
		return task{}, &sayErr{409, "vera needs an answer first: " + q}
	case "none":
		why := p.Why
		if why == "" {
			why = "no directory of files could hold it"
		}
		return task{}, &sayErr{409, "vera judged this needs no workspace: " + why}
	case "repo":
		// The offer may have carried a " # name: note" annotation; the
		// path is everything before it, whatever the model copied.
		p.Where = strings.TrimSpace(strings.SplitN(p.Where, " #", 2)[0])
		if s.repoOffered(s.boardSessions(now), p.Where) == "" {
			return task{}, &sayErr{400, "that workspace is not one the fleet has shown"}
		}
		dir = p.Where
	case "new":
		if !fileID(p.Name) {
			return task{}, &sayErr{400, "the plan's workspace name will not survive a filesystem"}
		}
		dir = filepath.Join(planHome(p.Home), p.Name)
		if _, err := os.Stat(dir); err == nil {
			return task{}, &sayErr{409, "a workspace named " + p.Name + " already exists there"}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return task{}, &sayErr{500, "cannot make the workspace: " + err.Error()}
		}
		// Git from birth: history and review are not code privileges.
		if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			return task{}, &sayErr{500, "git init refused: " + strings.TrimSpace(string(out))}
		}
		// Ground vera makes, vera remembers: the plan's why becomes
		// the workspace's standing description in the registry.
		s.marks.add(p.Name, dir, p.Why)
	default:
		return task{}, &sayErr{400, "a plan needs a kind: repo, new, or none"}
	}

	// The bid the nod answers: its held spend rides to the newborn,
	// and a bid executes once — edits ride the request, not the map.
	var held float64
	ask := p.Goal
	s.mu.Lock()
	if rec := s.plans[planID]; rec != nil && !rec.Done {
		rec.Done = true
		held = rec.USD
		if rec.Ask != "" {
			ask = rec.Ask
		}
	}
	s.mu.Unlock()

	t := task{
		ID:    s.tasks.nextID(),
		Title: transcript.Snip(ask, 90), Intent: ask,
		Goal: p.Goal, GoalActor: "vera",
		Cadence: p.Cadence, Deadline: p.Deadline,
		Mode:      mode,
		Workspace: dir,
		Col:       "progress", State: "in progress · a fresh agent is being born",
		Face:      "Planned by vera. Starting a fresh agent in " + filepath.Base(dir) + ".",
		CreatedAt: now, UpdatedAt: now,
	}
	t.event("human", "approved vera's plan", now)
	if p.Why != "" {
		t.event("vera", "planned: "+p.Why, now)
	}
	if p.Kind == "new" {
		t.event("vera", "created workspace "+dir+" (git init)", now)
	}
	if p.Deadline != "" {
		t.event("vera", "deadline "+p.Deadline, now)
	}
	if p.Cadence == "standing" {
		t.event("vera", "standing need — vera cannot yet return on its own; each pass starts from this card", now)
	}
	if err := s.tasks.write(t); err != nil {
		return task{}, &sayErr{500, err.Error()}
	}
	appendLine(s.planPath, planLine{ID: planID, Executed: t.ID, At: now})
	// A plan that is honestly several pieces lays the later ones on
	// the board as backlog, same ground, vera's authorship on the log —
	// the decomposition is visible before anyone asks for it. Each
	// card names its successor: accepting a finished piece is the nod
	// that starts the next, so the chain spends only at human
	// acceptance boundaries.
	stepIDs := make([]string, 0, len(p.Steps))
	for i, step := range p.Steps {
		st := task{
			ID:    s.tasks.nextID(),
			Title: transcript.Snip(step, 90), Intent: step,
			Workspace: dir, Mode: mode,
			Col: "inbox", State: "inbox · planned",
			Face:     "Step " + strconv.Itoa(i+2) + " of " + t.ID + "'s plan — starts when you accept the piece before it.",
			Proposal: "Start in " + filepath.Base(dir), ProposalWhy: "The piece before it is underway on the same ground.", ProposalKind: "start",
			CreatedAt: now, UpdatedAt: now,
		}
		st.event("vera", "planned as step "+strconv.Itoa(i+2)+" of "+t.ID+"'s plan", now)
		if err := s.tasks.write(st); err != nil {
			break
		}
		stepIDs = append(stepIDs, st.ID)
	}
	for i, id := range stepIDs {
		next := ""
		if i+1 < len(stepIDs) {
			next = stepIDs[i+1]
		}
		s.tasks.mutate(id, func(t *task) error { t.NextID = next; return nil })
	}
	if len(stepIDs) > 0 {
		if t2, err := s.tasks.mutate(t.ID, func(t *task) error { t.NextID = stepIDs[0]; return nil }); err == nil {
			t = t2
		}
	}
	s.spawnFresh(t, dir, mode, p.Goal, held)
	return t, nil
}

// chainNext is the acceptance boundary's other half: the owner just
// accepted a card that names a successor, so the successor starts —
// its intent compiled, a fresh worker born on its ground. A card the
// human already moved, started, or deleted is left exactly alone.
func (s *server) chainNext(id, mode string, now time.Time) {
	if s.llm == nil {
		return
	}
	next, err := s.tasks.get(id)
	if err != nil || next.Col != "inbox" || next.Workspace == "" {
		return
	}
	if _, err := os.Stat(next.Workspace); err != nil {
		return
	}
	var held float64
	ll := *s.llm
	ll.Spend = func(c float64) { held += c }
	goal, err := ll.CompileGoal(context.Background(), next.Intent)
	if err != nil {
		s.tasks.mutate(id, func(t *task) error {
			t.event("vera", "the step before this landed, but the goal would not compile: "+err.Error(), now)
			return nil
		})
		return
	}
	if mode != "read" {
		mode = "work"
	}
	next, err = s.tasks.mutate(id, func(t *task) error {
		t.Mode = mode
		t.Goal, t.GoalActor = goal, "vera"
		t.Col, t.State = "progress", "in progress · a fresh agent is being born"
		t.Face = "The piece before it landed. Starting a fresh agent in " + filepath.Base(t.Workspace) + "."
		t.clearProposal()
		t.event("vera", "the step before this was accepted — compiled intent → goal, starting", now)
		return nil
	})
	if err != nil {
		return
	}
	s.spawnFresh(next, next.Workspace, mode, goal, held)
}

// ---- the routes ----

func (s *server) handlePlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		httpErr(w, 400, "say what you need")
		return
	}
	rec, serr := s.planCore(r.Context(), req.Text, time.Now())
	if serr != nil {
		httpErr(w, serr.code, serr.msg)
		return
	}
	writeJSON(w, rec)
}

func (s *server) handlePlanExecute(w http.ResponseWriter, r *http.Request) {
	// A board mutation is a frame the watchers are owed.
	defer s.hub.notify()
	var req struct {
		ID   string     `json:"id"`
		Plan drive.Plan `json:"plan"`
		Mode string     `json:"mode"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	t, serr := s.executePlanCore(req.Plan, req.ID, req.Mode, time.Now())
	if serr != nil {
		httpErr(w, serr.code, serr.msg)
		return
	}
	writeJSON(w, t)
}
