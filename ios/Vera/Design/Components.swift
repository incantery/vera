import SwiftUI

// The v5 grammar, as reusable parts.
//
// Two rules from the product principles are enforced here rather than
// left to each screen:
//   · A card must earn its container — only materialized consequences
//     and real read-model objects get `surfaceCard`; talk is typography.
//   · Motion only marks changes in understanding or ownership — the
//     only animated component in this file is `MaterializationView`.

// MARK: - Your mark

/// The user's tick. Solid accent: this came from you.
struct UserTick: View {
    var body: some View {
        Rectangle()
            .fill(Nocturne.accent)
            .frame(width: 12, height: 2)
            .padding(.top, 9)
    }
}

/// The composer's state mark. Idle is a short neutral dash; live is a
/// longer accent dash with a glow.
struct ComposerTick: View {
    var isLive: Bool

    var body: some View {
        Rectangle()
            .fill(isLive ? Nocturne.accent : Nocturne.neutral800)
            .frame(width: isLive ? 16 : 10, height: isLive ? 2 : 1.5)
            .shadow(color: isLive ? Nocturne.accent : .clear, radius: 3)
    }
}

// MARK: - Section label

/// `.sec` — 11px, wide tracking, uppercase, tertiary.
struct SectionLabel: View {
    let text: String
    var color: Color = Nocturne.dim

    init(_ text: String, color: Color = Nocturne.dim) {
        self.text = text
        self.color = color
    }

    var body: some View {
        Text(text.uppercased())
            .font(VeraFont.body(11, .medium))
            .tracking(11 * 0.13)
            .foregroundStyle(color)
    }
}

// MARK: - Status header

/// "Vera" plus where she is running. The dot glows because local is the
/// default and worth being quietly proud of — and because the design has
/// named the machine since pass 2 ("Local · Nik's MacBook Pro"), this is
/// also the door to the machines she is running on.
struct StatusHeader: View {
    var locality: String = "Local"
    /// Hollow when nothing is answering: the dot is a claim about
    /// reachability, so it stops making it when it can't.
    var isLive: Bool = true
    var onWordmarkLongPress: (() -> Void)?
    var onLocalityTap: (() -> Void)?

    var body: some View {
        HStack {
            Text("Vera")
                .font(VeraFont.heading(16))
                .tracking(16 * 0.04)
                .foregroundStyle(Nocturne.text)
                .onLongPressGesture { onWordmarkLongPress?() }

            Spacer(minLength: 12)

            Button { onLocalityTap?() } label: {
                HStack(spacing: 7) {
                    Group {
                        if isLive {
                            Circle()
                                .fill(Nocturne.accent)
                                .shadow(color: Nocturne.accent, radius: 4)
                        } else {
                            Circle().strokeBorder(Nocturne.dim, lineWidth: 1)
                        }
                    }
                    .frame(width: 6, height: 6)

                    Text(locality)
                        .font(VeraFont.body(11.5))
                        .foregroundStyle(isLive ? Nocturne.soft : Nocturne.dim)
                }
                .padding(.horizontal, 11)
                .padding(.vertical, 5)
                .background(Nocturne.surface, in: Capsule())
                .overlay(Capsule().strokeBorder(Nocturne.neutral800, lineWidth: 1))
                .contentShape(Capsule())
            }
            .buttonStyle(.plain)
            .disabled(onLocalityTap == nil)
        }
    }
}

// MARK: - Back affordance

struct BackChevron: View {
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: "chevron.left")
                .font(.system(size: 17, weight: .semibold))
                .foregroundStyle(Nocturne.soft)
                .frame(width: 32, height: 32, alignment: .leading)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

// MARK: - Composer

/// The one persistent invitation. Its copy changes with what Vera is
/// holding — "What's on your mind?" when nothing is pending, an answer
/// prompt when something is, a context question inside a surface.
struct Composer: View {
    @Binding var text: String
    var placeholder: String
    /// Home and surfaces show the pill as a target; conversation edits in place.
    var isEditable: Bool = true
    var onTap: (() -> Void)?
    var onSubmit: (() -> Void)?

    @FocusState private var focused: Bool

    private var isLive: Bool { focused || !text.isEmpty }

    var body: some View {
        HStack(spacing: 11) {
            ComposerTick(isLive: isLive)

            ZStack(alignment: .leading) {
                if text.isEmpty {
                    Text(placeholder)
                        .font(VeraFont.body(14))
                        .foregroundStyle(isLive ? Nocturne.bright : Nocturne.dim)
                        .lineLimit(1)
                }
                if isEditable {
                    TextField("", text: $text, axis: .vertical)
                        .font(VeraFont.body(14))
                        .foregroundStyle(Nocturne.text)
                        .tint(Nocturne.accent)
                        .focused($focused)
                        .lineLimit(1...4)
                        .submitLabel(.send)
                        .onSubmit { onSubmit?() }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Button {
                if text.isEmpty { focused = true } else { onSubmit?() }
            } label: {
                Group {
                    if text.isEmpty {
                        MicGlyph()
                    } else {
                        Image(systemName: "arrow.up")
                            .font(.system(size: 15, weight: .semibold))
                            .foregroundStyle(Nocturne.accent)
                    }
                }
                .frame(width: 34, height: 34)
                .contentShape(Circle())
            }
            .buttonStyle(.plain)
        }
        .padding(.leading, 16)
        .padding(.trailing, 8)
        .frame(minHeight: 46)
        .background(Nocturne.surface, in: Capsule())
        .overlay(Capsule().strokeBorder(Nocturne.neutral800, lineWidth: 1))
        .contentShape(Capsule())
        .onTapGesture {
            if let onTap { onTap() } else { focused = true }
        }
    }
}

/// Hand-drawn rather than SF Symbols: the walkthrough's mic is a thin
/// 1.7px stroke, lighter than `mic` at any symbol weight.
struct MicGlyph: View {
    var body: some View {
        Canvas { ctx, size in
            let s = min(size.width, size.height) / 24
            let capsulePath = Path(
                roundedRect: CGRect(x: 9 * s, y: 3 * s, width: 6 * s, height: 11 * s),
                cornerRadius: 3 * s
            )
            ctx.stroke(capsulePath, with: .color(Nocturne.accent), lineWidth: 1.7 * s)

            var arc = Path()
            arc.addArc(
                center: CGPoint(x: 12 * s, y: 11 * s),
                radius: 7 * s,
                startAngle: .degrees(0),
                endAngle: .degrees(180),
                clockwise: false
            )
            ctx.stroke(arc, with: .color(Nocturne.accent), style: StrokeStyle(lineWidth: 1.7 * s, lineCap: .round))

            var stem = Path()
            stem.move(to: CGPoint(x: 12 * s, y: 18 * s))
            stem.addLine(to: CGPoint(x: 12 * s, y: 21 * s))
            ctx.stroke(stem, with: .color(Nocturne.accent), style: StrokeStyle(lineWidth: 1.7 * s, lineCap: .round))
        }
        .frame(width: 16, height: 16)
    }
}

// MARK: - Materialization

/// The accent hairline that crowns a materialized consequence. Inset
/// from both edges and fading out at each end — the same signature the
/// design system gives its rules.
struct MaterializationCrown: View {
    var body: some View {
        LinearGradient(
            colors: [.clear, Nocturne.accent, .clear],
            startPoint: .leading,
            endPoint: .trailing
        )
        .frame(height: 1.5)
        .padding(.horizontal, 20)
    }
}

/// A surface crowned with the accent hairline. Exactly two things earn
/// it: a materialized consequence in conversation, and a development on
/// a goal. Both are the same claim — something just became real.
struct CrownedBlock<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 0) { content }
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 15)
            .padding(.vertical, 12)
            .background(alignment: .top) {
                ZStack(alignment: .top) {
                    RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                        .fill(Nocturne.surface)
                        .elevation(.sm, radius: Nocturne.radiusMd)
                    MaterializationCrown()
                }
            }
    }
}

/// The only object a conversation ever produces. Talk leaves nothing
/// behind; consequence takes a surface.
struct MaterializationView: View {
    let item: Materialization
    var receded: Bool = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(item.kind.kicker.uppercased())
                .font(VeraFont.body(10.5, .medium))
                .tracking(10.5 * 0.12)
                .foregroundStyle(item.kind.isDashed ? Nocturne.dim : Nocturne.accent300)

            if let struck = item.struckLine {
                Text(struck)
                    .font(VeraFont.body(12))
                    .leading(12, 1.5)
                    .foregroundStyle(Nocturne.faint)
                    .strikethrough(true, color: Nocturne.barLow)
                    .padding(.top, 6)
            }

            if let title = item.title {
                Text(title)
                    .font(VeraFont.heading(15))
                    .foregroundStyle(Nocturne.text)
                    .padding(.top, 6)
            }

            Text(item.line)
                .font(VeraFont.body(item.title == nil ? 13.5 : 12.5))
                .leading(item.title == nil ? 13.5 : 12.5, 1.55)
                .foregroundStyle(item.title == nil ? Nocturne.text : Nocturne.body)
                .padding(.top, item.struckLine == nil && item.title == nil ? 6 : 5)

            if let note = item.footnote {
                Text(note)
                    .font(VeraFont.body(11))
                    .leading(11, 1.5)
                    .foregroundStyle(Nocturne.dim)
                    .padding(.top, 6)
            }
        }
        // Provenance wraps rather than truncating: a footnote that ends
        // in an ellipsis is worse than no footnote.
        .fixedSize(horizontal: false, vertical: true)
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 15)
        .padding(.vertical, 12)
        .background(alignment: .top) {
            if item.kind.isDashed {
                RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                    .strokeBorder(
                        Nocturne.neutral800,
                        style: StrokeStyle(lineWidth: 1, dash: [4, 4])
                    )
            } else {
                ZStack(alignment: .top) {
                    RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                        .fill(Nocturne.surface)
                        .elevation(.sm, radius: Nocturne.radiusMd)
                    MaterializationCrown()
                }
            }
        }
        .opacity(receded ? 0.6 : 1)
    }
}

// MARK: - Provenance chip

/// A pill that points back at where something came from. Retrieval
/// answers end in these; they are doorways, not metadata.
struct ProvenanceChip: View {
    let label: String
    var showsTick: Bool = false
    var action: (() -> Void)?

    var body: some View {
        Button { action?() } label: {
            HStack(spacing: 7) {
                if showsTick {
                    Rectangle()
                        .fill(Nocturne.accent)
                        .frame(width: 8, height: 1.5)
                }
                Text(label)
                    .font(VeraFont.body(11))
                    .foregroundStyle(Nocturne.soft)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 5)
            .overlay(Capsule().strokeBorder(Nocturne.neutral800, lineWidth: 1))
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .disabled(action == nil)
    }
}

// MARK: - Veil

/// Hypothesis 1, kept for quick in-context asks: talk rises over
/// whatever you were looking at, which stays legible behind it.
struct Veil<Context: View, Content: View>: View {
    @ViewBuilder var context: Context
    @ViewBuilder var content: Content

    /// Where the talk starts. The context keeps the top third, which is
    /// enough to stay recognisable without competing.
    private let coverage: CGFloat = 0.66

    var body: some View {
        GeometryReader { proxy in
            ZStack(alignment: .bottom) {
                context
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
                    .opacity(0.18)
                    .blur(radius: 1)
                    .allowsHitTesting(false)

                content
                    .padding(.top, 40)
                    .frame(
                        maxWidth: .infinity,
                        maxHeight: proxy.size.height * coverage,
                        alignment: .bottom
                    )
                    .background {
                        LinearGradient(
                            stops: [
                                .init(color: Nocturne.bg.opacity(0), location: 0),
                                .init(color: Nocturne.bg, location: 0.14),
                                .init(color: Nocturne.bg, location: 1)
                            ],
                            startPoint: .top,
                            endPoint: .bottom
                        )
                    }
            }
        }
    }
}

// MARK: - Screen scaffold

/// Every screen: the ground, 24pt gutters, and a composer pinned to the
/// bottom inside the safe area.
struct Screen<Content: View, Bottom: View>: View {
    @ViewBuilder var content: Content
    @ViewBuilder var bottom: Bottom

    var body: some View {
        // The inset is applied closest to the content so a ScrollView
        // inside actually reserves room for the composer instead of
        // running underneath it.
        content
            .safeAreaInset(edge: .bottom, spacing: 0) {
                bottom
                    .padding(.horizontal, 20)
                    .padding(.top, 12)
                    .padding(.bottom, 16)
                    // A long page scrolls *under* the composer, so the
                    // ground fades up behind it rather than cutting.
                    .background {
                        VStack(spacing: 0) {
                            LinearGradient(
                                colors: [Nocturne.bg.opacity(0), Nocturne.bg],
                                startPoint: .top,
                                endPoint: .bottom
                            )
                            .frame(height: 28)
                            Nocturne.bg
                        }
                        .ignoresSafeArea(edges: .bottom)
                    }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            .background(Nocturne.bg.ignoresSafeArea())
    }
}
