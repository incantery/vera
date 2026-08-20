import Foundation
import Observation

// What the Mac is looking at, and typing into it.
//
// The Mac keeps an attention model — which application is in front of
// the person, and inside the terminal, which rook pane. /watch pushes
// it here the moment it changes, so the phone can say where the words
// will land BEFORE they are sent. Typing goes to that pane by way of
// /type: cleaned, and never followed by Enter unless the person asks,
// because words typed into a coding agent can still be read; words
// sent cannot be unsent.
//
// LAN only for now: the peer link speaks say/resume and nothing else,
// and this is a thing you do at home, pacing.

struct Target: Decodable, Sendable, Equatable {
    struct App: Codable, Sendable, Equatable {
        let name: String
    }
    struct Terminal: Codable, Sendable, Equatable {
        let session: String
        let window: String
        let pane: String
        let command: String?
        let title: String?
        let path: String?
        let agent: String?

        var describe: String {
            let place = "\(session):\(window)"
            if agent == "claude-code" {
                let topic = (title ?? "").trimmingCharacters(in: CharacterSet(charactersIn: "✳ "))
                return topic.isEmpty ? "Claude Code (\(place))" : "Claude Code · \(topic)"
            }
            let dir = (path ?? "").split(separator: "/").last.map(String.init) ?? ""
            return "\(command ?? "shell") in \(dir) (\(place))"
        }
        var isAgent: Bool { agent != nil && !(agent ?? "").isEmpty }
    }

    let focus: App?
    let terminal: Terminal?
}

/// One place the person can jump to, from the Mac's frecency ranking.
struct RankedTarget: Decodable, Identifiable, Sendable, Equatable {
    let key: String
    let kind: String       // "app" or "pane"
    let label: String
    let score: Double
    let bundleID: String?
    let terminal: Target.Terminal?
    let current: Bool?
    var id: String { key }
    enum CodingKeys: String, CodingKey { case key, kind, label, score, bundleID = "bundle_id", terminal, current }
}

struct Typed: Decodable, Sendable {
    let text: String
    let raw: Bool?
    let enter: Bool?
}

@Observable
@MainActor
final class TerminalLink {
    private(set) var target: Target?
    private(set) var targets: [RankedTarget] = []
    /// When set, everything types HERE regardless of what the Mac is
    /// looking at — the phone having chosen one specific window.
    var pinned: RankedTarget?
    private(set) var going: String?
    private(set) var watching = false
    private(set) var problem: String?
    /// Everything typed this session, in order, for the screen.
    private(set) var transcript = ""
    private(set) var lastRaw = false
    private(set) var busy = false

    @ObservationIgnored private var watcher: Task<Void, Never>?
    // Chunks are typed one at a time, in the order they were spoken. Two
    // chunks racing to /type would clobber each other's result and lose
    // one, which is a whole sentence gone.
    @ObservationIgnored private var queue: [String] = []
    @ObservationIgnored private var draining = false
    private let client: Client

    init(client: Client) {
        self.client = client
    }

    func start() {
        watcher?.cancel()
        watcher = Task { [weak self] in
            var backoff: Duration = .seconds(1)
            while !Task.isCancelled, let self {
                do {
                    for try await status in client.watch() {
                        watching = true
                        problem = nil
                        backoff = .seconds(1)
                        // This Mac's own device is the one that matches
                        // the pairing name.
                        target = status.devices.first { $0.name == client.pairing.name }
                            .map { Target(focus: $0.focus, terminal: $0.terminal) }
                        targets = status.targets ?? []
                    }
                } catch {
                    problem = error.localizedDescription
                }
                watching = false
                try? await Task.sleep(for: backoff)
                backoff = min(backoff * 2, .seconds(8))
            }
        }
    }

    func stop() {
        watcher?.cancel()
        watcher = nil
        watching = false
    }

    /// Whether anything is typed and waiting to be sent.
    var hasText: Bool { !transcript.isEmpty || !queue.isEmpty }

    /// Enqueue a spoken chunk. Returns at once; the chunk is typed in
    /// its turn. Ordering is the point — see `queue`.
    func type(_ chunk: String) {
        let chunk = chunk.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !chunk.isEmpty else { return }
        queue.append(chunk)
        drain()
    }

    private func drain() {
        guard !draining else { return }
        draining = true
        Task { [weak self] in
            guard let self else { return }
            while !queue.isEmpty {
                let chunk = queue.removeFirst()
                busy = true
                do {
                    let typed = try await client.type(chunk, clean: true, enter: false, at: pinned?.terminal)
                    transcript += (transcript.isEmpty ? "" : " ") + typed.text
                    lastRaw = typed.raw ?? false
                    problem = nil
                } catch {
                    // Put it back at the front and stop, so nothing is
                    // typed out of order after a failure, and the words
                    // are not lost — the person sees why and can retry.
                    queue.insert(chunk, at: 0)
                    problem = error.localizedDescription
                    break
                }
            }
            busy = false
            draining = false
        }
    }

    /// Press Enter in the pane: send what has been typed. Only once the
    /// queue has drained, so nothing is sent half-typed.
    func send() async {
        guard queue.isEmpty else { problem = "Still typing — try Send again in a moment."; return }
        busy = true
        defer { busy = false }
        do {
            _ = try await client.type("", clean: false, enter: true, at: pinned?.terminal)
            transcript = ""
            problem = nil
        } catch {
            problem = error.localizedDescription
        }
    }

    /// Bring a place to the front on the Mac.
    func goto(_ t: RankedTarget) async {
        going = t.key
        defer { going = nil }
        do { try await client.goto(t); problem = nil }
        catch { problem = error.localizedDescription }
    }

    /// Forget the current transcript without sending — for a fresh start.
    func clear() { transcript = ""; queue.removeAll() }

    /// Surface a problem from outside (a failed transcription).
    func note(_ text: String) { problem = text }
}
