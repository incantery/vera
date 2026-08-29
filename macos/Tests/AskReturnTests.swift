// What Return means in the ask panel, checked without an app around it.
//
// The Mac app has no test target — it is one binary and one scheme —
// and adding one to reach three small decisions would cost more than
// it is worth. So the decisions were written to be reachable on their
// own, and this compiles the real `AskPanel.swift` beside a `main`
// that asserts them:
//
//     make test-mac
//
// It is not in the Xcode project, so the app build never sees it.

import AppKit

@MainActor
@main
struct AskReturnTests {
    static var failures = 0

    static func main() {
        // A running application: NSTextView wants one to lay text out.
        _ = NSApplication.shared

        // Enter sends. Nothing about a plain Return is a newline.
        expect(!AskReturn.isNewline(modifiers: [], afterBackslash: false), "a plain Return sends")

        // The modified ones break the line.
        for (name, mods) in [("shift", NSEvent.ModifierFlags.shift),
                             ("option", .option),
                             ("control", .control)] {
            expect(AskReturn.isNewline(modifiers: mods, afterBackslash: false), "\(name)+Return is a newline")
        }

        // A backslash a keystroke ago does it with no modifier at all.
        expect(AskReturn.isNewline(modifiers: [], afterBackslash: true), "backslash then Return is a newline")

        // Command is the one modifier that means nothing here, so a
        // shortcut passing through does not silently break the line.
        expect(!AskReturn.isNewline(modifiers: .command, afterBackslash: false), "command+Return still sends")

        // Caps lock and fn are not an instruction about anything, so
        // a backslash still gets to be the one that asked — and is
        // still the one swallowed.
        expect(!AskReturn.hasModifier(.capsLock), "caps lock is not a newline modifier")
        expect(!AskReturn.hasModifier(.function), "fn is not a newline modifier")
        expect(!AskReturn.hasModifier(.command), "command is not a newline modifier")
        expect(AskReturn.isNewline(modifiers: .capsLock, afterBackslash: true), "caps lock does not undo the backslash")

        // ctrl+J, and nothing else wearing control.
        expect(AskReturn.isNewlineChord(unmodified: "j", modifiers: .control), "ctrl+J is a newline")
        expect(!AskReturn.isNewlineChord(unmodified: "j", modifiers: []), "a bare j is a j")
        expect(!AskReturn.isNewlineChord(unmodified: "k", modifiers: .control), "ctrl+K is not a newline")

        // ⌘V, and only the bare one: the paste-and-match shortcuts
        // are somebody else's and must reach the field.
        expect(AskReturn.isPaste(unmodified: "v", modifiers: .command), "command+V is a paste")
        expect(AskReturn.isPaste(unmodified: "V", modifiers: .command), "the case of the key is not the question")
        expect(!AskReturn.isPaste(unmodified: "v", modifiers: []), "a bare v is a v")
        expect(!AskReturn.isPaste(unmodified: "c", modifiers: .command), "command+C is not a paste")
        for (name, mods) in [("shift", NSEvent.ModifierFlags([.command, .shift])),
                             ("option", [.command, .option]),
                             ("control", [.command, .control])] {
            expect(!AskReturn.isPaste(unmodified: "v", modifiers: mods), "command+\(name)+V is somebody else's shortcut")
        }

        // Pictures, on the way to Core. A TIFF is converted, because
        // several apps put only that on the pasteboard and everything
        // downstream expects one of four formats.
        expect(Attachment.png(png: nil, tiff: nil) == nil, "nothing on the pasteboard is nothing to send")
        expect(Attachment.png(png: Data(), tiff: nil) == nil, "empty PNG data is nothing to send")
        let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 4, pixelsHigh: 4,
                                      bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                      colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
        let asPNG = bitmap.representation(using: .png, properties: [:])!
        let asTIFF = bitmap.representation(using: .tiff, properties: [:])!
        expect(Attachment.png(png: asPNG, tiff: nil) == asPNG, "a PNG is passed straight through")
        if let converted = Attachment.png(png: nil, tiff: asTIFF) {
            expect(converted.starts(with: [0x89, 0x50, 0x4E, 0x47]), "a TIFF is converted to PNG")
        } else {
            expect(false, "a TIFF was not converted")
        }
        expect(Attachment.png(png: nil, tiff: Data([1, 2, 3])) == nil, "something that is not an image is nothing to send")

        // What goes on the wire: base64, named, with the type said.
        let picture = Attachment.image(asPNG, named: "pasted")
        expect(picture?.name == "pasted" && picture?.mime == "image/png", "the picture says what it is")
        expect(Data(base64Encoded: picture?.data ?? "") == asPNG, "the bytes survive the trip")
        expect(Attachment.image(Data(), named: "empty") == nil, "an empty picture is not a picture")
        expect(Attachment.image(Data(count: Attachment.maxBytes + 1), named: "huge") == nil,
               "a picture over the ceiling is refused on this side")

        // What the panel says is attached.
        expect(Attachment.summary([]) == "", "nothing attached says nothing")
        expect(Attachment.summary([picture!]) == "1 image attached", "one: \(Attachment.summary([picture!]))")
        expect(Attachment.summary([picture!, picture!]) == "2 images attached", "two")

        expect(AskReturn.isReturn(keyCode: 36), "Return")
        expect(AskReturn.isReturn(keyCode: 76), "the keypad's Return")
        expect(!AskReturn.isReturn(keyCode: 48), "Tab is not Return")

        // The field itself: what the cursor sits after, and what a
        // break leaves behind.
        let editor = NSTextView(frame: NSRect(x: 0, y: 0, width: 400, height: 100))

        editor.string = "first\\"
        editor.setSelectedRange(NSRange(location: 6, length: 0))
        expect(AskPanel.backslashBeforeCursor(editor), "the cursor is after a backslash")

        AskPanel.breakLine(in: editor, swallowingBackslash: true)
        expect(editor.string == "first\n", "the backslash goes: \(editor.string.debugDescription)")
        editor.insertText("second", replacementRange: editor.selectedRange())
        expect(editor.string == "first\nsecond", "typing carries on below: \(editor.string.debugDescription)")

        // A modified Return keeps every character of the question.
        editor.string = "a\\b"
        editor.setSelectedRange(NSRange(location: 3, length: 0))
        expect(!AskPanel.backslashBeforeCursor(editor), "a backslash elsewhere is just a backslash")
        AskPanel.breakLine(in: editor, swallowingBackslash: false)
        expect(editor.string == "a\\b\n", "nothing is eaten: \(editor.string.debugDescription)")

        // A break in the middle of the line breaks where the cursor is.
        editor.string = "ab"
        editor.setSelectedRange(NSRange(location: 1, length: 0))
        AskPanel.breakLine(in: editor, swallowingBackslash: false)
        expect(editor.string == "a\nb", "the break lands at the cursor: \(editor.string.debugDescription)")

        // An empty field has nothing before the cursor to read.
        editor.string = ""
        editor.setSelectedRange(NSRange(location: 0, length: 0))
        expect(!AskPanel.backslashBeforeCursor(editor), "an empty field is not a backslash")

        if failures > 0 {
            print("\(failures) failed")
            exit(1)
        }
        print("ask panel: ok")
    }

    static func expect(_ ok: Bool, _ what: String) {
        if !ok {
            print("FAIL: " + what)
            failures += 1
        }
    }
}
