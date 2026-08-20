import SwiftUI

// The phone as a microphone for the Mac.
//
// Tap the mic, walk, talk, tap it off. The phone records — that is all
// it does, and it does it reliably — and hands the audio to the Mac,
// which recognises it with Parakeet and types the words into whatever
// rook is looking at. They sit in the agent's input until you press
// Send. Pacing the room is the point; reading the reply is not, that is
// what the Mac is for.
//
// If the Mac has no speech engine yet, this is where you set one up:
// "download and install Parakeet" runs on the Mac and streams its
// progress here.

struct DictateView: View {
    @Environment(Conversation.self) private var conversation
    @Environment(\.dismiss) private var dismiss
    @State private var link: TerminalLink
    @State private var recorder = Recorder()
    private let client: Client

    @State private var on = false
    @State private var transcribing = false
    @State private var stt: Client.STT?
    @State private var installing = false
    @State private var installStep = ""
    @State private var checkFailed: String?

    init(client: Client) {
        self.client = client
        _link = State(initialValue: TerminalLink(client: client))
    }

    var body: some View {
        ZStack {
            N.bg.ignoresSafeArea()
            VStack(spacing: 0) {
                header
                Spacer()
                if let stt, stt.ready == true {
                    target
                    Spacer()
                    words
                    button
                    sendRow
                } else {
                    engineSetup
                    Spacer()
                }
            }
        }
        .task {
            _ = await recorder.authorize()
            link.start()
            await refreshSTT()
        }
        .onDisappear { link.stop(); recorder.stop() }
    }

    // MARK: - Header

    private var header: some View {
        HStack {
            Text(conversation.pairing?.name ?? "Vera")
                .font(N.body(13, .medium)).foregroundStyle(N.dim)
            Spacer()
            Button("Done") { dismiss() }
                .font(N.body(14, .medium)).foregroundStyle(N.accent300).buttonStyle(.plain)
        }
        .padding(.horizontal, 26).padding(.top, 18)
    }

    // MARK: - Engine setup

    private var engineSetup: some View {
        VStack(spacing: 18) {
            Image(systemName: "waveform.badge.magnifyingglass")
                .font(.system(size: 40)).foregroundStyle(N.dim)
            Text("Speech runs on the Mac")
                .font(N.body(20, .semibold)).foregroundStyle(N.text)
            Text(setupDetail)
                .font(N.body(14)).foregroundStyle(N.dim)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 40)
            if installing {
                ProgressView().tint(N.accent)
                Text(installStep).font(N.mono(12)).foregroundStyle(N.dim)
                    .multilineTextAlignment(.center).padding(.horizontal, 30)
            } else if stt?.uv == false {
                Text("The Mac needs uv first: install it from docs.astral.sh/uv, then come back.")
                    .font(N.body(12)).foregroundStyle(N.accent300)
                    .multilineTextAlignment(.center).padding(.horizontal, 30)
                Button("Check again") { Task { await refreshSTT() } }
                    .font(N.body(15, .medium)).foregroundStyle(N.accent300).buttonStyle(.plain)
            } else if stt != nil {
                Button("Download & install Parakeet") { install() }
                    .font(N.body(16, .medium)).foregroundStyle(.white)
                    .padding(.horizontal, 22).padding(.vertical, 12)
                    .background(N.accent, in: Capsule()).buttonStyle(.plain)
            } else if let checkFailed {
                Text(checkFailed).font(N.body(13)).foregroundStyle(N.accent300)
                Button("Try again") { Task { await refreshSTT() } }
                    .font(N.body(15, .medium)).foregroundStyle(N.accent300).buttonStyle(.plain)
            } else {
                ProgressView().tint(N.accent)
            }
        }
    }

    private var setupDetail: String {
        if installing { return "Setting up on \(conversation.pairing?.name ?? "the Mac"). This runs once." }
        if stt?.uv == false { return "So your voice never leaves your machines." }
        return "A one-time download of about 600 MB, on the Mac. Your voice never leaves your machines."
    }

    // MARK: - Target

    private var target: some View {
        VStack(spacing: 8) {
            Text(targetLabel).font(N.body(12)).foregroundStyle(N.dim)
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
        guard let terminal = link.target?.terminal else { return "rook isn't showing a pane" }
        return terminal.describe
    }

    // MARK: - Words

    private var words: some View {
        VStack(spacing: 8) {
            if let problem = link.problem, link.watching {
                Text(problem).font(N.body(13)).foregroundStyle(N.accent300)
            }
            if let problem = recorder.problem {
                Text(problem).font(N.body(13)).foregroundStyle(N.accent300)
            }
            if !link.transcript.isEmpty {
                Text(link.transcript).font(N.body(16)).foregroundStyle(N.text).lineLimit(6)
            } else if on {
                Text("Listening").font(N.body(16)).foregroundStyle(N.dim)
            } else if transcribing {
                Text("Transcribing…").font(N.body(16)).foregroundStyle(N.dim)
            } else {
                Text("Tap to talk").font(N.body(14)).foregroundStyle(N.dim)
            }
        }
        .multilineTextAlignment(.center)
        .frame(maxWidth: .infinity, minHeight: 80)
        .padding(.horizontal, 26).padding(.bottom, 14)
    }

    // MARK: - Button

    private var button: some View {
        Button { on ? end() : begin() } label: {
            ZStack {
                Circle().fill(on ? N.accent : N.surface).frame(width: 112, height: 112)
                Circle().strokeBorder(N.accent300.opacity(on ? 0.6 : 0), lineWidth: 3)
                    .frame(width: 126 + CGFloat(recorder.level) * 24, height: 126 + CGFloat(recorder.level) * 24)
                Image(systemName: on ? "stop.fill" : "mic.fill")
                    .font(.system(size: on ? 32 : 38))
                    .foregroundStyle(on ? .white : N.accent300)
            }
        }
        .buttonStyle(.plain)
        .scaleEffect(on ? 1.06 : 1)
        .animation(.spring(response: 0.25, dampingFraction: 0.7), value: on)
        .animation(.easeOut(duration: 0.1), value: recorder.level)
        .sensoryFeedback(.impact(weight: .medium), trigger: on)
        .opacity((targetOK || on) && !transcribing ? 1 : 0.45)
        .disabled((!targetOK && !on) || transcribing)
        .padding(.bottom, 22)
    }

    private var sendRow: some View {
        Button { Task { await link.send() } } label: {
            HStack(spacing: 8) { Image(systemName: "return"); Text("Send") }
                .font(N.body(15, .medium))
                .foregroundStyle(!link.hasText || on ? N.dim : .white)
                .padding(.horizontal, 22).padding(.vertical, 12)
                .background(!link.hasText || on ? N.surface : N.accent, in: Capsule())
        }
        .buttonStyle(.plain)
        .disabled(!link.hasText || on || link.busy || !targetOK)
        .padding(.bottom, 30)
    }

    // MARK: - Doing

    private func begin() {
        guard !on, !transcribing else { return }
        link.clear()
        if recorder.authorized {
            on = true
            recorder.start()
        } else {
            Task { if await recorder.authorize() { on = true; recorder.start() } }
        }
    }

    private func end() {
        guard on else { return }
        on = false
        guard let audio = recorder.stop() else { return }
        transcribing = true
        Task {
            defer { transcribing = false }
            do {
                let text = try await client.transcribe(audio)
                try? FileManager.default.removeItem(at: audio)
                if !text.isEmpty { link.type(text) }
            } catch {
                link.note(error.localizedDescription)
            }
        }
    }

    private func refreshSTT() async {
        do {
            stt = try await client.sttStatus()
            checkFailed = nil
        } catch {
            checkFailed = error.localizedDescription
        }
    }

    private func install() {
        installing = true
        installStep = "Starting…"
        Task {
            do {
                for try await frame in client.installSTT() {
                    if let step = frame.status { installStep = step }
                    if let err = frame.error, !err.isEmpty { throw ClientError.broken(err) }
                    if frame.done == true { break }
                }
                await refreshSTT()
            } catch {
                installStep = error.localizedDescription
            }
            installing = false
        }
    }
}
