// Pictures, into a terminal.
//
// A pasteboard picture does not arrive on the wire. ⌘V over a TUI
// puts TEXT there and nothing else — there is no escape sequence an
// image comes in on, in rook or anywhere else — so a chat that waited
// to be handed one would silently drop screenshots.
//
// So the chat goes and gets it instead. ctrl+v is a keystroke the
// terminal really does see, and it means here what ⌘V means
// everywhere else: read the pasteboard now and take what is on it —
// the picture if there is one, the words if there is not. `/paste` is
// the same fetch, typed; `/image <path>` takes a file. The screenshot
// key is still ⌘⇧⌃4 — you take the shot in whatever you were looking
// at, then press ctrl+v here.
//
// The Mac's own panel and the phone get a real paste, because they are
// real views. This file is the terminal making do.
package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/incantery/vera/attach"
)

// pasteboardScript writes the pasteboard's picture to the file named
// in argv, converting a TIFF (which is what several apps put there)
// into the PNG everything downstream expects.
//
// JavaScript for Automation reaching AppKit, rather than AppleScript's
// `the clipboard as «class PNGf»`. That older recipe is in every
// answer on the internet and it does not work on a current macOS: it
// fails with errAEAccessDenied (-10003) even when `clipboard info`
// says the PNG is right there, and the failure is not catchable by the
// script's own `try`. This route asks NSPasteboard directly and works.
//
// osascript rather than a helper binary because `vera` is one Go
// binary and stays one: no bundle, no cgo, no build step.
const pasteboardScript = `ObjC.import('AppKit');
function run(argv) {
  const board = $.NSPasteboard.generalPasteboard;
  let png = board.dataForType($.NSPasteboardTypePNG);
  if (png.isNil()) {
    const tiff = board.dataForType($.NSPasteboardTypeTIFF);
    if (tiff.isNil()) return "none";
    const rep = $.NSBitmapImageRep.imageRepWithData(tiff);
    if (rep.isNil()) return "none";
    png = rep.representationUsingTypeProperties($.NSBitmapImageFileTypePNG, $());
  }
  if (png.isNil()) return "none";
  return png.writeToFileAtomically($(argv[0]), true) ? "ok" : "failed";
}
`

// errNoPasteboardImage is the pasteboard holding words, or nothing —
// the ordinary case, and not a fault. ctrl+v reads it as "then take
// the words instead", so it is compared against and not only printed.
var errNoPasteboardImage = errors.New("there is no picture on the pasteboard — take a shot with ⌘⇧⌃4, or use /image <path>")

// pasteboardImage is what is on the pasteboard right now, as a message
// ready to send. It is the Mac's; macPasteboard is what decides that
// this machine is one.
//
// The temporary file it goes through is this function's alone and is
// gone before it returns. What Vera keeps is her own copy under her
// own state, made when the message reaches her.
func pasteboardImage() (attach.Image, error) {
	dir, err := os.MkdirTemp("", "vera-paste-")
	if err != nil {
		return attach.Image{}, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "pasteboard.png")

	// The script comes in on stdin rather than as -e lines: it is a
	// real script with a handler, and it should not have to survive a
	// trip through an argument list.
	cmd := exec.Command("osascript", "-l", "JavaScript", "-", path)
	cmd.Stdin = strings.NewReader(pasteboardScript)
	said, err := cmd.CombinedOutput()
	if err != nil {
		return attach.Image{}, errors.New("could not read the pasteboard: " + trim(oneLine(string(said)), 200))
	}
	if strings.TrimSpace(string(said)) != "ok" {
		return attach.Image{}, errNoPasteboardImage
	}
	im, err := attach.Read(path)
	if err != nil {
		return attach.Image{}, err
	}
	// The file was ours, so its name says nothing about the picture.
	// What a person will recognise is that they pasted it.
	im.Name = "pasted"
	return im, nil
}

// pasteboardText is what is on the pasteboard as words.
//
// pbpaste rather than the script above: text is the one thing the
// pasteboard has always handed over without a fight, and every Mac
// has the tool.
func pasteboardText() (string, error) {
	said, err := exec.Command("pbpaste").Output()
	if err != nil {
		return "", errors.New("could not read the pasteboard: " + trim(oneLine(err.Error()), 200))
	}
	return string(said), nil
}

// pasteboard is the machine's pasteboard, as the two things the chat
// asks it for. It is a value rather than a pair of calls so that a
// test can hand the chat a pasteboard with a known picture on it —
// there is no way to put one on the real one from a test, and a
// keystroke nothing can exercise is a keystroke that quietly rots.
//
// The zero value is a machine with no pasteboard vera can read. Both
// methods say so rather than crashing, and ctrl+v — which has to
// decide without a round trip — reads it as "not my key".
type pasteboard struct {
	image func() (attach.Image, error)
	text  func() (string, error)
}

// errNoPasteboard is the machine having no pasteboard to read. It is
// what /paste prints away from a Mac.
var errNoPasteboard = errors.New("the pasteboard is the Mac's; elsewhere use /image <path>")

// macPasteboard is the pasteboard as it is on a Mac, and the zero
// pasteboard anywhere else. This is the one place the platform is
// decided; everything downstream just reads what it is given.
func macPasteboard() pasteboard {
	if runtime.GOOS != "darwin" {
		return pasteboard{}
	}
	return pasteboard{image: pasteboardImage, text: pasteboardText}
}

// readable is whether there is anything here to read at all.
func (b pasteboard) readable() bool { return b.image != nil }

// picture is what is on the pasteboard now, as a message ready to
// send. errNoPasteboardImage means the pasteboard holds words, or
// nothing — the ordinary case, and not a fault.
func (b pasteboard) picture() (attach.Image, error) {
	if b.image == nil {
		return attach.Image{}, errNoPasteboard
	}
	return b.image()
}

// words is what is on the pasteboard now, as text.
func (b pasteboard) words() (string, error) {
	if b.text == nil {
		return "", errNoPasteboard
	}
	return b.text()
}
