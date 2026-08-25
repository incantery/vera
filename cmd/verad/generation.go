// Generation export — the half of Grafana's agent observability that
// OTLP does not carry.
//
// There are two paths into that product and they are not
// interchangeable. Traces and metrics go over OTLP to Tempo and
// Prometheus, and power the charts. The Conversations view — the thing
// worth having, where an exchange is a readable conversation rather
// than a span — is fed by a SEPARATE generation export to the Agent
// Observability API. Sending immaculate gen_ai.* spans and expecting
// Conversations to fill up does not work, which is a thing best
// discovered here rather than while staring at an empty screen.
//
// The SDK emits the same three gen_ai metrics this used to emit by
// hand, and its own spans, through the global OTel providers. So when
// it is configured it becomes the instrumentation, and the hand-rolled
// version stays as the fallback for running without Grafana at all.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/incantery/vera/journal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// generationExportConfigured: the endpoint and the token are separate
// credentials from the OTLP ones, with their own scope, so having one
// says nothing about having the other.
func generationExportConfigured() bool {
	return os.Getenv("AGENTO11Y_ENDPOINT") != "" || os.Getenv("SIGIL_ENDPOINT") != ""
}

func newGenerationExport() (*agento11y.Client, error) {
	config, err := agento11y.ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return agento11y.NewClient(config), nil
}

// exchange is one generation, however it is being watched.
//
// Exactly one of the two is live. A nil client hands back a no-op
// recorder rather than nil, so the SDK calls need no guarding — but
// the hand-rolled span does, or every exchange would be recorded twice
// and every duration counted twice.
type exchange struct {
	rec  *agento11y.GenerationRecorder
	span trace.Span
	// rounds is what happened between the question and the answer:
	// the tool calls the model asked for and what each one said back,
	// in order. The Conversations view shows an exchange as the model
	// lived it only if these are in the record.
	rounds []agento11y.Message

	// notes is the same story for the journal on disk, kept whether or
	// not anything remote is watching; pending is the round in progress,
	// which a tool fills in (the task it opened, the session it ran)
	// before record closes it.
	notes   []journal.Round
	pending journal.Round

	mind    *Mind
	labels  []attribute.KeyValue
	started time.Time
	first   time.Duration
	seen    time.Duration
	trace   string
}

func (m *Mind) begin(ctx context.Context, msg Message, text string, prior int, system string, tools []map[string]any) (context.Context, *exchange) {
	x := &exchange{mind: m, started: time.Now()}
	x.labels = []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.provider.name", "openai"),
		attribute.String("gen_ai.request.model", m.Model),
	}

	if m.Gen != nil {
		ctx, x.rec = m.Gen.StartStreamingGeneration(ctx, agento11y.GenerationStart{
			ConversationID: msg.Conversation,
			AgentName:      serviceName,
			AgentVersion:   version,
			Model:          agento11y.ModelRef{Provider: "openai", Name: m.Model},
			// The whole prompt, attention paragraph included: the point
			// of the record is to see what the model saw.
			SystemPrompt: system,
			Tools:        toolDefinitions(tools),
		})
		// The recorder does not expose its span, but the context it
		// hands back carries it, and the log line wants the id.
		x.trace = trace.SpanContextFromContext(ctx).TraceID().String()
		return ctx, x
	}

	// No generation export, so this is the only record there is.
	ctx, x.span = m.tracer.Start(ctx, "chat "+m.Model, trace.WithSpanKind(trace.SpanKindClient))
	x.span.SetAttributes(x.labels...)
	x.span.SetAttributes(attribute.Int("vera.history.turns", prior))
	if msg.Conversation != "" {
		// Span only. As a metric label it would mint a new time series
		// for every conversation ever held.
		x.span.SetAttributes(attribute.String("gen_ai.conversation.id", msg.Conversation))
	}
	if captureContent() {
		x.span.SetAttributes(attribute.String("gen_ai.input.messages", text))
	}
	return ctx, x
}

// sign: the moment the screen stopped being empty — a status line
// counts, a token counts, whichever came first. This is the number that
// matches what waiting felt like.
func (x *exchange) sign(ctx context.Context) {
	if x.seen != 0 {
		return
	}
	x.seen = time.Since(x.started)
	x.mind.firstSign.Record(ctx, x.seen.Seconds(), metric.WithAttributes(x.labels...))
}

// firstWord: the moment the wait ended, which is the number that
// decides whether this feels alive.
func (x *exchange) firstWord(ctx context.Context) {
	if x.first != 0 {
		return
	}
	x.first = time.Since(x.started)
	if x.rec != nil {
		x.rec.SetFirstTokenAt(time.Now())
		return
	}
	x.mind.firstToken.Record(ctx, x.first.Seconds(), metric.WithAttributes(x.labels...))
}

func (x *exchange) finish(ctx context.Context, said, answered string, used usage, err error) {
	elapsed := time.Since(x.started)

	if x.rec != nil {
		output := append([]agento11y.Message{}, x.rounds...)
		if answered != "" || len(output) == 0 {
			output = append(output, agento11y.AssistantTextMessage(answered))
		}
		x.rec.SetResult(agento11y.Generation{
			Input:  []agento11y.Message{agento11y.UserTextMessage(said)},
			Output: output,
			Usage: agento11y.TokenUsage{
				InputTokens:  int64(used.Prompt),
				OutputTokens: int64(used.Completion),
			},
		}, err)
		x.rec.End()
		return
	}

	done := x.labels
	if err != nil {
		done = append(append([]attribute.KeyValue{}, x.labels...),
			attribute.String("error.type", errorType(err)))
		x.span.SetStatus(codes.Error, err.Error())
	}
	x.mind.duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(done...))

	if used.Model != "" {
		x.span.SetAttributes(attribute.String("gen_ai.response.model", used.Model))
	}
	if used.Prompt > 0 {
		x.span.SetAttributes(attribute.Int("gen_ai.usage.input_tokens", used.Prompt))
		x.mind.tokens.Record(ctx, int64(used.Prompt), metric.WithAttributes(
			append(append([]attribute.KeyValue{}, x.labels...),
				attribute.String("gen_ai.token.type", "input"))...))
	}
	if used.Completion > 0 {
		x.span.SetAttributes(attribute.Int("gen_ai.usage.output_tokens", used.Completion))
		x.mind.tokens.Record(ctx, int64(used.Completion), metric.WithAttributes(
			append(append([]attribute.KeyValue{}, x.labels...),
				attribute.String("gen_ai.token.type", "output"))...))
	}
	if captureContent() {
		x.span.SetAttributes(attribute.String("gen_ai.output.messages", answered))
	}
	x.span.End()
}

// traceID, for the log line — so a line in the terminal is one click
// from the trace it belongs to.
func (x *exchange) traceID() string {
	if x.trace != "" {
		return x.trace
	}
	if x.span != nil {
		return x.span.SpanContext().TraceID().String()
	}
	return ""
}

func (x *exchange) firstMillis() int64 { return x.first.Milliseconds() }
func (x *exchange) signMillis() int64  { return x.seen.Milliseconds() }

func shutdownGenerations(c *agento11y.Client) {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Shutdown(ctx); err != nil {
		slog.Error("generation export", "error", err.Error())
	}
}

// beginTool / endTool put a delegated task on the record as a tool
// execution — which agent observability models as a first-class thing
// rather than as a gap in the middle of a generation.
func (m *Mind) beginTool(ctx context.Context, conversation string, call toolCall, task string) (context.Context, *agento11y.ToolExecutionRecorder) {
	if m.Gen == nil {
		return ctx, nil
	}
	return m.Gen.StartToolExecution(ctx, agento11y.ToolExecutionStart{
		ToolName:       call.Function.Name,
		ToolCallID:     call.ID,
		ToolType:       "agent",
		ConversationID: conversation,
		AgentName:      serviceName,
		AgentVersion:   version,
		RequestModel:   m.Model,
	})
}

func (m *Mind) endTool(ctx context.Context, rec *agento11y.ToolExecutionRecorder, out delegated, elapsed time.Duration, err error) {
	// What a delegated task COST is money, not tokens — Claude Code
	// bills its own way, and hiding that inside the exchange's token
	// count would make delegation look free.
	labels := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "execute_tool"),
		attribute.String("gen_ai.tool.name", "delegate"),
	}
	m.duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(labels...))
	if m.toolCost != nil && out.Cost > 0 {
		m.toolCost.Record(ctx, out.Cost, metric.WithAttributes(labels...))
	}

	if rec == nil {
		return
	}
	if err != nil {
		rec.SetExecError(err)
	}
	rec.End()
}

// recordSecondary meters a model call that is not the conversation —
// extraction today, anything else later. Same instruments, different
// operation name, so the cost of remembering can be told apart from
// the cost of answering rather than quietly inflating it.
func (m *Mind) recordSecondary(ctx context.Context, operation string, used usage, elapsed time.Duration) {
	labels := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", operation),
		attribute.String("gen_ai.provider.name", "openai"),
		attribute.String("gen_ai.request.model", m.Model),
	}
	m.duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(labels...))
	if used.Prompt > 0 {
		m.tokens.Record(ctx, int64(used.Prompt), metric.WithAttributes(
			append(append([]attribute.KeyValue{}, labels...),
				attribute.String("gen_ai.token.type", "input"))...))
	}
	if used.Completion > 0 {
		m.tokens.Record(ctx, int64(used.Completion), metric.WithAttributes(
			append(append([]attribute.KeyValue{}, labels...),
				attribute.String("gen_ai.token.type", "output"))...))
	}
}

// link ties the round in progress to what it reached: a fleet task,
// a Claude Code session, a cost. Empty values leave what is there.
func (x *exchange) link(task, session string, cost float64) {
	if task != "" {
		x.pending.Task = task
	}
	if session != "" {
		x.pending.Session = session
	}
	if cost != 0 {
		x.pending.CostUSD = cost
	}
}

// record closes the round in progress for the journal.
func (x *exchange) record(call toolCall, result string, started time.Time) {
	r := x.pending
	x.pending = journal.Round{}
	r.At = started
	r.Tool = call.Function.Name
	r.CallID = call.ID
	r.Args = json.RawMessage(call.Function.Arguments)
	r.Result = result
	r.TookMs = time.Since(started).Milliseconds()
	x.notes = append(x.notes, r)
}

// entry is the exchange as the journal keeps it.
func (x *exchange) entry(msg Message, system, said, answered string, used usage, err error) journal.Entry {
	return journal.Entry{
		At:           x.started,
		Version:      version,
		Conversation: msg.Conversation,
		Device:       msg.Device,
		Model:        x.mind.Model,
		TraceID:      x.traceID(),
		System:       system,
		Said:         said,
		Answered:     answered,
		Error:        errText(err),
		InputTokens:  used.Prompt,
		OutputTokens: used.Completion,
		FirstSignMs:  x.signMillis(),
		FirstTokenMs: x.firstMillis(),
		TookMs:       time.Since(x.started).Milliseconds(),
		Rounds:       x.notes,
	}
}

// asked records a round of tool calls the model made.
func (x *exchange) asked(calls []toolCall) {
	if x.rec == nil || len(calls) == 0 {
		return
	}
	parts := make([]agento11y.Part, 0, len(calls))
	for _, c := range calls {
		parts = append(parts, agento11y.ToolCallPart(agento11y.ToolCall{
			ID:        c.ID,
			Name:      c.Function.Name,
			InputJSON: json.RawMessage(c.Function.Arguments),
		}))
	}
	x.rounds = append(x.rounds, agento11y.Message{Role: agento11y.RoleAssistant, Parts: parts})
}

// answered records what a tool said back.
func (x *exchange) answered(call toolCall, result string) {
	if x.rec == nil {
		return
	}
	x.rounds = append(x.rounds, agento11y.Message{
		Role: agento11y.RoleTool,
		Parts: []agento11y.Part{agento11y.ToolResultPart(agento11y.ToolResult{
			ToolCallID: call.ID,
			Name:       call.Function.Name,
			Content:    result,
		})},
	})
}

// toolDefinitions is the OpenAI tool list in the record's shape.
func toolDefinitions(tools []map[string]any) []agento11y.ToolDefinition {
	var out []agento11y.ToolDefinition
	for _, t := range tools {
		fn, _ := t["function"].(map[string]any)
		if fn == nil {
			continue
		}
		d := agento11y.ToolDefinition{Type: "function"}
		d.Name, _ = fn["name"].(string)
		d.Description, _ = fn["description"].(string)
		if schema, err := json.Marshal(fn["parameters"]); err == nil {
			d.InputSchema = schema
		}
		out = append(out, d)
	}
	return out
}
