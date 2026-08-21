import AppKit
import SwiftUI

// Vera on the Mac.
//
// This app is the Mac's senses and face: it knows which application is
// in front of you, it hears you when you ask it to, and it shows a small
// surface with the answer. It holds no model, no memory and no opinion —
// all of that is Vera Core (cmd/vera), one HTTP hop away on this
// machine. Everything this app learns goes there as an observation;
// everything it says comes back from there as frames.

@main
struct VeraApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

    var body: some Scene {
        Window("Vera", id: "main") {
            MainWindow()
                .environment(Station.shared)
                .frame(minWidth: 640, minHeight: 420)
        }
        .defaultSize(width: 760, height: 520)
        .commands {
            CommandGroup(replacing: .newItem) {}
        }

        MenuBarExtra {
            MenuBarMenu().environment(Station.shared)
        } label: {
            MenuBarLabel().environment(Station.shared)
        }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        Station.shared.start()
    }

    // Closing the window leaves Vera in the menu bar. That is the whole
    // point of a menu bar item.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationWillTerminate(_ notification: Notification) {
        Station.shared.stop()
    }
}

// MARK: - Menu bar

struct MenuBarLabel: View {
    @Environment(Station.self) private var station

    var body: some View {
        Image(systemName: symbol)
    }

    private var symbol: String {
        switch station.voice {
        case .listening: return "waveform.circle.fill"
        case .thinking: return "ellipsis.circle.fill"
        case .idle: return station.core.isConnected ? "circle.fill" : "circle.dotted"
        }
    }
}

struct MenuBarMenu: View {
    @Environment(Station.self) private var station
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Text(station.core.isConnected ? "Vera Core connected" : "Vera Core \(station.core.state.label.lowercased())")
        if let focus = station.focus.current {
            Text("Looking at \(focus.name)")
        }
        Divider()
        Button("Open Vera") {
            openWindow(id: "main")
            NSApp.activate(ignoringOtherApps: true)
        }
        .keyboardShortcut("o")
        Button(station.voice == .idle ? "Start listening" : "Stop listening") {
            station.toggleVoice()
        }
        Divider()
        Button("Quit Vera") { NSApp.terminate(nil) }
            .keyboardShortcut("q")
    }
}
