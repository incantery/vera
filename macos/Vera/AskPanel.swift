import AppKit
import Observation
import SwiftUI

// The panel where you talk to Vera with a keyboard.
//
// Not everywhere is a place you can speak — a meeting, a café, a quiet
// office — so the same conversation has a typed door. Fn+T opens it as
// a floating field, Enter sends, a modified Enter breaks the line, Esc
// closes. Unlike the dictation pill it takes keyboard focus, and unlike
// a normal window it does not activate the app: the application you
// were in stays where it was, and the panel goes away when you are
// done.

@Observable
@MainActor
final class AskModel {
    var question = ""
    var answer = ""
    var status: String?
    var error: String?
    var busy = false
    var lastQuestion = ""
    /// Pictures pasted into the panel, waiting on the question they
    /// belong to. Vera cannot see them; she hands them to whichever
    /// agent she gives the work to. Taken by the send, so a picture
    /// goes with exactly one question.
    var attached: [SayImage] = []
}

/// How tall the field grows before it scrolls itself. The same six the
/// chat box uses, so a question that is too long to see looks the same
/// wherever it is being typed.
private let askLineLimit = 6

/// What a Return keystroke means in the field.
///
/// Enter sends: that is the whole point of a door you open, say one
/// thing through, and close. But a question worth typing is sometimes
/// two lines, and the newline has to be a keystroke you can name. These
/// are the chat box's own — shift, option or control with Return, and
/// ctrl+J — plus a backslash a keystroke ago, which is the one that
/// works in a terminal that swallows the modifiers. It is here too so
/// that the habit carries between the two places Vera is typed at.
enum AskReturn {
    /// The keystroke breaks the line rather than sending it.
    static func isNewline(modifiers: NSEvent.ModifierFlags, afterBackslash: Bool) -> Bool {
        hasModifier(modifiers) || afterBackslash
    }

    /// Whether a modifier asked for the newline, rather than a
    /// backslash having asked for it. Only these three: command is a
    /// shortcut on its way somewhere, and caps lock and fn are not an
    /// instruction about anything.
    static func hasModifier(_ modifiers: NSEvent.ModifierFlags) -> Bool {
        modifiers.contains(.shift) || modifiers.contains(.option) || modifiers.contains(.control)
    }

    /// ctrl+J is a newline wherever Return is. The key is read with its
    /// modifiers ignored: control turns a `j` into a line feed, so the
    /// character that arrives is already the thing being asked for.
    static func isNewlineChord(unmodified: String?, modifiers: NSEvent.ModifierFlags) -> Bool {
        modifiers.contains(.control) && unmodified?.lowercased() == "j"
    }

    /// Return, and the one on the keypad.
    static func isReturn(keyCode: UInt16) -> Bool { keyCode == 36 || keyCode == 76 }

    /// ⌘V, and nothing else: ⌘⇧V and ⌘⌥V are somebody's paste-and-match
    /// shortcuts and are not this.
    ///
    /// The keystroke is only intercepted when the pasteboard actually
    /// holds a picture — see AskPanel. Pasting text into the field has
    /// to keep working, and a field that swallowed ⌘V would be a field
    /// you cannot paste a URL into.
    static func isPaste(unmodified: String?, modifiers: NSEvent.ModifierFlags) -> Bool {
        modifiers.contains(.command)
            && !modifiers.contains(.shift) && !modifiers.contains(.option) && !modifiers.contains(.control)
            && unmodified?.lowercased() == "v"
    }
}

/// A borderless panel that can still become key — the Spotlight shape.
private final class KeyPanel: NSPanel {
    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { false }
}

@MainActor
final class AskPanel {
    let model = AskModel()
    var onAsk: (String) -> Void = { _ in }
    var onClose: () -> Void = {}

    private var panel: NSPanel?
    private var panelKeys: Any?
    private(set) var isVisible = false

    func toggle() {
        if isVisible { hide() } else { show() }
    }

    func show() {
        let panel = panel ?? makePanel()
        model.error = nil
        let mouse = NSEvent.mouseLocation
        let screen = NSScreen.screens.first { NSMouseInRect(mouse, $0.frame, false) } ?? NSScreen.main ?? NSScreen.screens[0]
        let visible = screen.visibleFrame
        watchForKeys(in: panel)
        let size = NSSize(width: 560, height: panel.frame.height)
        panel.setFrame(NSRect(x: visible.midX - size.width / 2, y: visible.maxY - visible.height * 0.28 - size.height, width: size.width, height: size.height), display: true)
        isVisible = true
        panel.alphaValue = 0
        panel.makeKeyAndOrderFront(nil)
        NSAnimationContext.runAnimationGroup { ctx in
            ctx.duration = 0.12
            panel.animator().alphaValue = 1
        }
    }

    func hide() {
        guard isVisible, let panel else { return }
        isVisible = false
        if let panelKeys { NSEvent.removeMonitor(panelKeys) }
        panelKeys = nil
        panel.orderOut(nil)
        onClose()
    }

    private func makePanel() -> NSPanel {
        let panel = KeyPanel(
            contentRect: NSRect(x: 0, y: 0, width: 560, height: 120),
            styleMask: [.borderless, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        panel.level = .floating
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = true
        panel.hidesOnDeactivate = false
        panel.isMovableByWindowBackground = true
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .ignoresCycle]
        panel.animationBehavior = .none
        let hosting = NSHostingView(rootView: AskView(model: model, onAsk: { [weak self] in self?.onAsk($0) }, onClose: { [weak self] in self?.hide() }))
        hosting.sizingOptions = [.intrinsicContentSize]
        panel.contentView = hosting
        self.panel = panel
        return panel
    }

    /// The keystrokes SwiftUI does not hand over: the newline ones,
    /// and a ⌘V that is a picture.
    ///
    /// SwiftUI hands a `TextField` one keystroke — the plain Return
    /// that `onSubmit` answers — and nothing for the rest, so the
    /// modified ones are read here and written into the field editor,
    /// which is the AppKit text view the field is being typed into.
    /// A pasted picture is the same problem from the other side: the
    /// field would take the pasteboard's text and drop its image, so
    /// the keystroke is caught before the field sees it — but ONLY
    /// when there is an image, so an ordinary paste is untouched.
    /// A monitor rather than a rewrite of the field: everything else
    /// about the door is SwiftUI's, and should stay that way. It lives
    /// exactly as long as the panel is up — the door is shut far more
    /// of the time than it is open, and a key monitor that outlives
    /// what it is for is a key monitor nobody remembers is there.
    private func watchForKeys(in panel: NSPanel) {
        guard panelKeys == nil else { return }
        panelKeys = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard let self, self.isVisible, event.window === panel,
                  let editor = panel.firstResponder as? NSTextView
            else { return event }

            let mods = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
            let backslash = Self.backslashBeforeCursor(editor)
            // ⌘V with a picture on the pasteboard attaches it. With
            // anything else on the pasteboard the keystroke is passed
            // straight through, so pasting a URL into the field is
            // exactly what it always was.
            if AskReturn.isPaste(unmodified: event.charactersIgnoringModifiers, modifiers: mods) {
                guard Attachment.hasImage(), let picture = Attachment.fromPasteboard() else { return event }
                self.model.attached.append(picture)
                return nil
            }
            if AskReturn.isReturn(keyCode: event.keyCode) {
                // A plain Return is left alone: it is the send, and
                // SwiftUI's own `onSubmit` is what answers it.
                guard AskReturn.isNewline(modifiers: mods, afterBackslash: backslash) else { return event }
            } else if !AskReturn.isNewlineChord(unmodified: event.charactersIgnoringModifiers, modifiers: mods) {
                return event
            }
            // The backslash is only an instruction when it is the
            // thing that asked; a modifier did the asking otherwise,
            // and every character of the question stays.
            Self.breakLine(in: editor, swallowingBackslash: backslash && !AskReturn.hasModifier(mods))
            return nil
        }
    }

    /// Whether the character the cursor sits just after is a backslash.
    /// Read in UTF-16, the way an `NSTextView` counts, and compared as
    /// a string so that half of a surrogate pair is simply not a match.
    static func backslashBeforeCursor(_ editor: NSTextView) -> Bool {
        let sel = editor.selectedRange()
        guard sel.length == 0, sel.location > 0 else { return false }
        let text = editor.string as NSString
        guard sel.location <= text.length else { return false }
        return text.substring(with: NSRange(location: sel.location - 1, length: 1)) == "\\"
    }

    /// Breaks the line at the cursor. The backslash was the
    /// instruction rather than part of the question, so it goes.
    static func breakLine(in editor: NSTextView, swallowingBackslash: Bool) {
        var range = editor.selectedRange()
        if swallowingBackslash, range.length == 0, range.location > 0 {
            range = NSRange(location: range.location - 1, length: 1)
        }
        editor.insertText("\n", replacementRange: range)
    }
}

struct AskView: View {
    let model: AskModel
    let onAsk: (String) -> Void
    let onClose: () -> Void
    @FocusState private var focused: Bool

    var body: some View {
        @Bindable var model = model
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
                Circle().fill(model.busy ? Color.accentColor : Color.secondary.opacity(0.5)).frame(width: 10, height: 10)
                TextField("Ask Vera…", text: $model.question, axis: .vertical)
                    .textFieldStyle(.plain)
                    .font(.system(size: 18))
                    .lineLimit(1...askLineLimit)
                    .focused($focused)
                    .onSubmit {
                        let q = model.question.trimmingCharacters(in: .whitespacesAndNewlines)
                        // A picture on its own is a whole question —
                        // pointing at something is what ⌘V here means.
                        guard !q.isEmpty || !model.attached.isEmpty, !model.busy else { return }
                        onAsk(q)
                        model.question = ""
                    }
                if model.busy { ProgressView().controlSize(.small) }
            }
            // What is going with the question. A count rather than a
            // thumbnail: the panel is one line tall by design, and the
            // mistake worth catching is not knowing anything is
            // attached at all.
            if !model.attached.isEmpty {
                HStack(spacing: 6) {
                    Image(systemName: "photo")
                    Text(Attachment.summary(model.attached))
                    Button("clear") { model.attached = [] }
                        .buttonStyle(.plain)
                        .foregroundStyle(Color.accentColor)
                }
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            }
            if !model.lastQuestion.isEmpty {
                Divider()
                VStack(alignment: .leading, spacing: 4) {
                    Text(model.lastQuestion).font(.system(size: 12)).foregroundStyle(.secondary)
                    if let error = model.error {
                        Text(error).foregroundStyle(.orange)
                    } else if let status = model.status, model.answer.isEmpty {
                        Text(status).foregroundStyle(.secondary)
                    } else {
                        Text(model.answer.isEmpty ? "…" : model.answer)
                            .font(.system(size: 14))
                            .textSelection(.enabled)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .padding(16)
        .frame(width: 560, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(.white.opacity(0.08)))
        .onAppear { focused = true }
        .onExitCommand { onClose() }
        .background(FocusOnShow { focused = true })
    }
}

/// Re-focuses the field each time the panel is shown; `onAppear` fires
/// once for the life of the hosting view, not per show.
private struct FocusOnShow: NSViewRepresentable {
    let focus: () -> Void
    func makeNSView(context: Context) -> Observer { Observer(focus: focus) }
    func updateNSView(_ view: Observer, context: Context) {}

    final class Observer: NSView {
        let focus: () -> Void
        init(focus: @escaping () -> Void) { self.focus = focus; super.init(frame: .zero) }
        required init?(coder: NSCoder) { nil }
        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            guard let window else { return }
            NotificationCenter.default.addObserver(forName: NSWindow.didBecomeKeyNotification, object: window, queue: .main) { [focus] _ in
                MainActor.assumeIsolated { focus() }
            }
        }
    }
}
