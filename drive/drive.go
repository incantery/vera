// Package drive turns one goal into a supervised conversation with a
// Claude Code session: run a turn, judge the reply against the goal,
// and either stop or say the next thing — a bounded number of times.
// It is the loop a human runs by hand when an agent needs three nudges
// to actually answer the question, made a verb.
//
// The engine's mechanism is the headless fork: `claude -p --resume`
// continues the conversation in a NEW session and answers on stdout,
// so a drive never types into anyone's live terminal — the original
// session keeps its transcript, the drive's rounds land in forks, and
// the final fork's id is on the record for a human to `claude
// --resume` when they want the wheel back. (rook's own plugins run the
// other mechanism — typed keystrokes into a live pane — behind the
// same judge vocabulary.)
package drive

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// A Turner runs one turn of a conversation: prompt in, reply out,
// plus wherever the conversation now lives.
type Turner interface {
	RunTurn(ctx context.Context, sessionID, prompt string) (Turn, error)
}

// Turn is one round trip's yield. SessionID is the fork the reply
// landed in — the next turn resumes THAT, not the original.
type Turn struct {
	Reply     string
	SessionID string
	CostUSD   float64 // what claude itself metered for the turn; 0 = not reported
}

// Exchange is one round of the drive's own conversation.
type Exchange struct {
	Prompt string `json:"prompt"`
	Reply  string `json:"reply"`
}

// A Judge reads the goal and the conversation so far and decides:
// done, or here is the next thing to say.
type Judge interface {
	Judge(ctx context.Context, goal string, history []Exchange) (Verdict, error)
}

type Verdict struct {
	Done   bool
	Prompt string // when not done: the exact next message to send
	Reason string // one line for the record
}

// Result is what a drive has to show for itself.
type Result struct {
	Done      bool
	Reason    string
	Turns     []Exchange
	SessionID string  // the final fork — resume it to take the wheel back
	CostUSD   float64 // the turns' metered cost, summed
}

// Loop is one drive's machinery.
type Loop struct {
	Turner   Turner
	Judge    Judge
	MaxTurns int               // prompts sent before giving up (default 4)
	Progress func(line string) // optional: one live line for a UI row
}

func (l *Loop) maxTurns() int {
	if l.MaxTurns > 0 {
		return l.MaxTurns
	}
	return 4
}

func (l *Loop) progress(format string, args ...any) {
	if l.Progress != nil {
		l.Progress(fmt.Sprintf(format, args...))
	}
}

// Run drives one session toward one goal. The returned error is an
// abnormal stop — a turn that failed, the judge breaking — and the
// Result still carries whatever rounds ran. A goal honestly not met
// within the turn budget is not an error; it is Done=false with the
// reason on the record.
func (l *Loop) Run(ctx context.Context, sessionID, goal string) (Result, error) {
	res := Result{SessionID: sessionID}
	prompt := goal
	for turn := 1; turn <= l.maxTurns(); turn++ {
		l.progress("turn %d/%d: asking claude", turn, l.maxTurns())
		t, err := l.Turner.RunTurn(ctx, res.SessionID, prompt)
		res.CostUSD += t.CostUSD
		if err != nil {
			return res, errors.New("the turn failed: " + err.Error())
		}
		if t.SessionID != "" {
			res.SessionID = t.SessionID
		}
		res.Turns = append(res.Turns, Exchange{Prompt: prompt, Reply: t.Reply})
		l.progress("turn %d/%d: judging the reply", turn, l.maxTurns())
		v, err := l.Judge.Judge(ctx, goal, res.Turns)
		if err != nil {
			return res, errors.New("the judge failed: " + err.Error())
		}
		if v.Done {
			res.Done = true
			res.Reason = v.Reason
			if res.Reason == "" {
				res.Reason = "the goal is met"
			}
			return res, nil
		}
		if strings.TrimSpace(v.Prompt) == "" {
			return res, errors.New("the judge wanted to continue but had nothing to say")
		}
		prompt = v.Prompt
	}
	res.Reason = fmt.Sprintf("the turn budget (%d) is spent and the goal is not met", l.maxTurns())
	return res, nil
}
