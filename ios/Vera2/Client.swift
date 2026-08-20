import Foundation

// Talking to the machine.
//
// Two jobs, and they are separate on purpose: finding the Mac, and
// having a conversation with it. Finding it is guesswork that changes
// every time you join a network; the conversation is the same forever.

struct Frame: Decodable, Sendable {
    var delta: String?
    var done: Bool?
    var error: String?
    /// Names the work on the Mac. Arrives first, and is what a phone
    /// that lost the connection reattaches to.
    var run: String?
    /// What is happening while nothing is being said. Replaces whatever
    /// came before it; never part of the answer.
    var status: String?
}

enum ClientError: LocalizedError {
    case unreachable
    case refused
    case gone
    case broken(String)

    // Spoken, because these end up on screen. "The Mac isn't answering"
    // is a fact about the world; "NSURLErrorDomain -1004" is not.
    var errorDescription: String? {
        switch self {
        case .unreachable: "I can't reach that Mac from here."
        case .refused: "That Mac doesn't recognise this phone any more."
        case .gone: "That was too long ago — the Mac isn't holding it any more."
        case .broken(let why): why
        }
    }
}

struct Client: Sendable {
    let pairing: Pairing
    let conversation: String

    private static let session: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        // A person talking is slow, and a model thinking is slower.
        config.timeoutIntervalForRequest = 120
        config.waitsForConnectivity = false
        return URLSession(configuration: config)
    }()

    // MARK: - Finding the machine

    /// Candidate addresses, best guess first. The one that worked last
    /// time is tried before the ones the QR code suggested, because a
    /// pairing may be months old and the cache is minutes old.
    private var candidates: [String] {
        var seen = Set<String>()
        var out: [String] = []
        for address in [PairingStore.lastGood].compactMap({ $0 }) + (pairing.hints ?? []) {
            if seen.insert(address).inserted { out.append(address) }
        }
        return out
    }

    /// Ask each candidate who it is. /ping needs no secret and answers
    /// instantly, so a wrong address costs a timeout rather than a
    /// failed conversation — and an address that answers with someone
    /// else's peer id is a different machine that happens to be at the
    /// address ours used to have.
    func resolve() async -> String? {
        for address in candidates {
            guard let url = URL(string: "http://\(address)/ping") else { continue }
            var request = URLRequest(url: url)
            request.timeoutInterval = 2

            guard let (data, response) = try? await Self.session.data(for: request),
                  (response as? HTTPURLResponse)?.statusCode == 200,
                  let who = try? JSONDecoder().decode([String: String].self, from: data),
                  who["peer"] == pairing.peer
            else { continue }

            PairingStore.lastGood = address
            return address
        }
        return nil
    }

    // MARK: - What the Mac is looking at

    /// Pushed status: one now, one on every change. Mirrors the fields
    /// the phone reads out of vera2's `Status`; the rest is ignored.
    struct Status: Decodable, Sendable {
        struct Device: Decodable, Sendable {
            let name: String
            let focus: Target.App?
            let terminal: Target.Terminal?
        }
        let devices: [Device]
    }

    func watch() -> AsyncThrowingStream<Status, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    guard let address = await resolve() else { throw ClientError.unreachable }
                    var parts = URLComponents(string: "http://\(address)/watch")!
                    parts.queryItems = [URLQueryItem(name: "device", value: "phone")]
                    var request = URLRequest(url: parts.url!)
                    request.timeoutInterval = 60 * 60 * 24
                    request.setValue("Bearer \(pairing.secret)", forHTTPHeaderField: "Authorization")
                    let (bytes, response) = try await Self.session.bytes(for: request)
                    switch (response as? HTTPURLResponse)?.statusCode {
                    case 200: break
                    case 401: throw ClientError.refused
                    case let code?: throw ClientError.broken("The Mac answered with \(code).")
                    case nil: throw ClientError.broken("The Mac answered with nothing at all.")
                    }
                    for try await line in bytes.lines {
                        guard !line.isEmpty, let status = try? JSONDecoder().decode(Status.self, from: Data(line.utf8)) else { continue }
                        continuation.yield(status)
                    }
                    continuation.finish()
                } catch is CancellationError {
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    /// Whether the Mac has a speech engine ready.
    struct STT: Decodable, Sendable {
        let engine: String
        let uv: Bool?
        let installed: Bool?
        let modelReady: Bool?
        let ready: Bool?
        let detail: String?
        let installing: Bool?
        enum CodingKeys: String, CodingKey { case engine, uv, installed, modelReady = "model_ready", ready, detail, installing }
    }

    func sttStatus() async throws -> STT {
        guard let address = await resolve() else { throw ClientError.unreachable }
        var request = URLRequest(url: URL(string: "http://\(address)/stt")!)
        request.timeoutInterval = 5
        request.setValue("Bearer \(pairing.secret)", forHTTPHeaderField: "Authorization")
        let (data, response) = try await Self.session.data(for: request)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw ClientError.broken("The Mac would not report its speech engine.") }
        return try JSONDecoder().decode(STT.self, from: data)
    }

    /// Kick off the managed install and stream its progress lines.
    func installSTT() -> AsyncThrowingStream<Frame, Error> {
        stream { address in
            var request = URLRequest(url: URL(string: "http://\(address)/stt/install")!)
            request.httpMethod = "POST"
            request.timeoutInterval = 20 * 60
            return request
        }
    }

    /// Send recorded audio; get back what was said. The Mac recognises.
    func transcribe(_ audio: URL) async throws -> String {
        guard let address = await resolve() else { throw ClientError.unreachable }
        var request = URLRequest(url: URL(string: "http://\(address)/transcribe")!)
        request.httpMethod = "POST"
        request.timeoutInterval = 120
        request.setValue("Bearer \(pairing.secret)", forHTTPHeaderField: "Authorization")
        request.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        let data = try Data(contentsOf: audio)
        let (out, response) = try await Self.session.upload(for: request, from: data)
        switch (response as? HTTPURLResponse)?.statusCode {
        case 200:
            struct R: Decodable { let text: String }
            return try JSONDecoder().decode(R.self, from: out).text
        case 401: throw ClientError.refused
        case 503: throw ClientError.broken("Speech-to-text isn't installed on the Mac yet.")
        case let code?: throw ClientError.broken("The Mac answered \(code) to transcribe.")
        case nil: throw ClientError.broken("The Mac answered with nothing at all.")
        }
    }

    /// Put words into the pane the Mac is looking at. Enter only if
    /// asked; a pane that is not a coding agent is refused by the Mac.
    func type(_ text: String, clean: Bool, enter: Bool) async throws -> Typed {
        guard let address = await resolve() else { throw ClientError.unreachable }
        var request = URLRequest(url: URL(string: "http://\(address)/type")!)
        request.httpMethod = "POST"
        request.timeoutInterval = 10
        request.setValue("Bearer \(pairing.secret)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        struct Body: Encodable { let text: String; let clean: Bool; let enter: Bool; let device: String }
        request.httpBody = try JSONEncoder().encode(Body(text: text, clean: clean, enter: enter, device: "phone"))
        let (data, response) = try await Self.session.data(for: request)
        switch (response as? HTTPURLResponse)?.statusCode {
        case 200: return try JSONDecoder().decode(Typed.self, from: data)
        case 401: throw ClientError.refused
        case 409: throw ClientError.broken(String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines))
        case 501: throw ClientError.broken("That Mac has nothing to type into — rook isn't running there.")
        case let code?: throw ClientError.broken("The Mac answered with \(code).")
        case nil: throw ClientError.broken("The Mac answered with nothing at all.")
        }
    }

    // MARK: - The conversation

    /// One exchange. Frames arrive as they are written, which is the
    /// whole point — the first words should be on screen while the rest
    /// is still being composed.
    ///
    /// Two routes to the same Mac. The network is tried first because
    /// it is fast and already warm when you are at home; peer-to-peer
    /// is the answer when the network refuses to carry the traffic,
    /// which is the normal state of a hotel or a guest wifi.
    func say(_ text: String) -> AsyncThrowingStream<Frame, Error> {
        route(
            peer: .say(text, in: conversation),
            lan: stream { address in
            var request = URLRequest(url: URL(string: "http://\(address)/say")!)
            request.httpMethod = "POST"
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONEncoder().encode([
                "text": text,
                "conversation": self.conversation,
            ])
            return request
            }
        )
    }

    /// Rejoin work already under way on the Mac, skipping the frames
    /// this phone has already seen. The Mac keeps a finished run for a
    /// while, so an answer produced while the app was closed is still
    /// waiting when it opens.
    func resume(run: String, from seen: Int) -> AsyncThrowingStream<Frame, Error> {
        route(
            peer: .resume(run, from: seen),
            lan: stream { address in
                var parts = URLComponents(string: "http://\(address)/resume")!
                parts.queryItems = [
                    URLQueryItem(name: "run", value: run),
                    URLQueryItem(name: "from", value: String(seen)),
                ]
                return URLRequest(url: parts.url!)
            }
        )
    }

    /// Try the network, and go around it if it will not carry the
    /// traffic. Only `unreachable` falls through: a Mac that answered
    /// and refused the secret would refuse it over the radio too, and
    /// retrying would turn one clear error into two slow ones.
    private func route(peer: PeerRequest,
                       lan: AsyncThrowingStream<Frame, Error>)
    -> AsyncThrowingStream<Frame, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                var delivered = false
                do {
                    for try await frame in lan {
                        delivered = true
                        continuation.yield(frame)
                    }
                    continuation.finish()
                    return
                } catch ClientError.unreachable where !delivered {
                    // Nothing was said over the network, so nothing is
                    // repeated by going around it.
                } catch {
                    continuation.finish(throwing: error)
                    return
                }
                do {
                    for try await frame in PeerLink.exchange(with: pairing, request: peer) {
                        continuation.yield(frame)
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    private func stream(_ build: @escaping @Sendable (String) throws -> URLRequest) -> AsyncThrowingStream<Frame, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    guard let address = await resolve() else {
                        throw ClientError.unreachable
                    }
                    var request = try build(address)
                    request.setValue("Bearer \(pairing.secret)", forHTTPHeaderField: "Authorization")

                    let (bytes, response) = try await Self.session.bytes(for: request)
                    switch (response as? HTTPURLResponse)?.statusCode {
                    case 200: break
                    case 401: throw ClientError.refused
                    case 404: throw ClientError.gone
                    case let code?: throw ClientError.broken("The Mac answered with \(code).")
                    case nil: throw ClientError.broken("The Mac answered with nothing at all.")
                    }

                    // .lines is per-byte underneath, which the prototype
                    // could not afford against 250KB board frames. Here
                    // a frame is a word, so the cost is nothing and the
                    // convenience is exactly right.
                    for try await line in bytes.lines {
                        guard !line.isEmpty,
                              let frame = try? JSONDecoder().decode(Frame.self, from: Data(line.utf8))
                        else { continue }
                        continuation.yield(frame)
                        if frame.done == true || frame.error != nil { break }
                    }
                    continuation.finish()
                } catch is CancellationError {
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }
}
