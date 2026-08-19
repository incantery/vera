import SwiftUI

@main
struct Vera2App: App {
    @State private var conversation = Conversation()

    var body: some Scene {
        WindowGroup {
            Group {
                if conversation.pairing == nil {
                    PairView { conversation.pairing = $0 }
                } else {
                    ConversationView()
                }
            }
            .environment(conversation)
            .preferredColorScheme(.dark)
            .tint(N.accent)
        }
    }
}
