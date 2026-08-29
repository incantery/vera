// Handing work to Claude Code.
//
// The boundary this draws is the point of it: Vera is never a coding
// agent. Vera knows you, decides what deserves attention, and hands
// execution to something already excellent at it. That keeps Vera off
// a frontier it would lose on, and turns every future capability
// question from "how do I build that" into "who do I hand it to".
//
// The failure mode to watch for is Vera slowly growing opinions about
// HOW delegated work should go. The moment this file starts describing
// steps rather than intent, the boundary has moved.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/incantery/mote/tool"
	"github.com/incantery/vera/attach"
	"github.com/incantery/vera/fleet"
	"go.opentelemetry.io/otel/propagation"
)

// The workspace is Vera's own directory, not yours. Claude Code starts
// there, so a task with no path in it lands somewhere harmless.
//
// This is a floor, not a fence: the delegate has a shell, and a shell
// can go anywhere its user can. What the workspace buys is that the
// DEFAULT is contained — an ambiguous task does not begin its life in
// a repository you care about.
func workspacePath(override string) string {
	if override != "" {
		return override
	}
	return filepath.Join(stateDir(), "vera", "workspace")
}

type Delegate struct {
	Workspace  string
	Permission string
	Timeout    time.Duration

	// Telemetry: whether the delegate reports its own work to the same
	// place Vera does.
	Telemetry bool
}

// DelegateTool is what the model is told it can reach for. The
// description is load-bearing — it is the entire basis on which the
// model decides between answering and delegating, so it says what the
// delegate is GOOD at rather than what it is.
//
// It is one of mote's tools, in the same registry as read and write,
// so a delegation is decided, run and journalled by the same path as
// everything else she does.
type DelegateTool struct {
	// Delegate is the Claude Code this hands work to.
	Delegate *Delegate
	// WithFleet changes what the model is told, not what happens.
	// Beside the fleet this is the SMALL tool and has to read as one.
	WithFleet bool
}

func (t *DelegateTool) Name() string { return "delegate" }

func (t *DelegateTool) Description() string { return delegateDescription(t.WithFleet) }

func delegateDescription(withFleet bool) string {
	if withFleet {
		// Beside the fleet, this is the SMALL tool, and it must read as
		// the small tool or the model will keep reaching for it: a
		// task it saw first, that returns a result it can quote.
		return "Hand a QUICK job to Claude Code — a lookup, a one-off command, a small fact " +
			"about this machine — that finishes within a minute or two while the person waits. " +
			"It runs in a scratch directory, NOT in any repository, and the person waits for the " +
			"result. Never use it for work on code, work in a repository, or anything that will " +
			"take more than a couple of minutes: that is the fleet tool's job. Do not use it for " +
			"conversation, opinions, or anything you can simply answer."
	}
	return "Hand a task to Claude Code, a capable coding agent running on this Mac. " +
		"It can read and write files, run shell commands, use git, search the web, and work " +
		"through a multi-step task on its own. Use it when answering requires DOING something " +
		"on the machine, or looking something up that you cannot know. Do not use it for " +
		"conversation, opinions, or anything you can simply answer."
}

func (t *DelegateTool) Schema() json.RawMessage { return json.RawMessage(delegateSchema) }

const delegateSchema = `{
  "type": "object",
  "properties": {
    "task": {
      "type": "string",
      "description": "What to accomplish, in plain prose, with everything needed to act on it alone. Say the goal, not the steps."
    }
  },
  "required": ["task"]
}`

// No Paths and no Command: a delegated task is prose, and the paths
// it will touch are not knowable until Claude Code has read it. What
// a policy can decide about this call is the tool itself.

// Run hands the task over and waits.
//
// An error is the delegation not happening — a subprocess that would
// not start, a run that outlived its timeout. A task that ran and
// came back unhappy is a Result saying so: the model can work with
// that, and it is not a failure of the tool.
func (t *DelegateTool) Run(ctx context.Context, args json.RawMessage, h tool.Handle) (tool.Result, error) {
	if t.Delegate == nil {
		return tool.Result{}, errors.New("there is nobody to hand this to")
	}
	var a struct {
		Task string `json:"task"`
	}
	if json.Unmarshal(args, &a) != nil || strings.TrimSpace(a.Task) == "" {
		return tool.Result{}, errors.New("the task was not readable — say what you want done in plain prose")
	}

	// A delegated task takes seconds to minutes, and a silent screen
	// for that long reads as broken — so the status says what is being
	// done, in the harness's voice, not "running tool".
	h.Say(delegating(a.Task))

	// The pictures that came with the person's message, named in the
	// task. Vera cannot look at a screenshot; the thing she is handing
	// this to can, and it only needs the path. The model is not asked
	// to copy them into its own prose — it never saw them, and a path
	// it invented would be a file that is not there.
	task := attach.Brief(a.Task, attached(h))

	started := time.Now()
	res, err := t.Delegate.run(ctx, task)
	elapsed := time.Since(started)
	logDelegation(h.Value(keyConversation), task, res, elapsed, err)

	// What it reached, for the harness and not for the model: the
	// Claude Code session it opened, and what that session cost. The
	// cost is money rather than tokens — Claude Code bills its own
	// way, and hiding it inside the exchange would make delegation
	// look free.
	meta := map[string]any{tool.MetaSession: res.Session, tool.MetaCost: res.Cost}

	switch {
	case err != nil:
		// The Meta goes back even on a failure: a run that was killed
		// by its timeout still spent what it spent.
		return tool.Result{Meta: meta}, err
	case res.Failed:
		return tool.Result{Text: "The task did not succeed. What came back: " + trim(res.Result, 4000), Meta: meta}, nil
	case strings.TrimSpace(res.Result) == "":
		return tool.Result{Text: "The task finished but reported nothing.", Meta: meta}, nil
	}
	return tool.Result{Text: res.Result, Meta: meta}, nil
}

type delegated struct {
	Result  string
	Cost    float64
	Turns   int
	Session string
	Failed  bool
}

func (d *Delegate) run(ctx context.Context, task string) (delegated, error) {
	if err := os.MkdirAll(d.Workspace, 0o755); err != nil {
		return delegated{}, err
	}

	timeout := d.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	permission := d.Permission
	if permission == "" {
		permission = "acceptEdits"
	}

	cmd := exec.CommandContext(ctx, "claude", "-p", task,
		"--output-format", "json",
		"--permission-mode", permission)
	cmd.Dir = d.Workspace
	cmd.Env = d.environment(ctx)

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return delegated{Failed: true}, fmt.Errorf("the task ran past %s and was stopped", timeout)
		}
		var detail string
		if ee, ok := err.(*exec.ExitError); ok {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		if detail == "" {
			detail = err.Error()
		}
		return delegated{Failed: true}, fmt.Errorf("%s", trim(detail, 300))
	}

	var res struct {
		Result   string  `json:"result"`
		IsError  bool    `json:"is_error"`
		NumTurns int     `json:"num_turns"`
		Cost     float64 `json:"total_cost_usd"`
		Session  string  `json:"session_id"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		// Claude Code answered with something that is not its own
		// envelope. The text is still probably useful.
		return delegated{Result: trim(string(out), 4000)}, nil
	}
	return delegated{
		Result:  res.Result,
		Cost:    res.Cost,
		Turns:   res.NumTurns,
		Session: res.Session,
		Failed:  res.IsError,
	}, nil
}

// delegating is what the person sees while it happens. A delegated task
// takes seconds to minutes, and a silent screen for that long reads as
// broken — so the status says what is being done, in Vera's voice, not
// "running tool".
func delegating(task string) string {
	task = strings.TrimSpace(task)
	if task == "" {
		return "Working on it…"
	}
	first := strings.SplitN(task, ". ", 2)[0]
	return "Working on it — " + strings.ToLower(first[:1]) + trimTail(first[1:], 80)
}

// trimTail ends the sentence exactly once — the ellipsis is either the
// cut or the trailing "still going", never both.
func trimTail(s string, n int) string {
	s = strings.TrimRight(s, ". ")
	if len(s) <= n {
		return s + "…"
	}
	return strings.TrimRight(s[:n], " ,;") + "…"
}

func logDelegation(conversation, task string, out delegated, elapsed time.Duration, err error) {
	slog.Info("delegated",
		"gen_ai.conversation.id", conversation,
		"task", trim(task, 300),
		"result", trim(out.Result, 500),
		"turns", out.Turns,
		"cost_usd", out.Cost,
		"session", out.Session,
		"took_ms", elapsed.Milliseconds(),
		"failed", out.Failed,
		"error", errText(err),
	)
}

// environment hands the delegate its own telemetry, and the thread that
// ties its work to ours.
//
// Claude Code speaks OpenTelemetry natively, and reads an inbound W3C
// TRACEPARENT in -p mode. So this is not a wrapper reporting on a
// subprocess from outside: the delegate's own spans —
// claude_code.interaction, its model requests, its tool calls — arrive
// as CHILDREN of the tool span Vera opened. One trace, two processes.
//
// Its cost lands under a different service.name on purpose. Vera's
// spend is OpenAI; the delegate's is Anthropic, on a different account
// and possibly a different kind of plan, and adding them together would
// produce a number that means nothing.
func (d *Delegate) environment(ctx context.Context) []string {
	// Strip the keys about to be set rather than appending over them.
	// os/exec does keep the last duplicate, but a subprocess's identity
	// should not rest on that.
	env := without(os.Environ(),
		"CLAUDE_CODE_ENABLE_TELEMETRY",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA",
		"OTEL_METRICS_EXPORTER", "OTEL_LOGS_EXPORTER", "OTEL_TRACES_EXPORTER",
		"OTEL_SERVICE_NAME", "OTEL_RESOURCE_ATTRIBUTES",
		"OTEL_METRIC_EXPORT_INTERVAL", "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE",
		"TRACEPARENT", "TRACESTATE",
	)
	if !d.Telemetry {
		// Explicitly off rather than absent: this process may have
		// OTEL_* set for Vera, and a child inheriting them would report
		// as Vera.
		return append(env, "CLAUDE_CODE_ENABLE_TELEMETRY=0")
	}

	env = append(env, telemetryEnv()...)
	// A delegated task lives seconds; the OTel default metric interval
	// is sixty of them. Left alone, a short run exports its resource
	// and exits before a single reading of what it cost — which is
	// exactly the number this was turned on for.
	env = append(env, "OTEL_METRIC_EXPORT_INTERVAL=2000")

	// The thread. Injected through the propagator rather than
	// hand-formatted, so sampling flags survive the hop.
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	if tp := carrier.Get("traceparent"); tp != "" {
		env = append(env, "TRACEPARENT="+tp)
		if st := carrier.Get("tracestate"); st != "" {
			env = append(env, "TRACESTATE="+st)
		}
	}
	return env
}

const delegateService = "claude-code"

// telemetryEnv is how a Claude Code we start reports to the same place
// Vera does, under its own service name.
func telemetryEnv() []string {
	return []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1", // traces are still beta
		"OTEL_METRICS_EXPORTER=otlp",
		"OTEL_LOGS_EXPORTER=otlp",
		"OTEL_TRACES_EXPORTER=otlp",
		// Prometheus is cumulative. Grafana Cloud accepts delta metrics
		// with a 200 and then drops them, so the default temporality
		// costs you every cost and token reading and reports nothing
		// wrong. Traces are unaffected, which is what makes it a
		// convincing dead end.
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=cumulative",
		// Last wins, so this overrides anything Vera set for itself.
		"OTEL_SERVICE_NAME=" + delegateService,
	}
}

// fleetEnv is the environment a fleet task's Claude Code gets. The
// pane's shell is not verad — it has none of verad's OTEL_* — so the
// endpoint and credentials are carried across explicitly, and the
// task id rides as a resource attribute so one task's spans and cost
// can be found by name.
func fleetEnv(telemetry bool) func(t *fleet.Task) []string {
	return func(t *fleet.Task) []string {
		if !telemetry {
			return []string{"CLAUDE_CODE_ENABLE_TELEMETRY=0"}
		}
		env := telemetryEnv()
		for _, k := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_PROTOCOL"} {
			if v := os.Getenv(k); v != "" {
				env = append(env, k+"="+v)
			}
		}
		return append(env, "OTEL_RESOURCE_ATTRIBUTES=vera.task="+t.ID+",vera.project="+shortPath(t.Project)+",vera.branch="+t.Branch)
	}
}

func without(env []string, keys ...string) []string {
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !drop[name] {
			out = append(out, kv)
		}
	}
	return out
}
