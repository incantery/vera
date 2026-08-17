import Foundation

// What vera actually sends, and how it becomes what the phone draws.
//
// The two vocabularies are not the same size. The server's GoalCard is
// a board row — state, face, owner, counts. The phone's Goal is the
// pass 2–4 grammar: stance, strata with provenance, pursuits, marks,
// outcome. The mapping below fills in what the wire carries and
// deliberately leaves the rest empty rather than inventing it, because
// a fabricated stratum would be a lie about what Vera used to believe.
//
// What has no wire representation yet, and so stays absent:
//   · strata — no stance history is transmitted at all
//   · marks  — your constraints as durable chips
//   · outcome with its kept-in-memory lesson
//   · stakes — blocked / compounds / work-continues / held-in-goal,
//     which is what ranks one ask above another on Home

// MARK: - WatchBoard

struct BoardFrame: Decodable, Sendable {
    var goals: [GoalCardWire]?
    var fleet: FleetWire?
    var inflight: Int?
    var spend: Double?
    var notice: String?
}

struct FleetWire: Decodable, Sendable {
    var agents: Int?
    var working: Int?
}

struct GoalCardWire: Decodable, Sendable {
    var id: String
    var title: String
    /// "Building" · "Reviewing" · "Needs you" · "Ready for you" · "Ready"
    var state: String?
    /// One sentence under the state. The closest thing the wire has to
    /// a stance, and it is honestly less than one.
    var face: String?
    /// "you" · "vera" · "done" — who owns the next decision.
    var owner: String?
    var nodes: Int?
    var active: Int?
    var landed: Int?
    var spend: Double?
    /// protojson sends 64-bit integers as strings.
    var updatedUnixMs: String?

    var updatedAt: Date? {
        guard let ms = updatedUnixMs.flatMap(Double.init) else { return nil }
        return Date(timeIntervalSince1970: ms / 1000)
    }
}

// MARK: - WatchGoal

struct GoalFrame: Decodable, Sendable {
    var id: String
    var title: String?
    var state: String?
    var face: String?
    var nodes: [GoalNodeWire]?
    var events: [GoalEventWire]?
    var spend: Double?
}

struct GoalNodeWire: Decodable, Sendable {
    var id: String
    var title: String?
    /// implement · investigate · review · verify · reconcile
    var kind: String?
    /// inbox · progress · waiting · done · dropped
    var col: String?
    var state: String?
    var face: String?
    var deps: [String]?
    var blockedBy: [String]?
    var model: String?
    var tier: String?
    var costUsd: Double?
    var readOnly: Bool?
    /// Set when it is waiting on the owner.
    var ask: String?
    var liveState: String?
    var liveNow: String?
}

struct GoalEventWire: Decodable, Sendable {
    var seq: String?
    var atUnixMs: String?
    var kind: String?
    var node: String?
    var text: String?
}

// MARK: - Becoming a Goal

extension GoalCardWire {
    /// A board row, read into as much of the goal grammar as it can
    /// honestly fill.
    func asGoal(origin: Connection, changed: Bool) -> Goal {
        var goal = Goal(
            id: Self.stableID(machine: origin.id, remote: id),
            title: title,
            kind: .diagnostic,
            // `face` is Vera's own sentence about this work. It is not
            // an evolving belief with history behind it — but it is the
            // sentence she would lead with, so it leads.
            stance: face?.isEmpty == false ? face! : (state ?? "Underway."),
            understanding: (state ?? "").lowercased(),
            lifecycle: Self.lifecycle(owner: owner, state: state)
        )
        goal.digest = face ?? state ?? ""
        goal.standing = Self.standing(state: state, active: active ?? 0, owner: owner)
        goal.changedSinceYouLooked = changed
        goal.activity = Self.activity(nodes: nodes, active: active, landed: landed, spend: spend)
        goal.remoteID = id
        goal.machine = origin.id
        goal.machineName = origin.shortName

        if goal.lifecycle == .needsYou {
            // The wire does not say what waiting costs, so Home cannot
            // rank this ask against another one. Recording the absence
            // rather than guessing a blocker keeps the card honest.
            goal.stakes = Stakes()
        }
        return goal
    }

    /// A goal's identity has to survive a reconnect *and* stay distinct
    /// when two machines hand out the same card id. Derived from the
    /// pair, with FNV rather than `Hasher` — Swift seeds `Hasher`
    /// per-process, so ids built with it would change on every launch
    /// and every row would look new.
    static func stableID(machine: UUID, remote: String) -> UUID {
        var input = [UInt8]()
        withUnsafeBytes(of: machine.uuid) { input.append(contentsOf: $0) }
        input.append(contentsOf: Array(remote.utf8))

        func fnv1a(_ bytes: [UInt8], seed: UInt64) -> UInt64 {
            var h = seed
            for b in bytes {
                h ^= UInt64(b)
                h = h &* 0x100_0000_01b3
            }
            return h
        }

        var out = [UInt8]()
        withUnsafeBytes(of: fnv1a(input, seed: 0xcbf2_9ce4_8422_2325).bigEndian) { out.append(contentsOf: $0) }
        withUnsafeBytes(of: fnv1a(input, seed: 0x8422_2325_cbf2_9ce4).bigEndian) { out.append(contentsOf: $0) }
        return UUID(uuid: (out[0], out[1], out[2], out[3], out[4], out[5], out[6], out[7],
                           out[8], out[9], out[10], out[11], out[12], out[13], out[14], out[15]))
    }

    private static func lifecycle(owner: String?, state: String?) -> Lifecycle {
        switch owner?.lowercased() {
        case "you": return .needsYou
        case "done": return .done
        default: break
        }
        let s = (state ?? "").lowercased()
        if s.contains("needs you") { return .needsYou }
        if s == "ready" || s.contains("done") { return .done }
        return .withVera
    }

    private static func standing(state: String?, active: Int, owner: String?) -> Standing {
        let s = (state ?? "").lowercased()
        if owner?.lowercased() == "you" || s.contains("ready for you") {
            return .waiting(on: "your call")
        }
        // "Waiting" on the board means *something*, but the wire does
        // not say what — so the digest says it is waiting and stops,
        // rather than asserting it is waiting on the person.
        if s.contains("waiting") { return .waiting(on: "something upstream") }
        if s.contains("watch") { return .watching }
        return active > 0 ? .moving : .quiet
    }

    /// Activity is not progress — so the counts and the spend, which is
    /// all the wire really offers about machine effort, go where machine
    /// effort goes: the ten-point monospace footnote.
    private static func activity(nodes: Int?, active: Int?, landed: Int?, spend: Double?) -> String {
        var parts: [String] = []
        if let nodes, nodes > 0 { parts.append("\(nodes) nodes") }
        if let active, active > 0 { parts.append("\(active) working") }
        if let landed, landed > 0 { parts.append("\(landed) landed") }
        if let spend, spend > 0 { parts.append(String(format: "$%.2f", spend)) }
        return parts.isEmpty ? "" : "activity: " + parts.joined(separator: " · ")
    }
}

extension GoalFrame {
    /// The full goal, once WatchGoal has answered. Nodes become
    /// pursuits — the mapping is close, because a node is a card and a
    /// pursuit is a thing being done because of the stance.
    func apply(to goal: inout Goal) {
        if let title, !title.isEmpty { goal.title = title }
        if let face, !face.isEmpty { goal.stance = face }
        if let state, !state.isEmpty { goal.understanding = state.lowercased() }
        goal.pursuits = (nodes ?? []).map { $0.asPursuit() }
        goal.setAside = (nodes ?? [])
            .filter { $0.col == "dropped" }
            .compactMap { $0.title }

        // Events could become developments, but the wire does not say
        // which ones *moved the stance* — that judgment is Vera's, and
        // guessing it here would put the phone in the business of
        // deciding what counts as a finding.
        if let spend, spend > 0 {
            goal.activity = String(format: "activity: %d nodes · $%.2f", (nodes ?? []).count, spend)
        }
    }
}

extension GoalNodeWire {
    func asPursuit() -> Pursuit {
        var note = face
        if let blockedBy, !blockedBy.isEmpty {
            note = "blocked on \(blockedBy.count) — \(face ?? state ?? "")"
        }
        if let ask, !ask.isEmpty { note = ask }

        return Pursuit(
            title ?? id,
            note: note?.isEmpty == false ? note : state,
            state: pursuitState,
            live: ask?.isEmpty == false
        )
    }

    private var pursuitState: PursuitState {
        if liveState?.isEmpty == false || liveNow?.isEmpty == false { return .lead }
        switch col {
        case "done": return .done
        case "dropped": return .out
        case "waiting": return (blockedBy?.isEmpty == false) ? .waiting : .active
        case "progress": return .active
        case "inbox": return .dim
        default: return .dim
        }
    }
}
