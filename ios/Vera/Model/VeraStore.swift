import Foundation
import Observation
import SwiftUI

// One relationship, one store.
//
// There is no per-screen state object because there are no modules —
// Home, the conversation and the personal surfaces are three views of
// the same structures. A consequence materialized in conversation
// changes what Home says and what a surface shows, because they read
// the same arrays.

@Observable
@MainActor
final class VeraStore {

    // MARK: - Navigation

    var path: [Route] = []
    /// A scaffold for reading the design doc on device, not product UI.
    /// Reached by long-pressing the wordmark.
    var showingWalkthrough = false
    var showingUnderTheHood = false

    // MARK: - The conversation

    private(set) var turns: [Turn] = []
    var draft = ""
    /// The composer inside a goal raises the veil instead of pushing a screen.
    var veilRaised = false

    // MARK: - Structure

    private(set) var sessions = Seed.sessions
    private(set) var principles = Seed.principles
    private(set) var memories: [Memory] = []
    private(set) var watches = Seed.watches
    private(set) var reminders: [Reminder] = []
    private(set) var goals: [Goal] = Seed.goals

    /// Walkthrough scaffold: when set, the goal page is being stepped
    /// through a specimen's transformations rather than shown live.
    var walkingSpecimen: String?
    var beat = 0

    private(set) var changes: [ChangeNote] = Seed.changes
    /// Set while a conversation is happening inside a goal's context.
    var currentGoal: Goal?

    // MARK: - Home

    /// Home reads the same goals every other surface reads. It shows
    /// five of them; the selection policy decides which, and Vera is
    /// accountable for the ones she chose not to show.
    var selection: HomeSelection { HomeSelection(goals: goals) }

    /// The one ask worth a container, if there is one.
    var attention: Goal? { selection.card }

    enum HomeState {
        /// Something crossed the threshold into needing a person.
        case needsYou(HomeSelection)
        /// Nothing needs you, but your picture of the work changed.
        case changed(HomeSelection, [ChangeNote])
        /// A user with no delegated work at all. Structure they grew
        /// from talking is still worth opening for.
        case around([SurfaceLink])
        case silence
    }

    var homeState: HomeState {
        let selection = selection
        if selection.card != nil { return .needsYou(selection) }
        if selection.withVeraCount > 0 || !changes.isEmpty {
            return .changed(selection, changes)
        }
        let links = surfaceLinks
        if !links.isEmpty { return .around(links) }
        return .silence
    }

    /// "Around you" — personal structures that grew from talking, derived
    /// from what actually exists rather than a list of installed modules.
    var surfaceLinks: [SurfaceLink] {
        var links: [SurfaceLink] = []

        if let latest = sessions.map(\.date).max() {
            let bench = Trend(lift: "bench press", sessions: sessions)
            let direction = (bench.percentChange ?? 0) > 1 ? "bench trending up" : "steady"
            links.append(SurfaceLink(
                name: "Your training",
                detail: "logged \(Self.relativeDay(latest)) · \(direction)",
                destination: .training
            ))
        }

        if !principles.isEmpty {
            let recent = principles.prefix(2).count
            links.append(SurfaceLink(
                name: "Product principles",
                detail: "\(Self.spelled(recent)) added this week",
                destination: .principles
            ))
        }

        for watch in watches where watch.quiet {
            links.append(SurfaceLink(
                name: "Watching: \(watch.subject.lowercased())",
                detail: "nothing yet",
                isQuiet: true
            ))
        }

        return links
    }

    /// The composer's copy follows what Vera is holding. It is the only
    /// place in the product where chrome asks for attention, and it says
    /// exactly why.
    var composerPlaceholder: String {
        if currentGoal != nil { return "…" }
        guard let card = attention else { return "What’s on your mind?" }
        return card.stakes?.blocked != nil
            ? "One decision is blocking — answer here works too"
            : "Answer here works too — or anything else"
    }

    // MARK: - Talking

    private var brain: VeraBrain {
        VeraBrain(context: .init(
            sessions: sessions,
            principles: principles,
            watches: watches,
            memories: memories,
            pendingAsk: attention,
            currentGoal: currentGoal,
            priorTurns: turns
        ))
    }

    func send() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        draft = ""
        say(text)
    }

    func say(_ text: String) {
        // Read before appending, so "keep that" can see what it points at.
        let reading = brain.read(text)
        turns.append(.user(text))

        if let effect = reading.effect { apply(effect) }

        // Motion marks the change in ownership: the consequence lands
        // before the sentence that acknowledges it.
        if let m = reading.materialization { turns.append(.material(m)) }
        for line in reading.says { turns.append(.vera(line)) }
        if let stat = reading.stat { turns.append(.stat(stat)) }
        if !reading.chips.isEmpty { turns.append(.chips(reading.chips)) }
    }

    /// Vera speaking first — opening a decision, or explaining a change.
    func veraOpens(_ line: String) {
        turns.append(.vera(line))
    }

    func clearConversation() {
        turns.removeAll()
    }

    // MARK: - Consequences becoming structure

    private func apply(_ effect: Reading.Effect) {
        switch effect {
        case .log(let set, let dayLabel):
            let date = dayLabel == "yesterday" ? Seed.day(1) : Seed.today
            if let i = sessions.firstIndex(where: { Calendar.current.isDate($0.date, inSameDayAs: date) }) {
                sessions[i].sets.append(set)
            } else {
                sessions.append(Session(date: date, sets: [set]))
            }
            sessions.sort { $0.date < $1.date }

        case .keep(let principle):
            principles.insert(principle, at: 0)

        case .remember(let memory):
            memories.insert(memory, at: 0)

        case .revise(let belief, let correction):
            if let i = memories.firstIndex(where: { $0.belief == belief }) {
                memories[i].supersededBy = correction
            } else {
                memories.insert(
                    Memory(belief: belief, provenance: "inferred earlier", supersededBy: correction),
                    at: 0
                )
            }

        case .watch(let item):
            watches.append(item)

        case .remind(let reminder):
            reminders.append(reminder)

        case .take(let goal):
            if let i = goals.firstIndex(where: { $0.id == goal.id }) {
                goals[i] = goal
            } else {
                goals.append(goal)
            }

        case .answerAsk(let goalID, let choice):
            guard let i = goals.firstIndex(where: { $0.id == goalID }) else { return }
            goals[i].incorporate(
                "“Go with \(choice.keyword).”",
                as: "From you: \(choice.title.lowercased())",
                said: "just now",
                standsUntil: "you lift it"
            )
            goals[i].note("your call is part of the model now", live: true)
            goals[i].stakes = nil
            goals[i].standing = .moving
            goals[i].digest = "\(choice.title) — landing now."
            changes.insert(
                ChangeNote(
                    lead: "Settled:",
                    text: "\(goals[i].title.lowercased()) takes the \(choice.keyword) route — \(choice.body.first?.lowercased() ?? " ")\(choice.body.dropFirst())",
                    why: "You chose \(choice.keyword). \(choice.body) The alternative was sound too; it just traded a different cost."
                ),
                at: 0
            )
        }
    }

    // MARK: - Goals

    func goal(_ id: UUID) -> Goal? {
        if let specimen = walkingSpecimen.flatMap(Specimen.named) {
            return specimen.state(at: beat)
        }
        return goals.first { $0.id == id }
    }

    /// Answering at the judgment boundary. The buttons and the composer
    /// are the same door — both land here.
    func answer(_ id: UUID, with choice: Decision.Choice) {
        // In a specimen, the next beat *is* the incorporation, so the
        // buttons step it rather than mutating a copy that gets folded
        // over on the next redraw.
        if walkingSpecimen != nil {
            step(1)
            return
        }
        guard let i = goals.firstIndex(where: { $0.id == id }) else { return }
        goals[i].incorporate(
            "“\(choice.title).”",
            as: "Your call: \(choice.title.lowercased())",
            said: "just now"
        )
        goals[i].lifecycle = .withVera
        goals[i].note("your call is part of the model now", live: true)
    }

    func step(_ delta: Int) {
        guard let specimen = walkingSpecimen.flatMap(Specimen.named) else { return }
        withAnimation(.easeOut(duration: 0.5)) {
            beat = max(0, min(specimen.beats.count, beat + delta))
        }
    }

    // MARK: - Entering the conversation from elsewhere

    /// The attention card's "Decide". The question is already on the
    /// table, so Vera says it rather than making you restate it.
    func openDecision(_ goal: Goal) {
        guard let decision = goal.decision else { return }
        if turns.isEmpty {
            veraOpens("\(decision.recommended.title), or \(decision.alternative.title.lowercased())? I recommend \(decision.recommended.keyword) — \(decision.recommended.body.first?.lowercased() ?? " ")\(decision.recommended.body.dropFirst())")
        }
        path.append(.conversation)
    }

    func openConversation() {
        path.append(.conversation)
    }

    /// "why ›" on a change note. Vera does not restate the change; she
    /// answers the question the arrow asked.
    func explain(_ note: ChangeNote) {
        guard let why = note.why else { return }
        veraOpens(why)
        path.append(.conversation)
    }

    func go(_ route: Route) {
        path.append(route)
    }

    // MARK: - Walkthrough scaffold
    //
    // The design doc's states, reachable for review. These set real
    // state rather than faking a screen, so what you see is what the
    // running app produces.

    enum WalkthroughEntry: String, CaseIterable, Identifiable {
        case homeNeedsYou = "5a"
        case homeChanged = "5b"
        case homeSilence = "5c"
        case homeAround = "5d"
        case script = "5f"
        case morning = "5i"
        case talkThenLog = "5j"
        case stronger = "5k"
        case watchThenSinging = "5l"
        case intention = "5m"
        case afternoon = "5n"
        case delegation = "5o"
        case veil = "5p"
        case retrieval = "5q"
        case training = "5r"
        case principlesSurface = "5s"
        case memoryCorrection = "5t"
        case capability = "5u"
        // Passes 2–4 — the goal page, walked through its transformations.
        case goalDiagnostic = "2g"
        case goalCreative = "3d"
        case goalAdaptive = "3e"

        var id: String { rawValue }

        var title: String {
            switch self {
            case .homeNeedsYou: "Home — something needs you"
            case .homeChanged: "Home — nothing needed, things changed"
            case .homeSilence: "Home — comfortable silence"
            case .homeAround: "Home — a user who mostly talks"
            case .script: "The script — a principle kept"
            case .morning: "8:00 — the briefing"
            case .talkThenLog: "8:05 — talk, then a consequence"
            case .stronger: "8:10 — am I getting stronger?"
            case .watchThenSinging: "11:00 — a watch, then a thought"
            case .intention: "12:40 — intention becomes work"
            case .afternoon: "2:00 — what should I focus on?"
            case .delegation: "3:00 — go fix the reconnection bug"
            case .veil: "5:30 — the veil, inside a goal"
            case .retrieval: "Evening — what did we decide?"
            case .training: "Your training"
            case .principlesSurface: "Vera product principles"
            case .memoryCorrection: "Correcting a memory"
            case .capability: "A capability grows"
            case .goalDiagnostic: "The goal page — diagnostic, 8 beats"
            case .goalCreative: "The goal page — creative, 8 beats"
            case .goalAdaptive: "The goal page — adaptive, 6 beats"
            }
        }

        var specimen: Specimen? {
            switch self {
            case .goalDiagnostic: Specimen.diagnostic
            case .goalCreative: Specimen.creative
            case .goalAdaptive: Specimen.adaptive
            default: nil
            }
        }
    }

    init() {
        // `-scene 5j` on the launch arguments drops straight into a
        // walkthrough state. Review scaffold; harmless in a shipped
        // build because nothing sets the default.
        if let raw = UserDefaults.standard.string(forKey: "scene"),
           let entry = WalkthroughEntry(rawValue: raw) {
            enter(entry)
            // `-beat 4` alongside it lands on a specific transformation.
            if let specimen = entry.specimen,
               UserDefaults.standard.object(forKey: "beat") != nil {
                beat = max(0, min(specimen.beats.count, UserDefaults.standard.integer(forKey: "beat")))
            }
        }
    }

    func enter(_ entry: WalkthroughEntry) {
        showingWalkthrough = false
        path.removeAll()
        clearConversation()
        currentGoal = nil
        veilRaised = false
        walkingSpecimen = nil
        beat = 0

        if let specimen = entry.specimen {
            walkingSpecimen = specimen.id
            path.append(.goal(specimen.opening.id))
            return
        }

        switch entry {
        case .homeNeedsYou:
            goals = Seed.goals
            changes = Seed.changes

        case .homeChanged:
            // The same installation with the one ask answered.
            goals = Seed.goals.filter { $0.lifecycle != .needsYou }
            changes = Seed.changes

        case .homeSilence:
            goals = []
            changes = []
            sessions = []
            principles = []
            watches = []

        case .homeAround:
            // Someone who mostly talks: no delegated work at all, and
            // Vera is still worth opening.
            goals = []
            changes = []
            restoreStructure()

        case .script:
            restoreStructure()
            say("I think we’ve been treating Vera too much like a coding agent.")
            say("Actually, keep that. I think it’s a core product principle.")
            path.append(.conversation)

        case .morning:
            path.append(.conversation)
            say("Morning. Anything I need to know?")

        case .talkThenLog:
            path.append(.conversation)
            say("My shoulders are pretty sore from yesterday.")
            say("Oh — log that I benched 185 for 5, 5, 4 yesterday.")

        case .stronger:
            path.append(.conversation)
            say("Am I actually getting stronger?")

        case .watchThenSinging:
            path.append(.conversation)
            say("Keep an eye out for my passport email.")
            say("I’ve been thinking about taking singing more seriously.")
            say("Honestly, all three eventually. Mostly I want to stop plateauing.")

        case .intention:
            path.append(.conversation)
            say("Yeah. I actually want to get meaningfully better at singing over the next six months.")

        case .afternoon:
            path.append(.conversation)
            say("What should I focus on this afternoon?")

        case .delegation:
            path.append(.conversation)
            say("Go fix the reconnection bug.")

        case .veil:
            let goal = Seed.lanInFlight
            goals = [goal]
            currentGoal = goal
            path.append(.goal(goal.id))
            veilRaised = true
            say("This is looking good. Oh — remind me to mow tomorrow.")

        case .retrieval:
            path.append(.conversation)
            say("What did we decide earlier about what Vera actually is?")

        case .training:
            path.append(.training)

        case .principlesSurface:
            path.append(.principles)

        case .memoryCorrection:
            path.append(.conversation)
            veraOpens("…I’d book the nonstop — you’ve said overnight layovers ruin your next day.")
            say("No — don’t treat that as a standing preference. I was just cranky today.")

        case .capability:
            sessions = Array(Seed.sessions.prefix(8))
            path.append(.conversation)
            say("Log squat 230 3x5.")
            say("Good. What would you have done if it changed how I log?")

        case .goalDiagnostic, .goalCreative, .goalAdaptive:
            break // handled above, by the specimen branch
        }
    }

    private func restoreStructure() {
        if sessions.isEmpty { sessions = Seed.sessions }
        if principles.isEmpty { principles = Seed.principles }
        if watches.isEmpty { watches = Seed.watches }
    }

    // MARK: - Formatting

    static func relativeDay(_ date: Date) -> String {
        let cal = Calendar.current
        if cal.isDateInToday(date) { return "today" }
        if cal.isDateInYesterday(date) { return "yesterday" }
        let days = cal.dateComponents([.day], from: date, to: Date()).day ?? 0
        if days < 7 {
            let f = DateFormatter()
            f.dateFormat = "EEEE"
            return f.string(from: date)
        }
        return "\(days / 7) week\(days / 7 == 1 ? "" : "s") ago"
    }

    private static func spelled(_ n: Int) -> String {
        ["zero", "one", "two", "three", "four", "five"].indices.contains(n)
            ? ["zero", "one", "two", "three", "four", "five"][n]
            : "\(n)"
    }
}
