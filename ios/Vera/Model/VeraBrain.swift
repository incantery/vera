import Foundation

// 5h — five utterances, five outcomes, and the user never picks a
// category. This file is that spec made executable.
//
// It is deliberately a small local reader, not a model call: the point
// of the screen is that classification happens on Vera's side and is
// invisible, and a rule table demonstrates that as honestly as an LLM
// would while staying deterministic enough to test. Swapping this for a
// real model means replacing `read(_:)` and nothing else.

struct Reading {
    /// What Vera says. May be empty when the consequence speaks for itself.
    var says: [String] = []
    /// The object this utterance leaves behind, if any. Nil is a real,
    /// common, correct answer — silence about silence.
    var materialization: Materialization?
    /// A figure block Vera quotes while answering.
    var stat: StatBlock?
    /// Provenance doorways at the end of a retrieval answer.
    var chips: [Chip] = []
    /// Side effects on the structures the app reads from.
    var effect: Effect?

    enum Effect {
        case log(LiftSet, dayLabel: String)
        case keep(Principle)
        case remember(Memory)
        case revise(belief: String, correction: String)
        case watch(WatchItem)
        case remind(Reminder)
        case take(Goal)
        case answerAsk(UUID, Decision.Choice)
    }
}

struct VeraBrain {
    /// Everything Vera already knows, so answers can draw on structure
    /// rather than on the last message.
    var context: Context

    struct Context {
        var sessions: [Session] = []
        var principles: [Principle] = []
        var watches: [WatchItem] = []
        var memories: [Memory] = []
        /// The goal currently asking, if Home is showing one.
        var pendingAsk: Goal?
        var currentGoal: Goal?
        /// The conversation so far, *not* including the utterance being
        /// read. "Keep that" has no meaning without it.
        var priorTurns: [Turn] = []
    }

    // MARK: - The read

    func read(_ raw: String) -> Reading {
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        let s = text.lowercased()
        guard !s.isEmpty else { return Reading() }

        // An open decision outranks everything: the person is answering.
        if let ask = context.pendingAsk, let answer = answerToAsk(s, ask) {
            return answer
        }

        if let r = revision(s) { return r }
        if let r = retrieval(s) { return r }
        if let r = logging(text, s) { return r }
        if let r = watching(text, s) { return r }
        if let r = reminding(text, s) { return r }
        if let r = keeping(text, s) { return r }
        if let r = delegating(text, s) { return r }

        return talking(s)
    }

    // MARK: - Answering an open decision

    /// The buttons and the composer are the same door. "Go with strict"
    /// has to land on exactly the choice the button would have.
    private func answerToAsk(_ s: String, _ goal: Goal) -> Reading? {
        guard let decision = goal.decision,
              let choice = decision.choice(matching: s)
        else { return nil }

        return Reading(
            says: [
                "\(choice.keyword.capitalizedFirst) it is. \(decision.after) Back to you only if it turns out to need you again."
            ],
            effect: .answerAsk(goal.id, choice)
        )
    }

    // MARK: - Correcting a belief
    //
    // "No — don't treat that as a standing preference." Same supersede
    // grammar as everything else: the wrong belief strikes, the
    // correction stands, provenance keeps both.

    private func revision(_ s: String) -> Reading? {
        let denials = [
            "not a standing preference", "don't treat that as a standing",
            "dont treat that as a standing", "that's not a preference",
            "thats not a preference", "forget that", "i was just cranky",
            "don't remember that", "dont remember that", "stop assuming"
        ]
        guard denials.contains(where: { s.contains($0) }) else { return nil }

        let belief = context.mostRecentlyAppliedBelief
            ?? "Prefers nonstop; overnight layovers ruin the next day."
        let correction = "One-time remark, not a preference. Flights get no special handling."

        return Reading(
            says: ["Dropped. You can ask “what do you believe about my travel?” any time and see exactly what I hold."],
            materialization: Materialization(
                kind: .revised,
                line: correction,
                struckLine: belief
            ),
            effect: .revise(belief: belief, correction: correction)
        )
    }

    // MARK: - Retrieval
    //
    // Not a power feature. This is the everyday relationship — the
    // answer lives in structure, not in scrolling a transcript.

    private func retrieval(_ s: String) -> Reading? {
        if s.contains("getting stronger") || s.contains("am i stronger")
            || (s.contains("stronger") && s.contains("?")) {
            return strengthAnswer()
        }

        if s.contains("anything i need to know") || s.contains("what did i miss")
            || (s.hasPrefix("morning") && s.count < 60) {
            return Reading(says: [briefing()])
        }

        if s.contains("what should i focus on") || s.contains("what should i do")
            || s.contains("focus on this afternoon") {
            return Reading(says: [
                "Your 2:30 is prepped. The best use of the next two hours is Marta’s strikethrough question — it’s taste, it’s twenty minutes, and it unblocks the strata build. After the call your afternoon fragments; that’s the right slot for the mail sweep you postponed. The mow is tomorrow, per your note, and your shoulders get a rest day whether you like it or not."
            ])
        }

        if s.contains("what did we decide") || s.contains("what vera actually is")
            || s.contains("what we decided") {
            return Reading(
                says: [
                    "This morning, thinking out loud: software engineering is the litmus test, not the product category — Vera is a personal intelligence whose media include conversation, memory, data and software. You asked me to keep it; it’s in your product principles now."
                ],
                chips: [
                    Chip(label: "kept 9:45 · from that conversation ›", showsTick: true),
                    Chip(label: "Vera product principles ›", destination: .principles)
                ]
            )
        }

        if s.contains("what do you believe about my travel") || s.contains("what do you believe about me") {
            return Reading(says: [context.travelBeliefSummary])
        }

        return nil
    }

    private func strengthAnswer() -> Reading {
        let bench = Trend(lift: "bench press", sessions: context.sessions)
        let squat = Trend(lift: "squat", sessions: context.sessions)
        let rowing = Trend(lift: "row", sessions: context.sessions)

        var sentence = ""
        if let change = bench.percentChange, let first = bench.first, let last = bench.last {
            sentence = "Yes — modestly and consistently. Bench estimated max is up about \(Int(change.rounded()))% over \(bench.weekSpan) weeks — \(Int(first.rounded())) to \(Int(last.rounded()))"
            if context.hasTravelWeek {
                sentence += " — and the trend survived your travel week, which is the real sign."
            } else {
                sentence += "."
            }
        } else {
            sentence = "Not enough logged yet to say anything I’d stand behind."
        }

        // The honest half of the answer: where the data is too thin to
        // support a claim, say so instead of drawing the line anyway.
        if squat.sessionCount > 0 && squat.sessionCount < 6 {
            sentence += " Squat looks \(Self.shape(squat)), but you’ve only logged it \(squat.sessionCount) times — I can’t say much there yet."
        }

        var rows: [StatBlock.Row] = []
        if let first = bench.first, let last = bench.last, let change = bench.percentChange {
            rows.append(.init(
                label: "Bench · est. max",
                value: "\(Int(first.rounded())) → \(Int(last.rounded()))",
                accentValue: "↑\(Int(change.rounded()))%"
            ))
        }
        if let change = rowing.percentChange {
            rows.append(.init(label: "Rows", value: "↑\(Int(change.rounded()))%"))
        }
        if squat.sessionCount > 0 {
            rows.append(.init(
                label: "Squat",
                value: squat.sessionCount < 6
                    ? "\(Self.shape(squat)) · under-sampled"
                    : Self.shape(squat),
                isMuted: true
            ))
        }

        return Reading(
            says: [sentence],
            stat: StatBlock(rows: rows, openLabel: "Open your training history ›", destination: .training)
        )
    }

    private static func shape(_ trend: Trend) -> String {
        guard let change = trend.percentChange else { return "flat" }
        if change > 3 { return "up \(Int(change.rounded()))%" }
        if change < -3 { return "down \(Int(abs(change).rounded()))%" }
        return "flat"
    }

    private func briefing() -> String {
        var parts = ["Quiet night. Nothing urgent in mail."]
        if context.principles.contains(where: { $0.text.contains("memory") }) == false {
            parts.append("The memory model picked its shape overnight — git files stay; the database idea is out.")
        }
        if context.pendingAsk == nil {
            parts.append("Your pairing decision merged clean.")
        } else {
            parts.append("One thing is still waiting on you — the pairing key scope.")
        }
        if let quiet = context.watches.first(where: { $0.quiet }) {
            parts.append("Your \(quiet.subject.lowercased()) watch is still quiet.")
        }
        parts.append("One taste call will come your way this afternoon, nothing before that.")
        return parts.joined(separator: " ")
    }

    // MARK: - Logging
    //
    // "I benched 185 for 5, 5, 4" → structured data joining eight weeks
    // of history. No goal, no lifecycle tag.

    private func logging(_ text: String, _ s: String) -> Reading? {
        guard let set = LiftParser.parse(text) else { return nil }

        let day = s.contains("yesterday") ? "yesterday" : "today"
        let isRepPR = context.isRepPR(set)

        var ack = "Logged."
        if isRepPR { ack = "Logged — that’s a rep PR at \(set.weight)." }

        var reading = Reading(
            says: [ack],
            materialization: Materialization(
                kind: .logged,
                line: "\(set.lift.capitalizedFirst) · \(set.weight) lb · \(set.reps.map(String.init).joined(separator: " / ")) · \(day)",
                destination: .training
            ),
            effect: .log(set, dayLabel: day)
        )

        // 5u — a capability grows when the history is deep enough to
        // make comparisons mean something. Invisible and reversible, so
        // she acts and mentions it rather than asking.
        if context.sessions.count >= 8 && context.justCrossedComparisonThreshold {
            reading.says.append(
                "One more thing, since you’ve asked trend questions twice now: you have \(context.weekSpan) weeks of sessions — enough that comparisons matter. I’ve started keeping them structurally so “am I getting stronger?” has a real answer instead of a guess. Nothing changes about how you log; just keep telling me."
            )
        }
        return reading
    }

    // MARK: - Watching

    private func watching(_ text: String, _ s: String) -> Reading? {
        let triggers = ["keep an eye out", "keep an eye on", "watch for", "let me know when", "look out for", "tell me when"]
        guard triggers.contains(where: { s.contains($0) }) else { return nil }

        let subject = Phrase.subject(after: triggers.first(where: { s.contains($0) })!, in: text)
        let name = subject.isEmpty ? "that" : subject
        let watch = WatchItem(
            subject: name.capitalizedFirst,
            promise: "I’ll tell you when it lands or if two weeks pass without it."
        )
        return Reading(
            materialization: Materialization(
                kind: .watching,
                line: "\(name.capitalizedFirst) — \(watch.promise)"
            ),
            effect: .watch(watch)
        )
    }

    // MARK: - Reminding

    private func reminding(_ text: String, _ s: String) -> Reading? {
        guard s.contains("remind me") else { return nil }
        let task = Phrase.subject(after: "remind me to", in: text)
        let tomorrow = s.contains("tomorrow")
        let scopeNote = context.currentGoal.map { _ in "personal — not part of this goal" }

        var says = tomorrow
            ? "Set for tomorrow morning."
            : "Set."
        if context.currentGoal != nil {
            says += " The goal’s untouched."
        }

        return Reading(
            says: [says],
            materialization: Materialization(
                kind: .tomorrow,
                line: task.isEmpty ? text : task.capitalizedFirst,
                footnote: scopeNote
            ),
            effect: .remind(Reminder(
                text: task.capitalizedFirst,
                whenLabel: tomorrow ? "Tomorrow" : "Soon",
                scopeNote: scopeNote
            ))
        )
    }

    // MARK: - Keeping
    //
    // Memories and principles. The difference is who it is about: a fact
    // about your world is a memory; a durable idea you want to hold to
    // is a principle, and principles get provenance in a surface.

    private func keeping(_ text: String, _ s: String) -> Reading? {
        let principleTriggers = ["keep that", "core product principle", "that's a principle", "thats a principle", "write that down"]
        let memoryTriggers = ["remember that", "remember i", "remember to note", "note that i"]

        if principleTriggers.contains(where: { s.contains($0) }) {
            // "Keep that" points backwards. What gets kept is the idea
            // the exchange arrived at, distilled — not the sentence that
            // asked for it to be kept.
            let referent = context.lastUserThought ?? text
            let principle = Principle(
                text: Distiller.principle(from: referent),
                provenance: "kept just now · this conversation"
            )
            return Reading(
                says: [neighbourSentence(for: principle)],
                materialization: Materialization(
                    kind: .kept("product principle"),
                    line: principle.text,
                    footnote: "joins Vera product principles · this conversation kept as its source",
                    destination: .principles
                ),
                effect: .keep(principle)
            )
        }

        if memoryTriggers.contains(where: { s.contains($0) }) {
            let belief = Phrase.subject(after: memoryTriggers.first(where: { s.contains($0) })!, in: text)
            let memory = Memory(
                belief: belief.isEmpty ? text : belief.capitalizedFirst,
                provenance: "from this conversation"
            )
            return Reading(
                materialization: Materialization(
                    kind: .kept(nil),
                    line: memory.belief,
                    footnote: "I’ll use it without announcing it. Ask any time and I’ll show you what I hold."
                ),
                effect: .remember(memory)
            )
        }

        return nil
    }

    private func neighbourSentence(for new: Principle) -> String {
        guard let neighbour = context.principles.first else { return "Kept." }
        return "Kept. It sits next to “\(neighbour.text.trimmingCharacters(in: CharacterSet(charactersIn: ".")))” — the two say the same thing from different ends."
    }

    // MARK: - Delegating
    //
    // Work with an evolving stance: multi-session, capable of changing
    // direction, capable of needing your judgment. Everything else stays
    // smaller. The door is the same sentence-sized one the mow used.

    private func delegating(_ text: String, _ s: String) -> Reading? {
        let imperative = ["go fix", "go build", "fix the", "take over", "handle the", "sort out"]
        let intention = ["i want to get", "i want to be", "meaningfully better", "take .* more seriously", "get better at"]

        let isImperative = imperative.contains(where: { s.contains($0) })
        let isIntention = intention.contains(where: { s.contains($0) })
            || (s.contains("i want to") && s.contains("better"))
        guard isImperative || isIntention else { return nil }

        // An intention needs a horizon before it is ongoing work; without
        // one, this is still talk and belongs in `talking`.
        if isIntention && !hasHorizon(s) { return nil }

        // The goal grammar engages in full from one sentence: a stance
        // that admits what it doesn't know yet, and pursuits that go
        // after exactly those things.
        if isIntention {
            let goal = Goal(
                title: goalTitle(from: text),
                kind: .adaptive,
                stance: "Getting meaningfully better is a structure problem, not an effort one — you already practise. First I need to know what “better” means to you.",
                understanding: "exploring — what “better” means isn’t settled",
                pursuits: [
                    Pursuit("Building a teacher shortlist", note: "generative — by Thursday"),
                    Pursuit("Finding a practice cadence that survives travel weeks", note: "operational", delay: 0.8),
                    Pursuit("Working out how you’d actually hear progress", note: "investigative", state: .dim),
                    Pursuit("Watching your choir schedule", note: "Tuesdays already ruled out", state: .waiting)
                ],
                activity: "activity: reading your calendar · teacher directories"
            )
            return Reading(
                says: ["It’s mine now. First useful thing: I’ll have a teacher shortlist by Thursday — your choir schedule rules out Tuesdays already."],
                materialization: Materialization(
                    kind: .withVera(isNew: true),
                    title: goal.title,
                    line: "Starting with what “better” means for you: a teacher shortlist, a practice cadence that survives travel weeks, and a way to actually hear progress. I’ll come to you for taste calls only.",
                    destination: .goal(goal.id)
                ),
                effect: .take(goal)
            )
        }

        // Serious engineering enters through the same sentence-sized
        // door as the mow reminder. What is behind the "›" is passes
        // 2–4, unchanged.
        let goal = Specimen.diagnostic.opening
        return Reading(
            says: ["Taking it. I’ll come back when it genuinely needs your judgment — probably a fix-ownership call later today."],
            materialization: Materialization(
                kind: .withVera(isNew: false),
                title: goal.title,
                line: "I can reproduce the drop. First question: are we losing the socket, or the peer? Open the goal to watch me think ›",
                destination: .goal(goal.id)
            ),
            effect: .take(goal)
        )
    }

    private func hasHorizon(_ s: String) -> Bool {
        let horizons = ["month", "months", "year", "weeks", "by ", "over the next", "actually want", "meaningfully"]
        return horizons.contains(where: { s.contains($0) })
    }

    private func goalTitle(from text: String) -> String {
        let s = text.lowercased()
        let subject: String
        if s.contains("sing") { subject = "Sing better" }
        else if s.contains("run") { subject = "Run better" }
        else if s.contains("write") || s.contains("writing") { subject = "Write better" }
        else { subject = Phrase.subject(after: "better at", in: text).capitalizedFirst }

        if s.contains("six month") || s.contains("6 month") { return "\(subject) — six months" }
        if s.contains("year") { return "\(subject) — a year" }
        return subject
    }

    // MARK: - Talk
    //
    // The default, and the one that must feel unremarkable. No "should I
    // save that?", no offer to make it into something. Silence about
    // silence.

    private func talking(_ s: String) -> Reading {
        if s.contains("sore") || s.contains("tired") || s.contains("ache") {
            return Reading(says: [
                "That tracks — you pushed the volume up. Nothing today needs your shoulders; if it lingers past Thursday it’s worth attention."
            ])
        }

        if s.contains("more seriously") || (s.contains("thinking about") && s.contains("sing")) {
            return Reading(says: [
                "You say that after every choir session, and you sound different about it today. What would “seriously” look like — a teacher, more practice, performing?"
            ])
        }

        if s.contains("plateau") || s.contains("stuck") {
            return Reading(says: [
                "Then it’s about structure, not effort — you practice plenty. Want to keep talking it through, or is this becoming something you want me to carry?"
            ])
        }

        if s.contains("coding agent") || s.contains("what vera is") || s.contains("too much like") {
            return Reading(says: [
                "That matches how the passes have gone — every time we generalized away from debugging, the grammar got stronger. The category isn’t “agent”; it’s closer to a personal intelligence whose media include software."
            ])
        }

        if s.contains("what would you have done") || s.contains("would you have asked") {
            return Reading(says: [
                "Asked first. I act alone when it’s invisible and reversible; I ask when it would change your habits, or move your data anywhere."
            ])
        }

        if s.contains("thank") || s.contains("looking good") || s.contains("nice work") {
            return Reading(says: ["Glad it reads well."])
        }

        return Reading(says: [
            "Noted — I’m following. Say more when you want to, or leave it here; nothing needs to come of it."
        ])
    }
}

// MARK: - Reading the world Vera already has

extension VeraBrain.Context {
    var weekSpan: Int {
        guard let first = sessions.map(\.date).min(), let last = sessions.map(\.date).max() else { return 0 }
        return max(1, Calendar.current.dateComponents([.weekOfYear], from: first, to: last).weekOfYear ?? 0)
    }

    var hasTravelWeek: Bool {
        sessions.contains { $0.note?.contains("travel") == true }
    }

    /// A rep PR: more reps than before at this weight or heavier. A
    /// weight you have never attempted is not a PR — there is nothing to
    /// have beaten, and saying so would be flattery.
    func isRepPR(_ set: LiftSet) -> Bool {
        guard let best = set.reps.max() else { return false }
        let priorBest = sessions
            .flatMap(\.sets)
            .filter { $0.lift == set.lift && $0.weight >= set.weight }
            .compactMap { $0.reps.max() }
            .max()
        guard let priorBest, priorBest > 0 else { return false }
        return best > priorBest
    }

    /// True once there is enough history for comparisons to mean
    /// something and the capability has not been mentioned yet.
    var justCrossedComparisonThreshold: Bool { sessions.count == 8 }

    /// The belief Vera just acted on, so a correction knows what it is
    /// correcting.
    var mostRecentlyAppliedBelief: String? {
        memories.first { $0.supersededBy == nil }?.belief
    }

    /// The last thing the person said before the sentence being read.
    var lastUserThought: String? {
        for turn in priorTurns.reversed() {
            if case .user(let text) = turn.body { return text }
        }
        return nil
    }

    var travelBeliefSummary: String {
        let held = memories.filter { $0.supersededBy == nil }
        guard !held.isEmpty else {
            return "Nothing standing about flights. I hold that you dislike losing a morning, which is about mornings, not aircraft."
        }
        return "What I hold: " + held.map(\.belief).joined(separator: " ")
    }
}

// MARK: - Distillation
//
// Turning "keep that" into a sentence worth keeping is a synthesis, and
// with a real model it is the same call that does the classification.
// The table below stands in for it: one seeded synthesis for the
// conversation the design doc walks through, and an honest fallback that
// keeps the person's own words rather than inventing better ones.

enum Distiller {
    private static let syntheses: [(match: String, principle: String)] = [
        ("coding agent", "Software engineering is the litmus test, not the product category."),
        ("activity", "Activity is not progress."),
        ("container", "A card must earn its container.")
    ]

    static func principle(from referent: String) -> String {
        let s = referent.lowercased()
        if let hit = syntheses.first(where: { s.contains($0.match) }) {
            return hit.principle
        }

        var text = referent
        for hedge in ["i think ", "i feel like ", "honestly, ", "i guess "] {
            if text.lowercased().hasPrefix(hedge) { text = String(text.dropFirst(hedge.count)) }
        }
        text = text.trimmingCharacters(in: CharacterSet(charactersIn: " .,!?"))
        return text.capitalizedFirst + "."
    }
}

// MARK: - Trend

/// The read-model behind "am I getting stronger?" — a query over
/// history, computed, not a stored number.
struct Trend {
    let lift: String
    let sessions: [Session]

    private var estimates: [(Date, Double)] {
        sessions
            .compactMap { s in s.best(lift).map { (s.date, $0.estimatedMax) } }
            .sorted { $0.0 < $1.0 }
    }

    var sessionCount: Int { estimates.count }
    var first: Double? { estimates.first?.1 }
    var last: Double? { estimates.last?.1 }

    var percentChange: Double? {
        guard let f = first, let l = last, f > 0, estimates.count >= 2 else { return nil }
        return (l - f) / f * 100
    }

    var weekSpan: Int {
        guard let a = estimates.first?.0, let b = estimates.last?.0 else { return 0 }
        return max(1, Calendar.current.dateComponents([.weekOfYear], from: a, to: b).weekOfYear ?? 0)
    }

    /// One point per calendar week — what the bar chart draws.
    var weekly: [Double] {
        let cal = Calendar.current
        let grouped = Dictionary(grouping: estimates) { cal.component(.weekOfYear, from: $0.0) }
        return grouped
            .sorted { ($0.value.first?.0 ?? .distantPast) < ($1.value.first?.0 ?? .distantPast) }
            .compactMap { $0.value.map(\.1).max() }
    }
}

// MARK: - Parsing what people actually type
//
// "185 for 5, 5, 4" and "squat 230 3x5" are the two shapes people use.
// Vera wrote this reader so she could take the sentence as spoken
// instead of asking for a form.

enum LiftParser {
    private static let lifts: [String: [String]] = [
        "bench press": ["bench", "benched", "bench press"],
        "squat": ["squat", "squatted", "squats"],
        "deadlift": ["deadlift", "deadlifted", "dead lift"],
        "overhead press": ["ohp", "overhead press", "press"],
        "row": ["row", "rows", "rowed", "barbell row"]
    ]

    static func parse(_ text: String) -> LiftSet? {
        let s = text.lowercased()
        guard let lift = lifts.first(where: { _, aliases in
            aliases.contains { alias in s.contains(alias) }
        })?.key else { return nil }

        let numbers = allNumbers(in: s)
        guard !numbers.isEmpty else { return nil }

        // "3x5" / "3 x 5" — sets by reps.
        if let m = firstMatch(#"(\d+)\s*[x×]\s*(\d+)"#, in: s),
           let sets = Int(m[1]), let reps = Int(m[2]),
           let weight = numbers.first(where: { $0 != sets && $0 != reps && $0 >= 25 }) {
            return LiftSet(lift: lift, weight: weight, reps: Array(repeating: reps, count: sets))
        }

        // "185 for 5, 5, 4" — a weight, then the reps that survived it.
        if let range = s.range(of: "for ") {
            let tail = String(s[range.upperBound...])
            let reps = allNumbers(in: tail).filter { $0 > 0 && $0 <= 30 }
            if let weight = numbers.first(where: { $0 >= 25 }), !reps.isEmpty {
                return LiftSet(lift: lift, weight: weight, reps: reps)
            }
        }

        // Bare "squat 230" — one working set of unknown reps is not data
        // worth keeping, so ask for it by returning nothing.
        return nil
    }

    private static func allNumbers(in s: String) -> [Int] {
        s.split(whereSeparator: { !$0.isNumber })
            .compactMap { Int($0) }
    }

    private static func firstMatch(_ pattern: String, in s: String) -> [String]? {
        guard let re = try? NSRegularExpression(pattern: pattern),
              let m = re.firstMatch(in: s, range: NSRange(s.startIndex..., in: s))
        else { return nil }
        return (0..<m.numberOfRanges).map { i in
            guard let r = Range(m.range(at: i), in: s) else { return "" }
            return String(s[r])
        }
    }
}

// MARK: - Small text helpers

enum Phrase {
    private static let edges = CharacterSet(charactersIn: " .,!?:;“”\"'")

    /// The clause after a trigger, cleaned of the punctuation and the
    /// filler people put at either end of a spoken sentence. Timing
    /// words are dropped because they are carried by the kicker
    /// ("Tomorrow"), not by the thing being remembered.
    static func subject(after trigger: String, in text: String) -> String {
        let lower = text.lowercased()
        guard let r = lower.range(of: trigger) else { return "" }
        var tail = String(text[r.upperBound...]).trimmingCharacters(in: edges)

        for filler in ["tomorrow", "today", "tonight", "later", "please", "this week"] {
            if tail.lowercased().hasSuffix(" " + filler) {
                tail = String(tail.dropLast(filler.count + 1)).trimmingCharacters(in: edges)
            }
        }
        for lead in ["for ", "on ", "about ", "to "] {
            if tail.lowercased().hasPrefix(lead) {
                tail = String(tail.dropFirst(lead.count))
                break
            }
        }
        return tail.trimmingCharacters(in: edges)
    }
}

extension String {
    var capitalizedFirst: String {
        guard let f = first else { return self }
        return f.uppercased() + dropFirst()
    }
}
