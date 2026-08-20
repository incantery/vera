import Foundation
import Observation

// Talking to Vera Core.
//
// The same wire the phone speaks (cmd/vera2/lan.go), over loopback. The
// one thing this app has that a phone does not is a seat at the machine,
// which is exactly what the loopback-only /pair.json is for: it hands
// out the identity and secret to whoever is sitting here. So pairing is
// one GET, and after that this is just another peer.

struct Pairing: Codable, Sendable {
    let peer: String
    let secret: String
    let name: String
}

/// What /status reports. Mirrors cmd/vera2/lan.go `Status`.
struct CoreStatus: Decodable, Sendable {
    struct Device: Decodable, Sendable, Identifiable {
        struct App: Decodable, Sendable {
            let name: String
            let bundleID: String?
            enum CodingKeys: String, CodingKey { case name, bundleID = "bundle_id" }
        }
        /// What rook shows inside the terminal. Mirrors `TerminalFocus`.
        struct Terminal: Decodable, Sendable {
            let session: String
            let window: String
            let pane: String
            let command: String?
            let title: String?
            let path: String?
            let agent: String?

            /// The same phrase the model reads.
            var describe: String {
                let place = "\(session):\(window)"
                if agent == "claude-code" {
                    let topic = (title ?? "").trimmingCharacters(in: CharacterSet(charactersIn: "✳ "))
                    return topic.isEmpty ? "Claude Code (\(place))" : "Claude Code: \(topic) (\(place))"
                }
                let dir = (path ?? "").split(separator: "/").last.map(String.init) ?? ""
                return "\(command ?? "shell") in \(dir) (\(place))"
            }
        }
        let name: String
        let lastSeen: Date
        let fresh: Bool
        let focus: App?
        let focusSince: Date?
        let terminal: Terminal?
        var id: String { name }
        enum CodingKeys: String, CodingKey { case name, lastSeen = "last_seen", fresh, focus, focusSince = "focus_since", terminal }
    }
    struct Provider: Decodable, Sendable, Identifiable {
        let name: String
        let installed: Bool
        let detail: String?
        let capabilities: [String]
        var id: String { name }
    }
    struct Integration: Decodable, Sendable, Identifiable {
        let name: String
        let connected: Bool
        let lastSeen: Date?
        var id: String { name }
        enum CodingKeys: String, CodingKey { case name, connected, lastSeen = "last_seen" }
    }

    let version: String
    let name: String
    let peer: String
    let mind: String
    let since: Date
    let runsInFlight: Int
    let devices: [Device]
    let providers: [Provider]
    let integrations: [Integration]

    enum CodingKeys: String, CodingKey {
        case version, name, peer, mind, since, devices, providers, integrations
        case runsInFlight = "runs_in_flight"
    }
}

/// One piece of an answer. Mirrors cmd/vera2/transport.go `Frame`.
struct Frame: Decodable, Sendable {
    var delta: String?
    var done: Bool?
    var error: String?
    var run: String?
    var status: String?
}

enum CoreError: LocalizedError {
    case unreachable(String)
    case refused
    case notPaired
    case broken(String)

    var errorDescription: String? {
        switch self {
        case .unreachable(let address): "Vera Core isn't answering at \(address)."
        case .refused: "Vera Core doesn't recognise this app any more."
        case .notPaired: "Not paired with Vera Core yet."
        case .broken(let why): why
        }
    }
}

@Observable
@MainActor
final class Core {
    enum State: Equatable {
        case disconnected(String)
        case connecting
        case connected

        var label: String {
            switch self {
            case .disconnected: "Disconnected"
            case .connecting: "Connecting"
            case .connected: "Connected"
            }
        }
    }

    private(set) var state: State = .disconnected("not started")
    private(set) var status: CoreStatus?
    private(set) var pairing: Pairing?
    private(set) var lastStatusAt: Date?
    private(set) var lastError: String?

    var address: String
    /// The name this Mac reports itself as. Once paired it is the core's
    /// own name for this machine — the rook adapter inside vera2 reports
    /// under that name, and the two must agree or the Mac appears twice.
    var device: String { pairing?.name ?? hostname }
    private let hostname: String

    var isConnected: Bool { state == .connected }

    /// This Mac, as the core sees it.
    var me: CoreStatus.Device? { status?.devices.first { $0.name == device } }

    /// Fires on each (re)connection, so the station can restate what
    /// was true before there was anyone to tell — the current focus,
    /// mostly.
    var onConnected: () -> Void = {}

    @ObservationIgnored private var poll: Task<Void, Never>?
    @ObservationIgnored private let log: EventLog

    nonisolated private static let session: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 120
        config.waitsForConnectivity = false
        return URLSession(configuration: config)
    }()

    nonisolated private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            // Go writes RFC 3339 with nanoseconds; ISO8601DateFormatter
            // without fractional seconds refuses it.
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let d = f.date(from: s) { return d }
            f.formatOptions = [.withInternetDateTime]
            if let d = f.date(from: s) { return d }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "not a date: \(s)"))
        }
        return d
    }()

    nonisolated private static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        return e
    }()

    init(address: String, log: EventLog) {
        self.address = address
        self.log = log
        var host = ProcessInfo.processInfo.hostName
        if let dot = host.firstIndex(of: ".") { host = String(host[..<dot]) }
        hostname = host.isEmpty ? "this Mac" : host
        if let data = UserDefaults.standard.data(forKey: "core.pairing"),
           let p = try? JSONDecoder().decode(Pairing.self, from: data) {
            pairing = p
        }
    }

    // MARK: - Lifecycle

    /// Holds one /watch stream open and is TOLD when anything changes.
    /// Pairs first if needed; reconnects with a short backoff; a stream
    /// that ends is a disconnection, whatever the reason.
    func start() {
        poll?.cancel()
        poll = Task { [weak self] in
            var backoff: Duration = .seconds(1)
            while !Task.isCancelled {
                guard let self else { return }
                if pairing == nil {
                    state = .connecting
                    do {
                        try await pair()
                    } catch {
                        fail(error)
                        try? await Task.sleep(for: backoff)
                        continue
                    }
                }
                do {
                    try await watch()
                    // Ended cleanly (server went away); reconnect promptly.
                    backoff = .seconds(1)
                    fail(CoreError.unreachable(address))
                } catch CoreError.refused {
                    pairing = nil
                    UserDefaults.standard.removeObject(forKey: "core.pairing")
                    fail(CoreError.refused)
                } catch {
                    fail(error)
                }
                try? await Task.sleep(for: backoff)
                backoff = min(backoff * 2, .seconds(5))
            }
        }
    }

    func stop() {
        poll?.cancel()
        poll = nil
    }

    /// One open stream of Status snapshots. Returns when the server
    /// closes it; throws when it could not be opened or broke.
    private func watch() async throws {
        var request = try authed("/watch?device=\(device)")
        request.timeoutInterval = 60 * 60 * 24
        let (bytes, response): (URLSession.AsyncBytes, URLResponse)
        do {
            (bytes, response) = try await Self.session.bytes(for: request)
        } catch {
            throw CoreError.unreachable(address)
        }
        switch (response as? HTTPURLResponse)?.statusCode ?? 0 {
        case 200: break
        case 401: throw CoreError.refused
        case let code: throw CoreError.broken("Vera Core answered \(code) to /watch.")
        }
        do {
            for try await line in bytes.lines {
                guard !line.isEmpty else { continue }
                let s = try Self.decoder.decode(CoreStatus.self, from: Data(line.utf8))
                status = s
                lastStatusAt = Date()
                lastError = nil
                if state != .connected {
                    state = .connected
                    log.info("Vera Core connected — \(s.name), v\(s.version), \(s.mind)")
                    onConnected()
                }
            }
        } catch is CancellationError {
            return
        } catch {
            throw CoreError.unreachable(address)
        }
    }

    private func fail(_ error: Error) {
        let why = error.localizedDescription
        if state != .disconnected(why) {
            log.error(why)
        }
        state = .disconnected(why)
        lastError = why
    }

    private func pair() async throws {
        var request = URLRequest(url: url("/pair.json"))
        request.timeoutInterval = 3
        let (data, response) = try await fetch(request)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else {
            throw CoreError.broken("Vera Core refused to pair — is this really loopback?")
        }
        let p = try JSONDecoder().decode(Pairing.self, from: data)
        pairing = p
        UserDefaults.standard.set(try JSONEncoder().encode(p), forKey: "core.pairing")
        log.info("Paired with \(p.name) (\(p.peer))")
    }

    // MARK: - Requests

    private func url(_ path: String) -> URL {
        URL(string: "http://\(address)\(path)")!
    }

    private func authed(_ path: String, method: String = "GET", body: Data? = nil) throws -> URLRequest {
        guard let pairing else { throw CoreError.notPaired }
        var request = URLRequest(url: url(path))
        request.httpMethod = method
        request.setValue("Bearer \(pairing.secret)", forHTTPHeaderField: "Authorization")
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        return request
    }

    private func fetch(_ request: URLRequest) async throws -> (Data, URLResponse) {
        do {
            return try await Self.session.data(for: request)
        } catch {
            throw CoreError.unreachable(address)
        }
    }

    private func get<T: Decodable>(_ path: String) async throws -> T {
        var request = try authed(path)
        request.timeoutInterval = 4
        let (data, response) = try await fetch(request)
        switch (response as? HTTPURLResponse)?.statusCode ?? 0 {
        case 200: return try Self.decoder.decode(T.self, from: data)
        case 401: throw CoreError.refused
        case let code: throw CoreError.broken("Vera Core answered \(code) to \(path).")
        }
    }

    /// Fire-and-forget: an observation is about a moment already past.
    func observe(_ event: ContextEvent) {
        guard pairing != nil else { return }
        Task { [weak self] in
            guard let self else { return }
            do {
                let request = try authed("/observe", method: "POST", body: try Self.encoder.encode(event))
                let (_, response) = try await fetch(request)
                let code = (response as? HTTPURLResponse)?.statusCode ?? 0
                if code == 401 { throw CoreError.refused }
                if code != 204 { throw CoreError.broken("observe answered \(code)") }
            } catch {
                log.error("observe \(event.type): \(error.localizedDescription)")
            }
        }
    }

    /// What /dictate gives back: the words, and whether the model was
    /// skipped.
    struct Cleaned: Decodable, Sendable {
        let text: String
        let raw: Bool?
    }

    /// Tidies dictation for the cursor. Bounded: past the budget the
    /// caller types the raw words, so the cursor never waits on a model.
    func dictate(_ text: String, app: FocusedApp?) async throws -> Cleaned {
        struct Body: Encodable {
            let text: String
            let device: String
            let app: ContextEvent.App?
        }
        let body = try Self.encoder.encode(Body(text: text, device: device, app: app.map { ContextEvent.App(name: $0.name, bundleID: $0.bundleID) }))
        var request = try authed("/dictate", method: "POST", body: body)
        request.timeoutInterval = 3
        let (data, response) = try await fetch(request)
        switch (response as? HTTPURLResponse)?.statusCode ?? 0 {
        case 200: return try Self.decoder.decode(Cleaned.self, from: data)
        case 401: throw CoreError.refused
        case let code: throw CoreError.broken("dictate answered \(code)")
        }
    }

    /// One exchange, streamed. The first words should be on screen while
    /// the rest is still being composed.
    func say(_ text: String, conversation: String) -> AsyncThrowingStream<Frame, Error> {
        // Built here, on the actor, so the stream's task needs nothing
        // of this object but a request and an address for the error.
        let prepared = Result {
            try authed("/say", method: "POST", body: try JSONEncoder().encode(["text": text, "conversation": conversation, "device": device]))
        }
        let address = self.address
        return AsyncThrowingStream { continuation in
            let task = Task.detached {
                do {
                    let request = try prepared.get()
                    let (bytes, response) = try await Self.session.bytes(for: request)
                    switch (response as? HTTPURLResponse)?.statusCode ?? 0 {
                    case 200: break
                    case 401: throw CoreError.refused
                    case let code: throw CoreError.broken("Vera Core answered \(code).")
                    }
                    for try await line in bytes.lines {
                        guard !line.isEmpty else { continue }
                        let frame = try Self.decoder.decode(Frame.self, from: Data(line.utf8))
                        continuation.yield(frame)
                        if frame.done == true || frame.error != nil { break }
                    }
                    continuation.finish()
                } catch let error as CoreError {
                    continuation.finish(throwing: error)
                } catch is CancellationError {
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: CoreError.unreachable(address))
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }
}
