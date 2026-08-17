import Foundation

// The v5 vocabulary.
//
// The load-bearing distinction: a conversation is *fluid* — turns are
// typography and they are allowed to leave nothing behind — while a
// *consequence* becomes structure, and structure is what the rest of
// the app reads from. Nothing here has a "message" type with a "sender"
// field, because that would make talk and consequence the same kind of
// thing.

// MARK: - Materialization

/// The six shapes a consequence can take, plus the one revision form.
/// The user never picks one of these; classification is Vera's problem.
enum MaterializationKind: Hashable {
    /// Structured data joined an existing series.
    case logged
    /// A durable idea, kept with provenance. The label carries where it went.
    case kept(String?)
    /// Ownership of a trigger, without ceremony.
    case watching
    /// Ongoing work Vera has taken. `isNew` gets the "now" that marks
    /// the moment of handover.
    case withVera(isNew: Bool)
    /// A dated one-shot.
    case tomorrow
    /// A belief superseded by a correction.
    case revised

    var kicker: String {
        switch self {
        case .logged: "Logged"
        case .kept(let dest): dest.map { "Kept · \($0)" } ?? "Kept"
        case .watching: "Watching"
        case .withVera(let isNew): isNew ? "With Vera now" : "With Vera"
        case .tomorrow: "Tomorrow"
        case .revised: "Revised"
        }
    }

    /// Memories and revisions are held in a dashed outline: inspectable,
    /// revisable, deliberately less solid than work or data.
    var isDashed: Bool {
        switch self {
        case .kept(.none), .revised: true
        default: false
        }
    }
}

struct Materialization: Identifiable, Hashable {
    let id = UUID()
    var kind: MaterializationKind
    /// Heading-weight line. Only goals carry one.
    var title: String?
    var line: String
    var footnote: String?
    /// The belief this supersedes, struck through above the correction.
    var struckLine: String?
    /// Where tapping this leads, if anywhere.
    var destination: Route?

    init(
        kind: MaterializationKind,
        title: String? = nil,
        line: String,
        footnote: String? = nil,
        struckLine: String? = nil,
        destination: Route? = nil
    ) {
        self.kind = kind
        self.title = title
        self.line = line
        self.footnote = footnote
        self.struckLine = struckLine
        self.destination = destination
    }
}

// MARK: - Turns

/// A figure Vera quotes inline. Not a widget — a doorway into a surface.
struct StatBlock: Hashable {
    struct Row: Hashable {
        var label: String
        var value: String
        /// Rendered in accent when the value is the point.
        var accentValue: String?
        var isMuted: Bool = false
    }

    var rows: [Row]
    var openLabel: String?
    var destination: Route?
}

struct Chip: Hashable {
    var label: String
    var showsTick: Bool = false
    var destination: Route?
}

enum TurnBody: Hashable {
    case user(String)
    case vera(String)
    case material(Materialization)
    case stat(StatBlock)
    case chips([Chip])
}

struct Turn: Identifiable, Hashable {
    let id = UUID()
    var body: TurnBody

    var isUser: Bool { if case .user = body { true } else { false } }
    var isMaterial: Bool { if case .material = body { true } else { false } }

    static func user(_ s: String) -> Turn { Turn(body: .user(s)) }
    static func vera(_ s: String) -> Turn { Turn(body: .vera(s)) }
    static func material(_ m: Materialization) -> Turn { Turn(body: .material(m)) }
    static func stat(_ s: StatBlock) -> Turn { Turn(body: .stat(s)) }
    static func chips(_ c: [Chip]) -> Turn { Turn(body: .chips(c)) }
}

// MARK: - Ageing
//
// Older turns recede. The boundary is not "the last N turns" — it is the
// end of the previous *exchange*, and an exchange ends when a consequence
// lands. So: everything up to and including the most recent
// materialization recedes, unless nothing has been said since, in which
// case the sentence that caused it stays live alongside it.

extension Array where Element == Turn {
    /// Index of the first turn still in the live foreground.
    var liveWindowStart: Int {
        guard let lastMaterial = lastIndex(where: { $0.isMaterial }) else { return 0 }
        let after = index(after: lastMaterial)
        if self[after...].contains(where: { $0.isUser }) {
            return after
        }
        // The consequence is the tail's own: keep the sentence that made it.
        let cause = self[..<lastMaterial].lastIndex(where: { $0.isUser })
        return cause ?? lastMaterial
    }
}

// MARK: - Structure the conversation grew

struct Principle: Identifiable, Hashable {
    let id = UUID()
    var text: String
    /// The belief this replaced, if it revised one.
    var supersedes: String?
    var provenance: String
    /// Vera elevated this herself rather than being asked.
    var elevatedByVera: Bool = false
    var revised: Bool { supersedes != nil }
}

struct Memory: Identifiable, Hashable {
    let id = UUID()
    var belief: String
    var provenance: String
    /// Corrections do not delete. The wrong belief strikes; both stay.
    var supersededBy: String?
}

struct LiftSet: Hashable {
    var lift: String
    var weight: Int
    var reps: [Int]

    /// Epley, noted as approximate wherever it is shown.
    var estimatedMax: Double {
        guard let best = reps.max(), best > 0 else { return Double(weight) }
        return Double(weight) * (1 + Double(best) / 30)
    }

    var repsDescription: String { reps.map(String.init).joined(separator: "/") }
}

struct Session: Identifiable, Hashable {
    let id = UUID()
    var date: Date
    var sets: [LiftSet]
    var note: String?

    func best(_ lift: String) -> LiftSet? {
        sets.filter { $0.lift == lift }.max { $0.estimatedMax < $1.estimatedMax }
    }
}

struct WatchItem: Identifiable, Hashable {
    let id = UUID()
    var subject: String
    var promise: String
    /// Nothing has arrived yet.
    var quiet: Bool = true
}

struct Reminder: Identifiable, Hashable {
    let id = UUID()
    var text: String
    var whenLabel: String
    /// Set when it arrived inside a goal it has nothing to do with, so
    /// the scope can be stated out loud.
    var scopeNote: String?
}

// `Goal` — the five primitives of passes 2–4 — lives in GoalModel.swift.

// MARK: - Home

// The thing that crossed the threshold into needing a person is a
// *goal* with an open decision — see HomeSelection.swift. Home never had
// its own notion of an attention item; it reads the same goals the goal
// page does.

/// A change worth mentioning that did not need you. `why` is Vera's
/// reasoning, held next to the claim rather than regenerated on demand —
/// the provenance model applies to her own decisions too.
struct ChangeNote: Identifiable, Hashable {
    let id = UUID()
    var lead: String?
    var text: String
    var why: String?

    var hasWhy: Bool { why != nil }
}

/// An entry in "Around you" — structure that grew out of talking.
struct SurfaceLink: Identifiable, Hashable {
    let id = UUID()
    var name: String
    var detail: String
    var destination: Route?
    var isQuiet: Bool = false
}

// MARK: - Routing

enum Route: Hashable {
    case conversation
    case training
    case principles
    case goal(UUID)
}
