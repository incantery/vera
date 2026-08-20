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
    struct App: Decodable, Sendable, Equatable {
        let name: String
    }
    struct Terminal: Decodable, Sendable, Equatable {
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

struct Typed: Decodable, Sendable {
    let text: String
    let raw: Bool?
    let enter: Bool?
}

@Observable
@MainActor
final class TerminalLink {
    private(set) var target: Target?
    private(set) var watching = false
    private(set) var problem: String?
    /// The last thing typed, for the screen.
    private(set) var lastTyped: String?
    private(set) var lastRaw = false
    private(set) var busy = false

    @ObservationIgnored private var watcher: Task<Void, Never>?
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

    /// Type the words into the focused pane. Cleaned on the Mac; not
    /// sent.
    func type(_ text: String) async {
        busy = true
        defer { busy = false }
        do {
            let typed = try await client.type(text, clean: true, enter: false)
            lastTyped = typed.text
            lastRaw = typed.raw ?? false
            problem = nil
        } catch {
            problem = error.localizedDescription
        }
    }

    /// Press Enter in the pane: send what has been typed.
    func send() async {
        busy = true
        defer { busy = false }
        do {
            _ = try await client.type("", clean: false, enter: true)
            lastTyped = nil
            problem = nil
        } catch {
            problem = error.localizedDescription
        }
    }
}
