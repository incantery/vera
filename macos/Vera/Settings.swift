import Foundation
import Observation
import ServiceManagement

// What the person can change. Five things, each of which matters today;
// nothing speculative. UserDefaults, because that is what it is for.

@Observable
@MainActor
final class Settings {
    enum OverlayEdge: String, CaseIterable, Identifiable {
        case top, bottom
        var id: String { rawValue }
        var label: String { rawValue.capitalized }
    }

    /// Where Vera Core listens. Loopback, always — this app is one more
    /// peer, and the only thing special about it is being on the machine.
    var coreAddress: String { didSet { save("coreAddress", coreAddress) } }
    var hotkey: KeyBinding { didSet { saveCodable("hotkey", hotkey) } }
    /// CoreAudio uid of the microphone to use; empty means "the first
    /// physical one", which is almost never the system default on a
    /// machine with a virtual audio device installed.
    var microphone: String { didSet { save("microphone", microphone) } }
    var overlayEdge: OverlayEdge { didSet { save("overlayEdge", overlayEdge.rawValue) } }
    /// Hold Fn: what you say is typed at the cursor.
    var fnDictates: Bool { didSet { save("fnDictates", fnDictates) } }
    /// Fn+T opens the panel where you talk to Vera by voice or keyboard.
    var fnOpensVera: Bool { didSet { save("fnOpensVera", fnOpensVera) } }
    /// A tap (under a third of a second) latches listening on until the
    /// next press; a hold listens while held. Off means hold only.
    var tapLatches: Bool { didSet { save("tapLatches", tapLatches) } }
    var launchAtLogin: Bool {
        didSet {
            do {
                if launchAtLogin { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            } catch {
                launchAtLoginProblem = error.localizedDescription
            }
        }
    }
    private(set) var launchAtLoginProblem: String?

    private let defaults = UserDefaults.standard

    init() {
        coreAddress = defaults.string(forKey: "coreAddress") ?? "127.0.0.1:4780"
        hotkey = Self.loadCodable("hotkey", from: defaults) ?? .standard
        microphone = defaults.string(forKey: "microphone") ?? ""
        overlayEdge = OverlayEdge(rawValue: defaults.string(forKey: "overlayEdge") ?? "") ?? .top
        fnDictates = defaults.object(forKey: "fnDictates") as? Bool ?? true
        fnOpensVera = defaults.object(forKey: "fnOpensVera") as? Bool ?? true
        tapLatches = defaults.object(forKey: "tapLatches") as? Bool ?? true
        launchAtLogin = SMAppService.mainApp.status == .enabled
    }

    private func save(_ key: String, _ value: Any) { defaults.set(value, forKey: key) }
    private func saveCodable<T: Encodable>(_ key: String, _ value: T) {
        if let data = try? JSONEncoder().encode(value) { defaults.set(data, forKey: key) }
    }
    private static func loadCodable<T: Decodable>(_ key: String, from defaults: UserDefaults) -> T? {
        guard let data = defaults.data(forKey: key) else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }
}
