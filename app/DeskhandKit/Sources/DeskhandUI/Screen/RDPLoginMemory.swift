import Foundation
import LocalAuthentication
import Security

// This value never enters UserDefaults, telemetry, the daemon, or error messages.
struct RDPSavedLogin: Codable, Equatable {
    let username: String
    let password: String
}

enum RDPCredentialError: Error { case unavailable }

@MainActor
protocol RDPCredentialVault {
    func read(key: String) throws -> RDPSavedLogin?
    func write(_ login: RDPSavedLogin, key: String) throws
    func remove(key: String) throws
}

@MainActor
struct RDPKeychainVault: RDPCredentialVault {
    private func query(_ key: String) -> [String: Any] {
        let context = LAContext()
        context.interactionNotAllowed = true
        return [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "io.github.yangtaeyoung.Deskhand.rdp-login",
            kSecAttrAccount as String: key,
            kSecAttrSynchronizable as String: false,
            kSecUseAuthenticationContext as String: context
        ]
    }

    func read(key: String) throws -> RDPSavedLogin? {
        var request = query(key)
        request[kSecReturnData as String] = true
        request[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(request as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data,
              let login = try? JSONDecoder().decode(RDPSavedLogin.self, from: data)
        else { throw RDPCredentialError.unavailable }
        return login
    }

    func write(_ login: RDPSavedLogin, key: String) throws {
        let data = try JSONEncoder().encode(login)
        let attributes: [String: Any] = [kSecValueData as String: data]
        var status = SecItemUpdate(query(key) as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            var request = query(key)
            request[kSecValueData as String] = data
            request[kSecAttrLabel as String] = "Deskhand Windows 로그인"
            status = SecItemAdd(request as CFDictionary, nil)
        }
        guard status == errSecSuccess else { throw RDPCredentialError.unavailable }
    }

    func remove(key: String) throws {
        let status = SecItemDelete(query(key) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw RDPCredentialError.unavailable
        }
    }
}

/// A failed login never overwrites known working credentials. Pending secrets
/// live only until success/failure/disconnect, not for the lifetime of the view.
@MainActor
final class RDPLoginMemory {
    private let vault: any RDPCredentialVault
    private var pending: (key: String, login: RDPSavedLogin)?

    init(vault: any RDPCredentialVault = RDPKeychainVault()) { self.vault = vault }

    static func key(hostID: String, certificate: String) -> String {
        // Length-prefixing avoids collisions even if host IDs contain separators.
        "\(hostID.utf8.count):\(hostID):\(certificate.lowercased())"
    }

    func restore(key: String) throws -> RDPSavedLogin? { try vault.read(key: key) }

    func prepare(key: String, username: String, password: String, remember: Bool) {
        pending = remember ? (key, RDPSavedLogin(username: username, password: password)) : nil
    }

    @discardableResult
    func connected() throws -> Bool {
        guard let value = pending else { return false }
        pending = nil
        try vault.write(value.login, key: value.key)
        return true
    }

    func failed() { pending = nil }

    func forget(key: String) throws {
        pending = nil
        try vault.remove(key: key)
    }
}
