import SwiftUI

// 5v — Under the hood of "Your training".
//
// For the power user who peels. Upstairs never says "SQLite"; it says
// "your training". This is the one place the machinery is named, and it
// is monospaced on purpose: it looks like what it is.

struct UnderTheHoodView: View {
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                SectionLabel("Your training · machinery")
                Spacer()
                Button { dismiss() } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(Nocturne.dim)
                }
                .buttonStyle(.plain)
            }
            .padding(.bottom, 12)

            VStack(alignment: .leading, spacing: 10) {
                ForEach(Seed.machinery, id: \.0) { path, detail in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(path)
                            .font(VeraFont.mono(11, .regular))
                            .foregroundStyle(Nocturne.soft)
                        Text(detail)
                            .font(VeraFont.mono(11, .regular))
                            .foregroundStyle(Nocturne.faint)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
            }

            Text(Seed.machineryNote)
                .font(VeraFont.body(11.5))
                .leading(11.5, 1.6)
                .foregroundStyle(Nocturne.dim)
                .padding(.top, 16)

            Spacer(minLength: 0)
        }
        .padding(.horizontal, 26)
        .padding(.top, 26)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Nocturne.bg.ignoresSafeArea())
        .presentationDetents([.medium])
        .presentationBackground(Nocturne.bg)
    }
}

// MARK: - Walkthrough
//
// A scaffold, not product surface: the design doc's own states, so the
// running app can be read against it on device. Each entry sets real
// state rather than faking a screen — what you see is what the app
// produces from its own data.

struct WalkthroughSheet: View {
    @Environment(VeraStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(VeraStore.WalkthroughEntry.allCases) { entry in
                        Button {
                            store.enter(entry)
                        } label: {
                            HStack(spacing: 10) {
                                Text(entry.rawValue)
                                    .font(VeraFont.mono(11))
                                    .foregroundStyle(Nocturne.accent300)
                                Text(entry.title)
                                    .font(VeraFont.body(14))
                                    .foregroundStyle(Nocturne.text)
                            }
                        }
                        .listRowBackground(Nocturne.surface)
                    }
                } footer: {
                    Text("Development scaffold — not part of the product surface. Each entry drives the real store.")
                        .font(VeraFont.body(11.5))
                        .foregroundStyle(Nocturne.dim)
                }
            }
            .scrollContentBackground(.hidden)
            .background(Nocturne.bg.ignoresSafeArea())
            .navigationTitle("Vera Mobile v5")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Close") { dismiss() }
                }
            }
        }
        .presentationBackground(Nocturne.bg)
    }
}
