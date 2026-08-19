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
    /// What is happening while nothing is being said. Replaces whatever
    /// came before it; never part of the answer.
    var status: String?
}

enum ClientError: LocalizedError {
    case unreachable
    case refused
    case broken(String)

    // Spoken, because these end up on screen. "The Mac isn't answering"
    // is a fact about the world; "NSURLErrorDomain -1004" is not.
    var errorDescription: String? {
        switch self {
        case .unreachable: "I can't reach that Mac from here."
        case .refused: "That Mac doesn't recognise this phone any more."
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

    // MARK: - The conversation

    /// One exchange. Frames arrive as they are written, which is the
    /// whole point — the first words should be on screen while the rest
    /// is still being composed.
    func say(_ text: String) -> AsyncThrowingStream<Frame, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    guard let address = await resolve() else {
                        throw ClientError.unreachable
                    }
                    var request = URLRequest(url: URL(string: "http://\(address)/say")!)
                    request.httpMethod = "POST"
                    request.setValue("Bearer \(pairing.secret)", forHTTPHeaderField: "Authorization")
                    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                    request.httpBody = try JSONEncoder().encode([
                        "text": text,
                        "conversation": conversation,
                    ])

                    let (bytes, response) = try await Self.session.bytes(for: request)
                    switch (response as? HTTPURLResponse)?.statusCode {
                    case 200: break
                    case 401: throw ClientError.refused
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
