package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/incantery/mote/tool"
	"github.com/incantery/vera/attach"
	"github.com/incantery/vera/fleet"
	"github.com/incantery/vera/mux"
)

func shot(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{1, 2, 3, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// The paths ride the exchange's context to whichever tool ends up
// handing the work to somebody with eyes.
func TestImagesRideTheContextOntoTheHandle(t *testing.T) {
	ctx := context.Background()
	if imagesOn(ctx) != nil {
		t.Fatal("an exchange with no pictures carries some")
	}
	// Nothing in, nothing added: a context untouched is the text-only
	// path staying exactly what it was.
	if withImages(ctx, nil) != ctx {
		t.Fatal("an empty list still wrapped the context")
	}
	paths := []string{"/s/a.png", "/s/b.png"}
	got := imagesOn(withImages(ctx, paths))
	if len(got) != 2 || got[1] != "/s/b.png" {
		t.Fatalf("carried %v", got)
	}

	// And a tool asks for them off the Handle, however the harness
	// packed them.
	h := tool.Handle{Values: map[string]any{keyImages: strings.Join(paths, "\n")}}
	if a := attached(h); len(a) != 2 || a[0] != "/s/a.png" {
		t.Fatalf("attached %v", a)
	}
	if attached(tool.Handle{}) != nil {
		t.Fatal("the zero Handle produced pictures")
	}
	if attached(tool.Handle{Values: map[string]any{keyImages: ""}}) != nil {
		t.Fatal("an empty value produced pictures")
	}
}

// A Vera with nowhere to keep a picture says so. Answering as though
// nothing was attached would leave a person looking at a reply about
// the wrong thing with no way to tell why.
func TestKeepRefusesOutLoudWithNoStore(t *testing.T) {
	m := &Mind{}
	if _, err := m.keep(Message{Text: "look"}); err != nil {
		t.Fatalf("a message with no pictures: %v", err)
	}
	_, err := m.keep(Message{Text: "look", Images: []attach.Image{{Data: shot(t)}}})
	if err != attach.ErrNoStore {
		t.Fatalf("no store: %v", err)
	}

	m.Attachments = &attach.Store{Dir: t.TempDir()}
	saved, err := m.keep(Message{Conversation: "c", Images: []attach.Image{{Data: shot(t)}}})
	if err != nil || len(saved) != 1 {
		t.Fatalf("%v %v", saved, err)
	}
	if _, err := os.Stat(saved[0].Path); err != nil {
		t.Fatalf("nothing on disk: %v", err)
	}
}

// The whole point, end to end on the delegate's side: a screenshot
// that arrived on /say has to reach Claude Code's prompt as a path it
// can open. The `claude` here is a script that records its arguments.
func TestTheDelegateHandsTheImagesToClaudeCode(t *testing.T) {
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + record + "\n" +
		`echo '{"result":"looked at it","session_id":"s1","total_cost_usd":0.01}'` + "\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	d := &DelegateTool{Delegate: &Delegate{Workspace: t.TempDir(), Timeout: 30 * time.Second}}
	res, err := d.Run(context.Background(), json.RawMessage(`{"task":"say what is wrong here"}`),
		tool.Handle{Values: map[string]any{keyImages: "/s/a.png\n/s/b.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "looked at it" {
		t.Fatalf("result %q", res.Text)
	}
	argv, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(argv)
	for _, want := range []string{"say what is wrong here", "/s/a.png", "/s/b.png", "Read them"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("Claude Code was never told %q:\n%s", want, prompt)
		}
	}

	// And a task with no pictures is handed over untouched — the
	// text-only delegation is exactly what it was.
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Run(context.Background(), json.RawMessage(`{"task":"just look it up"}`), tool.Handle{}); err != nil {
		t.Fatal(err)
	}
	argv, _ = os.ReadFile(record)
	if strings.Contains(string(argv), "attached") {
		t.Errorf("a plain delegation talks about pictures:\n%s", argv)
	}
}

// The fleet tool takes the pictures off the same Handle and puts them
// where the agent will read them. `answer` is the verb that needs
// nothing but a room, so it is the one that can be driven here: a
// picture sent to a task that asked a question has to arrive in the
// pane beside the words.
func TestTheFleetToolTypesTheImagesIntoTheRoom(t *testing.T) {
	m := &typingMux{}
	store := fleet.NewStore(t.TempDir())
	task := &fleet.Task{ID: "a1", Project: "/src/rook", Kind: fleet.Ship, Brief: "b", Pane: mux.ID{Session: "s", Window: "1", Pane: "0"}}
	if err := store.Save(task); err != nil {
		t.Fatal(err)
	}
	f := &FleetTool{Fleet: fleet.New(m, store)}

	_, err := f.Run(context.Background(),
		json.RawMessage(`{"action":"answer","task":"a1","text":"this is what I see"}`),
		tool.Handle{Values: map[string]any{keyImages: "/s/a.png"}})
	if err != nil {
		t.Fatal(err)
	}
	typed := strings.Join(m.typed, "")
	if !strings.Contains(typed, "this is what I see") || !strings.Contains(typed, "/s/a.png") {
		t.Fatalf("what reached the room: %q", typed)
	}

	// An answer with no picture is exactly the words, as it always was.
	m.typed = nil
	if _, err := f.Run(context.Background(),
		json.RawMessage(`{"action":"answer","task":"a1","text":"just words"}`), tool.Handle{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(m.typed, ""); got != "just words" {
		t.Fatalf("a plain answer was changed: %q", got)
	}
}

// typingMux is a multiplexer that does nothing but remember what was
// typed into it — the one thing this test is about.
type typingMux struct{ typed []string }

func (t *typingMux) Name() string                                        { return "typing" }
func (t *typingMux) Focus(context.Context) (*mux.Pane, error)            { return nil, mux.ErrNoFocus }
func (t *typingMux) Get(context.Context, mux.ID) (*mux.Pane, error)      { return nil, mux.ErrNoPane }
func (t *typingMux) List(context.Context) ([]mux.Pane, error)            { return nil, nil }
func (t *typingMux) Spawn(context.Context, mux.Spawn) (*mux.Pane, error) { return nil, mux.ErrNoPane }
func (t *typingMux) Kill(context.Context, mux.ID) error                  { return nil }
func (t *typingMux) Send(_ context.Context, _ mux.ID, text string) error {
	t.typed = append(t.typed, text)
	return nil
}
func (t *typingMux) Enter(context.Context, mux.ID) error               { return nil }
func (t *typingMux) Capture(context.Context, mux.ID) ([]string, error) { return nil, nil }
func (t *typingMux) GoTo(context.Context, mux.ID) error                { return nil }
func (t *typingMux) Narrow(context.Context, mux.ID, int) error         { return nil }
func (t *typingMux) Widen(context.Context, mux.ID) error               { return nil }
func (t *typingMux) Watch(ctx context.Context, _ func(mux.Event)) error {
	<-ctx.Done()
	return ctx.Err()
}
func (t *typingMux) Poke() {}

// What the model actually reads. It cannot see the picture, so the
// turn has to say a picture is there, say she cannot see it, and name
// the file — otherwise "what is wrong here?" is a question about
// nothing and she answers it as though it were prose.
func TestTheTurnSaysAPictureCameWithIt(t *testing.T) {
	mind, _, _ := askingMind(t, says("I'll hand that on."))
	mind.Attachments = &attach.Store{Dir: t.TempDir()}
	model := mind.Provider.(*scriptedModel)

	if err := mind.think(context.Background(), Message{
		Text: "what is wrong here", Conversation: "c1",
		Images: []attach.Image{{Name: "shot.png", Data: shot(t)}},
	}, func(Frame) error { return nil }); err != nil {
		t.Fatal(err)
	}
	sent := model.asked(0).Messages
	turn := sent[len(sent)-1].Text
	for _, want := range []string{"what is wrong here", "cannot see", ".png", "delegate"} {
		if !strings.Contains(turn, want) {
			t.Errorf("the turn never says %q:\n%s", want, turn)
		}
	}
	// Their words come first and are not rewritten.
	if !strings.HasPrefix(turn, "what is wrong here") {
		t.Errorf("their own words were displaced:\n%s", turn)
	}
}

// Pointing is a whole message. A screenshot with nothing typed beside
// it used to end the exchange before it began, because there were no
// words.
func TestAPictureWithNoWordsIsStillAMessage(t *testing.T) {
	mind, _, _ := askingMind(t, says("Looking."))
	mind.Attachments = &attach.Store{Dir: t.TempDir()}

	var answer strings.Builder
	if err := mind.think(context.Background(),
		Message{Conversation: "c1", Images: []attach.Image{{Data: shot(t)}}},
		func(f Frame) error { answer.WriteString(f.Delta); return nil }); err != nil {
		t.Fatal(err)
	}
	if answer.String() != "Looking." {
		t.Fatalf("a picture on its own was not answered: %q", answer.String())
	}

	// Nothing at all is still nothing at all.
	mind2, _, _ := askingMind(t, says("unreachable"))
	if err := mind2.think(context.Background(), Message{Conversation: "c2"},
		func(Frame) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if mind2.Provider.(*scriptedModel).rounds() != 0 {
		t.Fatal("an empty message reached the model")
	}
}

// A picture that could not be kept is told to the model, so she says
// so herself. Answering as though nothing was attached is the one
// behaviour worth ruling out: somebody who pasted a screenshot and
// asked what is wrong would get a reply about nothing.
//
// It rides the turn rather than an error frame because an error frame
// is terminal for two of the four clients — see attach.Trouble.
func TestAPictureThatCouldNotBeKeptIsToldToHer(t *testing.T) {
	mind, _, _ := askingMind(t, says("I can only go on the words."))
	mind.Attachments = &attach.Store{Dir: t.TempDir()}

	var answer strings.Builder
	err := mind.think(context.Background(), Message{
		Text: "look at this", Conversation: "c1",
		Images: []attach.Image{{Name: "notes.txt", Data: base64.StdEncoding.EncodeToString([]byte("not a picture at all"))}},
	}, func(f Frame) error {
		if f.Error != "" {
			t.Errorf("an error frame would truncate the answer on the phone: %q", f.Error)
		}
		answer.WriteString(f.Delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer.String() != "I can only go on the words." {
		t.Fatalf("the words went unanswered: %q", answer.String())
	}
	sent := mind.Provider.(*scriptedModel).asked(0).Messages
	turn := sent[len(sent)-1].Text
	for _, want := range []string{"look at this", "could not be kept", "notes.txt", "Say so"} {
		if !strings.Contains(turn, want) {
			t.Errorf("the turn never says %q:\n%s", want, turn)
		}
	}
	// And she is not told a picture is waiting on disk, because none
	// is.
	if strings.Contains(turn, "files on this machine") {
		t.Errorf("the model was pointed at a picture that was never kept:\n%s", turn)
	}
}

// The HTTP seam: a picture posted to /say reaches the handler with its
// bytes intact, and a body big enough to hold a screenshot of a whole
// display is not turned away at the door — /say carried a megabyte
// when a message was words.
func TestAPictureSurvivesTheWire(t *testing.T) {
	got := make(chan Message, 1)
	base, id := serveLANWith(t, func(_ context.Context, msg Message, reply func(Frame) error) error {
		got <- msg
		return reply(Frame{Done: true})
	}, nil)

	// Megabytes of real picture: past the megabyte /say carried when a
	// message was words, and about what a screenshot of a large
	// display actually weighs.
	big := bigShot(t)
	if len(big) < 2<<20 {
		t.Fatalf("the test's own picture is only %d bytes", len(big))
	}
	body, err := json.Marshal(Message{
		Text: "what is wrong here", Conversation: "c1",
		Images: []attach.Image{{Name: "shot.png", Data: base64.StdEncoding.EncodeToString(big)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream(t, base, id, string(body), func(Frame) {})

	select {
	case msg := <-got:
		if len(msg.Images) != 1 || msg.Images[0].Name != "shot.png" {
			t.Fatalf("what arrived: %+v", msg.Images)
		}
		back, err := base64.StdEncoding.DecodeString(msg.Images[0].Data)
		if err != nil || !bytes.Equal(back, big) {
			t.Fatalf("the bytes did not survive the wire: %v", err)
		}
	default:
		t.Fatal("nothing reached the handler")
	}
}

// bigShot is a PNG the size of a screenshot: noise, because a
// compressible one would encode to nothing and prove nothing about
// what the wire will carry.
func bigShot(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1200, 900))
	seed := uint32(1)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = byte(seed >> 24)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
