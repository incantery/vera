// `/model` as a card rather than a name you had to know.
//
// Typing `/model claude-opus-5 high` still works and is still the
// fastest way when you know the answer. The card is for the other
// case, which is most of them: you want to see what this machine can
// actually reach, what each one will take, and which of them anybody
// has a price for — and then move onto one without spelling it.
//
// Everything on it comes from verad's GET /models. The terminal knows
// no models of its own, on purpose: which models exist is a property
// of the keys on the machine verad runs on, and a list compiled into
// the client would be out of date on somebody else's laptop.
//
// The two ways out are the two scopes a choice can have, and the card
// says which is which: Enter moves the daemon — every conversation
// that has not chosen for itself, dictation, the next chat — and `s`
// moves this conversation and nothing else.
package main

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/incantery/mote/tui"
)

// effortOrder is the dial, from off to hardest. Rows name a subset of
// it and the card offers the union, because mote's Pick builds its
// dial once and the selection moves under it.
var effortOrder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// effortUnion is every effort any listed model will take, in the order
// a dial turns them.
func effortUnion(rows []ModelRow) []string {
	seen := map[string]bool{}
	for _, r := range rows {
		for _, e := range r.Efforts {
			seen[e] = true
		}
	}
	var out []string
	for _, e := range effortOrder {
		if seen[e] {
			out = append(out, e)
		}
	}
	// An effort nobody in effortOrder has heard of still belongs on the
	// dial: $VERA_MODELS is allowed to know things this binary does not.
	for _, r := range rows {
		for _, e := range r.Efforts {
			if !indexOf(effortOrder, e) && !indexOf(out, e) {
				out = append(out, e)
			}
		}
	}
	return out
}

func indexOf(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
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

// modelPick is the card, built from what verad said.
func (s *chatSession) modelPick(ans *ModelsAnswer) tui.Pick {
	using := ans.using()
	dial := effortUnion(ans.Models)

	p := tui.Pick{
		Title: "Select model",
		Text: "Enter moves Vera herself; s moves this conversation only. " +
			"←/→ sets the effort — each model takes the ones beside its name.",
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
	if len(dial) > 0 {
		d := tui.PickDial{Label: "effort", Options: dial}
		for i, e := range dial {
			if e == using.Effort {
				d.Current = i
			}
		}
		p.Dial = &d
	}
	p.OnPick = s.chooseFromPick(ans, dial)
	return p
}

// chooseFromPick is what the card does when it closes: nothing at all
// if it was cancelled, an error if the effort is one the chosen model
// will not take, and otherwise a request to verad in the scope the key
// asked for.
//
// The dial is the union of every row's efforts because mote builds one
// dial per card and it does not move as the selection does. So the
// combination is checked here, by name, rather than being made
// impossible — and the refusal says what that model does take, which
// is the thing the person needs in order to try again.
func (s *chatSession) chooseFromPick(ans *ModelsAnswer, dial []string) func(tui.PickChoice) tea.Cmd {
	c, w, conv := s.c, s.w, s.conversation()
	return func(choice tui.PickChoice) tea.Cmd {
		if choice.Cancelled {
			return nil // a card that was closed leaves nothing behind
		}
		if choice.Item < 0 || choice.Item >= len(ans.Models) {
			return nil
		}
		row := ans.Models[choice.Item]
		effort := ""
		if choice.Dial >= 0 && choice.Dial < len(dial) {
			effort = dial[choice.Dial]
		}
		if effort != "" && !indexOf(row.Efforts, effort) {
			return tui.Fail("%s does not take effort %s — it takes %s",
				row.Name, effort, strings.Join(row.Efforts, ", "))
		}

		conversationOnly := choice.Action == "s"
		return off(func(ctx context.Context) tea.Cmd {
			var res *Resolution
			var err error
			scope := "as Vera's default"
			if conversationOnly {
				scope = "for this conversation"
				res, err = c.chooseModel(ctx, conv, row.Name, effort)
			} else {
				res, err = c.setDefaultModel(ctx, row.Name, effort)
			}
			if err != nil {
				return tui.Fail("model: %s", err)
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
