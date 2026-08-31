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
//     policyTools and Registry.Own.
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

	"github.com/incantery/mote/mcp"
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
	// Dir is the profile directory: profile.md, policy.toml, and
	// mcp.toml if she has any servers.
	Dir string
	// Wait bounds an ask. Zero means askTimeout.
	Wait time.Duration

	registry *tool.Registry
	policy   *tool.Policy
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

	// servers is what mcp.toml declared, clients are the ones that
	// answered, and failed is why the others did not. See mcp.go.
	servers []mcp.Server
	clients []*mcp.Client
	failed  map[string]string
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
		Dir:       dir,
		Prompt:    strings.TrimSpace(prof.Prompt),
		Model:     strings.TrimSpace(prof.Model),
		registry:  reg,
		policy:    policy,
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
	// A rule keys on the argument that is the whole of the question.
	// It used to have to smuggle the verb out through Commander,
	// because a command prefix was the only thing a rule could see
	// inside a call; `when` is that, said plainly. It goes LAST, after
	// the file's own rules, so a person who writes a rule about
	// `fleet` in policy.toml overrides it — and the seeded file now
	// has one, so most people are overriding it with the same words.
	stop := tool.Rule{
		Tools:  []string{"fleet"},
		When:   map[string]string{"action": "stop"},
		Then:   tool.Ask,
		Reason: "stopping a task abandons the work in it — check they meant to",
	}
	// The same shape, for the same reason, on the list. Adding and
	// crossing off are what the list is for and both are reversible;
	// dropping takes the line out of the file, and the person cannot
	// get back a thing they can no longer see they have lost.
	drop := tool.Rule{
		Tools:  []string{"todo"},
		When:   map[string]string{"action": "drop"},
		Then:   tool.Ask,
		Reason: "dropping an item takes it off the list for good — crossing it off is the usual way",
	}
	out := make([]tool.Rule, 0, len(rules)+4)
	out = append(out, mine)
	at := 0
	for at < len(rules) && rules[at].Then == tool.Deny {
		out = append(out, rules[at])
		at++
	}
	out = append(out, ours)
	out = append(out, rules[at:]...)
	return append(out, stop, drop)
}

// policyTools is the default for the three tools the profile did not
// choose and cannot have listed.
//
// Handing work away is the thing Vera is FOR. The supervisor's own
// sentence is "you do not do the work; you decide what work there is,
// hand it to somebody who will" — so a delegation and a task run
// without asking, the way reading does. The list runs without asking
// for a plainer reason: it is a file in her own home, everything it
// does is visible in the next sentence she says, and a permission
// prompt between "remind me to call the bank" and the bank being on
// the list is a reason to stop using it. Dropping is the exception,
// and it is a rule above rather than a decision here.
//
// A file that DOES name them wins: this fills a gap, it does not
// overrule anybody.
func policyTools(tools map[string]tool.Decision) map[string]tool.Decision {
	if tools == nil {
		tools = map[string]tool.Decision{}
	}
	for _, name := range []string{"delegate", "fleet", "todo"} {
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
		if name == "profile.md" {
			// The example names a model as an example. Copied as is,
			// that placeholder outranks the model verad was started
			// with (the profile's hint wins over the flag's default),
			// and every reply is a 400. Her copy starts without one:
			// the person adds a line when they mean it.
			b = withoutModelLine(b)
		}
		if name == "policy.toml" {
			b = withFleetRule(b)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
		slog.Info("wrote the supervisor profile", "file", path)
	}
	return nil
}

// Adopt registers tools of Vera's own.
//
// The built-ins come from mote and the profile picks among them by
// name; these do not go through that gate. The delegate and the fleet
// are Vera — a profile that forgot to list them would be a Vera who
// cannot hand work to anybody — and the registry knows that word now:
// Own marks a tool as the harness's, and a `tools:` line narrows
// around it rather than through it.
//
// It used to build a second registry and re-add both lists in order,
// which was the only way to put a tool in front of the profile's. Own
// does that itself, so the registry a profile handed back is the one
// that is served from and nothing has to be rebuilt.
//
// Called at startup, before anything is served, and again by anything
// that adds tools later — the registry takes its own lock, and the
// definitions are recomputed from it.
func (h *Hands) Adopt(tools ...tool.Tool) error {
	if h == nil || len(tools) == 0 {
		return nil
	}
	if err := h.registry.Own(tools...); err != nil {
		return err
	}
	// Own appends, and mote puts what a harness owns in FRONT only
	// when a profile narrows the registry — so narrowing it to what is
	// already in it is how the order gets said out loud. It is one
	// call to mote's own function rather than the two-list rebuild
	// this used to be, and it happens once, at startup.
	var profile []string
	for _, t := range h.registry.List() {
		if !h.registry.Owns(t.Name()) {
			profile = append(profile, t.Name())
		}
	}
	reg, err := h.registry.Only(profile...)
	if err != nil {
		return err
	}
	h.registry = reg
	return nil
}

// Definitions is what the model is told it can reach for, in the shape
// the registry hands out — which is the shape every provider takes.
//
// Asked of the registry every time rather than kept, because the set
// changes while she is running: an MCP server that says its tool list
// changed writes to the registry from a goroutine of its own, and a
// cached copy would go on describing tools that are gone. It is once
// per exchange over a handful of tools, and the caller keeps the
// answer for the whole of a tool loop — so the model is told one
// stable list per exchange, which is what it should be told.
func (h *Hands) Definitions() []tool.Definition {
	if h == nil {
		return nil
	}
	return h.registry.Definitions()
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

// keyConversation is Vera's own Handle value: which conversation a
// call belongs to, for a tool that logs about itself. mote documents
// tool.Device and tool.Cwd and leaves the rest of the map to the
// harness, so this word never has to reach it.
const keyConversation = "conversation"

// looking is the directory the person is looking at on that device —
// "the repo in front of them", which a tool cannot learn any other
// way. Empty when nothing has been reported, which is a tool's cue to
// ask rather than to guess.
func (m *Mind) looking(device string) string {
	if m == nil || m.Attention == nil {
		return ""
	}
	return m.Attention.TerminalPath(device)
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
	res, err := t.Run(ctx, c.Args, tool.Handle{
		Output: &toolStream{id: call.ID, reply: reply},
		Status: func(text string) {
			// A status IS something appearing, so it stops the "first
			// sign" clock even though no token has been produced.
			x.sign(ctx)
			_ = reply(Frame{Status: text})
		},
		Values: map[string]any{
			tool.Device: device,
			// The repository in front of them, which is what a fleet
			// start with no project named means. It is what the
			// harness knows and the arguments do not say.
			tool.Cwd: m.looking(device),
			// Vera's own keys; mote documents the two above and lets a
			// harness add what it knows.
			keyConversation: conversation,
			// The pictures that came with the message. A tool that
			// hands work to somebody with eyes passes them on; every
			// other tool ignores them. See images.go.
			keyImages: strings.Join(imagesOn(ctx), "\n"),
		},
	})
	elapsed := time.Since(started)
	// What the call reached, out of the tool's own Meta: the journal
	// keeps it beside the round, and the phone's result card shows
	// the cost. The model never sees any of it.
	x.link(res.Meta)
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

// withFleetRule adds the one rule mote's example cannot write: the
// fleet is Vera's tool, not the profile's, and mote has never heard
// of it.
//
// It is seeded rather than only built in so that the file a person
// reads says what actually happens. policyRules appends the same rule
// behind everything the file says, for a home written before this
// existed; a file that has this one hits it first and the built-in
// never fires. Both say the same thing, so which one decided is
// invisible until the person edits theirs — which is the point.
func withFleetRule(b []byte) []byte {
	return append(b, []byte(`
# Stopping a task abandons the work in it. Every other fleet verb adds
# something or reports something; this is the one that subtracts, so
# it is the one to be asked about. `+"`when`"+` matches an argument by
# name — which is how one tool with several verbs gets several
# answers.
[[rules]]
tools = ["fleet"]
when = { action = "stop" }
then = "ask"
reason = "stopping a task abandons the work in it — check they meant to"

# The same, on the to-do list. Adding and crossing off are what it is
# for; dropping takes the line out of the file.
[[rules]]
tools = ["todo"]
when = { action = "drop" }
then = "ask"
reason = "dropping an item takes it off the list for good — crossing it off is the usual way"
`)...)
}

// withoutModelLine drops a `model:` line from a profile's front matter.
func withoutModelLine(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	out := lines[:0]
	for i, l := range lines {
		if i > 0 && i < 8 && strings.HasPrefix(l, "model:") {
			continue
		}
		out = append(out, l)
	}
	return []byte(strings.Join(out, "\n"))
}
