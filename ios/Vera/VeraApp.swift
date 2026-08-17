import SwiftUI

@main
struct VeraApp: App {
    @State private var store = VeraStore()
    @State private var fleet = Fleet()
    @Environment(\.scenePhase) private var phase

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(store)
                .environment(fleet)
                .task {
                    store.fleet = fleet
                    fleet.connectAll()
                }
                .onChange(of: phase) { _, new in
                    // Streams are held open while you are looking, and
                    // dropped when you are not. A phone in a pocket has
                    // no business holding a socket to four laptops.
                    switch new {
                    case .active: fleet.connectAll()
                    case .background: fleet.disconnectAll()
                    default: break
                    }
                }
                // Nocturne is a dark system. The light theme is not a
                // half-finished variant here — it does not exist yet.
                .preferredColorScheme(.dark)
                .tint(Nocturne.accent)
        }
    }
}
