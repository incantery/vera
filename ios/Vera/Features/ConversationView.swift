import SwiftUI

// Hypothesis 2 — the script. The chosen baseline.
//
// Turns stack from the bottom, because the present is the thing you are
// in. They dim and compress as they age; the transcript is scaffolding,
// and the materialized blocks are the truth. There is no header, no
// title, no toolbar: you are not "in a chat screen", you are talking.

struct ConversationView: View {
    @Environment(VeraStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        @Bindable var store = store

        Screen {
            ScriptView(turns: store.turns) { store.go($0) }
        } bottom: {
            Composer(
                text: $store.draft,
                placeholder: store.turns.isEmpty ? store.composerPlaceholder : "…",
                onSubmit: { store.send() }
            )
        }
        .toolbar(.hidden, for: .navigationBar)
        .overlay(alignment: .topLeading) {
            BackChevron { dismiss() }
                .padding(.leading, 16)
                .padding(.top, 4)
        }
    }
}

/// The script itself, reusable: it is also what rises inside the veil.
struct ScriptView: View {
    let turns: [Turn]
    var bottomAligned = true
    var onOpen: (Route) -> Void = { _ in }

    private var liveFrom: Int { turns.liveWindowStart }

    var body: some View {
        GeometryReader { proxy in
            ScrollViewReader { scroller in
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        ForEach(Array(turns.enumerated()), id: \.element.id) { index, turn in
                            TurnView(
                                turn: turn,
                                receded: index < liveFrom,
                                onOpen: onOpen
                            )
                            .id(turn.id)
                            // Motion marks the change in ownership: a
                            // consequence arrives, plain talk just is.
                            .transition(
                                turn.isMaterial
                                    ? .asymmetric(
                                        insertion: .opacity.combined(with: .offset(y: 8)),
                                        removal: .opacity
                                    )
                                    : .opacity
                            )
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 24)
                    .padding(.top, 40)
                    .padding(.bottom, 8)
                    .frame(
                        minHeight: bottomAligned ? proxy.size.height : 0,
                        alignment: .bottom
                    )
                }
                .scrollDismissesKeyboard(.interactively)
                .onChange(of: turns.count) {
                    guard let last = turns.last else { return }
                    withAnimation(.easeOut(duration: 0.28)) {
                        scroller.scrollTo(last.id, anchor: .bottom)
                    }
                }
            }
        }
        .animation(.easeOut(duration: 0.32), value: turns.count)
    }
}
