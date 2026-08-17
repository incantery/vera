import SwiftUI

// 5s — Vera product principles.
//
// The strata grammar carries revision here too: a principle supersedes
// itself the same way a stance does. Nothing is deleted, the wrong line
// strikes, and every entry points back at the conversation or the work
// that created it.

struct PrinciplesView: View {
    @Environment(VeraStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        @Bindable var store = store

        Screen {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    HStack {
                        BackChevron { dismiss() }
                        Spacer()
                        Text("Kept by Vera · git-backed")
                            .font(VeraFont.body(12))
                            .foregroundStyle(Nocturne.dim)
                    }

                    Text("Vera product principles")
                        .font(VeraFont.heading(22))
                        .foregroundStyle(Nocturne.text)
                        .padding(.top, 16)

                    VStack(alignment: .leading, spacing: 18) {
                        ForEach(store.principles) { principle in
                            PrincipleRow(principle: principle)
                        }
                    }
                    .padding(.top, 20)

                    if !store.memories.isEmpty {
                        VStack(alignment: .leading, spacing: 14) {
                            SectionLabel("What I hold about you")
                            ForEach(store.memories) { memory in
                                MemoryRow(memory: memory)
                            }
                        }
                        .padding(.top, 30)
                    }
                }
                .padding(.horizontal, 24)
                .padding(.top, 8)
                .padding(.bottom, 12)
            }
        } bottom: {
            Composer(
                text: $store.draft,
                placeholder: "When did I first start thinking this?",
                isEditable: false,
                onTap: { store.openConversation() }
            )
        }
        .toolbar(.hidden, for: .navigationBar)
    }
}

private struct PrincipleRow: View {
    let principle: Principle

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let old = principle.supersedes {
                Text(old)
                    .font(VeraFont.body(12))
                    .leading(12, 1.45)
                    .foregroundStyle(Nocturne.faint)
                    .strikethrough(true, color: Nocturne.barLow)
                    .padding(.bottom, 5)
            }

            Text(principle.text)
                .font(VeraFont.body(14))
                .leading(14, 1.5)
                .foregroundStyle(Nocturne.text)

            Text(principle.provenance + " ›")
                .font(VeraFont.body(11))
                .foregroundStyle(principle.revised ? Nocturne.accent300 : Nocturne.dim)
                .padding(.top, 4)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct MemoryRow: View {
    let memory: Memory

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let correction = memory.supersededBy {
                Text(memory.belief)
                    .font(VeraFont.body(12))
                    .leading(12, 1.45)
                    .foregroundStyle(Nocturne.faint)
                    .strikethrough(true, color: Nocturne.barLow)
                Text(correction)
                    .font(VeraFont.body(13.5))
                    .leading(13.5, 1.5)
                    .foregroundStyle(Nocturne.text)
                    .padding(.top, 5)
            } else {
                Text(memory.belief)
                    .font(VeraFont.body(13.5))
                    .leading(13.5, 1.5)
                    .foregroundStyle(Nocturne.text)
            }

            Text(memory.provenance)
                .font(VeraFont.body(11))
                .foregroundStyle(Nocturne.dim)
                .padding(.top, 4)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 15)
        .padding(.vertical, 12)
        .background {
            RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                .strokeBorder(Nocturne.neutral800, style: StrokeStyle(lineWidth: 1, dash: [4, 4]))
        }
    }
}
