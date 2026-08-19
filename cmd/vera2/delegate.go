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
	"fmt"
	"go.opentelemetry.io/otel/propagation"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "vera2", "workspace")
}

type Delegate struct {
	Workspace  string
	Permission string
	Timeout    time.Duration

	// Telemetry: whether the delegate reports its own work to the same
	// place Vera does.
	Telemetry bool
}

// tool is what the model is told it can reach for. The description is
// load-bearing — it is the entire basis on which the model decides
// between answering and delegating, so it says what the delegate is
// GOOD at rather than what it is.
func (d *Delegate) tool() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "delegate",
			"description": "Hand a task to Claude Code, a capable coding agent running on this Mac. " +
				"It can read and write files, run shell commands, use git, search the web, and work " +
				"through a multi-step task on its own. Use it when answering requires DOING something " +
				"on the machine, or looking something up that you cannot know. Do not use it for " +
				"conversation, opinions, or anything you can simply answer.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{
						"type": "string",
						"description": "What to accomplish, in plain prose, with everything needed to " +
							"act on it alone. Say the goal, not the steps.",
					},
				},
				"required": []string{"task"},
			},
		},
	}
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

	env = append(env,
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1", // traces are still beta
		"OTEL_METRICS_EXPORTER=otlp",
		"OTEL_LOGS_EXPORTER=otlp",
		"OTEL_TRACES_EXPORTER=otlp",
		// A delegated task lives seconds; the OTel default metric
		// interval is sixty of them. Left alone, a short run exports
		// its resource and exits before a single reading of what it
		// cost — which is exactly the number this was turned on for.
		"OTEL_METRIC_EXPORT_INTERVAL=2000",
		// Prometheus is cumulative. Grafana Cloud accepts delta metrics
		// with a 200 and then drops them, so the default temporality
		// costs you every cost and token reading and reports nothing
		// wrong. Traces are unaffected, which is what makes it a
		// convincing dead end.
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=cumulative",
		// Last wins, so this overrides anything Vera set for itself.
		"OTEL_SERVICE_NAME="+delegateService,
	)

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
