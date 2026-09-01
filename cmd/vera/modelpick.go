// `/model` and `/effort` as cards rather than names you had to know.
//
// Typing `/model claude-opus-5` or `/effort high` still works and is
// still the fastest way when you know the answer. The cards are for the
// other case, which is most of them: you want to see what this machine
// can actually reach, what each one will take, and which of them
// anybody has a price for — and then move onto one without spelling it.
//
// They are two cards because they are two questions. Which model
// answers is a choice among a dozen names that come and go with the
// keys on the machine; how hard it thinks is the same three words every
// time, on whichever model you are already on. Putting the second on a
// dial inside the first meant every model change restated the effort,
// and a combination no model would take was only caught on the way out.
//
// Everything on both comes from verad's GET /models. The terminal knows
// no models of its own, on purpose: which models exist is a property
// of the keys on the machine verad runs on, and a list compiled into
// the client would be out of date on somebody else's laptop.
//
// The two ways out are the two scopes a choice can have, and both cards
// say which is which: Enter moves the daemon — every conversation that
// has not chosen for itself, dictation, the next chat — and `s` moves
// this conversation and nothing else.
package main

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/incantery/mote/tui"
)

// effortDial is the toggle: low, medium, high, the three Claude Code
// offers and the three that mean the same thing on every vendor that
// has a dial at all.
//
// It is deliberately not every effort a model will accept. Anthropic
// takes max, the OpenAI reasoning models take minimal, and both are
// still reachable by typing `/effort max` — but a toggle with seven
// positions is a menu, and the point of a toggle is that you turn it
// without reading it.
var effortDial = []string{"low", "medium", "high"}

// effortMeans is the one line under each: what turning it there costs
// you and what it buys.
var effortMeans = map[string]string{
	"low":    "answers soonest, thinks least",
	"medium": "the middle setting",
	"high":   "thinks longest before answering",
}

func indexOf(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// rowFor is what verad said about one model by name.
func rowFor(rows []ModelRow, name string) (ModelRow, bool) {
	for _, r := range rows {
		if strings.EqualFold(r.Name, name) {
			return r, true
		}
	}
	return ModelRow{}, false
}

// effortsFor is which positions of the toggle the model in force will
// actually take.
//
// A model whose row says "none" and nothing else — the whole gpt-5.6
// family — has no dial, and gets an empty list rather than three
// options verad would refuse one at a time. A model verad listed no row
// for is not one this terminal may make claims about, so it gets all
// three and verad decides.
func effortsFor(ans *ModelsAnswer) []string {
	row, ok := rowFor(ans.Models, ans.using().Model)
	if !ok {
		return effortDial
	}
	var out []string
	for _, e := range effortDial {
		if indexOf(row.Efforts, e) {
			out = append(out, e)
		}
	}
	return out
}

// modelDetail is the right-hand half of a row: how it is reached, what
// is worth knowing about it, and whether anybody can price it. An
// unpriced model is said out loud because its turns will show tokens
// and no dollars, and finding that out afterwards is worse.
func modelDetail(r ModelRow) string {
	parts := []string{"via " + r.Provider}
	if r.Note != "" {
		parts = append(parts, r.Note)
	}
	if !r.Priced {
		parts = append(parts, "unpriced")
	}
	return strings.Join(parts, " · ")
}

// modelPick is the model card, built from what verad said. It says
// nothing about effort beyond leaving it alone: verad keeps the two
// halves apart, so moving onto another model keeps the effort you were
// thinking at, or drops it if the new one has no dial.
func (s *chatSession) modelPick(ans *ModelsAnswer) tui.Pick {
	using := ans.using()

	p := tui.Pick{
		Title: "Select model",
		Text: "Enter moves Vera herself; s moves this conversation only. " +
			"/effort sets how hard it thinks.",
		Actions: []tui.PickAction{
			{Key: "enter", Label: "make it Vera's default"},
			{Key: "s", Label: "this conversation"},
		},
	}
	for i, r := range ans.Models {
		p.Items = append(p.Items, tui.PickItem{
			Label:   r.Name,
			Detail:  modelDetail(r),
			Current: strings.EqualFold(r.Name, using.Model),
		})
		if strings.EqualFold(r.Name, using.Model) {
			p.Current = i
		}
	}
	p.OnPick = s.applyPick("model", func(choice tui.PickChoice) (string, string, bool) {
		if choice.Item < 0 || choice.Item >= len(ans.Models) {
			return "", "", false
		}
		return ans.Models[choice.Item].Name, "", true
	})
	return p
}

// effortPick is the effort card: the same three words whatever model is
// answering, with the model named so it is clear what is being turned.
func (s *chatSession) effortPick(ans *ModelsAnswer, dial []string) tui.Pick {
	using := ans.using()

	p := tui.Pick{
		Title: "Reasoning effort",
		Text: using.Model + " · Enter moves Vera herself; s moves this conversation only. " +
			"An effort that is not one of these three can still be typed: /effort max.",
		Actions: []tui.PickAction{
			{Key: "enter", Label: "make it Vera's default"},
			{Key: "s", Label: "this conversation"},
		},
	}
	for i, e := range dial {
		p.Items = append(p.Items, tui.PickItem{
			Label:   e,
			Detail:  effortMeans[e],
			Current: e == using.Effort,
		})
		if e == using.Effort {
			p.Current = i
		}
	}
	p.OnPick = s.applyPick("effort", func(choice tui.PickChoice) (string, string, bool) {
		if choice.Item < 0 || choice.Item >= len(dial) {
			return "", "", false
		}
		return "", dial[choice.Item], true
	})
	return p
}

// applyPick is what either card does when it closes: nothing at all if
// it was cancelled, and otherwise a request to verad in the scope the
// key asked for.
//
// half turns the choice into the one thing that card sets, and sends
// the other empty — which is verad's word for "leave it where it is".
// noun is what an error is about, since the two cards fail differently
// and "model:" on a refused effort would be a lie.
func (s *chatSession) applyPick(noun string, half func(tui.PickChoice) (model, effort string, ok bool)) func(tui.PickChoice) tea.Cmd {
	c, w, conv := s.c, s.w, s.conversation()
	return func(choice tui.PickChoice) tea.Cmd {
		if choice.Cancelled {
			return nil // a card that was closed leaves nothing behind
		}
		model, effort, ok := half(choice)
		if !ok {
			return nil
		}

		conversationOnly := choice.Action == "s"
		return off(func(ctx context.Context) tea.Cmd {
			var res *Resolution
			var err error
			scope := "as Vera's default"
			if conversationOnly {
				scope = "for this conversation"
				res, err = c.chooseModel(ctx, conv, model, effort)
			} else {
				res, err = c.setDefaultModel(ctx, model, effort)
			}
			if err != nil {
				return tui.Fail("%s: %s", noun, err)
			}
			// verad is the authority on what this conversation is now
			// on, and setting the daemon's default does not move a
			// conversation that has chosen for itself. So the status
			// line follows what it says, and the note says so too when
			// the two are not the same thing.
			w.pollModel(ctx)
			line := w.resolution().Line()
			if line == "" {
				line = res.Line() // verad answered and then went quiet
			}
			note := res.Line() + " " + scope + " — " + res.Says()
			if line != res.Line() {
				note += "; this conversation stays on " + line + " (its own choice)"
			}
			return tea.Batch(tui.SetModel(line), tui.Note("%s", note), tui.Refresh())
		})
	}
}
