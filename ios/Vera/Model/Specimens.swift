import Foundation

// The three specimens pass 3 tested the invariant against: a debugging
// session, a design iteration, and a day's plan.
//
// Each is written as an opening state plus a sequence of *transformations* —
// not as a sequence of hand-drawn screens. That is the actual claim being
// made: if the eight moves in the transformation vocabulary are
// sufficient, three kinds of work this different can be expressed with
// nothing else. Every beat below is a supersede, combine, eliminate,
// park, elevate, incorporate, branch or conclude.

struct Specimen: Sendable, Identifiable {
    var id: String
    var name: String
    var opening: Goal
    var beats: [Beat]

    struct Beat: Sendable {
        var name: String
        /// What this beat demonstrates. Shown only in the walkthrough.
        var caption: String
        var apply: @Sendable (inout Goal) -> Void
    }

    var beatCount: Int { beats.count + 1 }

    /// Fold the transformations forward. Beat 0 is the opening state.
    func state(at beat: Int) -> Goal {
        var goal = opening
        for i in 0..<max(0, min(beat, beats.count)) {
            beats[i].apply(&goal)
        }
        return goal
    }

    func beatName(_ beat: Int) -> String {
        beat == 0 ? "Opening" : beats[min(beat, beats.count) - 1].name
    }

    func beatCaption(_ beat: Int) -> String {
        beat == 0 ? openingCaption : beats[min(beat, beats.count) - 1].caption
    }

    var openingCaption: String
}

// MARK: - A · Diagnostic
//
// Fix flaky LAN reconnection. The control specimen: this is the one that
// eliminates, and elimination turned out to be the only move debugging
// needed that the other two didn't.

extension Specimen {

    static let lanGoalID = UUID()

    static let diagnostic = Specimen(
        id: "lan",
        name: "Fix flaky LAN reconnection · diagnostic",
        opening: Goal(
            id: lanGoalID,
            title: "Fix flaky LAN reconnection",
            kind: .diagnostic,
            stance: "I can reproduce it, but I don’t yet know whether we’re losing the socket or losing peer discovery.",
            understanding: "exploring — two candidate explanations",
            pursuits: [
                Pursuit("Watching the socket through a background cycle", note: "testing: “we lose the socket”"),
                Pursuit("Tracing Bonjour peer discovery", note: "testing: “we lose the peer”", delay: 0.8),
                Pursuit("Reviewing iOS background networking rules", note: "context for both", state: .dim),
                Pursuit("Reproducing the drop on background / foreground", note: "3 of 3 — parked as the test harness", state: .dim)
            ],
            activity: "activity: reading transport + discovery sources"
        ),
        beats: [
            .init(
                name: "Parallel investigation",
                caption: "Evidence accrues on the lines; the stance holds. Activity stays a footnote — five files read is not progress."
            ) { g in
                g.note("evidence accumulating on both theories")
                g.update("Watching the socket", note: "cycles 1–3: socket intact so far")
                g.update("Tracing Bonjour", note: "peer record goes missing ~45 s in")
                g.update("Reviewing iOS", note: "suspension rules read")
                g.update("Reproducing the drop", note: "harness steady")
                g.activity = "activity: 5 files read · 2 repro runs — activity, not progress"
            },
            .init(
                name: "Evidence",
                caption: "A finding rises once, never loops. The line it contradicts hollows out; it is not yet ruled out."
            ) { g in
                g.develop(
                    Development(kind: .finding, text: "The socket survives backgrounding. Discovery doesn’t reliably recover."),
                    understanding: "the leading explanation is weakening"
                )
                g.update("Watching the socket", state: .weak, note: "contradicts the transport theory", live: true)
                g.update("Tracing Bonjour", state: .lead, note: "now the leading thread", live: true)
                g.activity = "activity: discovery trace running"
            },
            .init(
                name: "Changed direction",
                caption: "Stance succession: the old belief strikes into strata with its provenance. Two lines converge; one is eliminated and drops to the ledger."
            ) { g in
                g.supersede(
                    with: "The real failure is rediscovery after foregrounding. I’m moving the fix out of transport and into peer discovery.",
                    understanding: "model revised just now — from the socket finding",
                    because: "the socket survives a full background cycle; discovery doesn’t",
                    by: "the socket finding",
                    held: "2 hours",
                    at: "14:22"
                )
                g.combine(
                    "Reviewing iOS",
                    into: "Tracing Bonjour",
                    rename: "Tracing rediscovery across the iOS lifecycle",
                    becoming: "iOS lifecycle review — folded into the trace",
                    note: "two lines converged here"
                )
                g.eliminate(
                    "Watching the socket",
                    becoming: "Watching the socket — socket survives",
                    reason: "ruled out 14:14",
                    ledger: "socket loss"
                )
                g.setAside.append("transport-level fixes")
                g.update("Reproducing the drop", note: "becomes the verification harness")
                g.activity = "activity: repro harness idle · trace running"
            },
            .init(
                name: "Needs you",
                caption: "Ownership flips. The decision rises from the composer — the place answers come from — and says what happens after you answer."
            ) { g in
                g.ask(
                    Decision(
                        intro: "Two sound ways to own the fix — they differ in honesty, not just cost.",
                        recommended: .init(
                            title: "Move reconnect ownership into peer discovery",
                            body: "Explicit recovery on foreground. Fixes the fault where it lives; no protocol change.",
                            keyword: "discovery"
                        ),
                        alternative: .init(
                            title: "Keep transport authoritative with a heartbeat",
                            body: "Masks the fault instead of fixing it, and costs battery.",
                            keyword: "heartbeat"
                        ),
                        after: "After you answer I implement, then verify across repeated background / foreground cycles."
                    ),
                    understanding: "paused at a judgment boundary"
                )
                g.update("Tracing rediscovery", note: "validated — waiting on your call", live: true)
                g.activity = "activity: workers holding"
            },
            .init(
                name: "Incorporation",
                caption: "Your words dock as a solid chip and stay for the life of the goal. The plan reorders around your call."
            ) { g in
                g.incorporate(
                    "“Go with discovery — and don’t touch the pairing protocol.”",
                    as: "From you: pairing protocol untouched",
                    said: "said by voice · today 14:31",
                    affects: "1 goal — the fix moves into peer discovery",
                    standsUntil: "you lift it"
                )
                g.supersede(
                    with: "Decided, with your constraint. Reconnect ownership moves into peer discovery — protocol untouched. Implementing the foreground recovery path.",
                    understanding: "your constraint is part of the model now",
                    because: "you chose discovery ownership and ruled the protocol out of scope",
                    by: "your call at 14:31",
                    at: "14:31"
                )
                g.open(Pursuit(
                    "Implementing the foreground recovery path",
                    note: "in peer discovery, per your call",
                    state: .lead,
                    live: true
                ))
                g.combine(
                    "Tracing rediscovery",
                    into: "Implementing the foreground recovery path",
                    becoming: "Rediscovery trace — became the plan"
                )
                g.replace("Reproducing the drop", with: Pursuit(
                    "Verification loop on the repro harness",
                    note: "queued behind the fix",
                    state: .dim
                ))
                g.activity = "activity: implementing · branch lan-rediscovery"
            },
            .init(
                name: "Convergence",
                caption: "One live line left. Confidence is visible as narrowness, not as a percentage."
            ) { g in
                g.conclude("Implementing the foreground recovery path", becoming: "Foreground recovery path — landed, 2 files")
                g.open(Pursuit(
                    "Verifying across background / foreground cycles",
                    note: "31 of 40 clean",
                    state: .lead,
                    live: true
                ))
                g.supersede(
                    with: "Fix is in. Verifying across repeated background / foreground cycles — 31 of 40 clean so far.",
                    understanding: "confident enough to act — acting",
                    because: "the recovery path landed and the first 31 cycles are clean",
                    at: "15:06"
                )
                g.activity = "activity: verification loop running"
            },
            .init(
                name: "Done",
                caption: "Pulses stop. The stance is the answer, your constraint stays docked, and what Vera learned is kept. Done is a lifecycle fact — the understanding resolved separately."
            ) { g in
                g.supersede(
                    with: "Fixed and verified. The fault was rediscovery, not transport.",
                    understanding: "understanding resolved · concluded",
                    because: "40 of 40 cycles clean",
                    at: "15:44"
                )
                g.setAside.append("heartbeat masking")
                g.settle(
                    Outcome(
                        text: "Implemented in peer discovery with an explicit foreground recovery path. Verified across 40 background / foreground cycles. Protocol untouched, as you asked.",
                        keptInMemory: "Bonjour expires peer records ~45 s into background. Recovery must be explicit on foreground — transport can’t see it."
                    ),
                    understanding: "understanding resolved · concluded",
                    summary: Pursuit("Verification — 40 of 40 cycles clean", state: .done)
                )
                g.activity = "activity: workers retired · branch merged"
            }
        ],
        openingCaption: "Two theories held apart. Honest uncertainty is designed to read competent, not incomplete — the pursuits underneath go after exactly what the stance says it doesn’t know."
    )
}

// MARK: - B · Creative
//
// Design Vera Mobile. Nothing is "ruled out" here — this isn't a
// mystery. Directions are parked with their reasons, observations get
// elevated into principles, and progress is allowed to produce a new
// tension rather than more confidence.

extension Specimen {

    private static let stanceB1 = "The app structurally works. What it lacks is a way to see understanding change — it reads like a well-organized report about Vera, not like Vera."
    private static let stanceB4 = "Motion was never the gap. The design needs understanding made visible — stance, history and pursuits as one structure; animation only marks its changes."

    static let creative = Specimen(
        id: "design",
        name: "Design Vera Mobile · creative",
        opening: Goal(
            title: "Design Vera Mobile",
            kind: .creative,
            stance: stanceB1,
            understanding: "a conviction to test — direction not yet chosen",
            pursuits: [
                Pursuit("Exploring stance-as-spine layouts", note: "generative — 3 variants planned"),
                Pursuit("Exploring motion as a semantic grammar", note: "generative", delay: 0.8),
                Pursuit("Auditing which cards earn their containers", note: "evaluative", state: .dim),
                Pursuit("Collecting moments the current build feels dead", note: "investigative", state: .dim)
            ],
            activity: "activity: sketching · reading pass-1 files"
        ),
        beats: [
            .init(
                name: "Exploration",
                caption: "Pursuits accrue observations. Nothing is eliminated, because nothing was ever a suspect."
            ) { g in
                g.note("explorations feeding back")
                g.update("Exploring stance-as-spine", note: "the strata idea came from variant 2")
                g.update("Exploring motion", note: "motion only reads when it’s rare")
                g.update("Auditing which cards", note: "11 cards; maybe 4 earn it")
                g.update("Collecting moments", note: "they cluster where nothing changed")
                g.activity = "activity: 3 sketch variants rendered"
            },
            .init(
                name: "Insight",
                caption: "A development that reframes rather than confirms. Same rise as a finding; a different kind of moment."
            ) { g in
                g.develop(
                    Development(kind: .insight, text: "The problem isn’t insufficient animation. The interface lacks a visual representation of changing understanding."),
                    understanding: "an insight is reframing the exploration"
                )
                g.update("Exploring stance-as-spine", state: .lead, note: "suddenly central", live: true)
                g.update("Exploring motion", state: .weak, note: "reframed — motion serves the spine")
                g.update("Collecting moments", note: "evidence for the insight")
                g.activity = "activity: sketching continues"
            },
            .init(
                name: "Revised direction",
                caption: "Three moves at once: the stance supersedes, motion folds into the system, and the card audit is elevated out of the pursuit list into a principle chip."
            ) { g in
                g.supersede(
                    with: stanceB4,
                    understanding: "direction revised just now — from the insight",
                    because: "every dead moment clustered where nothing had changed, not where nothing moved",
                    by: "the insight",
                    held: "6 days",
                    at: "today"
                )
                g.combine(
                    "Exploring motion",
                    into: "Exploring stance-as-spine",
                    rename: "Building the understanding-made-visible system",
                    becoming: "Motion grammar — folded into the system",
                    note: "motion exploration merged in"
                )
                g.elevate(
                    "Auditing which cards",
                    becoming: "Card audit — elevated to a principle",
                    asPrinciple: "a card must earn its container",
                    from: "the card audit (11 → 4) · two dead-moment clusters",
                    when: "3 weeks ago"
                )
                g.update("Collecting moments", note: "continues as the test set")
                g.setAside.append("animation-first direction (superseded, kept for reference)")
                g.activity = "activity: system sketching · 2 directions forming"
            },
            .init(
                name: "Your taste",
                caption: "A legitimate two-way call. Vera recommends and explains why, but taste is yours and she says so."
            ) { g in
                g.ask(
                    Decision(
                        intro: "Both are honest expressions of the insight. They differ in daily feel, not correctness.",
                        recommended: .init(
                            title: "Quiet typographic strata",
                            body: "History reads at a glance; nothing performs. Better for the hundredth open.",
                            keyword: "quiet"
                        ),
                        alternative: .init(
                            title: "Expressive living diagram",
                            body: "Understanding drawn as a moving structure. Demos beautifully; heavier daily.",
                            keyword: "expressive"
                        ),
                        after: "Whichever you pick, I’ll build the next iteration on it and come back with what it teaches."
                    ),
                    understanding: "paused at a judgment boundary — a taste call"
                )
                g.stance = "Two strong directions survived. One is more expressive; the other preserves glanceability. I recommend the quiet one — but this is taste, and taste is yours."
                g.update("Building the understanding-made-visible", state: .active, note: "holding at the fork", live: false)
                g.activity = "activity: both directions prototyped"
            },
            .init(
                name: "Incorporation",
                caption: "Your answer invented a combination neither option offered. Both halves dock as chips; the losing direction parks whole, with its reason."
            ) { g in
                g.incorporate(
                    "“Quiet one — but steal the diagram’s convergence moment for direction changes.”",
                    as: "Your taste: quiet strata baseline",
                    said: "said by voice · today",
                    affects: "this goal — and the pass-3 build",
                    standsUntil: "you revisit it"
                )
                g.marks.append(Mark(text: "From you: steal the convergence moment"))
                g.stance = "Quiet strata is the direction — with the diagram’s convergence moment stolen for direction changes, as you asked. Building the combined system now."
                g.note("your taste is part of the model now", live: true)
                g.lifecycle = .withVera
                g.replace("Building the understanding-made-visible", with: Pursuit(
                    "Building the strata system",
                    note: "baseline, per your taste",
                    state: .lead,
                    live: true
                ))
                g.open(Pursuit(
                    "Designing the convergence moment for direction changes",
                    note: "your combination — from the diagram",
                    live: true,
                    delay: 0.8
                ), at: 1)
                // The losing direction was never a pursuit row; it takes
                // one on its way out so the reason stays visible.
                g.park(
                    "Expressive diagram",
                    becoming: "Expressive diagram — parked",
                    reason: "kept whole; may serve marketing",
                    ledger: "expressive diagram (parked, with its reason)"
                )
                g.update("Collecting moments", note: "now the regression test")
                g.activity = "activity: implementing · branch strata-v1"
            },
            .init(
                name: "Iteration → tension",
                caption: "Progress produced a new tension rather than more confidence. The grammar allows that; a progress bar would not."
            ) { g in
                g.develop(
                    Development(kind: .tension, text: "The strata system works on goals but crowds Home at glance scale — history can’t be a stack there."),
                    understanding: "iteration surfaced a new tension — not more confidence"
                )
                g.supersede(
                    with: "Quiet strata is the baseline, with the diagram’s convergence moment reserved for direction changes. Home gets a compressed strata mark, not the stack.",
                    understanding: "iteration surfaced a new tension — not more confidence",
                    because: "the stack crowds Home at glance scale",
                    by: "the tension",
                    at: "today"
                )
                g.conclude("Building the strata system", note: "goal-page version landed")
                g.conclude("Designing the convergence moment", becoming: "Convergence moment for direction changes", note: "specified")
                g.conclude("Collecting moments", becoming: "Dead-moment set — became the regression test")
                g.open(Pursuit(
                    "Compressing strata to a glance mark for Home",
                    note: "opened by the tension",
                    state: .lead,
                    live: true
                ))
                g.activity = "activity: glance-mark variants rendering"
            },
            .init(
                name: "Done, not closed",
                caption: "The iteration concluded. The subject stays open, the parked diagram stays parked, and both facts are stated rather than hidden."
            ) { g in
                g.settle(
                    Outcome(
                        text: "The requested iteration is complete. Vera Mobile will keep evolving — this pass answered what it asked: understanding made visible, quietly.",
                        keptInMemory: "Making understanding visible beats adding animation. A card must earn its container.",
                        deliberatelyOpen: "Deliberately open: the 10–20 goal Home — noted for a future pass."
                    ),
                    understanding: "iteration concluded · the subject stays open",
                    summary: Pursuit("All three deliverables prototyped", state: .done)
                )
                g.setAside = ["expressive diagram", "the 10–20 goal Home"]
                g.activity = "activity: workers retired · specs filed"
            }
        ],
        openingCaption: "A conviction about quality rather than a hypothesis about facts — and it occupies exactly the same slot."
    )
}

// MARK: - C · Adaptive
//
// Today. The stance is a shape for the day; watching is a pursuit; the
// plan is loose on purpose and never becomes a task list.

extension Specimen {

    private static let stanceC1 = "Nothing urgent is waiting. The uninterrupted morning is worth more than the fragmented afternoon — deep Vera work first; the house absorbs interruptions later."
    private static let stanceC3 = "The afternoon is now fragmented past usefulness for deep work. Everything deep moves into the morning block; the afternoon re-casts as interruption-friendly house work."

    static let adaptive = Specimen(
        id: "today",
        name: "Today · adaptive planning",
        opening: Goal(
            title: "Today",
            kind: .adaptive,
            stance: stanceC1,
            understanding: "a loose plan, held loosely by design",
            pursuits: [
                Pursuit("Watching mail and calendar for anything urgent", note: "checked at 9:04 — clear", state: .waiting),
                Pursuit("Holding the morning clear for design work", note: "operational — protecting time"),
                Pursuit("Sequencing house tasks for the afternoon", note: "repair window · groceries · gutters", state: .dim)
            ],
            marks: [Mark(text: "From you: Vera work + house + check nothing urgent")],
            activity: "activity: mail + calendar scanned"
        ),
        beats: [
            .init(
                name: "The day moves",
                caption: "New information arrives from the world. Same development slot as a finding or an insight — a different source."
            ) { g in
                g.develop(
                    Development(kind: .newInformation, text: "Your 4:00 call moved to 1:30 — the afternoon just fragmented further."),
                    understanding: "the day just moved"
                )
                g.update("Watching mail", note: "caught the calendar change", live: true)
                g.update("Sequencing house tasks", state: .weak, note: "the sequence no longer fits")
                g.activity = "activity: calendar watched"
            },
            .init(
                name: "Reprioritized",
                caption: "The stance supersedes and the deep block grows. Nothing becomes a task list."
            ) { g in
                g.supersede(
                    with: stanceC3,
                    understanding: "reshaped just now — from your calendar",
                    because: "the 1:30 call cuts the afternoon into pieces too small for deep work",
                    by: "your calendar",
                    at: "9:40"
                )
                g.marks.append(Mark(
                    text: "From your calendar: call moved to 1:30",
                    kind: .world,
                    provenance: Provenance(
                        kicker: "From the world",
                        timing: "your calendar · today 9:38",
                        rows: [.init(label: "affects:", value: "the afternoon sequence, and the repair window")]
                    )
                ))
                g.replace("Holding the morning clear", with: Pursuit(
                    "Holding the morning clear — extended to 1:00",
                    note: "the deep block grew",
                    state: .lead,
                    live: true
                ))
                g.replace("Sequencing house tasks", with: Pursuit(
                    "Re-sequencing errands around the 1:30 call",
                    delay: 0.8
                ))
                g.activity = "activity: 2 reschedules drafted"
            },
            .init(
                name: "Small call",
                caption: "An operational judgment: cheap for you, wrong for Vera to guess. The weight of the ask matches the weight of the question."
            ) { g in
                g.ask(
                    Decision(
                        intro: "The dishwasher repair window collides with your 1:30 call. Rebooking costs nothing.",
                        recommended: .init(
                            title: "Rebook the repair for Thursday",
                            body: "Off your plate today; Thursday morning is open.",
                            keyword: "rebook"
                        ),
                        alternative: .init(
                            title: "Keep it — take the call from home",
                            body: "Works, but you host the repair mid-call.",
                            keyword: "keep it"
                        ),
                        after: "Either way I re-sequence the afternoon around it."
                    ),
                    understanding: "one small call is yours"
                )
                g.update("Holding the morning clear", state: .active, live: false)
                g.update("Re-sequencing errands", state: .weak, note: "waiting on your call", live: true)
                g.activity = "activity: repair slot held"
            },
            .init(
                name: "Afternoon underway",
                caption: "Deferring on purpose is a first-class outcome here, and it carries its reason. It never reads as failure."
            ) { g in
                g.incorporate(
                    "“Rebook it — Thursday’s fine.”",
                    as: "Your call: repair rebooked Thursday",
                    said: "said by voice · today 9:52",
                    affects: "today’s afternoon, and Thursday morning"
                )
                g.lifecycle = .withVera
                g.stance = "The day’s important work happened this morning. What’s left tolerates interruption — and two things are deliberately not today’s problem."
                g.note("afternoon underway")
                g.pursuits = [
                    Pursuit("Design work — 3 focused hours", note: "the morning held", state: .done),
                    Pursuit("Groceries + package drop", note: "interruption-friendly, as planned"),
                    Pursuit("Repair — rebooked Thursday", note: "off your plate", state: .done),
                    Pursuit("Gutters — deferred on purpose", note: "light rain, low value today", state: .waiting)
                ]
                g.setAside = ["gutters", "inbox sweep"]
                g.activity = "activity: 2 bookings confirmed"
            },
            .init(
                name: "Day closed",
                caption: "Not everything finished, on purpose — and Thursday inherits the rest by name."
            ) { g in
                g.supersede(
                    with: "The important work moved forward, the urgent obligations were handled, and two low-value items were deliberately deferred.",
                    understanding: "day closed — not everything finished, on purpose",
                    because: "the morning block held and nothing urgent slipped",
                    at: "18:10"
                )
                g.settle(
                    Outcome(
                        text: "Deep work got the morning; the fragmented afternoon absorbed everything interruptible. Nothing urgent slipped.",
                        keptInMemory: "Call-heavy days fragment your afternoons — mornings are your deep-work asset. Rebooking beats hosting.",
                        deliberatelyOpen: "Thursday inherits: the repair window, gutters, inbox sweep."
                    ),
                    understanding: "day closed — not everything finished, on purpose",
                    summary: Pursuit("3h design · errands done · repair rebooked", state: .done)
                )
                g.setAside = ["gutters", "inbox sweep → folded into Thursday’s picture"]
                g.activity = "activity: day summarized"
            }
        ],
        openingCaption: "The stance is the shape of the day. Watching is a pursuit, not a background service — Vera owns it, and says so."
    )

    static let all: [Specimen] = [diagnostic, creative, adaptive]

    static func named(_ id: String) -> Specimen? {
        all.first { $0.id == id }
    }
}
