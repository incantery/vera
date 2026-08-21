import SwiftUI

// The control centre for this Mac's Vera. Not where you talk to her —
// that is the hotkey — but where you see what she can sense, what she
// is connected to, and why something is not working.

enum Pane: String, CaseIterable, Identifiable {
    case overview, settings, connections, health
    var id: String { rawValue }
    var label: String { rawValue.capitalized }
    var symbol: String {
        switch self {
        case .overview: "circle.grid.2x2"
        case .settings: "slider.horizontal.3"
        case .connections: "point.3.connected.trianglepath.dotted"
        case .health: "waveform.path.ecg"
        }
    }
}

struct MainWindow: View {
    @Environment(Station.self) private var station
    @State private var pane: Pane = .overview

    var body: some View {
        NavigationSplitView {
            List(Pane.allCases, selection: $pane) { s in
                Label(s.label, systemImage: s.symbol).tag(s)
            }
            .navigationSplitViewColumnWidth(min: 160, ideal: 180)
        } detail: {
            Group {
                switch pane {
                case .overview: OverviewView()
                case .settings: SettingsView()
                case .connections: ConnectionsView()
                case .health: HealthView()
                }
            }
            .navigationTitle(pane.label)
        }
    }
}

// MARK: - Shared bits

private struct StateDot: View {
    let ok: Bool?
    var body: some View {
        Circle()
            .fill(ok == nil ? Color.secondary.opacity(0.4) : ok! ? Color.green : Color.orange)
            .frame(width: 8, height: 8)
    }
}

private struct Row: View {
    let label: String
    let value: String
    var ok: Bool? = nil
    var body: some View {
        LabeledContent {
            HStack(spacing: 8) {
                Text(value).foregroundStyle(.secondary).multilineTextAlignment(.trailing)
                if ok != nil { StateDot(ok: ok) }
            }
        } label: {
            Text(label)
        }
    }
}

private let clock: DateFormatter = {
    let f = DateFormatter()
    f.dateFormat = "HH:mm:ss"
    return f
}()

// MARK: - Overview

struct OverviewView: View {
    @Environment(Station.self) private var station

    var body: some View {
        Form {
            Section("Now") {
                Row(label: "Vera Core", value: station.core.state.label, ok: station.core.isConnected)
                Row(label: "This Mac", value: station.core.device)
                Row(label: "Looking at", value: lookingAt)
                Row(label: "Voice", value: voiceLabel)
                Row(label: "Hotkey", value: station.hotkey.registered?.display ?? (station.hotkey.problem ?? "not registered"), ok: station.hotkey.registered != nil)
            }
            Section("Latest") {
                if let last = station.interactions.first {
                    VStack(alignment: .leading, spacing: 6) {
                        HStack {
                            Text("You").font(.caption).foregroundStyle(.secondary)
                            if let focus = last.focus {
                                Text("· in \(focus)").font(.caption).foregroundStyle(.tertiary)
                            }
                            Spacer()
                            Text(clock.string(from: last.at)).font(.caption.monospacedDigit()).foregroundStyle(.tertiary)
                        }
                        Text(last.said)
                        Text("Vera").font(.caption).foregroundStyle(.secondary).padding(.top, 4)
                        if let error = last.error {
                            Text(error).foregroundStyle(.orange)
                        } else {
                            Text(last.answer.isEmpty ? "…" : last.answer)
                        }
                    }
                    .padding(.vertical, 4)
                } else {
                    Text("Hold \(station.settings.hotkey.display) and say something.")
                        .foregroundStyle(.secondary)
                }
            }
            if let status = station.core.status {
                Section("Capabilities") {
                    ForEach(status.providers) { p in
                        Row(label: p.name.capitalized, value: p.installed ? "installed" : "not installed", ok: p.installed ? true : nil)
                    }
                    ForEach(status.integrations) { i in
                        Row(label: i.name.capitalized, value: i.connected ? "connected" : "not installed", ok: i.connected ? true : nil)
                    }
                    if status.runsInFlight > 0 {
                        Row(label: "Working on", value: "\(status.runsInFlight) run\(status.runsInFlight == 1 ? "" : "s")")
                    }
                }
            }
        }
        .formStyle(.grouped)
    }

    /// The app, and — when rook can see inside it — what is inside.
    private var lookingAt: String {
        guard let app = station.focus.current else { return "—" }
        if let terminal = station.core.me?.terminal, isTerminal(app) {
            return "\(app.name) → \(terminal.describe)"
        }
        return app.name
    }

    private func isTerminal(_ app: FocusedApp) -> Bool {
        let id = (app.bundleID + " " + app.name).lowercased()
        return ["ghostty", "terminal", "iterm", "rook", "kitty", "alacritty", "wezterm"].contains { id.contains($0) }
    }

    private var voiceLabel: String {
        switch station.voice {
        case .idle: "idle"
        case .listening: "listening"
        case .thinking: "thinking"
        }
    }
}

// MARK: - Settings

struct SettingsView: View {
    @Environment(Station.self) private var station

    var body: some View {
        @Bindable var settings = station.settings
        Form {
            Section {
                HStack(spacing: 14) {
                    Toggle("⌃", isOn: $settings.hotkey.control)
                    Toggle("⌥", isOn: $settings.hotkey.option)
                    Toggle("⇧", isOn: $settings.hotkey.shift)
                    Toggle("⌘", isOn: $settings.hotkey.command)
                    Picker("Key", selection: $settings.hotkey.keyCode) {
                        ForEach(KeyBinding.keys) { key in
                            Text(key.name).tag(key.code)
                        }
                    }
                    .labelsHidden()
                    .frame(width: 100)
                }
                .toggleStyle(.button)
                .onChange(of: settings.hotkey) { station.applyHotkey() }
                if !settings.hotkey.hasModifier {
                    Text("A bare key would swallow that key everywhere. Add a modifier.")
                        .font(.caption).foregroundStyle(.orange)
                } else if let problem = station.hotkey.problem {
                    Text(problem).font(.caption).foregroundStyle(.orange)
                } else {
                    Text("Hold \(settings.hotkey.display) to talk; release to send.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Toggle("A quick tap keeps listening until the next press", isOn: $settings.tapLatches)
            } header: {
                Text("Hotkey")
            }

            Section {
                Toggle("Hold Fn to dictate — what you say is typed at the cursor", isOn: $settings.fnDictates)
                    .onChange(of: settings.fnDictates) { station.applyKeyTap() }
                Toggle("Fn+T opens Vera — ask by typing or voice", isOn: $settings.fnOpensVera)
                    .onChange(of: settings.fnOpensVera) { station.applyKeyTap() }
                if !station.accessibility {
                    HStack {
                        Text("Needs Accessibility to watch Fn and type at the cursor.")
                            .font(.caption).foregroundStyle(.orange)
                        Spacer()
                        Button("Grant Accessibility…") { station.requestAccessibility() }
                    }
                } else if let problem = station.keys.problem {
                    Text(problem).font(.caption).foregroundStyle(.orange)
                } else {
                    Text("In System Settings → Keyboard, set “Press 🌐 key to” → Do Nothing, and quit Wispr Flow or anything else that owns Fn.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            } header: {
                Text("Fn key")
            }

            Section("Microphone") {
                Picker("Microphone", selection: $settings.microphone) {
                    Text("First physical microphone").tag("")
                    ForEach(Microphones.all()) { mic in
                        Text(mic.name + (mic.virtualDevice ? " (virtual)" : "")).tag(mic.uid)
                    }
                }
                Text("The system default is ignored on purpose — on a Mac with a virtual audio device it is usually silence.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("Overlay") {
                Picker("Appears at", selection: $settings.overlayEdge) {
                    ForEach(Settings.OverlayEdge.allCases) { Text($0.label).tag($0) }
                }
                .onChange(of: settings.overlayEdge) { station.applyOverlayEdge() }
            }

            Section("Launch") {
                Toggle("Open Vera at login", isOn: $settings.launchAtLogin)
                if let problem = settings.launchAtLoginProblem {
                    Text(problem).font(.caption).foregroundStyle(.orange)
                }
            }

            Section {
                TextField("Address", text: $settings.coreAddress)
                    .onSubmit { station.applyCoreAddress() }
                Text("Loopback only. Vera Core is `go run ./cmd/vera` on this machine.")
                    .font(.caption).foregroundStyle(.secondary)
            } header: {
                Text("Vera Core")
            }
        }
        .formStyle(.grouped)
    }
}

// MARK: - Connections

struct ConnectionsView: View {
    @Environment(Station.self) private var station

    var body: some View {
        Form {
            Section("Vera Core") {
                Row(label: "Status", value: station.core.state.label, ok: station.core.isConnected)
                if let s = station.core.status {
                    Row(label: "Machine", value: "\(s.name) · \(s.peer)")
                    Row(label: "Version", value: s.version)
                    Row(label: "Mind", value: s.mind)
                    Row(label: "Up since", value: s.since.formatted(date: .abbreviated, time: .shortened))
                } else if let why = station.core.lastError {
                    Text(why).foregroundStyle(.orange)
                }
            }
            if let s = station.core.status {
                Section("Providers") {
                    ForEach(s.providers) { p in
                        VStack(alignment: .leading, spacing: 2) {
                            Row(label: p.name.capitalized, value: p.installed ? "installed" : "not installed", ok: p.installed ? true : nil)
                            if let detail = p.detail { Text(detail).font(.caption).foregroundStyle(.tertiary) }
                            Text(p.capabilities.isEmpty ? "no capabilities advertised yet" : p.capabilities.joined(separator: ", "))
                                .font(.caption).foregroundStyle(.tertiary)
                        }
                    }
                }
                Section("Integrations") {
                    ForEach(s.integrations) { i in
                        Row(label: i.name.capitalized,
                            value: i.connected ? "connected" : (i.lastSeen.map { "last seen \(clock.string(from: $0))" } ?? "not installed"),
                            ok: i.connected ? true : nil)
                    }
                }
                Section("Devices") {
                    ForEach(s.devices) { d in
                        VStack(alignment: .leading, spacing: 2) {
                            Row(label: d.name,
                                value: (d.focus.map { "\($0.name) · " } ?? "") + (d.fresh ? "present" : "away"),
                                ok: d.fresh)
                            if let t = d.terminal {
                                Text("rook: " + t.describe).font(.caption).foregroundStyle(.tertiary)
                            }
                        }
                    }
                }
            }
        }
        .formStyle(.grouped)
    }
}

// MARK: - Health

struct HealthView: View {
    @Environment(Station.self) private var station
    @State private var tab = 0

    var body: some View {
        VStack(spacing: 0) {
            Form {
                Section("Signals") {
                    Row(label: "Vera Core", value: station.core.state.label, ok: station.core.isConnected)
                    Row(label: "Last status", value: station.core.lastStatusAt.map { clock.string(from: $0) } ?? "—")
                    Row(label: "Paired as", value: station.core.pairing.map { "\($0.name) · \($0.peer)" } ?? "not yet", ok: station.core.pairing != nil)
                    Row(label: "Hotkey", value: station.hotkey.registered?.display ?? "not registered", ok: station.hotkey.registered != nil)
                    Row(label: "Accessibility", value: station.accessibility ? "granted" : "not granted", ok: station.accessibility)
                    Row(label: "Fn key tap", value: station.keys.running ? "running" : (station.keys.problem ?? "off"), ok: station.keys.running ? true : nil)
                    Row(label: "Microphone", value: station.listener.micAuthorized ? "allowed" : "not yet asked / denied", ok: station.listener.micAuthorized ? true : nil)
                    Row(label: "Speech recognition", value: station.listener.speechAuthorized ? (station.listener.onDevice ? "allowed · on-device" : "allowed · Apple's servers") : "not yet asked / denied", ok: station.listener.speechAuthorized ? true : nil)
                    Row(label: "Capturing", value: station.listener.isListening ? "yes" : "no")
                    Row(label: "Microphone in use", value: station.listener.microphone ?? "—")
                    Row(label: "Focus", value: station.focus.current.map { "\($0.name) (\($0.bundleID))" } ?? "—")
                    Row(label: "Last event", value: station.recentEvents.first?.summary ?? "—")
                    Row(label: "App", value: "\(Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") ?? "?") (\(Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") ?? "?"))")
                    Row(label: "Vera Core", value: station.core.status?.version ?? "—")
                }
            }
            .formStyle(.grouped)
            .frame(maxHeight: 330)

            Picker("", selection: $tab) {
                Text("Log").tag(0)
                Text("Focus").tag(1)
                Text("Events").tag(2)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .padding(.horizontal)
            .padding(.bottom, 6)

            List {
                switch tab {
                case 0:
                    ForEach(station.log.entries) { e in
                        HStack(alignment: .top, spacing: 8) {
                            Text(clock.string(from: e.at)).font(.caption.monospacedDigit()).foregroundStyle(.tertiary)
                            Text(e.text).font(.caption.monospaced())
                                .foregroundStyle(e.level == .error ? .orange : e.level == .event ? .secondary : .primary)
                                .textSelection(.enabled)
                        }
                    }
                case 1:
                    ForEach(Array(station.focus.history.enumerated()), id: \.offset) { _, app in
                        HStack(spacing: 8) {
                            Text(clock.string(from: app.at)).font(.caption.monospacedDigit()).foregroundStyle(.tertiary)
                            Text(app.name).font(.caption)
                            Text(app.bundleID).font(.caption).foregroundStyle(.tertiary)
                        }
                    }
                default:
                    ForEach(station.recentEvents) { e in
                        HStack(spacing: 8) {
                            Text(clock.string(from: e.at)).font(.caption.monospacedDigit()).foregroundStyle(.tertiary)
                            Text(e.summary).font(.caption.monospaced())
                        }
                    }
                }
            }
            .listStyle(.inset)
        }
    }
}
