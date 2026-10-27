import Foundation
import DeskhandKeychain
import LocalAuthentication
import Security

// This value never enters UserDefaults, telemetry, the daemon, or error messages.
struct RDPSavedLogin: Codable, Equatable, Sendable {
    let username: String
    let password: String
}

enum RDPCredentialError: Error { case unavailable }

@MainActor
protocol RDPCredentialVault {
    func read(key: String) async throws -> RDPSavedLogin?
    func write(_ login: RDPSavedLogin, key: String) async throws
    func remove(key: String) async throws
}

@MainActor
struct RDPKeychainVault: RDPCredentialVault {
    private static let executor = BoundedCredentialExecutor()

    nonisolated private static func query(_ key: String) -> [String: Any] {
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

    func read(key: String) async throws -> RDPSavedLogin? {
        try await Self.executor.perform { try Self.readWithoutUI(key: key) }
    }

    nonisolated private static func readWithoutUI(key: String) throws -> RDPSavedLogin? {
        var request = query(key)
        request[kSecReturnData as String] = true
        request[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: Unmanaged<CFTypeRef>?
        let status = DeskhandKeychainCopyMatching(request as CFDictionary, &result)
        let item = result?.takeRetainedValue()
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = item as? Data,
              let login = try? JSONDecoder().decode(RDPSavedLogin.self, from: data)
        else { throw RDPCredentialError.unavailable }
        return login
    }

    func write(_ login: RDPSavedLogin, key: String) async throws {
        try await Self.executor.perform { try Self.writeWithoutUI(login, key: key) }
    }

    nonisolated private static func writeWithoutUI(_ login: RDPSavedLogin, key: String) throws {
        let data = try JSONEncoder().encode(login)
        let attributes: [String: Any] = [kSecValueData as String: data]
        var status = DeskhandKeychainUpdate(query(key) as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            var request = query(key)
            request[kSecValueData as String] = data
            request[kSecAttrLabel as String] = "Deskhand Windows 로그인"
            status = DeskhandKeychainAdd(request as CFDictionary)
        }
        guard status == errSecSuccess else { throw RDPCredentialError.unavailable }
    }

    func remove(key: String) async throws {
        try await Self.executor.perform { try Self.removeWithoutUI(key: key) }
    }

    nonisolated private static func removeWithoutUI(key: String) throws {
        let status = DeskhandKeychainDelete(query(key) as CFDictionary)
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

    func restore(key: String) async throws -> RDPSavedLogin? { try await vault.read(key: key) }

    func prepare(key: String, username: String, password: String, remember: Bool) {
        pending = remember ? (key, RDPSavedLogin(username: username, password: password)) : nil
    }

    @discardableResult
    func connected() async throws -> Bool {
        guard let value = pending else { return false }
        pending = nil
        try await vault.write(value.login, key: value.key)
        return true
    }

    func failed() { pending = nil }

    func forget(key: String) async throws {
        pending = nil
        try await vault.remove(key: key)
    }
}
