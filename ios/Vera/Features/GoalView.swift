import SwiftUI

// The goal page — passes 2–4, unchanged by v5.
//
// One rule organises the whole screen: every element exists in relation
// to one evolving belief. The stance is the center of gravity, the
// superseded stances compress above it, your marks dock under it, and
// the pursuits below carry their relationship to it. Change is legible
// as movement around a fixed anchor.
//
// Reading order, top to bottom:
//   back · lifecycle tag        — who owns this right now
//   title                       — demoted to a kicker; the belief leads
//   strata                      — what Vera used to think
//   STANCE                      — what Vera thinks now
//   understanding note          — how settled that belief is
//   your marks                  — what you and the world put into it
//   development                 — the moment it last moved
//   pursuits on the spine       — what is being done because of it
//   set-aside ledger            — what narrowing has bought
//   your call / outcome         — the boundary, or the end
//   activity                    — machine noise, 10px, and never more
//
// The composer sits under all of it, because an answer is just talk that
// happens to resolve something.

struct GoalView: View {
    @Environment(VeraStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    let goalID: UUID

    @State private var openProvenance: UUID?

    private var goal: Goal {
        store.goal(goalID) ?? Specimen.diagnostic.opening
    }

    var body: some View {
        @Bindable var store = store

        ZStack {
            Nocturne.bg.ignoresSafeArea()

            if store.veilRaised {
                // A conversation rising over the work, 5p. The goal stays
                // legible behind it; the talk never replaces the context.
                Veil {
                    body(of: goal)
                } content: {
                    VStack(spacing: 0) {
                        ScriptView(turns: store.turns) { store.go($0) }
                        Composer(
                            text: $store.draft,
                            placeholder: "…",
                            onSubmit: { store.send() }
                        )
                        .padding(.horizontal, 24)
                        .padding(.top, 12)
                    }
                    .padding(.bottom, 16)
                }
                .transition(.opacity)
            } else {
                Screen {
                    ScrollView { body(of: goal) }
                } bottom: {
                    VStack(spacing: 10) {
                        if store.walkingSpecimen != nil {
                            BeatStepper()
                        }
                        Composer(
                            text: $store.draft,
                            placeholder: composerHint,
                            isEditable: false,
                            onTap: {
                                store.currentGoal = goal
                                withAnimation(.easeOut(duration: 0.3)) { store.veilRaised = true }
                            }
                        )
                    }
                }
            }
        }
        .toolbar(.hidden, for: .navigationBar)
        .onAppear {
            // A board row is a summary. Opening it asks the machine it
            // came from for the pursuits underneath, and keeps asking
            // for as long as the page is up.
            let goal = goal
            if goal.isRemote { store.openRemote(goal) }
        }
        .onDisappear {
            store.veilRaised = false
            store.currentGoal = nil
            store.walkingSpecimen = nil
            store.closeRemote()
        }
    }

    /// The composer says what it is for. On a goal, context is implicit:
    /// "what's left here?" resolves against this goal without an
    /// @-mention or a mode switch.
    private var composerHint: String {
        switch goal.lifecycle {
        case .needsYou: "…or just answer here"
        case .done: "Ask about this goal…"
        case .withVera: "Steer this goal…"
        }
    }

    // MARK: - The page

    @ViewBuilder
    private func body(of goal: Goal) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                BackChevron { dismiss() }
                Spacer()
                LifecycleTag(lifecycle: goal.lifecycle)
            }

            // The goal's name is a kicker. The belief is the headline.
            Text(goal.title)
                .font(VeraFont.body(13))
                .foregroundStyle(Nocturne.dim)
                .padding(.top, 14)

            ForEach(goal.visibleStrata, id: \.stratum.id) { entry in
                StratumRow(
                    stratum: entry.stratum,
                    elided: entry.elided,
                    openProvenance: $openProvenance
                )
                .padding(.top, 10)
            }

            StanceBlock(text: goal.stance, isSettled: goal.lifecycle == .done)
                .id(goal.stanceKey)
                .padding(.top, 12)

            Text(goal.understanding)
                .font(VeraFont.body(11.5))
                .foregroundStyle(goal.understandingIsLive ? Nocturne.accent300 : Nocturne.dim)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 7)

            if !goal.marks.isEmpty {
                FlowRow(spacing: 8) {
                    ForEach(goal.marks) { mark in
                        MarkChip(mark: mark, openProvenance: $openProvenance)
                    }
                }
                .padding(.top, 12)

                if let mark = goal.marks.first(where: { $0.id == openProvenance }),
                   let provenance = mark.provenance {
                    ProvenancePanel(provenance: provenance, belief: mark.text)
                        .padding(.top, 10)
                        .transition(.opacity.combined(with: .offset(y: -4)))
                }
            }

            if let development = goal.development {
                DevelopmentBlock(development: development)
                    .padding(.top, 18)
                    .transition(.opacity.combined(with: .offset(y: 8)))
            }

            if let said = goal.saidByYou {
                (Text("You said: ").foregroundStyle(Nocturne.soft)
                    + Text(said).foregroundStyle(Nocturne.text))
                    .font(VeraFont.body(13))
                    .leading(13, 1.5)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 16)
            }

            PursuitSpine(pursuits: goal.pursuits, isSettled: goal.lifecycle == .done)
                .padding(.top, 20)

            if let ledger = goal.setAsideLine {
                Text(ledger)
                    .font(VeraFont.body(11.5))
                    .leading(11.5, 1.5)
                    .foregroundStyle(Nocturne.dim)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, 22)
                    .padding(.top, 14)
            }

            if let decision = goal.decision {
                DecisionBlock(decision: decision) { choice in
                    store.answer(goalID, with: choice)
                }
                .padding(.top, 22)
                .transition(.opacity.combined(with: .offset(y: 8)))
            }

            if let outcome = goal.outcome {
                OutcomeBlock(outcome: outcome)
                    .padding(.top, 20)
            }

            // Activity is not progress. It gets ten and a half points of
            // monospace at the bottom of the page, and nothing else.
            Text(goal.activity)
                .font(VeraFont.mono(10.5, .regular))
                .foregroundStyle(Nocturne.faint)
                .padding(.top, 18)
                .padding(.bottom, 12)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 24)
        .padding(.top, 8)
        .animation(.easeOut(duration: 0.45), value: goal)
    }
}

// MARK: - Walkthrough scaffold
//
// Not product surface. The design doc walks each specimen through its
// beats beside the phone; this puts the same rail on the device so the
// transformations can be read against the doc.

private struct BeatStepper: View {
    @Environment(VeraStore.self) private var store

    var body: some View {
        guard let specimen = store.walkingSpecimen.flatMap(Specimen.named) else {
            return AnyView(EmptyView())
        }

        return AnyView(
            HStack(spacing: 12) {
                stepButton("chevron.left", enabled: store.beat > 0) { store.step(-1) }

                VStack(alignment: .leading, spacing: 1) {
                    Text("\(store.beat + 1)/\(specimen.beatCount) · \(specimen.beatName(store.beat))")
                        .font(VeraFont.mono(10.5))
                        .foregroundStyle(Nocturne.accent300)
                    Text(specimen.beatCaption(store.beat))
                        .font(VeraFont.body(10.5))
                        .leading(10.5, 1.4)
                        .foregroundStyle(Nocturne.dim)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .frame(maxWidth: .infinity, alignment: .leading)

                stepButton("chevron.right", enabled: store.beat < specimen.beats.count) { store.step(1) }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .background {
                RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                    .strokeBorder(Nocturne.neutral800, style: StrokeStyle(lineWidth: 1, dash: [3, 3]))
            }
        )
    }

    private func stepButton(_ symbol: String, enabled: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(enabled ? Nocturne.soft : Nocturne.neutral800)
                .frame(width: 28, height: 28)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
    }
}
