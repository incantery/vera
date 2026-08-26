package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/incantery/mote/provider"
	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
	"github.com/incantery/vera/journal"
)

// scriptRound is one turn of a scripted model: the events it sends and
// what it says that cost.
type scriptRound struct {
	events []provider.Event
	usage  provider.Usage
}

// scriptedModel is the model, answering each round of the tool loop
// with the next thing in the script. The real one is stateless per
// request and so is this; what it is not is the same answer twice,
// which would loop.
//
// It is a provider rather than an HTTP server now: the wire belongs to
// mote, which tests it against its own sockets, and what verad has
// left to test is the loop over it.
type scriptedModel struct {
	mu     sync.Mutex
	script []scriptRound
	round  int
	// seen is every request it was asked, in order, for a test about
	// what the model was actually told.
	seen []provider.Request
}

func scripted(t *testing.T, rounds ...scriptRound) *scriptedModel {
	t.Helper()
	return &scriptedModel{script: rounds}
}

func (m *scriptedModel) Stream(ctx context.Context, req provider.Request, fn func(provider.Event)) (provider.Usage, error) {
	m.mu.Lock()
	i := m.round
	m.round++
	m.seen = append(m.seen, req)
	var r scriptRound
	if i < len(m.script) {
		r = m.script[i]
	}
	m.mu.Unlock()

	for _, ev := range r.events {
		if err := ctx.Err(); err != nil {
			return r.usage, err
		}
		fn(ev)
	}
	return r.usage, nil
}

// asked is the nth request the model was given, or the zero one.
func (m *scriptedModel) asked(n int) provider.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n >= len(m.seen) {
		return provider.Request{}
	}
	return m.seen[n]
}

func (m *scriptedModel) rounds() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.seen)
}

// callsTool is a round in which the model asks for one tool.
func callsTool(t *testing.T, id, name string, args any) scriptRound {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return scriptRound{
		events: []provider.Event{provider.Calling(id, name, string(raw))},
		usage:  provider.Usage{Model: "m", Input: 9, Output: 2},
	}
}

// says is a round in which the model answers.
func says(text string) scriptRound {
	return scriptRound{
		events: []provider.Event{provider.Delta(text)},
		usage:  provider.Usage{Model: "m", Input: 3, Output: 1},
	}
}

// askingMind is a mind with hands over a fresh home, a scripted model,
// and a journal on disk.
func askingMind(t *testing.T, rounds ...scriptRound) (*Mind, string, string) {
	t.Helper()
	return askingMindAt(t, filepath.Join(t.TempDir(), "vera"), nil, rounds...)
}

// askingMindAt is askingMind for a test that had to know where her
// home was before it could write the script.
func askingMindAt(t *testing.T, root string, projects *fleet.Projects, rounds ...scriptRound) (*Mind, string, string) {
	t.Helper()
	place, err := home.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	hands, err := openHands(root, projects)
	if err != nil {
		t.Fatal(err)
	}
	hands.Refresh(context.Background())
	journalDir := t.TempDir()
	return &Mind{
		Provider:    scripted(t, rounds...),
		Model:       "m",
		History:     newHistory(),
		Home:        place,
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
