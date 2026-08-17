import Foundation
import Security

// A machine running vera.
//
// The phone holds several. That is not a power feature bolted on: the
// design has always named the machine — pass 2's header reads "Local ·
// Nik's MacBook Pro", and the whole of 4i is a work Mac going offline
// and Vera reporting it as *work* ("one pursuit paused, nothing else
// cares") rather than as an error. Machines are part of the world Vera
// talks about, so they get a first-class place here.

struct Connection: Identifiable, Codable, Hashable {
    var id: UUID = UUID()
    /// What Vera calls this machine in a sentence. "your work Mac".
    var name: String
    var host: String
    var port: Int = 4770

    var baseURL: URL? {
        var c = URLComponents()
        c.scheme = "http"
        c.host = host
        c.port = port
        return c.url
    }

    /// Loopback needs no key — vera says so at startup, and refuses to
    /// serve the LAN without one.
    var isLoopback: Bool {
        ["127.0.0.1", "localhost", "::1"].contains(host.lowercased())
    }

    /// A machine's own short name, for the digest lines that mention it.
    var shortName: String {
        name.isEmpty ? host : name
    }

    // MARK: - Parsing what vera prints
    //
    // On a non-loopback bind vera prints the whole thing:
    //   vera:   http://192.168.1.20:4770/?key=8f2a…
    // so pasting that one line is the entire pairing flow. A bare host
    // or host:port works too, for the loopback case that needs no key.

    static func parse(_ raw: String) -> (connection: Connection, key: String?)? {
        var text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return nil }

        // Tolerate the "vera:   " prefix from a copied terminal line.
        if let range = text.range(of: "http://") { text = String(text[range.lowerBound...]) }
        if !text.contains("://") { text = "http://" + text }

        guard let components = URLComponents(string: text),
              let host = components.host, !host.isEmpty
        else { return nil }

        let key = components.queryItems?.first { $0.name == "key" }?.value
        let connection = Connection(
            name: Self.suggestedName(for: host),
            host: host,
            port: components.port ?? 4770
        )
        return (connection, key?.isEmpty == false ? key : nil)
    }

    /// A first guess at what to call it. The user renames it; the point
    /// of the name is that Vera can say it in a sentence.
    static func suggestedName(for host: String) -> String {
        if ["127.0.0.1", "localhost", "::1"].contains(host.lowercased()) { return "This Mac" }
        // Bonjour hostnames carry a real name: "nik-macbook-pro.local".
        if host.hasSuffix(".local") {
            return String(host.dropLast(6))
                .replacingOccurrences(of: "-", with: " ")
                .split(separator: " ")
                .map { String($0).capitalizedFirst }
                .joined(separator: " ")
        }
        return host
    }
}

// MARK: - Keys
//
// The key is a credential, so it lives in the Keychain rather than
// alongside the host in UserDefaults. Losing it means re-pasting one
// line, which is cheap; leaking it means handing over the machine.

enum KeyStore {
    private static let service = "com.incantery.vera.connection-key"

    static func save(_ key: String?, for id: UUID) {
        guard let key, !key.isEmpty else { delete(id); return }
        delete(id)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: id.uuidString,
            kSecValueData as String: Data(key.utf8),
            // The phone is unlocked when you are looking at it, and a
            // background reconnect can wait for that.
            kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        ]
        SecItemAdd(query as CFDictionary, nil)
    }

    static func key(for id: UUID) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: id.uuidString,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]
        var out: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data
        else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func delete(_ id: UUID) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: id.uuidString
        ]
        SecItemDelete(query as CFDictionary)
    }
}

// MARK: - Reachability, said the way Vera would say it

enum Reach: Hashable {
    case idle
    case connecting
    case live
    /// Not an error banner. A fact about the world, phrased as one.
    case away(String)

    var isLive: Bool { if case .live = self { true } else { false } }

    var line: String? {
        switch self {
        case .idle: nil
        case .connecting: "reaching…"
        case .live: nil
        case .away(let why): why
        }
    }
}
