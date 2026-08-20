import AppKit
import Observation
import SwiftUI

// The panel where you talk to Vera with a keyboard.
//
// Not everywhere is a place you can speak — a meeting, a café, a quiet
// office — so the same conversation has a typed door. Fn+T opens it as
// a floating field, Enter sends, Esc closes. Unlike the dictation pill
// it takes keyboard focus, and unlike a normal window it does not
// activate the app: the application you were in stays where it was,
// and the panel goes away when you are done.

@Observable
@MainActor
final class AskModel {
    var question = ""
    var answer = ""
    var status: String?
    var error: String?
    var busy = false
    var lastQuestion = ""
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
                TextField("Ask Vera…", text: $model.question)
                    .textFieldStyle(.plain)
                    .font(.system(size: 18))
                    .focused($focused)
                    .onSubmit {
                        let q = model.question.trimmingCharacters(in: .whitespacesAndNewlines)
                        guard !q.isEmpty, !model.busy else { return }
                        onAsk(q)
                        model.question = ""
                    }
                if model.busy { ProgressView().controlSize(.small) }
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
