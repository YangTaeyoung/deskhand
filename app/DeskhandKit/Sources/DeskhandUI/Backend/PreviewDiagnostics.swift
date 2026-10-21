import DeskhandClient
import Foundation
import Synchronization
import struct DeskhandClient.Host

/// PIDs each preview computer no longer runs, and the moment the fixture times are relative to.
final class PreviewProcessStore: Sendable {
    /// The moment process start times and boot times are fixed to.
    let anchor = Date.now
    private let ended = Mutex<[String: Set<Int>]>([:])

    func ended(hostID: String) -> Set<Int> { ended.withLock { $0[hostID] ?? [] } }

    func end(_ pids: some Sequence<Int>, hostID: String) {
        ended.withLock { $0[hostID, default: []].formUnion(pids) }
    }
}

// MARK: Processes

extension PreviewBackend {
    /// The listed host when it is connected, or `OFFLINE`.
    private func connectedHost(_ hostID: String) throws -> Host {
        let host = try listedHost(hostID)
        guard host.connection == .ready || host.connection == .degraded else {
            throw RPCErrorInfo(message: "\(host.name)에 연결되어 있지 않습니다", code: "OFFLINE", retryable: true)
        }
        return host
    }

    private func currentProcesses(_ host: Host) -> [RemoteProcess] {
        PreviewFixtures.processes(hostID: host.hostID, user: host.session?.user ?? "user", ended: processes.ended(hostID: host.hostID),
                                 anchor: processes.anchor)
    }

    public func procList(hostID: String) async throws -> [RemoteProcess] {
        let host = try connectedHost(hostID)
        await pause(fraction: 0.25)
        return currentProcesses(host)
    }

    /// Ends the process and with `tree` its descendants, except the agent; a `started` that differs
    /// from the listed start is refused with `STALE_TARGET`, the agent itself, `MsMpEng.exe` and the
    /// critical system processes with `PERMISSION_DENIED`, and `Shop.exe` ends but its reply is lost.
    public func procKill(hostID: String, pid: Int, started: Timestamp?, tree: Bool) async throws -> KillReceipt {
        let host = try listedHost(hostID)
        guard host.connection == .ready || host.connection == .degraded else {
            return KillReceipt(status: .failed, error: OperationError(code: "OFFLINE", message: "\(host.name)에 연결되어 있지 않습니다"))
        }
        await pause(fraction: 0.6)
        let list = currentProcesses(host)
        guard let target = list.first(where: { $0.pid == pid }) else {
            let number = String(pid)
            return KillReceipt(status: .failed, error: OperationError(
                code: "NOT_FOUND", message: "PID \(number) 프로세스가 없습니다. 이미 끝났을 수 있습니다", nextAction: "check_state"))
        }
        if let started, let current = target.started, started != current {
            let number = String(pid)
            return KillReceipt(status: .failed, error: OperationError(
                code: "STALE_TARGET", message: "PID \(number) 프로세스의 시작 시각이 목록과 달라 종료하지 않았습니다", nextAction: "check_state"))
        }
        if target.isSelf {
            return KillReceipt(status: .failed, error: OperationError(
                code: "PERMISSION_DENIED", message: "에이전트는 자기 자신을 종료하지 않습니다"))
        }
        if ProcessGuard.block(for: target) != nil || target.name.caseInsensitiveCompare(PreviewFixtures.deniedKillName) == .orderedSame {
            let name = target.name
            return KillReceipt(status: .failed, error: OperationError(
                code: "PERMISSION_DENIED",
                message: "Windows가 ‘\(name)’의 종료를 거부했습니다. 보호된 프로세스라 관리자 권한으로도 끝낼 수 없습니다"))
        }
        var victims = [target]
        if tree {
            victims += ProcessForest(list).descendants(of: pid).filter { ProcessGuard.block(for: $0) == nil }
        }
        processes.end(victims.map(\.pid), hostID: hostID)
        if target.name.caseInsensitiveCompare(PreviewFixtures.unknownKillName) == .orderedSame {
            return KillReceipt(status: .unknown, error: OperationError(
                code: "EXECUTION_OUTCOME_UNKNOWN", message: "에이전트의 응답을 받기 전에 연결이 잠시 끊겼습니다", nextAction: "check_state"))
        }
        return KillReceipt(status: .succeeded, killed: victims.count)
    }
}

// MARK: Diagnostics

extension PreviewBackend {
    /// `OFFLINE` as the failed envelope of a `diag.*` call reports it.
    private func diagnosticsHost(_ hostID: String) throws -> Host {
        let host = try listedHost(hostID)
        guard host.connection == .ready || host.connection == .degraded else {
            throw OperationError(code: "OFFLINE", message: "\(host.name)에 연결되어 있지 않습니다")
        }
        return host
    }

    public func diagSystem(hostID: String) async throws -> DiagSystem {
        let host = try diagnosticsHost(hostID)
        await pause(fraction: 0.4)
        let busy = currentProcesses(host).filter { $0.pid != 0 }.compactMap(\.cpuPct).reduce(0, +)
        return PreviewFixtures.system(hostID: hostID, cpuUsage: min(100, (busy * 10).rounded() / 10), anchor: processes.anchor)
    }

    public func diagEvents(_ query: EventLogQuery) async throws -> EventLogPage {
        _ = try diagnosticsHost(query.hostID)
        await pause(fraction: 0.5)
        let since = query.since ?? Date.now.addingTimeInterval(-86_400)
        let levels = query.levels.map(Set.init)
        let matching = PreviewFixtures.events(hostID: query.hostID, log: query.log).filter { event in
            let time = event.time.date
            guard time >= since else { return false }
            if let until = query.until, time > until { return false }
            if let levels, let level = event.eventLevel, !levels.contains(level) { return false }
            if let provider = query.provider, event.provider != provider { return false }
            return true
        }
        let limit = min(max(query.max, 1), EventLogQuery.maximumCount)
        return EventLogPage(events: Array(matching.prefix(limit)), truncated: matching.count > limit)
    }

    public func diagCrashes(hostID: String, since: Date?) async throws -> [CrashDump] {
        let host = try diagnosticsHost(hostID)
        await pause(fraction: 0.4)
        let cutoff = since ?? Date.now.addingTimeInterval(-30 * 86_400)
        return PreviewFixtures.crashDumps(hostID: hostID, user: host.session?.user ?? "user").filter { $0.time.date >= cutoff }
    }
}
