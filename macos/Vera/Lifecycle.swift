import AppKit
import Network
import Observation

// The machine's own lifecycle: asleep or awake, on the network or off it.
//
// Focus.swift says which app the person is looking at. This says whether
// there is a machine to look at one on. The distinction matters because
// everything Vera believes about the agents she is supervising is read
// from silence — a pane that has drawn nothing, a worktree nothing has
// been written to — and silence means nothing at all while the lid is
// shut. So the Mac reports its own absences the way it reports anything
// else: as observations, with the times they happened.
//
// Two things about the shape are forced by the platform. macOS gives a
// handler a moment before it sleeps and no more, so an event sent on the
// way out usually dies in flight; the sleep is therefore remembered and
// reported again on the way back, stamped with when it actually
// happened. And the network is not NSWorkspace's business at all — it is
// NWPathMonitor's — but it belongs here because for an agent whose every
// turn is an API call, no network is nearly the same thing as no machine.

@Observable
@MainActor
final class LifecycleTracker {
    private(set) var asleep = false
    private(set) var online = true
    /// When the machine went, kept across the sleep itself.
    private(set) var went: Date?
    /// The last absence that ended: when it ended, and how long it was.
    private(set) var woke: Date?
    private(set) var slept: TimeInterval = 0

    /// The machine is going. There is almost no time here.
    var onSleep: (Date) -> Void = { _ in }
    /// It is back: when, when it went (nil if that was never seen), and
    /// how long it was gone.
    var onWake: (Date, Date?, TimeInterval) -> Void = { _, _, _ in }
    /// The network came or went.
    var onNetwork: (Bool, Date) -> Void = { _, _ in }

    @ObservationIgnored private var tokens: [NSObjectProtocol] = []
    @ObservationIgnored private var monitor: NWPathMonitor?

    func start() {
        let center = NSWorkspace.shared.notificationCenter
        // Screen sleep and screensaver are deliberately not here: the
        // machine keeps working through both, and an agent that ran all
        // night behind a dark screen ran all night.
        tokens.append(center.addObserver(forName: NSWorkspace.willSleepNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.noteSleep(at: Date()) }
        })
        tokens.append(center.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.noteWake(at: Date()) }
        })
        watchTheNetwork()
    }

    func stop() {
        for t in tokens { NSWorkspace.shared.notificationCenter.removeObserver(t) }
        tokens.removeAll()
        monitor?.cancel()
        monitor = nil
    }

    private func watchTheNetwork() {
        let m = NWPathMonitor()
        monitor = m
        m.pathUpdateHandler = { path in
            let up = path.status == .satisfied
            Task { @MainActor [weak self] in self?.noteNetwork(up, at: Date()) }
        }
        m.start(queue: DispatchQueue(label: "com.incantery.vera.network"))
    }

    // MARK: - The decisions, reachable without a machine to sleep

    /// The lid is shutting. Reported now in case it gets out, and
    /// remembered because it usually does not.
    func noteSleep(at: Date) {
        guard !asleep else { return }
        asleep = true
        went = at
        onSleep(at)
    }

    /// The lid is open. The sleep is reported again first, with the
    /// moment it happened, so the far side has the whole span and not
    /// just its end.
    func noteWake(at: Date) {
        guard asleep else { return }
        asleep = false
        let from = went
        slept = from.map { max(0, at.timeIntervalSince($0)) } ?? 0
        woke = at
        went = nil
        onWake(at, from, slept)
    }

    /// The network came or went. Only a change is news; NWPathMonitor
    /// reports the current path on start and on every route change,
    /// most of which are the same answer twice.
    func noteNetwork(_ up: Bool, at: Date) {
        guard up != online else { return }
        online = up
        onNetwork(up, at)
    }

    /// How long the machine was away, as a person would say it.
    static func roughly(_ seconds: TimeInterval) -> String {
        let s = Int(seconds.rounded())
        if s < 60 { return "\(max(s, 0))s" }
        if s < 3600 { return "\(s / 60)m" }
        let hours = s / 3600, minutes = (s % 3600) / 60
        return minutes == 0 ? "\(hours)h" : "\(hours)h\(minutes)m"
    }
}
