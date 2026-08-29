// Pictures, into a terminal.
//
// A terminal cannot be pasted into. ⌘V over a TUI puts TEXT on the
// wire and nothing else — there is no escape sequence a pasteboard
// image arrives on, in rook or anywhere else — so the gesture that
// works everywhere else is simply not available here, and a chat that
// pretended otherwise would silently drop screenshots.
//
// So the chat asks for the picture instead of waiting to be handed
// one: `/paste` takes what is on the pasteboard right now, `/image
// <path>` takes a file. The keystroke is still ⌘⇧⌃4 — you take the
// shot in whatever you were looking at, then type /paste here.
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
// the ordinary case, and not a fault.
var errNoPasteboardImage = errors.New("there is no picture on the pasteboard — take a shot with ⌘⇧⌃4, or use /image <path>")

// pasteboardImage is what is on the pasteboard right now, as a message
// ready to send.
//
// The temporary file it goes through is this function's alone and is
// gone before it returns. What Vera keeps is her own copy under her
// own state, made when the message reaches her.
func pasteboardImage() (attach.Image, error) {
	if runtime.GOOS != "darwin" {
		return attach.Image{}, errors.New("/paste reads the Mac's pasteboard; elsewhere use /image <path>")
	}
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
