package dump

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
	"github.com/incantery/vera/journal"
)

const secret = "glc_VERYSECRETTOKENVALUE1234567890"

func fixture(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	claude := filepath.Join(root, "claude")
	config := filepath.Join(root, "config")
	worktree := filepath.Join(root, "repo--room")
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(config, 0o755))
	must(os.WriteFile(filepath.Join(config, "grafana.env"), []byte("OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic "+secret+"\n"), 0o600))
	must(os.WriteFile(filepath.Join(config, "openai_key"), []byte("sk-openai-1234567890abcdef\n"), 0o600))
	must(os.MkdirAll(state, 0o755))
	must(os.WriteFile(filepath.Join(state, "identity.json"), []byte(`{"peer":"p","secret":"pairing-secret-value","name":"mac"}`), 0o600))
	must(os.WriteFile(filepath.Join(state, "verad.json"), []byte(`{"pid":1}`), 0o644))

	now := time.Date(2026, 8, 25, 18, 0, 0, 0, time.Local)
	var log strings.Builder
	for i, m := range []string{"before", "during", "after"} {
		at := now.Add(time.Duration(i-1) * time.Hour)
		line, _ := json.Marshal(map[string]any{"time": at, "level": "INFO", "msg": m, "token": secret})
		log.Write(line)
		log.WriteString("\n")
	}
	must(os.WriteFile(filepath.Join(state, "verad.log"), []byte(log.String()), 0o644))

	w := &journal.Writer{Dir: filepath.Join(state, "conversations")}
	must(w.Write(journal.Entry{At: now.Add(-3 * time.Hour), Conversation: "chat-old", Said: "old", Answered: "yes", System: "sys"}))
	must(w.Write(journal.Entry{At: now, Conversation: "chat-new", Said: "start a task", Answered: "started", System: "the prompt " + secret,
		TookMs: 3000, InputTokens: 10, OutputTokens: 5,
		Rounds: []journal.Round{
			{Tool: "fleet", Args: json.RawMessage(`{"action":"start"}`), Result: "Started abc12345", Task: "abc12345"},
			{Tool: "delegate", Args: json.RawMessage(`{"task":"look"}`), Result: "looked", Session: "sess-1", CostUSD: 0.02},
		}}))

	store := fleet.NewStore(filepath.Join(state, "fleet"))
	task := &fleet.Task{ID: "abc12345", Project: filepath.Join(root, "repo"), Name: "vera-abc12345", Kind: fleet.Ship,
		Brief: "do the thing", Worktree: worktree, Spawned: now.Add(time.Second)}
	must(store.Save(task))
	must(store.Append(task.ID, fleet.Status{At: now.Add(2 * time.Second), Verb: fleet.Working, Text: "on it", By: "agent"}))
	must(os.WriteFile(filepath.Join(store.TaskDir(task.ID), "env"), []byte("OTEL_EXPORTER_OTLP_HEADERS='Authorization=Basic "+secret+"'\nVERA_TASK='abc12345'\n"), 0o600))
	must(os.WriteFile(filepath.Join(store.TaskDir(task.ID), "report.md"), []byte("# report\nall good\n"), 0o644))

	session := func(dir, id, model string) {
		must(os.MkdirAll(dir, 0o755))
		var b strings.Builder
		line := func(v any) {
			j, _ := json.Marshal(v)
			b.Write(j)
			b.WriteString("\n")
		}
		line(map[string]any{"type": "user", "cwd": worktree, "timestamp": now, "message": map[string]any{"role": "user", "content": "hi" + map[string]string{"fbe5": " post to /fleet/abc12345/status", "sess-1": " task abc12345"}[id]}})
		usage := map[string]any{"input_tokens": 1000, "cache_creation_input_tokens": 2000, "cache_read_input_tokens": 3000, "output_tokens": 400}
		// Two lines, one message: usage counted once.
		line(map[string]any{"type": "assistant", "timestamp": now.Add(time.Second), "message": map[string]any{"id": "m1", "model": model, "usage": usage, "content": []any{map[string]any{"type": "text", "text": "ok " + secret}}}})
		line(map[string]any{"type": "assistant", "timestamp": now.Add(2 * time.Second), "message": map[string]any{"id": "m1", "model": model, "usage": usage, "content": []any{map[string]any{"type": "tool_use", "name": "Bash"}}}})
		must(os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(b.String()), 0o644))
	}
	session(projectDir(claude, worktree), "fbe5", "claude-opus-5")
	// The person's own session in the same checkout: not the task's.
	session(projectDir(claude, worktree), "mine", "claude-opus-5")
	session(filepath.Join(claude, "projects", "-somewhere-else"), "sess-1", "claude-mystery-9")

	// Her home: what she believed while all this was happening.
	place, err := home.Open(filepath.Join(root, "vera"))
	must(err)
	must(place.Memory().Apply(home.Revision{Add: []home.Note{
		{Name: "lives-in-vienna", Type: home.TypeUser, Fact: "Lives in Vienna."},
		{Name: "pairing", Type: home.TypeReference, Fact: "Pairs with the secret pairing-secret-value."},
	}}, "chat-new"))
	must(place.Project("repo", filepath.Join(root, "repo"), "main", nil))
	must(os.WriteFile(filepath.Join(place.Root, home.NotesDir, "private.md"), []byte("hers\n"), 0o600))

	return Options{StateDir: state, ClaudeDir: claude, ConfigDir: config, HomeDir: place.Root, Out: filepath.Join(root, "out"), Now: now.Add(time.Minute), Version: "test"}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

func TestBuildLatestConversation(t *testing.T) {
	o := fixture(t)
	res, err := Build(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conversations) != 1 || res.Conversations[0] != "chat-new" {
		t.Fatalf("conversations: %v", res.Conversations)
	}
	if len(res.Tasks) != 1 || res.Tasks[0] != "abc12345" {
		t.Fatalf("tasks: %v", res.Tasks)
	}
	if res.Sessions != 2 {
		t.Errorf("sessions: %d", res.Sessions)
	}
	// opus: 1000*5 + 2000*6.25 + 3000*0.5 + 400*25 = 5000+12500+1500+10000 = 29000 / 1e6
	if res.CostUSD < 0.028 || res.CostUSD > 0.030 || res.Priced {
		t.Errorf("cost %.4f priced %v", res.CostUSD, res.Priced)
	}

	for _, rel := range []string{"README.md", "costs.md", "versions.txt", "config.keys",
		"conversations/chat-new.md", "conversations/chat-new.jsonl", "conversations/chat-new.system.md",
		"fleet/abc12345/task.json", "fleet/abc12345/status.log", "fleet/abc12345/report.md", "fleet/abc12345/brief.md",
		"fleet/abc12345/env.keys", "fleet/abc12345/sessions/fbe5.jsonl", "fleet/abc12345/sessions.md",
		"delegate/sess-1.jsonl", "delegate/sess-1.md", "verad/verad.log", "verad/verad.json",
		"home/MEMORY.md", "home/memory/lives-in-vienna.md", "home/memory/pairing.md", "home/projects/repo.md"} {
		text := read(t, res.Dir, rel)
		if strings.Contains(text, secret) || strings.Contains(text, "sk-openai") || strings.Contains(text, "pairing-secret-value") {
			t.Errorf("%s leaks a secret", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "conversations", "chat-old.md")); err == nil {
		t.Error("the old conversation was not asked for")
	}
	if keys := read(t, res.Dir, "fleet/abc12345/env.keys"); keys != "OTEL_EXPORTER_OTLP_HEADERS\nVERA_TASK\n" {
		t.Errorf("env.keys: %q", keys)
	}
	if md := read(t, res.Dir, "conversations/chat-new.md"); !strings.Contains(md, "task `abc12345`") || !strings.Contains(md, "**vera:** started") {
		t.Errorf("transcript:\n%s", md)
	}
	if sys := read(t, res.Dir, "conversations/chat-new.system.md"); sys != "the prompt "+redacted {
		t.Errorf("system: %q", sys)
	}
	if log := read(t, res.Dir, "verad/verad.log"); !strings.Contains(log, `"during"`) || strings.Contains(log, `"before"`) || strings.Contains(log, `"after"`) {
		t.Errorf("log window:\n%s", log)
	}
	if sum := read(t, res.Dir, "fleet/abc12345/sessions.md"); !strings.Contains(sum, "turns 1") || !strings.Contains(sum, "tool uses 1") || !strings.Contains(sum, "≈ $0.03") {
		t.Errorf("sessions.md:\n%s", sum)
	}
	if costs := read(t, res.Dir, "costs.md"); !strings.Contains(costs, "some models unpriced") {
		t.Errorf("costs.md:\n%s", costs)
	}
}

func TestBuildSelections(t *testing.T) {
	o := fixture(t)
	o.Out = filepath.Join(filepath.Dir(o.Out), "all")
	o.All = true
	o.Tar = true
	res, err := Build(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conversations) != 2 {
		t.Errorf("all: %v", res.Conversations)
	}
	if _, err := os.Stat(res.Tarball); err != nil {
		t.Errorf("tarball: %v", err)
	}

	o.Out = filepath.Join(filepath.Dir(o.Out), "byprefix")
	o.All, o.Tar = false, false
	o.Conversations = []string{"chat-o"}
	res, err = Build(o)
	if err != nil || len(res.Conversations) != 1 || res.Conversations[0] != "chat-old" {
		t.Errorf("prefix: %v %v", res.Conversations, err)
	}

	o.Out = filepath.Join(filepath.Dir(o.Out), "bytask")
	o.Conversations = nil
	o.Tasks = []string{"abc1"}
	res, err = Build(o)
	if err != nil || len(res.Conversations) != 0 || len(res.Tasks) != 1 {
		t.Errorf("task: %v %v %v", res.Conversations, res.Tasks, err)
	}

	o.Out = filepath.Join(filepath.Dir(o.Out), "missing")
	o.Tasks = []string{"zzz"}
	if _, err = Build(o); err == nil {
		t.Error("a missing task should be an error")
	}

	o.Out = filepath.Join(filepath.Dir(o.Out), "since")
	o.Tasks = nil
	o.Since = o.Now.Add(-2 * time.Hour)
	res, err = Build(o)
	if err != nil || len(res.Conversations) != 1 || res.Conversations[0] != "chat-new" || len(res.Tasks) != 1 {
		t.Errorf("since: %v %v %v", res.Conversations, res.Tasks, err)
	}
}

func TestRedactorShapes(t *testing.T) {
	r := &redactor{shapes: secretShapes}
	for in, want := range map[string]string{
		"Authorization=Basic abcdefghijklmnopqrstuvwxyz==": "Authorization=Basic " + redacted,
		"Bearer abcdefghijklmnopqrstuvwxyz0123":            "Bearer " + redacted,
		"key sk-abcdefghijklmnopqrstuvwxyz here":           "key " + redacted + " here",
		`{"secret":"hush hush"}`:                           `{"secret":"` + redacted + `"}`,
		"nothing to see":                                   "nothing to see",
	} {
		if got := r.apply(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// A dump exists to answer "why did Vera say that", and half of that
// answer is usually a fact she was carrying in the prompt.
func TestTheDumpCarriesWhatSheBelieved(t *testing.T) {
	o := fixture(t)
	res, err := Build(o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Memories != 2 {
		t.Fatalf("copied %d memory files, want 2", res.Memories)
	}
	if index := read(t, res.Dir, "home/MEMORY.md"); !strings.Contains(index, "lives-in-vienna") {
		t.Errorf("the index did not come along:\n%s", index)
	}
	if fact := read(t, res.Dir, "home/memory/pairing.md"); strings.Contains(fact, "pairing-secret-value") {
		t.Errorf("a memory leaked a secret; the home is redacted like everything else:\n%s", fact)
	}
	if proj := read(t, res.Dir, "home/projects/repo.md"); !strings.Contains(proj, "default branch") {
		t.Errorf("the project file did not come along:\n%s", proj)
	}
	// notes/ are hers. A dump is something a person hands to somebody
	// else, and that is a decision, not a default.
	if _, err := os.Stat(filepath.Join(res.Dir, "home", "notes")); err == nil {
		t.Error("her notes went into the dump")
	}
	if readme := read(t, res.Dir, "README.md"); !strings.Contains(readme, "What she knew") {
		t.Errorf("the README does not point at it:\n%s", readme)
	}
}
