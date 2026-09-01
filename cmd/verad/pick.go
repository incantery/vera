// Which model answers this exchange, and where that was decided.
//
// verad used to be handed one model at startup and ask it for the rest
// of its life. That is the wrong shape for the question people actually
// have — "would opus have got that right?" — because the only way to
// ask it was to restart the daemon and lose the conversation.
//
// So the model is now a property of the exchange rather than of the
// process. Six things can say what it should be, and they are ranked
// so that the most specific statement wins:
//
//	this conversation  (`/model opus` — remembered, and it sticks)
//	this message       (`vera say -m opus` — one exchange only)
//	--model            (what this daemon was started with)
//	the saved default  (Enter in the `/model` picker — kept, see saved.go)
//	the profile        (profiles/supervisor/profile.md `model:`)
//	the built-in default
//
// The profile's line used to outrank the flag. It does not any more:
// a profile is a default about what this agent is, and a flag is
// somebody typing right now. The saved default is between the two for
// the same reason: it is a person overruling the profile, and it is
// not somebody typing right now.
//
// verad is the single writer. The terminal never keeps its own idea of
// which model a conversation is on — it asks, and it is told.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/incantery/mote/provider"
	"github.com/incantery/vera/responses"
)

// Where a choice came from, in the words `/model` prints.
const (
	fromConversation = "this conversation"
	fromMessage      = "this message"
	fromFlag         = "the --model flag"
	fromEffortFlag   = "the --effort flag"
	fromSaved        = "the saved default"
	fromBuiltin      = "the built-in default"
)

// Choice is one model, ready to be asked: the provider that reaches
// it, which wire that turned out to be, and what to ask it for.
//
// Effort and Thinking are not free-standing settings — what they may
// be depends on the vendor, and tune is the one place that is decided.
type Choice struct {
	Model    string
	Vendor   string
	Provider provider.Provider
	Effort   provider.Effort
	Thinking provider.Thinking

	// Where each half came from, for `/model` to print.
	ModelFrom  string
	EffortFrom string
}

// Resolution is a Choice as a client reads it: no provider, no
// thinking workaround, just what is in force and who said so.
type Resolution struct {
	Model      string `json:"model"`
	Effort     string `json:"effort,omitempty"`
	Provider   string `json:"provider,omitempty"`
	ModelFrom  string `json:"model_from,omitempty"`
	EffortFrom string `json:"effort_from,omitempty"`
}

func (c Choice) Resolution() Resolution {
	return Resolution{
		Model:      c.Model,
		Effort:     string(c.Effort),
		Provider:   c.Vendor,
		ModelFrom:  c.ModelFrom,
		EffortFrom: c.EffortFrom,
	}
}

// Line is the resolution in one phrase, for a status line or a note.
func (r Resolution) Line() string {
	if r.Effort == "" {
		return r.Model
	}
	return r.Model + " · " + r.Effort
}

// Says is where each half came from, in a sentence.
func (r Resolution) Says() string {
	if r.ModelFrom == r.EffortFrom || r.EffortFrom == "" {
		return "from " + r.ModelFrom
	}
	return "model from " + r.ModelFrom + ", effort from " + r.EffortFrom
}

// tune is the vendor's half of the question, and the one place it is
// answered — startup and every exchange after it come through here.
//
// The OpenAI-compatible endpoint verad was written against refuses
// function tools unless reasoning is off. That was found at the socket
// rather than in a doc, so it stays: reasoning off, effort none, unless
// somebody actually typed an effort, because an explicit dial is a
// stronger statement than a workaround for a different model. The
// Anthropic side leaves Thinking empty and thinks adaptively.
//
// Nothing here changed when the responses wire arrived, and that is
// deliberate. Thinking is a chat-completions field and package
// responses ignores it — there, reasoning is turned off by asking for
// effort none, which is what an unasked-for exchange already sends. So
// the default costs what it always cost, and the dial is what somebody
// typed.
func tune(vendor, effort string, explicit bool) (provider.Effort, provider.Thinking) {
	if vendor == "anthropic" {
		return provider.Effort(effort), ""
	}
	if explicit {
		return provider.Effort(effort), provider.ThinkingOff
	}
	return provider.Effort("none"), provider.ThinkingOff
}

// vendorOf is whose wire this is. The concrete type is the answer;
// there is no interface method for it, and a name is what the banner,
// the telemetry label and the journal all want. It is the VENDOR and
// not the API: a responses.Wire is OpenAI the same way an OpenAI-shaped
// chat endpoint is, and nothing downstream of here wants to be told
// which of the two paths the bytes took.
func vendorOf(p provider.Provider) string {
	if _, ok := p.(*provider.Anthropic); ok {
		return "anthropic"
	}
	return "openai"
}

// Wires builds a provider per model name and keeps it.
//
// It is the one function that knows a claude-* name goes to the
// Messages API and everything else to an OpenAI-compatible
// /chat/completions — mote decides that from the name, and this is
// where verad asks. The one thing verad decides for itself is the
// second OpenAI wire: a model whose table row says `openai/responses`
// is reached through package responses instead, because that is where
// the reasoning dial survives having tools in the request. Providers
// are cached because a model switched back and forth should not mint a
// new HTTP client each time.
type Wires struct {
	OpenAIKey  string
	OpenAIBase string

	mu   sync.Mutex
	made map[string]*madeWire
}

type madeWire struct {
	p      provider.Provider
	vendor string
}

// For reaches a model, building the provider the first time.
func (w *Wires) For(model string) (provider.Provider, string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, "", errors.New("no model")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if got, ok := w.made[model]; ok {
		return got.p, got.vendor, nil
	}
	if p, ok := w.responses(model); ok {
		if w.made == nil {
			w.made = map[string]*madeWire{}
		}
		w.made[model] = &madeWire{p: p, vendor: "openai"}
		return p, "openai", nil
	}
	p, err := provider.New(provider.Config{
		Model:      model,
		OpenAIKey:  w.OpenAIKey,
		OpenAIBase: w.OpenAIBase,
	})
	if err != nil {
		return nil, "", err
	}
	if w.made == nil {
		w.made = map[string]*madeWire{}
	}
	w.made[model] = &madeWire{p: p, vendor: vendorOf(p)}
	return p, vendorOf(p), nil
}

// responses is the second OpenAI wire, when the table asks for it and
// this machine has something to point it at.
//
// A row that asks for it on a machine with no OpenAI key and no base
// URL is NOT an error here: it falls through to mote, which owns the
// sentence about which key was missing and is also where a claude key
// and an unrecognised name still find each other.
func (w *Wires) responses(model string) (provider.Provider, bool) {
	row, ok := rowFor(model)
	if !ok || row.Wire != wireResponses || row.Provider != "openai" {
		return nil, false
	}
	key := firstSet(w.OpenAIKey, os.Getenv(provider.EnvOpenAIKey))
	base := firstSet(w.OpenAIBase, os.Getenv(provider.EnvOpenAIBase))
	if key == "" && base == "" {
		return nil, false
	}
	r := responses.New(base, key)
	r.Model = model
	return r, true
}

// firstSet is the first of these that somebody actually set.
func firstSet(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Pick is what a conversation was told to use. Either half may be
// empty, and an empty half is one nobody has said anything about:
// `/model opus` with no effort leaves the effort exactly where it was,
// and `/effort high` with no model leaves the model.
type Pick struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

func (p Pick) empty() bool { return p.Model == "" && p.Effort == "" }

// settle folds a named half into what was already chosen. The model and
// the effort are two separate toggles — the card that moves one of them
// says nothing about the other — so a half left empty is left where it
// was rather than cleared. Both empty is the one exception, and it is
// how a choice is given back: it means "forget this, follow the
// daemon".
func settle(now Pick, model, effort string) Pick {
	if model == "" && effort == "" {
		return Pick{}
	}
	want := now
	if model != "" {
		want.Model = model
	}
	if effort != "" {
		want.Effort = effort
	}
	return want
}

// agree checks a settled choice before it is written down, and is the
// one place the two toggles have to be considered together.
//
// A model with no wire on this machine is refused here rather than on
// the next thing said. So is an effort the model will not take — but
// only when somebody just asked for that effort. An effort carried over
// from a previous choice onto a model with no dial at all is dropped
// instead, because moving onto gpt-5.6-luna is not a mistake to be
// refused: it is a model where the dial does not apply, and the honest
// answer is to stop applying it.
func (m *Mind) agree(want Pick, saidEffort bool) (Pick, error) {
	if want.empty() {
		return want, nil
	}
	// Resolve against a clean slate so the model named is the one
	// checked, not whatever the conversation was already on.
	c, err := m.choose("", Pick{Model: want.Model, Effort: want.Effort})
	if err != nil {
		return Pick{}, err
	}
	if err := takesEffort(c.Model, want.Effort); err != nil {
		if saidEffort {
			return Pick{}, err
		}
		want.Effort = ""
	}
	return want, nil
}

// choose resolves one exchange's model, most specific statement first.
//
// A model that cannot be reached is an error rather than a silent fall
// back to the base one: asking for a model by name and being answered
// by a different one is the worst outcome available. "Cannot be
// reached" means there is no wire for it on this machine — no key, no
// endpoint. Whether the far end has ever heard of the name is not
// knowable without asking it, and a name it has not heard of comes
// back as a 404 on the first thing said, with the name in it.
func (m *Mind) choose(conversation string, said Pick) (Choice, error) {
	c := Choice{
		Model:      m.Model,
		ModelFrom:  m.ModelFrom,
		EffortFrom: m.EffortFrom,
	}
	effort, explicit := m.BaseEffort, m.EffortExplicit

	// The saved default outranks what startup resolved, unless startup
	// resolved it from the flag — the one thing it does not outrank.
	// It is read here rather than folded into m.Model at startup so
	// that a PUT /model moves the next exchange rather than the next
	// process.
	if d, ok := m.Default.Get(); ok {
		if d.Model != "" && c.ModelFrom != fromFlag {
			c.Model, c.ModelFrom = d.Model, fromSaved
		}
		if d.Effort != "" && c.EffortFrom != fromEffortFlag {
			effort, explicit, c.EffortFrom = d.Effort, true, fromSaved
		}
	}

	if m.Picks != nil && conversation != "" {
		if p, ok := m.Picks.Get(conversation); ok {
			if p.Model != "" {
				c.Model, c.ModelFrom = p.Model, fromConversation
			}
			if p.Effort != "" {
				effort, explicit, c.EffortFrom = p.Effort, true, fromConversation
			}
		}
	}
	if said.Model != "" {
		c.Model, c.ModelFrom = said.Model, fromMessage
	}
	if said.Effort != "" {
		effort, explicit, c.EffortFrom = said.Effort, true, fromMessage
	}

	// The base provider is already built; anything else is reached
	// through the cache.
	if c.Model == m.Model && m.Provider != nil {
		c.Provider, c.Vendor = m.Provider, m.vendor()
	} else {
		if m.Wires == nil {
			return Choice{}, fmt.Errorf("cannot reach %s: this verad has no way to build a provider", c.Model)
		}
		p, vendor, err := m.Wires.For(c.Model)
		if err != nil {
			return Choice{}, fmt.Errorf("cannot reach %s: %w", c.Model, err)
		}
		c.Provider, c.Vendor = p, vendor
	}
	c.Effort, c.Thinking = tune(c.Vendor, effort, explicit)
	return c, nil
}

// base is the daemon's own model as a Choice — the one dictation asks,
// because a cursor has no conversation and so has chosen nothing.
//
// It resolves rather than reading the fields, so that a saved default
// reaches everything with no conversation of its own. What is left is
// the fallback for a machine that cannot build a wire at all, which is
// the startup answer and is the most honest thing there is to say.
func (m *Mind) base() Choice {
	if c, err := m.choose("", Pick{}); err == nil {
		return c
	}
	return Choice{
		Model:      m.Model,
		Vendor:     m.vendor(),
		Provider:   m.Provider,
		Effort:     m.Effort,
		Thinking:   m.Thinking,
		ModelFrom:  m.ModelFrom,
		EffortFrom: m.EffortFrom,
	}
}

// Pick reads what is in force for a conversation without sending
// anything — what `/model` on its own prints.
func (m *Mind) Pick(conversation string) (Resolution, error) {
	c, err := m.choose(conversation, Pick{})
	if err != nil {
		return Resolution{}, err
	}
	return c.Resolution(), nil
}

// Choose sets a conversation's model, its effort, or both, and says
// what is now in force. Each half named replaces its own and leaves the
// other alone — they are two toggles, not one setting in two fields —
// and both empty clears the conversation's choice and puts it back on
// the daemon's.
//
// A wire for the model is built before anything is written down, so a
// machine that cannot reach it at all says so now rather than on the
// next thing said. That is as far as checking can go without a request
// to the provider — see choose and agree.
func (m *Mind) Choose(conversation, model, effort string) (Resolution, error) {
	if conversation == "" {
		return Resolution{}, errors.New("a model is chosen for a conversation; this request named none")
	}
	if m.Picks == nil {
		return Resolution{}, errors.New("this verad keeps no per-conversation state")
	}
	model, effort = strings.TrimSpace(model), strings.TrimSpace(effort)
	if effort != "" && !validEffort(effort) {
		return Resolution{}, fmt.Errorf("effort %q: it is none, minimal, low, medium, high, xhigh or max", effort)
	}
	now, _ := m.Picks.Get(conversation)
	want, err := m.agree(settle(now, model, effort), effort != "")
	if err != nil {
		return Resolution{}, err
	}
	if err := m.Picks.Set(conversation, want); err != nil {
		return Resolution{}, err
	}
	return m.Pick(conversation)
}

// SetDefault moves the daemon's own model — what everything with no
// conversation of its own runs on — and keeps it. It reads the two
// halves the way Choose does: each one named replaces its own, and both
// empty forgets the saved choice and puts the daemon back on the flag,
// the profile or the built-in default.
//
// It does not outrank --model: a daemon started with one says so, and
// this is written down for the next one rather than ignored. The
// answer says what is actually in force, which is how a caller finds
// out that the flag won.
func (m *Mind) SetDefault(model, effort string) (Resolution, error) {
	if m.Default == nil {
		return Resolution{}, errors.New("this verad keeps no saved default")
	}
	model, effort = strings.TrimSpace(model), strings.TrimSpace(effort)
	if effort != "" && !validEffort(effort) {
		return Resolution{}, fmt.Errorf("effort %q: it is none, minimal, low, medium, high, xhigh or max", effort)
	}
	now, _ := m.Default.Get()
	want, err := m.agree(settle(now, model, effort), effort != "")
	if err != nil {
		return Resolution{}, err
	}
	if err := m.Default.Set(want); err != nil {
		return Resolution{}, err
	}
	return m.Pick("")
}
