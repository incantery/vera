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
const planGen = "p7|"

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
	ll := *s.llmFor(partPlan)
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
	t, err := s.tasks.create(t)
	if err != nil {
		return task{}, &sayErr{500, err.Error()}
	}
	appendLine(s.planPath, planLine{ID: planID, Executed: t.ID, At: now})
	s.layGraph(&t, p, dir, mode, now)
	s.spawnFresh(t, dir, mode, p.Goal, held)
	return t, nil
}

// layGraph puts a plan's later pieces on the board as one goal's
// nodes. The root card is piece 1 and every node points back at it
// through Root, so the work view can draw the whole shape from any
// card in it.
//
// Dependencies resolve backwards only: a node may wait on pieces
// written before it, never after. That is not a limitation of the
// planner's expressiveness but the cheapest possible cycle guard — a
// graph that cannot point forward cannot point at itself, and a
// deadlocked plan is worse than a slightly flatter one.
//
// A node whose dependencies all resolve to nothing waits on the root,
// which is the honest default: it is a later piece of THIS goal, and
// it should not open before the goal itself has moved.
func (s *server) layGraph(root *task, p drive.Plan, dir, mode string, now time.Time) {
	if len(p.Nodes) == 0 {
		return
	}
	// Piece 1 is the root; ids fill in as the cards are made.
	ids := make([]string, len(p.Nodes)+1)
	ids[0] = root.ID

	made := 0
	for i, n := range p.Nodes {
		piece := i + 2 // pieces are 1-based and the root took 1
		kind := nodeKind(n.Kind)
		var deps []string
		for _, d := range n.Deps {
			if d < 1 || d >= piece || ids[d-1] == "" {
				continue // forward, self, or a piece that failed to be made
			}
			deps = append(deps, ids[d-1])
		}
		if len(deps) == 0 {
			deps = []string{root.ID}
		}
		nt := task{
			Title: transcript.Snip(n.Text, 90), Intent: n.Text,
			Workspace: dir, Mode: modeFor(kind, mode),
			Kind: kind, Deps: deps, Root: root.ID,
			Col: "inbox", State: "inbox · planned · waiting on " + strings.Join(deps, ", "),
			Face:      faceFor(kind, deps),
			CreatedAt: now, UpdatedAt: now,
		}
		// A writing node still offers itself: the owner may always start
		// a piece early. A reading one does not — vera opens those
		// herself the moment their ground is ready, and an offer for
		// something already handled is noise on the card.
		if !readOnly(kind) {
			nt.Proposal = "Start in " + filepath.Base(dir)
			nt.ProposalWhy = "You can start it now rather than waiting for " + strings.Join(deps, ", ") + "."
			nt.ProposalKind = "start"
		}
		nt.event("vera", "planned as piece "+strconv.Itoa(piece)+" of "+root.ID+"'s graph ("+
			kind+", waiting on "+strings.Join(deps, ", ")+")", now)
		nt, err := s.tasks.create(nt)
		if err != nil {
			continue
		}
		ids[i+1] = nt.ID
		made++
		s.events.emit(evNodePlanned, root.ID, nt.ID,
			"Planned a "+kind+" node waiting on "+strings.Join(deps, ", ")+": "+transcript.Snip(n.Text, 80))
	}
	if made == 0 {
		return
	}
	if r2, err := s.tasks.mutate(root.ID, func(t *task) error {
		t.Root, t.Kind = t.ID, kindImplement
		t.event("vera", "piece 1 of a "+strconv.Itoa(made+1)+"-node graph", now)
		return nil
	}); err == nil {
		*root = r2
	}
	s.events.emit(evPlanDrawn, root.ID, root.ID,
		"Drew a "+strconv.Itoa(made+1)+"-node graph for this goal.")
}

// faceFor is the one line the card wears before it runs — what this
// node is for, and what has to happen first.
func faceFor(kind string, deps []string) string {
	waiting := " Waits on " + strings.Join(deps, ", ") + "."
	switch kind {
	case kindReview:
		return "A review of what the work produced." + waiting + " Vera opens it herself — it reads only."
	case kindVerify:
		return "Runs the checks and reports." + waiting + " Vera opens it herself — it reads only."
	case kindInvestigate:
		return "Reads and reports; writes nothing." + waiting + " Vera opens it herself."
	case kindReconcile:
		return "Reads a disagreement and rules on it." + waiting + " Vera opens it herself."
	default:
		return "A later piece of this goal." + waiting + " Starts when you accept what it waits on."
	}
}

// chainNext is the acceptance boundary's other half: the owner just
// accepted a card that names a successor, so the successor starts —
// its intent compiled, a fresh worker born on its ground. A card the
// human already moved, started, or deleted is left exactly alone.
func (s *server) chainNext(id, mode string, now time.Time) {
	if mode != "read" {
		mode = "work"
	}
	s.igniteCard(id, mode, "the step before this was accepted", now)
}

// igniteCard starts an inbox card whose moment came — a chain step
// just accepted, a schedule entry due. cause is the honest clause the
// log wears; the mechanism is the same either way: compile the
// intent, birth a fresh worker on the card's ground.
func (s *server) igniteCard(id, mode, cause string, now time.Time) {
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
	ll := *s.llmFor(partCompile)
	ll.Spend = func(c float64) { held += c }
	goal, err := ll.CompileGoal(context.Background(), next.Intent)
	if err != nil {
		s.tasks.mutate(id, func(t *task) error {
			t.event("vera", cause+", but the goal would not compile: "+err.Error(), now)
			return nil
		})
		return
	}
	next, err = s.tasks.mutate(id, func(t *task) error {
		t.Mode = mode
		t.Goal, t.GoalActor = goal, "vera"
		t.Col, t.State = "progress", "in progress · a fresh agent is being born"
		t.Face = "Starting a fresh agent in " + filepath.Base(t.Workspace) + "."
		t.clearProposal()
		t.event("vera", cause+" — compiled intent → goal, starting", now)
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
