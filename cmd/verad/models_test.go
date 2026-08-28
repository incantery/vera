package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// noKeys clears every key mote reads, so a test starts from a machine
// that can reach nothing and says what it can reach.
func noKeys(t *testing.T) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv(EnvModels, "")
}

func names(rows []ModelRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

// The one rule the table must never break: a provider with no key
// contributes no rows. A picker that offers a model this machine
// cannot call is a picker that hands you an error three keystrokes
// later.
func TestAProviderWithNoKeyContributesNoRows(t *testing.T) {
	noKeys(t)

	// Nothing at all.
	if rows := models((&Wires{}).reach(), ""); len(rows) != 0 {
		t.Fatalf("a machine with no keys offered %v", names(rows))
	}

	// An OpenAI key, and only OpenAI rows.
	rows := models((&Wires{OpenAIKey: "sk-test"}).reach(), "")
	if len(rows) == 0 {
		t.Fatal("an OpenAI key should reach the OpenAI models")
	}
	for _, r := range rows {
		if r.Provider != "openai" {
			t.Errorf("no Anthropic key, but %s (%s) is offered", r.Name, r.Provider)
		}
	}

	// An endpoint that needs no key is a way to reach one too.
	if rows := models((&Wires{OpenAIBase: "http://localhost:1234/v1"}).reach(), ""); len(rows) == 0 {
		t.Error("a base URL with no key should still reach the OpenAI models")
	}

	// The Anthropic half is read from the environment, the way mote
	// reads it.
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	rows = models((&Wires{}).reach(), "")
	if got := names(rows); len(got) == 0 || !strings.HasPrefix(got[0], "claude") {
		t.Fatalf("with only an Anthropic key: %v", got)
	}
}

// What the table exists to encode: gpt-5.6 takes effort none and
// nothing else, gpt-5 takes the dial, and every row says whether its
// tokens can be turned into dollars.
func TestTheTableSaysWhatEachModelTakes(t *testing.T) {
	noKeys(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	rows := models((&Wires{}).reach(), "")

	by := map[string]ModelRow{}
	for _, r := range rows {
		by[r.Name] = r
	}
	if got := by["gpt-5.6-luna"]; len(got.Efforts) != 1 || got.Efforts[0] != "none" || got.Note == "" {
		t.Errorf("gpt-5.6-luna: %+v — it takes effort none only, and should say why", got)
	}
	if got := by["gpt-5"]; strings.Join(got.Efforts, ",") != "none,low,medium,high" {
		t.Errorf("gpt-5 efforts: %v", got.Efforts)
	}
	if got := by["claude-opus-5"]; strings.Join(got.Efforts, ",") != "low,medium,high,max" {
		t.Errorf("claude-opus-5 efforts: %v", got.Efforts)
	}
	for _, r := range rows {
		if !r.Priced {
			t.Errorf("%s is in the table but `price` does not know it", r.Name)
		}
	}
}

// $VERA_MODELS adds a row the table has never heard of, corrects one it
// has, and a bad entry is dropped without taking the good ones with it.
func TestTheEnvironmentAddsAndCorrectsRows(t *testing.T) {
	noKeys(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	rows := models((&Wires{}).reach(), "my-local-7b=openai:none|high, gpt-5=openai:none, nonsense, gpt-5-mini=openai:sideways")

	by := map[string]ModelRow{}
	for _, r := range rows {
		by[r.Name] = r
	}
	added, ok := by["my-local-7b"]
	if !ok || strings.Join(added.Efforts, ",") != "none,high" {
		t.Errorf("added row: %+v", added)
	}
	if added.Priced {
		t.Error("a model no price table knows is not a free one — it is an unpriced one")
	}
	if got := by["gpt-5"]; strings.Join(got.Efforts, ",") != "none" {
		t.Errorf("an entry naming a model in the table should correct it: %v", got.Efforts)
	}
	if _, ok := by["gpt-5-mini"]; !ok {
		t.Error("a bad entry took a good row with it")
	}
	if got := by["gpt-5-mini"]; strings.Join(got.Efforts, ",") != "none,low,medium,high" {
		t.Errorf("a bad entry rewrote the row it names: %v", got.Efforts)
	}
	if _, bad := parseModels("nonsense, a=openai:sideways, b=:low, c=openai"); len(bad) != 3 {
		t.Errorf("bad entries: %v", bad)
	}
}

// GET /models is what the picker draws: what is in force, what this
// conversation chose if it chose anything, and the rows.
func TestModelsOverTheWire(t *testing.T) {
	noKeys(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	m := mindFor(t)
	base, id := serveLANWith(t, echo, func(l *lanTransport) { l.picker = m })

	get := func(path string) ModelsAnswer {
		t.Helper()
		req, _ := http.NewRequest("GET", base+path, nil)
		req.Header.Set("Authorization", "Bearer "+id.Secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %s", path, resp.Status)
		}
		var got ModelsAnswer
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	got := get("/models")
	if got.Default.Model != "gpt-5.6-luna" || got.Default.From != fromBuiltin {
		t.Errorf("default: %+v", got.Default)
	}
	if got.Conversation != nil {
		t.Errorf("a conversation that chose nothing has nothing to report: %+v", got.Conversation)
	}
	if len(got.Models) == 0 {
		t.Fatal("no rows")
	}

	// A conversation that has chosen says so, and that is the row the
	// picker ticks.
	if _, err := m.Choose("c1", "gpt-5-mini", "low"); err != nil {
		t.Fatal(err)
	}
	got = get("/models?conversation=c1")
	if got.Conversation == nil || got.Conversation.Model != "gpt-5-mini" || got.Conversation.Effort != "low" {
		t.Errorf("conversation: %+v", got.Conversation)
	}
	if got.Default.Model != "gpt-5.6-luna" {
		t.Errorf("one conversation's choice moved the daemon's own: %+v", got.Default)
	}
}

// The saved default sits between --model and the profile: it outranks
// the profile, the flag outranks it, and it survives the process.
func TestTheSavedDefaultOutranksTheProfileAndNotTheFlag(t *testing.T) {
	noKeys(t)
	path := filepath.Join(t.TempDir(), "model.json")
	m := mindFor(t)
	m.Default = &Default{Path: path}

	res, err := m.SetDefault("claude-opus-5", "high")
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "claude-opus-5" || res.ModelFrom != fromSaved {
		t.Fatalf("after setting: %+v", res)
	}

	// Every exchange with no conversation of its own follows it.
	got, err := m.choose("c-fresh", Pick{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "claude-opus-5" || got.ModelFrom != fromSaved || got.Effort != "high" {
		t.Fatalf("the saved default did not reach an exchange: %+v", got)
	}

	// A conversation's own choice is still more specific.
	if _, err := m.Choose("c1", "gpt-5-mini", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.choose("c1", Pick{}); got.Model != "gpt-5-mini" {
		t.Fatalf("the saved default outranked a conversation: %+v", got)
	}

	// A daemon started with --model keeps it: the choice is written
	// down for the next one rather than ignored.
	flagged := mindFor(t)
	flagged.Default = &Default{Path: path}
	flagged.ModelFrom = fromFlag
	if got, _ := flagged.choose("", Pick{}); got.Model != "gpt-5.6-luna" || got.ModelFrom != fromFlag {
		t.Fatalf("the saved default outranked --model: %+v", got)
	}

	// And a new process reads the same file, over the profile.
	again := mindFor(t)
	again.Default = &Default{Path: path}
	if got, _ := again.choose("", Pick{}); got.Model != "claude-opus-5" || got.ModelFrom != fromSaved {
		t.Fatalf("the saved default did not survive the process: %+v", got)
	}
	if model, source := modelFor("gpt-5.6-luna", false, Pick{Model: "claude-opus-5"}, &Hands{Model: "gpt-5-mini"}); model != "claude-opus-5" || source != fromSaved {
		t.Fatalf("startup put the profile over the saved default: %q (%s)", model, source)
	}

	// Clearing it puts the daemon back where it was.
	if _, err := again.SetDefault("", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := again.choose("", Pick{}); got.Model != "gpt-5.6-luna" || got.ModelFrom != fromBuiltin {
		t.Fatalf("clearing left %+v", got)
	}
}

// A model with no wire is refused before it is written down — the same
// rule the per-conversation choice goes by.
func TestASavedDefaultIsCheckedBeforeItIsKept(t *testing.T) {
	noKeys(t)
	path := filepath.Join(t.TempDir(), "model.json")
	m := mindFor(t)
	m.Default = &Default{Path: path}
	if _, err := m.SetDefault("no-such-model", ""); err == nil {
		t.Fatal("a model with no provider was accepted as the default")
	}
	if _, err := m.SetDefault("claude-opus-5", "sideways"); err == nil {
		t.Fatal("a nonsense effort was accepted")
	}
	if _, ok := m.Default.Get(); ok {
		t.Fatal("it was written down anyway")
	}
}
