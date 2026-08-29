package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/vera/attach"
)

func writeShot(t *testing.T, name string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 3))
	img.Set(1, 1, color.RGBA{200, 30, 30, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The picture and the words are one message, typed at two different
// moments. The stage is the gap between them.
func TestTheStageHoldsUntilSomethingIsSaid(t *testing.T) {
	var s stage
	if s.count() != 0 || s.clear() != 0 || s.take() != nil {
		t.Fatal("a fresh stage is not empty")
	}
	line := s.add(attach.Image{Name: "shot.png"})
	if !strings.Contains(line, "shot.png") || !strings.Contains(line, "next message") {
		t.Errorf("what the person was told: %q", line)
	}
	line = s.add(attach.Image{Name: "other.png"})
	if !strings.Contains(line, "shot.png") || !strings.Contains(line, "other.png") {
		t.Errorf("the second one hid the first: %q", line)
	}
	if s.count() != 2 {
		t.Fatalf("holding %d", s.count())
	}

	// Taken once. A picture goes with exactly one thing you said.
	took := s.take()
	if len(took) != 2 || s.count() != 0 {
		t.Fatalf("took %d, %d still held", len(took), s.count())
	}
	if s.take() != nil {
		t.Fatal("the same picture went twice")
	}

	// A picture with no name is still something a person can see is
	// attached.
	if line := s.add(attach.Image{}); !strings.Contains(line, "image") {
		t.Errorf("an unnamed picture reads as %q", line)
	}
	if n := s.clear(); n != 1 {
		t.Fatalf("cleared %d", n)
	}
}

// /image reads the file here, on this side of the wire, and says what
// is now waiting. A path that is not a picture is refused before an
// exchange is paid for.
func TestImageCommandAttachesAFile(t *testing.T) {
	f := newFakeVerad(t)
	s := &chatSession{c: f.client(), w: newFleetWatch(f.client()), conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}

	shot := writeShot(t, "header.png")
	if cmd := s.handle("image", shot); cmd == nil {
		t.Fatal("no answer")
	} else {
		cmd()
	}
	if s.held.count() != 1 {
		t.Fatalf("holding %d after /image", s.held.count())
	}

	// /image with nothing forgets what is attached.
	s.handle("image", "")
	if s.held.count() != 0 {
		t.Fatal("/image with no argument did not forget")
	}

	// Something that is not a picture, and something that is not
	// there, are both refused here rather than at the far end after
	// an exchange has been paid for.
	notAPicture := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(notAPicture, []byte("this is just some prose"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{notAPicture, filepath.Join(t.TempDir(), "gone.png")} {
		if cmd := s.handle("image", path); cmd != nil {
			cmd()
		}
		if s.held.count() != 0 {
			t.Fatalf("attached %s", path)
		}
	}
}

// A conversation you left does not hand its pictures to the next one.
func TestANewConversationStartsWithNothingAttached(t *testing.T) {
	f := newFakeVerad(t)
	s := &chatSession{c: f.client(), w: newFleetWatch(f.client()), conv: "chat-1", dir: t.TempDir(), open: &openSessions{}}
	s.held.add(attach.Image{Name: "old.png"})
	if cmd := s.handle("new", ""); cmd != nil {
		cmd()
	}
	if s.held.count() != 0 {
		t.Fatal("the picture followed you into the new conversation")
	}
}

// The whole of the terminal's side, end to end: attach, say something,
// and the picture rides that one message and no other.
func TestTheAgentSendsWhatIsAttachedExactlyOnce(t *testing.T) {
	f := newFakeVerad(t)
	var held stage
	a := veraAgent{c: f.client(), held: &held}

	im, err := attach.Read(writeShot(t, "overlap.png"))
	if err != nil {
		t.Fatal(err)
	}
	held.add(im)

	drain := func(text string) {
		t.Helper()
		events, err := a.Send(context.Background(), "chat-1", text)
		if err != nil {
			t.Fatal(err)
		}
		for range events {
		}
	}
	drain("what is wrong with this")
	drain("and now")

	said := f.messages()
	if len(said) != 2 {
		t.Fatalf("%d messages", len(said))
	}
	if len(said[0].Images) != 1 || said[0].Images[0].Name != "overlap.png" {
		t.Fatalf("the first message carried %+v", said[0].Images)
	}
	if said[0].Images[0].Data == "" {
		t.Fatal("the picture arrived with no bytes in it")
	}
	if len(said[1].Images) != 0 {
		t.Fatalf("the picture rode a second message too: %+v", said[1].Images)
	}

	// A chat with no stage at all still says things — the zero
	// veraAgent is what the older tests build.
	plain := veraAgent{c: f.client()}
	events, err := plain.Send(context.Background(), "chat-1", "just words")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := expandHome("~/Desktop/shot.png"); got != filepath.Join(home, "Desktop", "shot.png") {
		t.Errorf("~ did not expand: %q", got)
	}
	for _, path := range []string{"/tmp/shot.png", "shot.png", "~notauser/x.png", ""} {
		if got := expandHome(path); got != path {
			t.Errorf("expandHome(%q) = %q", path, got)
		}
	}
}
