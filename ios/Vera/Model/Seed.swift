import Foundation

// The world Vera already has when you open her.
//
// Eight weeks of training, five principles with provenance, one open
// decision, one quiet watch. All of it is *data*, not copy: the training
// figures on 5k and 5r are computed from these sessions, so logging a
// set from the conversation genuinely moves them.

enum Seed {

    static var today: Date { Calendar.current.startOfDay(for: Date()) }

    static func day(_ daysAgo: Int) -> Date {
        Calendar.current.date(byAdding: .day, value: -daysAgo, to: today) ?? today
    }

    // MARK: - Training
    //
    // Pressing climbs; squat is genuinely flat and genuinely
    // under-sampled, so the app can say so without being told to.

    static let sessions: [Session] = [
        Session(date: day(51), sets: [
            LiftSet(lift: "bench press", weight: 170, reps: [5, 5, 5]),
            LiftSet(lift: "row", weight: 140, reps: [8, 8, 8])
        ]),
        Session(date: day(44), sets: [
            LiftSet(lift: "bench press", weight: 175, reps: [5, 5, 4]),
            LiftSet(lift: "squat", weight: 225, reps: [5, 5, 5])
        ]),
        Session(date: day(37), sets: [
            LiftSet(lift: "bench press", weight: 175, reps: [5, 5, 5]),
            LiftSet(lift: "row", weight: 142, reps: [8, 8, 7])
        ]),
        Session(date: day(30), sets: [
            LiftSet(lift: "bench press", weight: 180, reps: [5, 5, 4]),
            LiftSet(lift: "squat", weight: 230, reps: [5, 4, 4])
        ]),
        // The travel week — one session, and the trend held. `note` is
        // what lets Vera say so without being told to.
        Session(date: day(23), sets: [
            LiftSet(lift: "bench press", weight: 180, reps: [5, 5, 5])
        ], note: "travel week"),
        Session(date: day(16), sets: [
            LiftSet(lift: "bench press", weight: 185, reps: [4, 4, 3]),
            LiftSet(lift: "squat", weight: 225, reps: [5, 5, 5])
        ]),
        Session(date: day(9), sets: [
            LiftSet(lift: "bench press", weight: 185, reps: [4, 4, 4]),
            LiftSet(lift: "row", weight: 148, reps: [8, 8, 8])
        ]),
        Session(date: day(6), sets: [
            LiftSet(lift: "bench press", weight: 180, reps: [5, 5, 5, 5, 5])
        ], note: "easy"),
        Session(date: day(4), sets: [
            LiftSet(lift: "squat", weight: 225, reps: [5, 5, 5])
        ], note: "felt heavy"),
        // Yesterday: five, five, four at 185 — a rep PR, because the
        // best prior showing at that weight was four.
        Session(date: day(1), sets: [
            LiftSet(lift: "bench press", weight: 185, reps: [5, 5, 4]),
            LiftSet(lift: "row", weight: 148, reps: [8, 8, 8])
        ])
    ]

    // MARK: - Principles
    //
    // 5s. One is struck and replaced rather than edited — a principle
    // supersedes itself the same way a stance does.

    static let principles: [Principle] = [
        Principle(
            text: "Conversation is fluid; consequences become structure.",
            provenance: "kept this morning · from a design conversation"
        ),
        Principle(
            text: "Software engineering is the litmus test, not the product category.",
            provenance: "kept 9:45 today · same conversation"
        ),
        Principle(
            text: "Motion only marks changes in understanding or ownership.",
            supersedes: "Motion makes Vera feel alive.",
            provenance: "revised 2 weeks ago — the pass-2 motion audit"
        ),
        Principle(
            text: "A card must earn its container.",
            provenance: "elevated by Vera · from the card audit, 3 weeks ago",
            elevatedByVera: true
        ),
        Principle(
            text: "Activity is not progress.",
            provenance: "kept 3 weeks ago · pass-2 brief"
        )
    ]

    // MARK: - Watches

    static let watches: [WatchItem] = [
        WatchItem(
            subject: "Passport update",
            promise: "I’ll tell you when it lands or if two weeks pass without it."
        )
    ]

    // MARK: - The installation
    //
    // Fifteen responsibilities, which is the scale pass 4 tested Home
    // against and the scale at which a card stack stopped working. Home
    // shows five of them; the rest are named in a digest line or left
    // quiet on purpose, and every one of those calls is made by the
    // selection policy rather than written down here.

    static var goals: [Goal] {
        [
            pairingKeys,
            designVeraMobile,
            lanInFlight,
            todayGoal,
            simple("Memory model", digest: "Shape settled overnight — git files stay.", standing: .moving),
            rookIntegration,
            simple("Prototype feedback", digest: "Marta’s notes.", standing: .watching),
            simple("Upstream Bonjour fix", digest: "Tracking the upstream thread.", standing: .watching),
            relayKeyMigration,
            simple("Docs site refresh", standing: .quiet),
            simple("Release notes for 0.4", standing: .quiet),
            simple("Sing better — six months", standing: .quiet),
            simple("Passport renewal", standing: .quiet),
            simple("Kitchen shelving", standing: .quiet),
            pairingReview
        ]
    }

    // — the one thing that genuinely needs a person —

    static var pairingKeys: Goal {
        var g = Goal(
            title: "Harden pairing key storage",
            kind: .diagnostic,
            stance: "The device key is readable by other local processes. I’ve paused the merge — the stricter fix re-keys every phone.",
            understanding: "paused at a judgment boundary",
            understandingIsLive: true,
            lifecycle: .needsYou,
            pursuits: [
                Pursuit("Scoping the Keychain access group", note: "validated — waiting on your call", state: .lead, live: true),
                Pursuit("Drafting the re-key migration", note: "ready either way"),
                Pursuit("Holding the merge", note: "operational", state: .waiting)
            ],
            activity: "activity: branch pairing-keychain · merge paused"
        )
        g.decision = Decision(
            intro: "Both are sound. They differ in who can read the key, and in what it costs your users once.",
            recommended: .init(
                title: "Strict Keychain scope",
                body: "Only this app on this device can read it. Re-keys every paired phone once.",
                keyword: "strict"
            ),
            alternative: .init(
                title: "Compatible scope",
                body: "No re-key, but a restored backup can carry a usable key onto another machine.",
                keyword: "compatible"
            ),
            after: "After you answer I land the migration and note it in the PR — that unblocks the merge."
        )
        g.stakes = Stakes(blocked: "blocking · 40m", compounds: true)
        g.digest = "A security boundary on the pairing fix — it’s blocking the merge"
        g.standing = .waiting(on: "your call")
        g.changedSinceYouLooked = true
        return g
    }

    // — an ask that can wait, because work continues around it —

    static var designVeraMobile: Goal {
        var g = Specimen.creative.state(at: 4)
        g.stakes = Stakes(workContinues: true, mayWithdraw: true)
        g.digest = "a taste call when you have a minute. Work continues meanwhile."
        g.standing = .moving
        g.changedSinceYouLooked = true
        return g
    }

    // — work that changed your picture without needing you —

    static var todayGoal: Goal {
        var g = Specimen.adaptive.state(at: 2)
        g.digest = "Morning is clear for the pairing decision and design work; your 2:00 is prepped."
        g.standing = .moving
        g.lastMoved = "reshaped 20m ago"
        g.changedSinceYouLooked = true
        return g
    }

    // — a held ask: real, and deliberately not Home's —

    static var rookIntegration: Goal {
        var g = simple("Rook integration", digest: "Naming cleanup pending.", standing: .moving)
        g.lifecycle = .needsYou
        g.stakes = Stakes(workContinues: true, heldInGoal: true, mayWithdraw: true)
        return g
    }

    // — stuck without you, and honest about why —

    static var relayKeyMigration: Goal {
        var g = simple(
            "Relay signing key migration",
            digest: "The signing credentials expired Friday. I’ve prepared the migration and verified it against a scratch key — when your work Mac wakes, I’ll need you to approve a fresh credential.",
            standing: .waiting(on: "your work Mac")
        )
        g.activity = "activity: tried cached key · scratch rehearsal — no other path from here"
        return g
    }

    static var pairingReview: Goal {
        var g = simple("Review the mobile pairing implementation", digest: "pairing review concluded")
        g.lifecycle = .done
        g.changedSinceYouLooked = true
        g.outcome = Outcome(
            text: "Reviewed. The implementation matches the spec; one boundary needed hardening, which became its own goal.",
            keptInMemory: "Pairing keys were readable by other local processes — worth checking on any Keychain use."
        )
        g.pursuits = [Pursuit("Review — complete", state: .done)]
        return g
    }

    /// A goal with nothing to say yet. Most of an installation looks
    /// like this, and Home is right to leave it alone.
    static func simple(_ title: String, digest: String = "", standing: Standing = .moving) -> Goal {
        var g = Goal(
            title: title,
            kind: .adaptive,
            stance: digest.isEmpty ? "Underway." : digest,
            understanding: "with me"
        )
        g.digest = digest
        g.standing = standing
        return g
    }

    /// What Vera settled without you overnight.
    static let changes: [ChangeNote] = [
        ChangeNote(
            lead: "Changed:",
            text: "the memory model picked its shape overnight — git files stay.",
            why: "I compared the two shapes on your own history. Files won because provenance survives a git log and a database row doesn’t — you can read what changed six months from now without me. The database idea would have been faster to query and worse to trust."
        ),
        ChangeNote(
            text: "Your passport watch is still quiet. Everything else is moving normally."
        )
    ]

    /// The line under the attention card: what is moving without you.
    static let quietLine = "Everything else is moving. Your training and the singing work are quiet today."

    // MARK: - The goal a veil sits over
    //
    // 5p rises over the LAN work at the moment it changed direction —
    // which is the diagnostic specimen three transformations in, not a
    // separately drawn screen.

    static var lanInFlight: Goal {
        var g = Specimen.diagnostic.state(at: 3)
        g.digest = "It’s rediscovery, not socket loss — the fix moves into peer discovery."
        g.lastMoved = "changed direction 6m ago · one theory ruled out"
        g.changedSinceYouLooked = true
        g.standing = .moving
        return g
    }

    // MARK: - Under the hood (5v)

    static let machinery: [(String, String)] = [
        ("~/.vera/personal/workouts.db", "SQLite · 214 rows · created by Vera 6 weeks ago"),
        ("~/.vera/personal/training-notes/", "git-backed markdown · free-form session notes"),
        ("session-parser.ts", "42 lines · Vera wrote it to read “185 for 5,5,4” reliably"),
        ("est-max model", "Epley, noted as approximate in every answer")
    ]

    static let machineryNote = "Local only. When this history crosses three months, Vera will raise backup/sync as a decision — yours, because it moves data. Upstairs never says “SQLite”; it says “your training.”"
}
