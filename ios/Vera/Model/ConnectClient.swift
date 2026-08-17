import Foundation

// The typed wire, by hand.
//
// vera serves connectrpc over plain HTTP/1.1, so this needs URLSession
// and nothing else — no gRPC stack, no generated client, no build step
// added to an app that currently has zero dependencies.
//
// Two shapes:
//   · unary — POST JSON, get JSON back.
//   · server stream — POST one *enveloped* request, read a series of
//     enveloped responses. An envelope is a 5-byte prefix (one flag
//     byte, then a big-endian uint32 length) followed by that many
//     bytes of payload. The flag bit 0x02 marks the final envelope,
//     whose payload carries the error, if any.
//
// Note on protojson: 64-bit integers arrive as *strings*, which is why
// the wire types below decode them that way.

struct ConnectError: LocalizedError {
    var code: String
    var message: String
    var errorDescription: String? { message }

    /// Vera's voice, not HTTP's. These lines end up in front of a
    /// person, next to sentences she wrote.
    var spoken: String {
        switch code {
        case "unauthenticated", "permission_denied":
            "the key was refused"
        case "unavailable", "offline":
            message
        default:
            message.isEmpty ? code : message
        }
    }
}

struct ConnectClient: Sendable {
    var base: URL
    var key: String?

    private static let service = "vera.v1.VeraService"

    /// One session for the whole app. A watch stream is idle between
    /// frames — a board can sit still for a long time, and that is not
    /// a failure — so the request timeout is generous rather than the
    /// default sixty seconds.
    private static let session: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 600
        config.timeoutIntervalForResource = .greatestFiniteMagnitude
        config.waitsForConnectivity = false
        config.httpMaximumConnectionsPerHost = 8
        return URLSession(configuration: config)
    }()

    private func request(_ method: String, streaming: Bool) -> URLRequest {
        var r = URLRequest(url: base.appending(path: "\(Self.service)/\(method)"))
        r.httpMethod = "POST"
        r.setValue(streaming ? "application/connect+json" : "application/json",
                   forHTTPHeaderField: "Content-Type")
        r.setValue("1", forHTTPHeaderField: "Connect-Protocol-Version")
        if let key, !key.isEmpty {
            r.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
        }
        return r
    }

    // MARK: - Unary

    func unary<Response: Decodable>(
        _ method: String,
        body: some Encodable = EmptyMessage(),
        as: Response.Type = Response.self
    ) async throws -> Response {
        var r = request(method, streaming: false)
        r.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await Self.session.data(for: r)
        guard let http = response as? HTTPURLResponse else {
            throw ConnectError(code: "unavailable", message: "no answer")
        }
        guard http.statusCode == 200 else {
            throw Self.error(from: data, status: http.statusCode)
        }
        return try JSONDecoder().decode(Response.self, from: data)
    }

    // MARK: - Server streaming

    func stream<Response: Decodable & Sendable>(
        _ method: String,
        body: some Encodable = EmptyMessage(),
        as: Response.Type = Response.self
    ) -> AsyncThrowingStream<Response, Error> {
        // The request is built here, before the stream's closure, so
        // only Sendable values cross into it.
        var built = request(method, streaming: true)
        built.httpBody = Self.envelope((try? JSONEncoder().encode(body)) ?? Data("{}".utf8))
        let r = built

        return AsyncThrowingStream { continuation in
            // Chunks, not bytes. A board frame is a quarter of a
            // megabyte, and URLSession.bytes hands those over one
            // UInt8 at a time — a quarter of a million awaits per
            // frame, for a payload that arrives in a few dozen reads.
            let chunks = Self.chunks(for: r)

            let task = Task {
                var buffer = [UInt8]()
                buffer.reserveCapacity(1 << 18)

                do {
                    for try await chunk in chunks {
                        buffer.append(contentsOf: chunk)
                        let (frames, consumed) = ConnectEnvelope.drain(buffer)
                        if consumed > 0 { buffer.removeFirst(consumed) }

                        for frame in frames {
                            if frame.isEndOfStream {
                                // A payload carrying an `error` means
                                // the stream ended badly.
                                if let end = try? JSONDecoder().decode(EndOfStream.self, from: frame.payload),
                                   let error = end.error {
                                    throw ConnectError(code: error.code ?? "unknown",
                                                       message: error.message ?? "")
                                }
                                continuation.finish()
                                return
                            }
                            continuation.yield(try JSONDecoder().decode(Response.self, from: frame.payload))
                        }
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

    /// The response body as it actually arrives, in whatever sizes the
    /// network hands it over.
    private static func chunks(for request: URLRequest) -> AsyncThrowingStream<Data, Error> {
        AsyncThrowingStream { continuation in
            let collector = ChunkCollector(continuation: continuation)
            let session = URLSession(
                configuration: session.configuration,
                delegate: collector,
                delegateQueue: nil
            )
            let task = session.dataTask(with: request)
            continuation.onTermination = { _ in
                task.cancel()
                session.finishTasksAndInvalidate()
            }
            task.resume()
        }
    }

    private static func envelope(_ payload: Data) -> Data {
        ConnectEnvelope.wrap(payload)
    }

    fileprivate static func error(from data: Data, status: Int) -> ConnectError {
        if let wire = try? JSONDecoder().decode(WireError.self, from: data) {
            return ConnectError(code: wire.code ?? "unknown", message: wire.message ?? "")
        }
        if status == 401 || status == 403 {
            return ConnectError(code: "unauthenticated", message: "the key was refused")
        }
        return ConnectError(code: "unknown", message: "vera answered \(status)")
    }
}

// MARK: - Envelopes on the wire
//
// An envelope is a one-byte flag, a big-endian uint32 length, and that
// many bytes of payload. Chunks arrive at whatever sizes the network
// chose, so the parser has to be indifferent to where the boundaries
// fall — one frame split across ten reads, or ten frames in one.

enum ConnectEnvelope {
    struct Frame: Equatable {
        var flags: UInt8
        var payload: Data
        var isEndOfStream: Bool { flags & 0x02 != 0 }
    }

    static func wrap(_ payload: Data, flags: UInt8 = 0) -> Data {
        var out = Data([flags])
        var length = UInt32(payload.count).bigEndian
        withUnsafeBytes(of: &length) { out.append(contentsOf: $0) }
        out.append(payload)
        return out
    }

    /// Every complete frame at the head of the buffer, and how many
    /// bytes they used. A partial frame at the tail is left alone.
    static func drain(_ buffer: [UInt8]) -> (frames: [Frame], consumed: Int) {
        var frames: [Frame] = []
        var start = 0
        while buffer.count - start >= 5 {
            let length = Int(buffer[start + 1]) << 24
                | Int(buffer[start + 2]) << 16
                | Int(buffer[start + 3]) << 8
                | Int(buffer[start + 4])
            guard buffer.count - start >= 5 + length else { break }
            frames.append(Frame(
                flags: buffer[start],
                payload: Data(buffer[(start + 5)..<(start + 5 + length)])
            ))
            start += 5 + length
        }
        return (frames, start)
    }
}

struct EmptyMessage: Codable, Sendable {}

private struct WireError: Decodable {
    var code: String?
    var message: String?
}

private struct EndOfStream: Decodable {
    var error: WireError?
}

/// Bridges URLSession's delegate callbacks into an async sequence of
/// real chunks, and holds back a non-200 body so the error can be read
/// out of it rather than parsed as envelopes.
private final class ChunkCollector: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private let continuation: AsyncThrowingStream<Data, Error>.Continuation
    private var badStatus: Int?
    private var errorBody = Data()

    init(continuation: AsyncThrowingStream<Data, Error>.Continuation) {
        self.continuation = continuation
    }

    func urlSession(
        _ session: URLSession,
        dataTask: URLSessionDataTask,
        didReceive response: URLResponse
    ) async -> URLSession.ResponseDisposition {
        if let http = response as? HTTPURLResponse, http.statusCode != 200 {
            badStatus = http.statusCode
        }
        return .allow
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        if badStatus != nil { errorBody.append(data) } else { continuation.yield(data) }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: (any Error)?) {
        if let status = badStatus {
            continuation.finish(throwing: ConnectClient.error(from: errorBody, status: status))
        } else if let error, (error as NSError).code != NSURLErrorCancelled {
            continuation.finish(throwing: ConnectError(code: "unavailable", message: "out of reach"))
        } else {
            continuation.finish()
        }
    }
}
