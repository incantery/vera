import AppKit
import ApplicationServices

// Putting words where the cursor is.
//
// The reliable way on macOS is the one every dictation tool uses: put
// the text on the pasteboard, press ⌘V on the person's behalf, and put
// the pasteboard back the way it was. The Accessibility API can set a
// field's value directly, but it is wrong in enough web views and
// terminals that it is used here only to LOOK — is there an editable
// field at all? — which is the signal that later tells dictation and
// commands apart.

enum Paster {
    enum Problem: LocalizedError {
        case notTrusted
        case noEventSource
        var errorDescription: String? {
            switch self {
            case .notTrusted: "Vera needs Accessibility to type at the cursor."
            case .noEventSource: "Couldn't create a keyboard event."
            }
        }
    }

    /// Whether the focused element accepts text. nil means macOS would
    /// not say — common for terminals and some web views, which is why
    /// this is a signal and not a gate.
    @MainActor
    static func focusedFieldIsEditable() -> Bool? {
        guard KeyTap.trusted else { return nil }
        let system = AXUIElementCreateSystemWide()
        var focused: CFTypeRef?
        guard AXUIElementCopyAttributeValue(system, kAXFocusedUIElementAttribute as CFString, &focused) == .success,
              let element = focused, CFGetTypeID(element) == AXUIElementGetTypeID() else { return nil }
        let ax = unsafeBitCast(element, to: AXUIElement.self)
        var role: CFTypeRef?
        AXUIElementCopyAttributeValue(ax, kAXRoleAttribute as CFString, &role)
        if let role = role as? String, ["AXTextField", "AXTextArea", "AXComboBox", "AXSearchField"].contains(role) {
            return true
        }
        var settable = DarwinBoolean(false)
        if AXUIElementIsAttributeSettable(ax, kAXValueAttribute as CFString, &settable) == .success, settable.boolValue {
            return true
        }
        var range: CFTypeRef?
        if AXUIElementCopyAttributeValue(ax, kAXSelectedTextRangeAttribute as CFString, &range) == .success {
            return true
        }
        return nil
    }

    /// Types `text` at the cursor by way of the pasteboard, then
    /// restores whatever was on it.
    @MainActor
    static func insert(_ text: String) throws {
        guard KeyTap.trusted else { throw Problem.notTrusted }
        let pasteboard = NSPasteboard.general
        let previous = pasteboard.string(forType: .string)
        pasteboard.clearContents()
        pasteboard.setString(text, forType: .string)
        let ours = pasteboard.changeCount

        guard let source = CGEventSource(stateID: .combinedSessionState),
              let down = CGEvent(keyboardEventSource: source, virtualKey: 9, keyDown: true), // V
              let up = CGEvent(keyboardEventSource: source, virtualKey: 9, keyDown: false) else {
            throw Problem.noEventSource
        }
        down.flags = .maskCommand
        up.flags = .maskCommand
        down.post(tap: .cghidEventTap)
        up.post(tap: .cghidEventTap)

        // Give the receiving app a moment to read it before it is taken
        // back. Only restore if nobody else has written since.
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(400))
            guard pasteboard.changeCount == ours else { return }
            pasteboard.clearContents()
            if let previous { pasteboard.setString(previous, forType: .string) }
        }
    }
}
