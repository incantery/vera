import Foundation
import Security

// What the QR code said, and where the machine it names can be found
// right now.
//
// Those are two different lifetimes, which is the whole point: the
// Pairing is permanent and lives in the keychain; the address is a
// guess that expires the moment you walk out of the building.

struct Pairing: Codable, Sendable, Equatable {
    var v: Int
    var peer: String
    var secret: String
    var name: String
    var hints: [String]?

    /// The QR payload is JSON. A code we cannot read is not an error
    /// worth a message — it is almost always a different QR code
    /// entirely, pointed at by accident.
    static func decode(_ scanned: String) -> Pairing? {
        guard let data = scanned.data(using: .utf8),
              let pairing = try? JSONDecoder().decode(Pairing.self, from: data),
              pairing.v == 1, !pairing.peer.isEmpty, !pairing.secret.isEmpty
        else { return nil }
        return pairing
    }
}

// MARK: - Where it lives

/// The secret goes in the keychain rather than UserDefaults, which is
/// a plist any backup can read.
enum PairingStore {
    private static let service = "com.incantery.vera2.pairing"
    private static let account = "current"

    static func save(_ pairing: Pairing) {
        guard let data = try? JSONEncoder().encode(pairing) else { return }
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(query as CFDictionary)
        var add = query
        add[kSecValueData as String] = data
        // ThisDeviceOnly: a pairing restored onto a different phone is
        // a machine trusting a device its owner never showed it.
        add[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        SecItemAdd(add as CFDictionary, nil)
    }

    static func load() -> Pairing? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data
        else { return nil }
        return try? JSONDecoder().decode(Pairing.self, from: data)
    }

    static func clear() {
        SecItemDelete([
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ] as CFDictionary)
        UserDefaults.standard.removeObject(forKey: lastGoodKey)
    }

    // The address that worked last time. Not secret, and genuinely
    // disposable — it is a cache, and being wrong costs one timeout.
    private static let lastGoodKey = "vera2.lastGoodAddress"
    static var lastGood: String? {
        get { UserDefaults.standard.string(forKey: lastGoodKey) }
        set { UserDefaults.standard.set(newValue, forKey: lastGoodKey) }
    }
}
