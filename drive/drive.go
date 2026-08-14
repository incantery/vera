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
	Done     bool
	Escalate bool   // the question belongs to the owner, not to more turns
	Prompt   string // when continuing: the exact next message to send
	Reason   string // DONE's how, or ESCALATE's question for the owner
}

// Result is what a drive has to show for itself.
type Result struct {
	Done      bool
	Escalated bool   // stopped on purpose: the Ask belongs to a human
	Ask       string // what the owner is being asked, when escalated
	Reason    string
	Turns     []Exchange
	Root      string  // the session the drive began as — the agent's identity
	SessionID string  // the final fork — resume it to take the wheel back
	CostUSD   float64 // the turns' metered cost, summed
}

// A Starter can birth a session: the first turn of a conversation
// that does not exist yet. Headless implements it; a Turner that
// cannot start can still Run against existing sessions.
type Starter interface {
	StartTurn(ctx context.Context, prompt string) (Turn, error)
}

// Loop is one drive's machinery.
type Loop struct {
	Turner   Turner
	Judge    Judge
	MaxTurns int               // prompts sent before giving up (default 4)
	MaxUSD   float64           // metered claude spend before stopping (default 5)
	Progress func(line string) // optional: one live line for a UI row
	// OnTurn sees every automatic decision as it is made — the audit
	// trail's feed. Called after each judged exchange.
	OnTurn func(turn int, ex Exchange, v Verdict)
}

func (l *Loop) maxUSD() float64 {
	if l.MaxUSD > 0 {
		return l.MaxUSD
	}
	return 5
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
	return l.run(ctx, sessionID, goal, goal, nil)
}

// run drives from an existing session: `prompt` is the next thing to
// say, `seed` is whatever conversation is already on the record (the
// fresh path's first exchange arrives this way).
func (l *Loop) run(ctx context.Context, sessionID, goal, prompt string, seed []Exchange) (Result, error) {
	res := Result{Root: sessionID, SessionID: sessionID, Turns: seed}
	off := len(seed)
	total := l.maxTurns() + off
	var prevReply string
	for turn := 1 + off; turn <= total; turn++ {
		l.progress("turn %d/%d: asking claude", turn, total)
		t, err := l.Turner.RunTurn(ctx, res.SessionID, prompt)
		res.CostUSD += t.CostUSD
		if err != nil {
			return res, errors.New("the turn failed: " + err.Error())
		}
		if t.SessionID != "" {
			res.SessionID = t.SessionID
		}
		ex := Exchange{Prompt: prompt, Reply: t.Reply}
		res.Turns = append(res.Turns, ex)
		l.progress("turn %d/%d: judging the reply", turn, total)
		v, err := l.Judge.Judge(ctx, goal, res.Turns)
		if err != nil {
			return res, errors.New("the judge failed: " + err.Error())
		}
		if l.OnTurn != nil {
			l.OnTurn(turn, ex, v)
		}
		switch {
		case v.Done:
			res.Done = true
			res.Reason = v.Reason
			if res.Reason == "" {
				res.Reason = "the goal is met"
			}
			return res, nil
		case v.Escalate:
			res.Escalated = true
			res.Ask = v.Reason
			res.Reason = "escalated to the owner"
			return res, nil
		}
		if strings.TrimSpace(v.Prompt) == "" {
			return res, errors.New("the judge wanted to continue but had nothing to say")
		}
		// The circling guard: the judge re-issuing the prompt it just
		// used, or the worker giving the same reply twice running, is a
		// conversation going nowhere — stop and hand the wheel to a
		// human rather than bill laps.
		if fold(v.Prompt) == fold(prompt) || (prevReply != "" && fold(t.Reply) == fold(prevReply)) {
			res.Escalated = true
			res.Ask = "The conversation is circling — the last exchange repeated itself. What should change?"
			res.Reason = "escalated: circling"
			return res, nil
		}
		// The spend cap: metered money is a budget, not a suggestion.
		if res.CostUSD >= l.maxUSD() {
			res.Escalated = true
			res.Ask = fmt.Sprintf("The run has spent $%.2f (cap $%.2f) without meeting the goal. Keep going, change course, or drop?", res.CostUSD, l.maxUSD())
			res.Reason = "escalated: spend cap"
			return res, nil
		}
		prevReply = t.Reply
		prompt = v.Prompt
	}
	res.Reason = fmt.Sprintf("the turn budget (%d) is spent and the goal is not met", total)
	return res, nil
}

// fold collapses whitespace for the circling comparison — repetition
// is about words, not formatting.
func fold(s string) string { return strings.Join(strings.Fields(s), " ") }

// Continue picks up a drive whose owner just answered: the reply is
// the next prompt, the recorded exchanges are the seed, and the judge
// keeps judging against the original goal.
func (l *Loop) Continue(ctx context.Context, sessionID, goal, reply string, seed []Exchange) (Result, error) {
	return l.run(ctx, sessionID, goal, reply, seed)
}

// RunFresh drives a session that does not exist yet: the Turner (which
// must also be a Starter) births it with the goal as the first turn,
// and the loop continues from whatever session that turn became. The
// Result's Root names the newborn agent.
func (l *Loop) RunFresh(ctx context.Context, goal string) (Result, error) {
	st, ok := l.Turner.(Starter)
	if !ok {
		return Result{}, errors.New("this turner cannot start a session")
	}
	var res Result
	l.progress("turn 1/%d: starting a fresh agent", l.maxTurns())
	t, err := st.StartTurn(ctx, goal)
	res.CostUSD += t.CostUSD
	if err != nil {
		return res, errors.New("the first turn failed: " + err.Error())
	}
	if t.SessionID == "" {
		return res, errors.New("the first turn came back without a session id")
	}
	res.Root, res.SessionID = t.SessionID, t.SessionID
	ex := Exchange{Prompt: goal, Reply: t.Reply}
	res.Turns = append(res.Turns, ex)
	l.progress("turn 1/%d: judging the reply", l.maxTurns())
	v, err := l.Judge.Judge(ctx, goal, res.Turns)
	if err != nil {
		return res, errors.New("the judge failed: " + err.Error())
	}
	if l.OnTurn != nil {
		l.OnTurn(1, ex, v)
	}
	switch {
	case v.Done:
		res.Done, res.Reason = true, v.Reason
		if res.Reason == "" {
			res.Reason = "the goal is met"
		}
		return res, nil
	case v.Escalate:
		res.Escalated = true
		res.Ask = v.Reason
		res.Reason = "escalated to the owner"
		return res, nil
	}
	if strings.TrimSpace(v.Prompt) == "" {
		return res, errors.New("the judge wanted to continue but had nothing to say")
	}
	// The rest of the drive is an ordinary run against the newborn,
	// with one turn already on the record.
	rest := *l
	rest.MaxTurns = l.maxTurns() - 1
	more, err := rest.run(ctx, res.SessionID, goal, v.Prompt, res.Turns)
	more.Root = res.Root
	more.CostUSD += res.CostUSD
	return more, err
}
