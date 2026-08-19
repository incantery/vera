import Foundation

// What was on screen last time.
//
// Memory lives on the Mac and survives everything; this is the much
// smaller matter of the phone not looking like nothing ever happened.
// Closing the app and reopening it to a blank screen — while the Mac is
// still holding the conversation under an id nothing points at any more
// — is the difference between a thing you use and a thing you demo.
//
// A file rather than UserDefaults: a transcript grows, and UserDefaults
// is a plist read into memory at launch.

struct Transcript: Codable, Sendable {
    var conversation: String
    var exchanges: [Exchange]
    var saved: Date
}

enum TranscriptStore {

    /// Enough to scroll back through, not enough to become a database.
    /// The Mac's own window is far shorter, so this is scrollback
    /// rather than context.
    private static let keep = 100

    private static var url: URL? {
        guard let dir = try? FileManager.default.url(
            for: .applicationSupportDirectory, in: .userDomainMask,
            appropriateFor: nil, create: true
        ) else { return nil }
        return dir.appendingPathComponent("transcript.json")
    }

    static func save(conversation: String, exchanges: [Exchange]) {
        guard let url else { return }
        let kept = exchanges.suffix(keep).map { exchange -> Exchange in
            // Never persist a wait. Whatever was in flight is over —
            // the process is gone — and storing it as unfinished would
            // restore a screen that thinks forever.
            var e = exchange
            e.status = nil
            return e
        }
        let transcript = Transcript(conversation: conversation, exchanges: Array(kept), saved: Date())
        guard let data = try? JSONEncoder().encode(transcript) else { return }
        try? data.write(to: url, options: .atomic)
    }

    static func load() -> Transcript? {
        guard let url, let data = try? Data(contentsOf: url),
              var transcript = try? JSONDecoder().decode(Transcript.self, from: data)
        else { return nil }

        // An exchange interrupted by the app going away is finished
        // now, one way or another. Partial words are kept — they are
        // what was actually seen — and a reply that never started says
        // so rather than pretending to still be coming.
        transcript.exchanges = transcript.exchanges.map { exchange in
            guard !exchange.done else { return exchange }
            var e = exchange
            e.done = true
            e.status = nil
            if e.reply.isEmpty && e.failed == nil {
                e.failed = "Interrupted."
            }
            return e
        }
        return transcript
    }

    static func clear() {
        guard let url else { return }
        try? FileManager.default.removeItem(at: url)
    }
}
