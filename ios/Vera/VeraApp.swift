import SwiftUI

@main
struct VeraApp: App {
    @State private var store = VeraStore()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(store)
                // Nocturne is a dark system. The light theme is not a
                // half-finished variant here — it does not exist yet.
                .preferredColorScheme(.dark)
                .tint(Nocturne.accent)
        }
    }
}
