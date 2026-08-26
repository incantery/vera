// Vera's hands: mote's tools, under the supervisor's policy.
//
// Until now the only thing Vera could do was hand work to somebody
// else — a delegate for a minute's job, a fleet task for a repository.
// She could not open a file. That is the gap this closes: read, list,
// search, write, edit and run, from mote, decided by a policy that
// lives in her home as a file a person can read and edit.
//
// The boundary is the point, and it is the same one the delegate drew.
// She may look at anything, and she may write in her own home. She may
// NOT change a project: that goes to a task in its own copy of the
// repository, and the policy says so in the profile's own sentence —
// "start a task for that" — which is what the model is told when it
// tries. Anywhere else she asks, and the question goes to the phone.
//
// Four things are decided here rather than in the file, and each one
// is a thing the file cannot know:
//
//   - where her home actually is ($VERA_HOME moves it, and the file
//     says `~/vera`),
//   - which repositories are projects (the fleet knows; the file has a
//     list that was true when it was written),
//   - that she may not edit the profile that governs her. A rule she
//     can rewrite is not a rule. See policyRules below,
//   - what to say about the two tools that are not mote's. The
//     delegate and the fleet are Vera rather than the profile's to
//     choose, and a file that never heard of them would send every
//     one of those calls to the phone to be asked about. See
//     policyTools and Adopt.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/incantery/mote/profile"
	"github.com/incantery/mote/profiles"
	"github.com/incantery/mote/provider"
	"github.com/incantery/mote/tool"
	"github.com/incantery/mote/tool/builtin"
	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
)

// askTimeout is how long a question waits before it answers itself.
// A person who has put the phone down has not said yes, and a tool
// call parked forever holds the exchange — and its model context —
// open behind it.
const askTimeout = 2 * time.Minute

// Hands is the registry, the policy, and the questions in flight.
type Hands struct {
	// Root is her home: what the tools resolve a relative path
	// against, and what the policy calls `~/vera`.
	Root string
	// Prompt is the profile's own words, appended to Vera's voice.
	Prompt string
	// Model is the profile's `model:` line — a hint about which model
	// suits this agent, and what verad asks when nobody said --model.
	// Empty is fine: then the flag's default is the answer.
	Model string
	// Wait bounds an ask. Zero means askTimeout.
	Wait time.Duration

	registry *tool.Registry
	policy   *tool.Policy
	defs     []tool.Definition
	// profile is what the profile chose, in its order; own is what
	// Vera brought — the delegate, the fleet — which goes in front of
	// it. Kept apart so Adopt can rebuild the registry in that order.
	profile []tool.Tool
	own     []tool.Tool
	// fileRoots are the roots policy.toml itself listed. The fleet's
	// are added to them rather than replacing them: a repository the
	// fleet has not noticed yet is still not hers to edit.
	fileRoots []string

	// Projects is the fleet's view of what repositories exist. Nil is
	// fine — then the file's roots are all there are.
	Projects *fleet.Projects

	mu sync.Mutex
	// gates are per conversation, because an "always" is an answer
	// given in a conversation and should not outlive it.
	gates map[string]*gate
	// asking maps a call id to the gate waiting on it, so an answer
	// arriving over HTTP finds the question without searching.
	asking map[string]*tool.Gate
	// choices remembers what was answered, for the journal: Wait
	// reports whether the call may run, not which word was said.
	choices map[string]string
}

// gate is one conversation's gate and when it was last used, so the
// map is bounded the way History's is.
type gate struct {
	g       *tool.Gate
	touched time.Time
}

// maxGates bounds the per-conversation gates. A phone that reinstalls
// mints a new conversation id, so this is a real ceiling.
const maxGates = 200

// openHands reads the profile out of her home — writing mote's worked
// example there first if there is none — and builds the tools from it.
func openHands(root string, projects *fleet.Projects) (*Hands, error) {
	dir := filepath.Join(root, filepath.FromSlash(home.ProfileDir))
	if err := seedProfile(dir); err != nil {
		return nil, err
	}
	prof, err := profile.Load(dir)
	if err != nil {
		return nil, err
	}
	reg, err := prof.Registry(builtin.Registry(root))
	if err != nil {
		return nil, err
	}
	policy := prof.Policy
	// The same directory the tools resolve against, or a rule and a
	// tool disagree about what "notes.md" means.
	policy.Dir = root
	policy.Rules = policyRules(policy.Rules, root)
	policy.Tools = policyTools(policy.Tools)

	h := &Hands{
		Root:      root,
		Prompt:    strings.TrimSpace(prof.Prompt),
		Model:     strings.TrimSpace(prof.Model),
		registry:  reg,
		policy:    policy,
		defs:      reg.Definitions(),
		profile:   reg.List(),
		fileRoots: append([]string(nil), policy.Roots...),
		Projects:  projects,
		gates:     map[string]*gate{},
		asking:    map[string]*tool.Gate{},
		choices:   map[string]string{},
	}
	return h, nil
}

// policyRules puts the two rules the file cannot write in front of and
// behind the ones it can.
//
// The first is a deny: her profile is not hers to edit. `~/vera/**` is
// allowed to her, and her profile lives under `~/vera`, so without
// this she can answer a policy she dislikes by rewriting it — a
// privilege escalation that survives a restart and looks, in the
// journal, like an ordinary allowed write. It goes with the file's own
// denies, at the front.
//
// The second is the allow for her home, by its real path. The file
// says `~/vera` because that is where it is; $VERA_HOME can put it
// somewhere else, and then the file's rule matches nothing. It goes
// after every deny, so a project root inside her home would still be a
// project.
func policyRules(rules []tool.Rule, root string) []tool.Rule {
	mine := tool.Rule{
		Tools: []string{"write", "edit"},
		Paths: []string{filepath.Join(root, filepath.FromSlash(home.ProfileDir)) + string(filepath.Separator) + "**"},
		Then:  tool.Deny,
		// Said as a thing to do rather than a wall, because this is
		// what the model is told when it tries.
		Reason: "your profile is theirs to change, not yours — say what you would change and why",
	}
	ours := tool.Rule{
		Tools:  []string{"write", "edit"},
		Paths:  []string{root + string(filepath.Separator) + "**"},
		Then:   tool.Allow,
		Reason: "her own home",
	}
	// Stopping a task abandons the work in it. Every other verb the
	// fleet has adds something or reports something; this is the one
	// that subtracts, so it is the one she asks about.
	//
	// It is expressed the only way a rule can key on an argument: the
	// fleet says its verb as its Command, and a rule matches a command
	// by prefix. It goes LAST, after the file's own rules, so a person
	// who writes a rule about `fleet` in policy.toml overrides it.
	stop := tool.Rule{
		Tools:    []string{"fleet"},
		Commands: []string{"stop"},
		Then:     tool.Ask,
		Reason:   "stopping a task abandons the work in it — check they meant to",
	}
	out := make([]tool.Rule, 0, len(rules)+3)
	out = append(out, mine)
	at := 0
	for at < len(rules) && rules[at].Then == tool.Deny {
		out = append(out, rules[at])
		at++
	}
	out = append(out, ours)
	out = append(out, rules[at:]...)
	return append(out, stop)
}

// policyTools is the default for the two tools the profile did not
// choose and cannot have listed.
//
// Handing work away is the thing Vera is FOR. The supervisor's own
// sentence is "you do not do the work; you decide what work there is,
// hand it to somebody who will" — so a delegation and a task run
// without asking, the way reading does. A file that DOES name them
// wins: this fills a gap, it does not overrule anybody.
func policyTools(tools map[string]tool.Decision) map[string]tool.Decision {
	if tools == nil {
		tools = map[string]tool.Decision{}
	}
	for _, name := range []string{"delegate", "fleet"} {
		if _, said := tools[name]; !said {
			tools[name] = tool.Allow
		}
	}
	return tools
}

// seedProfile writes mote's worked example into her home the first
// time, and never again: after that the file is the person's.
func seedProfile(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"profile.md", "policy.toml"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		b, err := fs.ReadFile(profiles.FS(), "supervisor/"+name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
		slog.Info("wrote the supervisor profile", "file", path)
	}
	return nil
}

// Adopt registers tools of Vera's own, in front of the profile's.
//
// The built-ins come from mote and the profile picks among them by
// name; these do not go through that gate. The delegate and the fleet
// are Vera — a profile that forgot to list them would be a Vera who
// cannot hand work to anybody — and they go first because handing
// work away is what she should reach for before doing it herself.
//
// Called at startup, before anything is served: the registry and the
// definitions are read without a lock on every exchange.
func (h *Hands) Adopt(tools ...tool.Tool) error {
	if h == nil || len(tools) == 0 {
		return nil
	}
	h.own = append(h.own, tools...)
	reg := &tool.Registry{}
	for _, t := range append(append([]tool.Tool{}, h.own...), h.profile...) {
		if err := reg.Add(t); err != nil {
			return err
		}
	}
	h.registry = reg
	h.defs = reg.Definitions()
	return nil
}

// Definitions is what the model is told it can reach for, in the shape
// the registry hands out — which is the shape every provider takes.
func (h *Hands) Definitions() []tool.Definition {
	if h == nil {
		return nil
	}
	return h.defs
}

// Names is the tools she has, for the startup banner.
func (h *Hands) Names() []string {
	if h == nil {
		return nil
	}
	out := make([]string, 0, len(h.registry.List()))
	for _, t := range h.registry.List() {
		out = append(out, t.Name())
	}
	return out
}

// Tool finds one by the name the model used.
func (h *Hands) Tool(name string) (tool.Tool, bool) {
	if h == nil {
		return nil, false
	}
	return h.registry.Get(name)
}

// Where is the sentence the profile cannot write, and the one a live
// model needed: where her home actually is.
//
// Told only "you keep your own notes under ~/vera", a model reads `~`
// as the machine's home directory and looks in the wrong place — and
// then reports, honestly and uselessly, that the file is not there.
// Her home may be anywhere ($VERA_HOME), and a bare path lands in it.
func (h *Hands) Where() string {
	if h == nil {
		return ""
	}
	return "\n\nYour own home is the directory " + h.Root + ", and that is what your tools " +
		"resolve a bare path against: `notes/today.md` is a file in your home. A leading `~` is " +
		"THEIR home directory on this machine, which is a different place — say " + h.Root +
		" when you mean yours. Your commands run there too unless you say otherwise.\n" +
		"A tool that was refused, denied, or answered no did nothing: say so plainly. Never report a " +
		"change you did not make — if the file is still there, it is still there."
}

// Refresh points the policy's ${root} at the repositories the fleet
// knows about, on top of the ones the file listed. Called before every
// exchange; it logs only when the set actually changed.
func (h *Hands) Refresh(ctx context.Context) {
	if h == nil || h.Projects == nil {
		return
	}
	seen := map[string]bool{}
	roots := append([]string(nil), h.fileRoots...)
	for _, r := range h.fileRoots {
		seen[h.policy.CleanPath(r)] = true
	}
	for _, repo := range h.Projects.Known(ctx) {
		if repo.Root == "" || seen[h.policy.CleanPath(repo.Root)] {
			continue
		}
		seen[h.policy.CleanPath(repo.Root)] = true
		roots = append(roots, repo.Root)
	}
	sort.Strings(roots[len(h.fileRoots):])

	h.mu.Lock()
	defer h.mu.Unlock()
	if same(h.policy.Roots, roots) {
		return
	}
	h.policy.Roots = roots
	slog.Info("project roots", "count", len(roots), "roots", strings.Join(roots, " "))
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Decide answers a call in the named conversation. The lock is held
// across the decision because Refresh writes the roots it reads —
// deciding is pure and cheap, so this costs nothing worth measuring.
func (h *Hands) Decide(conversation string, c tool.Call) (tool.Verdict, *tool.Gate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	g := h.gateLocked(conversation)
	return g.Decide(c), g
}

// Asking records that a call is waiting, so an answer over HTTP can
// find it. Done before the frame goes out, so an answer that arrives
// immediately is not lost.
func (h *Hands) Asking(id string, g *tool.Gate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asking[id] = g
}

// Answered closes the question and reports what was said, which is
// what the journal records. Wait tells the caller whether the call may
// run; it does not say which word did it.
func (h *Hands) Answered(id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.asking, id)
	choice := h.choices[id]
	delete(h.choices, id)
	return choice
}

// Answer is the person's word, off the wire. Its signature is
// agent.Answerer's, which is what the terminal calls.
func (h *Hands) Answer(ctx context.Context, id, choice string) error {
	h.mu.Lock()
	g, ok := h.asking[id]
	if ok {
		h.choices[id] = choice
	}
	h.mu.Unlock()
	if !ok {
		return errors.New("nothing is waiting on that")
	}
	return g.Answer(ctx, id, choice)
}

// gateLocked is the conversation's gate, made on first use. Called
// with the lock.
func (h *Hands) gateLocked(conversation string) *tool.Gate {
	if h.gates == nil {
		h.gates = map[string]*gate{}
	}
	if g, ok := h.gates[conversation]; ok {
		g.touched = time.Now()
		return g.g
	}
	h.evictLocked()
	g := &gate{g: &tool.Gate{Policy: h.policy}, touched: time.Now()}
	h.gates[conversation] = g
	return g.g
}

// evictLocked drops the stalest conversation's grants when there are
// too many. An "always" is worth remembering for a conversation, not
// for the life of the process.
func (h *Hands) evictLocked() {
	for len(h.gates) >= maxGates {
		var oldest string
		var when time.Time
		for id, g := range h.gates {
			if oldest == "" || g.touched.Before(when) {
				oldest, when = id, g.touched
			}
		}
		delete(h.gates, oldest)
	}
}

// waitFor is Wait with the ask timeout applied. It returns the answer
// and whether the exchange itself is still alive: a timeout answers
// no, a cancelled exchange answers nothing.
func (h *Hands) waitFor(ctx context.Context, g *tool.Gate, c tool.Call) (ok, alive bool) {
	wait := h.Wait
	if wait <= 0 {
		wait = askTimeout
	}
	inner, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	ok, err := g.Wait(inner, c)
	if err != nil {
		return false, ctx.Err() == nil
	}
	return ok, true
}

// maxToolStream bounds what a running tool is allowed to push onto the
// wire. The result is capped separately; this is about a command that
// prints a megabyte and a phone that has to read it.
const maxToolStream = 32 << 10

// toolStream turns a tool's output into frames as it is written, and
// stops sending once it has sent enough. It never errors: a tool that
// cannot narrate itself should still finish.
type toolStream struct {
	id    string
	reply func(Frame) error
	sent  int
}

func (w *toolStream) Write(p []byte) (int, error) {
	if room := maxToolStream - w.sent; room > 0 && len(p) > 0 {
		text := string(p)
		if len(text) > room {
			text = text[:room] + "\n…(the rest is in the result)"
		}
		w.sent += len(text)
		_ = w.reply(Frame{ToolOutput: &ToolOutputFrame{ID: w.id, Text: text}})
	}
	return len(p), nil
}

// --- the round, as a tool sees it ------------------------------------

// round is the one call in progress, carried in its context.
//
// mote's Run takes arguments and a writer and gives back text. Three
// things Vera's own tools need are not in that shape: which device
// asked (a fleet start with no project means the repository in front
// of them), a line to put on the phone before there is any result,
// and what the call reached — a task id, a Claude Code session, what
// it cost — which the journal keeps beside the round. They ride in
// the context, the one thing that is already per-call. If mote's
// Result ever carries more than text, most of this goes away.
type round struct {
	// conversation is which conversation this call belongs to, for a
	// tool that logs about itself.
	conversation string
	// device is which of the person's devices asked.
	device string
	// say puts a line on the phone, in Vera's voice, while the work
	// happens.
	say func(string)
	// link ties this round to what it reached.
	link func(task, session string, cost float64)
}

type roundKey struct{}

func withRound(ctx context.Context, r *round) context.Context {
	return context.WithValue(ctx, roundKey{}, r)
}

// roundOf is the call in progress, or a round that goes nowhere — so
// a tool run from a test, or from anywhere that is not an exchange,
// needs no ceremony.
func roundOf(ctx context.Context) *round {
	if r, ok := ctx.Value(roundKey{}).(*round); ok && r != nil {
		return r.filled()
	}
	return (&round{}).filled()
}

func (r *round) filled() *round {
	if r.say == nil {
		r.say = func(string) {}
	}
	if r.link == nil {
		r.link = func(string, string, float64) {}
	}
	return r
}

// --- one call, decided and run -----------------------------------------

// invokeTool runs one tool: decided by the policy, asked about if the
// policy says so, and streamed while it runs. It is the only path —
// the fleet and the delegate come through here too, so that what is
// allowed, what is shown and what is written down is decided in one
// place for everything she can do.
//
// Everything it returns is for the model, including the refusals. A
// denial is not an error — it is the thing the model most needs to
// know, in the profile's own words, so that it does the allowed thing
// instead of trying the same call again.
func (m *Mind) invokeTool(ctx context.Context, conversation, device string, x *exchange, t tool.Tool, call provider.Call, reply func(Frame) error) string {
	c := tool.NewCall(call.ID, t, jsonArgs(call.Arguments))
	verdict, g := m.Hands.Decide(conversation, c)

	switch verdict.Decision {
	case tool.Deny:
		x.decided(tool.Deny, "", verdict.Reason)
		slog.Info("tool denied", "gen_ai.conversation.id", conversation,
			"tool", c.Tool, "rule", verdict.Rule, "reason", verdict.Reason)
		return "Not allowed: " + verdict.Reason

	case tool.Ask:
		// A question on the screen is something appearing, so it stops
		// the "first sign" clock the way a status line does.
		x.sign(ctx)
		m.Hands.Asking(call.ID, g)
		// A status beside the ask, for a client that does not know the
		// frame yet. The phone ignores unknown fields, so without this
		// an ask there is two minutes of silence that reads as broken
		// — and then a no it never saw the question for.
		_ = reply(Frame{Status: "Waiting for you: " + c.Tool + "…"})
		_ = reply(Frame{Ask: &AskFrame{ID: call.ID, Name: c.Tool,
			Args: trim(string(c.Args), maxRecordedArgs), Text: verdict.Reason}})
		ok, alive := m.Hands.waitFor(ctx, g, c)
		choice := m.Hands.Answered(call.ID)
		switch {
		case !alive:
			x.decided(tool.Ask, "", verdict.Reason)
			return "The exchange ended before they answered."
		case choice == "":
			// Nobody answered inside the window. Silence is not
			// consent, and a call parked forever holds the exchange
			// open behind it.
			x.decided(tool.Ask, tool.No, verdict.Reason)
			slog.Info("tool ask timed out", "gen_ai.conversation.id", conversation, "tool", c.Tool)
			return "They did not answer, so no. Ask them in words if it matters."
		case !ok:
			x.decided(tool.Ask, choice, verdict.Reason)
			return "They said no."
		default:
			x.decided(tool.Ask, choice, verdict.Reason)
		}

	default:
		x.decided(verdict.Decision, "", verdict.Reason)
	}

	started := time.Now()
	// The tool execution is its own thing on the record, and its span
	// is what a delegate's own telemetry hangs off — so the context
	// the tool runs in is this one.
	ctx, rec := m.beginTool(ctx, conversation, call)
	res, err := t.Run(withRound(ctx, &round{
		conversation: conversation,
		device:       device,
		say: func(text string) {
			// A status IS something appearing, so it stops the "first
			// sign" clock even though no token has been produced.
			x.sign(ctx)
			_ = reply(Frame{Status: text})
		},
		link: x.link,
	}), c.Args, &toolStream{id: call.ID, reply: reply})
	elapsed := time.Since(started)
	m.endTool(ctx, rec, c.Tool, x.pending.CostUSD, elapsed, err)

	// One line per call, whatever the tool was. Both what was asked
	// and what came back are in here on purpose: the question being
	// debugged next week is "why did it do that".
	slog.Info("tool",
		"gen_ai.conversation.id", conversation,
		"gen_ai.tool.name", c.Tool,
		"args", trim(string(c.Args), 300),
		"result", trim(res.Text, 300),
		"decision", string(verdict.Decision),
		"rule", verdict.Rule,
		"task", x.pending.Task,
		"session", x.pending.Session,
		"cost_usd", x.pending.CostUSD,
		"took_ms", elapsed.Milliseconds(),
		"error", errText(err),
	)

	if err != nil {
		// mote's convention, and the one the terminal already marks a
		// card failed on.
		return "error: " + err.Error()
	}
	return trim(res.Text, 8000)
}

// jsonArgs keeps an empty argument list from reaching a tool as the
// empty string, which is not JSON.
func jsonArgs(s string) json.RawMessage {
	if strings.TrimSpace(s) == "" {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(s)
}
