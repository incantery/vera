package main

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/incantery/mote/tui"
	"github.com/incantery/vera/attach"
)

// ctrlV is the key as it arrives from a real terminal.
func ctrlV() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl} }

// pasted is a pasteboard with a picture on it, or words, or nothing —
// the one thing a test cannot arrange on the real one.
func pasted(t *testing.T, board pasteboard) (*chatSession, *pasteKey) {
	t.Helper()
	outsideRook(t)
	f := newFakeVerad(t)
	c := f.client()
	w := newFleetWatch(c)
	s := &chatSession{c: c, w: w, conv: "chat-1", dir: t.TempDir(), open: &openSessions{}, board: board}
	w.conv = s.conversation
	w.pollModel(context.Background())

	p := withPasteKey(tui.New(veraAgent{c: c, held: &s.held}, headless(chatOptions(&Status{Name: "vera"}, s, nil, ""))), s)
	p.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	return s, p
}

// withPicture is a pasteboard holding the picture in the file named.
func withPicture(t *testing.T, path string) pasteboard {
	t.Helper()
	return pasteboard{
		image: func() (attach.Image, error) {
			im, err := attach.Read(path)
			if err != nil {
				return attach.Image{}, err
			}
			im.Name = "pasted"
			return im, nil
		},
		text: func() (string, error) { return "", nil },
	}
}

// withWords is a pasteboard holding words and no picture — what a
// pasteboard has on it nearly all of the time.
func withWords(said string) pasteboard {
	return pasteboard{
		image: func() (attach.Image, error) { return attach.Image{}, errNoPasteboardImage },
		text:  func() (string, error) { return said, nil },
	}
}

// ctrl+v is /paste on a keystroke: it reads the pasteboard now and
// the picture waits for the next thing you say.
func TestCtrlVAttachesThePictureOnThePasteboard(t *testing.T) {
	s, p := pasted(t, withPicture(t, writeShot(t, "screenshot.png")))

	_, cmd := p.Update(ctrlV())
	deliver(p, cmd)

	if s.held.count() != 1 {
		t.Fatalf("holding %d after ctrl+v", s.held.count())
	}
	// And the person can see what they attached, by name.
	if v := screen(p); !strings.Contains(v, "pasted") || !strings.Contains(v, "next message") {
		t.Errorf("ctrl+v said nothing about the picture:\n%s", v)
	}
}

// A pasteboard with words on it is the ordinary case, and ctrl+v
// still means paste: the words go in the box, as one paste and not as
// typing, and nothing is attached.
func TestCtrlVWithWordsPastesThemIntoTheBox(t *testing.T) {
	s, p := pasted(t, withWords("the fleet rail is showing a1 twice"))

	_, cmd := p.Update(ctrlV())
	deliver(p, cmd)

	if s.held.count() != 0 {
		t.Fatalf("words on the pasteboard attached %d images", s.held.count())
	}
	if v := screen(p); !strings.Contains(v, "the fleet rail is showing a1 twice") {
		t.Errorf("the words did not land in the box:\n%s", v)
	}
}

// An empty pasteboard is not an error, and not silence either: a key
// that appears to do nothing is a key a person presses again.
func TestCtrlVWithAnEmptyPasteboardSaysSo(t *testing.T) {
	_, p := pasted(t, withWords("   \n "))

	_, cmd := p.Update(ctrlV())
	deliver(p, cmd)

	if v := screen(p); !strings.Contains(v, "nothing on the pasteboard") {
		t.Errorf("an empty pasteboard should say so:\n%s", v)
	}
}

// A pasteboard that cannot be read at all is a fault, and visible.
// "There is no picture" is the one answer that is not.
func TestCtrlVShowsWhatWentWrong(t *testing.T) {
	board := pasteboard{
		image: func() (attach.Image, error) {
			return attach.Image{}, errors.New("could not read the pasteboard: osascript is not there")
		},
		text: func() (string, error) { return "unreachable", nil },
	}
	_, p := pasted(t, board)

	_, cmd := p.Update(ctrlV())
	deliver(p, cmd)

	v := screen(p)
	if !strings.Contains(v, "could not read the pasteboard") {
		t.Errorf("a broken pasteboard should be visible:\n%s", v)
	}
	if strings.Contains(v, "unreachable") {
		t.Errorf("a failure fell through to the words:\n%s", v)
	}
}

// Everything that is not ctrl+v is mote's, untouched — including a
// real bracketed paste, which is the terminal emulator's own ⌘V and
// belongs to the input box exactly as it did before.
func TestThePasteKeyLeavesEveryOtherKeyAlone(t *testing.T) {
	s, p := pasted(t, withPicture(t, writeShot(t, "unused.png")))

	for _, r := range "hello" {
		p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	p.Update(tea.PasteMsg{Content: " and some pasted words"})

	if v := screen(p); !strings.Contains(v, "hello and some pasted words") {
		t.Errorf("typing and pasting should reach the box:\n%s", v)
	}
	if s.held.count() != 0 {
		t.Fatal("ordinary keys read the pasteboard")
	}

	// The wrapper stays the model bubbletea holds: mote answers with
	// itself, and a model that handed that back would lose the key.
	next, _ := p.Update(tea.KeyPressMsg{Code: '!', Text: "!"})
	if _, ok := next.(*pasteKey); !ok {
		t.Fatalf("the wrapper was dropped: %T", next)
	}
}

// Where there is no pasteboard vera can read, ctrl+v is not her key:
// it goes to the input like any other, and nothing is attached or
// said. This is what a Linux terminal gets today.
func TestCtrlVIsNotTakenWhereThereIsNoPasteboard(t *testing.T) {
	s, p := pasted(t, pasteboard{})

	_, cmd := p.Update(ctrlV())
	deliver(p, cmd)

	if s.held.count() != 0 {
		t.Fatal("something was attached from a pasteboard that is not there")
	}
	// Nothing said: no note, no error, nothing about a pasteboard.
	//
	// Not "the frame is unchanged". Where vera does not take ctrl+v
	// the text area keeps it, and the text area's own ctrl+v pastes
	// the machine's clipboard — so the box, and the height of the
	// transcript above it, move with whatever the tester last copied.
	// What this test is about is that VERA says nothing.
	said := transcriptOf(screen(p))
	for _, quiet := range []string{"✗", "attached", "pasteboard", "/image"} {
		if strings.Contains(said, quiet) {
			t.Errorf("ctrl+v said %q where there is nothing to read:\n%s", quiet, said)
		}
	}
}

// transcriptOf is the screen above the rule: everything the terminal
// has said, without the input box under it — which holds whatever the
// text area pasted, and is not this package's business.
func transcriptOf(v string) string {
	lines := strings.Split(v, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && strings.Trim(l, "─") == "" {
			return strings.Join(lines[:i], "\n")
		}
	}
	return v
}

// /paste and ctrl+v are the same fetch: both go through the session's
// pasteboard, so what one attaches the other does.
func TestPasteCommandReadsTheSamePasteboard(t *testing.T) {
	s, p := pasted(t, withPicture(t, writeShot(t, "shared.png")))

	deliver(p, s.handle("paste", ""))
	if s.held.count() != 1 {
		t.Fatalf("/paste attached %d", s.held.count())
	}

	// And away from a Mac it says where the pasteboard is instead of
	// panicking on one that is not there.
	s.board = pasteboard{}
	s.held.clear()
	deliver(p, s.handle("paste", ""))
	if s.held.count() != 0 {
		t.Fatal("/paste attached something from a pasteboard that is not there")
	}
	if v := screen(p); !strings.Contains(v, "/image <path>") {
		t.Errorf("/paste with no pasteboard should say what to use instead:\n%s", v)
	}
}

// The platform is decided in exactly one place.
func TestOnlyAMacHasAPasteboardVeraCanRead(t *testing.T) {
	if got, want := macPasteboard().readable(), runtime.GOOS == "darwin"; got != want {
		t.Errorf("macPasteboard().readable() = %v on %s", got, runtime.GOOS)
	}
	// The zero pasteboard answers rather than crashing.
	var none pasteboard
	if _, err := none.picture(); !errors.Is(err, errNoPasteboard) {
		t.Errorf("picture() off a machine with no pasteboard: %v", err)
	}
	if _, err := none.words(); !errors.Is(err, errNoPasteboard) {
		t.Errorf("words() off a machine with no pasteboard: %v", err)
	}
}
