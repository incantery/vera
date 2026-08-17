import Foundation

// Home at scale — selection, not inventory.
//
// Pass 2 put three goals on Home as three cards and that was hierarchy.
// Pass 4 ran the same design at fifteen and it became a feed: uniform
// card weight collapsed, recency-as-ordering collapsed, and "moving"
// pulses stopped meaning anything once four things pulsed at once.
//
// What survived is the *grammar* — stance-first rows, the goal name
// demoted to a kicker, the strata glance mark, one card for the thing
// that genuinely needs a person. What replaced the stack is this: a
// five-level attention policy, and two new selection behaviors over the
// existing primitives — the **digest line** (Vera's authored one-liner
// for work she chose not to show) and the **held ask** (a needs-you
// that lives in its goal and never reaches Home).
//
// No level is ever shown. There are no priority numbers anywhere.

/// The five levels. Product logic; never rendered.
enum Attention: Int, Comparable {
    /// Machine activity matching expectations. Under the hood only.
    case ignore
    /// Real but non-reorienting. Retrievable by asking; provenance kept.
    case record
    /// Changes your picture next time you look. Waits for you.
    case surfaceOnHome
    /// You'd want to know before you next open the app.
    case notify
    /// Vera cannot responsibly continue and your judgment changes the
    /// outcome. States what's blocked and what waiting costs.
    case needsYou

    static func < (a: Attention, b: Attention) -> Bool { a.rawValue < b.rawValue }
}

extension Goal {
    /// The deciding factors, in the order pass 4 lists them: consequence
    /// of waiting · reversibility · whether human judgment changes the
    /// outcome · novelty against your current picture.
    var attention: Attention {
        if lifecycle == .needsYou, let stakes {
            // A held ask is still a needs-you; it just isn't Home's.
            return stakes.heldInGoal ? .record : .needsYou
        }
        if lifecycle == .done { return changedSinceYouLooked ? .surfaceOnHome : .record }
        if changedSinceYouLooked { return .surfaceOnHome }
        if case .quiet = standing { return .ignore }
        return .record
    }
}

// MARK: - What Home shows

struct HomeSelection {
    /// The one ask worth a container. At most one, ever.
    var card: Goal?
    /// Asks that can wait. A quiet row each, with the cost of waiting
    /// stated and work continuing meanwhile.
    var lesserAsks: [Goal] = []
    /// Stance-first rows for work that changed your picture.
    var changed: [Goal] = []
    /// Asks Vera decided to hold inside their goals.
    var heldAsks: Int = 0

    /// Digest lines: named, not shown. Vera's sentence about the rest.
    var alsoMoving: [String] = []
    var watching: [String] = []
    var waiting: [(name: String, on: String)] = []
    var quiet: Int = 0
    var settled: [Goal] = []

    var askCount: Int {
        let carded: Int = card == nil ? 0 : 1
        return carded + lesserAsks.count
    }

    /// Everything Home chose not to show by name.
    var unspokenCount: Int {
        let named: Int = alsoMoving.count + watching.count + waiting.count
        return named + quiet
    }

    var withVeraCount: Int { changed.count + unspokenCount }

    // MARK: - Building it

    /// At most this many stance-first rows. Beyond that the rows stop
    /// being hierarchy and start being a list, which is the failure
    /// pass 4 found.
    private static let rowBudget = 2

    init(goals: [Goal]) {
        var asks: [Goal] = []

        for goal in goals {
            switch goal.attention {
            case .needsYou:
                asks.append(goal)

            case .surfaceOnHome, .notify:
                if goal.lifecycle == .done {
                    settled.append(goal)
                } else if goal.stakes?.heldInGoal == true {
                    heldAsks += 1
                } else {
                    changed.append(goal)
                }

            case .record, .ignore:
                // A goal can be moving *and* holding a question. The
                // held ask is counted, and the goal is still named among
                // the work — the two facts don't cancel.
                if goal.stakes?.heldInGoal == true { heldAsks += 1 }
                if goal.lifecycle == .done { continue }
                switch goal.standing {
                case .moving: alsoMoving.append(goal.title)
                case .watching: watching.append(goal.title)
                case .waiting(let on): waiting.append((goal.title, on))
                case .quiet: quiet += 1
                }
            }
        }

        // The card goes to the ask that is actually blocking something;
        // failing that, the one that compounds. There is no tie-break by
        // recency — that was the thing that collapsed.
        let ranked = asks.sorted { a, b in
            rank(a) > rank(b)
        }
        card = ranked.first
        lesserAsks = Array(ranked.dropFirst())

        // Rows are a budget, not a filter: what doesn't fit becomes a
        // digest line rather than disappearing.
        if changed.count > Self.rowBudget {
            let overflow = changed[Self.rowBudget...]
            alsoMoving.insert(contentsOf: overflow.map(\.title), at: 0)
            changed = Array(changed.prefix(Self.rowBudget))
        }
    }

    private func rank(_ goal: Goal) -> Int {
        guard let stakes = goal.stakes else { return 0 }
        var score = 0
        if stakes.blocked != nil { score += 4 }
        if stakes.compounds { score += 2 }
        if stakes.workContinues { score -= 1 }
        return score
    }
}

// MARK: - What Vera says about it
//
// The headline earns its size from what sits underneath it. Every
// sentence here is derived from the selection, so Home cannot claim a
// calm it doesn't have.

extension HomeSelection {

    var headline: String {
        switch askCount {
        case 0: "Nothing needs you."
        case 1: "One thing needs you."
        default: "\(Self.spelled(askCount).capitalizedFirst) things need you."
        }
    }

    /// Under the headline. With an ask pending this is the card goal's
    /// own digest; with nothing pending it is an honest accounting of
    /// the load being carried.
    var subhead: String {
        guard askCount > 0 else { return loadSentence }

        var parts: [String] = []
        if let blocked = card?.stakes?.blocked, !blocked.isEmpty {
            parts.append(card?.digest ?? "One is blocking work")
        } else if let digest = card?.digest, !digest.isEmpty {
            parts.append(digest)
        }
        if heldAsks > 0 {
            parts.append(heldAsks == 1
                ? "another question is waiting quietly inside its goal"
                : "\(Self.spelled(heldAsks)) more are waiting quietly inside their goals")
        }
        return parts.joined(separator: " · ")
    }

    /// 4b — the best state in the product. Load carried, visibly.
    var loadSentence: String {
        guard withVeraCount > 0 else { return "Quiet on my side too." }

        var parts = ["\(Self.spelled(withVeraCount).capitalizedFirst) thing\(withVeraCount == 1 ? " is" : "s are") with me."]

        var clauses: [String] = []
        let moving = changed.count + alsoMoving.count
        if moving > 0 { clauses.append("\(Self.spelled(moving)) \(moving == 1 ? "is" : "are") moving") }
        if !watching.isEmpty { clauses.append("\(Self.spelled(watching.count)) \(watching.count == 1 ? "is" : "are") watching") }
        if let first = waiting.first {
            clauses.append(waiting.count == 1
                ? "one waits on \(first.on)"
                : "\(Self.spelled(waiting.count)) are waiting")
        }
        if !clauses.isEmpty {
            parts.append(clauses.joined(separator: ", ").capitalizedFirst + ".")
        }
        if quiet > 0 { parts.append("The rest are quiet on purpose.") }

        return parts.joined(separator: " ")
    }

    /// "Also moving: memory model · Rook integration — nothing worth
    /// your eyes yet." Named, not shown: Vera is accountable for the
    /// judgment that they didn't need a row.
    var digestLines: [(label: String, names: String, note: String?)] {
        var lines: [(String, String, String?)] = []
        if !alsoMoving.isEmpty {
            lines.append(("Also moving:", alsoMoving.joined(separator: " · "), "nothing worth your eyes yet"))
        }
        if !watching.isEmpty {
            lines.append(("Watching:", watching.joined(separator: " · "), nil))
        }
        for item in waiting {
            lines.append(("Waiting:", item.name, "needs \(item.on)"))
        }
        return lines
    }

    /// The way out of the selection, for when you want everything.
    var browseLine: String? {
        guard unspokenCount > 0 else { return nil }
        return "\(Self.spelled(unspokenCount).capitalizedFirst) more goals — ask or browse ›"
    }

    static func spelled(_ n: Int) -> String {
        let words = ["zero", "one", "two", "three", "four", "five", "six",
                     "seven", "eight", "nine", "ten", "eleven", "twelve",
                     "thirteen", "fourteen", "fifteen"]
        return words.indices.contains(n) ? words[n] : "\(n)"
    }
}
