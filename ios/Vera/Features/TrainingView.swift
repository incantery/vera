import SwiftUI

// 5r — Your training.
//
// A Vera-kept surface, not a fitness app. Stance-first like a goal page,
// but with no lifecycle tag: this isn't work, it's kept structure. One
// modest trend mark, an observation Vera noticed unprompted, and the
// composer already inside the context.
//
// Every number on this screen is computed from the logged sessions. Log
// a set in conversation and this page moves — which is the whole claim
// of "consequences become structure", held to.

struct TrainingView: View {
    @Environment(VeraStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    private var bench: Trend { Trend(lift: "bench press", sessions: store.sessions) }
    private var squat: Trend { Trend(lift: "squat", sessions: store.sessions) }

    var body: some View {
        @Bindable var store = store

        Screen {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    HStack {
                        BackChevron { dismiss() }
                        Spacer()
                        Text("Kept by Vera · \(bench.weekSpan) weeks")
                            .font(VeraFont.body(12))
                            .foregroundStyle(Nocturne.dim)
                    }

                    Text("Your training")
                        .font(VeraFont.heading(22))
                        .foregroundStyle(Nocturne.text)
                        .padding(.top, 16)

                    Text(stance)
                        .font(VeraFont.body(14))
                        .leading(14, 1.6)
                        .foregroundStyle(Nocturne.bright)
                        .padding(.top, 12)

                    // The whole span, not a flattering window: six bars
                    // would crop the climb down to its plateau and make
                    // a real trend look like noise.
                    WeeklyBars(values: bench.weekly.suffix(8).map { $0 })
                        .padding(.top, 18)

                    VStack(alignment: .leading, spacing: 11) {
                        SectionLabel("Recent")
                            .padding(.bottom, -2)

                        ForEach(recentSessions) { session in
                            HStack(alignment: .firstTextBaseline, spacing: 5) {
                                Text("\(VeraStore.relativeDay(session.date).capitalizedFirst) —")
                                    .foregroundStyle(Nocturne.dim)
                                Text(describe(session))
                                    .foregroundStyle(Nocturne.bright)
                            }
                            .font(VeraFont.body(12.5))
                            .leading(12.5, 1.5)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }
                    .padding(.top, 22)

                    VStack(alignment: .leading, spacing: 4) {
                        Text("Worth knowing: your best sessions follow rest days, not other lifting days.")
                        Button {
                            store.showingUnderTheHood = true
                        } label: {
                            Text("Under the hood: workouts.db on your Mac ›")
                                .foregroundStyle(Nocturne.dim)
                        }
                        .buttonStyle(.plain)
                    }
                    .font(VeraFont.body(11.5))
                    .leading(11.5, 1.6)
                    .foregroundStyle(Nocturne.dim)
                    .padding(.top, 20)
                    .padding(.bottom, 12)
                }
                .padding(.horizontal, 24)
                .padding(.top, 8)
            }
        } bottom: {
            Composer(
                text: $store.draft,
                placeholder: "How does this compare with last month?",
                isEditable: false,
                onTap: { store.openConversation() }
            )
        }
        .toolbar(.hidden, for: .navigationBar)
    }

    // MARK: - What Vera says about it

    /// Vera's reading of the data, in sentences, derived rather than
    /// stored — so it cannot drift away from what was logged.
    private var stance: String {
        var parts: [String] = []

        if let first = bench.first, let last = bench.last, let change = bench.percentChange {
            let direction = change > 1 ? "trending up" : (change < -1 ? "slipping" : "holding")
            var line = "Pressing is \(direction) — bench est. max \(Int(first.rounded())) → \(Int(last.rounded())) over \(bench.weekSpan) weeks"
            line += hadTravelWeek ? ", and it held through your travel week." : "."
            parts.append(line)
        }

        if squat.sessionCount > 0 && squat.sessionCount < 6 {
            parts.append("Squat is under-sampled; \(squat.sessionCount) sessions isn’t a trend.")
        }

        return parts.isEmpty
            ? "Not enough logged yet for me to say anything I’d stand behind."
            : parts.joined(separator: " ")
    }

    private var hadTravelWeek: Bool {
        store.sessions.contains { $0.note?.contains("travel") == true }
    }

    private var recentSessions: [Session] {
        store.sessions.sorted { $0.date > $1.date }.prefix(3).map { $0 }
    }

    private func describe(_ session: Session) -> String {
        var pieces = session.sets.map { set -> String in
            let name = set.lift == "bench press" ? "bench" : set.lift
            let uniform = set.reps.allSatisfy { $0 == set.reps.first }
            let reps = uniform
                ? "\(set.reps.count)×\(set.reps.first ?? 0)"
                : set.repsDescription
            var piece = "\(name) \(set.weight) · \(reps)"
            if isRepPR(set, in: session) { piece += " (rep PR)" }
            return piece
        }
        if let note = session.note { pieces.append(note) }
        return pieces.joined(separator: " · ")
    }

    /// A PR needs something to have beaten. A weight never attempted
    /// before is not a record, it is a first attempt.
    private func isRepPR(_ set: LiftSet, in session: Session) -> Bool {
        guard let best = set.reps.max() else { return false }
        let prior = store.sessions
            .filter { $0.date < session.date }
            .flatMap(\.sets)
            .filter { $0.lift == set.lift && $0.weight >= set.weight }
            .compactMap { $0.reps.max() }
            .max()
        guard let prior, prior > 0 else { return false }
        return best > prior
    }
}

/// One bar a week. The ramp reads as value, and only the newest bar
/// takes the accent — a trend mark, not a chart.
struct WeeklyBars: View {
    let values: [Double]
    var caption = "bench est. max, weekly"

    private var range: (min: Double, max: Double) {
        let lo = (values.min() ?? 0) * 0.92
        let hi = values.max() ?? 1
        return (lo, max(hi, lo + 1))
    }

    var body: some View {
        HStack(alignment: .bottom, spacing: 5) {
            ForEach(Array(values.enumerated()), id: \.offset) { index, value in
                let fraction = (value - range.min) / (range.max - range.min)
                let isLatest = index == values.count - 1
                RoundedRectangle(cornerRadius: 2)
                    .fill(isLatest ? Nocturne.accent.opacity(0.85) : fill(for: fraction))
                    .frame(width: 18, height: max(22, 22 + fraction * 22))
            }

            Spacer(minLength: 8)

            Text(caption)
                .font(VeraFont.body(10.5))
                .foregroundStyle(Nocturne.dim)
                .padding(.bottom, 2)
        }
        .frame(height: 44, alignment: .bottom)
    }

    private func fill(for fraction: Double) -> Color {
        switch fraction {
        case ..<0.34: Nocturne.barLow
        case ..<0.67: Nocturne.barMid
        default: Nocturne.barHigh
        }
    }
}
