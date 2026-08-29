import PhotosUI
import SwiftUI

// The only screen: what has been said, and the thing you say it with.

struct ConversationView: View {
    @Environment(Conversation.self) private var conversation
    @State private var listener = Listener()

    @State private var draft = ""
    /// Pictures attached but not yet sent. They wait here because the
    /// picture and the sentence about it are one message, typed at two
    /// different moments.
    @State private var attached: [SayImage] = []
    @State private var picking: [PhotosPickerItem] = []
    /// Said out loud when a picture cannot be made of what was picked
    /// — a silent drop would leave a question about nothing.
    @State private var trouble: String?
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
        .onChange(of: picking) { _, items in
            guard !items.isEmpty else { return }
            Task { await absorb(items) }
        }
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
            // Every frame, not only every word: a tool printing while
            // it runs and a question arriving are both things that grow
            // the last exchange, and both are worth following.
            .onChange(of: conversation.exchanges.last?.seen) {
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

            if !attached.isEmpty || trouble != nil {
                HStack(spacing: 8) {
                    if let trouble {
                        Text(trouble).foregroundStyle(N.accent300)
                    } else {
                        Image(systemName: "photo")
                        Text(Attachment.summary(attached) + " going with this")
                        Button("clear") { attached = [] }
                            .buttonStyle(.plain)
                            .foregroundStyle(N.accent300)
                    }
                    Spacer()
                }
                .font(N.body(12))
                .foregroundStyle(N.dim)
                .padding(.horizontal, 26)
                .padding(.bottom, 10)
            }

            HStack(alignment: .bottom, spacing: 10) {
                Attach(picking: $picking, onPaste: pasteImage)

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
                SendButton(enabled: !draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !attached.isEmpty,
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
        // A picture on its own is a whole message.
        guard !text.isEmpty || !attached.isEmpty else { return }
        conversation.send(text, images: attached)
        draft = ""
        attached = []
        trouble = nil
        typedBeforeRecording = ""
    }

    /// The pasteboard, if it is holding a picture. iOS shows its own
    /// "pasted from" banner; that is the system telling the person what
    /// happened, and it is not this app's to suppress.
    private func pasteImage() {
        guard let picture = Attachment.fromPasteboard() else {
            trouble = "There's no picture on the clipboard."
            return
        }
        attached.append(picture)
        trouble = nil
    }

    /// What the photo picker handed over, turned into something the
    /// Mac keeps. A phone's own pictures are HEIC, which nothing
    /// downstream reads, so this is where they stop being HEIC.
    private func absorb(_ items: [PhotosPickerItem]) async {
        for item in items {
            guard let data = try? await item.loadTransferable(type: Data.self),
                  let picture = Attachment.image(data, named: item.itemIdentifier ?? "photo")
            else {
                trouble = "I couldn't make a picture of that one."
                continue
            }
            attached.append(picture)
            trouble = nil
        }
        picking = []
    }
}

// MARK: - Composer buttons

/// Pictures, from the two places a phone has them: the library, and
/// whatever was last copied.
///
/// Vera cannot see them. She keeps them on the Mac and hands the files
/// to whichever agent she gives the work to — which is what makes a
/// screenshot worth sending at all.
private struct Attach: View {
    @Binding var picking: [PhotosPickerItem]
    let onPaste: () -> Void

    var body: some View {
        Menu {
            PhotosPicker("Photo", selection: $picking, maxSelectionCount: 4, matching: .images)
            Button("Paste", action: onPaste)
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
            if !exchange.said.isEmpty {
                Text(exchange.said)
                    .font(N.body(15))
                    .leading(15, 1.5)
                    .foregroundStyle(N.dim)
                    .fixedSize(horizontal: false, vertical: true)
            }
            // What went with it. The picture itself is not kept here —
            // the Mac has it — but a transcript that did not say one
            // was sent would make the answer read as a non sequitur.
            if let count = exchange.images, count > 0 {
                Label(count == 1 ? "1 image" : "\(count) images", systemImage: "photo")
                    .font(N.body(12))
                    .foregroundStyle(N.dim.opacity(0.7))
            }

            // Everything that came back, where it came back. A question
            // shown under the paragraph that follows it is a question
            // about nothing.
            ForEach(exchange.steps) { step in
                switch step.body {
                case .said(let words):
                    if !words.isEmpty {
                        Text(words)
                            .font(N.body(17))
                            .leading(17, 1.5)
                            .foregroundStyle(N.text)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                case .tool(let run):
                    ToolRow(run: run)
                case .ask(let question):
                    AskCard(question: question, exchange: exchange.id)
                }
            }

            if let failed = exchange.failed {
                Text(failed)
                    .font(N.body(15))
                    .leading(15, 1.5)
                    .foregroundStyle(N.accent300)
                    .fixedSize(horizontal: false, vertical: true)
            } else if waiting {
                // A named wait is a different thing from a pause: the
                // dots mean "thinking", the sentence means "doing", and
                // only the second one is worth minutes of a person's
                // patience. A question needs neither — the card is the
                // thing being waited on, and it says so itself.
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
                } else if exchange.steps.isEmpty {
                    ThinkingDots()
                }
            }
        }
    }

    private var waiting: Bool {
        !exchange.done && exchange.openQuestion == nil
    }
}

// MARK: - A tool round

/// One call, compact: what it is, roughly what it was given, and how it
/// is going. Opened, it is the arguments in full and whatever the tool
/// printed — which is the difference between "she ran something" and
/// knowing what she ran.
private struct ToolRow: View {
    let run: ToolRun
    @State private var open = false

    /// Enough of a long result to see what happened, not so much that
    /// the transcript becomes the tool's output.
    private static let bodyLines = 40

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Button { open.toggle() } label: { head }
                .buttonStyle(.plain)
            if open { details }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .background(N.surface.opacity(0.55), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
    }

    private var head: some View {
        HStack(spacing: 8) {
            Image(systemName: open ? "chevron.down" : "chevron.right")
                .font(.system(size: 9, weight: .semibold))
                .foregroundStyle(N.dim)
                .frame(width: 10)
            mark
            Text(run.name)
                .font(N.mono(13))
                .foregroundStyle(N.accent300)
                .lineLimit(1)
                .layoutPriority(1)
            let summary = Args.summary(run.args)
            if !summary.isEmpty {
                Text(summary)
                    .font(N.mono(12))
                    .foregroundStyle(N.dim)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
            Spacer(minLength: 6)
            if !aside.isEmpty {
                Text(aside)
                    .font(N.mono(11))
                    .foregroundStyle(N.dim)
                    .lineLimit(1)
                    .layoutPriority(1)
            }
        }
        .contentShape(Rectangle())
    }

    @ViewBuilder private var mark: some View {
        if run.isRunning {
            ProgressView()
                .progressViewStyle(.circular)
                .scaleEffect(0.55)
                .frame(width: 12, height: 12)
        } else {
            Image(systemName: run.stopped || run.isFailed ? "xmark" : "checkmark")
                .font(.system(size: 10, weight: .semibold))
                .foregroundStyle(run.stopped || run.isFailed ? N.accent300 : N.dim)
                .frame(width: 12, height: 12)
        }
    }

    /// The right-hand end of the row: how much a running tool has said,
    /// and what a finished one took.
    private var aside: String {
        if run.stopped { return "stopped" }
        if run.isRunning { return run.output.isEmpty ? "" : volume(run.output) }
        var out = duration(run.durationMs)
        if let cost = run.cost, cost > 0 {
            out += out.isEmpty ? "" : "  "
            out += cost >= 1 ? String(format: "$%.2f", cost) : String(format: "$%.4f", cost)
        }
        return out
    }

    @ViewBuilder private var details: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let fields = Args.fields(run.args), !fields.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(fields.prefix(8), id: \.key) { field in
                        Text(field.key).font(N.mono(10)).foregroundStyle(N.dim)
                        Text(Args.short(field.value, limit: 400))
                            .font(N.mono(12))
                            .foregroundStyle(N.text)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            } else if !run.args.isEmpty {
                Text(run.args)
                    .font(N.mono(12))
                    .foregroundStyle(N.text)
                    .lineLimit(8)
            }

            if run.stopped && run.body.isEmpty {
                Text("stopped — the exchange ended first")
                    .font(N.body(12))
                    .foregroundStyle(N.accent300)
            } else if run.body.isEmpty {
                Text(run.isRunning ? "running…" : "nothing came back")
                    .font(N.body(12))
                    .foregroundStyle(N.dim)
            } else {
                let lines = run.body.trimmingCharacters(in: .newlines).components(separatedBy: "\n")
                let shown = lines.suffix(Self.bodyLines)
                Text(run.result == nil ? "output · \(lines.count) lines" : "result · \(lines.count) lines")
                    .font(N.mono(10))
                    .foregroundStyle(N.dim)
                Text(shown.joined(separator: "\n"))
                    .font(N.mono(12))
                    .foregroundStyle(run.isFailed ? N.accent300 : N.text)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func duration(_ ms: Int?) -> String {
        guard let ms, ms > 0 else { return "" }
        if ms < 1000 { return "\(ms)ms" }
        if ms < 60_000 { return String(format: "%.2fs", Double(ms) / 1000) }
        return "\(ms / 60_000)m\(String(format: "%02d", (ms % 60_000) / 1000))s"
    }

    /// Lines are what a person counts; the count is what says it is
    /// still moving when the lines are long.
    private func volume(_ text: String) -> String {
        let lines = text.components(separatedBy: "\n").count
        return lines == 1 ? "1 line" : "\(lines) lines"
    }
}

// MARK: - A question

/// The one thing on this screen that is not there to be read. Vera will
/// not run this call without a word, and until a word goes back the
/// exchange on the Mac is parked — so the card says what she wants to
/// run, what she was going to run it with, why she is asking, and gives
/// the three answers she understands.
private struct AskCard: View {
    let question: Question
    let exchange: Exchange.ID
    @Environment(Conversation.self) private var conversation

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 8) {
                Image(systemName: "hand.raised.fill")
                    .font(.system(size: 11))
                    .foregroundStyle(question.isOpen ? N.accent300 : N.dim)
                Text(question.name)
                    .font(N.mono(13))
                    .foregroundStyle(N.text)
                    .lineLimit(1)
                Spacer(minLength: 6)
                if !outcome.isEmpty {
                    Text(outcome)
                        .font(N.body(11))
                        .foregroundStyle(N.dim)
                        .lineLimit(1)
                }
            }

            let summary = Args.summary(question.args)
            if !summary.isEmpty {
                Text(summary)
                    .font(N.mono(12))
                    .foregroundStyle(N.dim)
                    .lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if !question.reason.isEmpty {
                Text(question.reason)
                    .font(N.body(14))
                    .leading(14, 1.45)
                    .foregroundStyle(N.text)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if let trouble = question.trouble {
                Text(trouble)
                    .font(N.body(12))
                    .foregroundStyle(N.accent300)
                    .fixedSize(horizontal: false, vertical: true)
            }

            HStack(spacing: 8) {
                ForEach(Choice.all) { option in
                    ChoiceButton(option: option,
                                 chosen: question.choice == option.choice,
                                 enabled: question.isOpen) {
                        conversation.answer(option.choice, to: question.id, in: exchange)
                    }
                }
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(N.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .strokeBorder(question.isOpen ? N.accent.opacity(0.55) : .clear, lineWidth: 1)
        }
    }

    /// What became of it, once it is no longer a question.
    private var outcome: String {
        switch question.state {
        case .waiting: ""
        case .answered: "you said \(question.choice ?? "")"
        case .closed: "no answer went back"
        }
    }
}

private struct ChoiceButton: View {
    let option: Choice.Option
    let chosen: Bool
    let enabled: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(option.label)
                .font(N.body(14, .medium))
                .foregroundStyle(foreground)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 10)
                .background(background, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .sensoryFeedback(.impact(weight: .light), trigger: chosen)
        .accessibilityLabel("\(option.label) — \(option.help)")
    }

    private var foreground: Color {
        if chosen { return .white }
        return enabled ? N.text : N.dim.opacity(0.6)
    }

    private var background: Color {
        if chosen { return N.accent }
        return enabled ? N.bg.opacity(0.6) : N.bg.opacity(0.3)
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
