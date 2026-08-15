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
	"strings"
	"time"

	"github.com/incantery/vera/drive"
	"github.com/incantery/vera/transcript"
)

// planGen salts the plan journal so a prompt change reads as a new
// generation in later analysis. Bump on any planSysPrompt change.
const planGen = "p2|"

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
	var repos []string
	for _, r := range repoList(s.boardSessions(now), homeDir(), s.scratch.list()) {
		repos = append(repos, r["cwd"])
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
	case "none":
		why := p.Why
		if why == "" {
			why = "no directory of files could hold it"
		}
		return task{}, &sayErr{409, "vera judged this needs no workspace: " + why}
	case "repo":
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
	s.spawnFresh(t, dir, mode, p.Goal, held)
	return t, nil
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
