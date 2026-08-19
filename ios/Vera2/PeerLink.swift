import Foundation
import Network

// Reaching the Mac when the network will not carry it.
//
// Hotel and conference access points routinely isolate clients from
// each other: the phone and the Mac are on the same wifi and cannot
// exchange a packet. No amount of retrying fixes that, because the
// access point is doing it on purpose. Peer-to-peer goes around it —
// AWDL, with Bluetooth to find each other — and involves the network
// not at all.
//
// The pairing code already carried an identity rather than an address,
// which is what makes this a different route to the same machine
// rather than a second pairing. The peer id travels in the Bonjour TXT
// record, so this can tell which Mac it has found before saying
// anything to it.

/// What a phone says over the direct link. Typed rather than a
/// dictionary so it can cross between tasks — `[String: Any]` is not
/// Sendable, and Swift 6 is right to object.
struct PeerRequest: Encodable, Sendable {
    var secret = ""
    var op: String
    var message: Said?
    var run: String?
    var from: Int?

    struct Said: Encodable, Sendable {
        var text: String
        var conversation: String
    }

    static func say(_ text: String, in conversation: String) -> PeerRequest {
        PeerRequest(op: "say", message: Said(text: text, conversation: conversation))
    }

    static func resume(_ run: String, from seen: Int) -> PeerRequest {
        PeerRequest(op: "resume", run: run, from: seen)
    }
}

/// Length-prefixed JSON, both directions. Not HTTP: there is no
/// URLSession over an NWConnection, and a link with no status codes or
/// headers should not be made to pretend it has them.
enum PeerLink {

    private static let service = "_vera._tcp"

    /// One request, then frames until the run ends.
    static func exchange(with pairing: Pairing, request: PeerRequest) -> AsyncThrowingStream<Frame, Error> {
        AsyncThrowingStream { continuation in
            let session = PeerSession(pairing: pairing, request: request, continuation: continuation)
            continuation.onTermination = { _ in session.stop() }
            session.start()
        }
    }

    fileprivate static var browseParameters: NWParameters {
        let parameters = NWParameters.tcp
        parameters.includePeerToPeer = true
        return parameters
    }

    fileprivate static var descriptor: NWBrowser.Descriptor {
        .bonjourWithTXTRecord(type: service, domain: nil)
    }
}

/// One attempt: browse, pick the machine we are paired with, connect,
/// ask, and stream what comes back.
private final class PeerSession: @unchecked Sendable {
    private let pairing: Pairing
    private let request: PeerRequest
    private let continuation: AsyncThrowingStream<Frame, Error>.Continuation
    private let queue = DispatchQueue(label: "vera.peer")

    private var browser: NWBrowser?
    private var connection: NWConnection?
    private var buffer = Data()
    private var finished = false
    private var timeout: DispatchWorkItem?

    init(pairing: Pairing, request: PeerRequest,
         continuation: AsyncThrowingStream<Frame, Error>.Continuation) {
        self.pairing = pairing
        self.request = request
        self.continuation = continuation
    }

    func start() {
        // Peer discovery is not instant — the radios have to find each
        // other — but it must not hang forever either.
        let giveUp = DispatchWorkItem { [weak self] in
            self?.fail(ClientError.unreachable)
        }
        timeout = giveUp
        queue.asyncAfter(deadline: .now() + 20, execute: giveUp)

        let browser = NWBrowser(for: PeerLink.descriptor, using: PeerLink.browseParameters)
        self.browser = browser
        browser.browseResultsChangedHandler = { [weak self] results, _ in
            self?.consider(results)
        }
        browser.stateUpdateHandler = { [weak self] state in
            if case .failed(let error) = state {
                self?.fail(ClientError.broken("Can't look for nearby Macs: \(error.localizedDescription)"))
            }
        }
        browser.start(queue: queue)
    }

    /// Names are for people and are not unique. The peer id from the
    /// pairing code is the identity, and it rides in TXT so this can be
    /// decided before any secret is sent.
    private func consider(_ results: Set<NWBrowser.Result>) {
        guard connection == nil else { return }
        for result in results {
            guard case .bonjour(let txt) = result.metadata,
                  txt["peer"] == pairing.peer
            else { continue }
            connect(to: result.endpoint)
            return
        }
    }

    private func connect(to endpoint: NWEndpoint) {
        browser?.cancel()
        browser = nil

        let connection = NWConnection(to: endpoint, using: PeerLink.browseParameters)
        self.connection = connection
        connection.stateUpdateHandler = { [weak self] state in
            switch state {
            case .ready: self?.ask()
            case .failed(let error):
                self?.fail(ClientError.broken("Lost the direct link: \(error.localizedDescription)"))
            case .cancelled:
                self?.fail(ClientError.unreachable)
            default: break
            }
        }
        connection.start(queue: queue)
    }

    private func ask() {
        var body = request
        body.secret = pairing.secret
        guard let payload = try? JSONEncoder().encode(body) else {
            fail(ClientError.broken("Couldn't put the message together."))
            return
        }
        var framed = Data()
        var length = UInt32(payload.count).bigEndian
        withUnsafeBytes(of: &length) { framed.append(contentsOf: $0) }
        framed.append(payload)

        connection?.send(content: framed, completion: .contentProcessed { [weak self] error in
            if let error {
                self?.fail(ClientError.broken("Couldn't send that: \(error.localizedDescription)"))
                return
            }
            self?.receive()
        })
    }

    private func receive() {
        connection?.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { [weak self] data, _, isComplete, error in
            guard let self else { return }
            if let error {
                fail(ClientError.broken("The link dropped: \(error.localizedDescription)"))
                return
            }
            if let data, !data.isEmpty {
                buffer.append(data)
                drain()
            }
            if isComplete {
                // The Mac finished talking. Whatever was yielded stands.
                done()
                return
            }
            if !finished { receive() }
        }
    }

    /// Four bytes of big-endian length, then that many bytes of JSON,
    /// as many times as the buffer holds.
    private func drain() {
        while buffer.count >= 4 {
            let length = buffer.prefix(4).reduce(UInt32(0)) { ($0 << 8) | UInt32($1) }
            let total = 4 + Int(length)
            guard buffer.count >= total else { return }
            let payload = buffer.subdata(in: 4..<total)
            buffer.removeSubrange(0..<total)

            guard let frame = try? JSONDecoder().decode(Frame.self, from: payload) else { continue }
            // The first real sign of life; discovery is over.
            timeout?.cancel()
            continuation.yield(frame)
            if frame.done == true || frame.error != nil {
                done()
                return
            }
        }
    }

    private func done() {
        guard !finished else { return }
        finished = true
        continuation.finish()
        stop()
    }

    private func fail(_ error: Error) {
        guard !finished else { return }
        finished = true
        continuation.finish(throwing: error)
        stop()
    }

    func stop() {
        timeout?.cancel()
        browser?.cancel()
        connection?.cancel()
        browser = nil
        connection = nil
    }
}
