// A mind, briefly.
//
// One model call per exchange, streamed, carrying the last few turns of
// the conversation it belongs to. It still has no memory of you — only
// of what was said a minute ago, and only until the process ends.
//
// Streaming is the part that matters today. A finished answer arriving
// in one piece tells you nothing about what talking to this feels like;
// the shape of the wait, and the moment the first word lands, is the
// whole question.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
	"github.com/incantery/vera/journal"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Voice-first, so the default posture is short. A model that answers a
// spoken question with six paragraphs is not being helpful, it is being
// a web page.
const voice = `You are Vera, answering someone who is talking to their phone.
Be brief and direct — a sentence or two unless more is genuinely needed.
Speak plainly. Do not use markdown, headings or bullet points.
If you do not know something, say so.
You can see the recent turns of this conversation.

You can hand real work to Claude Code, a capable agent on this Mac, using the delegate tool — reading and writing files, running commands, looking things up. Reach for it when answering means DOING something rather than knowing something. Answer directly when you can; delegating a question you could simply answer wastes a minute of their time.

When a delegated task comes back, tell them what happened in a sentence. Do not narrate the steps.

Any work on code or in a repository — inspecting one, changing one, investigating something in one — goes to the fleet tool, always: it starts a separate agent in its own copy of the repository that keeps working after this conversation ends. The delegate tool is only for a quick lookup or a one-off command they wait a minute for. Ask the fleet when they want to know how things are going, and pass their replies to a task that is waiting on them. Speak of tasks in their words — what it is doing, whether it needs them — never in terms of branches, panes or ids unless they ask.`

type Mind struct {
	Client   *http.Client
	Base     string
	Key      string
	Model    string
	Effort   string
	Preface  string
	History  *History
	Memory   *home.Memory
	Delegate *Delegate
	// Fleet is where work that outlives the conversation goes. Nil
	// when there is no multiplexer to put a pane in.
	Fleet *fleet.Fleet
	// Projects knows which repositories have a pane open, so "the rook
	// repo" is a path the fleet can be pointed at. Nil is fine.
	Projects *fleet.Projects
	Gen      *agento11y.Client
	// Journal is the record on disk, every exchange; nil keeps none.
	Journal *journal.Writer
	// Attention is what the devices have reported about where the
	// person is looking. Nil is fine: a mind with no senses.
	Attention *Attention

	instruments

	// So a process that is quitting, or an eval that is finishing, can
	// wait for what it is still learning.
	learning sync.WaitGroup
}

// Settle waits for outstanding extraction. Without it a short-lived
// run exits mid-thought and remembers nothing.
func (m *Mind) Settle() {
	if m != nil {
		m.learning.Wait()
	}
}

// The three instruments Grafana's agent observability reads, by their
// conventional names. Built once: an instrument created per call is a
// new time series per call.
type instruments struct {
	duration   metric.Float64Histogram
	firstToken metric.Float64Histogram
	tokens     metric.Int64Histogram
	toolCost   metric.Float64Histogram
	firstSign  metric.Float64Histogram
	tracer     trace.Tracer
}

func newInstruments() instruments {
	m := otel.Meter("vera/mind")
	duration, _ := m.Float64Histogram("gen_ai.client.operation.duration",
		metric.WithUnit("s"), metric.WithDescription("How long a generation took."))
	firstToken, _ := m.Float64Histogram("gen_ai.client.time_to_first_token",
		metric.WithUnit("s"), metric.WithDescription("How long until the first word arrived."))
	tokens, _ := m.Int64Histogram("gen_ai.client.token.usage",
		metric.WithUnit("{token}"), metric.WithDescription("Tokens spent on a generation."))
	toolCost, _ := m.Float64Histogram("vera.tool.cost",
		metric.WithUnit("USD"), metric.WithDescription("What a delegated task cost."))
	// Not the same as time_to_first_token, and the difference is the
	// point. The convention's metric measures the model's first token,
	// which for a delegating exchange arrives only after the delegate
	// has finished — eleven seconds, while the person watched a status
	// line appear in one. This measures what they actually saw.
	firstSign, _ := m.Float64Histogram("vera.time_to_first_sign",
		metric.WithUnit("s"), metric.WithDescription("How long until anything at all reached the phone."))
	return instruments{duration, firstToken, tokens, toolCost, firstSign, otel.Tracer("vera/mind")}
}

// think is the Handler. Everything transport-shaped stays on the other
// side of the interface, which is why this can be swapped for the echo
// without either of them knowing.
// maxRounds bounds the loop. A model that keeps delegating is a model
// stuck, and an unbounded agent loop is an unbounded bill.
const maxRounds = 4

func (m *Mind) think(ctx context.Context, msg Message, reply func(Frame) error) error {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return reply(Frame{Done: true})
	}

	prior := m.History.recall(msg.Conversation)
	system := m.preface() + m.present(msg.Device)
	var tools []map[string]any
	if m.Delegate != nil {
		tools = append(tools, m.Delegate.tool(m.Fleet != nil))
	}
	if m.Fleet != nil {
		tools = append(tools, fleetTool())
	}
	ctx, x := m.begin(ctx, msg, text, len(prior), system, tools)

	messages := make([]chatMessage, 0, len(prior)+2+2*maxRounds)
	messages = append(messages, chatMessage{Role: "system", Content: system})
	for _, t := range prior {
		messages = append(messages, chatMessage{Role: t.Role, Content: t.Content})
	}
	messages = append(messages, chatMessage{Role: "user", Content: text})

	var answer strings.Builder
	var used usage
	var err error
	var delegations int

	for round := 0; round < maxRounds; round++ {
		var calls []toolCall
		var spent usage
		calls, err = m.stream(ctx, messages, tools, func(delta string) error {
			x.sign(ctx)
			x.firstWord(ctx)
			answer.WriteString(delta)
			return reply(Frame{Delta: delta})
		}, &spent)

		// Rounds accumulate: what the exchange cost is all of them, not
		// the last one.
		used.Model = spent.Model
		used.Prompt += spent.Prompt
		used.Completion += spent.Completion

		if err != nil || len(calls) == 0 {
			break
		}

		messages = append(messages, chatMessage{Role: "assistant", ToolCalls: calls})
		x.asked(calls)
		for _, call := range calls {
			started := time.Now()
			_ = reply(Frame{ToolCall: &ToolCallFrame{ID: call.ID, Name: call.Function.Name, Args: call.Function.Arguments}})
			result := m.invoke(ctx, msg.Conversation, msg.Device, x, call, reply)
			delegations++
			x.answered(call, result)
			x.record(call, result, started)
			last := x.notes[len(x.notes)-1]
			_ = reply(Frame{ToolResult: &ToolResultFrame{ID: call.ID, Result: trim(result, 8000), DurationMs: last.TookMs, CostUSD: last.CostUSD}})
			messages = append(messages, chatMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
	}

	x.finish(ctx, text, answer.String(), used, err)
	spend(ctx, used.Prompt, used.Completion)

	// On disk before anything else: the log line and the remote record
	// are copies; this is the one `vera dump` reads.
	if m.Journal != nil {
		if jerr := m.Journal.Write(x.entry(msg, system, text, answer.String(), used, err)); jerr != nil {
			slog.Error("journal", "error", jerr.Error())
		}
	}

	// One line per exchange regardless of what else is watching. Both
	// halves of the conversation are in here on purpose: the question
	// being debugged next week is "why did it say that", and no
	// aggregate answers that after the fact.
	slog.Info("exchange",
		"gen_ai.conversation.id", msg.Conversation,
		"gen_ai.request.model", m.Model,
		"gen_ai.prompt", text,
		"gen_ai.completion", answer.String(),
		"turns_recalled", len(prior),
		"delegations", delegations,
		"gen_ai.usage.input_tokens", used.Prompt,
		"gen_ai.usage.output_tokens", used.Completion,
		"first_token_ms", x.firstMillis(),
		"first_sign_ms", x.signMillis(),
		"trace_id", x.traceID(),
		"error", errText(err),
	)

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil // the phone hung up; not an error to report
		}
		return err
	}

	// After the answer, not before: an exchange that failed did not
	// happen, and half of one poisons every exchange after it.
	m.History.remember(msg.Conversation, text, answer.String())

	// And behind the reply, never in front of it.
	if m.Memory != nil {
		m.learning.Add(1)
		go func(said, answered string) {
			defer m.learning.Done()
			m.remember(msg.Conversation, said, answered)
		}(text, answer.String())
	}

	return reply(Frame{Done: true})
}

// invoke runs one tool call and returns what the model should be told.
// Errors come back as text rather than as a failed exchange: a delegate
// that could not finish is information the model can work with, and a
// person asking a question should not get a stack trace because a
// subprocess was unhappy.
func (m *Mind) invoke(ctx context.Context, conversation, device string, x *exchange, call toolCall, reply func(Frame) error) string {
	if call.Function.Name == "fleet" && m.Fleet != nil {
		return m.invokeFleet(ctx, conversation, device, x, call, reply)
	}
	if call.Function.Name != "delegate" || m.Delegate == nil {
		return "That tool does not exist."
	}

	var args struct {
		Task string `json:"task"`
	}
	if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil || strings.TrimSpace(args.Task) == "" {
		return "The task was not readable. Say what you want done in plain prose."
	}

	// A status IS something appearing, so it stops the "first sign"
	// clock even though no token has been produced.
	x.sign(ctx)
	_ = reply(Frame{Status: delegating(args.Task)})

	started := time.Now()
	ctx, rec := m.beginTool(ctx, conversation, call, args.Task)
	out, err := m.Delegate.run(ctx, args.Task)
	elapsed := time.Since(started)

	m.endTool(ctx, rec, out, elapsed, err)
	logDelegation(conversation, args.Task, out, elapsed, err)
	x.link("", out.Session, out.Cost)

	if err != nil {
		return "The task could not be completed: " + err.Error()
	}
	if out.Failed {
		return "The task did not succeed. What came back: " + trim(out.Result, 4000)
	}
	if strings.TrimSpace(out.Result) == "" {
		return "The task finished but reported nothing."
	}
	return trim(out.Result, 8000)
}

type usage struct {
	Model      string
	Prompt     int
	Completion int
}

// chatMessage is the wire shape of one message, including the two
// shapes a tool round trip needs: an assistant turn that asked for a
// tool, and a tool turn that answered.
type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name string `json:"name"`
	// Arguments is JSON, as a string, and it arrives one fragment at a
	// time across many chunks.
	Arguments string `json:"arguments"`
}

// stream does one chat-completions call and hands back content as it
// arrives, plus any tool calls the model asked for instead.
func (m *Mind) stream(ctx context.Context, messages []chatMessage, tools []map[string]any, onDelta func(string) error, used *usage) ([]toolCall, error) {
	body := map[string]any{
		"model":  m.Model,
		"stream": true,
		// Without this the stream simply ends and the token counts —
		// which is to say the cost — are never reported at all.
		"stream_options": map[string]any{"include_usage": true},
		"messages":       messages,
	}
	if len(tools) > 0 {
		body["tools"] = tools
		// This model refuses function tools on /v1/chat/completions
		// unless reasoning is off. Found at the socket, not in a doc.
		body["reasoning_effort"] = "none"
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	base := m.Base
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.Key != "" {
		req.Header.Set("Authorization", "Bearer "+m.Key)
	}

	res, err := m.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("I couldn't reach the model.")
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		// The API's own words are more useful than a status code, but
		// they are also long and full of JSON, so they go to the log
		// and a short sentence goes to the phone.
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		slog.Error("model refused", "status", res.StatusCode, "body", string(detail))
		switch res.StatusCode {
		case http.StatusUnauthorized:
			return nil, errors.New("The model rejected my key.")
		case http.StatusTooManyRequests:
			return nil, errors.New("The model is rate limiting me.")
		default:
			return nil, fmt.Errorf("The model answered with %d.", res.StatusCode)
		}
	}

	// Tool calls arrive in fragments keyed by index — the name in one
	// chunk, the arguments a few characters at a time across dozens.
	pending := map[int]*toolCall{}
	var order []int

	scan := bufio.NewScanner(res.Body)
	scan.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int          `json:"index"`
						ID       string       `json:"id"`
						Type     string       `json:"type"`
						Function toolFunction `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		if chunk.Model != "" {
			used.Model = chunk.Model
		}
		// The usage chunk arrives last and carries no choices.
		if chunk.Usage != nil {
			used.Prompt = chunk.Usage.PromptTokens
			used.Completion = chunk.Usage.CompletionTokens
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				if err := onDelta(choice.Delta.Content); err != nil {
					return nil, err
				}
			}
			for _, frag := range choice.Delta.ToolCalls {
				call, seen := pending[frag.Index]
				if !seen {
					call = &toolCall{Type: "function"}
					pending[frag.Index] = call
					order = append(order, frag.Index)
				}
				if frag.ID != "" {
					call.ID = frag.ID
				}
				if frag.Type != "" {
					call.Type = frag.Type
				}
				if frag.Function.Name != "" {
					call.Function.Name = frag.Function.Name
				}
				call.Function.Arguments += frag.Function.Arguments
			}
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}

	calls := make([]toolCall, 0, len(order))
	for _, i := range order {
		calls = append(calls, *pending[i])
	}
	return calls, nil
}

// errorType is the low-cardinality label; the sentence itself is too
// various to be a metric dimension.
func errorType(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "error"
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// findKey looks where vera already looks, so one key serves both.
func findKey(explicit string) string {
	if explicit != "" {
		if b, err := os.ReadFile(explicit); err == nil {
			return strings.TrimSpace(string(b))
		}
		return ""
	}
	if k := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); k != "" {
		return k
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home, ".config", "vera", "openai_key"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// preface is the system prompt actually in force, with whatever Vera
// remembers folded in.
//
// What goes in is MEMORY.md — the index of her home, one line per
// memory, whole. Not the memory files themselves: the index is what is
// small enough to send on every exchange, and the bodies are for a
// person reading them and for the tools Vera gets shortly.
//
// Memory is stated as things known rather than as instructions, and
// with an explicit note not to bring them up unprompted — otherwise a
// model reads a list of facts about someone as a list of topics it has
// been asked to raise, and every answer turns into a performance of
// how much it remembers.
func (m *Mind) preface() string {
	base := m.Preface
	if base == "" {
		base = voice
	}
	if m.Memory == nil {
		return base
	}
	known := m.Memory.Recite()
	if known == "" {
		return base
	}
	return base + "\n\nWhat you know about them, from earlier conversations:\n" +
		known + "\nUse this only when it is relevant. Do not mention it, list it, or bring it up unprompted."
}

// present is the paragraph about this moment — which application has
// their attention on which machine. It is appended after the preface
// rather than folded into it because the preface is a stable prompt
// worth caching and evaluating, and this changes every minute.
//
// The wording tells the model the limits of what is known. "Ghostty has
// focus" is a fact; what is in that terminal is not, unless something
// reported it, and the model is told not to pretend otherwise.
func (m *Mind) present(device string) string {
	var b strings.Builder
	if m.Attention != nil {
		if now := m.Attention.Describe(time.Now(), device); now != "" {
			b.WriteString("\n\nWhere their attention is right now, as reported by their devices:\n" + now +
				"\nThis tells you which application is in front of them, not what is inside it. " +
				"When they say \"this\" or \"here\", it most likely refers to that application. " +
				"Do not describe or guess at its contents unless they were reported above. Do not recite this unprompted.")
		}
	}
	if m.Projects != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		repos := m.Projects.Known(ctx)
		cancel()
		if len(repos) > 0 {
			b.WriteString("\n\nRepositories they have open in their terminal, by name and path — when they name one of these, pass its path as the fleet task's project:")
			for _, r := range repos {
				b.WriteString("\n- " + r.Name + ": " + r.Root)
			}
		}
	}
	return b.String()
}
