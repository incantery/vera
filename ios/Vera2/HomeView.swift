import SwiftUI

// Home is where you have been.
//
// Not a blank conversation waiting for a question — the places on the
// Mac, ranked by frecency, the one in front of you marked. Tap a pane
// and you go INTO it: the snapshot, dictation, and the session's own
// buttons, on the phone. Tap an app and there is nothing to enter, so
// Vera and rook just bring it to the front of the desk. The phone stops
// being a second screen you glance at and becomes a way to work the
// machine.

struct HomeView: View {
    @Environment(Conversation.self) private var conversation
    @State private var link: TerminalLink
    @State private var chatting = false
    @State private var dictateTo: RankedTarget?
    private let client: Client

    init(client: Client) {
        self.client = client
        _link = State(initialValue: TerminalLink(client: client))
    }

    var body: some View {
        ZStack {
            N.bg.ignoresSafeArea()
            VStack(spacing: 0) {
                header
                if link.targets.isEmpty {
                    Spacer()
                    Text(link.watching ? "Nowhere yet — use the Mac for a moment." : (link.problem ?? "Finding the Mac…"))
                        .font(N.body(14)).foregroundStyle(N.dim)
                        .multilineTextAlignment(.center).padding(.horizontal, 40)
                    Spacer()
                } else {
                    places
                }
                composer
            }
        }
        .task { link.start() }
        .onDisappear { link.stop() }
        .fullScreenCover(item: $dictateTo) { target in
            DictateView(client: client, pinned: target).environment(conversation)
        }
        .fullScreenCover(isPresented: $chatting) {
            NavigationStack { ConversationView().environment(conversation) }
        }
    }

    private var header: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(conversation.pairing?.name ?? "Vera").font(N.body(17, .semibold)).foregroundStyle(N.text)
                Text(link.watching ? "connected" : "reaching…").font(N.body(11)).foregroundStyle(N.dim)
            }
            Spacer()
            Button("Unpair") { conversation.unpair() }
                .font(N.body(12)).foregroundStyle(N.dim).buttonStyle(.plain)
        }
        .padding(.horizontal, 22).padding(.top, 18).padding(.bottom, 14)
    }

    private var places: some View {
        ScrollView {
            VStack(spacing: 8) {
                ForEach(link.targets) { t in
                    row(t)
                }
            }
            .padding(.horizontal, 16).padding(.bottom, 12)
        }
    }

    private func row(_ t: RankedTarget) -> some View {
        Button {
            // Tap anything to go into it: the Mac follows you there, and
            // the panel shows whatever that thing can do — a pane's screen
            // and dictation, or an app's buttons.
            dictateTo = t
        } label: {
            HStack(spacing: 12) {
                Image(systemName: t.kind == "pane" ? "terminal" : "app.dashed")
                    .font(.system(size: 16))
                    .foregroundStyle(t.current == true ? N.accent300 : N.dim)
                    .frame(width: 24)
                VStack(alignment: .leading, spacing: 1) {
                    Text(t.label).font(N.body(15, .medium)).foregroundStyle(N.text).lineLimit(1)
                    if t.current == true {
                        Text("in front of you now").font(N.body(11)).foregroundStyle(N.accent300)
                    }
                }
                Spacer()
                if link.going == t.key { ProgressView().controlSize(.small) }
                else {
                    Image(systemName: t.kind == "pane" ? "chevron.right" : "arrow.up.forward.app")
                        .font(.system(size: 13)).foregroundStyle(N.dim)
                }
            }
            .padding(.horizontal, 14).padding(.vertical, 10)
            .background(t.current == true ? N.accent.opacity(0.14) : N.surface, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        }
        .buttonStyle(.plain)
        .disabled(link.going != nil)
    }

    private var composer: some View {
        Button { chatting = true } label: {
            Label("Chat with Vera", systemImage: "text.bubble")
                .font(N.body(15, .medium)).foregroundStyle(N.accent300)
                .frame(maxWidth: .infinity).padding(.vertical, 14)
                .background(N.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 18).padding(.bottom, 12).padding(.top, 6)
        .background(N.bg)
    }
}
