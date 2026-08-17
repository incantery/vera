import Testing
import Foundation
@testable import Vera

// The claims v5 makes that are checkable: the classifier's five
// outcomes, the reader that takes a sentence as spoken, the rule that
// decides which turns recede, and the arithmetic behind "am I getting
// stronger?".

// MARK: - 5h · Five utterances, five outcomes

@MainActor
@Suite("Consequence range")
struct ConsequenceRange {
    private func read(_ s: String, _ context: VeraBrain.Context = .init(sessions: Seed.sessions)) -> Reading {
        VeraBrain(context: context).read(s)
    }

    @Test("Sore shoulders leave nothing behind")
    func talkLeavesNothing() {
        let r = read("My shoulders are pretty sore from yesterday.")
        #expect(r.materialization == nil)
        #expect(r.effect == nil)
        #expect(!r.says.isEmpty, "it is still a conversation, just not a consequence")
    }

    @Test("A preference becomes a memory, held in a dashed block")
    func preferenceBecomesMemory() {
        let r = read("Remember I hate overnight flights.")
        #expect(r.materialization?.kind == .kept(nil))
        #expect(r.materialization?.kind.isDashed == true)
        if case .remember = r.effect {} else { Issue.record("expected a memory effect") }
    }

    @Test("A logged set becomes structured data")
    func setBecomesData() {
        let r = read("I benched 185 for 5, 5, 4.")
        #expect(r.materialization?.kind == .logged)
        guard case .log(let set, _)? = r.effect else {
            Issue.record("expected a log effect"); return
        }
        #expect(set.lift == "bench press")
        #expect(set.weight == 185)
        #expect(set.reps == [5, 5, 4])
    }

    @Test("A trigger becomes a watch")
    func triggerBecomesWatch() {
        let r = read("Keep an eye out for the passport email.")
        #expect(r.materialization?.kind == .watching)
        guard case .watch(let item)? = r.effect else {
            Issue.record("expected a watch effect"); return
        }
        #expect(item.subject.lowercased().contains("passport"))
        #expect(!item.subject.lowercased().hasPrefix("for "), "the trigger word is not part of the subject")
    }

    @Test("An intention with a horizon becomes ongoing work")
    func intentionBecomesGoal() {
        let r = read("I want to get meaningfully better at singing over the next six months.")
        #expect(r.materialization?.kind == .withVera(isNew: true))
        #expect(r.materialization?.title == "Sing better — six months")
        if case .take = r.effect {} else { Issue.record("expected a goal effect") }
    }

    @Test("An intention without a horizon stays talk")
    func intentionWithoutHorizonStaysTalk() {
        let r = read("I’ve been thinking about taking singing more seriously.")
        #expect(r.materialization == nil, "Vera names the fork instead of manufacturing a plan")
        #expect(r.says.first?.contains("seriously") == true)
    }

    @Test("A reminder inside a goal states its scope out loud")
    func reminderStatesScope() {
        var context = VeraBrain.Context(sessions: Seed.sessions)
        context.currentGoal = Seed.lanInFlight
        let r = read("This is looking good. Oh — remind me to mow tomorrow.", context)

        #expect(r.materialization?.kind == .tomorrow)
        #expect(r.materialization?.footnote?.contains("not part of this goal") == true)
        #expect(r.materialization?.line == "Mow", "the timing word belongs to the kicker, not the task")
    }

    @Test("A correction supersedes rather than deletes")
    func correctionSupersedes() {
        var context = VeraBrain.Context(sessions: [])
        context.memories = [Memory(belief: "Prefers nonstop; overnight layovers ruin the next day.", provenance: "inferred")]
        let r = read("No — don’t treat that as a standing preference. I was just cranky today.", context)

        #expect(r.materialization?.kind == .revised)
        #expect(r.materialization?.struckLine == "Prefers nonstop; overnight layovers ruin the next day.")
        #expect(r.materialization?.line.contains("One-time remark") == true)
    }
}

// MARK: - The reader Vera wrote for herself

@Suite("Lift parsing")
struct LiftParsing {
    @Test("Reads a weight and the reps that survived it")
    func readsForList() throws {
        let set = try #require(LiftParser.parse("Oh — log that I benched 185 for 5, 5, 4 yesterday."))
        #expect(set.lift == "bench press")
        #expect(set.weight == 185)
        #expect(set.reps == [5, 5, 4])
    }

    @Test("Reads sets-by-reps")
    func readsCrossNotation() throws {
        let set = try #require(LiftParser.parse("Log squat 230 3x5."))
        #expect(set.lift == "squat")
        #expect(set.weight == 230)
        #expect(set.reps == [5, 5, 5])
    }

    @Test("A sentence with no set in it is not a log")
    func rejectsProse() {
        #expect(LiftParser.parse("My shoulders are pretty sore from yesterday.") == nil)
        #expect(LiftParser.parse("Am I actually getting stronger?") == nil)
        // A weight with no reps is not data worth keeping.
        #expect(LiftParser.parse("squat 230") == nil)
    }

    @Test("Epley, and it is only ever approximate")
    func estimatedMax() {
        let set = LiftSet(lift: "bench press", weight: 185, reps: [5, 5, 4])
        #expect(abs(set.estimatedMax - 215.83) < 0.01)
    }
}

// MARK: - Ageing
//
// The four cases the design doc draws. An exchange ends when a
// consequence lands, so the boundary moves with materializations, not
// with a fixed turn count.

@Suite("Which turns recede")
struct Ageing {
    @Test("5f — the consequence is the tail's own, so its cause stays live")
    func consequenceIsTheTail() {
        let turns: [Turn] = [
            .user("I think we’ve been treating Vera too much like a coding agent."),
            .vera("That matches how the passes have gone…"),
            .user("Actually, keep that."),
            .material(Materialization(kind: .kept("product principle"), line: "…")),
            .vera("Kept.")
        ]
        #expect(turns.liveWindowStart == 2)
    }

    @Test("5l — a new topic after a consequence leaves it behind")
    func newTopicAfterConsequence() {
        let turns: [Turn] = [
            .user("Keep an eye out for my passport email."),
            .material(Materialization(kind: .watching, line: "…")),
            .user("I’ve been thinking about taking singing more seriously."),
            .vera("What would “seriously” look like?"),
            .user("Mostly I want to stop plateauing."),
            .vera("Then it’s about structure, not effort.")
        ]
        #expect(turns.liveWindowStart == 2)
    }

    @Test("5u — a consequence that opens the exchange recedes on its own")
    func consequenceOpensExchange() {
        let turns: [Turn] = [
            .material(Materialization(kind: .logged, line: "Squat · 230 lb")),
            .vera("Logged. One more thing…"),
            .user("What would you have done if it changed how I log?"),
            .vera("Asked first.")
        ]
        #expect(turns.liveWindowStart == 1)
    }

    @Test("A conversation with no consequence is entirely live")
    func noConsequence() {
        let turns: [Turn] = [.user("Morning. Anything I need to know?"), .vera("Quiet night.")]
        #expect(turns.liveWindowStart == 0)
    }
}

// MARK: - The read-model behind "am I getting stronger?"

@Suite("Training trend")
struct TrainingTrend {
    @Test("Pressing climbs, and the figure is derived rather than stored")
    func benchClimbs() throws {
        let bench = Trend(lift: "bench press", sessions: Seed.sessions)
        let first = try #require(bench.first)
        let last = try #require(bench.last)
        let change = try #require(bench.percentChange)

        #expect(last > first)
        #expect(change > 5 && change < 12)
        #expect(bench.weekly.count >= 6)
    }

    @Test("Squat is under-sampled, and saying so is the honest answer")
    func squatUnderSampled() {
        let squat = Trend(lift: "squat", sessions: Seed.sessions)
        #expect(squat.sessionCount == 4)
    }

    @Test("A weight never attempted before is not a record")
    func firstAttemptIsNotAPR() {
        let context = VeraBrain.Context(sessions: Seed.sessions)
        // 185 has been attempted for four reps, so five is a PR.
        #expect(context.isRepPR(LiftSet(lift: "bench press", weight: 185, reps: [5, 5, 4])) == false,
                "yesterday already holds five at 185")
        // Nothing at 315 has ever been tried; beating nothing is not a PR.
        #expect(context.isRepPR(LiftSet(lift: "deadlift", weight: 315, reps: [5])) == false)
    }
}

// MARK: - The goal page · passes 2–4
//
// Pass 3's claim is that five primitives and eight transformations carry
// three kinds of work. These check the claim rather than the drawing.

@Suite("The transformation vocabulary")
struct Transformations {
    private func goal() -> Goal {
        Goal(
            title: "Test",
            kind: .diagnostic,
            stance: "First belief.",
            understanding: "exploring",
            pursuits: [
                Pursuit("Alpha", note: "a"),
                Pursuit("Beta", note: "b"),
                Pursuit("Gamma", state: .dim)
            ]
        )
    }

    @Test("Supersede preserves the old belief as history, not as a deletion")
    func supersede() {
        var g = goal()
        g.supersede(with: "Second belief.", understanding: "revised", because: "evidence", at: "14:22")

        #expect(g.stance == "Second belief.")
        #expect(g.strata.map(\.text) == ["First belief."])
        #expect(g.understandingIsLive)
        #expect(g.strata[0].provenance.rows.contains { $0.value == "evidence" })
    }

    @Test("Only the last two strata are drawn, and the rest collapse behind an ellipsis")
    func strataCompress() {
        var g = goal()
        for i in 1...4 {
            g.supersede(with: "Belief \(i).", understanding: "revised", because: "r", at: "t")
        }
        #expect(g.strata.count == 4)
        let visible = g.visibleStrata
        #expect(visible.count == 2)
        #expect(visible.first?.elided == true, "older history exists and says so")
        #expect(visible.last?.elided == false)
    }

    @Test("Combine folds the absorbed line under its survivor, and the survivor leads")
    func combine() {
        var g = goal()
        g.combine("Gamma", into: "Beta", rename: "Beta, widened", note: "two lines converged")

        #expect(g.pursuits.first?.text == "Beta, widened")
        #expect(g.pursuits.first?.state == .lead)
        #expect(g.pursuits[1].state == .merged)
        #expect(g.pursuits[1].text.contains("Gamma"))
    }

    @Test("Eliminate strikes the line and buys a ledger entry")
    func eliminate() {
        var g = goal()
        g.eliminate("Alpha", reason: "ruled out 14:14", ledger: "socket loss")

        #expect(g.pursuits.last?.text == "Alpha", "struck lines fall to the bottom")
        #expect(g.pursuits.last?.state == .out)
        #expect(g.setAsideLine == "Ruled out so far: socket loss")
    }

    @Test("The ledger's label follows the kind of work, not the debugger")
    func ledgerLabels() {
        var diagnostic = goal()
        diagnostic.eliminate("Alpha", reason: "r", ledger: "a theory")
        #expect(diagnostic.setAsideLine?.hasPrefix("Ruled out so far") == true)

        var creative = goal()
        creative.kind = .creative
        creative.park("Alpha", reason: "r", ledger: "a direction")
        #expect(creative.setAsideLine?.hasPrefix("Set aside so far") == true)

        var adaptive = goal()
        adaptive.kind = .adaptive
        adaptive.park("Alpha", reason: "r", ledger: "gutters")
        #expect(adaptive.setAsideLine == "Deferred on purpose: gutters")
    }

    @Test("Parking never reads as failure — the line stays, with its reason")
    func park() {
        var g = goal()
        g.park("Alpha", becoming: "Alpha — parked", reason: "may serve marketing", ledger: "alpha")

        let parked = g.pursuits.first { $0.text == "Alpha — parked" }
        #expect(parked?.state == .waiting)
        #expect(parked?.note == "may serve marketing")
    }

    @Test("A direction that was never a pursuit can still be parked")
    func parkSomethingNew() {
        var g = goal()
        g.park("Expressive diagram", reason: "heavier daily", ledger: "diagram")
        #expect(g.pursuits.contains { $0.text == "Expressive diagram" && $0.state == .waiting })
    }

    @Test("Elevate moves an observation out of the pursuits and into a hollow chip")
    func elevate() {
        var g = goal()
        g.elevate("Gamma", asPrinciple: "a card must earn its container", from: "the audit", when: "3 weeks ago")

        #expect(g.marks.last?.kind == .principle)
        #expect(g.marks.last?.text.contains("a card must earn its container") == true)
        #expect(g.pursuits.first { $0.text == "Gamma" }?.state == .done)
    }

    @Test("Incorporate docks your words and flips ownership back")
    func incorporate() {
        var g = goal()
        g.ask(
            Decision(
                intro: "i",
                recommended: .init(title: "a", body: "b", keyword: "alpha"),
                alternative: .init(title: "c", body: "d", keyword: "gamma"),
                after: "then this"
            ),
            understanding: "paused at a judgment boundary"
        )
        #expect(g.lifecycle == .needsYou)

        g.incorporate("“Do it.”", as: "From you: do it", standsUntil: "you lift it")

        #expect(g.lifecycle == .withVera, "the ownership flip reverses on answer")
        #expect(g.decision == nil)
        #expect(g.saidByYou == "“Do it.”")
        #expect(g.marks.last?.kind == .yours)
        #expect(g.marks.last?.provenance?.rows.contains { $0.label == "stands until:" } == true)
    }

    @Test("Branch splits one line into two genuinely distinct ones")
    func branch() {
        var g = goal()
        g.branch("Beta", into: [Pursuit("Beta — implement"), Pursuit("Beta — verify", state: .dim)])

        #expect(g.pursuits.count == 4)
        #expect(g.pursuits[1].text == "Beta — implement")
        #expect(g.pursuits[2].text == "Beta — verify")
    }

    @Test("Settling stops the pulses and keeps what was learned")
    func settle() {
        var g = goal()
        g.settle(
            Outcome(text: "done", keptInMemory: "the lesson"),
            understanding: "understanding resolved · concluded",
            summary: Pursuit("All checks clean", state: .done)
        )

        #expect(g.lifecycle == .done)
        #expect(g.pursuits.count == 1)
        #expect(!g.pursuits.contains { $0.state.isLive }, "nothing breathes on a settled goal")
        #expect(g.outcome?.keptInMemory == "the lesson")
    }

    @Test("Lifecycle and understanding never share a slot")
    func twoDimensions() {
        // High confidence while Vera still owns it.
        var executing = Goal(title: "t", kind: .diagnostic, stance: "It's rediscovery.", understanding: "understanding resolved · executing")
        executing.lifecycle = .withVera
        #expect(executing.lifecycle == .withVera && executing.understanding.contains("resolved"))

        // Honest uncertainty, also while Vera owns it.
        let uncertain = Goal(title: "t", kind: .creative, stance: "I don’t know yet.", understanding: "exploring — honest uncertainty")
        #expect(uncertain.lifecycle == .withVera && uncertain.understanding.contains("exploring"))

        // Concluded lifecycle, open understanding.
        var concluded = uncertain
        concluded.settle(
            Outcome(text: "t", keptInMemory: "m"),
            understanding: "iteration concluded · the subject stays open",
            summary: Pursuit("done", state: .done)
        )
        #expect(concluded.lifecycle == .done)
        #expect(concluded.understanding.contains("stays open"))
    }
}

@Suite("The three specimens")
struct Specimens {
    @Test("Every specimen reaches Done through transformations alone", arguments: Specimen.all)
    func reachesDone(specimen: Specimen) {
        let final = specimen.state(at: specimen.beats.count)

        #expect(final.lifecycle == .done, "\(specimen.name) should conclude")
        #expect(final.outcome != nil, "\(specimen.name) should produce an outcome")
        #expect(!final.strata.isEmpty, "\(specimen.name) should have changed its mind at least once")
        #expect(!final.pursuits.contains { $0.state.isLive })
    }

    @Test("Every specimen passes through a judgment boundary and comes back", arguments: Specimen.all)
    func passesThroughNeedsYou(specimen: Specimen) {
        let states = (0...specimen.beats.count).map { specimen.state(at: $0) }

        #expect(states.contains { $0.lifecycle == .needsYou }, "\(specimen.name) should ask once")
        #expect(states.contains { $0.decision != nil })
        #expect(states.last?.marks.contains { $0.kind == .yours } == true,
                "\(specimen.name) should keep your mark for the life of the goal")
    }

    @Test("Only the diagnostic eliminates — the other two set aside without ruling out")
    func eliminationIsDiagnosticOnly() {
        let diagnostic = Specimen.diagnostic.state(at: Specimen.diagnostic.beats.count)
        #expect(diagnostic.setAsideLine?.contains("Ruled out") == true)

        for specimen in [Specimen.creative, Specimen.adaptive] {
            let states = (0...specimen.beats.count).map { specimen.state(at: $0) }
            let everOut = states.contains { $0.pursuits.contains { $0.state == .out } }
            #expect(!everOut, "\(specimen.name) has no suspects to rule out")
        }
    }

    @Test("The creative specimen elevates a principle; the others don't need to")
    func onlyCreativeElevates() {
        let creative = Specimen.creative.state(at: Specimen.creative.beats.count)
        #expect(creative.marks.contains { $0.kind == .principle })
    }

    @Test("The adaptive specimen takes a mark from the world, not just from you")
    func adaptiveTakesWorldInput() {
        let adaptive = Specimen.adaptive.state(at: Specimen.adaptive.beats.count)
        #expect(adaptive.marks.contains { $0.kind == .world })
    }

    @Test("5p's veil rises over the LAN work at the moment it changed direction")
    func veilContext() {
        #expect(Seed.lanInFlight.stance.hasPrefix("The real failure is rediscovery after foregrounding"))
        #expect(Seed.lanInFlight.strata.count == 1)
    }
}

// MARK: - The wire
//
// Everything here is a place the app could be silently wrong: a frame
// split across reads, a 64-bit field that arrives as a string, an id
// that changes on every launch, a stance invented out of nothing.

@Suite("Connect envelopes")
struct Envelopes {
    private func bytes(_ payloads: [String], endWith end: Bool = false) -> [UInt8] {
        var out = Data()
        for p in payloads { out.append(ConnectEnvelope.wrap(Data(p.utf8))) }
        if end { out.append(ConnectEnvelope.wrap(Data("{}".utf8), flags: 0x02)) }
        return [UInt8](out)
    }

    @Test("A frame is recovered whole")
    func roundTrip() {
        let (frames, consumed) = ConnectEnvelope.drain(bytes([#"{"a":1}"#]))
        #expect(frames.count == 1)
        #expect(String(data: frames[0].payload, encoding: .utf8) == #"{"a":1}"#)
        #expect(consumed == 12)
    }

    @Test("Several frames in one read all come out")
    func manyInOneChunk() {
        let (frames, _) = ConnectEnvelope.drain(bytes(["one", "two", "three"]))
        #expect(frames.map { String(data: $0.payload, encoding: .utf8) } == ["one", "two", "three"])
    }

    @Test("A frame split across reads waits for the rest")
    func splitAcrossChunks() {
        let whole = bytes([#"{"goals":[]}"#])
        for cut in 1..<whole.count {
            let (frames, consumed) = ConnectEnvelope.drain(Array(whole.prefix(cut)))
            #expect(frames.isEmpty, "a partial frame must not be handed over")
            #expect(consumed == 0, "and must not be consumed")
        }
        let (frames, consumed) = ConnectEnvelope.drain(whole)
        #expect(frames.count == 1)
        #expect(consumed == whole.count)
    }

    @Test("The end-of-stream flag is recognised")
    func endOfStream() {
        let (frames, _) = ConnectEnvelope.drain(bytes(["hello"], endWith: true))
        #expect(frames.count == 2)
        #expect(frames[0].isEndOfStream == false)
        #expect(frames[1].isEndOfStream)
    }

    @Test("A trailing partial frame leaves the finished ones alone")
    func partialTail() {
        var buffer = bytes(["done"])
        buffer.append(contentsOf: [0, 0, 0, 0, 90, 1, 2])  // a frame that hasn't landed
        let (frames, consumed) = ConnectEnvelope.drain(buffer)
        #expect(frames.count == 1)
        #expect(consumed == 9, "only the complete frame is consumed")
    }
}

@Suite("Pairing")
struct Pairing {
    @Test("The line vera prints is the whole pairing flow")
    func printedURL() throws {
        let parsed = try #require(Connection.parse("vera:   http://192.168.1.20:4770/?key=8f2adead"))
        #expect(parsed.connection.host == "192.168.1.20")
        #expect(parsed.connection.port == 4770)
        #expect(parsed.key == "8f2adead")
    }

    @Test("A bare host is enough for the loopback case that needs no key")
    func bareHost() throws {
        let parsed = try #require(Connection.parse("localhost"))
        #expect(parsed.connection.host == "localhost")
        #expect(parsed.connection.port == 4770)
        #expect(parsed.key == nil)
        #expect(parsed.connection.isLoopback)
    }

    @Test("A Bonjour hostname becomes a name Vera could say in a sentence")
    func bonjourName() throws {
        let parsed = try #require(Connection.parse("niks-macbook-pro.local:4770"))
        #expect(parsed.connection.name == "Niks Macbook Pro")
        #expect(!parsed.connection.isLoopback)
    }

    @Test("Junk is refused rather than turned into a half-address")
    func junk() {
        #expect(Connection.parse("") == nil)
        #expect(Connection.parse("   ") == nil)
    }
}

@Suite("Reading the board")
struct BoardMapping {
    private func card(
        id: String = "g1",
        title: String = "Harden pairing key storage",
        state: String = "Building",
        owner: String = "vera",
        face: String = "A sentence Vera wrote.",
        active: Int = 1,
        updated: String? = nil
    ) throws -> GoalCardWire {
        let json = """
        {"id":"\(id)","title":"\(title)","state":"\(state)","owner":"\(owner)",
         "face":"\(face)","nodes":4,"active":\(active),"landed":2,"spend":1.5
         \(updated.map { ",\"updatedUnixMs\":\"\($0)\"" } ?? "")}
        """
        return try JSONDecoder().decode(GoalCardWire.self, from: Data(json.utf8))
    }

    private let machine = Connection(name: "Work Mac", host: "10.0.0.5")

    @Test("protojson sends 64-bit fields as strings, and they still become a date")
    func int64AsString() throws {
        let wire = try card(updated: "1734000000000")
        let at = try #require(wire.updatedAt)
        #expect(abs(at.timeIntervalSince1970 - 1_734_000_000) < 1)
    }

    @Test("Owner decides who holds the next decision")
    func lifecycleFromOwner() throws {
        #expect(try card(owner: "you").asGoal(origin: machine, changed: false).lifecycle == .needsYou)
        #expect(try card(owner: "vera").asGoal(origin: machine, changed: false).lifecycle == .withVera)
        #expect(try card(owner: "done").asGoal(origin: machine, changed: false).lifecycle == .done)
    }

    @Test("Vera's own sentence leads; the goal's name demotes to a kicker")
    func faceLeads() throws {
        let goal = try card().asGoal(origin: machine, changed: true)
        #expect(goal.stance == "A sentence Vera wrote.")
        #expect(goal.title == "Harden pairing key storage")
        #expect(goal.digest == "A sentence Vera wrote.")
        #expect(goal.machineName == "Work Mac")
        #expect(goal.isRemote)
    }

    @Test("What the wire does not carry is left empty rather than invented")
    func inventsNothing() throws {
        let goal = try card(owner: "you").asGoal(origin: machine, changed: false)
        #expect(goal.strata.isEmpty, "no stance history is transmitted")
        #expect(goal.marks.isEmpty, "no constraints are transmitted")
        #expect(goal.outcome == nil)
        #expect(goal.decision == nil, "the board says that it needs you, not what the options are")
        // An ask exists, but nothing is claimed about what waiting costs.
        #expect(goal.stakes != nil)
        #expect(goal.stakes?.blocked == nil)
        #expect(goal.stakes?.compounds == false)
    }

    @Test("A board that says only “waiting” is not made to say “waiting on you”")
    func waitingIsNotAttributed() throws {
        let goal = try card(state: "Waiting", owner: "vera", active: 0).asGoal(origin: machine, changed: false)
        guard case .waiting(let on) = goal.standing else {
            Issue.record("expected a waiting standing"); return
        }
        #expect(on != "your call")
    }

    @Test("Counts and spend land in the footnote, because activity is not progress")
    func activityStaysAFootnote() throws {
        let goal = try card().asGoal(origin: machine, changed: false)
        #expect(goal.activity.contains("4 nodes"))
        #expect(goal.activity.contains("$1.50"))
        #expect(!goal.stance.contains("$"), "money never reaches the stance")
    }

    @Test("Ids are stable across launches and distinct across machines")
    func stableIdentity() {
        let other = Connection(name: "Home Mac", host: "10.0.0.9")
        let a = GoalCardWire.stableID(machine: machine.id, remote: "g1")
        let b = GoalCardWire.stableID(machine: machine.id, remote: "g1")
        let c = GoalCardWire.stableID(machine: other.id, remote: "g1")
        let d = GoalCardWire.stableID(machine: machine.id, remote: "g2")

        #expect(a == b, "the same goal keeps its id — Hasher would not, it is seeded per process")
        #expect(a != c, "two machines using the same card id stay distinct")
        #expect(a != d)
    }

    @Test("Nodes become pursuits, carrying what they are blocked on")
    func nodesBecomePursuits() throws {
        let json = """
        {"id":"g1","title":"Fix it","state":"Reviewing","face":"A stance.","nodes":[
          {"id":"n1","title":"Tracing","col":"progress","liveState":"working","face":"on it"},
          {"id":"n2","title":"Socket theory","col":"dropped","state":"ruled out"},
          {"id":"n3","title":"Recovery path","col":"waiting","blockedBy":["n1"],"face":"2 files"},
          {"id":"n4","title":"Verification","col":"done","state":"40 of 40"}]}
        """
        let frame = try JSONDecoder().decode(GoalFrame.self, from: Data(json.utf8))
        var goal = try card().asGoal(origin: machine, changed: false)
        frame.apply(to: &goal)

        #expect(goal.stance == "A stance.")
        #expect(goal.pursuits.count == 4)
        #expect(goal.pursuits[0].state == .lead, "a worker is on it right now")
        #expect(goal.pursuits[1].state == .out)
        #expect(goal.pursuits[2].state == .waiting)
        #expect(goal.pursuits[2].note?.contains("blocked on 1") == true)
        #expect(goal.pursuits[3].state == .done)
        #expect(goal.setAside == ["Socket theory"])
    }
}

// MARK: - Home at scale
//
// Pass 4's finding: three goals were hierarchy, fifteen would be a feed.
// These check that Home selects rather than inventories.

@Suite("Home selection")
struct HomeSelectionTests {
    private var selection: HomeSelection { HomeSelection(goals: Seed.goals) }

    @Test("Fifteen responsibilities, five things shown")
    func selectsRatherThanLists() {
        let s = selection
        #expect(Seed.goals.count == 15)

        let shown = (s.card == nil ? 0 : 1) + s.lesserAsks.count + s.changed.count
        #expect(shown <= 5, "shown by name in a row or a card: \(shown)")
        #expect(s.unspokenCount > 0, "the rest are named in a digest or left alone")
    }

    @Test("Only one thing ever gets a container")
    func oneCard() {
        let s = selection
        #expect(s.askCount == 2, "two asks are pending")
        #expect(s.card != nil)
        #expect(s.lesserAsks.count == 1, "the second gets a quiet row, not a second card")
    }

    @Test("The card goes to what is blocking, not to what is newest")
    func blockingWinsTheCard() {
        #expect(selection.card?.stakes?.blocked != nil)
        #expect(selection.lesserAsks.first?.stakes?.workContinues == true)
    }

    @Test("A held ask never reaches Home, but Home admits it exists")
    func heldAsk() {
        let s = selection
        #expect(s.heldAsks == 1)
        #expect(s.card?.title != "Rook integration")
        #expect(s.lesserAsks.allSatisfy { $0.title != "Rook integration" })
        #expect(s.subhead.contains("inside its goal"))
    }

    @Test("Rows are a budget — what overflows becomes a digest line, not a disappearance")
    func rowsAreABudget() {
        var goals = Seed.goals
        for i in 1...4 {
            var extra = Seed.simple("Extra \(i)", digest: "Moved.")
            extra.changedSinceYouLooked = true
            goals.append(extra)
        }
        let s = HomeSelection(goals: goals)

        #expect(s.changed.count <= 2)
        #expect(s.alsoMoving.contains("Extra 4"), "overflow is still named")
    }

    @Test("Quiet goals are counted, never listed")
    func quietIsCounted() {
        let s = selection
        #expect(s.quiet > 0)
        #expect(s.browseLine?.contains("more goals") == true)
    }

    @Test("With nothing pending, the headline earns its size from the load underneath")
    func loadSentence() {
        let calm = Seed.goals.filter { $0.lifecycle != .needsYou }
        let s = HomeSelection(goals: calm)

        #expect(s.headline == "Nothing needs you.")
        #expect(s.subhead.contains("with me"))
        #expect(s.subhead.contains("watching"))
        #expect(s.subhead.contains("quiet on purpose"))
    }

    @Test("An empty installation says so plainly rather than inventing a digest")
    func emptyInstallation() {
        let s = HomeSelection(goals: [])
        #expect(s.headline == "Nothing needs you.")
        #expect(s.subhead == "Quiet on my side too.")
        #expect(s.browseLine == nil)
    }

    @Test("Attention levels follow the policy, not the goal's age")
    func attentionLevels() {
        #expect(Seed.pairingKeys.attention == .needsYou)
        #expect(Seed.rookIntegration.attention == .record, "a held ask is recorded, not surfaced")
        #expect(Seed.todayGoal.attention == .surfaceOnHome)
        #expect(Seed.simple("Docs site refresh", standing: .quiet).attention == .ignore)
    }
}

// MARK: - Consequences becoming structure

@MainActor
@Suite("The store")
struct StoreBehaviour {
    @Test("Logging in conversation moves the training surface")
    func loggingMovesTheSurface() {
        let store = VeraStore()
        let before = store.sessions.flatMap(\.sets).count

        store.say("Log bench 195 for 5, 5, 5.")

        #expect(store.sessions.flatMap(\.sets).count == before + 1)
        #expect(store.turns.contains { $0.isMaterial })
    }

    @Test("“Keep that” keeps the idea, not the request to keep it")
    func keepThatResolvesItsReferent() {
        let store = VeraStore()
        store.say("I think we’ve been treating Vera too much like a coding agent.")
        store.say("Actually, keep that. I think it’s a core product principle.")

        let kept = store.principles.first
        #expect(kept?.text == "Software engineering is the litmus test, not the product category.")
        #expect(kept?.text.contains("keep that") == false)
    }

    @Test("Answering the blocking ask hands the card to the next one")
    func answeringPromotesTheNextAsk() {
        let store = VeraStore()
        #expect(store.attention?.title == "Harden pairing key storage",
                "the ask that blocks something takes the card")
        #expect(store.selection.headline == "Two things need you.")

        store.say("Go with strict.")

        #expect(store.attention?.title == "Design Vera Mobile",
                "the taste call was always second; now it's the only one left")
        #expect(store.selection.headline == "One thing needs you.")
        #expect(store.changes.first?.text.contains("strict") == true)

        let answered = store.goals.first { $0.title == "Harden pairing key storage" }
        #expect(answered?.lifecycle == .withVera, "ownership flips back")
        #expect(answered?.marks.contains { $0.kind == .yours } == true)
    }

    @Test("The composer and the buttons are the same door")
    func composerAnswersLikeAButton() {
        let byComposer = VeraStore()
        byComposer.say("strict, please")

        let byButton = VeraStore()
        let goal = byButton.attention!
        byButton.answer(goal.id, with: goal.decision!.recommended)

        let a = byComposer.goals.first { $0.title == "Harden pairing key storage" }
        let b = byButton.goals.first { $0.title == "Harden pairing key storage" }
        #expect(a?.lifecycle == b?.lifecycle)
        #expect(a?.marks.count == b?.marks.count)
    }

    @Test("Delegating in one sentence opens the full goal grammar behind the ›")
    func delegationOpensAGoal() {
        let store = VeraStore()
        store.say("Go fix the reconnection bug.")

        guard let turn = store.turns.first(where: { $0.isMaterial }),
              case .material(let m) = turn.body,
              case .goal(let id) = m.destination
        else { Issue.record("expected a goal materialization"); return }

        let goal = store.goal(id)
        #expect(goal?.title == "Fix flaky LAN reconnection")
        #expect(goal?.pursuits.count == 4, "the stance arrives with pursuits, not an empty page")
        #expect(goal?.understanding.contains("exploring") == true)
    }

    @Test("An intention with a horizon opens a goal whose pursuits chase what it doesn’t know")
    func intentionOpensAGoal() {
        let store = VeraStore()
        store.say("I actually want to get meaningfully better at singing over the next six months.")

        let goal = store.goals.last
        #expect(goal?.title == "Sing better — six months")
        #expect(goal?.pursuits.contains { $0.state == .waiting } == true, "watching is a pursuit")
        #expect(goal?.marks.isEmpty == true, "nothing of yours has docked yet")
    }

    @Test("Home is comfortable with silence when there is nothing to say")
    func silence() {
        let store = VeraStore()
        store.enter(.homeSilence)
        if case .silence = store.homeState {} else { Issue.record("expected silence") }
    }

    @Test("A watch shows up in Around you without being installed")
    func watchAppearsAround() {
        let store = VeraStore()
        store.enter(.homeAround)
        store.say("Keep an eye out for the roof quote.")

        let names = store.surfaceLinks.map(\.name.localizedLowercase)
        #expect(names.contains { $0.contains("roof quote") })
    }
}
