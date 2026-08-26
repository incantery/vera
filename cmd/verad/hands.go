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
// Three things are decided here rather than in the file, and each one
// is a thing the file cannot know:
//
//   - where her home actually is ($VERA_HOME moves it, and the file
//     says `~/vera`),
//   - which repositories are projects (the fleet knows; the file has a
//     list that was true when it was written),
//   - that she may not edit the profile that governs her. A rule she
//     can rewrite is not a rule. See policyRules below.
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
	// Wait bounds an ask. Zero means askTimeout.
	Wait time.Duration

	registry *tool.Registry
	policy   *tool.Policy
	defs     []map[string]any
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

	h := &Hands{
		Root:      root,
		Prompt:    strings.TrimSpace(prof.Prompt),
		registry:  reg,
		policy:    policy,
		defs:      definitionMaps(reg.Definitions()),
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
	out := make([]tool.Rule, 0, len(rules)+2)
	out = append(out, mine)
	at := 0
	for at < len(rules) && rules[at].Then == tool.Deny {
		out = append(out, rules[at])
		at++
	}
	out = append(out, ours)
	return append(out, rules[at:]...)
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

// definitionMaps is the registry in the shape the request body and the
// generation record already speak. Built once: the built-ins do not
// change while the process runs.
func definitionMaps(defs []tool.Definition) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		var params any
		if len(d.Function.Parameters) > 0 {
			if err := json.Unmarshal(d.Function.Parameters, &params); err != nil {
				continue
			}
		}
		out = append(out, map[string]any{
			"type": d.Type,
			"function": map[string]any{
				"name":        d.Function.Name,
				"description": d.Function.Description,
				"parameters":  params,
			},
		})
	}
	return out
}

// Definitions is what the model is told it can reach for.
func (h *Hands) Definitions() []map[string]any {
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

// --- one call, decided and run -----------------------------------------

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

// invokeTool runs one built-in: decided by the policy, asked about if
// the policy says so, and streamed while it runs.
//
// Everything it returns is for the model, including the refusals. A
// denial is not an error — it is the thing the model most needs to
// know, in the profile's own words, so that it does the allowed thing
// instead of trying the same call again.
func (m *Mind) invokeTool(ctx context.Context, conversation string, x *exchange, t tool.Tool, call toolCall, reply func(Frame) error) string {
	c := tool.NewCall(call.ID, t, jsonArgs(call.Function.Arguments))
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
		_ = reply(Frame{Ask: &AskFrame{ID: call.ID, Name: c.Tool, Args: string(c.Args), Text: verdict.Reason}})
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

	res, err := t.Run(ctx, c.Args, &toolStream{id: call.ID, reply: reply})
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
