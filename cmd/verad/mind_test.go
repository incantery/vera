package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/mote/provider"
	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
	"github.com/incantery/vera/journal"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// The loop over a provider, against a scripted one: a tool round trip,
// what it accumulates, and a model that declined.
//
// What used to be here was the wire — SSE chunks, reassembled tool
// call fragments, whether the request asked for usage. That is mote's
// now, tested against its own sockets. What is left is verad's: the
// rounds, the record, and what reaches the phone.

func TestAToolRoundTripReachesTheModelAndComesBack(t *testing.T) {
	mind, _, journalDir := askingMind(t,
		callsTool(t, "call_r", "read", map[string]any{"path": "MEMORY.md"}),
		says("It is empty."))
	model := mind.Provider.(*scriptedModel)

	var answer strings.Builder
	if err := mind.think(context.Background(),
		Message{Text: "what do you know", Conversation: "c1"},
		func(f Frame) error {
			answer.WriteString(f.Delta)
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	if answer.String() != "It is empty." {
		t.Fatalf("the answer was %q", answer.String())
	}
	if model.rounds() != 2 {
		t.Fatalf("the loop ran %d rounds, want 2", model.rounds())
	}

	// The second request carries the call the model made and the
	// answer it got, in that order, tied by id.
	second := model.asked(1).Messages
	if len(second) != 3 {
		t.Fatalf("the second round carried %d messages: %+v", len(second), second)
	}
	if second[1].Role != provider.RoleAssistant || len(second[1].Calls) != 1 ||
		second[1].Calls[0].Name != "read" || second[1].Calls[0].ID != "call_r" {
		t.Fatalf("the tool call did not go back to the model: %+v", second[1])
	}
	if second[2].Role != provider.RoleTool || second[2].CallID != "call_r" {
		t.Fatalf("the tool result did not answer the call: %+v", second[2])
	}
	if second[2].Text == "" {
		t.Fatal("the tool result reached the model empty")
	}
	// And the tools went with it, from the registry, unwrapped.
	if len(model.asked(0).Tools) == 0 {
		t.Fatal("no tool definitions reached the model")
	}
	// The journal has the round.
	entries := readJournal(t, journalDir, "c1")
	if len(entries) != 1 || len(entries[0].Rounds) != 1 || entries[0].Rounds[0].Tool != "read" {
		t.Fatalf("the journal did not record the round: %+v", entries)
	}
}

// Rounds accumulate: what an exchange cost is all of them, not the
// last one — and the two cache numbers are part of the prompt rather
// than additions to it.
func TestUsageAccumulatesAcrossRounds(t *testing.T) {
	model := scripted(t,
		scriptRound{
			events: []provider.Event{provider.Calling("call_1", "nonesuch", `{}`)},
			usage:  provider.Usage{Model: "m", Input: 10, Output: 2, CacheWrite: 100},
		},
		scriptRound{
			events: []provider.Event{provider.Delta("done")},
			usage:  provider.Usage{Model: "m", Input: 5, Output: 3, CacheRead: 100},
		})
	journalDir := t.TempDir()
	mind := &Mind{Provider: model, Model: "m", History: newHistory(),
		Journal: &journal.Writer{Dir: journalDir}, instruments: newInstruments()}

	if err := mind.think(context.Background(), Message{Text: "go", Conversation: "c1"},
		func(Frame) error { return nil }); err != nil {
		t.Fatal(err)
	}

	got := readJournal(t, journalDir, "c1")
	if len(got) != 1 {
		t.Fatalf("wrote %d entries", len(got))
	}
	e := got[0]
	// 10+100 written, then 5+100 read: the prompt is all of it.
	if e.InputTokens != 215 || e.OutputTokens != 5 {
		t.Fatalf("usage did not accumulate: %d in, %d out", e.InputTokens, e.OutputTokens)
	}
	if e.CacheReadTokens != 100 || e.CacheWriteTokens != 100 {
		t.Fatalf("the cache numbers did not reach the journal: read %d, write %d",
			e.CacheReadTokens, e.CacheWriteTokens)
	}
}

// A model that declined is not an outage. It happened, it was paid
// for, and the person should see what it said.
func TestAnErrorEventIsToldRatherThanRaised(t *testing.T) {
	model := scripted(t, scriptRound{
		events: []provider.Event{provider.Fail("I will not do that."), provider.Delta("Sorry.")},
		usage:  provider.Usage{Model: "m", Input: 1, Output: 1, StopReason: "refusal"},
	})
	mind := &Mind{Provider: model, Model: "m", History: newHistory(), instruments: newInstruments()}

	var told, said string
	err := mind.think(context.Background(), Message{Text: "do the thing", Conversation: "c1"},
		func(f Frame) error {
			if f.Error != "" {
				told = f.Error
			}
			said += f.Delta
			return nil
		})
	if err != nil {
		t.Fatalf("a refusal came back as an error from think: %v", err)
	}
	if told != "I will not do that." {
		t.Fatalf("the refusal did not reach the phone: %q", told)
	}
	if said != "Sorry." {
		t.Fatalf("the reply was %q", said)
	}
}

// Thinking is the model's working, not its answer. It is not shown,
// and the journal keeps only that there was some.
func TestThinkingIsCountedAndNotSpoken(t *testing.T) {
	model := scripted(t, scriptRound{
		events: []provider.Event{
			provider.Thought("let me see"), provider.Thought(" — yes"),
			provider.Delta("Yes."),
		},
		usage: provider.Usage{Model: "m", Input: 1, Output: 1},
	})
	journalDir := t.TempDir()
	mind := &Mind{Provider: model, Model: "m", History: newHistory(),
		Journal: &journal.Writer{Dir: journalDir}, instruments: newInstruments()}

	var said string
	if err := mind.think(context.Background(), Message{Text: "well?", Conversation: "c1"},
		func(f Frame) error { said += f.Delta; return nil }); err != nil {
		t.Fatal(err)
	}
	if said != "Yes." {
		t.Fatalf("the model's working reached the person: %q", said)
	}
	if got := readJournal(t, journalDir, "c1"); len(got) != 1 || got[0].ThinkingParts != 2 {
		t.Fatalf("the journal did not count the thinking: %+v", got)
	}
}

// The cached prefix is worth having only if it is still there next
// time. The stable part of the prompt goes first and the parts that
// change go after it, so writing a memory between two exchanges of one
// conversation must not move a byte of the prefix.
func TestTheStablePrefixSurvivesAnExchange(t *testing.T) {
	place, err := home.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	model := scripted(t, says("one"), says("two"))
	mind := &Mind{Provider: model, Model: "m", History: newHistory(),
		Home: place, Memory: place.Memory(), instruments: newInstruments()}
	swallow := func(Frame) error { return nil }

	stable := mind.stable()
	if err := mind.think(context.Background(), Message{Text: "hello", Conversation: "c1"}, swallow); err != nil {
		t.Fatal(err)
	}
	// Between the two, she learns something — which is the one thing
	// that changes the prompt without anybody restarting anything.
	place.Memory().Apply(home.Revision{Add: []home.Note{
		{Name: "lives-in-vienna", Type: home.TypeUser, Fact: "Lives in Vienna."},
	}}, "c1")
	if err := mind.think(context.Background(), Message{Text: "and now", Conversation: "c1"}, swallow); err != nil {
		t.Fatal(err)
	}

	first, second := model.asked(0).System, model.asked(1).System
	if first == second {
		t.Fatal("the second prompt is the first one — the memory never reached it")
	}
	for i, sys := range []string{first, second} {
		if !strings.HasPrefix(sys, stable) {
			t.Fatalf("prompt %d does not start with the stable part:\n%s", i, sys)
		}
	}
	if mind.stable() != stable {
		t.Fatal("the stable part of the prompt changed under an exchange")
	}
	// And the request asks for it to be cached, or none of this buys
	// anything.
	if !model.asked(1).CacheSystem {
		t.Fatal("the request did not ask the provider to cache the prefix")
	}
}

// Which wire answers is decided by the model name and the keys on the
// machine, and the banner has to say which one it was.
func TestTheProviderIsChosenFromTheModelAndTheKeys(t *testing.T) {
	for _, c := range []struct {
		name       string
		model      string
		anthropic  string
		openAI     string
		effortSet  bool
		wantVendor string
		wantEffort provider.Effort
		wantThink  provider.Thinking
	}{
		{name: "a claude model with a key goes to the Messages API",
			model: "claude-opus-5", anthropic: "sk-ant-x", openAI: "sk-o",
			wantVendor: "anthropic", wantEffort: provider.EffortHigh},
		{name: "anything else goes to the OpenAI-compatible endpoint",
			model: "gpt-5.6-luna", openAI: "sk-o",
			wantVendor: "openai", wantEffort: "none", wantThink: provider.ThinkingOff},
		{name: "a claude model with no key is somebody's proxy",
			model: "claude-opus-5", openAI: "sk-o",
			wantVendor: "openai", wantEffort: "none", wantThink: provider.ThinkingOff},
		{name: "an effort somebody typed beats the workaround",
			model: "gpt-5.6-luna", openAI: "sk-o", effortSet: true,
			wantVendor: "openai", wantEffort: provider.EffortHigh, wantThink: provider.ThinkingOff},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", c.anthropic)
			t.Setenv("OPENAI_API_KEY", c.openAI)
			t.Setenv("OPENAI_BASE_URL", "")
			_, mind, how := chooseMind(mindOptions{
				Model: c.model, Effort: "high", EffortSet: c.effortSet,
			})
			if mind == nil {
				t.Fatalf("no mind at all: %s", how)
			}
			if mind.Vendor != c.wantVendor {
				t.Errorf("went to %s, want %s", mind.Vendor, c.wantVendor)
			}
			if mind.Effort != c.wantEffort {
				t.Errorf("effort is %q, want %q", mind.Effort, c.wantEffort)
			}
			if mind.Thinking != c.wantThink {
				t.Errorf("thinking is %q, want %q", mind.Thinking, c.wantThink)
			}
			if !strings.Contains(how, c.model) || !strings.Contains(how, c.wantVendor) {
				t.Errorf("the banner does not say what answers: %q", how)
			}
		})
	}
}

// Nothing to call at all is the echo, said out loud. A binary that
// silently repeats your words is one you will spend an hour debugging
// the model behind.
func TestNoWireAtAllIsTheEcho(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	// --key-file that names nothing, so the file under ~/.config is not
	// what this test is about.
	_, mind, how := chooseMind(mindOptions{
		Model: "gpt-5.6-luna", Effort: "high",
		KeyFile: filepath.Join(t.TempDir(), "there-is-no-key-here"),
	})
	if mind != nil {
		t.Fatalf("found a model to talk to: %s", how)
	}
	if !strings.HasPrefix(how, "echoing") || !strings.Contains(how, "OPENAI_API_KEY") {
		t.Errorf("the reason is not in the banner: %q", how)
	}
}

// An explicit --model wins; otherwise the profile is what says which
// model suits this agent.
func TestTheProfileNamesTheModelWhenNobodyElseDoes(t *testing.T) {
	own := &Hands{Model: "claude-opus-5"}
	if got, source := modelFor("gpt-5.6-luna", true, own); got != "gpt-5.6-luna" || source != "" {
		t.Errorf("--model did not win: %q (%s)", got, source)
	}
	got, source := modelFor("gpt-5.6-luna", false, own)
	if got != "claude-opus-5" || !strings.Contains(source, "profile.md") {
		t.Errorf("the profile's hint was not used: %q (%s)", got, source)
	}
	// No profile, or a profile that says nothing: the flag's default.
	if got, _ := modelFor("gpt-5.6-luna", false, nil); got != "gpt-5.6-luna" {
		t.Errorf("with no profile the flag stands: %q", got)
	}
	if got, _ := modelFor("gpt-5.6-luna", false, &Hands{}); got != "gpt-5.6-luna" {
		t.Errorf("a profile with no model hint should not blank it: %q", got)
	}
}

// readJournal is the exchanges one conversation left on disk.
func readJournal(t *testing.T, dir, conversation string) []journal.Entry {
	t.Helper()
	got, err := journal.Read(journal.Path(dir, conversation))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The one that matters for a bill: a metric label is a time series
// forever, so a conversation id must never become one.
func TestMetricsCarryNoUnboundedLabels(t *testing.T) {
	reader := metric.NewManualReader()
	otel.SetMeterProvider(metric.NewMeterProvider(metric.WithReader(reader)))

	mind := &Mind{Provider: scripted(t, says("ok")), Model: "m",
		History: newHistory(), instruments: newInstruments()}
	err := mind.think(context.Background(),
		Message{Text: "hello", Conversation: "conversation-that-is-unique-forever"},
		func(Frame) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}

	var found int
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			found++
			for _, attrs := range attributeSets(m) {
				for _, kv := range attrs {
					switch string(kv.Key) {
					case "gen_ai.conversation.id", "gen_ai.prompt", "gen_ai.completion",
						"gen_ai.input.messages", "gen_ai.output.messages":
						t.Fatalf("%s carries %s as a label — that is a new time series per conversation",
							m.Name, kv.Key)
					}
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("no metrics were recorded at all")
	}
}

// attributeSets pulls the label sets out of whichever aggregation the
// instrument happened to use.
type attributeKV = attribute.KeyValue

func kvs(in []attribute.KeyValue) []attributeKV { return in }

func attributeSets(m metricdata.Metrics) [][]attributeKV {
	var out [][]attributeKV
	switch data := m.Data.(type) {
	case metricdata.Histogram[float64]:
		for _, p := range data.DataPoints {
			out = append(out, kvs(p.Attributes.ToSlice()))
		}
	case metricdata.Histogram[int64]:
		for _, p := range data.DataPoints {
			out = append(out, kvs(p.Attributes.ToSlice()))
		}
	}
	return out
}

// What the prompt carries is MEMORY.md, whole, under the heading it
// has always had — the wording is what the evals were written
// against, and memory becoming a directory should not have moved it.
func TestThePromptCarriesTheIndex(t *testing.T) {
	place, err := home.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &Mind{Memory: place.Memory()}

	// Nothing known: no section at all, rather than an empty heading
	// the model has to interpret.
	if got := m.preface(); strings.Contains(got, "What you know about them") {
		t.Fatalf("an empty memory still added a section:\n%s", got)
	}

	place.Memory().Apply(home.Revision{Add: []home.Note{
		{Name: "lives-in-vienna", Type: home.TypeUser, Fact: "Lives in Vienna."},
	}}, "c1")

	got := m.preface()
	if !strings.Contains(got, "What you know about them, from earlier conversations:") {
		t.Fatalf("the heading changed:\n%s", got)
	}
	if !strings.Contains(got, "Lives in Vienna.") {
		t.Fatalf("the fact did not reach the prompt:\n%s", got)
	}
	if !strings.Contains(got, "Do not mention it, list it, or bring it up unprompted.") {
		t.Fatalf("the instruction not to perform its memory went missing:\n%s", got)
	}
	if !strings.HasPrefix(got, voice) {
		t.Fatal("memory displaced the system prompt rather than following it")
	}
}

// A project file per known repository in every prompt would be most of
// the prompt and none of it read. Only the one they named.
func TestOnlyTheProjectInPlayReachesThePrompt(t *testing.T) {
	place, err := home.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := place.Project("rook", "/src/rook", "main", []string{"checks before landing: `zig build test`"}); err != nil {
		t.Fatal(err)
	}
	if err := place.Project("mote", "/src/mote", "main", nil); err != nil {
		t.Fatal(err)
	}
	m := &Mind{Home: place}
	repos := []fleet.Repo{{Name: "rook", Root: "/src/rook"}, {Name: "mote", Root: "/src/mote"}}

	got := m.aboutProjects(repos, "how is the rook build looking")
	if !strings.Contains(got, "zig build test") {
		t.Fatalf("the named repo's file did not reach the prompt:\n%s", got)
	}
	if strings.Contains(got, "/src/mote") {
		t.Errorf("a repo nobody mentioned came along:\n%s", got)
	}
	// The front matter is bookkeeping, not something to tell a model.
	if strings.Contains(got, "root: /src/rook\nsince:") {
		t.Errorf("front matter went into the prompt:\n%s", got)
	}
	if got := m.aboutProjects(repos, "what is the weather"); got != "" {
		t.Errorf("nothing was named, so nothing should be said:\n%s", got)
	}
	// "rook" inside "rookie" is not the repository.
	if got := m.aboutProjects(repos, "I was a rookie once"); got != "" {
		t.Errorf("a substring pulled in a project note:\n%s", got)
	}
}
