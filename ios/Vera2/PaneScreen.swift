import SwiftUI

// Vera's one eye into the terminal.
//
// The phone cannot see rook — rook is on the Mac. So when you want to
// watch a pane (the agent's reply forming while you dictate, or the PR
// comment you are about to answer), the Mac reads the pane's visible
// rows and ships them here, and this polls for them a couple of times a
// second. Read-only: a snapshot, not a terminal you can drive. Reading
// the reply is still what the Mac is for — this is a glance, so you
// know what you are talking to before you talk to it.
//
// Borrowed in spirit from remux (MIT), which renders tmux on iOS over
// its own SSH transport. We keep our transport — the paired vera2
// channel — and take only the idea: show the pane on the phone. The
// live Ghostty terminal is a later step, if a glance turns out not to
// be enough.

@Observable
@MainActor
final class PaneFeed {
    private(set) var lines: [String] = []
    private(set) var problem: String?
    /// True once a read has landed; false while the first is in flight
    /// or after one fails, so the view can tell "waiting" from "empty".
    private(set) var live = false

    @ObservationIgnored private var poller: Task<Void, Never>?
    @ObservationIgnored private let client: Client
    /// The pane to read; nil means whatever the Mac is looking at, which
    /// follows focus on its own — the Mac resolves it each read.
    @ObservationIgnored private let target: Target.Terminal?
    /// The width, in columns, to hold the pane at while this is open, so
    /// the agent lays itself out for the phone. The Mac reflows the
    /// window to this and restores it when the polls stop.
    @ObservationIgnored private let cols: Int

    init(client: Client, target: Target.Terminal? = nil, cols: Int = 0) {
        self.client = client
        self.target = target
        self.cols = cols
    }

    func start() {
        poller?.cancel()
        poller = Task { [weak self] in
            while !Task.isCancelled, let self {
                do {
                    lines = try await client.screen(at: target, mobile: cols > 0, cols: cols)
                    live = true
                    problem = nil
                } catch is CancellationError {
                    break
                } catch {
                    live = false
                    problem = error.localizedDescription
                }
                try? await Task.sleep(for: .milliseconds(1200))
            }
        }
    }

    func stop() {
        poller?.cancel()
        poller = nil
        live = false
        // Snap the desk back to full width now, rather than wait for the
        // Mac's own timeout to notice the polls stopped. Best-effort; the
        // timeout is the guarantee.
        guard cols > 0 else { return }
        let client = client, target = target
        Task { try? await client.screen(at: target, mobile: false, cols: cols) }
    }
}

// The pane, drawn as text. Monospaced, scrollable both ways because an
// agent's TUI is wider than a phone, and pinned to the bottom as new
// lines arrive — where the conversation is.
struct PaneScreenView: View {
    let lines: [String]

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView([.vertical, .horizontal]) {
                VStack(alignment: .leading, spacing: 1) {
                    ForEach(Array(lines.enumerated()), id: \.offset) { i, line in
                        Text(line.isEmpty ? " " : line)
                            .font(N.mono(10))
                            .foregroundStyle(N.text)
                            .fixedSize(horizontal: true, vertical: false)
                            .id(i)
                    }
                }
                .padding(12)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .onChange(of: lines.count) { _, n in
                guard n > 0 else { return }
                withAnimation(.easeOut(duration: 0.15)) { proxy.scrollTo(n - 1, anchor: .bottom) }
            }
        }
        .background(N.bg, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .strokeBorder(N.surface, lineWidth: 1)
        )
    }
}
