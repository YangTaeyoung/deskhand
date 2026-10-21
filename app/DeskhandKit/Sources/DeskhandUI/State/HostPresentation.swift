import DeskhandClient
import Foundation
import SwiftUI
import struct DeskhandClient.Host

extension Host {
    /// `state` as a `ConnectionState`; unknown strings read as offline.
    public var connection: ConnectionState { ConnectionState(rawValue: state) ?? .offline }

    /// `owner` as a `ControlOwner`; unknown strings read as nobody.
    public var controlOwner: ControlOwner { ControlOwner(rawValue: owner) ?? .none }

    /// SF Symbol for the remote operating system.
    public var osSymbol: String { HostOS.symbol(os) }

    /// `Windows · x64`.
    public var platformTitle: String { "\(HostOS.title(os)) · \(HostOS.archTitle(arch))" }

    /// Capabilities in display order, unknown names last.
    public var orderedCapabilities: [(name: String, capability: Capability)] {
        caps.items
            .sorted { lhs, rhs in
                let l = CapabilityCatalog.order.firstIndex(of: lhs.key) ?? Int.max
                let r = CapabilityCatalog.order.firstIndex(of: rhs.key) ?? Int.max
                return l == r ? lhs.key < rhs.key : l < r
            }
            .map { (name: $0.key, capability: $0.value) }
    }

    /// When the daemon last heard from the computer, relative to now (`3시간 전`).
    public var lastSeenText: String? {
        lastSeen.map { DS.Format.relative($0.date) }
    }

    /// True while Windows shows the lock or sign-in screen, so screen and input are unavailable.
    public var isSessionLocked: Bool {
        guard let session else { return false }
        return session.locked || session.inputDesktop == "Winlogon"
    }

    /// Subtitle under the computer name in the toolbar: the signed-in session while connected,
    /// otherwise the sidebar status line.
    public var sessionSubtitle: String {
        guard connection == .ready || connection == .degraded, let session else { return statusLine() }
        return .ui("host.status.session", "\(session.user) · \(HostOS.sessionTitle(session))")
    }

    /// Number of capabilities in the `ready` state.
    public var readyCapabilityCount: Int {
        caps.items.values.filter { $0.state == CapabilityState.ready.rawValue }.count
    }

    /// Second line of a sidebar row: the signed-in session when connected, otherwise the connection state.
    public func statusLine(now: Date = .now) -> String {
        switch connection {
        case .ready:
            guard let session else { return connection.title }
            return .ui("host.status.session", "\(session.user) · \(HostOS.sessionTitle(session))")
        case .offline:
            guard let ago = lastSeenText else { return connection.title }
            return .ui("host.status.lastSeen", "\(connection.title) · \(ago)")
        case .blocked:
            return .ui("host.status.blocked", "인증서 불일치")
        case .connecting, .reconnecting, .degraded:
            return connection.title
        }
    }
}

/// Display names and symbols for host platform strings.
public enum HostOS {
    public static func symbol(_ os: String) -> String {
        switch os.lowercased() {
        case "windows": "pc"
        case "darwin", "macos": "desktopcomputer"
        case "linux": "server.rack"
        default: "display"
        }
    }

    public static func title(_ os: String) -> String {
        switch os.lowercased() {
        case "windows": "Windows"
        case "darwin", "macos": "macOS"
        case "linux": "Linux"
        default: os
        }
    }

    public static func archTitle(_ arch: String) -> String {
        switch arch.lowercased() {
        case "amd64", "x86_64", "x64": "x64"
        case "arm64", "aarch64": "ARM64"
        default: arch
        }
    }

    /// `콘솔` or `원격 데스크톱`, with `잠김` when the session is locked.
    public static func sessionTitle(_ session: SessionInfo) -> String {
        let kind = session.protocolKind == "rdp"
            ? String.ui("session.rdp", "원격 데스크톱")
            : String.ui("session.console", "콘솔")
        return session.locked ? .ui("session.locked", "\(kind) · 잠김") : kind
    }

    /// `input_desktop` as shown in the inspector.
    public static func desktopTitle(_ desktop: String) -> String {
        switch desktop {
        case "Default": .ui("session.desktop.default", "기본 데스크톱")
        case "Winlogon": .ui("session.desktop.winlogon", "로그인 화면")
        default: .ui("session.desktop.unknown", "알 수 없음")
        }
    }
}

/// Names of agent capabilities (`caps` keys) in display order.
public enum CapabilityCatalog {
    public static let order = ["screen", "input", "uia", "exec", "pty", "files", "processes", "diag", "tunnel", "clipboard"]

    public static func title(_ name: String) -> String {
        switch name {
        case "screen": .ui("cap.screen", "화면")
        case "input": .ui("cap.input", "입력")
        case "uia": .ui("cap.uia", "UI 자동화")
        case "exec": .ui("cap.exec", "명령 실행")
        case "pty": .ui("cap.pty", "터미널")
        case "files": .ui("cap.files", "파일")
        case "processes": .ui("cap.processes", "프로세스")
        case "diag": .ui("cap.diag", "진단")
        case "tunnel": .ui("cap.tunnel", "디버그 터널")
        case "clipboard": .ui("cap.clipboard", "클립보드")
        default: name
        }
    }
}

extension Capability {
    public var capabilityState: CapabilityState { CapabilityState(rawValue: state) ?? .unavailable }
}
