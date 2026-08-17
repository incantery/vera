import SwiftUI

// One stack. There is no tab bar, because there are no modules — every
// surface is reached from the relationship, either by talking or by
// following a "›" out of something Vera said.

struct RootView: View {
    @Environment(VeraStore.self) private var store

    var body: some View {
        @Bindable var store = store

        NavigationStack(path: $store.path) {
            HomeView()
                .navigationDestination(for: Route.self) { route in
                    switch route {
                    case .conversation:
                        ConversationView()
                    case .training:
                        TrainingView()
                    case .principles:
                        PrinciplesView()
                    case .goal(let id):
                        GoalView(goalID: id)
                    }
                }
        }
        .sheet(isPresented: $store.showingWalkthrough) { WalkthroughSheet() }
        .sheet(isPresented: $store.showingUnderTheHood) { UnderTheHoodView() }
    }
}
