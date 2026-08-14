// roost — the smallest useful piece of rook: run one binary, get
// the membrane. It watches this machine's Claude Code sessions the way
// rook's plugins do (the transcripts are the witness; nothing is
// installed into anything), serves a local web page that shows what
// every session is doing, and lets you hand a session a GOAL: the
// drive loop prompts Claude, judges the reply, and keeps the
// conversation going until the goal is met or the turn budget runs out
// — headlessly, in forks, never touching a live terminal.
//
// No rook, no cloud, no account. The judge wants an OpenAI-compatible
// endpoint ($OPENAI_API_KEY, ~/.config/rook/openai_key, or --api-base
// pointed at a local server); without one the watcher still works and
// the page says exactly what is missing.
//
// The default bind is loopback. --addr :4770 opens it to your LAN —
// that is a decision, not a default, so it is a flag you type.
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/incantery/rook-host/engine/drive"
	"github.com/incantery/rook-host/engine/transcript"
	"github.com/incantery/rook-host/engine/usage"
)

// The UI is a SvelteKit SPA (web/), built by `npm run build` and
// committed under web/build so the module is `go run`-able with no
// node in sight. all: because the output starts with _app/.
//
//go:embed all:web/build
var webFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:4770", "listen address (\":4770\" opens it to your LAN)")
	dir := flag.String("dir", "", "projects directory (default ~/.claude/projects)")
	window := flag.Duration("window", 48*time.Hour, "how far back sessions are shown")
	model := flag.String("model", "gpt-5.6-luna", "judge model (any OpenAI-compatible server's name for it)")
	apiBase := flag.String("api-base", "", "judge API base URL (default OpenAI; ollama etc. work)")
	keyFile := flag.String("key-file", "", "judge API key file (default ~/.config/rook/openai_key)")
	effort := flag.String("effort", "low", "judge reasoning effort (empty omits the field)")
	claudeBin := flag.String("claude", "", "the claude binary (default: claude from PATH)")
	turns := flag.Int("turns", 4, "prompts a drive may send before giving up")
	statePath := flag.String("state", defaultLineagePath(), "lineage journal (empty forgets forks across restarts)")
	artifactsDir := flag.String("artifacts", defaultArtifactsDir(), "artifact shelf directory (empty disables it)")
	tasksDir := flag.String("tasks", defaultTasksDir(), "task board directory (empty disables it)")
	flag.Parse()

	home, _ := os.UserHomeDir()
	if *dir == "" {
		if home == "" {
			fmt.Fprintln(os.Stderr, "no home directory and no --dir")
			os.Exit(1)
		}
		*dir = filepath.Join(home, ".claude", "projects")
	}
	if *keyFile == "" && home != "" {
		*keyFile = filepath.Join(home, ".config", "rook", "openai_key")
	}
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		if b, err := os.ReadFile(*keyFile); err == nil {
			key = strings.TrimSpace(string(b))
		}
	}

	s := &server{
		sc: &transcript.Scanner{
			Dir:    *dir,
			Window: *window,
			Idle:   10 * time.Minute,
			Quiet:  60 * time.Second,
			Max:    50,
		},
		claudeBin:  *claudeBin,
		turns:      *turns,
		ln:         openLineage(*statePath),
		says:       map[string]*sayJob{},
		spend:      map[string]*agentSpend{},
		digests:    map[string]*digestRec{},
		sent:       map[string]string{},
		uc:         &usage.Collector{Bin: *claudeBin},
		shelf:      &artifactStore{dir: *artifactsDir},
		tasks:      &taskStore{dir: *tasksDir},
		scratch:    &scratchStore{parent: defaultScratchParent()},
		spendPath:  defaultSpendPath(),
		digestPath: defaultDigestPath(),
		uploads:    defaultUploadsDir(),
	}
	s.loadJournals()
	go s.uc.Loop()
	// A missing key is a standing notice only where the default API
	// lives; a custom base is a local server that wants no auth.
	if key == "" && *apiBase == "" {
		s.notice = "no rook-agent key — set $OPENAI_API_KEY or write " + *keyFile + " (drives, digests and phrasing are off; watching works)"
	} else {
		s.llm = &drive.LLM{
			Client: &http.Client{Timeout: 90 * time.Second},
			Base:   *apiBase, Key: key, Name: *model, Effort: *effort,
		}
	}

	mux := http.NewServeMux()
	mux.Handle("GET /", webHandler())
	// The login screen's probe: carries no data, exists to be guarded.
	// requireKey answers 401 without a key; reaching the handler at all
	// means the door opened (or the bind is loopback and has no door).
	mux.HandleFunc("GET /api/auth", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/agent/{id}", s.handleAgent)
	mux.HandleFunc("POST /api/agent/{id}/say", s.handleSay)
	mux.HandleFunc("POST /api/agent/{id}/interrupt", s.handleInterrupt)
	mux.HandleFunc("POST /api/agent/{id}/upload", s.handleUpload)
	mux.HandleFunc("GET /api/agent/{id}/uploads/{name}", s.handleUploadGet)
	mux.HandleFunc("GET /api/agent/{id}/artifacts", s.handleArtifactList)
	mux.HandleFunc("POST /api/agent/{id}/artifacts", s.handleArtifactCreate)
	mux.HandleFunc("GET /api/agent/{id}/artifacts/{aid}", s.handleArtifactGet)
	mux.HandleFunc("PUT /api/agent/{id}/artifacts/{aid}", s.handleArtifactUpdate)
	mux.HandleFunc("DELETE /api/agent/{id}/artifacts/{aid}", s.handleArtifactDelete)
	mux.HandleFunc("GET /api/tasks", s.handleTaskList)
	mux.HandleFunc("POST /api/tasks", s.handleTaskCapture)
	mux.HandleFunc("POST /api/tasks/{tid}/start", s.handleTaskStart)
	mux.HandleFunc("POST /api/tasks/{tid}/act", s.handleTaskAct)
	mux.HandleFunc("POST /api/tasks/{tid}/reply", s.handleTaskReply)
	mux.HandleFunc("POST /api/workspaces", s.handleWorkspaceCreate)
	mux.HandleFunc("DELETE /api/workspaces/{name}", s.handleWorkspaceDelete)
	mux.HandleFunc("POST /api/drive", s.handleDrive)
	mux.HandleFunc("POST /api/drive/stop", s.handleStop)

	fmt.Printf("roost: watching %s\n", *dir)
	handler := http.Handler(mux)
	if loopbackOnly(*addr) {
		fmt.Printf("roost: open http://%s (loopback only — no key needed)\n", printableAddr(*addr))
	} else {
		// Beyond loopback, the door gets its key: minted once, printed
		// as part of the URL, required on every /api call.
		key, err := loadOrCreateKey(defaultKeyPath())
		if err != nil {
			fmt.Fprintln(os.Stderr, "roost: cannot mint a key ("+err.Error()+") — refusing to serve the LAN unlocked")
			os.Exit(1)
		}
		handler = requireKey(mux, key)
		_, port, _ := strings.Cut(*addr, ":")
		fmt.Println("roost: serving beyond loopback — a key guards the API. Open with:")
		for _, u := range lanURLs(port) {
			fmt.Printf("roost:   %s/?key=%s\n", u, key)
		}
		fmt.Println("roost: (or open any of them bare and type the key at the login screen)")
	}
	if s.notice != "" {
		fmt.Println("roost: " + s.notice)
	}
	if err := http.ListenAndServe(*addr, handler); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func printableAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

// webHandler serves the embedded SPA: real files as themselves, any
// other path as the shell — client routing's 404 is not the server's
// business.
func webHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web/build")
	if err != nil {
		panic(err) // the embed is broken at build time, not at runtime
	}
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(sub, p); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		// The shell must never be cached: its asset names are hashed,
		// and a stale shell after a rebuild asks for files that no
		// longer exist — a page that half-loads for no visible reason.
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFileFS(w, r, sub, "index.html")
	})
}

// ---- the server ----

type server struct {
	sc        *transcript.Scanner
	llm       *drive.LLM // the rook agent's wire; nil = no key, membrane off
	notice    string
	claudeBin string
	turns     int
	ln        *lineage
	uc        *usage.Collector
	shelf     *artifactStore
	tasks     *taskStore
	scratch   *scratchStore

	spendPath  string // spend journal; "" = remember only while running
	digestPath string // digest journal; same deal
	uploads    string // pasted-image directory, namespaced per agent

	mu      sync.Mutex
	runs    []*run                 // newest first
	says    map[string]*sayJob     // agent root -> the say in flight (or its failure)
	spend   map[string]*agentSpend // agent root -> what has been spent on it, ever
	digests map[string]*digestRec  // reply-hash -> the membrane's compression of it
	sent    map[string]string      // sent-text-hash -> the rough words behind it
	queues  map[string][]queuedSay // agent root -> direct messages typed ahead
}

// digestRec is one reply's compression, cached by the reply's hash so
// a turn is billed once however many times the page polls.
type digestRec struct {
	State    string   `json:"state"` // "pending" | "ready" | "failed"
	Headline string   `json:"headline,omitempty"`
	Bullets  []string `json:"bullets,omitempty"`
}

// rootLLM is the rook agent's wire with its meter bound to one agent's
// ledger.
func (s *server) rootLLM(root string) *drive.LLM {
	ll := *s.llm
	ll.Spend = func(c float64) { s.addSpend(root, 0, c) }
	return &ll
}

// agentSpend is one agent's bill for this process's lifetime: what
// claude metered for its turns, and what the judge's endpoint cost.
type agentSpend struct {
	ClaudeUSD float64 `json:"claudeUsd,omitempty"`
	JudgeUSD  float64 `json:"judgeUsd,omitempty"`
}

func (s *server) addSpend(root string, claude, judge float64) {
	if claude == 0 && judge == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp := s.spend[root]
	if sp == nil {
		sp = &agentSpend{}
		s.spend[root] = sp
	}
	sp.ClaudeUSD += claude
	sp.JudgeUSD += judge
	appendLine(s.spendPath, spendLine{Root: root, Claude: claude, Judge: judge, At: time.Now()})
}

// sayJob is one chat message on its way through a headless turn — or
// the honest record of the one that failed, kept until the next send.
type sayJob struct {
	Text    string    `json:"text"`           // the human's words
	Sent    string    `json:"sent,omitempty"` // what the membrane phrased and delivered
	Status  string    `json:"status"`         // "phrasing" | "thinking" | "failed"
	Err     string    `json:"error,omitempty"`
	Direct  bool      `json:"direct,omitempty"` // straight to claude, no membrane
	Perm    string    `json:"perm,omitempty"`   // the tool policy the turn ran under
	Images  []string  `json:"images,omitempty"` // attached files riding this message
	CostUSD float64   `json:"costUsd,omitempty"`
	At      time.Time `json:"at"`

	cancel      context.CancelFunc // interrupt: kill the claude subprocess
	interrupted bool
}

// queuedSay is a direct-mode message typed while a turn was in
// flight; it lands when that turn ends, in order, with its own policy.
type queuedSay struct {
	Text   string   `json:"text"`
	Perm   string   `json:"perm,omitempty"`
	Images []string `json:"images,omitempty"`
}

const queueCap = 3

// permPolicy maps a direct-mode policy name onto claude's own
// permission system. The names are a closed set; the third answer is
// the refusal message, empty when the name is good.
func permPolicy(perm string) ([]string, string, string) {
	switch perm {
	case "", "read":
		return nil, "", ""
	case "edit":
		return workTools, "", ""
	case "all":
		return nil, "bypassPermissions", ""
	}
	return nil, "", "perm must be read, edit, or all"
}

// run is one drive's row-worth of truth, live or finished.
type run struct {
	ID           string           `json:"id"`
	SessionID    string           `json:"sessionId"`
	SessionTitle string           `json:"sessionTitle"`
	Goal         string           `json:"goal"`
	Status       string           `json:"status"`
	Finished     bool             `json:"finished"`
	Done         bool             `json:"done"`
	Reason       string           `json:"reason,omitempty"`
	Turns        []drive.Exchange `json:"turns,omitempty"`
	ResumeID     string           `json:"resumeId,omitempty"` // the final fork; `claude --resume` it
	TaskID       string           `json:"taskId,omitempty"`   // the board task this run belongs to, when it does
	ClaudeUSD    float64          `json:"claudeUsd,omitempty"`
	JudgeUSD     float64          `json:"judgeUsd,omitempty"`
	At           time.Time        `json:"at"`

	cancel context.CancelFunc
}

type wireSession struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	State    string `json:"state"`
	Dir      string `json:"dir"`
	Cwd      string `json:"cwd"`
	Branch   string `json:"branch,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	LastText string `json:"lastText,omitempty"`
	CtxPct   int    `json:"ctxPct,omitempty"`
	Model    string `json:"model,omitempty"`
	Age      string `json:"age"`
	Driving  bool   `json:"driving"`
	// The membrane's line: the transcript's latest tool call. Present
	// whenever the tail holds one — the UI decides how loudly to wear
	// it (live while working, "last did" while quiet).
	Tool       string `json:"tool,omitempty"`
	ToolDetail string `json:"toolDetail,omitempty"`
	// The rail's ranking facts: the open board task this agent is
	// assigned to (its id), and whether the agent lives in a
	// roost-made scratch workspace.
	Task    string `json:"task,omitempty"`
	Scratch bool   `json:"scratch,omitempty"`
}

// handleState is the index's poll: one agent per LINEAGE. Forks are
// plumbing and never listed; a root whose conversation moved to a fork
// wears the fork's live state, so the list reads as agents, not as the
// thread-management the fork mechanic would otherwise leak.
func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	scanned := s.sc.Scan(now)
	byID := map[string]*transcript.Session{}
	for i := range scanned {
		byID[scanned[i].ID] = &scanned[i]
	}
	// Which agents carry open board work — the rail ranks by this.
	onTask := map[string]string{}
	for _, t := range s.tasks.list() {
		if t.open() && t.Agent != "" {
			onTask[t.Agent] = t.ID
		}
	}
	var sessions []wireSession
	// current is the agent whose lineage saw the freshest transcript
	// activity — "the session you are living in right now". The home
	// screen prefers it and wears it as the glowing dot.
	current, currentAt := "", time.Time{}
	for i := range scanned {
		t := &scanned[i]
		if s.ln.isFork(t.ID) {
			continue
		}
		live := t
		if h := byID[s.ln.headOf(t.ID)]; h != nil {
			live = h
		}
		if live.Mtime.After(currentAt) {
			current, currentAt = t.ID, live.Mtime
		}
		ws := wireSession{
			ID: t.ID, Title: live.Title, State: string(live.State),
			Dir: filepath.Base(live.Cwd), Cwd: live.Cwd, Branch: live.Branch,
			Prompt:     transcript.Snip(live.Prompt, 200),
			LastText:   transcript.Snip(live.LastText, 500),
			Model:      live.Model,
			Age:        transcript.RelAge(now.Sub(live.Mtime)),
			Driving:    s.driving(t.ID),
			Tool:       live.ToolName,
			ToolDetail: live.ToolDetail,
			Task:       onTask[t.ID],
			Scratch:    s.scratch.has(live.Cwd),
		}
		if pct := transcript.CtxPct(live.CtxTokens, live.Model); pct >= 0 {
			ws.CtxPct = pct
		}
		sessions = append(sessions, ws)
	}
	s.mu.Lock()
	runs := make([]run, 0, len(s.runs))
	for _, r := range s.runs {
		runs = append(runs, *r)
	}
	s.mu.Unlock()
	writeJSON(w, map[string]any{
		"sessions": sessions,
		"current":  current,
		"drives":   runs,
		"notice":   s.notice,
		"turns":    s.turns,
		"usage":    s.uc.Latest(),
	})
}

// resolveAgent finds an agent's root and its live head session. The
// head's transcript is the whole conversation; the root is the
// identity the URL carries forever.
func (s *server) resolveAgent(id string, now time.Time) (root string, head *transcript.Session) {
	root = s.ln.rootOf(id)
	headID := s.ln.headOf(root)
	var rootSess *transcript.Session
	for _, t := range s.sc.Scan(now) {
		tt := t
		if t.ID == headID {
			head = &tt
		}
		if t.ID == root {
			rootSess = &tt
		}
	}
	if head == nil {
		// The head fell out of the window (or was cleaned up); the
		// root's word is stale but better than a dead page.
		head = rootSess
	}
	return root, head
}

func (s *server) handleAgent(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	root, head := s.resolveAgent(r.PathValue("id"), now)
	if head == nil {
		httpErr(w, 404, "that agent is gone from the window")
		return
	}
	agent := wireSession{
		ID: root, Title: head.Title, State: string(head.State),
		Dir: filepath.Base(head.Cwd), Cwd: head.Cwd, Branch: head.Branch,
		Model:      head.Model,
		Age:        transcript.RelAge(now.Sub(head.Mtime)),
		Driving:    s.driving(root),
		Tool:       head.ToolName,
		ToolDetail: head.ToolDetail,
	}
	if pct := transcript.CtxPct(head.CtxTokens, head.Model); pct >= 0 {
		agent.CtxPct = pct
	}
	// The context block: how full the window is and where it went —
	// straight from the last assistant message's own usage record.
	var ctx map[string]any
	if head.CtxTokens > 0 {
		ctx = map[string]any{
			"tokens": head.CtxTokens, "in": head.CtxIn,
			"cacheRead": head.CtxCacheRd, "cacheWrite": head.CtxCacheWr,
			"out": head.CtxOut, "window": transcript.Window(head.Model),
			"model": head.Model,
		}
	}
	// ?digests=0 is direct mode reading raw: the rough-words provenance
	// still attaches, but no digest is computed or billed — and the
	// direct cockpit gets the working-tree readout in the same fetch.
	raw := r.URL.Query().Get("digests") == "0"
	history := s.membraneHistory(root, transcript.History(head.Path), raw)
	var tree []treeFile
	if raw {
		tree = gitTree(head.Cwd)
	}
	s.mu.Lock()
	pending := s.says[root]
	queue := append([]queuedSay(nil), s.queues[root]...)
	var spent *agentSpend
	if sp := s.spend[root]; sp != nil {
		c := *sp
		spent = &c
	}
	var drives []run
	for _, rn := range s.runs {
		if rn.SessionID == root {
			drives = append(drives, *rn)
		}
	}
	s.mu.Unlock()
	writeJSON(w, map[string]any{
		"agent":     agent,
		"history":   history,
		"ctx":       ctx,
		"spend":     spent,
		"pending":   pending,
		"queue":     queue,
		"tree":      tree,
		"drives":    drives,
		"resume":    head.ID,
		"notice":    s.notice,
		"turns":     s.turns,
		"usage":     s.uc.Latest(),
		"artifacts": len(s.shelf.list(root)),
	})
}

// wireMsg is one history bubble with the membrane's artifacts riding
// along: the digest of an assistant turn, the rough words behind an
// expanded user turn.
type wireMsg struct {
	transcript.Msg
	Rough  string     `json:"rough,omitempty"`
	Digest *digestRec `json:"digest,omitempty"`
}

// How much of the tail gets the membrane's compression: recent turns
// are what the human is deciding on; ancient ones can be read raw by
// choice without billing the whole scrollback on first view.
const digestTail = 8

// digestGen salts the digest cache key: bump it when the digest
// prompt changes, so every cached and journaled digest regenerates
// under the new prompt instead of serving the old one forever.
const digestGen = "g2|"

// digestWorthy: short replies read faster raw than compressed.
func digestWorthy(m transcript.Msg) bool {
	return m.Role == "assistant" && len(strings.Fields(m.Text)) >= 120
}

// membraneHistory dresses the raw history: rough words attached to the
// user turns the membrane phrased, digests attached to recent long
// assistant turns — queued on first sight, filled in by the poll.
func (s *server) membraneHistory(root string, history []transcript.Msg, raw bool) []wireMsg {
	out := make([]wireMsg, len(history))
	worthy := 0
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		out[i] = wireMsg{Msg: m}
		if m.Role == "user" {
			s.mu.Lock()
			out[i].Rough = s.sent[textHash(m.Text)]
			s.mu.Unlock()
			continue
		}
		if raw || !digestWorthy(m) || worthy >= digestTail {
			continue
		}
		worthy++
		prompt := ""
		if i > 0 && history[i-1].Role == "user" {
			prompt = history[i-1].Text
		}
		out[i].Digest = s.digestFor(root, prompt, m.Text)
	}
	return out
}

// digestFor returns the cached digest for a reply, starting a worker
// on first sight. Pending is marked before the goroutine exists, so
// however many polls race, one call is billed.
func (s *server) digestFor(root, prompt, reply string) *digestRec {
	if s.llm == nil {
		return nil
	}
	h := textHash(digestGen + reply)
	s.mu.Lock()
	if rec := s.digests[h]; rec != nil {
		c := *rec
		s.mu.Unlock()
		return &c
	}
	rec := &digestRec{State: "pending"}
	s.digests[h] = rec
	s.mu.Unlock()
	go func() {
		headline, bullets, err := s.rootLLM(root).Digest(context.Background(), prompt, reply)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			rec.State = "failed"
			return
		}
		rec.State = "ready"
		rec.Headline = headline
		rec.Bullets = bullets
		// Journaled so a restart never re-bills this turn.
		appendLine(s.digestPath, digestLine{Hash: h, Headline: headline, Bullets: bullets, At: time.Now()})
	}()
	return &digestRec{State: "pending"}
}

// textHash identifies a turn by its text. FNV-1a: an identity for a
// map key, not a defense.
func textHash(s string) string {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%08x", uint32(h^(h>>32)))
}

// isCommand: a message that IS a claude command ("/compact") must
// reach claude exactly as typed — the membrane phrasing a slash
// command into prose would turn a verb into a paragraph.
func isCommand(text string) bool { return strings.HasPrefix(text, "/") }

// handleSay is the outbound membrane: the human's rough words are
// phrased by the rook agent (unless verbatim), the phrased message
// runs one headless turn, and BOTH texts stay on the record — the
// membrane never speaks invisibly.
func (s *server) handleSay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text     string   `json:"text"`
		Verbatim bool     `json:"verbatim"`
		Direct   bool     `json:"direct"` // direct mode: no membrane, ever
		Perm     string   `json:"perm"`   // "" | "read" | "edit" | "all"
		Images   []string `json:"images"` // paths handleUpload answered with
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
	permTools, permMode, permErr := permPolicy(req.Perm)
	if permErr != "" {
		httpErr(w, 400, permErr)
		return
	}
	now := time.Now()
	root, head := s.resolveAgent(r.PathValue("id"), now)
	if head == nil {
		httpErr(w, 404, "that agent is gone from the window")
		return
	}
	if msg := s.checkImages(root, req.Images); msg != "" {
		httpErr(w, 400, msg)
		return
	}
	expand := !req.Direct && !req.Verbatim && s.llm != nil && !isCommand(req.Text)
	s.mu.Lock()
	if j := s.says[root]; j != nil && (j.Status == "thinking" || j.Status == "phrasing") {
		// Direct mode types ahead: the message queues and lands when
		// this turn ends. The membrane path still answers busy —
		// phrasing against a moving transcript would invent context.
		if req.Direct {
			if len(s.queues[root]) >= queueCap {
				s.mu.Unlock()
				httpErr(w, 409, "the queue is full — three ahead is enough")
				return
			}
			if s.queues == nil {
				s.queues = map[string][]queuedSay{}
			}
			s.queues[root] = append(s.queues[root], queuedSay{Text: req.Text, Perm: req.Perm, Images: req.Images})
			s.mu.Unlock()
			writeJSON(w, map[string]string{"status": "queued"})
			return
		}
		s.mu.Unlock()
		httpErr(w, 409, "still working on the last message")
		return
	}
	if s.drivingLocked(root) {
		s.mu.Unlock()
		httpErr(w, 409, "a drive is running — stop it to chat")
		return
	}
	status := "thinking"
	if expand {
		status = "phrasing"
	}
	turnCtx, cancel := context.WithCancel(context.Background())
	job := &sayJob{Text: req.Text, Status: status, Direct: req.Direct, Perm: req.Perm, Images: req.Images, At: now, cancel: cancel}
	s.says[root] = job
	s.mu.Unlock()

	headID, cwd, headPath := head.ID, head.Cwd, head.Path
	go func() {
		defer cancel()
		sent := req.Text
		if expand {
			// The last exchange anchors the phrasing so its specifics
			// are real, not invented.
			lastPrompt, lastReply := "", ""
			for _, m := range transcript.History(headPath) {
				if m.Role == "user" {
					lastPrompt = m.Text
				} else {
					lastReply = m.Text
				}
			}
			exp, err := s.rootLLM(root).Expand(context.Background(), req.Text, lastPrompt, lastReply)
			if err != nil {
				s.mu.Lock()
				job.Status = "failed"
				job.Err = "the membrane could not phrase it: " + err.Error()
				s.mu.Unlock()
				return
			}
			sent = exp
			s.mu.Lock()
			job.Sent = sent
			job.Status = "thinking"
			// The provenance the history view hangs on: this delivered
			// text belongs to those rough words.
			s.sent[textHash(sent)] = req.Text
			s.mu.Unlock()
		}
		turner := &drive.Headless{Bin: s.claudeBin, Dir: cwd,
			AllowedTools: permTools, PermissionMode: permMode}
		turn, err := turner.RunTurn(turnCtx, headID, withImages(sent, req.Images))
		s.addSpend(root, turn.CostUSD, 0)
		s.mu.Lock()
		defer s.mu.Unlock()
		job.CostUSD = turn.CostUSD
		if err != nil {
			job.Status = "failed"
			job.Err = err.Error()
			if job.interrupted {
				job.Err = "interrupted — whatever landed stays in the transcript"
			}
			return
		}
		s.ln.advance(root, turn.SessionID)
		// The exchange is in the head's transcript now; the pending
		// bubble has nothing left to say.
		delete(s.says, root)
		go s.drainQueue(root)
	}()
	writeJSON(w, map[string]string{"status": status})
}

// drainQueue runs the next queued direct message, if the agent is
// free. Each landing drains the next — an interrupted or failed turn
// stops the chain, because the human plainly wants the wheel.
func (s *server) drainQueue(root string) {
	now := time.Now()
	s.mu.Lock()
	q := s.queues[root]
	if len(q) == 0 {
		s.mu.Unlock()
		return
	}
	if j := s.says[root]; j != nil && (j.Status == "thinking" || j.Status == "phrasing") {
		s.mu.Unlock()
		return
	}
	if s.drivingLocked(root) {
		s.mu.Unlock()
		return
	}
	next := q[0]
	s.queues[root] = q[1:]
	turnCtx, cancel := context.WithCancel(context.Background())
	job := &sayJob{Text: next.Text, Status: "thinking", Direct: true, Perm: next.Perm, Images: next.Images, At: now, cancel: cancel}
	s.says[root] = job
	s.mu.Unlock()

	_, head := s.resolveAgent(root, now)
	permTools, permMode, _ := permPolicy(next.Perm)
	go func() {
		defer cancel()
		if head == nil {
			s.mu.Lock()
			job.Status = "failed"
			job.Err = "the agent left the window before its queued message could land"
			s.mu.Unlock()
			return
		}
		turner := &drive.Headless{Bin: s.claudeBin, Dir: head.Cwd,
			AllowedTools: permTools, PermissionMode: permMode}
		turn, err := turner.RunTurn(turnCtx, head.ID, withImages(next.Text, next.Images))
		s.addSpend(root, turn.CostUSD, 0)
		s.mu.Lock()
		defer s.mu.Unlock()
		job.CostUSD = turn.CostUSD
		if err != nil {
			job.Status = "failed"
			job.Err = err.Error()
			if job.interrupted {
				job.Err = "interrupted — whatever landed stays in the transcript"
			}
			return
		}
		s.ln.advance(root, turn.SessionID)
		delete(s.says, root)
		go s.drainQueue(root)
	}()
}

// handleInterrupt kills the in-flight say turn — the TUI's Esc, for
// the web. The subprocess dies; the transcript keeps whatever landed;
// the session resumes cleanly on the next send.
func (s *server) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	root, head := s.resolveAgent(r.PathValue("id"), now)
	if head == nil {
		httpErr(w, 404, "that agent is gone from the window")
		return
	}
	s.mu.Lock()
	j := s.says[root]
	if j == nil || (j.Status != "thinking" && j.Status != "phrasing") || j.cancel == nil {
		s.mu.Unlock()
		httpErr(w, 409, "nothing in flight to interrupt")
		return
	}
	j.interrupted = true
	j.cancel()
	s.mu.Unlock()
	writeJSON(w, map[string]string{"status": "interrupting"})
}

func (s *server) driving(root string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drivingLocked(root)
}

func (s *server) drivingLocked(root string) bool {
	for _, r := range s.runs {
		if r.SessionID == root && !r.Finished {
			return true
		}
	}
	return false
}

func (s *server) handleDrive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Goal      string `json:"goal"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	if s.llm == nil {
		httpErr(w, 409, s.notice)
		return
	}
	req.Goal = strings.TrimSpace(req.Goal)
	if req.Goal == "" {
		httpErr(w, 400, "say what the drive should achieve")
		return
	}
	// The drive targets the agent (the root id); the turn resumes the
	// HEAD, where the conversation actually lives.
	root, head := s.resolveAgent(req.SessionID, time.Now())
	if head == nil {
		httpErr(w, 404, "that agent is gone from the window")
		return
	}
	s.mu.Lock()
	if s.drivingLocked(root) {
		s.mu.Unlock()
		httpErr(w, 409, "already driving that agent — stop that drive first")
		return
	}
	if j := s.says[root]; j != nil && j.Status == "thinking" {
		s.mu.Unlock()
		httpErr(w, 409, "still thinking about a chat message — one conversation at a time")
		return
	}
	id := make([]byte, 4)
	rand.Read(id)
	ctx, cancel := context.WithCancel(context.Background())
	rn := &run{
		ID: "drive-" + hex.EncodeToString(id), SessionID: root,
		SessionTitle: head.Title, Goal: req.Goal,
		Status: "starting", At: time.Now(), cancel: cancel,
	}
	s.runs = append([]*run{rn}, s.runs...)
	s.mu.Unlock()

	ll := *s.llm // copy, so the per-run spend meter is this run's
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
		res, err := loop.Run(ctx, headID, req.Goal)
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
	}()
	writeJSON(w, map[string]string{"id": rn.ID})
}

func (s *server) handleStop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req) != nil {
		httpErr(w, 400, "the request did not parse")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rn := range s.runs {
		if rn.ID == req.ID && !rn.Finished {
			rn.cancel()
			writeJSON(w, map[string]string{"status": "stopping"})
			return
		}
	}
	httpErr(w, 404, "that drive is not running")
}

func (s *server) update(id string, f func(*run)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if r.ID == id {
			f(r)
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
