// ctrl+v, over mote's terminal.
//
// mote takes the keyboard and offers no hook into it, which is right:
// a terminal that let every application redefine enter would not be a
// terminal any more. So the one key Vera adds is added from outside,
// by wrapping the model — every message goes straight through except
// this one, and mote never learns there was a wrapper.
//
// The key is ctrl+v and not ⌘V because ⌘V is the terminal emulator's,
// not ours: it turns the pasteboard's text into a bracketed paste and
// hands over that, having already thrown any picture away. ctrl+v
// arrives here as itself, and here is where the pasteboard can still
// be asked what is actually on it.
//
// ctrl+v already meant paste in the box — it is the text area's own
// binding, and it reads the pasteboard's words. That is kept: what is
// added is the answer for when the pasteboard holds a picture, which
// until now was a paste that appeared to do nothing.
package main

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/incantery/mote/tui"
)

// pasteKey is mote's terminal with ctrl+v meaning something.
//
// It holds the chat session rather than the pieces of it because both
// halves of the key live there: the pasteboard to read and the stage
// the picture waits on.
type pasteKey struct {
	m *tui.Model
	s *chatSession
}

// withPasteKey puts the key on the terminal. Everywhere the terminal
// was a tea.Model, this is one too.
func withPasteKey(m *tui.Model, s *chatSession) *pasteKey { return &pasteKey{m: m, s: s} }

func (p *pasteKey) Init() tea.Cmd  { return p.m.Init() }
func (p *pasteKey) View() tea.View { return p.m.View() }

// Update takes ctrl+v and passes everything else along untouched —
// including a real bracketed paste, which is the emulator's own ⌘V
// and still belongs to the input box.
//
// Where there is no pasteboard to read the key is not taken at all,
// and the text area keeps it: on a Mac that decision is Vera's, and
// everywhere else nothing has changed.
//
// mote's Update always answers with the same model, so what comes
// back is dropped: the wrapper stays the model bubbletea holds.
func (p *pasteKey) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "ctrl+v" && p.s.board.readable() {
		return p, p.paste()
	}
	_, cmd := p.m.Update(msg)
	return p, cmd
}

// paste is the whole of the key: the picture if the pasteboard has
// one, its words if it does not.
//
// The pasteboard is read off the UI goroutine — osascript is a
// process start and half a second of AppKit, and the screen should
// not stop for it. The words come back as a paste rather than as
// typing, so a pasted paragraph lands in the box in one piece, the
// way the emulator's own paste does.
func (p *pasteKey) paste() tea.Cmd {
	board, held := p.s.board, &p.s.held
	return off(func(context.Context) tea.Cmd {
		im, err := board.picture()
		switch {
		case err == nil:
			return tui.Note("%s", held.add(im))
		case !errors.Is(err, errNoPasteboardImage):
			// Something went wrong reading it. "There is no picture"
			// is the one answer that is not a fault.
			return tui.Fail("%s", err)
		}
		words, err := board.words()
		if err != nil {
			return tui.Fail("%s", err)
		}
		if strings.TrimSpace(words) == "" {
			return tui.Note("nothing on the pasteboard — take a shot with ⌘⇧⌃4, or copy something")
		}
		return func() tea.Msg { return tea.PasteMsg{Content: words} }
	})
}
