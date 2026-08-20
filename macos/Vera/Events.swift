import Foundation
import os

// Context events: what this Mac tells Vera Core about the present.
//
// Semantic, small, transport-independent. The envelope is the same one
// an editor or a browser integration will use; only `type` and the
// payload differ. Nothing here is a Swift object in disguise — it is the
// JSON that goes over the wire, and the Go side keeps whatever it does
// not yet understand.

struct ContextEvent: Encodable, Identifiable, Sendable {
    struct App: Encodable, Sendable {
        let name: String
        let bundleID: String
        enum CodingKeys: String, CodingKey { case name, bundleID = "bundle_id" }
    }

    let id = UUID()
    let type: String
    let device: String
    let at: Date
    var app: App?
    var text: String?

    enum CodingKeys: String, CodingKey { case type, device, at, app, text }

    static func focused(_ app: FocusedApp, device: String) -> ContextEvent {
        ContextEvent(type: "app.focused", device: device, at: app.at, app: App(name: app.name, bundleID: app.bundleID))
    }

    static func unfocused(_ app: FocusedApp, device: String) -> ContextEvent {
        ContextEvent(type: "app.unfocused", device: device, at: app.at, app: App(name: app.name, bundleID: app.bundleID))
    }

    static func plain(_ type: String, device: String, text: String? = nil) -> ContextEvent {
        ContextEvent(type: type, device: device, at: Date(), text: text)
    }

    /// One line for the Health view.
    var summary: String {
        if let app { return "\(type)  \(app.name)" }
        if let text { return "\(type)  \(text)" }
        return type
    }
}

/// The rolling debug log. Everything worth seeing without opening
/// Console: events sent, frames received, state changes, errors.
struct LogEntry: Identifiable, Sendable {
    enum Level: Sendable { case info, event, error }
    let id = UUID()
    let at: Date
    let level: Level
    let text: String
}

@MainActor
final class EventLog {
    private(set) var entries: [LogEntry] = []
    private let limit = 300

    func info(_ text: String) { add(.info, text) }
    func event(_ text: String) { add(.event, text) }
    func error(_ text: String) { add(.error, text) }

    private let system = Logger(subsystem: "com.incantery.vera.mac", category: "vera")

    private func add(_ level: LogEntry.Level, _ text: String) {
        // Mirrored to the unified log so `log stream --predicate
        // 'subsystem == "com.incantery.vera.mac"'` reads the same thing
        // the Health view shows.
        switch level {
        case .error: system.error("\(text, privacy: .public)")
        default: system.info("\(text, privacy: .public)")
        }
        entries.insert(LogEntry(at: Date(), level: level, text: text), at: 0)
        if entries.count > limit { entries.removeLast(entries.count - limit) }
    }
}
