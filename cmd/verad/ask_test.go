package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
	"github.com/incantery/vera/journal"
)

// scripted is the model, answering each round of the tool loop with
// the next thing in the script. The real one is stateless per request
// and so is this; what it is not is the same answer twice, which would
// loop.
func scripted(t *testing.T, rounds ...string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	var round int
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		i := round
		round++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if i < len(rounds) {
			for _, line := range strings.Split(rounds[i], "\n") {
				if line != "" {
					_, _ = w.Write([]byte("data: " + line + "\n\n"))
				}
			}
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
}

// callsTool is one SSE chunk asking for a tool, and the usage chunk
// after it.
func callsTool(t *testing.T, id, name string, args any) string {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	chunk := map[string]any{
		"model": "m",
		"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0, "id": id, "type": "function",
				"function": map[string]any{"name": name, "arguments": string(raw)},
			}},
		}}},
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n" + `{"model":"m","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":2}}`
}

func says(text string) string {
	b, _ := json.Marshal(map[string]any{"model": "m",
		"choices": []any{map[string]any{"delta": map[string]any{"content": text}}}})
	return string(b) + "\n" + `{"model":"m","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1}}`
}

// askingMind is a mind with hands over a fresh home, a scripted model,
// and a journal on disk.
func askingMind(t *testing.T, rounds ...string) (*Mind, string, string) {
	t.Helper()
	return askingMindAt(t, filepath.Join(t.TempDir(), "vera"), nil, rounds...)
}

// askingMindAt is askingMind for a test that had to know where her
// home was before it could write the script.
func askingMindAt(t *testing.T, root string, projects *fleet.Projects, rounds ...string) (*Mind, string, string) {
	t.Helper()
	if _, err := home.Open(root); err != nil {
		t.Fatal(err)
	}
	hands, err := openHands(root, projects)
	if err != nil {
		t.Fatal(err)
	}
	hands.Refresh(context.Background())
	srv := scripted(t, rounds...)
	t.Cleanup(srv.Close)
	journalDir := t.TempDir()
	return &Mind{
		Client:      srv.Client(),
		Base:        srv.URL,
		Model:       "m",
		History:     newHistory(),
		Hands:       hands,
		Journal:     &journal.Writer{Dir: journalDir},
		instruments: newInstruments(),
	}, root, journalDir
}

// The round trip: a tool the policy will not run unattended, a
// question on the wire, a person's word coming back on another
// request, and the call running.
func TestAskGoesOutOverTheWireAndComesBackAnswered(t *testing.T) {
	target := filepath.Join(t.TempDir(), "somewhere-else.txt")
	mind, _, journalDir := askingMind(t,
		callsTool(t, "call_w", "write", map[string]any{"path": target, "content": "written"}),
		says("Done."))

	base, id := serveLANWith(t, mind.think, func(l *lanTransport) { l.answer = mind.Hands.Answer })

	answered := make(chan string, 1)
	frames := stream(t, base, id, `{"text":"put a file there","conversation":"c1"}`, func(f Frame) {
		if f.Ask == nil {
			return
		}
		// The fake client: a person's yes, on its own request, while
		// the exchange is still streaming.
		post(t, base, id, "/ask/"+f.Ask.ID, `{"choice":"yes"}`, http.StatusOK)
		answered <- f.Ask.Name
	})

	select {
	case name := <-answered:
		if name != "write" {
			t.Fatalf("asked about %q", name)
		}
	default:
		t.Fatalf("no ask frame reached the client:\n%s", show(frames))
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "written" {
		t.Fatalf("the answered call did not run: %v %q", err, b)
	}

	round := onlyRound(t, journalDir, "c1")
	if round.Decision != "ask" || round.Answer != "yes" {
		t.Fatalf("the journal says %+v — a decision nobody can read afterwards is not a record", round)
	}
	if round.Reason == "" {
		t.Fatal("the journal kept no reason, so the transcript cannot say why it asked")
	}
}

// Silence is not consent. The call does not run, the model is told,
// and the journal says it was asked and never answered.
func TestAnAskNobodyAnswersIsNo(t *testing.T) {
	target := filepath.Join(t.TempDir(), "unanswered.txt")
	mind, _, journalDir := askingMind(t,
		callsTool(t, "call_w", "write", map[string]any{"path": target, "content": "written"}),
		says("They did not say."))
	mind.Hands.Wait = 40 * time.Millisecond

	var told string
	err := mind.think(context.Background(), Message{Text: "put a file there", Conversation: "c2"},
		func(f Frame) error {
			if f.ToolResult != nil {
				told = f.ToolResult.Result
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("a question nobody answered ran anyway")
	}
	if !strings.Contains(told, "did not answer") {
		t.Fatalf("the model was told %q — it needs to know it was not refused on the merits", told)
	}
	round := onlyRound(t, journalDir, "c2")
	if round.Decision != "ask" || round.Answer != "no" {
		t.Fatalf("the journal says %+v, want asked → no", round)
	}
}

// A project is not hers to edit, and the model is told the profile's
// own sentence — "start a task for that" — rather than a status code.
// That sentence is the whole design: a refusal that says what to do
// instead is a refusal the model can act on.
func TestDenyTellsTheModelWhatToDoInstead(t *testing.T) {
	project := filepath.Join(t.TempDir(), "someproject")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	projects := &fleet.Projects{}
	projects.Remember(fleet.Repo{Root: project, Name: "someproject"})

	mind, _, journalDir := askingMindAt(t, filepath.Join(t.TempDir(), "vera"), projects,
		callsTool(t, "call_e", "edit", map[string]any{
			"path": filepath.Join(project, "main.go"), "old": "a", "new": "b"}),
		says("That is a task's job."))

	var told string
	if err := mind.think(context.Background(), Message{Text: "fix the bug in someproject", Conversation: "c3"},
		func(f Frame) error {
			if f.ToolResult != nil {
				told = f.ToolResult.Result
			}
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(told, "start a task for that") {
		t.Fatalf("the model was told %q — it needs the sentence, not just the refusal", told)
	}
	round := onlyRound(t, journalDir, "c3")
	if round.Decision != "deny" || !strings.Contains(round.Reason, "start a task") {
		t.Fatalf("the journal says %+v, want denied with the reason", round)
	}
}

// Her own profile is denied whatever the file says, because a rule she
// can rewrite is not a rule.
func TestSheCannotRewriteHerOwnRules(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vera")
	policy := filepath.Join(root, home.ProfileDir, "policy.toml")
	mind, _, _ := askingMindAt(t, root, nil,
		callsTool(t, "call_e", "edit", map[string]any{
			"path": policy, "old": `default = "ask"`, "new": `default = "allow"`}),
		says("I cannot."))

	before, err := os.ReadFile(policy)
	if err != nil {
		t.Fatal(err)
	}
	var told string
	if err := mind.think(context.Background(), Message{Text: "loosen your policy", Conversation: "c5"},
		func(f Frame) error {
			if f.ToolResult != nil {
				told = f.ToolResult.Result
			}
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(told, "Not allowed") {
		t.Fatalf("the model was told %q", told)
	}
	after, err := os.ReadFile(policy)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("she rewrote the policy that governs her")
	}
}

// An allowed tool runs without a word, and the journal says so — a
// round with no decision at all would read as an ungated tool.
func TestAllowedToolIsRecordedAsAllowed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	mind, _, journalDir := askingMind(t,
		callsTool(t, "call_r", "read", map[string]any{"path": filepath.Join(dir, "a.txt")}),
		says("It says hello."))

	var result string
	if err := mind.think(context.Background(), Message{Text: "what is in it", Conversation: "c4"},
		func(f Frame) error {
			if f.ToolResult != nil {
				result = f.ToolResult.Result
			}
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "hello") {
		t.Fatalf("read came back with %q", result)
	}
	if round := onlyRound(t, journalDir, "c4"); round.Decision != "allow" {
		t.Fatalf("the journal says %+v, want allowed", round)
	}
}

// --- the small change of clothes ---------------------------------------

func stream(t *testing.T, base string, id Identity, body string, each func(Frame)) []Frame {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/say", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+id.Secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out []Frame
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var f Frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		out = append(out, f)
		each(f)
		if f.Done || f.Error != "" {
			break
		}
	}
	return out
}

func post(t *testing.T, base string, id Identity, path, body string, want int) {
	t.Helper()
	req, _ := http.NewRequest("POST", base+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+id.Secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("POST %s got %d, want %d", path, res.StatusCode, want)
	}
}

func onlyRound(t *testing.T, dir, conversation string) journal.Round {
	t.Helper()
	entries, err := journal.Read(journal.Path(dir, conversation))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || len(entries[0].Rounds) != 1 {
		t.Fatalf("wanted one exchange with one round, got %d exchanges", len(entries))
	}
	return entries[0].Rounds[0]
}

func show(frames []Frame) string {
	var b strings.Builder
	for _, f := range frames {
		j, _ := json.Marshal(f)
		b.Write(j)
		b.WriteByte('\n')
	}
	return b.String()
}
