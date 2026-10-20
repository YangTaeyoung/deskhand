import Foundation
import ServiceManagement

/// Whether the app opens when the person logs in.
public enum LoginItemStatus: Equatable, Sendable {
    case enabled
    case disabled
    /// Registered, but turned off in System Settings › General › Login Items.
    case requiresApproval
    /// The system could not find the app as a login item.
    case unavailable
}

/// Opening the app at login. `SystemLoginItem` uses ServiceManagement; previews, tests and
/// snapshots use `MemoryLoginItem`.
@MainActor
public protocol LoginItemControl: AnyObject {
    var status: LoginItemStatus { get }
    func setEnabled(_ enabled: Bool) throws
    /// Shows System Settings › General › Login Items.
    func openSystemSettings()
}

/// The app itself as a login item (`SMAppService.mainApp`).
@MainActor
public final class SystemLoginItem: LoginItemControl {
    public init() {}

    public var status: LoginItemStatus {
        switch SMAppService.mainApp.status {
        case .enabled: .enabled
        case .notRegistered: .disabled
        case .requiresApproval: .requiresApproval
        case .notFound: .unavailable
        @unknown default: .unavailable
        }
    }

    public func setEnabled(_ enabled: Bool) throws {
        if enabled {
            try SMAppService.mainApp.register()
        } else {
            try SMAppService.mainApp.unregister()
        }
    }

    public func openSystemSettings() {
        SMAppService.openSystemSettingsLoginItems()
    }
}

/// A login item kept in memory, which never touches the system.
@MainActor
public final class MemoryLoginItem: LoginItemControl {
    public private(set) var status: LoginItemStatus
    /// Thrown by the next `setEnabled`.
    public var failure: (any Error)?

    public init(status: LoginItemStatus = .disabled) {
        self.status = status
    }

    public func setEnabled(_ enabled: Bool) throws {
        if let failure { throw failure }
        status = enabled ? .enabled : .disabled
    }

    public func openSystemSettings() {}
}
