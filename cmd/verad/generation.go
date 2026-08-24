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
	"log/slog"
	"os"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
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

	mind    *Mind
	labels  []attribute.KeyValue
	started time.Time
	first   time.Duration
	seen    time.Duration
	trace   string
}

func (m *Mind) begin(ctx context.Context, msg Message, text string, prior int) (context.Context, *exchange) {
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
			SystemPrompt:   m.preface(),
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
		x.rec.SetResult(agento11y.Generation{
			Input:  []agento11y.Message{agento11y.UserTextMessage(said)},
			Output: []agento11y.Message{agento11y.AssistantTextMessage(answered)},
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
