import SwiftUI

// A dialogue script, not bubbles.
//
// Your words carry your mark — the solid accent tick from the chips
// grammar. Vera's are plain text, one step quieter. Nothing here has a
// container: the only objects that ever hold a surface are materialized
// consequences, which is how you can feel the difference between talking
// and consequence without being told.

struct TurnView: View {
    let turn: Turn
    let receded: Bool
    var onOpen: (Route) -> Void = { _ in }

    var body: some View {
        switch turn.body {
        case .user(let text):
            UserTurnView(text: text, receded: receded)

        case .vera(let text):
            VeraTurnView(text: text, receded: receded)

        case .material(let item):
            Button {
                if let d = item.destination { onOpen(d) }
            } label: {
                MaterializationView(item: item, receded: receded)
            }
            .buttonStyle(.plain)
            .disabled(item.destination == nil)

        case .stat(let block):
            StatBlockView(block: block, onOpen: onOpen)

        case .chips(let chips):
            ChipRow(chips: chips, onOpen: onOpen)
        }
    }
}

struct UserTurnView: View {
    let text: String
    var receded = false

    private var size: CGFloat { receded ? 13.5 : 15 }

    var body: some View {
        HStack(alignment: .top, spacing: 11) {
            UserTick()
            Text(text)
                .font(VeraFont.body(size))
                .leading(size, 1.55)
                .foregroundStyle(Nocturne.text)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .opacity(receded ? 0.4 : 1)
    }
}

struct VeraTurnView: View {
    let text: String
    var receded = false

    private var size: CGFloat { receded ? 13 : 14 }

    var body: some View {
        Text(text)
            .font(VeraFont.body(size))
            .leading(size, 1.7)
            .foregroundStyle(Nocturne.body)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
            .opacity(receded ? 0.55 : 1)
    }
}

/// Figures Vera quotes while answering. A doorway into the surface that
/// holds them, not a widget that lives in the transcript.
struct StatBlockView: View {
    let block: StatBlock
    var onOpen: (Route) -> Void = { _ in }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            VStack(spacing: 8) {
                ForEach(block.rows, id: \.self) { row in
                    HStack {
                        Text(row.label)
                            .foregroundStyle(Nocturne.soft)
                        Spacer(minLength: 12)
                        HStack(spacing: 5) {
                            Text(row.value)
                                .foregroundStyle(row.isMuted ? Nocturne.dim : Nocturne.text)
                            if let accent = row.accentValue {
                                Text(accent).foregroundStyle(Nocturne.accent300)
                            }
                        }
                    }
                    .font(VeraFont.body(12.5))
                }
            }

            if let label = block.openLabel {
                Text(label)
                    .font(VeraFont.body(11.5))
                    .foregroundStyle(Nocturne.dim)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 15)
        .padding(.vertical, 12)
        .surfaceCard()
        .contentShape(Rectangle())
        .onTapGesture {
            if let d = block.destination { onOpen(d) }
        }
    }
}

struct ChipRow: View {
    let chips: [Chip]
    var onOpen: (Route) -> Void = { _ in }

    var body: some View {
        FlowRow(spacing: 8) {
            ForEach(chips, id: \.self) { chip in
                ProvenanceChip(
                    label: chip.label,
                    showsTick: chip.showsTick,
                    action: chip.destination.map { d in { onOpen(d) } }
                )
            }
        }
    }
}

/// Chips wrap; a horizontal scroller would hide provenance off-screen.
struct FlowRow: Layout {
    var spacing: CGFloat = 8

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, rowHeight: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > 0, x + size.width > width {
                x = 0
                y += rowHeight + spacing
                rowHeight = 0
            }
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
        }
        return CGSize(width: proposal.width ?? x, height: y + rowHeight)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, rowHeight: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > bounds.minX, x + size.width > bounds.maxX {
                x = bounds.minX
                y += rowHeight + spacing
                rowHeight = 0
            }
            view.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
        }
    }
}
