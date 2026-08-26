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
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
	"github.com/incantery/vera/journal"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/incantery/mote/provider"
	"github.com/incantery/mote/tool"
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
	// Provider is the wire, and the only thing here that knows what an
	// HTTP request looks like. mote chooses it from the model name and
	// the keys on this machine; a Mind is handed the answer.
	Provider provider.Provider
	// Vendor is which wire that turned out to be — "openai" or
	// "anthropic" — for the banner and for the one telemetry label
	// that has always claimed to say so.
	Vendor string
	Model  string
	Effort provider.Effort
	// Thinking is what to ask for when there are tools in the request.
	// It exists for one model: the OpenAI-compatible endpoint verad
	// was written against refuses function tools unless reasoning is
	// off, which is a thing found at the socket rather than in a doc.
	// The Anthropic side leaves it empty and thinks adaptively.
	Thinking provider.Thinking
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
	// Home is where what she knows lives. The memory above is part of
	// it; this is the rest — the project files, read when a repository
	// is what the exchange is about.
	Home *home.Home
	// Hands is what she can do herself: mote's tools, under the
	// supervisor profile's policy. Nil is a Vera who can only ask
	// somebody else to look.
	Hands *Hands

	instruments

	// So a process that is quitting, or an eval that is finishing, can
	// wait for what it is still learning.
	learning sync.WaitGroup
}

// vendor is which wire answered — "openai" unless something said
// otherwise, which is what every record said before there were two.
func (m *Mind) vendor() string {
	if m == nil || m.Vendor == "" {
		return "openai"
	}
	return m.Vendor
}

// Tools is her hands, if she has any — nil-safe, because the echo
// mind is a nil *Mind and the banner is printed either way.
func (m *Mind) Tools() *Hands {
	if m == nil {
		return nil
	}
	return m.Hands
}

// Settle waits for anything still running behind a reply. Nothing does
// today — extraction used to, and memory is now written inside the
// exchange that decided to write it — so this is the hook rather than
// a wait, kept because an eval turn can still say "after learning".
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
// stuck, and an unbounded agent loop is an unbounded bill. Four was
// right when every tool handed work away and came back with a result;
// with tools of her own, reading a file and then writing one and then
// saying so is three rounds before a word is spoken.
const maxRounds = 8

func (m *Mind) think(ctx context.Context, msg Message, reply func(Frame) error) error {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return reply(Frame{Done: true})
	}

	prior := m.History.recall(msg.Conversation)
	// The policy's ${root} is the fleet's projects, and they change
	// while the process runs — a repository opened this morning is a
	// project this afternoon.
	m.Hands.Refresh(ctx)
	system := m.preface() + m.present(msg.Device, text)
	// Every tool she has, in one list from one registry: the delegate
	// and the fleet first — handing work away is what she should reach
	// for before doing it herself — and mote's built-ins after them.
	tools := m.Hands.Definitions()
	ctx, x := m.begin(ctx, msg, text, len(prior), system, tools)

	// The system prompt is a field of the request now rather than the
	// first message: half the APIs do not have a system role, and the
	// one that caches a prefix wants it somewhere of its own.
	messages := make([]provider.Message, 0, len(prior)+2+2*maxRounds)
	for _, t := range prior {
		if t.Role == provider.RoleAssistant {
			messages = append(messages, provider.Assistant(t.Content))
			continue
		}
		messages = append(messages, provider.User(t.Content))
	}
	messages = append(messages, provider.User(text))

	// A callback cannot fail — a consumer that wants to stop cancels
	// the context — so a phone that hung up mid-sentence is this pair:
	// the error is kept, and the cancel ends the stream.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var answer strings.Builder
	var used usage
	var err error
	var delegations int

	for round := 0; round < maxRounds; round++ {
		var calls []provider.Call
		var gone error
		// Where this round's own words start, so the assistant message
		// that carries the tool calls carries what was said before them
		// too. An API that will not take an empty assistant turn cares,
		// and a transcript that drops the sentence before the tool call
		// reads as if it was never said.
		said := answer.Len()

		spent, serr := m.Provider.Stream(ctx, m.request(system, messages, tools), func(ev provider.Event) {
			switch ev.Kind {
			case provider.KindDelta:
				x.sign(ctx)
				x.firstWord(ctx)
				answer.WriteString(ev.Text)
				if e := reply(Frame{Delta: ev.Text}); e != nil && gone == nil {
					gone = e
					cancel()
				}
			case provider.KindThinking:
				// Not shown: it is the model's working, not its answer,
				// and a phone reading it aloud would be reading the
				// wrong thing. Counted, so the journal says it thought.
				x.thought()
			case provider.KindToolCall:
				calls = append(calls, ev.Call)
			case provider.KindError:
				// The model declined, or said something that was not an
				// answer. It happened and it was paid for, so it is not
				// Stream's error — but the person should see it.
				_ = reply(Frame{Error: ev.Text})
			}
		})
		err = serr
		if gone != nil {
			// The phone going away is the reason this stopped, not
			// whatever the cancelled socket said on its way out.
			err = gone
		}

		// Rounds accumulate: what the exchange cost is all of them, not
		// the last one.
		used.add(spent)

		if err != nil || len(calls) == 0 {
			break
		}

		messages = append(messages, provider.Assistant(answer.String()[said:], calls...))
		x.asked(calls)
		for _, call := range calls {
			started := time.Now()
			// Capped for the same reason the record is: a card showing
			// a whole file is a card nobody reads, on a phone.
			_ = reply(Frame{ToolCall: &ToolCallFrame{ID: call.ID, Name: call.Name,
				Args: trim(call.Arguments, maxRecordedArgs)}})
			result := m.invoke(ctx, msg.Conversation, msg.Device, x, call, reply)
			delegations++
			x.answered(call, result)
			x.record(call, result, started)
			last := x.notes[len(x.notes)-1]
			_ = reply(Frame{ToolResult: &ToolResultFrame{ID: call.ID, Result: trim(result, 8000), DurationMs: last.TookMs, CostUSD: last.CostUSD}})
			messages = append(messages, provider.Answer(call.ID, result))
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
		"gen_ai.provider.name", m.vendor(),
		"gen_ai.request.model", m.Model,
		"gen_ai.prompt", text,
		"gen_ai.completion", answer.String(),
		"turns_recalled", len(prior),
		"delegations", delegations,
		"gen_ai.usage.input_tokens", used.Prompt,
		"gen_ai.usage.output_tokens", used.Completion,
		// The two numbers that say whether the cached prefix is being
		// read back. A cache_read of zero on the second exchange of a
		// conversation means the prompt is not stable and the saving
		// is not happening.
		"gen_ai.usage.cache_read_tokens", used.CacheRead,
		"gen_ai.usage.cache_write_tokens", used.CacheWrite,
		"thinking_parts", x.thoughts,
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

	// Extraction used to run here, behind the reply: a second model
	// call that read the exchange and decided what was worth keeping.
	// It is gone. She keeps her own memory now, with read, write and
	// edit, inside the exchange — a thing she does deliberately rather
	// than a thing that happens to her. See aboutMemory.

	return reply(Frame{Done: true})
}

// invoke runs one tool call and returns what the model should be told.
//
// It is a lookup and nothing else. Everything that used to be decided
// here — which tool this is, whether it may run, what it cost, what
// goes in the journal — is invokeTool's, for every tool alike.
func (m *Mind) invoke(ctx context.Context, conversation, device string, x *exchange, call provider.Call, reply func(Frame) error) string {
	t, ok := m.Hands.Tool(call.Name)
	if !ok {
		return "That tool does not exist."
	}
	return m.invokeTool(ctx, conversation, device, x, t, call, reply)
}

// request is one turn as the provider is asked for it. It is here
// rather than inline because dictation asks for the same thing with
// no tools and no history, and the two should not drift.
func (m *Mind) request(system string, messages []provider.Message, tools []tool.Definition) provider.Request {
	req := provider.Request{
		Model:    m.Model,
		System:   system,
		Messages: messages,
		Tools:    tools,
		Effort:   m.Effort,
		// The stable prefix — the tools, then the part of the prompt
		// that does not change — written once and read back on every
		// turn after. A provider with no cache to be told about
		// ignores it.
		CacheSystem: true,
	}
	// Only when there are tools, because that is the only case the
	// workaround was ever for. See Mind.Thinking.
	//
	// On the Anthropic side Thinking is empty, so the model thinks the
	// way it thinks by default — which for a Claude 5 is adaptively.
	// There is a live gap behind that: the Messages API wants an
	// assistant turn's thinking blocks passed back unchanged on the
	// next turn, and provider.Message has nowhere to carry them, so
	// this loop drops them. If a second round of one tool exchange
	// against a claude model comes back 400 about ordering or a
	// signature, that is why, and the fix is in mote rather than here.
	if len(tools) > 0 {
		req.Thinking = m.Thinking
	}
	return req
}

// usage is what an exchange spent, in the terms the record has always
// used: Prompt is the WHOLE prompt, cached tokens included, because
// that is what "input tokens" has meant in every journal line and
// every dashboard so far. CacheRead and CacheWrite say how much of it
// came from, or went into, the cache — they are part of Prompt, not
// additions to it.
type usage struct {
	Model      string
	Prompt     int
	Completion int
	CacheRead  int
	CacheWrite int
}

// add folds one round's usage in. A provider reports its four counts
// without overlap; Prompt puts them back together.
func (u *usage) add(spent provider.Usage) {
	if spent.Model != "" {
		u.Model = spent.Model
	}
	u.Prompt += spent.Input + spent.CacheRead + spent.CacheWrite
	u.Completion += spent.Output
	u.CacheRead += spent.CacheRead
	u.CacheWrite += spent.CacheWrite
}

// trim caps what the model is told. Every tool result goes through it:
// four megabytes of a log file has not helped anybody, least of all
// the model paying for it by the token.
func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
	base := m.stable()
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

// stable is the part of the system prompt that does not change while
// the process runs: her voice, the profile's own words, where her home
// is and what her memory files look like.
//
// It is a separate function because it is FIRST, and being first is
// what makes it worth caching. A provider that caches a prefix caches
// the longest run of the request it has seen before — the tools, then
// this. Everything that changes goes after it, in order of how often:
// the memory index (rarely, and only when she writes one), then the
// paragraph about this minute (every exchange). Move any of it in
// front of this and the cache is written fresh every time, which costs
// more than not caching at all.
func (m *Mind) stable() string {
	base := m.Preface
	if base == "" {
		base = voice
	}
	if m.Hands != nil {
		if p := strings.TrimSpace(m.Hands.Prompt); p != "" {
			base += "\n\n" + p
		}
		base += m.Hands.Where() + m.aboutMemory()
	}
	return base
}

// aboutMemory is the part of the prompt that makes memory hers.
//
// It used to be a second model call behind every reply that read the
// exchange and wrote what it thought was worth keeping. That is the
// wrong shape for the same reason a diary written by somebody else is:
// she never chose any of it, could not correct it, and the one thing
// she could not do was throw a wrong fact away. Now the files are just
// files and she has the tools, so this says where they are and what
// they look like — and, mostly, that changing them is rare.
func (m *Mind) aboutMemory() string {
	root := "~/vera"
	if m.Home != nil && m.Home.Root != "" {
		root = m.Home.Root
	}
	return "\n\nYour memory is yours to keep, in " + root + ".\n" +
		home.Index + " is the index — one line per fact, `- [slug](" + home.MemoryDir + "/slug.md) — the fact in one line`. " +
		home.MemoryDir + "/<slug>.md is the fact itself: front matter with name, description, type " +
		"(user, feedback, project or reference) and since (a date), then a sentence or two of prose. " +
		"A slug says what the fact is about, in lowercase words joined by hyphens — `lives-in-vienna`, " +
		"`prefers-short-answers` — and the name in the front matter is that same slug, spelled the same " +
		"way as the file.\n" +
		"Read them with read and search. Maintain them with write and edit: when they say something that " +
		"will still be true next month, write its file and add its line to the index; when something you " +
		"know turns out to be wrong, rewrite that file rather than adding a second one that contradicts it. " +
		"To drop a fact entirely, take its line out of the index and delete the file with delete. " +
		"The index line and the file must say the same thing.\n" +
		"Do this quietly and rarely, and never announce it. Most conversations change nothing, and a memory " +
		"that grows every turn has learned nothing in particular."
}

// present is the paragraph about this moment — which application has
// their attention on which machine. It is appended after the preface
// rather than folded into it because the preface is a stable prompt
// worth caching and evaluating, and this changes every minute.
//
// The wording tells the model the limits of what is known. "Ghostty has
// focus" is a fact; what is in that terminal is not, unless something
// reported it, and the model is told not to pretend otherwise.
func (m *Mind) present(device, said string) string {
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
		b.WriteString(m.aboutProjects(repos, said))
	}
	return b.String()
}

// aboutProjects is what she knows about the repository this exchange
// is about — where it is, what it branches from, what she has landed
// in it. Only the ones in play: a project file per known repository in
// every prompt would be most of the prompt, and none of it read.
//
// "In play" is either of the two things that can put a repo in front of
// her: they named it, or the fleet is working in it right now.
func (m *Mind) aboutProjects(repos []fleet.Repo, said string) string {
	if m.Home == nil || len(repos) == 0 {
		return ""
	}
	wanted := map[string]bool{}
	for _, r := range repos {
		if mentions(said, r.Name) {
			wanted[r.Root] = true
		}
	}
	// The store rather than Fleet.Tasks: this runs before every model
	// call, and what is open is on disk. Tasks() would ask the
	// multiplexer, which is a subprocess, for liveness nobody here
	// needs.
	if m.Fleet != nil && m.Fleet.Store != nil {
		if tasks, err := m.Fleet.Store.List(); err == nil {
			for _, t := range tasks {
				if !t.Closed {
					wanted[t.Project] = true
				}
			}
		}
	}

	var b strings.Builder
	shown := 0
	for _, r := range repos {
		if !wanted[r.Root] || shown >= maxProjectNotes {
			continue
		}
		note, ok := m.Home.Note(r.Name, r.Root)
		if !ok {
			continue
		}
		if shown == 0 {
			b.WriteString("\n\nWhat you know about the repositories this is about, from working in them before:")
		}
		b.WriteString("\n\n" + trim(note, projectNoteCap))
		shown++
	}
	if shown > 0 {
		b.WriteString("\n\nUse it the way you use anything else you remember: to answer without asking again. Do not recite it.")
	}
	return b.String()
}

// Two, because a question is about one repository and occasionally
// about two, and never about nine.
const (
	maxProjectNotes = 2
	projectNoteCap  = 2000
)

// mentions is a word match rather than a substring one: "rook" in
// "rookie" is not the repository, and a project note pulled in by an
// accident of spelling is prompt nobody asked for.
func mentions(said, name string) bool {
	if name == "" {
		return false
	}
	said, name = strings.ToLower(said), strings.ToLower(name)
	for i := 0; ; {
		j := strings.Index(said[i:], name)
		if j < 0 {
			return false
		}
		j += i
		before := j == 0 || !isWordByte(said[j-1])
		end := j + len(name)
		after := end == len(said) || !isWordByte(said[end])
		if before && after {
			return true
		}
		i = j + 1
	}
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
