import Foundation
import Observation

// The conversation.
//
// The Mac holds what was said (history) and what is true about you
// (memory). This holds neither — only what is on screen, and only so
// that reopening the app does not look like nothing ever happened.

struct Exchange: Identifiable, Codable, Sendable {
    var id = UUID()
    var said: String
    var reply = ""
    var failed: String?
    var done = false

    /// What Vera is doing while it has nothing to say yet. Some work
    /// takes minutes, and a silent screen for that long reads as
    /// broken.
    var status: String?
}

@Observable
@MainActor
final class Conversation {

    var pairing: Pairing? {
        didSet { if let pairing { PairingStore.save(pairing) } }
    }
    private(set) var exchanges: [Exchange] = []
    private(set) var replying = false

    /// What ties this run of exchanges together, on both ends: the Mac
    /// keys its history by it, and cannot work it out for itself.
    ///
    /// It is restored along with the transcript, so reopening the app
    /// continues the conversation rather than starting a stranger. The
    /// Mac drops a conversation after six idle hours, so a transcript
    /// older than that will still read correctly and no longer be
    /// followed — the screen is honest about what was said either way.
    private(set) var conversationID = UUID().uuidString

    @ObservationIgnored private var inFlight: Task<Void, Never>?

    init() {
        pairing = PairingStore.load()

        // Only worth restoring if there is still a machine to continue
        // with. Unpairing clears both.
        if pairing != nil, let transcript = TranscriptStore.load() {
            conversationID = transcript.conversation
            exchanges = transcript.exchanges
        }

        // Review scaffolds, not product surface. The Simulator has no
        // camera worth pointing at a screen and no microphone worth
        // talking into, so the two things that start a conversation are
        // both unavailable there:
        //   -pair '{"v":1,"peer":"…","secret":"…","name":"…","hints":["…"]}'
        //   -say  "is this thing on"
        // Read the raw arguments rather than UserDefaults: the
        // NSArgumentDomain parses a value beginning with "{" as a plist
        // dictionary, so the pairing JSON never arrives as a string.
        if let json = launchArgument("-pair"), let scanned = Pairing.decode(json) {
            pairing = scanned
        }
        if let opening = launchArgument("-say"), !opening.isEmpty {
            Task { @MainActor in
                try? await Task.sleep(for: .milliseconds(300))
                self.send(opening)
            }
        }
    }

    /// Start again. The Mac keys its history on this id, so a new one
    /// is genuinely a fresh conversation on both ends rather than just
    /// a cleared screen.
    func startNew() {
        inFlight?.cancel()
        conversationID = UUID().uuidString
        exchanges = []
        replying = false
        TranscriptStore.clear()
    }

    func unpair() {
        inFlight?.cancel()
        PairingStore.clear()
        TranscriptStore.clear()
        pairing = nil
        exchanges = []
        conversationID = UUID().uuidString
    }

    func send(_ text: String) {
        guard let pairing else { return }
        let said = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !said.isEmpty else { return }

        exchanges.append(Exchange(said: said))
        let index = exchanges.count - 1
        replying = true
        // Saved before the answer as well as after: the app can be
        // killed mid-reply, and losing the question is worse than
        // restoring it marked interrupted.
        persist()

        inFlight?.cancel()
        inFlight = Task { [weak self] in
            let client = Client(pairing: pairing, conversation: self?.conversationID ?? "")
            do {
                for try await frame in client.say(said) {
                    guard let self, !Task.isCancelled else { return }
                    if let delta = frame.delta {
                        exchanges[index].reply += delta
                        // The first real word means the waiting is over.
                        exchanges[index].status = nil
                    }
                    if let status = frame.status { exchanges[index].status = status }
                    if let error = frame.error { exchanges[index].failed = error }
                    if frame.done == true || frame.error != nil { break }
                }
                self?.exchanges[index].done = true
                self?.exchanges[index].status = nil
                self?.persist()
            } catch {
                self?.exchanges[index].failed = error.localizedDescription
                self?.exchanges[index].done = true
                self?.exchanges[index].status = nil
                self?.persist()
            }
            self?.replying = false
        }
    }
}


extension Conversation {
    /// Written on settle rather than on every delta — a token at a time
    /// would be a file write per word.
    fileprivate func persist() {
        TranscriptStore.save(conversation: conversationID, exchanges: exchanges)
    }
}

/// The value following `flag` in the launch arguments, if any.
private func launchArgument(_ flag: String) -> String? {
    let args = ProcessInfo.processInfo.arguments
    guard let i = args.firstIndex(of: flag), i + 1 < args.count else { return nil }
    return args[i + 1]
}
