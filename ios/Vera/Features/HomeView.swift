import SwiftUI

// Home — where I am with Vera.
//
// Four states, one code path. The attention headline wins the screen
// when something has crossed the threshold into needing a person; when
// nothing has, Home is comfortable saying so and stopping. No fabricated
// digest, no suggested prompts filling the void: the emptiness is the
// message.

struct HomeView: View {
    @Environment(VeraStore.self) private var store
    @Environment(Fleet.self) private var fleet

    var body: some View {
        @Bindable var store = store

        Screen {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    StatusHeader(
                        locality: fleet.localityLine,
                        isLive: fleet.isConnected || !fleet.hasConnections,
                        onWordmarkLongPress: { store.showingWalkthrough = true },
                        onLocalityTap: { store.showingConnections = true }
                    )
                    .padding(.bottom, 20)

                    switch store.homeState {
                    case .needsYou(let selection):
                        NeedsYouBody(selection: selection)
                    case .changed(let selection, let notes):
                        ChangedBody(selection: selection, notes: notes)
                    case .around(let links):
                        AroundYouBody(links: links)
                    case .silence:
                        SilenceBody()
                    }

                    // A machine out of reach is work news, not an
                    // alert: 4i tells you the Mac went to sleep and
                    // that one pursuit paused, in Vera's own voice.
                    if let away = fleet.awayLine {
                        Text(away)
                            .font(VeraFont.body(12))
                            .leading(12, 1.6)
                            .foregroundStyle(Nocturne.dim)
                            .fixedSize(horizontal: false, vertical: true)
                            .padding(.top, 22)
                            .padding(.horizontal, 18)
                    }
                }
                .padding(.horizontal, 24)
                .padding(.top, 12)
            }
            .scrollBounceBehavior(.basedOnSize)
            .onDisappear { fleet.stoppedLooking() }
        } bottom: {
            Composer(
                text: $store.draft,
                placeholder: store.composerPlaceholder,
                isEditable: false,
                onTap: { store.openConversation() }
            )
        }
        .toolbar(.hidden, for: .navigationBar)
    }
}

// MARK: - Something needs you
//
// One card, never two. Everything else that is asking gets a quiet row
// with the cost of waiting stated — attention is a scarce resource and
// three pending asks are not an inbox of three.

private struct NeedsYouBody: View {
    @Environment(VeraStore.self) private var store
    let selection: HomeSelection

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Headline(text: selection.headline, subhead: selection.subhead, size: 26)

            if let card = selection.card {
                VStack(alignment: .leading, spacing: 0) {
                    SectionLabel("Needs you", color: Nocturne.accent300)
                        .padding(.bottom, 9)

                    AskCard(goal: card) { store.openDecision(card) }

                    ForEach(selection.lesserAsks) { ask in
                        LesserAskRow(goal: ask) { store.go(.goal(ask.id)) }
                            .padding(.top, 14)
                    }
                }
            }

            WithVeraSection(selection: selection)
        }
    }
}

/// The one ask worth a container. The crown says a consequence landed;
/// the kicker says which goal it came out of; the question leads.
private struct AskCard: View {
    let goal: Goal
    let onDecide: () -> Void

    var body: some View {
        Button(action: onDecide) {
            VStack(alignment: .leading, spacing: 0) {
                HStack(alignment: .firstTextBaseline, spacing: 10) {
                    Text(goal.title)
                        .font(VeraFont.body(12))
                        .foregroundStyle(Nocturne.dim)
                    Spacer(minLength: 0)
                    if let blocked = goal.stakes?.blocked {
                        Text(blocked)
                            .font(VeraFont.body(11))
                            .foregroundStyle(Nocturne.accent300)
                    }
                }

                if let decision = goal.decision {
                    Text("\(decision.recommended.title), or \(decision.alternative.title.lowercased())?")
                        .font(VeraFont.heading(14.5))
                        .leading(14.5, 1.5)
                        .foregroundStyle(Nocturne.text)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.top, 6)

                    Text("I recommend \(decision.recommended.keyword). \(decision.recommended.body)")
                        .font(VeraFont.body(12.5))
                        .leading(12.5, 1.55)
                        .foregroundStyle(Nocturne.body)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.top, 5)
                } else {
                    // A goal off the wire has no options attached — the
                    // board says *that* it needs you and why, not what
                    // the choices are. Showing Vera's own sentence is
                    // better than showing a title and a button.
                    Text(goal.stance)
                        .font(VeraFont.heading(14.5))
                        .leading(14.5, 1.5)
                        .foregroundStyle(Nocturne.text)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.top, 6)
                }

                HStack(spacing: 6) {
                    Text("Decide")
                    Image(systemName: "chevron.right")
                        .font(.system(size: 9, weight: .semibold))
                }
                .font(VeraFont.body(12.5, .medium))
                .foregroundStyle(Nocturne.accent300)
                .padding(.top, 9)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 18)
            .padding(.vertical, 15)
            .background(alignment: .top) {
                ZStack(alignment: .top) {
                    RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                        .fill(Nocturne.surface)
                        .elevation(.md, radius: Nocturne.radiusMd)
                    MaterializationCrown()
                }
            }
        }
        .buttonStyle(.plain)
    }
}

/// An ask that can wait. No container — the row *is* the statement that
/// this one is lighter, and it says so in words too.
private struct LesserAskRow: View {
    let goal: Goal
    let onOpen: () -> Void

    var body: some View {
        Button(action: onOpen) {
            HStack(alignment: .top, spacing: 10) {
                (Text(goal.title).foregroundStyle(Nocturne.bright)
                    + Text(" — \(goal.digest)").foregroundStyle(Nocturne.soft))
                    .font(VeraFont.body(12.5))
                    .leading(12.5, 1.5)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)

                Image(systemName: "chevron.right")
                    .font(.system(size: 8, weight: .semibold))
                    .foregroundStyle(Nocturne.dim)
                    .padding(.top, 5)
            }
            .padding(.horizontal, 18)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

// MARK: - Nothing needs you, but your picture changed

private struct ChangedBody: View {
    @Environment(VeraStore.self) private var store
    let selection: HomeSelection
    let notes: [ChangeNote]

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Headline(text: selection.headline, subhead: selection.subhead, size: 30)
                .padding(.top, 40)

            if !notes.isEmpty {
                VStack(alignment: .leading, spacing: 13) {
                    ForEach(notes) { note in
                        ChangeNoteRow(note: note) { store.explain(note) }
                    }
                }
                .padding(.top, 26)
            }

            WithVeraSection(selection: selection)
                .padding(.top, 26)
        }
    }
}

private struct Headline: View {
    let text: String
    let subhead: String
    let size: CGFloat

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(text)
                .font(VeraFont.heading(size))
                .leading(size, 1.25)
                .foregroundStyle(Nocturne.text)
            if !subhead.isEmpty {
                Text(subhead)
                    .font(VeraFont.body(13))
                    .leading(13, 1.6)
                    .foregroundStyle(Nocturne.dim)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

// MARK: - With Vera
//
// Pass 2's grammar, pass 4's selection. Rows lead with belief and demote
// the goal's name to a kicker; the strata mark says this understanding
// has history. What didn't earn a row is named in a digest line rather
// than hidden — Vera is accountable for choosing not to show it.

private struct WithVeraSection: View {
    @Environment(VeraStore.self) private var store
    let selection: HomeSelection

    var body: some View {
        if selection.withVeraCount > 0 {
            VStack(alignment: .leading, spacing: 0) {
                SectionLabel("With Vera")
                    .padding(.bottom, 11)

                VStack(alignment: .leading, spacing: 13) {
                    ForEach(selection.changed) { goal in
                        GoalRow(goal: goal) { store.go(.goal(goal.id)) }
                    }

                    ForEach(Array(selection.digestLines.enumerated()), id: \.offset) { _, line in
                        DigestLine(label: line.label, names: line.names, note: line.note)
                    }
                }
                .padding(.horizontal, 18)

                FooterRow(selection: selection)
                    .padding(.top, 18)
                    .padding(.horizontal, 18)
            }
        }
    }
}

/// A goal, led by what Vera believes about it. The name is a kicker.
private struct GoalRow: View {
    @Environment(Fleet.self) private var fleet
    let goal: Goal
    let onOpen: () -> Void

    private var showsMachine: Bool { fleet.connections.count > 1 }

    var body: some View {
        Button(action: onOpen) {
            VStack(alignment: .leading, spacing: 0) {
                Text(goal.title)
                    .font(VeraFont.body(12))
                    .foregroundStyle(Nocturne.dim)

                Text(goal.digest.isEmpty ? goal.stance : goal.digest)
                    .font(VeraFont.body(13.5))
                    .leading(13.5, 1.5)
                    .foregroundStyle(Nocturne.bright)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 3)

                if !goal.strata.isEmpty {
                    HStack(spacing: 8) {
                        StrataGlance()
                        Text(goal.lastMoved ?? "changed direction")
                            .font(VeraFont.body(11.5))
                            .foregroundStyle(Nocturne.accent300)
                    }
                    .padding(.top, 6)
                } else if let moved = goal.lastMoved {
                    Text(moved)
                        .font(VeraFont.body(11.5))
                        .foregroundStyle(Nocturne.dim)
                        .padding(.top, 5)
                }

                // Which machine, but only when there is more than one
                // to distinguish between. On a single-Mac setup the
                // answer is never interesting.
                if showsMachine, let machine = goal.machineName {
                    Text(machine)
                        .font(VeraFont.body(11))
                        .foregroundStyle(Nocturne.dim)
                        .padding(.top, 4)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

/// The strata mark at glance scale: a stacked edge that says this
/// understanding *has* history. It is not a counter — two bars is the
/// mark, the same way it is on the goal page's compressed strata.
private struct StrataGlance: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Rectangle().fill(Nocturne.neutral800).frame(width: 26, height: 1.5)
            Rectangle().fill(Nocturne.barLow).frame(width: 18, height: 1.5)
        }
    }
}

/// Vera's authored one-liner for work she chose not to show. Not a
/// truncated list — a sentence she stands behind.
private struct DigestLine: View {
    let label: String
    let names: String
    let note: String?

    var body: some View {
        Group {
            Text(label).foregroundStyle(Nocturne.dim)
                + Text(" \(names)").foregroundStyle(Nocturne.soft)
                + Text(note.map { " — \($0)" } ?? "").foregroundStyle(Nocturne.dim)
        }
        .font(VeraFont.body(12))
        .leading(12, 1.55)
        .fixedSize(horizontal: false, vertical: true)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// What settled, and the way out of the selection.
private struct FooterRow: View {
    let selection: HomeSelection

    var body: some View {
        // Stacked rather than opposed: on a phone these two compete for
        // the same line and the settled one always loses, which is
        // exactly the sentence you'd rather not truncate.
        VStack(alignment: .leading, spacing: 6) {
            if let settled = selection.settled.first {
                Text("Yesterday: \(settled.digest)")
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            if let browse = selection.browseLine {
                Text(browse)
                    .frame(maxWidth: .infinity, alignment: .trailing)
            }
        }
        .font(VeraFont.body(12))
        .foregroundStyle(Nocturne.dim)
    }
}

private struct ChangeNoteRow: View {
    let note: ChangeNote
    let onWhy: () -> Void

    /// "why ›" is inline, at the end of the sentence — it is where the
    /// thought ends, not a control sitting beside a paragraph.
    private var line: Text {
        var text = Text("")
        if let lead = note.lead {
            text = Text(lead).foregroundStyle(Nocturne.accent300) + Text(" ")
                + Text(note.text).foregroundStyle(Nocturne.soft)
        } else {
            text = Text(note.text).foregroundStyle(Nocturne.dim)
        }
        if note.hasWhy {
            text = text + Text("  why ›").foregroundStyle(Nocturne.dim)
        }
        return text
    }

    var body: some View {
        Button {
            if note.hasWhy { onWhy() }
        } label: {
            line
                .font(VeraFont.body(13))
                .leading(13, 1.55)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .buttonStyle(.plain)
        .disabled(!note.hasWhy)
    }
}

// MARK: - 5d · A user who mostly talks

private struct AroundYouBody: View {
    @Environment(VeraStore.self) private var store
    let links: [SurfaceLink]

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Nothing needs you.")
                .font(VeraFont.heading(30))
                .leading(30, 1.2)
                .foregroundStyle(Nocturne.text)
                .padding(.top, 52)

            VStack(alignment: .leading, spacing: 12) {
                SectionLabel("Around you")
                    .padding(.bottom, -3)

                ForEach(links) { link in
                    Button {
                        if let d = link.destination { store.go(d) }
                    } label: {
                        Group {
                            if link.isQuiet {
                                Text("\(link.name) · \(link.detail)")
                                    .foregroundStyle(Nocturne.dim)
                            } else {
                                Text(link.name).foregroundStyle(Nocturne.bright)
                                + Text(" — \(link.detail) ").foregroundStyle(Nocturne.soft)
                                + Text("›").foregroundStyle(Nocturne.dim)
                            }
                        }
                        .font(VeraFont.body(13))
                        .leading(13, 1.5)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .buttonStyle(.plain)
                    .disabled(link.destination == nil)
                }
            }
            .padding(.top, 30)
        }
    }
}

// MARK: - 5c · Nothing changed at all

private struct SilenceBody: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Spacer(minLength: 0)
            Text("Nothing needs you.")
                .font(VeraFont.heading(32))
                .leading(32, 1.2)
                .foregroundStyle(Nocturne.text)
            Text("Quiet on my side too.")
                .font(VeraFont.body(14))
                .foregroundStyle(Nocturne.dim)
            Spacer(minLength: 0)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.bottom, 80)
    }
}
