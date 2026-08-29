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
