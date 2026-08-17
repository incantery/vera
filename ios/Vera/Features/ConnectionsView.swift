import SwiftUI

// The machines Vera is running on.
//
// Reached by tapping the locality pill, which is where the design has
// always named the machine. Adding one is a single paste: on a
// non-loopback bind vera prints the whole URL with the key in it, so
// the line it already gives you *is* the pairing flow.

struct ConnectionsView: View {
    @Environment(Fleet.self) private var fleet
    @Environment(\.dismiss) private var dismiss

    @State private var pasted = ""
    @State private var problem: String?
    @FocusState private var typing: Bool

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(fleet.connections) { connection in
                        MachineRow(connection: connection)
                        if connection.id != fleet.connections.last?.id {
                            Rectangle()
                                .fill(Nocturne.rule)
                                .frame(height: 1)
                                .padding(.vertical, 14)
                        }
                    }

                    if fleet.connections.isEmpty {
                        Text("No machines yet.")
                            .font(VeraFont.heading(20))
                            .foregroundStyle(Nocturne.text)
                        Text("Vera is running on a Mac, not here. Start it there and paste the line it prints.")
                            .font(VeraFont.body(13))
                            .leading(13, 1.6)
                            .foregroundStyle(Nocturne.dim)
                            .fixedSize(horizontal: false, vertical: true)
                            .padding(.top, 6)
                    }

                    AddMachine(pasted: $pasted, problem: $problem, typing: $typing) { add() }
                        .padding(.top, fleet.connections.isEmpty ? 26 : 28)
                }
                .padding(.horizontal, 24)
                .padding(.top, 8)
                .padding(.bottom, 24)
            }
            .background(Nocturne.bg.ignoresSafeArea())
            .navigationTitle("Machines")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
        }
        .presentationBackground(Nocturne.bg)
    }

    private func add() {
        guard let (connection, key) = Connection.parse(pasted) else {
            problem = "That doesn’t look like an address. Paste the whole line vera printed, or just the machine’s name."
            return
        }
        if key == nil && !connection.isLoopback {
            problem = "That address has no key in it. Beyond loopback vera mints one and prints it as part of the URL — paste the whole line."
            return
        }
        fleet.add(connection, key: key)
        pasted = ""
        problem = nil
        typing = false
    }
}

// MARK: - One machine

private struct MachineRow: View {
    @Environment(Fleet.self) private var fleet
    let connection: Connection

    @State private var editingName = false
    @State private var draftName = ""

    private var reach: Reach { fleet.reach[connection.id] ?? .idle }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 9) {
                ReachDot(reach: reach)

                if editingName {
                    TextField("Name", text: $draftName)
                        .font(VeraFont.heading(16))
                        .foregroundStyle(Nocturne.text)
                        .tint(Nocturne.accent)
                        .submitLabel(.done)
                        .onSubmit { commitName() }
                } else {
                    Text(connection.shortName)
                        .font(VeraFont.heading(16))
                        .foregroundStyle(Nocturne.text)
                }

                Spacer(minLength: 8)

                Button(editingName ? "Save" : "Rename") {
                    if editingName { commitName() } else {
                        draftName = connection.name
                        editingName = true
                    }
                }
                .font(VeraFont.body(12))
                .foregroundStyle(Nocturne.accent300)
                .buttonStyle(.plain)
            }

            // String(), not interpolation: a port is an identifier, and
            // interpolating an Int hands it to the locale formatter,
            // which renders 4779 as "4,779".
            Text(connection.host + ":" + String(connection.port))
                .font(VeraFont.mono(11, .regular))
                .foregroundStyle(Nocturne.dim)
                .padding(.top, 5)

            // What it is doing, or why it isn't.
            Text(statusLine)
                .font(VeraFont.body(12))
                .leading(12, 1.5)
                .foregroundStyle(reach.isLive ? Nocturne.soft : Nocturne.dim)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 5)

            HStack(spacing: 18) {
                Button("Reconnect") { fleet.connect(connection) }
                    .foregroundStyle(Nocturne.accent300)
                Button("Forget") { fleet.remove(connection) }
                    .foregroundStyle(Nocturne.dim)
            }
            .font(VeraFont.body(12))
            .buttonStyle(.plain)
            .padding(.top, 9)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var statusLine: String {
        let count = fleet.goalsByMachine[connection.id]?.count ?? 0
        switch reach {
        case .live:
            return count == 0
                ? "Connected. Nothing on its board."
                : "Connected · \(HomeSelection.spelled(count)) goal\(count == 1 ? "" : "s")"
        case .connecting: return "Reaching…"
        case .idle: return "Not connected."
        case .away(let why): return why.capitalizedFirst + "."
        }
    }

    private func commitName() {
        let name = draftName.trimmingCharacters(in: .whitespacesAndNewlines)
        if !name.isEmpty { fleet.rename(connection, to: name) }
        editingName = false
    }
}

private struct ReachDot: View {
    let reach: Reach
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var breathing = false

    var body: some View {
        Group {
            switch reach {
            case .live:
                Circle()
                    .fill(Nocturne.accent)
                    .frame(width: 7, height: 7)
                    .shadow(color: Nocturne.accent, radius: 4)
            case .connecting:
                Circle()
                    .fill(Nocturne.accent)
                    .frame(width: 7, height: 7)
                    .opacity(breathing ? 1 : 0.35)
                    .onAppear {
                        guard !reduceMotion else { return }
                        withAnimation(.easeInOut(duration: 0.9).repeatForever()) { breathing = true }
                    }
            case .idle, .away:
                Circle()
                    .strokeBorder(Nocturne.dim, lineWidth: 1)
                    .frame(width: 7, height: 7)
            }
        }
        .frame(width: 8)
    }
}

// MARK: - Adding one

private struct AddMachine: View {
    @Binding var pasted: String
    @Binding var problem: String?
    @FocusState.Binding var typing: Bool
    let onAdd: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            SectionLabel("Add a machine")

            Text("Run vera with `--addr :4770` and paste the line it prints — the key is already in it. On this Mac, `localhost` is enough.")
                .font(VeraFont.body(12))
                .leading(12, 1.6)
                .foregroundStyle(Nocturne.dim)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 8)

            TextField(
                "",
                text: $pasted,
                prompt: Text("http://192.168.1.20:4770/?key=…").foregroundStyle(Nocturne.dim),
                axis: .vertical
            )
                .font(VeraFont.mono(12, .regular))
                .foregroundStyle(Nocturne.text)
                .tint(Nocturne.accent)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .keyboardType(.URL)
                .lineLimit(1...3)
                .focused($typing)
                .padding(.horizontal, 14)
                .padding(.vertical, 12)
                .background(Nocturne.surface, in: RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous))
                .overlay {
                    RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                        .strokeBorder(problem == nil ? Nocturne.neutral800 : Nocturne.accent, lineWidth: 1)
                }
                .padding(.top, 12)
                .onChange(of: pasted) { problem = nil }

            if let problem {
                Text(problem)
                    .font(VeraFont.body(11.5))
                    .leading(11.5, 1.5)
                    .foregroundStyle(Nocturne.accent300)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 8)
            }

            Button(action: onAdd) {
                Text("Connect")
                    .font(VeraFont.body(13, .medium))
                    .foregroundStyle(pasted.isEmpty ? Nocturne.dim : Nocturne.accent300)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 9)
                    .overlay {
                        RoundedRectangle(cornerRadius: Nocturne.radiusMd, style: .continuous)
                            .strokeBorder(pasted.isEmpty ? Nocturne.neutral800 : Nocturne.accent, lineWidth: 1)
                    }
            }
            .buttonStyle(.plain)
            .disabled(pasted.isEmpty)
            .padding(.top, 12)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
