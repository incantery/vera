import SwiftUI

// The phone as a microphone for the Mac.
//
// Tap the mic, walk, talk. What is said is typed into whatever the Mac
// is looking at — a Claude Code session, usually — in chunks as the
// recogniser settles them, and it sits there until you press Send.
// Pacing the room is the point; reading the agent's reply is not, that
// is what the Mac is for. The screen's one job is to say WHERE the
// words will go before they go there.
//
// Chunks, because Apple's recogniser ends a session by itself after
// about a minute or a long pause. Each ending is typed and listening
// starts again, so a long thought is a paragraph in the agent's input
// rather than a sentence and a silence.

struct DictateView: View {
    @Environment(Conversation.self) private var conversation
    @Environment(\.dismiss) private var dismiss
    @State private var link: TerminalLink
    @State private var listener = Listener()
    /// The mic is on: listening, and restarting whenever the recogniser
    /// stops by itself.
    @State private var on = false
    @State private var heard = ""
    @State private var finishing = false

    init(client: Client) {
        _link = State(initialValue: TerminalLink(client: client))
    }

    var body: some View {
        ZStack {
            N.bg.ignoresSafeArea()
            VStack(spacing: 0) {
                header
                Spacer()
                target
                Spacer()
                words
                button
                sendRow
            }
        }
        .task {
            _ = await listener.authorize()
            link.start()
        }
        .onDisappear { link.stop(); listener.release() }
        .onChange(of: listener.heard) { _, now in
            if listener.isListening { heard = now }
        }
    }

    // MARK: - Parts

    private var header: some View {
        HStack {
            Text(conversation.pairing?.name ?? "Vera")
                .font(N.body(13, .medium)).foregroundStyle(N.dim)
            Spacer()
            Button("Done") { dismiss() }
                .font(N.body(14, .medium)).foregroundStyle(N.accent300).buttonStyle(.plain)
        }
        .padding(.horizontal, 26)
        .padding(.top, 18)
    }

    /// Where the words will land. Honest about the cases: no Mac, a Mac
    /// with nothing in front of it, a shell, or an agent.
    private var target: some View {
        VStack(spacing: 8) {
            Text(targetLabel)
                .font(N.body(12)).foregroundStyle(N.dim)
            Text(targetTitle)
                .font(N.body(22, .semibold))
                .foregroundStyle(targetOK ? N.text : N.dim)
                .multilineTextAlignment(.center)
            if let focus = link.target?.focus, let terminal = link.target?.terminal, targetOK {
                Text("\(focus.name) · \(terminal.path.map { ($0 as NSString).lastPathComponent } ?? "")")
                    .font(N.mono(12)).foregroundStyle(N.dim)
            }
        }
        .padding(.horizontal, 30)
        .animation(.easeOut(duration: 0.2), value: link.target)
    }

    private var targetOK: Bool { link.target?.terminal?.isAgent == true }

    private var targetLabel: String {
        if !link.watching { return "Looking for the Mac" }
        if link.target?.terminal == nil { return "Nothing to type into" }
        return targetOK ? "Typing into" : "Not an agent"
    }

    private var targetTitle: String {
        guard link.watching else { return link.problem ?? "…" }
        guard let terminal = link.target?.terminal else {
            return "rook isn't showing a pane"
        }
        return terminal.describe
    }

    private var words: some View {
        VStack(spacing: 8) {
            if let problem = link.problem, link.watching {
                Text(problem).font(N.body(13)).foregroundStyle(N.accent300)
            }
            if let problem = listener.problem {
                Text(problem).font(N.body(13)).foregroundStyle(N.accent300)
            }
            if on || finishing {
                if !link.transcript.isEmpty {
                    Text(link.transcript).font(N.body(14)).foregroundStyle(N.dim).lineLimit(4)
                }
                Text(heard.isEmpty ? (finishing ? "…" : "Listening") : heard)
                    .font(N.body(16)).foregroundStyle(N.text)
            } else if !link.transcript.isEmpty {
                VStack(spacing: 4) {
                    Text("Typed" + (link.lastRaw ? " · as heard" : ""))
                        .font(N.body(11)).foregroundStyle(N.dim)
                    Text(link.transcript).font(N.body(16)).foregroundStyle(N.text).lineLimit(6)
                }
            } else {
                Text("Tap to talk").font(N.body(14)).foregroundStyle(N.dim)
            }
        }
        .multilineTextAlignment(.center)
        .frame(maxWidth: .infinity, minHeight: 80)
        .padding(.horizontal, 26)
        .padding(.bottom, 14)
    }

    private var button: some View {
        Button {
            on ? end() : begin()
        } label: {
            ZStack {
                Circle()
                    .fill(on ? N.accent : N.surface)
                    .frame(width: 112, height: 112)
                Circle()
                    .strokeBorder(N.accent300.opacity(on ? 0.6 : 0), lineWidth: 3)
                    .frame(width: 126, height: 126)
                Image(systemName: on ? "stop.fill" : "mic.fill")
                    .font(.system(size: on ? 32 : 38))
                    .foregroundStyle(on ? .white : N.accent300)
            }
        }
        .buttonStyle(.plain)
        .scaleEffect(on ? 1.06 : 1)
        .animation(.spring(response: 0.25, dampingFraction: 0.7), value: on)
        .sensoryFeedback(.impact(weight: .medium), trigger: on)
        .opacity((targetOK || on) && !finishing ? 1 : 0.45)
        .disabled((!targetOK && !on) || finishing)
        .padding(.bottom, 22)
    }

    private var sendRow: some View {
        Button {
            Task { await link.send() }
        } label: {
            HStack(spacing: 8) {
                Image(systemName: "return")
                Text("Send")
            }
            .font(N.body(15, .medium))
            .foregroundStyle(!link.hasText || on ? N.dim : .white)
            .padding(.horizontal, 22)
            .padding(.vertical, 12)
            .background(!link.hasText || on ? N.surface : N.accent, in: Capsule())
        }
        .buttonStyle(.plain)
        .disabled(!link.hasText || on || link.busy || !targetOK)
        .padding(.bottom, 30)
    }

    // MARK: - Doing

    private func begin() {
        guard !on, !finishing else { return }
        on = true
        heard = ""
        link.clear()
        // Each session Apple ends by itself is typed and listening
        // carries on with no gap in the audio. Chunks are queued and
        // typed in order.
        listener.onChunk = { chunk in link.type(chunk) }
        if listener.authorized {
            listener.startContinuous()
        } else {
            Task { if await listener.authorize() { listener.startContinuous() } }
        }
    }

    /// Mic off: the last chunk is typed once the recogniser has settled
    /// it; the Send button then has something to send.
    private func end() {
        guard on else { return }
        on = false
        finishing = true
        Task {
            let said = await listener.finish()
            finishing = false
            heard = ""
            if let said { link.type(said) }
        }
    }
}
