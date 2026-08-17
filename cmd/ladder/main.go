// The ladder: vera's measuring stick for the cheap-model theory. It
// runs a corpus of mechanically-checkable tasks across a matrix of
// worker models × arms — "bare" (one unsupervised `claude -p` turn)
// and "drive" (the same supervised loop vera's cards run) — each run
// in its own fresh directory under a disposable world, and journals
// one JSONL line per cell. The question it answers: which is cheaper
// per SUCCESS, an expensive model alone or a cheap model supervised?
//
//	go run ./cmd/ladder -corpus cmd/ladder/corpus.example.json -world ~/vera-lab \
//	  -models claude-sonnet-5,claude-opus-5 -reps 3
//	go run ./cmd/ladder -world ~/vera-lab -table
//
// It answers a second question too, once the corpus tags its tasks with
// a node kind: does vera's ROUTING table hold? -route runs each task at
// every tier alias and -by-kind reads the answer back per kind, with the
// routed tier marked and a verdict that names which way the table should
// move — or refuses to call it when the runs are too few to mean
// anything.
//
//	go run ./cmd/ladder -corpus cmd/ladder/corpus.routing.json -world ~/vera-lab \
//	  -route -arms drive -reps 8
//	go run ./cmd/ladder -world ~/vera-lab -by-kind
//
// Reruns skip cells already on the record, so the matrix can be grown
// (more models, more reps, more tasks) and re-invoked; only the new
// cells bill. Pass/fail comes from each task's own check command or
// expected substrings — never from the supervising judge, which would
// be grading its own steering.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/drive"
	"github.com/incantery/vera/route"
)

type cell struct {
	task  task
	model string
	arm   string // bare | drive
	rep   int
}

type lab struct {
	world, runsDir, resultsPath string
	claudeBin, perm             string
	turns                       int
	maxUSD                      float64
	timeout                     time.Duration
	judge                       func(spend func(float64)) drive.Judge // nil: the drive arm is unavailable
	mu                          sync.Mutex                            // guards the journal and the progress line counter
	ran, total                  int
}

func main() {
	corpusPath := flag.String("corpus", "", "the task corpus (JSON; see cmd/ladder/corpus.example.json)")
	world := flag.String("world", "", "lab directory: run dirs and results.jsonl live under it; rm -rf resets the experiment")
	models := flag.String("models", "claude-sonnet-5,claude-opus-5", "worker models, comma-separated (claude's --model names)")
	arms := flag.String("arms", "bare,drive", "arms to run: bare (one unsupervised turn), drive (the supervised loop)")
	reps := flag.Int("reps", 3, "repetitions per cell — one run is an anecdote")
	only := flag.String("only", "", "run these task ids only, comma-separated")
	parallel := flag.Int("parallel", 1, "cells in flight at once")
	turns := flag.Int("turns", 4, "drive arm: prompts before giving up")
	maxUSD := flag.Float64("max-usd", 5, "drive arm: metered claude spend before escalating")
	timeout := flag.Duration("timeout", 10*time.Minute, "per-turn timeout")
	claudeBin := flag.String("claude", "", "the claude binary (default: claude from PATH)")
	perm := flag.String("perm", "", "claude --permission-mode for worker turns (empty omits it)")
	judgeModel := flag.String("judge-model", "gpt-5.6-luna", "drive arm's judge model")
	judgeBase := flag.String("judge-api-base", "", "judge API base URL (default OpenAI; ollama etc. work)")
	judgeKeyFile := flag.String("judge-key-file", "", "judge API key file (default ~/.config/vera/openai_key)")
	judgeEffort := flag.String("judge-effort", "low", "judge reasoning effort (empty omits the field)")
	table := flag.Bool("table", false, "print the results table and exit; runs nothing")
	byTask := flag.Bool("by-task", false, "table: one row per task × model × arm")
	// Routing mode: run every kind at every tier so the routing table's
	// claims become checkable. -models is ignored here on purpose — the
	// point is to compare the tiers vera actually routes to, not an
	// arbitrary list.
	routeMode := flag.Bool("route", false, "measure the routing table: run each task at every tier alias (ignores -models)")
	// The board is the better instrument. A corpus cheap enough to write
	// is too well-specified to separate the tiers on implementation; a
	// real node is several files, an existing repository, and no tests
	// until someone writes them. This reads vera's outcome journal and
	// grades it with the same verdict the lab uses.
	board := flag.String("board", "", "read vera's outcome journal instead of the lab (default ~/.local/state/rook/vera-outcomes.jsonl when bare)")
	byKind := flag.Bool("by-kind", false, "table: one block per node kind, with the routed tier marked and a verdict")
	flag.Parse()

	// The board mode reads a journal and runs nothing, so it needs no
	// lab and no world.
	if *board != "" || (*byKind && *world == "") {
		path := *board
		if path == "" || path == "-" {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, ".local", "state", "rook", "vera-outcomes.jsonl")
		}
		obs, err := loadBoardOutcomes(path)
		if err != nil {
			fatal(err.Error())
		}
		fmt.Printf("%s — %d finished node(s)\n", path, len(obs))
		route.WriteTable(os.Stdout, obs,
			"no routing evidence on the board yet — a node contributes when it is\n"+
				"planned into a graph (so it has a kind), routed (so it has a model),\n"+
				"and then accepted or dropped by you (so it has a verdict).")
		return
	}
	if *world == "" {
		fatal("pick a -world directory — the lab needs ground it owns")
	}
	l := &lab{
		world:       *world,
		runsDir:     filepath.Join(*world, "runs"),
		resultsPath: filepath.Join(*world, "results.jsonl"),
		claudeBin:   *claudeBin,
		perm:        *perm,
		turns:       *turns,
		maxUSD:      *maxUSD,
		timeout:     *timeout,
	}

	if *table || *byKind {
		results, err := loadResults(l.resultsPath)
		if err != nil {
			fatal(err.Error())
		}
		if *byKind {
			writeRoutingTable(os.Stdout, results)
		} else {
			writeTable(os.Stdout, results, *byTask)
		}
		return
	}

	if *corpusPath == "" {
		fatal("pick a -corpus (or -table to read the record)")
	}
	tasks, err := loadCorpus(*corpusPath)
	if err != nil {
		fatal(err.Error())
	}
	if *only != "" {
		tasks = filterTasks(tasks, strings.Split(*only, ","))
		if len(tasks) == 0 {
			fatal("-only matched no task in the corpus")
		}
	}

	armList := splitList(*arms)
	for _, a := range armList {
		if a != "bare" && a != "drive" {
			fatal("unknown arm " + a + " (bare and drive are the vocabulary)")
		}
	}
	modelList := splitList(*models)
	if *routeMode {
		// The tier aliases, cheapest first, so a comparison walks in the
		// direction the saving would come from.
		modelList = nil
		for _, t := range route.Tiers {
			modelList = append(modelList, route.WorkerAlias[t])
		}
		var untagged []string
		for _, t := range tasks {
			if t.Kind == "" {
				untagged = append(untagged, t.ID)
			}
		}
		if len(untagged) == len(tasks) {
			fatal("-route needs a corpus tagged with kinds; none of these tasks carry one")
		}
		if len(untagged) > 0 {
			fmt.Printf("note: %d task(s) carry no kind and sit out the routing verdict: %s\n",
				len(untagged), strings.Join(untagged, ", "))
		}
	}
	if len(modelList) == 0 || len(armList) == 0 {
		fatal("the matrix needs at least one model and one arm")
	}

	// The judge exists only if the drive arm runs, and wants a key only
	// against the default (OpenAI) base — the same resolution as vera's.
	if hasArm(armList, "drive") {
		key := judgeKey(*judgeKeyFile)
		if key == "" && *judgeBase == "" {
			fatal("the drive arm needs a judge: set $OPENAI_API_KEY, write ~/.config/vera/openai_key, or point -judge-api-base at a keyless server")
		}
		l.judge = func(spend func(float64)) drive.Judge {
			return &drive.LLMJudge{LLM: &drive.LLM{
				Base: *judgeBase, Key: key, Name: *judgeModel, Effort: *judgeEffort, Spend: spend,
			}}
		}
	}

	if err := os.MkdirAll(l.runsDir, 0o755); err != nil {
		fatal(err.Error())
	}
	prior, err := loadResults(l.resultsPath)
	if err != nil {
		fatal(err.Error())
	}
	done := map[string]bool{}
	for _, r := range prior {
		done[r.key()] = true
	}

	var cells []cell
	skipped := 0
	for _, t := range tasks {
		for _, m := range modelList {
			for _, a := range armList {
				for rep := 1; rep <= *reps; rep++ {
					c := cell{task: t, model: m, arm: a, rep: rep}
					if done[(&result{Task: t.ID, Model: m, Arm: a, Rep: rep}).key()] {
						skipped++
						continue
					}
					cells = append(cells, c)
				}
			}
		}
	}
	l.total = len(cells)
	fmt.Printf("%d cells to run (%d already on the record) → %s\n", len(cells), skipped, l.resultsPath)
	if len(cells) == 0 {
		writeTable(os.Stdout, prior, *byTask)
		if *routeMode {
			writeRoutingTable(os.Stdout, prior)
		}
		return
	}

	// Ctrl-C stops cleanly: in-flight cells die with the context and
	// simply are not on the record; everything journaled stays.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	jobs := make(chan cell)
	var wg sync.WaitGroup
	for w := 0; w < max(1, *parallel); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				l.runCell(ctx, c)
			}
		}()
	}
feed:
	for _, c := range cells {
		select {
		case jobs <- c:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	results, err := loadResults(l.resultsPath)
	if err != nil {
		fatal(err.Error())
	}
	fmt.Println()
	writeTable(os.Stdout, results, *byTask)
	if *routeMode {
		writeRoutingTable(os.Stdout, results)
	}
}

// runCell runs one (task, model, arm, rep) in a fresh directory and
// journals the outcome. A turn that errors is a recorded failure, not
// a crash — the matrix keeps moving.
func (l *lab) runCell(ctx context.Context, c cell) {
	if ctx.Err() != nil {
		return
	}
	r := result{
		At: time.Now().UTC().Format(time.RFC3339), Task: c.task.ID,
		Model: c.model, Arm: c.arm, Rep: c.rep, Kind: c.task.Kind,
	}
	dir, err := l.freshDir(c)
	if err != nil {
		r.Err = err.Error()
		l.record(r, 0)
		return
	}
	turner := &drive.Headless{
		Bin: l.claudeBin, Dir: dir, Model: c.model, Timeout: l.timeout,
		AllowedTools: c.task.tools(), PermissionMode: l.perm,
	}
	start := time.Now()
	var reply string
	switch c.arm {
	case "bare":
		t, err := turner.StartTurn(ctx, c.task.Goal)
		r.ClaudeUSD, r.Turns, r.Session = t.CostUSD, 1, t.SessionID
		if err != nil {
			r.Err = err.Error()
		} else {
			r.Done, reply = true, t.Reply
		}
	case "drive":
		judge := l.judge(func(cost float64) { r.JudgeUSD += cost })
		loop := &drive.Loop{Turner: turner, Judge: judge, MaxTurns: l.turns, MaxUSD: l.maxUSD}
		res, err := loop.RunFresh(ctx, c.task.Goal)
		r.ClaudeUSD, r.Turns, r.Session = res.CostUSD, len(res.Turns), res.Root
		r.Done, r.Escalated, r.Reason = res.Done, res.Escalated, res.Reason
		if err != nil {
			r.Err = err.Error()
		}
		if n := len(res.Turns); n > 0 {
			reply = res.Turns[n-1].Reply
		}
	}
	r.Secs = time.Since(start).Seconds()

	// The bar. Both halves must hold when both are stated; an errored
	// run still faces it — a crash that left the tests passing did not
	// happen, and the check saying so is the honest record.
	pass := true
	if c.task.Check != "" {
		ok, out := runCheck(ctx, dir, c.task.Check)
		r.CheckOK = &ok
		if !ok {
			pass = false
			r.CheckOut = out
		}
	}
	if len(c.task.Expect) > 0 {
		ok := expectAll(reply, c.task.Expect)
		r.ExpectOK = &ok
		pass = pass && ok
	}
	r.Pass = pass && r.Err == ""
	l.record(r, r.ClaudeUSD+r.JudgeUSD)
}

// freshDir makes the cell's directory — wiping a half-run leftover
// first (only ever inside the lab's own runs dir) — and seeds the
// task's files.
func (l *lab) freshDir(c cell) (string, error) {
	name := fmt.Sprintf("%s--%s--%s--r%d", c.task.ID, safeName(c.model), c.arm, c.rep)
	dir := filepath.Join(l.runsDir, name)
	if rel, err := filepath.Rel(l.runsDir, dir); err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("run dir %q escapes the lab", name)
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for p, content := range c.task.Files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func (l *lab) record(r result, usd float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := appendResult(l.resultsPath, r); err != nil {
		fmt.Fprintln(os.Stderr, "journal write failed (the cell ran, the record lost it):", err)
	}
	l.ran++
	word := "fail"
	if r.Pass {
		word = "pass"
	}
	if r.Err != "" {
		word += " (errored: " + snip(r.Err, 60) + ")"
	}
	fmt.Printf("[%d/%d] %s %s %s r%d → %s  $%.2f  %.0fs\n",
		l.ran, l.total, r.Task, r.Model, r.Arm, r.Rep, word, usd, r.Secs)
}

// runCheck is the mechanical bar: the command, in the run's directory,
// exit 0 or not. Output rides back trimmed for the record.
func runCheck(ctx context.Context, dir, check string) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", check)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, ""
	}
	return false, snip(string(out)+" "+err.Error(), 400)
}

func expectAll(reply string, wants []string) bool {
	folded := strings.ToLower(reply)
	for _, w := range wants {
		if !strings.Contains(folded, strings.ToLower(w)) {
			return false
		}
	}
	return true
}

// judgeKey resolves the judge's credentials the way cmd/vera does:
// $OPENAI_API_KEY, then the key file (default ~/.config/vera/openai_key).
func judgeKey(keyFile string) string {
	if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
		return key
	}
	if keyFile == "" {
		if home, _ := os.UserHomeDir(); home != "" {
			keyFile = filepath.Join(home, ".config", "vera", "openai_key")
		}
	}
	if b, err := os.ReadFile(keyFile); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

func filterTasks(tasks []task, ids []string) []task {
	want := map[string]bool{}
	for _, id := range ids {
		want[strings.TrimSpace(id)] = true
	}
	var out []task
	for _, t := range tasks {
		if want[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func hasArm(arms []string, want string) bool {
	for _, a := range arms {
		if a == want {
			return true
		}
	}
	return false
}

// safeName folds a model name into the run-directory charset.
func safeName(s string) string {
	out := []byte(s)
	for i, c := range out {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			out[i] = '-'
		}
	}
	return string(out)
}

func snip(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
