import DeskhandClient
import Foundation
import Observation
import SwiftUI
import struct DeskhandClient.Host

/// State of the diagnostics sheet for one computer (`host.doctor`).
@MainActor
@Observable
public final class DoctorModel: Identifiable {
    public enum Status: Equatable {
        case loading
        case loaded([DoctorCheck])
        case failed(OperationError)
    }

    public let id = UUID()
    public let hostID: String
    public let hostName: String
    public private(set) var status: Status
    public private(set) var finishedAt: Date?

    @ObservationIgnored private let backend: any Backend
    @ObservationIgnored private var task: Task<Void, Never>?

    public init(backend: any Backend, host: Host, status: Status = .loading, finishedAt: Date? = nil) {
        self.backend = backend
        self.hostID = host.hostID
        self.hostName = host.name
        self.status = status
        self.finishedAt = finishedAt
    }

    public var isRunning: Bool { status == .loading }

    public func run() {
        task?.cancel()
        status = .loading
        let backend = self.backend
        let hostID = self.hostID
        task = Task { [weak self] in
            do {
                let checks = try await backend.hostDoctor(hostID: hostID)
                self?.status = .loaded(checks)
                self?.finishedAt = .now
            } catch is CancellationError {
            } catch {
                self?.status = .failed(OperationError(error))
                self?.finishedAt = .now
            }
        }
    }

    public func cancel() {
        task?.cancel()
        task = nil
    }

    /// Failures, then warnings, then passed and skipped checks, keeping the daemon's order within each group.
    nonisolated static func problemsFirst(_ checks: [DoctorCheck]) -> [DoctorCheck] {
        let rank: [DoctorStatus: Int] = [.fail: 0, .warn: 1, .pass: 2, .skip: 2]
        return checks.enumerated()
            .sorted { (rank[$0.element.doctorStatus] ?? 2, $0.offset) < (rank[$1.element.doctorStatus] ?? 2, $1.offset) }
            .map(\.element)
    }

    /// True when the first word of `fix` has no Hangul, as in a shell or PowerShell command.
    nonisolated static func looksLikeCommand(_ fix: String) -> Bool {
        guard let first = fix.split(separator: " ").first else { return false }
        return !first.unicodeScalars.contains { (0xAC00...0xD7A3).contains($0.value) || (0x3131...0x318E).contains($0.value) }
    }
}

/// `DoctorCheck.status` values.
public enum DoctorStatus: String, CaseIterable, Sendable {
    case pass
    case fail
    case warn
    case skip

    public var title: String {
        switch self {
        case .pass: .ui("doctor.pass", "통과")
        case .fail: .ui("doctor.fail", "실패")
        case .warn: .ui("doctor.warn", "경고")
        case .skip: .ui("doctor.skip", "건너뜀")
        }
    }

    public var symbol: String {
        switch self {
        case .pass: "checkmark.circle.fill"
        case .fail: "xmark.octagon.fill"
        case .warn: "exclamationmark.triangle.fill"
        case .skip: "minus.circle.fill"
        }
    }

    public var tint: Color {
        switch self {
        case .pass: DS.Color.success
        case .fail: DS.Color.danger
        case .warn: DS.Color.warning
        case .skip: DS.Color.neutral
        }
    }
}

extension DoctorCheck {
    public var doctorStatus: DoctorStatus { DoctorStatus(rawValue: status) ?? .skip }

    /// Korean name of the check, falling back to its ID.
    public var title: String {
        switch id {
        case "ssh_reachable": .ui("doctor.check.sshReachable", "SSH 접속")
        case "agent_version": .ui("doctor.check.agentVersion", "에이전트 버전")
        case "quic_connect": .ui("doctor.check.quicConnect", "보안 연결 (QUIC)")
        case "fingerprint": .ui("doctor.check.fingerprint", "인증서 지문")
        case "session": .ui("doctor.check.session", "사용자 세션")
        case "firewall_rule": .ui("doctor.check.firewallRule", "방화벽 규칙")
        default: id
        }
    }
}
