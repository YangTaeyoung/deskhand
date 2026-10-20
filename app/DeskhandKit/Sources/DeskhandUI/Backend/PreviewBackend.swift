import DeskhandClient
import Foundation
import Synchronization
import struct DeskhandClient.Host

/// Starting data of a `PreviewBackend`.
public enum PreviewScenario: String, CaseIterable, Sendable {
    /// Five computers in different connection and control states.
    case standard
    /// No computers yet.
    case empty
    /// The daemon cannot be started.
    case daemonUnavailable = "daemon-unavailable"
}

/// In-memory `Backend` with fixture data. Mutating calls change its state and emit the events
/// the daemon would send; `hostAdd` plays the installation steps with `stepDelay` between them.
public final class PreviewBackend: Backend {
    private struct Subscriber {
        let kinds: [String]?
        let hostID: String?
        let continuation: AsyncThrowingStream<DaemonEvent, Error>.Continuation

        func matches(_ event: DaemonEvent) -> Bool {
            if let kinds, !kinds.contains(event.kind) { return false }
            if let hostID, event.hostID != hostID { return false }
            return true
        }
    }

    private struct State {
        var hosts: [Host]
        var controls: [String: ControlState]
        /// Per host, newest first.
        var timelines: [String: [TimelineEntry]]
        var subscribers: [UUID: Subscriber] = [:]
        var seq = 0
        var jobs = 0
    }

    public let scenario: PreviewScenario
    private let stepDelay: Duration
    private let state: Mutex<State>

    public init(scenario: PreviewScenario = .standard, stepDelay: Duration = .milliseconds(700)) {
        self.scenario = scenario
        self.stepDelay = stepDelay
        let hosts = scenario == .standard ? PreviewFixtures.hosts() : []
        let timelines = Dictionary(uniqueKeysWithValues: hosts.map { ($0.hostID, PreviewFixtures.timeline(hostID: $0.hostID)) })
        state = Mutex(State(hosts: hosts, controls: PreviewFixtures.controls(for: hosts), timelines: timelines))
    }

    /// Ends every open `events` stream, finishing it with `error` when given, as a dropped
    /// daemon subscription would.
    public func endEventStreams(throwing error: (any Error)? = nil) {
        let continuations = state.withLock { s -> [AsyncThrowingStream<DaemonEvent, Error>.Continuation] in
            let all = s.subscribers.values.map(\.continuation)
            s.subscribers = [:]
            return all
        }
        for continuation in continuations { continuation.finish(throwing: error) }
    }

    /// The value as the daemon spells it in JSON (snake_case keys).
    static func payload(_ value: some Encodable) -> JSONValue {
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        guard let data = try? encoder.encode(value),
              let json = try? JSONDecoder().decode(JSONValue.self, from: data) else { return .null }
        return json
    }

    /// Delivers an event to every matching `events` stream, as the daemon would.
    public func emit(kind: String, hostID: String?, payload: [String: JSONValue] = [:]) {
        let (event, targets) = state.withLock { s -> (DaemonEvent, [AsyncThrowingStream<DaemonEvent, Error>.Continuation]) in
            s.seq += 1
            var params = payload
            params["kind"] = .string(kind)
            params["seq"] = .int(s.seq)
            if let hostID { params["host_id"] = .string(hostID) }
            let event = DaemonEvent(seq: s.seq, ts: Timestamp(date: .now), hostID: hostID, kind: kind, payload: params)
            return (event, s.subscribers.values.filter { $0.matches(event) }.map(\.continuation))
        }
        for continuation in targets { continuation.yield(event) }
    }

    private func pause(_ multiple: Int = 1) async {
        guard stepDelay > .zero else { return }
        try? await Task.sleep(for: stepDelay * multiple)
    }

    private func host(_ hostID: String) throws -> Host {
        guard let host = state.withLock({ $0.hosts.first { $0.hostID == hostID } }) else {
            throw RPCErrorInfo(message: "컴퓨터를 찾을 수 없습니다", code: "NOT_FOUND")
        }
        return host
    }

    private func setState(_ hostID: String, _ connection: ConnectionState) {
        let generation = state.withLock { s -> Int? in
            guard let i = s.hosts.firstIndex(where: { $0.hostID == hostID }) else { return nil }
            s.hosts[i].state = connection.rawValue
            if connection == .ready { s.hosts[i].connectionGeneration += 1 }
            return s.hosts[i].connectionGeneration
        }
        guard let generation else { return }
        emit(kind: EventKind.connectionState, hostID: hostID,
             payload: ["state": .string(connection.rawValue), "connection_generation": .int(generation)])
    }
}

// MARK: Lifecycle

extension PreviewBackend {
    public func start() async throws {
        await pause()
        if scenario == .daemonUnavailable {
            throw ClientError.daemonUnavailable(logPath: Paths.daemonLog)
        }
    }
}

// MARK: Hosts

extension PreviewBackend {
    public func hostList() async throws -> [Host] {
        state.withLock { $0.hosts }
    }

    public func hostAdd(name: String, ssh: String) async throws -> JobReceipt {
        let job = try state.withLock { s -> Int in
            if s.hosts.contains(where: { $0.name == name }) {
                throw RPCErrorInfo(message: "‘\(name)’ 이름의 컴퓨터가 이미 있습니다. 다른 이름을 쓰세요", code: "INVALID_ARGUMENT")
            }
            s.jobs += 1
            return s.jobs
        }
        Task { await self.playInstall(name: name, alias: ssh, job: job) }
        return JobReceipt(jobID: "j-preview-\(job)", status: .running)
    }

    private func playInstall(name: String, alias: String, job: Int) async {
        for step in InstallStep.allCases {
            emit(kind: EventKind.installProgress, hostID: nil, payload: InstallProgress(
                name: name, step: step, status: .active,
                detail: PreviewFixtures.installDetail(step, alias: alias, done: false)).payload)
            await pause(step == .copy ? 2 : 1)
            if alias == PreviewFixtures.failingAlias, step == .install {
                emit(kind: EventKind.installProgress, hostID: nil, payload: InstallProgress(
                    name: name, step: step, status: .failed, error: PreviewFixtures.installFailure(alias: alias)).payload)
                return
            }
            var hostID: String?
            if step == .verify {
                let host = PreviewFixtures.newHost(id: "host-preview-\(job)", name: name, alias: alias)
                state.withLock {
                    $0.hosts.append(host)
                    $0.controls[host.hostID] = ControlState(owner: "none", gen: 1, aiPaused: false)
                }
                hostID = host.hostID
            }
            emit(kind: EventKind.installProgress, hostID: hostID, payload: InstallProgress(
                hostID: hostID, name: name, step: step, status: .done,
                detail: PreviewFixtures.installDetail(step, alias: alias, done: true)).payload)
            if let hostID, let host = state.withLock({ $0.hosts.first { $0.hostID == hostID } }) {
                emit(kind: EventKind.hostAdded, hostID: hostID, payload: ["host": Self.payload(host)])
            }
        }
    }

    public func hostRemove(hostID: String, uninstall: Bool) async throws {
        _ = try host(hostID)
        await pause()
        state.withLock {
            $0.hosts.removeAll { $0.hostID == hostID }
            $0.controls[hostID] = nil
        }
        emit(kind: EventKind.hostRemoved, hostID: hostID)
    }

    public func hostDoctor(hostID: String) async throws -> [DoctorCheck] {
        _ = try host(hostID)
        await pause(2)
        return PreviewFixtures.doctor(hostID: hostID)
    }

    public func hostConnect(hostID: String) async throws {
        _ = try host(hostID)
        setState(hostID, .connecting)
        Task { [self] in
            await pause(2)
            setState(hostID, .ready)
        }
    }

    public func hostDisconnect(hostID: String) async throws {
        _ = try host(hostID)
        setState(hostID, .offline)
    }

    public func hostRepair(hostID: String) async throws -> JobReceipt {
        _ = try host(hostID)
        let job = state.withLock { s -> Int in
            s.jobs += 1
            return s.jobs
        }
        setState(hostID, .connecting)
        Task { [self] in
            await pause(3)
            setState(hostID, .ready)
        }
        return JobReceipt(jobID: "j-preview-\(job)", hostID: hostID, status: .running)
    }

    public func sessionInspect(hostID: String) async throws -> SessionInspect {
        let host = try host(hostID)
        guard let session = host.session, host.state == ConnectionState.ready.rawValue else {
            throw RPCErrorInfo(message: "\(host.name)에 연결되어 있지 않습니다", code: "OFFLINE", retryable: true)
        }
        let control = state.withLock { $0.controls[hostID] }
        return SessionInspect(session: session, displays: host.displays, caps: host.caps, control: control,
                              connectionGeneration: host.connectionGeneration)
    }

    public func sshAliases() async -> [String] {
        PreviewFixtures.sshAliases
    }
}

// MARK: Control

extension PreviewBackend {
    public func controlGet(hostID: String) async throws -> ControlState {
        _ = try host(hostID)
        return state.withLock { $0.controls[hostID] } ?? ControlState(owner: "none", gen: 0, aiPaused: false)
    }

    public func controlAcquire(hostID: String, as owner: ControlOwner, force: Bool) async throws -> ControlState {
        await pause()
        let current = try await controlGet(hostID: hostID)
        if owner == .ai, current.owner == ControlOwner.human.rawValue {
            throw RPCErrorInfo(message: "사람이 조작 중입니다. 사람이 제어권을 넘길 때까지 기다리세요",
                               code: "CONTROL_HELD_BY_HUMAN", nextAction: "human_release")
        }
        if owner == .ai, current.aiPaused {
            throw RPCErrorInfo(message: "AI가 일시정지되어 있습니다. 사람이 AI에게 넘기기를 해야 합니다",
                               code: "CONTROL_HELD_BY_HUMAN", nextAction: "human_resume_ai")
        }
        let client = owner == .human ? nil : "Claude Code"
        let summary = owner == .human ? "제어권을 가져왔습니다" : "제어권을 얻었습니다"
        return update(hostID, ControlState(owner: owner.rawValue, ownerClient: client, gen: current.gen + 1, aiPaused: current.aiPaused),
                      actor: owner == .human ? "human" : "ai", client: client ?? "Deskhand", summary: summary)
    }

    public func controlRelease(hostID: String) async throws {
        let current = try await controlGet(hostID: hostID)
        _ = update(hostID, ControlState(owner: "none", gen: current.gen + 1, aiPaused: current.aiPaused),
                   actor: "human", client: "Deskhand", summary: "제어권을 놓았습니다")
    }

    public func controlStop(hostID: String) async throws -> ControlState {
        let current = try await controlGet(hostID: hostID)
        return update(hostID, ControlState(owner: "human", gen: current.gen + 1, aiPaused: true),
                      actor: "human", client: "Deskhand", summary: "긴급 정지 · AI 입력을 멈췄습니다")
    }

    public func controlResumeAI(hostID: String) async throws -> ControlState {
        await pause()
        let current = try await controlGet(hostID: hostID)
        return update(hostID, ControlState(owner: "none", gen: current.gen + 1, aiPaused: false),
                      actor: "human", client: "Deskhand", summary: "AI에게 제어권을 넘겼습니다")
    }

    /// Stores `control`, records the change in the host's timeline, and emits `control.changed`
    /// and `timeline.appended`.
    private func update(_ hostID: String, _ control: ControlState, actor: String, client: String, summary: String) -> ControlState {
        let entry = state.withLock { s -> TimelineEntry in
            s.controls[hostID] = control
            if let i = s.hosts.firstIndex(where: { $0.hostID == hostID }) {
                s.hosts[i].owner = control.owner
                s.hosts[i].ownerClient = control.ownerClient
            }
            let seq = (s.timelines[hostID]?.first?.seq ?? 0) + 1
            let entry = TimelineEntry(seq: seq, ts: Timestamp(date: .now), hostID: hostID, actor: actor, clientName: client,
                                      kind: "control", summary: summary, outcome: "ok")
            s.timelines[hostID, default: []].insert(entry, at: 0)
            return entry
        }
        var payload: [String: JSONValue] = [
            "owner": .string(control.owner), "gen": .int(control.gen), "ai_paused": .bool(control.aiPaused),
        ]
        if let client = control.ownerClient { payload["owner_client"] = .string(client) }
        emit(kind: EventKind.controlChanged, hostID: hostID, payload: payload)
        emit(kind: EventKind.timelineAppended, hostID: hostID, payload: ["entry": Self.payload(entry)])
        return control
    }
}

// MARK: Timeline

extension PreviewBackend {
    public func timelineList(hostID: String, since: Int?, limit: Int) async throws -> [TimelineEntry] {
        _ = try host(hostID)
        let entries = state.withLock { $0.timelines[hostID] ?? [] }.filter { since == nil || $0.seq > since! }
        return Array(entries.prefix(limit))
    }
}

// MARK: Events

extension PreviewBackend {
    public func events(kinds: [String]?, hostID: String?) -> AsyncThrowingStream<DaemonEvent, Error> {
        let (stream, continuation) = AsyncThrowingStream.makeStream(of: DaemonEvent.self)
        let id = UUID()
        state.withLock { $0.subscribers[id] = Subscriber(kinds: kinds, hostID: hostID, continuation: continuation) }
        continuation.onTermination = { [weak self] _ in
            self?.state.withLock { $0.subscribers[id] = nil }
        }
        return stream
    }
}
