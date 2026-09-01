// Which models verad can reach, and what each of them will take.
//
// This is a table, not a question put to the vendor. There is no
// endpoint on either API that answers "which of your models will
// accept this request with these tools at this effort", and the one
// fact that matters most here — that OpenAI's gpt-5.6 family refuses
// a reasoning_effort other than none when there are function tools on
// a chat completion — was found at the socket rather than in a doc.
// A table is the honest shape for knowledge like that: it is what we
// have learned, written down, editable, and wrong in a way somebody
// can see and fix.
//
// What the table is NOT allowed to do is claim a model this machine
// cannot call. A provider with no key contributes no rows, because a
// picker that offers claude-opus-5 on a laptop with no Anthropic key
// is a picker that hands you an error three keystrokes later.
//
// $VERA_MODELS adds rows, or replaces them, without a rebuild:
//
//	VERA_MODELS="my-local-7b=openai:none, gpt-5=openai:none|high"
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/incantery/mote/provider"
	"github.com/incantery/vera/price"
)

// EnvModels adds to the table, or overrides it.
const EnvModels = "VERA_MODELS"

// ModelRow is one model this daemon can reach: its name, the wire that
// reaches it, the efforts it will actually accept, whatever is worth
// knowing about it, and whether `price` can turn its tokens into
// dollars.
type ModelRow struct {
	Name     string   `json:"name"`
	Provider string   `json:"provider"`
	Efforts  []string `json:"efforts"`
	Note     string   `json:"note,omitempty"`
	Priced   bool     `json:"priced"`
}

// InForce is one model as something already using it: what it is, how
// hard it is thinking, and — for the daemon's own — who said so.
type InForce struct {
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
	From   string `json:"from,omitempty"`
}

// ModelsAnswer is GET /models: what is in force, and what else there
// is. Conversation is absent when this conversation has chosen
// nothing of its own — which is most of them, and is not the same as
// "it chose the default".
type ModelsAnswer struct {
	Default      InForce    `json:"default"`
	Conversation *InForce   `json:"conversation,omitempty"`
	Models       []ModelRow `json:"models"`
}

// modelTable is what verad knows, in the order it is worth reading:
// the cheap fast ones first, the ones you reach for when the cheap
// fast one got it wrong last.
var modelTable = []ModelRow{
	{Name: "gpt-5.6-luna", Provider: "openai", Efforts: []string{"none"},
		Note: "effort none only (chat completions)"},
	{Name: "gpt-5.6-terra", Provider: "openai", Efforts: []string{"none"},
		Note: "effort none only (chat completions)"},
	{Name: "gpt-5.6", Provider: "openai", Efforts: []string{"none"}},
	{Name: "gpt-5", Provider: "openai", Efforts: []string{"none", "low", "medium", "high"}},
	{Name: "gpt-5-mini", Provider: "openai", Efforts: []string{"none", "low", "medium", "high"}},
	{Name: "claude-opus-5", Provider: "anthropic", Efforts: []string{"low", "medium", "high", "max"}},
	{Name: "claude-sonnet-5", Provider: "anthropic", Efforts: []string{"low", "medium", "high"}},
}

// reach is which wires this machine has at all. It mirrors the rule
// mote's provider.New goes by — a key, or a base URL for an endpoint
// that needs none — rather than guessing from the model name.
type reach struct{ openai, anthropic bool }

// reach reads the keys this daemon was built with, and the environment
// for the ones it was not. A Wires is where verad keeps the OpenAI
// half; the Anthropic key is read from the environment by mote itself,
// so it is read from there here too.
func (w *Wires) reach() reach {
	r := reach{anthropic: strings.TrimSpace(os.Getenv(provider.EnvAnthropicKey)) != ""}
	if w != nil {
		r.openai = strings.TrimSpace(w.OpenAIKey) != "" || strings.TrimSpace(w.OpenAIBase) != ""
	}
	if strings.TrimSpace(os.Getenv(provider.EnvOpenAIKey)) != "" ||
		strings.TrimSpace(os.Getenv(provider.EnvOpenAIBase)) != "" {
		r.openai = true
	}
	return r
}

// has says whether a row's provider can be called from here. A
// provider neither half of this knows — somebody's own name in
// $VERA_MODELS — is taken at its word: they added it because they can
// reach it.
func (r reach) has(vendor string) bool {
	switch vendor {
	case "openai":
		return r.openai
	case "anthropic":
		return r.anthropic
	}
	return true
}

// models is the table this machine can actually use, with the
// environment's own rows folded in and every row priced or not.
func models(r reach, spec string) []ModelRow {
	var out []ModelRow
	for _, row := range modelTable {
		if r.has(row.Provider) {
			out = append(out, row)
		}
	}
	extra, _ := parseModels(spec)
	for _, row := range extra {
		if !r.has(row.Provider) {
			continue
		}
		// An entry naming a model the table already has replaces it
		// where it stands: the point of overriding a row is to correct
		// it, not to list it twice.
		if i := indexModel(out, row.Name); i >= 0 {
			out[i] = row
			continue
		}
		out = append(out, row)
	}
	for i := range out {
		_, priced := price.For(out[i].Name)
		out[i].Priced = priced
	}
	return out
}

func indexModel(rows []ModelRow, name string) int {
	for i, row := range rows {
		if strings.EqualFold(row.Name, name) {
			return i
		}
	}
	return -1
}

// parseModels reads $VERA_MODELS: comma-separated
// `name=provider:eff1|eff2`, with the efforts optional and "none" when
// they are left out. A bad entry is named and dropped; it does not
// take the good ones with it, for the same reason a bad price does
// not.
func parseModels(spec string) (rows []ModelRow, bad []string) {
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, rest, ok := strings.Cut(entry, "=")
		name, rest = strings.TrimSpace(name), strings.TrimSpace(rest)
		if !ok || name == "" || rest == "" {
			bad = append(bad, entry)
			continue
		}
		vendor, dial, _ := strings.Cut(rest, ":")
		vendor = strings.TrimSpace(vendor)
		if vendor == "" {
			bad = append(bad, entry)
			continue
		}
		row := ModelRow{Name: name, Provider: vendor, Efforts: []string{"none"}}
		if dial = strings.TrimSpace(dial); dial != "" {
			row.Efforts = nil
			for _, e := range strings.Split(dial, "|") {
				e = strings.TrimSpace(e)
				if !validEffort(e) {
					row.Efforts = nil
					break
				}
				row.Efforts = append(row.Efforts, e)
			}
		}
		if len(row.Efforts) == 0 {
			bad = append(bad, entry)
			continue
		}
		rows = append(rows, row)
	}
	return rows, bad
}

// Models is GET /models for one conversation: what this daemon is on,
// what this conversation chose if it chose anything, and every model
// there is a wire for.
func (m *Mind) Models(conversation string) ModelsAnswer {
	base := m.base()
	ans := ModelsAnswer{
		Default: InForce{Model: base.Model, Effort: string(base.Effort), From: base.ModelFrom},
		Models:  models(m.Wires.reach(), os.Getenv(EnvModels)),
	}
	if p, ok := m.Picks.Get(conversation); ok {
		ans.Conversation = &InForce{Model: p.Model, Effort: p.Effort}
	}
	if ans.Models == nil {
		ans.Models = []ModelRow{}
	}
	return ans
}

// rowFor is what the table says about one model, whether or not this
// machine has a wire for it. Reach is deliberately not consulted: "does
// gpt-5.6-terra take a reasoning effort" is a fact about the model, not
// about the keys on this laptop.
func rowFor(name string) (ModelRow, bool) {
	rows := append([]ModelRow(nil), modelTable...)
	extra, _ := parseModels(os.Getenv(EnvModels))
	for _, row := range extra {
		if i := indexModel(rows, row.Name); i >= 0 {
			rows[i] = row
			continue
		}
		rows = append(rows, row)
	}
	if i := indexModel(rows, name); i >= 0 {
		return rows[i], true
	}
	return ModelRow{}, false
}

// takesEffort says whether a model will accept a dial setting, and
// names what it does take when it will not. Two things are not
// refusals: an empty effort, which is "leave it alone", and a model the
// table has never heard of, about which nothing is known and so nothing
// may be claimed.
func takesEffort(model, effort string) error {
	if effort == "" {
		return nil
	}
	row, ok := rowFor(model)
	if !ok {
		return nil
	}
	for _, e := range row.Efforts {
		if strings.EqualFold(e, effort) {
			return nil
		}
	}
	return fmt.Errorf("%s does not take effort %s — it takes %s",
		model, effort, strings.Join(row.Efforts, ", "))
}
