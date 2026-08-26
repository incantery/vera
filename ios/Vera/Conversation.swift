import Foundation
import Observation

// The conversation.
//
// The Mac holds what was said (history) and what is true about you
// (memory). This holds neither — only what is on screen, and only so
// that reopening the app does not look like nothing ever happened.
//
// What an exchange is made of lives in Reply.swift: this is the part
// that talks to the Mac, and the part that decides when what is on
// screen is worth writing down.

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
        rejoinUnfinished()

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
                    exchanges[index].apply(frame)
                    // Saved the moment the run is known. Waiting until
                    // the exchange settles would mean the one case this
                    // exists for — the app dying mid-answer — is the
                    // one case with nothing to rejoin. A question is
                    // saved for the same reason: it is the one thing on
                    // screen that is worth coming back to.
                    if frame.run != nil || frame.ask != nil { persist() }
                    if frame.done == true || frame.error != nil { break }
                }
                self?.exchanges[index].close()
                self?.persist()
            } catch {
                self?.exchanges[index].failed = error.localizedDescription
                self?.exchanges[index].close()
                self?.persist()
            }
            self?.replying = false
        }
    }

    /// One word — yes, no or always — back to the call parked on it.
    ///
    /// The card closes on the tap, not on the reply: the answer is
    /// theirs the moment they give it. If it does not reach the Mac the
    /// question opens again with the reason under it, because the
    /// exchange on the other end is still waiting for one.
    func answer(_ choice: String, to questionID: String, in exchangeID: Exchange.ID) {
        guard let pairing else { return }
        guard let index = exchanges.firstIndex(where: { $0.id == exchangeID }) else { return }
        guard exchanges[index].answering(questionID, choice) else { return }
        persist()

        let client = Client(pairing: pairing, conversation: conversationID)
        Task { @MainActor [weak self] in
            do {
                try await client.answer(choice, to: questionID)
            } catch {
                guard let self, let index = exchanges.firstIndex(where: { $0.id == exchangeID }) else { return }
                exchanges[index].answerFailed(questionID, error.localizedDescription)
                persist()
            }
        }
    }
}


extension Conversation {

    /// Anything that was still running when the app went away is
    /// probably still running now — the Mac does not stop because a
    /// phone did. Rejoin it, skipping what was already read.
    ///
    /// TranscriptStore marks interrupted exchanges finished so the
    /// screen never lies while this is in flight; a successful rejoin
    /// simply overwrites that with the truth.
    func rejoinUnfinished() {
        guard let pairing else { return }
        for (index, exchange) in exchanges.enumerated() {
            guard let run = exchange.run, exchange.failed == "Interrupted." else { continue }
            let client = Client(pairing: pairing, conversation: conversationID)
            let seen = exchange.seen
            Task { @MainActor [weak self] in
                do {
                    for try await frame in client.resume(run: run, from: seen) {
                        guard let self, index < exchanges.count else { return }
                        // It was not interrupted after all.
                        exchanges[index].failed = nil
                        exchanges[index].done = false
                        exchanges[index].apply(frame)
                        if frame.ask != nil { persist() }
                        if frame.done == true || frame.error != nil { break }
                    }
                    self?.exchanges[index].close()
                    self?.persist()
                } catch {
                    // The run is gone, or the Mac is. The exchange stays
                    // marked interrupted, which is what it was.
                }
            }
        }
    }

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
