// Vera, as a terminal sees her.
//
// mote's terminal talks to one interface — say a thing, get a stream
// of events — and nothing in it knows where the agent lives. This is
// that interface over the phone's wire: /say frames in, mote events
// out. It is a translation and nothing else; when the wire grows a
// frame kind, this is the only place that has to learn it.
package main

import (
	"context"
	"time"

	"github.com/incantery/mote/agent"
	"github.com/incantery/vera/attach"
)

// veraAgent is verad over HTTP, behind mote's one method.
type veraAgent struct {
	c *chatClient
	// held is what /paste and /image attached and have not sent. It
	// is taken here rather than by the command that staged it,
	// because the picture and the words are one message and this is
	// where the message is. Nil is a chat with no way to attach.
	held *stage
}

// Send opens the exchange before it returns, so a call that could not
// start at all — a wrong secret, a daemon that went away — is an
// error rather than a channel that carries one. Cancelling ctx breaks
// the read and closes the channel.
func (a veraAgent) Send(ctx context.Context, conversation, text string) (<-chan agent.Event, error) {
	// Taken, not copied: an attached picture goes with exactly one
	// thing you said. If this send fails the pictures are gone with
	// it, which is the same as any other message that did not land.
	var pictures []attach.Image
	if a.held != nil {
		pictures = a.held.take()
	}
	body, err := a.c.openSay(ctx, Message{Text: text, Conversation: conversation, Images: pictures})
	if err != nil {
		return nil, err
	}
	out := make(chan agent.Event, 64)
	go func() {
		defer close(out)
		defer body.Close()
		send := func(ev agent.Event) {
			select {
			case out <- ev:
			case <-ctx.Done():
			}
		}
		// What the exchange spent, kept until the end: mote wants it on
		// the one event that closes the turn, and only the terminal
		// frame knows it.
		var spent *UsageFrame
		err := streamFrames(body, func(f Frame) {
			if f.Usage != nil {
				spent = f.Usage
			}
			for _, ev := range translate(f) {
				send(ev)
			}
		})
		if err != nil && ctx.Err() == nil {
			send(agent.Err(err))
		}
		send(finish(spent))
	}()
	return out, nil
}

// finish is the event that ends an exchange. With usage on the wire it
// says what the turn cost, which is what puts a running total on the
// status line; without it — an older verad, or an exchange that never
// reached the model — it is a plain done, and the screen stays quiet
// rather than claiming the turn was free.
//
// The dollars are only passed on when somebody knew a price. Tokens
// are passed on either way: they are counted, not estimated.
func finish(u *UsageFrame) agent.Event {
	if u == nil {
		return agent.Done()
	}
	cost := 0.0
	if u.Priced {
		cost = u.CostUSD
	}
	return agent.Spent(cost, u.InputTokens, u.OutputTokens)
}

// Answer is agent.Answerer: the terminal's y / n / a, back to the
// exchange parked on the question. The terminal finds this by type
// assertion, so an agent without it renders the card and says the
// question went nowhere rather than locking up.
func (a veraAgent) Answer(ctx context.Context, id, choice string) error {
	return a.c.answer(ctx, id, choice)
}

// translate turns one frame into the events it means. Done is not
// among them: the end of the stream is the end of the exchange, and
// Send is the one place that says so, exactly once.
func translate(f Frame) []agent.Event {
	var out []agent.Event
	if f.Error != "" {
		out = append(out, agent.Fail(f.Error))
	}
	if f.Status != "" {
		out = append(out, agent.Status(f.Status))
	}
	if f.Delta != "" {
		out = append(out, agent.Delta(f.Delta))
	}
	if tc := f.ToolCall; tc != nil {
		// The card reads as a sentence where this terminal knows the
		// tool's shape — `start scout vera "Investigate…"` rather
		// than the JSON it was called with. mote summarizes the
		// arguments itself for everything else, which is the right
		// answer for a tool the chat has never heard of.
		out = append(out, agent.Call(tc.ID, tc.Name, tc.Args).WithSummary(toolSays(tc.Name, tc.Args)))
	}
	if ask := f.Ask; ask != nil {
		out = append(out, agent.Asking(ask.ID, ask.Name, ask.Args, ask.Text).WithSummary(toolSays(ask.Name, ask.Args)))
	}
	if to := f.ToolOutput; to != nil {
		out = append(out, agent.Output(to.ID, to.Text))
	}
	if tr := f.ToolResult; tr != nil {
		out = append(out, agent.Result(tr.ID, tr.Result, time.Duration(tr.DurationMs)*time.Millisecond, tr.CostUSD))
	}
	return out
}
