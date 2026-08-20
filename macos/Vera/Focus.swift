import AppKit
import Observation

// Which application has the person's attention.
//
// Activation is the one focus signal macOS hands out for free — no
// Accessibility permission, no scraping, no guessing. It says which app
// is frontmost and nothing about what is inside it, and that limit is
// preserved all the way to the model: "Ghostty has focus" is a fact,
// what is on its screen is not.

struct FocusedApp: Equatable, Sendable {
    let name: String
    let bundleID: String
    let at: Date
}

@Observable
@MainActor
final class FocusTracker {
    private(set) var current: FocusedApp?
    /// Newest first, bounded. The Health view reads it.
    private(set) var history: [FocusedApp] = []

    var onFocus: (FocusedApp) -> Void = { _ in }
    var onUnfocus: (FocusedApp) -> Void = { _ in }

    @ObservationIgnored private var tokens: [NSObjectProtocol] = []
    private let limit = 50

    func start() {
        let center = NSWorkspace.shared.notificationCenter
        // Delivered on the main queue; the assertion says so out loud
        // rather than leaving the isolation to be inferred.
        tokens.append(center.addObserver(forName: NSWorkspace.didActivateApplicationNotification, object: nil, queue: .main) { [weak self] note in
            let app = Self.describe(note)
            MainActor.assumeIsolated { self?.record(app) }
        })
        tokens.append(center.addObserver(forName: NSWorkspace.didDeactivateApplicationNotification, object: nil, queue: .main) { [weak self] note in
            let app = Self.describe(note)
            MainActor.assumeIsolated { self?.onUnfocus(app) }
        })
        if let front = NSWorkspace.shared.frontmostApplication {
            record(FocusedApp(name: front.localizedName ?? "Unknown", bundleID: front.bundleIdentifier ?? "", at: Date()))
        }
    }

    func stop() {
        for t in tokens { NSWorkspace.shared.notificationCenter.removeObserver(t) }
        tokens.removeAll()
    }

    nonisolated private static func describe(_ note: Notification) -> FocusedApp {
        let app = note.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication
        return FocusedApp(name: app?.localizedName ?? "Unknown", bundleID: app?.bundleIdentifier ?? "", at: Date())
    }

    private func record(_ app: FocusedApp) {
        if let current, current.bundleID == app.bundleID, current.name == app.name { return }
        current = app
        history.insert(app, at: 0)
        if history.count > limit { history.removeLast(history.count - limit) }
        onFocus(app)
    }
}
