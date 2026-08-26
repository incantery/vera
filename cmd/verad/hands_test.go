package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/incantery/mote/provider"
	"github.com/incantery/mote/tool"
	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/home"
)

// newHands builds hands over a fresh home with one project root, the
// way verad does at startup.
func newHands(t *testing.T) (*Hands, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vera")
	place, err := home.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = place

	project := filepath.Join(t.TempDir(), "someproject")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	projects := &fleet.Projects{}
	projects.Remember(fleet.Repo{Root: project, Name: "someproject"})

	h, err := openHands(root, projects)
	if err != nil {
		t.Fatal(err)
	}
	h.Refresh(context.Background())
	return h, root, project
}

// call is one tool call as the model would make it, decided.
func decide(t *testing.T, h *Hands, conversation, name string, args any) tool.Verdict {
	t.Helper()
	tl, ok := h.Tool(name)
	if !ok {
		t.Fatalf("no tool named %q — the profile lists %v", name, h.Names())
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := h.Decide(conversation, tool.NewCall("call_1", tl, raw))
	return v
}

// The supervisor's three sentences, against the three places a path
// can be. This is the whole boundary, so it is asserted from the file
// rather than from a policy written in the test.
func TestSupervisorDecidesByWhereThePathIs(t *testing.T) {
	h, root, project := newHands(t)

	cases := []struct {
		what string
		tool string
		args any
		want tool.Decision
		says string
	}{
		{"writing in her own home", "write",
			map[string]any{"path": filepath.Join(root, "notes", "today.md"), "content": "hi"},
			tool.Allow, "her own home"},
		{"editing in a project", "edit",
			map[string]any{"path": filepath.Join(project, "main.go"), "old": "a", "new": "b"},
			tool.Deny, "start a task for that"},
		{"writing anywhere else", "write",
			map[string]any{"path": "/tmp/scratch-of-no-fixed-abode.txt", "content": "hi"},
			tool.Ask, ""},
		{"reading a project", "read",
			map[string]any{"path": filepath.Join(project, "main.go")},
			tool.Allow, ""},
		{"a .git anywhere", "write",
			map[string]any{"path": filepath.Join(root, ".git", "config"), "content": "x"},
			tool.Deny, ""},
		{"her own profile", "edit",
			map[string]any{"path": filepath.Join(root, home.ProfileDir, "policy.toml"), "old": "ask", "new": "allow"},
			tool.Deny, "not yours"},
		{"a command that only reads", "run",
			map[string]any{"command": "git status --short"},
			tool.Allow, ""},
		{"a command that does not", "run",
			map[string]any{"command": "rm -rf /"},
			tool.Ask, ""},
	}
	for _, c := range cases {
		v := decide(t, h, "conv", c.tool, c.args)
		if v.Decision != c.want {
			t.Errorf("%s: %s → %s (%s: %s), want %s", c.what, c.tool, v.Decision, v.Rule, v.Reason, c.want)
		}
		if c.says != "" && !strings.Contains(v.Reason, c.says) {
			t.Errorf("%s: reason %q does not say %q — that sentence is what the model is told", c.what, v.Reason, c.says)
		}
	}
}

// A repository the fleet learned about after startup is a project too,
// and the ones the file listed do not stop being ones.
func TestRootsFollowTheFleet(t *testing.T) {
	h, _, _ := newHands(t)

	late := filepath.Join(t.TempDir(), "arrived-later")
	if err := os.MkdirAll(late, 0o755); err != nil {
		t.Fatal(err)
	}
	if v := decide(t, h, "conv", "write", map[string]any{"path": filepath.Join(late, "x.go"), "content": "x"}); v.Decision == tool.Deny {
		t.Fatal("denied before the fleet had ever heard of it")
	}

	h.Projects.Remember(fleet.Repo{Root: late, Name: "arrived-later"})
	h.Refresh(context.Background())

	if v := decide(t, h, "conv", "write", map[string]any{"path": filepath.Join(late, "x.go"), "content": "x"}); v.Decision != tool.Deny {
		t.Fatalf("a project the fleet knows about is still writable: %s (%s)", v.Decision, v.Rule)
	}
	// And the file's own roots survived the refresh.
	if len(h.policy.Roots) <= len(h.fileRoots) {
		t.Fatalf("roots %v dropped the file's %v", h.policy.Roots, h.fileRoots)
	}
}

// The profile is written into her home the first time and never again:
// after that it is the person's file.
func TestProfileIsSeededOnceAndIsMotesOwn(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vera")
	if _, err := openHands(root, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, home.ProfileDir, "policy.toml")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "start a task for that") {
		t.Fatalf("that is not mote's worked example:\n%s", first)
	}

	mine := string(first) + "\n# mine now\n"
	if err := os.WriteFile(path, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openHands(root, nil); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != mine {
		t.Fatal("a second start wrote over the person's policy")
	}
}

// "Always" is an answer given in a conversation. It holds for the rest
// of that one and reaches no other.
func TestAlwaysHoldsForTheConversationOnly(t *testing.T) {
	h, _, _ := newHands(t)
	dir := t.TempDir()
	args := map[string]any{"path": filepath.Join(dir, "one.txt"), "content": "x"}

	tl, _ := h.Tool("write")
	raw, _ := json.Marshal(args)
	c := tool.NewCall("call_always", tl, raw)

	v, g := h.Decide("here", c)
	if v.Decision != tool.Ask {
		t.Fatalf("first write outside her home should ask, got %s", v.Decision)
	}
	h.Asking(c.ID, g)
	done := make(chan bool, 1)
	go func() {
		ok, _ := h.waitFor(context.Background(), g, c)
		done <- ok
	}()
	waitUntil(t, func() bool { return h.Answer(context.Background(), c.ID, tool.Always) == nil })
	if !<-done {
		t.Fatal("always did not run the call")
	}
	h.Answered(c.ID)

	// A second call in the same directory, same conversation: no
	// question this time.
	next, _ := json.Marshal(map[string]any{"path": filepath.Join(dir, "two.txt"), "content": "y"})
	if v, _ := h.Decide("here", tool.NewCall("call_2", tl, next)); v.Decision != tool.Allow {
		t.Fatalf("the same directory still asks after always: %s", v.Decision)
	}
	// The same call in another conversation: asked again.
	if v, _ := h.Decide("elsewhere", tool.NewCall("call_3", tl, next)); v.Decision != tool.Ask {
		t.Fatalf("an always leaked into another conversation: %s (%s)", v.Decision, v.Rule)
	}
}

// An answer for a question nobody asked is the caller's mistake, and
// so is a word that is not one of the three.
func TestAnswerWithoutAQuestion(t *testing.T) {
	h, _, _ := newHands(t)
	if err := h.Answer(context.Background(), "call_nothing", tool.Yes); err == nil {
		t.Fatal("answering a question nobody asked should say so")
	}

	tl, _ := h.Tool("write")
	raw, _ := json.Marshal(map[string]any{"path": "/tmp/x", "content": "x"})
	c := tool.NewCall("call_1", tl, raw)
	_, g := h.Decide("conv", c)
	h.Asking(c.ID, g)
	if err := h.Answer(context.Background(), c.ID, "maybe"); err == nil {
		t.Fatal(`"maybe" is not an answer`)
	}
}

func waitUntil(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("never happened")
}

// A write's arguments are the file's whole content. The record and
// the wire both cap them: a journal line the size of the file it wrote
// is a journal nobody reads and a disk nobody meant to fill.
func TestLongArgumentsAreCapped(t *testing.T) {
	big := `{"path":"/tmp/x","content":"` + strings.Repeat("a", 200_000) + `"}`
	got := capArgs(big)
	if len(got) > maxRecordedArgs+64 {
		t.Fatalf("kept %d bytes of a %d-byte argument", len(got), len(big))
	}
	if !json.Valid(got) {
		t.Fatalf("what is left is not JSON, so the whole line will be dropped: %q", got)
	}
	// Short arguments are kept exactly, and stay readable as JSON
	// rather than becoming a string that happens to look like some.
	small := `{"path":"/tmp/x"}`
	if string(capArgs(small)) != small {
		t.Fatalf("a short argument was rewritten: %s", capArgs(small))
	}
}

// --- the one path ------------------------------------------------------

// sayingTool does the two things only Vera's own tools do: it says
// something while it works, and it reaches something — a task, a
// session, a cost — that the journal keeps beside the result. mote's
// Run carries neither, so both come through the round.
type sayingTool struct{ err error }

func (sayingTool) Name() string        { return "saying" }
func (sayingTool) Description() string { return "says things" }
func (sayingTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t sayingTool) Run(ctx context.Context, args json.RawMessage, out io.Writer) (tool.Result, error) {
	r := roundOf(ctx)
	r.say("Opening a room for that…")
	r.link("task-9", "session-9", 0.25)
	_, _ = out.Write([]byte("half way"))
	if t.err != nil {
		return tool.Result{}, t.err
	}
	return tool.Result{Text: "done, for " + r.device}, nil
}

// oneCall builds the exchange the way think does and runs a single
// call through invoke, which is now the whole of the dispatch.
func oneCall(t *testing.T, h *Hands, tl tool.Tool) (string, []Frame, *exchange) {
	t.Helper()
	if err := h.Adopt(tl); err != nil {
		t.Fatal(err)
	}
	h.policy.Tools[tl.Name()] = tool.Allow
	m := &Mind{Hands: h, Model: "m", instruments: newInstruments()}
	ctx, x := m.begin(context.Background(), Message{Conversation: "c", Device: "phone"}, "hi", 0, "system", nil)
	var frames []Frame
	call := provider.Call{ID: "call_1", Name: tl.Name(), Arguments: `{}`}
	said := m.invoke(ctx, "c", "phone", x, call, func(f Frame) error {
		frames = append(frames, f)
		return nil
	})
	return said, frames, x
}

func TestARoundCarriesWhatRunCannot(t *testing.T) {
	h, _, _ := newHands(t)
	said, frames, x := oneCall(t, h, sayingTool{})

	if said != "done, for phone" {
		t.Errorf("the tool never saw which device asked: %q", said)
	}
	var status, output string
	for _, f := range frames {
		if f.Status != "" {
			status = f.Status
		}
		if f.ToolOutput != nil {
			output = f.ToolOutput.Text
		}
	}
	if status != "Opening a room for that…" {
		t.Errorf("the status line did not reach the phone: %q", status)
	}
	if output != "half way" {
		t.Errorf("what the tool wrote while it ran did not reach the phone: %q", output)
	}
	// The clock that measures what the wait felt like starts at the
	// status, not at the first token.
	if x.signMillis() < 0 || x.seen == 0 {
		t.Error("a status line did not count as something appearing")
	}
	// And the journal's round knows what the call reached.
	if x.pending.Task != "task-9" || x.pending.Session != "session-9" || x.pending.CostUSD != 0.25 {
		t.Errorf("the round did not record what it reached: %+v", x.pending)
	}
	if x.pending.Decision != string(tool.Allow) {
		t.Errorf("the policy's word is not on the round: %+v", x.pending)
	}
}

// A tool that could not run at all says so in mote's words, and the
// terminal already marks that card failed.
func TestAToolThatCouldNotRunSaysSo(t *testing.T) {
	h, _, _ := newHands(t)
	said, _, _ := oneCall(t, h, sayingTool{err: errors.New("no room to open one in")})
	if said != "error: no room to open one in" {
		t.Errorf("a failed call said %q", said)
	}
}

// The policy decides every tool alike now, including the two that
// used to be dispatched by name before anything was asked.
func TestEveryToolGoesThroughThePolicy(t *testing.T) {
	h, _, _ := newHands(t)
	if err := h.Adopt(sayingTool{}); err != nil {
		t.Fatal(err)
	}
	h.policy.Tools["saying"] = tool.Deny
	m := &Mind{Hands: h, Model: "m", instruments: newInstruments()}
	ctx, x := m.begin(context.Background(), Message{Conversation: "c"}, "hi", 0, "system", nil)
	call := provider.Call{ID: "call_1", Name: "saying", Arguments: `{}`}
	said := m.invoke(ctx, "c", "", x, call, func(Frame) error { return nil })
	if !strings.HasPrefix(said, "Not allowed:") {
		t.Errorf("a denied call said %q", said)
	}
	if x.pending.Decision != string(tool.Deny) {
		t.Errorf("the refusal is not on the round: %+v", x.pending)
	}

	// And a name nothing answers to is still a name nothing answers to.
	if got := m.invoke(ctx, "c", "", x, provider.Call{ID: "x", Name: "nonesuch"}, func(Frame) error { return nil }); got != "That tool does not exist." {
		t.Errorf("an unknown tool said %q", got)
	}
}
