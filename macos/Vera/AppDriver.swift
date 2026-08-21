import AppKit
import CoreGraphics

// The hands that carry out a command on the desk.
//
// The phone taps a button; the core hands down a Command; this brings
// the named app to the front and presses the shortcut. It is the generic
// executor behind every Tier-0 app profile — Xcode's ⌘B, Chrome's ⌘R —
// so a new app becomes drivable by listing its shortcuts on the core,
// with no new code here. Vera.app is the right place for this: it already
// holds the accessibility trust that posting keystrokes to another app
// requires.

enum AppDriver {
    static func perform(_ c: Command) {
        switch c.type {
        case "keystroke": keystroke(c)
        default: break
        }
    }

    private static func keystroke(_ c: Command) {
        guard let key = c.key, let code = keyCode(for: key) else { return }
        let flags = modifiers(c.mods ?? [])

        // Bring the target forward first — a shortcut lands in whatever is
        // frontmost, so the app has to be it. Activation is asynchronous,
        // so the key is posted a beat later, once it has come up.
        activate(bundleID: c.bundleID)
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(140))
            post(code: code, flags: flags)
        }
    }

    private static func activate(bundleID: String?) {
        guard let bundleID, !bundleID.isEmpty else { return }
        if let app = NSRunningApplication.runningApplications(withBundleIdentifier: bundleID).first {
            app.activate()
        } else if let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID) {
            NSWorkspace.shared.openApplication(at: url, configuration: NSWorkspace.OpenConfiguration())
        }
    }

    private static func post(code: CGKeyCode, flags: CGEventFlags) {
        let src = CGEventSource(stateID: .combinedSessionState)
        if let down = CGEvent(keyboardEventSource: src, virtualKey: code, keyDown: true) {
            down.flags = flags
            down.post(tap: .cghidEventTap)
        }
        if let up = CGEvent(keyboardEventSource: src, virtualKey: code, keyDown: false) {
            up.flags = flags
            up.post(tap: .cghidEventTap)
        }
    }

    private static func modifiers(_ mods: [String]) -> CGEventFlags {
        var flags: CGEventFlags = []
        for m in mods {
            switch m {
            case "command": flags.insert(.maskCommand)
            case "shift": flags.insert(.maskShift)
            case "option": flags.insert(.maskAlternate)
            case "control": flags.insert(.maskControl)
            default: break
            }
        }
        return flags
    }

    // The US ANSI virtual keycodes. Enough for the characters shortcuts
    // are built from; unknown keys are refused rather than guessed.
    private static func keyCode(for key: String) -> CGKeyCode? {
        map[key.lowercased()]
    }

    private static let map: [String: CGKeyCode] = [
        "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7,
        "c": 8, "v": 9, "b": 11, "q": 12, "w": 13, "e": 14, "r": 15,
        "y": 16, "t": 17, "1": 18, "2": 19, "3": 20, "4": 21, "6": 22,
        "5": 23, "=": 24, "9": 25, "7": 26, "-": 27, "8": 28, "0": 29,
        "]": 30, "o": 31, "u": 32, "[": 33, "i": 34, "p": 35, "l": 37,
        "j": 38, "'": 39, "k": 40, ";": 41, "\\": 42, ",": 43, "/": 44,
        "n": 45, "m": 46, ".": 47, "`": 50,
        "return": 36, "tab": 48, "space": 49, "delete": 51, "escape": 53,
    ]
}
