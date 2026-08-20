import SwiftUI

// The phone as a microphone for the Mac.
//
// Hold the button, talk, let go: the words are typed into whatever the
// Mac is looking at — a Claude Code session, usually — and they sit
// there until you press Send. Pacing the room is the point; reading the
// agent's reply is not, that is what the Mac is for. The screen's one
// job is to say WHERE the words will go before they go there.

struct DictateView: View {
    @Environment(Conversation.self) private var conversation
    @Environment(\.dismiss) private var dismiss
    @State private var link: TerminalLink
    @State private var listener = Listener()
    @State private var holding = false
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
        .onDisappear { link.stop(); listener.stop() }
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
            if holding || finishing {
                Text(heard.isEmpty ? (finishing ? "…" : "Listening") : heard)
                    .font(N.body(16)).foregroundStyle(N.text)
            } else if let typed = link.lastTyped {
                VStack(spacing: 4) {
                    Text("Typed" + (link.lastRaw ? " · as heard" : ""))
                        .font(N.body(11)).foregroundStyle(N.dim)
                    Text(typed).font(N.body(16)).foregroundStyle(N.text)
                }
            } else {
                Text("Hold to talk").font(N.body(14)).foregroundStyle(N.dim)
            }
        }
        .multilineTextAlignment(.center)
        .frame(maxWidth: .infinity, minHeight: 80)
        .padding(.horizontal, 26)
        .padding(.bottom, 14)
    }

    private var button: some View {
        ZStack {
            Circle()
                .fill(holding ? N.accent : N.surface)
                .frame(width: 112, height: 112)
            Circle()
                .strokeBorder(N.accent300.opacity(holding ? 0.6 : 0), lineWidth: 3)
                .frame(width: 126, height: 126)
            Image(systemName: "mic.fill")
                .font(.system(size: 38))
                .foregroundStyle(holding ? .white : N.accent300)
        }
        .scaleEffect(holding ? 1.06 : 1)
        .animation(.spring(response: 0.25, dampingFraction: 0.7), value: holding)
        .sensoryFeedback(.impact(weight: .medium), trigger: holding)
        .opacity(targetOK && !finishing ? 1 : 0.45)
        .onLongPressGesture(minimumDuration: .infinity, pressing: { pressing in
            pressing ? begin() : end()
        }, perform: {})
        .disabled(!targetOK || finishing)
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
            .foregroundStyle(link.lastTyped == nil ? N.dim : .white)
            .padding(.horizontal, 22)
            .padding(.vertical, 12)
            .background(link.lastTyped == nil ? N.surface : N.accent, in: Capsule())
        }
        .buttonStyle(.plain)
        .disabled(link.lastTyped == nil || link.busy || !targetOK)
        .padding(.bottom, 30)
    }

    // MARK: - Doing

    private func begin() {
        guard !holding, !finishing else { return }
        holding = true
        heard = ""
        if listener.authorized {
            listener.start()
        } else {
            Task { if await listener.authorize() { listener.start() } }
        }
    }

    private func end() {
        guard holding else { return }
        holding = false
        finishing = true
        Task {
            let said = await listener.finish()
            finishing = false
            guard let said else { return }
            heard = said
            await link.type(said)
        }
    }
}
