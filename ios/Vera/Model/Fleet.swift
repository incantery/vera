import Foundation
import Observation
import SwiftUI

// Several machines, one relationship.
//
// Each Mac runs its own vera and holds its own goals. The phone keeps a
// stream open to each and merges what comes back, so Home reads one
// list of work rather than a tab per laptop. Which machine a goal lives
// on stays attached to it, because Vera already talks that way — 4i's
// whole scene is a work Mac going to sleep and one pursuit pausing
// while nothing else cares.
//
// A machine that is away is not an error state. It is a fact about the
// world, reported the way Vera reports facts about the world.

@Observable
@MainActor
final class Fleet {

    private(set) var connections: [Connection] = []
    private(set) var reach: [UUID: Reach] = [:]
    /// The most recent board frame from each machine.
    private(set) var goalsByMachine: [UUID: [Goal]] = [:]

    /// When Home was last looked at. A goal that moved since then is
    /// what "changes your picture next time you look" actually means.
    private var lastLooked: Date
    private var seenUpdates: [String: Date] = [:]

    private var watchers: [UUID: Task<Void, Never>] = [:]

    private static let storeKey = "fleet.connections"
    private static let lookedKey = "fleet.lastLooked"

    init() {
        let defaults = UserDefaults.standard
        lastLooked = defaults.object(forKey: Self.lookedKey) as? Date ?? .distantPast
        if let data = defaults.data(forKey: Self.storeKey),
           let saved = try? JSONDecoder().decode([Connection].self, from: data) {
            connections = saved
        }

        // Review scaffold, alongside -scene and -beat:
        //   -connect "http://127.0.0.1:4779/?key=…,http://127.0.0.1:4771"
        // pairs machines at launch so the connected states can be driven
        // without tapping through the sheet.
        if let seeded = defaults.string(forKey: "connect"), !seeded.isEmpty {
            connections.removeAll()
            for piece in seeded.split(separator: ",") {
                guard let (connection, key) = Connection.parse(String(piece)) else { continue }
                connections.append(connection)
                KeyStore.save(key, for: connection.id)
            }
        }
    }

    // MARK: - What the phone shows

    /// Every machine's goals, in one list. Home does not care which Mac
    /// a piece of work is on; the goal itself remembers.
    var goals: [Goal] {
        connections.flatMap { goalsByMachine[$0.id] ?? [] }
    }

    var isConnected: Bool { reach.values.contains { $0.isLive } }
    var hasConnections: Bool { !connections.isEmpty }

    var liveConnections: [Connection] {
        connections.filter { reach[$0.id]?.isLive == true }
    }

    var awayConnections: [(connection: Connection, why: String)] {
        connections.compactMap { c in
            if case .away(let why) = reach[c.id] { return (c, why) }
            return nil
        }
    }

    /// What the header pill says. One machine names itself; several
    /// count themselves; none says so plainly.
    var localityLine: String {
        guard hasConnections else { return "Local" }
        let live = liveConnections
        switch live.count {
        case 0: return "Away"
        case 1: return live[0].shortName
        default: return "\(live.count) machines"
        }
    }

    /// A machine being away is work news, not an alert. This is the
    /// sentence Home puts under the headline when one has gone quiet.
    var awayLine: String? {
        let away = awayConnections
        guard !away.isEmpty else { return nil }
        if away.count == 1 {
            return "\(away[0].connection.shortName) is \(away[0].why) — anything of its own is paused; nothing else cares."
        }
        let names = away.map(\.connection.shortName).joined(separator: " and ")
        return "\(names) are out of reach — their work is paused; nothing else cares."
    }

    // MARK: - Managing machines

    func add(_ connection: Connection, key: String?) {
        var connection = connection
        if connections.contains(where: { $0.host == connection.host && $0.port == connection.port }) {
            // Re-pasting a URL for a machine already known is a key
            // rotation, not a duplicate.
            if let existing = connections.first(where: { $0.host == connection.host && $0.port == connection.port }) {
                connection.id = existing.id
                remove(existing)
            }
        }
        connections.append(connection)
        KeyStore.save(key, for: connection.id)
        persist()
        connect(connection)
    }

    func rename(_ connection: Connection, to name: String) {
        guard let i = connections.firstIndex(where: { $0.id == connection.id }) else { return }
        connections[i].name = name
        // The name is part of what Vera says about the work, so goals
        // already in hand pick it up without waiting for a frame.
        goalsByMachine[connection.id] = goalsByMachine[connection.id]?.map {
            var g = $0
            g.machineName = name
            return g
        }
        persist()
    }

    func remove(_ connection: Connection) {
        watchers[connection.id]?.cancel()
        watchers[connection.id] = nil
        connections.removeAll { $0.id == connection.id }
        goalsByMachine[connection.id] = nil
        reach[connection.id] = nil
        KeyStore.delete(connection.id)
        persist()
    }

    private func persist() {
        if let data = try? JSONEncoder().encode(connections) {
            UserDefaults.standard.set(data, forKey: Self.storeKey)
        }
    }

    // MARK: - Streams

    func connectAll() {
        for connection in connections where watchers[connection.id] == nil {
            connect(connection)
        }
    }

    func disconnectAll() {
        for (_, task) in watchers { task.cancel() }
        watchers.removeAll()
        for id in reach.keys { reach[id] = .idle }
    }

    func connect(_ connection: Connection) {
        watchers[connection.id]?.cancel()
        reach[connection.id] = .connecting

        watchers[connection.id] = Task { [weak self] in
            await self?.watch(connection)
        }
    }

    /// One machine's board, held open. A stream that ends is not a
    /// failure — a laptop closes its lid — so it is retried with a
    /// backoff that gives up on nothing and shouts about nothing.
    private func watch(_ connection: Connection) async {
        var backoff: UInt64 = 1
        while !Task.isCancelled {
            guard let base = connection.baseURL else {
                reach[connection.id] = .away("not a reachable address")
                return
            }
            let client = ConnectClient(base: base, key: KeyStore.key(for: connection.id))

            // URLSession will sit on an unroutable address for a minute
            // before giving up. Nothing is wrong with waiting, but
            // "Reaching…" for a minute is a screen that looks broken —
            // so say it isn't answering yet, and keep trying anyway.
            let patience = Task { [weak self] in
                try? await Task.sleep(for: .seconds(8))
                guard !Task.isCancelled else { return }
                if self?.reach[connection.id] == .connecting {
                    self?.reach[connection.id] = .away("not answering yet")
                }
            }
            defer { patience.cancel() }

            do {
                for try await frame in client.stream("WatchBoard", as: BoardFrame.self) {
                    if Task.isCancelled { return }
                    patience.cancel()
                    reach[connection.id] = .live
                    backoff = 1
                    receive(frame, from: connection)
                }
                // A clean end just means vera stopped talking for now.
                if Task.isCancelled { return }
                reach[connection.id] = .away("not answering")
            } catch let error as ConnectError {
                if Task.isCancelled { return }
                reach[connection.id] = .away(error.spoken)
                // A refused key will be refused again in two seconds.
                if error.code == "unauthenticated" { return }
            } catch {
                if Task.isCancelled { return }
                reach[connection.id] = .away("out of reach")
            }

            try? await Task.sleep(for: .seconds(min(backoff, 30)))
            backoff *= 2
        }
    }

    private func receive(_ frame: BoardFrame, from connection: Connection) {
        let cards = frame.goals ?? []
        goalsByMachine[connection.id] = cards.map { card in
            // "Changed since you looked" is a real comparison against
            // when you last looked, not a guess from how recent it is.
            let updated = card.updatedAt
            let previous = seenUpdates[card.id]
            let changed = (updated ?? .distantPast) > max(lastLooked, previous ?? .distantPast)
            if let updated { seenUpdates[card.id] = max(previous ?? .distantPast, updated) }
            return card.asGoal(origin: connection, changed: changed)
        }
    }

    /// You stopped looking at Home, so everything on it has now been
    /// seen and the next arrival is judged against this moment.
    ///
    /// Deliberately *not* called when Home appears. Frames arrive a
    /// beat after launch, so marking them seen on appear would judge
    /// every goal against a look that happened before it existed —
    /// which silently emptied the rows and left only digest lines.
    func stoppedLooking() {
        lastLooked = Date()
        UserDefaults.standard.set(lastLooked, forKey: Self.lookedKey)
    }

    // MARK: - One goal, in full

    /// WatchGoal, for as long as the goal page is open. The board row
    /// is a summary; this is the pursuits underneath it.
    func watchGoal(_ goal: Goal, into apply: @escaping @MainActor (Goal) -> Void) -> Task<Void, Never>? {
        guard let machine = goal.machine,
              let remote = goal.remoteID,
              let connection = connections.first(where: { $0.id == machine }),
              let base = connection.baseURL
        else { return nil }

        let client = ConnectClient(base: base, key: KeyStore.key(for: machine))
        return Task { @MainActor in
            do {
                for try await frame in client.stream(
                    "WatchGoal",
                    body: ["id": remote],
                    as: GoalFrame.self
                ) {
                    if Task.isCancelled { return }
                    var updated = goal
                    frame.apply(to: &updated)
                    apply(updated)
                }
            } catch {
                // The row already said what it knows. A failed detail
                // stream leaves that standing rather than blanking it.
            }
        }
    }
}
