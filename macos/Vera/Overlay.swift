import AppKit
import Observation
import SwiftUI

// The floating surface.
//
// A small panel that appears when the hotkey is held and leaves when the
// exchange is over. It never takes focus — `.nonactivatingPanel` is the
// whole trick — so the application you were working in is still the one
// receiving your keystrokes, which is what makes "say something about
// this" possible in the first place.

enum OverlayState: Equatable {
    case hidden
    case listening(heard: String)
    case processing(said: String, status: String?)
    case answering(said: String, answer: String, done: Bool)
    /// Dictation that landed at the cursor.
    case inserted(text: String, into: String?, raw: Bool)
    case error(String)
}

@Observable
@MainActor
final class OverlayModel {
    var state: OverlayState = .hidden
}

@MainActor
final class Overlay {
    let model = OverlayModel()
    var edge: Settings.OverlayEdge = .top

    var onPresented: () -> Void = {}
    var onDismissed: () -> Void = {}

    private var panel: NSPanel?
    private var hosting: NSHostingView<OverlayView>?
    private var dismissal: Task<Void, Never>?
    private(set) var isVisible = false

    private let width: CGFloat = 380
    private var lastFrame = NSRect.zero

    func show(_ state: OverlayState) {
        dismissal?.cancel()
        dismissal = nil
        guard model.state != state || !isVisible else { return }
        model.state = state
        let panel = panel ?? makePanel()
        layout(panel)
        if !isVisible {
            isVisible = true
            panel.alphaValue = 0
            panel.orderFrontRegardless()
            NSAnimationContext.runAnimationGroup { ctx in
                ctx.duration = 0.16
                panel.animator().alphaValue = 1
            }
            onPresented()
        }
    }

    /// Lets the surface linger long enough to be read, then fades it.
    func dismiss(after delay: Duration) {
        dismissal?.cancel()
        dismissal = Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard !Task.isCancelled else { return }
            self?.hide()
        }
    }

    func hide() {
        dismissal?.cancel()
        dismissal = nil
        guard isVisible, let panel else { return }
        isVisible = false
        NSAnimationContext.runAnimationGroup({ ctx in
            ctx.duration = 0.2
            panel.animator().alphaValue = 0
        }, completionHandler: {
            // The completion runs on the main thread; the assertion
            // makes the hop explicit for the compiler.
            MainActor.assumeIsolated {
                if !self.isVisible {
                    panel.orderOut(nil)
                    self.model.state = .hidden
                    self.onDismissed()
                }
            }
        })
    }

    // MARK: - Window

    private func makePanel() -> NSPanel {
        let panel = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: width, height: 80),
            styleMask: [.borderless, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        panel.level = .floating
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = true
        panel.hidesOnDeactivate = false
        panel.isMovableByWindowBackground = false
        panel.ignoresMouseEvents = true
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary, .ignoresCycle]
        panel.animationBehavior = .none

        let hosting = NSHostingView(rootView: OverlayView(model: model))
        hosting.sizingOptions = [.intrinsicContentSize]
        panel.contentView = hosting
        self.hosting = hosting
        self.panel = panel
        return panel
    }

    /// Top- or bottom-centre of the screen the mouse is on: near where
    /// attention is, away from where the work is.
    private func layout(_ panel: NSPanel) {
        guard let hosting else { return }
        hosting.layoutSubtreeIfNeeded()
        var size = hosting.fittingSize
        size.width = width
        size.height = max(56, min(size.height, 320))

        let mouse = NSEvent.mouseLocation
        let screen = NSScreen.screens.first { NSMouseInRect(mouse, $0.frame, false) } ?? NSScreen.main ?? NSScreen.screens[0]
        let visible = screen.visibleFrame
        let x = visible.midX - size.width / 2
        let y: CGFloat
        switch edge {
        case .top: y = visible.maxY - size.height - 28
        case .bottom: y = visible.minY + 36
        }
        let frame = NSRect(x: x, y: y, width: size.width, height: size.height)
        guard frame != lastFrame else { return }
        lastFrame = frame
        if isVisible {
            // Never `setFrame(animate: true)`: that animation blocks the
            // main thread for its whole duration, and the words arriving
            // behind it queue up into bursts.
            NSAnimationContext.runAnimationGroup { ctx in
                ctx.duration = 0.12
                panel.animator().setFrame(frame, display: true)
            }
        } else {
            panel.setFrame(frame, display: true)
        }
    }
}

// MARK: - The view

struct OverlayView: View {
    let model: OverlayModel

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Orb(state: model.state)
                .frame(width: 22, height: 22)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 4) {
                Text(headline)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(.secondary)
                if let detail {
                    Text(detail)
                        .font(.system(size: 14))
                        .foregroundStyle(.primary)
                        .lineLimit(8)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 14)
        .frame(width: 380, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(.white.opacity(0.08)))
        .animation(.easeOut(duration: 0.2), value: model.state)
    }

    private var headline: String {
        switch model.state {
        case .hidden: ""
        case .listening(let heard): heard.isEmpty ? "Listening…" : "Listening"
        case .processing(_, let status): status ?? "Thinking…"
        case .answering(_, _, let done): done ? "Vera" : "Vera…"
        case .inserted(_, let into, let raw): "Typed" + (into.map { " into \($0)" } ?? "") + (raw ? " · as heard" : "")
        case .error: "Hm."
        }
    }

    private var detail: String? {
        switch model.state {
        case .hidden: nil
        case .listening(let heard): heard.isEmpty ? nil : heard
        case .processing(let said, _): "“\(said)”"
        case .answering(_, let answer, _): answer.isEmpty ? "…" : answer
        case .inserted(let text, _, _): text
        case .error(let why): why
        }
    }
}

/// The one piece of motion: a dot that breathes while listening, spins
/// while thinking, and sits still when there is something to read.
private struct Orb: View {
    let state: OverlayState
    @State private var phase = false

    var body: some View {
        ZStack {
            Circle().fill(color.opacity(0.25))
                .scaleEffect(listening ? (phase ? 1.35 : 0.9) : 1)
            Circle().fill(color)
                .padding(5)
            if thinking {
                Circle()
                    .trim(from: 0.15, to: 0.85)
                    .stroke(color, style: StrokeStyle(lineWidth: 2, lineCap: .round))
                    .rotationEffect(.degrees(phase ? 360 : 0))
            }
        }
        .onAppear { restart() }
        .onChange(of: state) { restart() }
    }

    private func restart() {
        phase = false
        if listening {
            withAnimation(.easeInOut(duration: 0.9).repeatForever(autoreverses: true)) { phase = true }
        } else if thinking {
            withAnimation(.linear(duration: 1).repeatForever(autoreverses: false)) { phase = true }
        }
    }

    private var listening: Bool { if case .listening = state { return true } else { return false } }
    private var thinking: Bool {
        switch state {
        case .processing: return true
        case .answering(_, _, let done): return !done
        default: return false
        }
    }

    private var color: Color {
        switch state {
        case .listening: .red
        case .processing, .answering: .accentColor
        case .inserted: .green
        case .error: .orange
        case .hidden: .secondary
        }
    }
}
