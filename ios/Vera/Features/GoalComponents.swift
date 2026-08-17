import SwiftUI

// The goal page's parts.
//
// Motion rule, applied throughout: nothing animates unless understanding
// or ownership changed. The only ambient motion in the whole product is
// the 2.4s breathing dot on a live pursuit — sub-1Hz, opacity only — and
// it stops the moment the goal settles.

// MARK: - Lifecycle tag

struct LifecycleTag: View {
    let lifecycle: Lifecycle

    var body: some View {
        Text(lifecycle.label)
            .font(VeraFont.body(10.5))
            .foregroundStyle(foreground)
            .padding(.horizontal, 9)
            .padding(.vertical, 3)
            .background(background, in: RoundedRectangle(cornerRadius: 5, style: .continuous))
            .overlay {
                if lifecycle == .needsYou {
                    RoundedRectangle(cornerRadius: 5, style: .continuous)
                        .strokeBorder(Nocturne.accent, lineWidth: 1)
                }
            }
            .animation(.easeInOut(duration: 0.45), value: lifecycle)
    }

    private var foreground: Color {
        lifecycle == .done ? Nocturne.soft : Nocturne.accent300
    }

    private var background: Color {
        switch lifecycle {
        case .withVera: Nocturne.accent.opacity(0.14)
        case .needsYou: .clear
        case .done: Color(hex: 0x787D92, opacity: 0.16)
        }
    }
}

// MARK: - Stance
//
// The strongest object on every goal. Its underline sweeps once when the
// belief succeeds itself and never again — that single fade is how you
// know Vera changed her mind rather than merely did something.

struct StanceBlock: View {
    let text: String
    let isSettled: Bool
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var underline: Double = 0.9

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(text)
                .font(VeraFont.heading(isSettled ? 22 : 20))
                .leading(isSettled ? 22 : 20, 1.42)
                .foregroundStyle(Nocturne.text)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)

            LinearGradient(
                colors: [Nocturne.accent, .clear],
                startPoint: .leading,
                endPoint: .trailing
            )
            .frame(height: 1.5)
            .opacity(underline)
        }
        .onAppear {
            guard !reduceMotion else { underline = 0; return }
            withAnimation(.easeOut(duration: 1.4)) { underline = 0 }
        }
    }
}

// MARK: - Strata
//
// Superseded beliefs, compressed. Only the last two are drawn and each
// is clamped to one line: this is present state with provenance, not a
// feed. Tapping opens the history in place.

struct StratumRow: View {
    let stratum: Stratum
    let elided: Bool
    @Binding var openProvenance: UUID?

    private var isOpen: Bool { openProvenance == stratum.id }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button {
                withAnimation(.easeOut(duration: 0.25)) {
                    openProvenance = isOpen ? nil : stratum.id
                }
            } label: {
                Text((elided ? "… " : "") + stratum.text)
                    .font(VeraFont.body(11.5))
                    .leading(11.5, 1.45)
                    .foregroundStyle(Nocturne.faint)
                    .strikethrough(true, color: Nocturne.barLow)
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if isOpen {
                ProvenancePanel(provenance: stratum.provenance, belief: stratum.text)
                    .transition(.opacity.combined(with: .offset(y: -4)))
            }
        }
    }
}

// MARK: - Provenance
//
// Pass 4: one pattern everywhere — believed / because / when.

struct ProvenancePanel: View {
    let provenance: Provenance
    /// The struck belief, repeated in full because the row above clamps it.
    var belief: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            Text(provenance.kicker.uppercased())
                .font(VeraFont.body(10.5, .medium))
                .tracking(10.5 * 0.12)
                .foregroundStyle(provenance.isStance ? Nocturne.accent300 : Nocturne.soft)

            if let belief {
                Text(belief)
                    .font(VeraFont.body(12))
                    .leading(12, 1.5)
                    .foregroundStyle(Nocturne.soft)
                    .strikethrough(true, color: Nocturne.neutral800)
                    .fixedSize(horizontal: false, vertical: true)
            }

            VStack(alignment: .leading, spacing: 2) {
                Text(provenance.timing)
                    .foregroundStyle(Nocturne.dim)
                ForEach(provenance.rows, id: \.self) { row in
                    Text(row.label).foregroundStyle(Nocturne.soft)
                        + Text(" " + row.value).foregroundStyle(Nocturne.dim)
                }
            }
            .font(VeraFont.body(11))
            .leading(11, 1.6)
            .fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 15)
        .padding(.vertical, 13)
        .background {
            RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                .fill(provenance.isStance ? Nocturne.accent.opacity(0.05) : .clear)
                .overlay {
                    RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                        .strokeBorder(
                            provenance.isStance ? Nocturne.accent : Nocturne.neutral800,
                            lineWidth: 1
                        )
                }
        }
    }
}

// MARK: - Your marks
//
// Yours are solid, Vera's principles are hollow, the world's are grey.
// They dock under the stance and stay for the life of the goal.

struct MarkChip: View {
    let mark: Mark
    @Binding var openProvenance: UUID?

    var body: some View {
        Button {
            guard mark.provenance != nil else { return }
            withAnimation(.easeOut(duration: 0.25)) {
                openProvenance = openProvenance == mark.id ? nil : mark.id
            }
        } label: {
            HStack(spacing: 8) {
                tick
                Text(mark.text)
                    .font(VeraFont.body(11.5))
                    .foregroundStyle(Nocturne.bright)
            }
            .padding(.horizontal, 13)
            .padding(.vertical, 6)
            .overlay(Capsule().strokeBorder(Nocturne.neutral800, lineWidth: 1))
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .disabled(mark.provenance == nil)
    }

    @ViewBuilder private var tick: some View {
        switch mark.kind {
        case .yours:
            Rectangle().fill(Nocturne.accent).frame(width: 10, height: 1.5)
        case .principle:
            RoundedRectangle(cornerRadius: 2)
                .strokeBorder(Nocturne.accent, lineWidth: 1)
                .frame(width: 10, height: 4)
        case .world:
            Rectangle().fill(Nocturne.dim).frame(width: 10, height: 1.5)
        }
    }
}

// MARK: - Pursuits on the spine
//
// Everything on this list exists in relation to the stance. The spine
// runs bright at the top and fades out at the bottom — the same
// end-fading rule the design system gives its rules.

struct PursuitSpine: View {
    let pursuits: [Pursuit]
    let isSettled: Bool

    var body: some View {
        ZStack(alignment: .topLeading) {
            LinearGradient(
                stops: [
                    .init(color: Nocturne.accent, location: 0),
                    .init(color: Nocturne.neutral800, location: 0.45),
                    .init(color: .clear, location: 1)
                ],
                startPoint: .top,
                endPoint: .bottom
            )
            .frame(width: 1.5)
            .padding(.bottom, 8)
            .opacity(isSettled ? 0.35 : 1)

            VStack(alignment: .leading, spacing: 14) {
                ForEach(pursuits) { pursuit in
                    PursuitRow(pursuit: pursuit, isSettled: isSettled)
                }
            }
            .padding(.leading, 22)
        }
        .animation(.timingCurve(0.4, 0, 0.2, 1, duration: 0.6), value: pursuits)
    }
}

struct PursuitRow: View {
    let pursuit: Pursuit
    let isSettled: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            PursuitMark(state: pursuit.state, delay: pursuit.pulseDelay, isSettled: isSettled)

            VStack(alignment: .leading, spacing: 3) {
                Text(pursuit.text)
                    .font(VeraFont.body(pursuit.state == .merged ? 12.5 : 13.5))
                    .leading(pursuit.state == .merged ? 12.5 : 13.5, 1.45)
                    .foregroundStyle(textColor)
                    .strikethrough(pursuit.state == .out, color: Nocturne.faint)
                    .fixedSize(horizontal: false, vertical: true)

                if let note = pursuit.note {
                    Text(note)
                        .font(VeraFont.body(11.5))
                        .leading(11.5, 1.4)
                        .foregroundStyle(pursuit.noteIsLive ? Nocturne.accent300 : Nocturne.dim)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.leading, pursuit.state == .merged ? 14 : 0)
        .opacity(pursuit.state.opacity)
    }

    private var textColor: Color {
        switch pursuit.state {
        case .lead: Nocturne.text
        case .active: Nocturne.bright
        default: Nocturne.dim
        }
    }
}

/// The mark on the spine. Its shape says what kind of line this is:
/// filled and breathing means live, a ring means concluded, hollow means
/// owned-but-not-running or ruled out, an arrow means absorbed.
struct PursuitMark: View {
    let state: PursuitState
    var delay: Double = 0
    var isSettled: Bool = false

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var breathing = false

    var body: some View {
        Group {
            switch state {
            case .lead, .active:
                Circle()
                    .fill(Nocturne.accent)
                    .frame(width: 7, height: 7)
                    .opacity(breathing ? 1 : 0.45)
                    .padding(.top, 6)
                    .onAppear {
                        guard !reduceMotion, !isSettled else { breathing = true; return }
                        withAnimation(
                            .easeInOut(duration: 1.2).repeatForever().delay(delay)
                        ) { breathing = true }
                    }

            case .done:
                Circle()
                    .strokeBorder(Nocturne.accent, lineWidth: 1.5)
                    .frame(width: 7, height: 7)
                    .padding(.top, 6)

            case .out, .waiting, .weak:
                Circle()
                    .strokeBorder(Nocturne.dim, lineWidth: 1)
                    .frame(width: 5, height: 5)
                    .padding(.top, 7)

            case .merged:
                Text("↳")
                    .font(VeraFont.body(12))
                    .foregroundStyle(Nocturne.dim)
                    .padding(.top, 3)

            case .dim:
                Circle()
                    .fill(Nocturne.dim)
                    .frame(width: 5, height: 5)
                    .padding(.top, 7)
            }
        }
        // Leading-aligned in a fixed slot so every mark, whatever its
        // size, hangs off the same line as the spine.
        .frame(width: 12, alignment: .leading)
    }
}

// MARK: - Development
//
// A moment the stance materially moved. It rises once and never loops;
// the next change of understanding clears it.

struct DevelopmentBlock: View {
    let development: Development

    var body: some View {
        CrownedBlock {
            Text(development.kind.label.uppercased())
                .font(VeraFont.body(10.5, .medium))
                .tracking(10.5 * 0.12)
                .foregroundStyle(Nocturne.accent300)

            Text(development.text)
                .font(VeraFont.body(13.5))
                .leading(13.5, 1.55)
                .foregroundStyle(Nocturne.text)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 5)
        }
    }
}

// MARK: - The judgment boundary
//
// The decision rises from the composer region, because that is where
// answers come from. Vera recommends and says why; she never hides the
// alternative or its honest cost.

struct DecisionBlock: View {
    let decision: Decision
    let onAnswer: (Decision.Choice) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            SectionLabel("Your call", color: Nocturne.accent300)

            Text(decision.intro)
                .font(VeraFont.body(13))
                .leading(13, 1.6)
                .foregroundStyle(Nocturne.body)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 8)

            VStack(spacing: 10) {
                ChoiceButton(choice: decision.recommended, isRecommended: true) {
                    onAnswer(decision.recommended)
                }
                ChoiceButton(choice: decision.alternative, isRecommended: false) {
                    onAnswer(decision.alternative)
                }
            }
            .padding(.top, 12)

            Text(decision.after)
                .font(VeraFont.body(12))
                .leading(12, 1.6)
                .foregroundStyle(Nocturne.dim)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 10)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct ChoiceButton: View {
    let choice: Decision.Choice
    let isRecommended: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 0) {
                if isRecommended {
                    Text("VERA RECOMMENDS")
                        .font(VeraFont.body(10.5, .medium))
                        .tracking(10.5 * 0.12)
                        .foregroundStyle(Nocturne.accent300)
                        .padding(.bottom, 5)
                }

                Text(choice.title)
                    .font(VeraFont.body(13.5, .medium))
                    .foregroundStyle(Nocturne.text)
                    .fixedSize(horizontal: false, vertical: true)

                Text(choice.body)
                    .font(VeraFont.body(12))
                    .leading(12, 1.5)
                    .foregroundStyle(Nocturne.body)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 4)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 15)
            .padding(.vertical, 13)
            .background(Nocturne.surface, in: RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                    .strokeBorder(isRecommended ? Nocturne.accent : Nocturne.neutral800, lineWidth: 1)
            }
        }
        .buttonStyle(.plain)
    }
}

// MARK: - Outcome
//
// What the work produced, what was kept, and what deliberately stays
// open. The kept lesson takes the same dashed outline a memory takes in
// conversation, because it is one.

struct OutcomeBlock: View {
    let outcome: Outcome

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(outcome.text)
                .font(VeraFont.body(13.5))
                .leading(13.5, 1.65)
                .foregroundStyle(Nocturne.bright)
                .fixedSize(horizontal: false, vertical: true)

            VStack(alignment: .leading, spacing: 5) {
                SectionLabel("Kept in memory")
                Text(outcome.keptInMemory)
                    .font(VeraFont.body(12.5))
                    .leading(12.5, 1.6)
                    .foregroundStyle(Nocturne.body)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 15)
            .padding(.vertical, 12)
            .background {
                RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                    .strokeBorder(Nocturne.neutral800, style: StrokeStyle(lineWidth: 1, dash: [4, 4]))
            }
            .padding(.top, 16)

            if let open = outcome.deliberatelyOpen {
                Text(open)
                    .font(VeraFont.body(12))
                    .leading(12, 1.55)
                    .foregroundStyle(Nocturne.dim)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 12)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
