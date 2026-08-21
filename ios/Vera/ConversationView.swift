import SwiftUI

// The only screen: what has been said, and the thing you say it with.

struct ConversationView: View {
    @Environment(Conversation.self) private var conversation
    @State private var listener = Listener()

    @State private var draft = ""
    /// What was already in the field when recording started. The
    /// recognizer replaces its whole transcript on every update, so
    /// anything typed beforehand has to be held separately or it gets
    /// overwritten by the first word.
    @State private var typedBeforeRecording = ""
    @FocusState private var typing: Bool

    var body: some View {
        ZStack {
            N.bg.ignoresSafeArea()

            VStack(spacing: 0) {
                header
                transcript
            }
            .safeAreaInset(edge: .bottom, spacing: 0) { composer }
        }
        .task { _ = await listener.authorize() }
        .onChange(of: listener.heard) { _, heard in
            guard listener.isListening else { return }
            draft = typedBeforeRecording.isEmpty ? heard : typedBeforeRecording + " " + heard
        }
    }

    // MARK: - Parts

    private var header: some View {
        HStack(spacing: 14) {
            Text(conversation.pairing?.name ?? "Vera")
                .font(N.body(13, .medium))
                .foregroundStyle(N.dim)
            Spacer()
            Button("New") {
                conversation.startNew()
                draft = ""
                typedBeforeRecording = ""
            }
            .font(N.body(12))
            .foregroundStyle(conversation.exchanges.isEmpty ? N.dim.opacity(0.4) : N.accent300)
            .buttonStyle(.plain)
            .disabled(conversation.exchanges.isEmpty)

            Button("Unpair") { conversation.unpair() }
                .font(N.body(12))
                .foregroundStyle(N.dim)
                .buttonStyle(.plain)
        }
        .padding(.horizontal, 26)
        .padding(.bottom, 18)
    }

    private var transcript: some View {
        ScrollViewReader { scroll in
            ScrollView {
                VStack(alignment: .leading, spacing: 30) {
                    if conversation.exchanges.isEmpty {
                        Text("Say something, or type it.")
                            .font(N.body(15))
                            .foregroundStyle(N.dim)
                            .padding(.top, 60)
                    }
                    ForEach(conversation.exchanges) { exchange in
                        ExchangeView(exchange: exchange).id(exchange.id)
                    }
                    // Anchor: scrolling to the last exchange would stop
                    // with its first line at the bottom edge while the
                    // reply kept growing past it.
                    Color.clear.frame(height: 1).id("bottom")
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 26)
                .padding(.bottom, 20)
            }
            .scrollDismissesKeyboard(.interactively)
            .onChange(of: conversation.exchanges.last?.status) {
                withAnimation(.easeOut(duration: 0.2)) { scroll.scrollTo("bottom", anchor: .bottom) }
            }
            .onChange(of: conversation.exchanges.last?.reply) {
                withAnimation(.easeOut(duration: 0.2)) { scroll.scrollTo("bottom", anchor: .bottom) }
            }
            .onChange(of: conversation.exchanges.count) {
                withAnimation(.easeOut(duration: 0.2)) { scroll.scrollTo("bottom", anchor: .bottom) }
            }
        }
    }

    private var composer: some View {
        VStack(spacing: 0) {
            if let problem = listener.problem {
                Text(problem)
                    .font(N.body(12))
                    .foregroundStyle(N.accent300)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 26)
                    .padding(.bottom, 10)
            }

            HStack(alignment: .bottom, spacing: 10) {
                Attach()

                TextField(
                    "",
                    text: $draft,
                    prompt: Text(listener.isListening ? "Listening…" : "Message")
                        .foregroundStyle(N.dim),
                    axis: .vertical
                )
                .font(N.body(16))
                .foregroundStyle(N.text)
                .tint(N.accent)
                .lineLimit(1...6)
                .focused($typing)
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .background(N.surface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))

                MicButton(listening: listener.isListening, action: toggleRecording)
                SendButton(enabled: !draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
                           action: send)
            }
            .padding(.horizontal, 18)
            .padding(.bottom, 10)
        }
        .padding(.top, 10)
        .background(N.bg)
    }

    // MARK: - Doing

    private func toggleRecording() {
        if listener.isListening {
            listener.stop()
            return
        }
        // Whatever is in the field stays; the transcript is added to it.
        typedBeforeRecording = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        typing = false

        if listener.authorized {
            // Synchronous, so the recording starts on the tap rather
            // than a frame or two after it.
            listener.start()
        } else {
            Task {
                if await listener.authorize() { listener.start() }
            }
        }
    }

    private func send() {
        if listener.isListening { listener.stop() }
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        conversation.send(text)
        draft = ""
        typedBeforeRecording = ""
    }
}

// MARK: - Composer buttons

/// Photos and files, when there is anything on the other end that could
/// read them. Disabled rather than absent: the shape of the composer is
/// part of what is being judged, and a button that quietly does nothing
/// would be worse than one that says it isn't ready.
private struct Attach: View {
    var body: some View {
        Menu {
            Section("Not yet") {
                Button("Photo") {}.disabled(true)
                Button("File") {}.disabled(true)
            }
        } label: {
            Image(systemName: "plus")
                .font(.system(size: 19, weight: .medium))
                .foregroundStyle(N.dim)
                .frame(width: 40, height: 40)
        }
    }
}

private struct MicButton: View {
    let listening: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            ZStack {
                Circle()
                    .fill(listening ? N.accent : N.surface)
                    .frame(width: 40, height: 40)
                Image(systemName: listening ? "stop.fill" : "mic.fill")
                    .font(.system(size: listening ? 14 : 17))
                    .foregroundStyle(listening ? .white : N.accent300)
            }
            // A recording that has not visibly started is a recording
            // you will talk over the beginning of.
            .overlay {
                if listening {
                    Circle().strokeBorder(N.accent300.opacity(0.5), lineWidth: 2).frame(width: 46, height: 46)
                }
            }
        }
        .buttonStyle(.plain)
        .animation(.spring(response: 0.25, dampingFraction: 0.75), value: listening)
        .sensoryFeedback(.impact(weight: .light), trigger: listening)
    }
}

private struct SendButton: View {
    let enabled: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            ZStack {
                Circle()
                    .fill(enabled ? N.accent : N.surface)
                    .frame(width: 40, height: 40)
                Image(systemName: "arrow.up")
                    .font(.system(size: 17, weight: .semibold))
                    .foregroundStyle(enabled ? .white : N.dim)
            }
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .animation(.easeOut(duration: 0.15), value: enabled)
    }
}

// MARK: - One exchange

private struct ExchangeView: View {
    let exchange: Exchange

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(exchange.said)
                .font(N.body(15))
                .leading(15, 1.5)
                .foregroundStyle(N.dim)
                .fixedSize(horizontal: false, vertical: true)

            if let failed = exchange.failed {
                Text(failed)
                    .font(N.body(15))
                    .leading(15, 1.5)
                    .foregroundStyle(N.accent300)
                    .fixedSize(horizontal: false, vertical: true)
            } else if exchange.reply.isEmpty {
                // A named wait is a different thing from a pause: the
                // dots mean "thinking", the sentence means "doing", and
                // only the second one is worth minutes of a person's
                // patience.
                if let status = exchange.status {
                    HStack(alignment: .top, spacing: 9) {
                        ThinkingDots()
                        Text(status)
                            .font(N.body(14))
                            .leading(14, 1.45)
                            .foregroundStyle(N.dim)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .transition(.opacity)
                } else {
                    ThinkingDots()
                }
            } else {
                Text(exchange.reply)
                    .font(N.body(17))
                    .leading(17, 1.5)
                    .foregroundStyle(N.text)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}

/// The gap between sending and the first word. Nothing else in the app
/// moves on its own.
private struct ThinkingDots: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var phase = 0.0

    var body: some View {
        HStack(spacing: 5) {
            ForEach(0..<3, id: \.self) { i in
                Circle()
                    .fill(N.dim)
                    .frame(width: 5, height: 5)
                    .opacity(reduceMotion ? 0.5 : 0.25 + 0.75 * pulse(i))
            }
        }
        .frame(height: 17)
        .onAppear {
            guard !reduceMotion else { return }
            withAnimation(.linear(duration: 1.1).repeatForever(autoreverses: false)) { phase = 3 }
        }
    }

    private func pulse(_ i: Int) -> Double {
        let t = (phase - Double(i)).truncatingRemainder(dividingBy: 3)
        return t > 0 && t < 1 ? sin(t * .pi) : 0
    }
}
