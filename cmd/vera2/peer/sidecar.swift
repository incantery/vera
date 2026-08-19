// vera-peer — the part of Vera that has to be written in Swift.
//
// Peer-to-peer on Apple platforms means AWDL, and AWDL means
// Network.framework: there is no Go for it. So this exists, and it is
// kept as stupid as it can possibly be — it does discovery and it
// copies bytes. It knows nothing about Vera's protocol, its messages,
// or its secret.
//
// That is deliberate. A clever sidecar would mean maintaining the
// protocol twice, and the whole point of the arrangement is that
// Android later gets its own equally stupid sidecar over its own radio
// while nothing above the byte stream moves.
//
//   vera-peer --socket <path> --service _vera._tcp --name <label> --peer <id>
//
// Advertises the service over peer-to-peer, and for each peer that
// connects, opens the unix socket and pipes both directions.

import Foundation
import Network

func argument(_ name: String) -> String? {
    let args = CommandLine.arguments
    guard let i = args.firstIndex(of: "--" + name), i + 1 < args.count else { return nil }
    return args[i + 1]
}

guard let socketPath = argument("socket"),
      let serviceType = argument("service"),
      let label = argument("name"),
      let peerID = argument("peer")
else {
    FileHandle.standardError.write("vera-peer: --socket, --service, --name and --peer are all required\n".data(using: .utf8)!)
    exit(2)
}

let log = { (message: String) in
    FileHandle.standardError.write("vera-peer: \(message)\n".data(using: .utf8)!)
}

// pipe copies one direction until it ends, then closes the other side
// so a half-open connection cannot strand the pair.
func pipe(from source: NWConnection, to sink: NWConnection, label direction: String) {
    source.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { data, _, isComplete, error in
        if let data, !data.isEmpty {
            sink.send(content: data, completion: .contentProcessed { sendError in
                if let sendError {
                    log("\(direction) send failed: \(sendError)")
                    source.cancel(); sink.cancel()
                    return
                }
                pipe(from: source, to: sink, label: direction)
            })
            return
        }
        if isComplete || error != nil {
            // Tell the far side the stream is over rather than leaving
            // it waiting on bytes that will never come.
            sink.send(content: nil, contentContext: .finalMessage, isComplete: true,
                      completion: .contentProcessed { _ in sink.cancel() })
            source.cancel()
            return
        }
        pipe(from: source, to: sink, label: direction)
    }
}

func bridge(_ peer: NWConnection) {
    let local = NWConnection(to: .unix(path: socketPath), using: .tcp)
    let queue = DispatchQueue(label: "vera-peer.bridge")

    local.stateUpdateHandler = { state in
        switch state {
        case .ready:
            pipe(from: peer, to: local, label: "peer→vera")
            pipe(from: local, to: peer, label: "vera→peer")
        case .failed(let error):
            log("cannot reach vera on \(socketPath): \(error)")
            peer.cancel()
        case .cancelled:
            peer.cancel()
        default:
            break
        }
    }
    peer.stateUpdateHandler = { state in
        if case .failed = state { local.cancel() }
        if case .cancelled = state { local.cancel() }
    }
    peer.start(queue: queue)
    local.start(queue: queue)
}

let parameters = NWParameters.tcp
// The whole reason this file exists. Without it this is just Bonjour
// over the access point, which is the thing that fails in a hotel.
parameters.includePeerToPeer = true

let listener: NWListener
do {
    listener = try NWListener(using: parameters)
} catch {
    log("cannot listen: \(error)")
    exit(1)
}

// The peer id travels in TXT so a phone can tell WHICH machine it has
// found before it says anything to it. Names are for people and are
// not unique; the id is the identity the pairing code carried.
listener.service = NWListener.Service(
    name: label,
    type: serviceType,
    txtRecord: NWTXTRecord(["peer": peerID])
)

listener.stateUpdateHandler = { state in
    switch state {
    case .ready:
        // Read by vera2 to know the sidecar is actually up.
        print("ready")
        fflush(stdout)
    case .failed(let error):
        log("listener failed: \(error)")
        exit(1)
    case .waiting(let error):
        log("waiting: \(error)")
    default:
        break
    }
}
listener.newConnectionHandler = { bridge($0) }
listener.start(queue: .main)

RunLoop.main.run()
