import SwiftUI

// Home is where you have been.
//
// Not a blank conversation waiting for a question — the places on the
// Mac, ranked by frecency, the one in front of you marked. Tap one and
// Vera and rook bring it forward on the desk: an app to the front, a
// rook pane deep-linked in tmux. The phone stops being a second screen
// you glance at and becomes a way to move the machine.

struct HomeView: View {
    @Environment(Conversation.self) private var conversation
    @State private var link: TerminalLink
    @State private var dictating = false
    @State private var chatting = false
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
        .fullScreenCover(isPresented: $dictating) {
            DictateView(client: client).environment(conversation)
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
                    Button { Task { await link.goto(t) } } label: { row(t) }
                        .buttonStyle(.plain)
                        .disabled(link.going != nil)
                }
            }
            .padding(.horizontal, 16).padding(.bottom, 12)
        }
    }

    private func row(_ t: RankedTarget) -> some View {
        HStack(spacing: 12) {
            Image(systemName: t.kind == "pane" ? "terminal" : "app.dashed")
                .font(.system(size: 16))
                .foregroundStyle(t.current == true ? N.accent300 : N.dim)
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 1) {
                Text(t.label).font(N.body(15, .medium)).foregroundStyle(N.text)
                    .lineLimit(1)
                if t.current == true {
                    Text("in front of you now").font(N.body(11)).foregroundStyle(N.accent300)
                }
            }
            Spacer()
            if link.going == t.key {
                ProgressView().controlSize(.small)
            } else {
                Image(systemName: "arrow.up.forward.app").font(.system(size: 13)).foregroundStyle(N.dim)
            }
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
        .background(t.current == true ? N.accent.opacity(0.14) : N.surface, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }

    private var composer: some View {
        HStack(spacing: 12) {
            Button { chatting = true } label: {
                Label("Chat", systemImage: "text.bubble")
                    .font(N.body(15, .medium)).foregroundStyle(N.accent300)
                    .frame(maxWidth: .infinity).padding(.vertical, 14)
                    .background(N.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            }.buttonStyle(.plain)
            Button { dictating = true } label: {
                Label("Dictate", systemImage: "mic.fill")
                    .font(N.body(15, .medium)).foregroundStyle(.white)
                    .frame(maxWidth: .infinity).padding(.vertical, 14)
                    .background(N.accent, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            }.buttonStyle(.plain)
        }
        .padding(.horizontal, 18).padding(.bottom, 12).padding(.top, 6)
        .background(N.bg)
    }
}
