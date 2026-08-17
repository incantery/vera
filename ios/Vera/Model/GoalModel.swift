import Foundation

// The goal page — passes 2–4.
//
// Pass 3 settled the invariant: five primitives, and everything else is
// content. Stance, Pursuits, Developments, Your marks, Outcome. The same
// five carry a debugging session, a design iteration and a day's plan;
// what varies is the *kind* of development, the verbs in the pursuits,
// and what "set aside" means.
//
// Two dimensions ride on top and never share a slot:
//   · lifecycle    — Needs you / With Vera / Done. Lives in the tag.
//   · understanding — exploring · evidence accumulating · revised just
//     now · executing · resolved. Lives in the note under the stance.
//
// A goal that says "I don't know yet" is designed to read competent, not
// incomplete: it names what must be learned, and the pursuits underneath
// visibly go after exactly those things.

// MARK: - Lifecycle

enum Lifecycle: Hashable {
    case withVera
    case needsYou
    case done

    var label: String {
        switch self {
        case .withVera: "With Vera"
        case .needsYou: "Needs you"
        case .done: "Done"
        }
    }
}

// MARK: - The kind of work
//
// The only thing the kind changes is what setting something aside is
// called. Pass 3 found `eliminate` to be the sole debugging-specific
// move; every other transformation appeared in at least two specimens.

enum WorkKind: Hashable {
    case diagnostic
    case creative
    case adaptive

    var setAsideLabel: String {
        switch self {
        case .diagnostic: "Ruled out"
        case .creative: "Set aside"
        case .adaptive: "Deferred on purpose"
        }
    }
}

// MARK: - Provenance
//
// Pass 4: tap anything historical and get one pattern — believed /
// because / when. The goal page stays present-tense; history opens in
// place and closes on tap.

struct Provenance: Hashable {
    var kicker: String
    /// Freeform first line — "held 6 days · superseded 9:41".
    var timing: String
    /// Labelled rows: ("because:", "the graph duplicated git's guarantees").
    var rows: [Row]
    /// Draw the panel in accent — reserved for a superseded stance.
    var isStance: Bool = false

    struct Row: Hashable {
        var label: String
        var value: String
    }
}

// MARK: - Strata
//
// Superseded stances. Present state with provenance, not a feed: only
// the last two are drawn, the older ones collapse behind an ellipsis.

struct Stratum: Identifiable, Hashable {
    let id = UUID()
    var text: String
    var provenance: Provenance
}

// MARK: - Pursuits
//
// What Vera is doing *because of* the stance. A pursuit exists to change
// the stance or the world; it is never a todo item.

enum PursuitState: Hashable {
    /// The one thread carrying the work right now.
    case lead
    /// Running, contributing.
    case active
    /// Contradicted by evidence, not yet ruled out.
    case weak
    /// Background context.
    case dim
    /// Owned but not computing — a watch, or something held.
    case waiting
    /// Ruled out by evidence. Struck, not deleted.
    case out
    /// Absorbed into another pursuit; folds under it with a ↳.
    case merged
    /// Achieved its purpose.
    case done

    var isLive: Bool { self == .lead || self == .active }

    /// Opacity, from the pass-2 visibility table.
    var opacity: Double {
        switch self {
        case .lead, .active: 1
        case .weak: 0.8
        case .done: 0.8
        case .waiting: 0.7
        case .dim: 0.55
        case .merged: 0.55
        case .out: 0.5
        }
    }
}

struct Pursuit: Identifiable, Hashable {
    let id = UUID()
    var text: String
    var note: String?
    /// The note is the interesting thing right now — draw it in accent.
    var noteIsLive: Bool = false
    var state: PursuitState = .active
    /// Staggers the breathing dot so live lines don't pulse in unison.
    var pulseDelay: Double = 0

    init(
        _ text: String,
        note: String? = nil,
        state: PursuitState = .active,
        live: Bool = false,
        delay: Double = 0
    ) {
        self.text = text
        self.note = note
        self.state = state
        self.noteIsLive = live
        self.pulseDelay = delay
    }
}

// MARK: - Your marks
//
// Human input that became structure. Vera's own elevated principles dock
// beside them — hollow where yours are solid — and facts the world
// supplied are neutral.

enum MarkKind: Hashable {
    /// Yours: a decision, constraint, correction, preference, new fact.
    case yours
    /// Vera's, elevated from her own work.
    case principle
    /// From the world — a calendar, a mailbox, a repository.
    case world
}

struct Mark: Identifiable, Hashable {
    let id = UUID()
    var text: String
    var kind: MarkKind = .yours
    var provenance: Provenance?
}

// MARK: - Developments
//
// Moments the stance materially moved. Rare by definition.

enum DevelopmentKind: Hashable {
    case finding
    case insight
    case tension
    case newInformation

    var label: String {
        switch self {
        case .finding: "Finding"
        case .insight: "Insight"
        case .tension: "Tension"
        case .newInformation: "New information"
        }
    }
}

struct Development: Hashable {
    var kind: DevelopmentKind
    var text: String
}

// MARK: - The judgment boundary

struct Decision: Hashable {
    var intro: String
    var recommended: Choice
    var alternative: Choice
    /// What happens after you answer. Stated before you answer.
    var after: String

    struct Choice: Hashable {
        var title: String
        var body: String
        /// The word a person would actually say. The composer and the
        /// buttons are the same door, and "go with strict" has to land
        /// on the same choice the button does.
        var keyword: String
    }

    var choices: [Choice] { [recommended, alternative] }

    func choice(matching utterance: String) -> Choice? {
        let s = utterance.lowercased()
        return choices.first { s.contains($0.keyword.lowercased()) }
    }
}

// MARK: - Home presence
//
// Pass 4's contribution: at fifteen goals, a card stack becomes a feed.
// These three fields exist so Home can *select* rather than inventory —
// they are not status on the goal page, which never needed them.

/// Where a goal stands, from Home's point of view.
enum Standing: Hashable {
    case moving
    case watching
    case waiting(on: String)
    /// Nothing changed. No reason to speak.
    case quiet
}

/// Why an ask carries the weight it does. Product logic — never shown
/// as a score, a priority, or a number.
struct Stakes: Hashable {
    /// What is blocked, and for how long. Nil when nothing is.
    var blocked: String?
    /// Waiting compounds the cost.
    var compounds: Bool = false
    /// Vera keeps working meanwhile; the answer can arrive whenever.
    var workContinues: Bool = false
    /// A held ask: it lives in its goal and never reaches Home. You meet
    /// it if you open the goal.
    var heldInGoal: Bool = false
    /// Evidence may settle it first, in which case Vera withdraws it
    /// and says so.
    var mayWithdraw: Bool = false
}

// MARK: - Outcome
//
// What this requested piece of work produced, what was kept, and what
// deliberately remains open. Done is a lifecycle fact, not an epistemic
// one.

struct Outcome: Hashable {
    var text: String
    var keptInMemory: String
    var deliberatelyOpen: String?
}

// MARK: - The goal

struct Goal: Identifiable, Hashable {
    let id: UUID
    var title: String
    var kind: WorkKind

    // — the five primitives —
    var stance: String
    var strata: [Stratum] = []
    var pursuits: [Pursuit] = []
    var development: Development?
    var marks: [Mark] = []
    var outcome: Outcome?

    // — the two dimensions —
    var lifecycle: Lifecycle = .withVera
    /// The understanding note. Never the lifecycle.
    var understanding: String
    /// Understanding just moved — draw the note in accent.
    var understandingIsLive: Bool = false

    // — the rest —
    var decision: Decision?
    /// Your words, quoted back the moment they land.
    var saidByYou: String?
    var setAside: [String] = []
    /// Machine activity. A 10px monospace footnote, and never more.
    var activity: String = ""

    // — how Home sees it —
    var standing: Standing = .moving
    /// Present only while this goal is asking.
    var stakes: Stakes?
    /// Vera's own one-line account of where this stands. Her sentence,
    /// not a truncated stance — she is accountable for it.
    var digest: String = ""
    /// How long since the understanding last moved, in words.
    var lastMoved: String?
    /// Home speaks about a goal only when it would change your picture
    /// of the work. Everything else stays quiet on purpose.
    var changedSinceYouLooked: Bool = false

    // — where it lives —
    //
    // A goal that came off the wire remembers which machine it came
    // from, because the machine is part of what Vera says about it:
    // "one waits on your work Mac" is a sentence about a goal *and* a
    // sentence about a laptop that went to sleep.

    /// The card id on its machine. Nil for goals that only exist here.
    var remoteID: String?
    var machine: UUID?
    var machineName: String?
    var isRemote: Bool { remoteID != nil }

    init(
        id: UUID = UUID(),
        title: String,
        kind: WorkKind,
        stance: String,
        understanding: String,
        understandingIsLive: Bool = false,
        lifecycle: Lifecycle = .withVera,
        pursuits: [Pursuit] = [],
        marks: [Mark] = [],
        activity: String = ""
    ) {
        self.id = id
        self.title = title
        self.kind = kind
        self.stance = stance
        self.understanding = understanding
        self.understandingIsLive = understandingIsLive
        self.lifecycle = lifecycle
        self.pursuits = pursuits
        self.marks = marks
        self.activity = activity
    }

    /// Changes whenever the belief changes — the stance block keys off
    /// this so its underline sweeps once per succession and not on every
    /// unrelated redraw.
    var stanceKey: Int { strata.count }

    /// The set-aside ledger. What narrowing has bought, in the tense the
    /// work is actually in: "so far" while it is live, "along the way"
    /// once it is done.
    var setAsideLine: String? {
        guard !setAside.isEmpty else { return nil }
        let qualifier = lifecycle == .done ? " along the way" : " so far"
        return "\(kind.setAsideLabel)\(kind == .adaptive ? "" : qualifier): "
            + setAside.joined(separator: " · ")
    }

    /// The two most recent superseded stances, oldest first, with a
    /// leading ellipsis when there is more history than that.
    var visibleStrata: [(stratum: Stratum, elided: Bool)] {
        let tail = strata.suffix(2)
        return tail.enumerated().map { index, s in
            (s, strata.count > 2 && index == 0)
        }
    }
}

// MARK: - The eight transformations
//
// Pass 3's transformation vocabulary — system behaviors, not
// user-facing labels. Every visible change on a goal page is one of
// these, which is why the page never needs a changelog.

extension Goal {

    /// A new stance replaces the old; the old strikes and compresses
    /// into strata with its provenance.
    mutating func supersede(
        with newStance: String,
        understanding: String,
        because: String,
        by: String? = nil,
        held: String = "",
        at when: String
    ) {
        var rows = [Provenance.Row(label: "because:", value: because)]
        if let by { rows.append(.init(label: "by:", value: by)) }

        strata.append(Stratum(
            text: stance,
            provenance: Provenance(
                kicker: "Stance stratum",
                timing: held.isEmpty ? "superseded \(when)" : "held \(held) · superseded \(when)",
                rows: rows,
                isStance: true
            )
        ))
        stance = newStance
        self.understanding = understanding
        understandingIsLive = true
        development = nil
    }

    /// Two pursuits become one stronger one; the absorbed line folds
    /// under its survivor with a ↳.
    mutating func combine(
        _ absorbed: String,
        into survivor: String,
        rename: String? = nil,
        becoming: String? = nil,
        note: String? = nil
    ) {
        guard let i = index(of: survivor) else { return }
        pursuits[i].state = .lead
        if let rename { pursuits[i].text = rename }
        if let note { pursuits[i].note = note; pursuits[i].noteIsLive = true }

        guard let j = index(of: absorbed) else { return }
        pursuits[j].state = .merged
        pursuits[j].text = becoming ?? (pursuits[j].text + " — folded in")
        pursuits[j].note = nil
        move(j, after: i)
        promoteLead()
    }

    /// A new pursuit opens. Not one of the eight moves — it is what the
    /// stance does when it is first taken, or when a development opens
    /// ground the existing lines don't cover.
    mutating func open(_ pursuit: Pursuit, at index: Int = 0) {
        pursuits.insert(pursuit, at: Swift.min(index, pursuits.count))
    }

    /// A possibility is ruled out by evidence: strike, fade, and drop to
    /// the set-aside ledger. Progress by narrowing.
    mutating func eliminate(_ pursuit: String, becoming text: String? = nil, reason: String, ledger: String) {
        guard let i = index(of: pursuit) else { return }
        pursuits[i].state = .out
        if let text { pursuits[i].text = text }
        pursuits[i].note = reason
        pursuits[i].noteIsLive = false
        if !setAside.contains(ledger) { setAside.append(ledger) }
        moveToEnd(i)
    }

    /// Still valid, intentionally not now. Kept visible with its reason;
    /// never reads as failure. A direction that never became a pursuit
    /// row can still be parked — it takes a row on its way out.
    mutating func park(_ pursuit: String, becoming text: String? = nil, reason: String, ledger: String) {
        if let i = index(of: pursuit) {
            pursuits[i].state = .waiting
            if let text { pursuits[i].text = text }
            pursuits[i].note = reason
            pursuits[i].noteIsLive = false
        } else {
            pursuits.append(Pursuit(text ?? pursuit, note: reason, state: .waiting))
        }
        if !setAside.contains(ledger) { setAside.append(ledger) }
    }

    /// An observation becomes a durable principle: it leaves the pursuit
    /// list and docks as a hollow chip beside your marks.
    mutating func elevate(
        _ pursuit: String,
        becoming text: String? = nil,
        asPrinciple principle: String,
        from: String,
        when: String
    ) {
        if let i = index(of: pursuit) {
            pursuits[i].state = .done
            if let text { pursuits[i].text = text }
            pursuits[i].note = "now docked above"
            pursuits[i].noteIsLive = false
        }
        marks.append(Mark(
            text: "principle: \(principle)",
            kind: .principle,
            provenance: Provenance(
                kicker: "Vera principle",
                timing: "elevated \(when)",
                rows: [.init(label: "from:", value: from)]
            )
        ))
    }

    /// Your words become a solid chip under the stance; affected
    /// pursuits reorder around it. Decisions, constraints, corrections,
    /// preferences and new facts are all the same move.
    mutating func incorporate(
        _ words: String,
        as markText: String,
        kind: MarkKind = .yours,
        said: String? = nil,
        affects: String? = nil,
        standsUntil: String? = nil
    ) {
        saidByYou = words
        decision = nil
        // The ownership flip reverses the moment you answer.
        if lifecycle == .needsYou { lifecycle = .withVera }

        var rows: [Provenance.Row] = []
        if let affects { rows.append(.init(label: "affects:", value: affects)) }
        if let standsUntil { rows.append(.init(label: "stands until:", value: standsUntil)) }

        marks.append(Mark(
            text: markText,
            kind: kind,
            provenance: rows.isEmpty ? nil : Provenance(
                kicker: kind == .yours ? "Your constraint" : "From the world",
                timing: said ?? "just now",
                rows: rows
            )
        ))
    }

    /// A development creates two genuinely distinct pursuits. Rare —
    /// used only when honest.
    mutating func branch(_ pursuit: String, into replacements: [Pursuit]) {
        guard let i = index(of: pursuit) else { return }
        pursuits.replaceSubrange(i...i, with: replacements)
    }

    /// A pursuit achieved its purpose: ring mark, then it leaves the
    /// active set.
    mutating func conclude(_ pursuit: String, becoming text: String? = nil, note: String? = nil) {
        guard let i = index(of: pursuit) else { return }
        pursuits[i].state = .done
        if let text { pursuits[i].text = text }
        pursuits[i].note = note
        pursuits[i].noteIsLive = false
    }

    /// The goal-level conclude: pulses stop, the working set retires,
    /// everything condenses into the outcome.
    mutating func settle(_ outcome: Outcome, understanding: String, summary: Pursuit) {
        lifecycle = .done
        self.outcome = outcome
        self.understanding = understanding
        understandingIsLive = false
        development = nil
        decision = nil
        pursuits = [summary]
    }

    // — asking —

    /// Ownership flips to you. The decision rises from the composer,
    /// which is where answers come from.
    mutating func ask(_ decision: Decision, understanding: String) {
        self.decision = decision
        lifecycle = .needsYou
        self.understanding = understanding
        understandingIsLive = true
    }

    /// A development rises. It plays once and is cleared by the next
    /// change of understanding.
    mutating func develop(_ development: Development, understanding: String) {
        self.development = development
        self.understanding = understanding
        understandingIsLive = true
    }

    mutating func note(_ text: String, live: Bool = false) {
        understanding = text
        understandingIsLive = live
    }

    mutating func update(_ pursuit: String, state: PursuitState? = nil, note: String? = nil, live: Bool? = nil) {
        guard let i = index(of: pursuit) else { return }
        if let state { pursuits[i].state = state }
        if let note { pursuits[i].note = note }
        if let live { pursuits[i].noteIsLive = live }
        promoteLead()
    }

    /// A line becomes a different line in place — same slot on the
    /// spine, restated because the work under it changed shape.
    mutating func replace(_ pursuit: String, with replacement: Pursuit) {
        guard let i = index(of: pursuit) else { return }
        pursuits[i] = replacement
        promoteLead()
    }

    // — plumbing —

    private func index(of pursuitPrefix: String) -> Int? {
        pursuits.firstIndex { $0.text.hasPrefix(pursuitPrefix) }
    }

    /// The thread carrying the work leads the list, and brings anything
    /// folded under it along. This is the reordering the incorporation
    /// move promises: affected pursuits arrange themselves around what
    /// the stance now says matters.
    private mutating func promoteLead() {
        guard let i = pursuits.firstIndex(where: { $0.state == .lead }), i > 0 else { return }
        var end = i + 1
        while end < pursuits.count, pursuits[end].state == .merged { end += 1 }
        let block = Array(pursuits[i..<end])
        pursuits.removeSubrange(i..<end)
        pursuits.insert(contentsOf: block, at: 0)
    }

    private mutating func move(_ from: Int, after to: Int) {
        let item = pursuits.remove(at: from)
        let target = from < to ? to : to + 1
        pursuits.insert(item, at: min(target, pursuits.count))
    }

    private mutating func moveToEnd(_ from: Int) {
        let item = pursuits.remove(at: from)
        pursuits.append(item)
    }
}
