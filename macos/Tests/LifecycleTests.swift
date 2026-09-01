// What the Mac reports about its own absences, checked without one.
//
// Same arrangement as AskReturnTests: the app has no test target, so
// the decisions were written to be reachable on their own and this
// compiles the real `Lifecycle.swift` beside a `main` that asserts them.
//
//     make test-mac
//
// It is not in the Xcode project, so the app build never sees it.

import AppKit

@MainActor
@main
struct LifecycleTests {
    static var failures = 0

    static func main() {
        let l = LifecycleTracker()
        var slept: [Date] = []
        var woke: [(Date, Date?, TimeInterval)] = []
        var network: [Bool] = []
        l.onSleep = { slept.append($0) }
        l.onWake = { woke.append(($0, $1, $2)) }
        l.onNetwork = { up, _ in network.append(up) }

        let midnight = Date(timeIntervalSince1970: 1_772_150_400)
        let morning = midnight.addingTimeInterval(8 * 3600)

        // Nothing has happened: the machine is here and connected.
        expect(!l.asleep && l.online, "a machine that has said nothing is here")

        // Waking a machine that never slept is not news.
        l.noteWake(at: morning)
        expect(woke.isEmpty, "a wake with no sleep before it says nothing")

        l.noteSleep(at: midnight)
        expect(l.asleep && slept == [midnight], "the sleep is reported when it happens")

        // macOS delivers willSleep more than once in some sequences
        // (a lid shut on an already-sleeping machine). The absence is
        // still one absence.
        l.noteSleep(at: midnight.addingTimeInterval(1))
        expect(slept.count == 1, "the second sleep notification is the same sleep")

        l.noteWake(at: morning)
        expect(!l.asleep, "the machine is awake again")
        expect(woke.count == 1, "the wake is reported once")
        if let w = woke.first {
            expect(w.0 == morning, "the wake says when it woke")
            // The moment it went is carried across the sleep: without
            // it the far side has an end and no span, and eight hours
            // of silence stay eight hours of agents that stalled.
            expect(w.1 == midnight, "the wake carries the moment it went: \(String(describing: w.1))")
            expect(w.2 == 8 * 3600, "the wake says how long it was: \(w.2)")
        }
        expect(l.woke == morning && l.slept == 8 * 3600, "the last absence is remembered")

        // The network. Only a change is news: NWPathMonitor reports the
        // same path on every route change, and an observation per
        // Wi-Fi hop is a log nobody can read.
        l.noteNetwork(true, at: morning)
        expect(network.isEmpty, "already online is not news")
        l.noteNetwork(false, at: morning)
        l.noteNetwork(false, at: morning)
        expect(network == [false], "the network going is said once")
        expect(!l.online, "the machine knows it has no network")
        l.noteNetwork(true, at: morning)
        expect(network == [false, true], "the network coming back is said")

        // How long it was gone, as a person would say it.
        expect(LifecycleTracker.roughly(0) == "0s", "no time at all")
        expect(LifecycleTracker.roughly(-3) == "0s", "a clock that went backwards is not a negative sleep")
        expect(LifecycleTracker.roughly(45) == "45s", "seconds")
        expect(LifecycleTracker.roughly(90) == "1m", "minutes")
        expect(LifecycleTracker.roughly(3600) == "1h", "an exact hour says no minutes")
        expect(LifecycleTracker.roughly(8 * 3600 + 1800) == "8h30m", "hours and minutes")

        if failures > 0 {
            print("\(failures) failed")
            exit(1)
        }
        print("lifecycle: ok")
    }

    static func expect(_ ok: Bool, _ what: String) {
        if !ok {
            print("FAIL: " + what)
            failures += 1
        }
    }
}
