import SwiftUI

@main
struct VeraApp: App {
    @State private var conversation = Conversation()

    var body: some Scene {
        WindowGroup {
            Group {
                if conversation.pairing == nil {
                    PairView { conversation.pairing = $0 }
                } else if let pairing = conversation.pairing {
                    HomeView(client: Client(pairing: pairing, conversation: conversation.conversationID))
                }
            }
            .environment(conversation)
            .preferredColorScheme(.dark)
            .tint(N.accent)
        }
    }
}
