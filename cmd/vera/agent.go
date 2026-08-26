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
)

// veraAgent is verad over HTTP, behind mote's one method.
type veraAgent struct{ c *chatClient }

// Send opens the exchange before it returns, so a call that could not
// start at all — a wrong secret, a daemon that went away — is an
// error rather than a channel that carries one. Cancelling ctx breaks
// the read and closes the channel.
func (a veraAgent) Send(ctx context.Context, conversation, text string) (<-chan agent.Event, error) {
	body, err := a.c.openSay(ctx, text, conversation)
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
		err := streamFrames(body, func(f Frame) {
			for _, ev := range translate(f) {
				send(ev)
			}
		})
		if err != nil && ctx.Err() == nil {
			send(agent.Err(err))
		}
		send(agent.Done())
	}()
	return out, nil
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
		out = append(out, agent.Call(tc.ID, tc.Name, tc.Args))
	}
	// Nothing sends this yet (see Frame.ToolOutput); it is translated
	// so that nothing has to when something does.
	if to := f.ToolOutput; to != nil {
		out = append(out, agent.Output(to.ID, to.Text))
	}
	if tr := f.ToolResult; tr != nil {
		out = append(out, agent.Result(tr.ID, tr.Result, time.Duration(tr.DurationMs)*time.Millisecond, tr.CostUSD))
	}
	return out
}
