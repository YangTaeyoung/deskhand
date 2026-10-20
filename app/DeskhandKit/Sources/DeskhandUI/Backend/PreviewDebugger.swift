import DeskhandClient
import Foundation
import Synchronization
import struct DeskhandClient.Host

/// What a paused simulated target was told to do next.
enum PreviewResume: Sendable {
    case resume
    case step(DebugStepKind)
    case close
}

/// Debugging sessions of the preview: the Shop Admin simulation of `PreviewDebugProgram`, and
/// attached sessions that run nothing.
final class PreviewDebugStore: Sendable {
    typealias Program = PreviewDebugProgram

    struct Pause: Sendable {
        var flow: Program.Flow
        var step: Int
        var gen: Int
        var reason: DebugPauseReason
        var hits: [String]
    }

    struct Session: Sendable {
        var info: DebugSession
        /// Runs the simulated app's flows.
        var simulated: Bool
        var console: [ConsoleEntry] = []
        var loaded: Set<Program.Script> = []
        var pauses: [String: Pause] = [:]
        var gens: [String: Int] = [:]
        var pauseRequested: Set<String> = []
        var waiting: [String: CheckedContinuation<PreviewResume, Never>] = [:]
        var runner: Task<Void, Never>?
        var iteration = 0
    }

    struct State: Sendable {
        var sessions: [String: Session] = [:]
        var order: [String] = []
        var sessionCount = 0
        var breakpointCount = 0
    }

    let state = Mutex(State())

    func with<T>(_ debugID: String, _ body: (inout Session) throws -> T) throws -> T {
        try state.withLock { s in
            guard var session = s.sessions[debugID], session.info.sessionState != .closed else {
                throw RPCErrorInfo(message: "디버그 세션을 찾을 수 없습니다. 이미 닫혔을 수 있습니다 (\(debugID))", code: "NOT_FOUND")
            }
            let result = try body(&session)
            s.sessions[debugID] = session
            return result
        }
    }
}

extension PreviewBackend {
    typealias Program = PreviewDebugProgram

    // MARK: Sessions

    public func debugList(hostID: String) async throws -> [DebugSession] {
        _ = try listedHost(hostID)
        return debugger.state.withLock { s in s.order.compactMap { s.sessions[$0]?.info }.filter { $0.hostID == hostID } }
    }

    public func debugGet(debugID: String) async throws -> DebugSession {
        guard let info = debugger.state.withLock({ $0.sessions[debugID]?.info }) else {
            throw RPCErrorInfo(message: "디버그 세션을 찾을 수 없습니다 (\(debugID))", code: "NOT_FOUND")
        }
        return info
    }

    /// The host when it is connected and its agent can open tunnels; otherwise the failed envelope's error.
    private func debuggableHost(_ hostID: String) throws -> Host {
        let host = try listedHost(hostID)
        guard host.connection == .ready || host.connection == .degraded else {
            throw OperationError(code: "OFFLINE", message: "\(host.name)에 연결되어 있지 않습니다")
        }
        if let tunnel = host.caps["tunnel"], tunnel.capabilityState != .ready {
            throw OperationError(code: "CAPABILITY_UNSUPPORTED", message: tunnel.reason ?? "이 PC의 에이전트가 터널을 열 수 없습니다")
        }
        return host
    }

    /// Launches the simulated Shop Admin; `Shop.exe` (WebView2) never opens a debug port and a
    /// path that is not an `.exe` is not found.
    public func debugLaunch(_ request: DebugLaunchRequest) async throws -> DebugSession {
        _ = try debuggableHost(request.hostID)
        await pause(fraction: 1.2)
        let path = request.path.trimmingCharacters(in: .whitespaces)
        guard path.lowercased().hasSuffix(".exe") else {
            throw OperationError(code: "NOT_FOUND", message: "‘\(path)’ 실행 파일을 찾을 수 없습니다")
        }
        if path.caseInsensitiveCompare(Program.notElectronPath) == .orderedSame {
            await pause(fraction: 1.5)
            throw OperationError(code: "TIMEOUT", message: "20초 안에 디버그 포트가 열리지 않아 앱을 종료했습니다")
        }
        let name = String(FilePath.lastComponent(path, style: .windows).dropLast(4))
        let created = Date.now
        let session = debugger.state.withLock { s -> DebugSession in
            s.sessionCount += 1
            let id = "d-preview-\(s.sessionCount)"
            var info = Program.session(hostID: request.hostID, debugID: id, created: created)
            info.name = name
            info.path = path
            info.args = request.args
            var session = PreviewDebugStore.Session(info: info, simulated: true)
            session.loaded = Set(Program.Script.loadedAtLaunch)
            s.sessions[id] = session
            s.order.append(id)
            return info
        }
        emit(kind: EventKind.debugSessionChanged, hostID: request.hostID,
             payload: ["debug_id": .string(session.debugID), "session": Self.payload(session)])
        startSimulation(session.debugID, pauseOnStart: request.pauseOnStart)
        return session
    }

    /// Attaches to Visual Studio Code (PID 7000), the Vite dev server (PID 9272) or a Node
    /// inspector on port 9229. These sessions run nothing the preview simulates.
    public func debugAttach(_ request: DebugAttachRequest) async throws -> DebugSession {
        let host = try debuggableHost(request.hostID)
        await pause(fraction: 0.8)
        let name: String
        let pid: Int?
        let port: Int
        switch request.target {
        case .pid(let value):
            let list = PreviewFixtures.processes(hostID: host.hostID, user: host.session?.user ?? "user", ended: processes.ended(hostID: host.hostID),
                                                 anchor: processes.anchor)
            guard let process = list.first(where: { $0.pid == value }) else {
                throw OperationError(code: "NOT_FOUND", message: "PID \(String(value)) 프로세스가 없습니다")
            }
            let lowered = process.name.lowercased()
            guard lowered == "node.exe" || lowered == "code.exe" || lowered == "slack.exe" else {
                throw OperationError(code: "CAPABILITY_UNSUPPORTED",
                                     message: "‘\(process.name)’ 프로세스는 Node나 Electron 앱이 아니어서 검사기를 켤 수 없습니다")
            }
            name = String(process.name.dropLast(4))
            pid = value
            port = 9229
        case .port(let value, _):
            guard value == 9229 else {
                throw OperationError(code: "NOT_FOUND", message: "원격 PC의 포트 \(String(value))에서 응답하는 디버거가 없습니다")
            }
            name = "node"
            pid = nil
            port = value
        }
        let created = Date.now
        let session = debugger.state.withLock { s -> DebugSession in
            s.sessionCount += 1
            let id = "d-preview-\(s.sessionCount)"
            let target = DebugTarget(id: UUID().uuidString.lowercased(), role: DebugTargetRole.main.rawValue, type: "node",
                                     url: "file:///C:/Program Files/\(name)/main.js", title: name)
            let info = DebugSession(debugID: id, hostID: request.hostID, jobID: "j-debug-\(s.sessionCount)", kind: .attach, name: name,
                                    pid: pid, ports: DebugPorts(inspect: port), targets: [target], createdAt: Timestamp(date: created))
            var session = PreviewDebugStore.Session(info: info, simulated: false)
            session.console = [ConsoleEntry(seq: 1, time: Timestamp(date: created), targetID: target.id, level: .info,
                                            text: "Debugger attached.")]
            s.sessions[id] = session
            s.order.append(id)
            return info
        }
        emit(kind: EventKind.debugSessionChanged, hostID: request.hostID,
             payload: ["debug_id": .string(session.debugID), "session": Self.payload(session)])
        return session
    }

    public func debugTargetAttach(debugID: String, targetID: String) async throws -> DebugTarget {
        try debugger.with(debugID) { session in
            guard let i = session.info.targets.firstIndex(where: { $0.id == targetID }) else {
                throw RPCErrorInfo(message: "대상을 찾을 수 없습니다", code: "NOT_FOUND")
            }
            session.info.targets[i].attached = true
            return session.info.targets[i]
        }
    }

    public func debugClose(debugID: String, keepProcess: Bool) async throws {
        let (hostID, runner, waiting) = try debugger.with(debugID) { session in
            session.info.state = DebugSessionState.closed.rawValue
            session.info.closedAt = Timestamp(date: .now)
            session.info.closeReason = DebugCloseReason.requested.rawValue
            let waiting = Array(session.waiting.values)
            session.waiting = [:]
            session.pauses = [:]
            return (session.info.hostID, session.runner, waiting)
        }
        runner?.cancel()
        for continuation in waiting { continuation.resume(returning: .close) }
        await pause(fraction: 0.3)
        emit(kind: EventKind.debugClosed, hostID: hostID,
             payload: ["debug_id": .string(debugID), "reason": .string(DebugCloseReason.requested.rawValue)])
    }

    // MARK: Breakpoints

    public func debugBreakpointSet(_ request: BreakpointRequest) async throws -> DebugBreakpoint {
        guard request.line >= 1 else { throw RPCErrorInfo(message: "줄 번호는 1 이상이어야 합니다", code: "INVALID_ARGUMENT") }
        var breakpoint: DebugBreakpoint
        switch request.match {
        case .url(let url):
            breakpoint = DebugBreakpoint(breakpointID: "", url: url, line: request.line, column: request.column, condition: request.condition)
        case .regex(let pattern):
            guard (try? NSRegularExpression(pattern: pattern)) != nil else {
                throw RPCErrorInfo(message: "url_regex가 올바른 정규식이 아닙니다", code: "INVALID_ARGUMENT")
            }
            breakpoint = DebugBreakpoint(breakpointID: "", urlRegex: pattern, line: request.line, column: request.column,
                                         condition: request.condition)
        }
        let hostID = try debugger.with(request.debugID) { $0.info.hostID }
        breakpoint.breakpointID = debugger.nextBreakpointID()
        let resolved = try debugger.with(request.debugID) { session -> DebugBreakpoint in
            var bp = breakpoint
            bp.locations = Self.locations(of: bp, in: session.loaded)
            bp.resolved = !bp.locations.isEmpty
            session.info.breakpoints.append(bp)
            return bp
        }
        await pause(fraction: 0.2)
        emitBreakpoint(resolved, debugID: request.debugID, hostID: hostID)
        return resolved
    }

    public func debugBreakpointEnable(debugID: String, breakpointID: String, enabled: Bool) async throws -> DebugBreakpoint {
        let (hostID, bp) = try debugger.with(debugID) { session -> (String, DebugBreakpoint) in
            guard let i = session.info.breakpoints.firstIndex(where: { $0.breakpointID == breakpointID }) else {
                throw RPCErrorInfo(message: "중단점을 찾을 수 없습니다", code: "NOT_FOUND")
            }
            session.info.breakpoints[i].enabled = enabled
            return (session.info.hostID, session.info.breakpoints[i])
        }
        emitBreakpoint(bp, debugID: debugID, hostID: hostID)
        return bp
    }

    public func debugBreakpointRemove(debugID: String, breakpointID: String) async throws {
        let (hostID, bp) = try debugger.with(debugID) { session -> (String, DebugBreakpoint) in
            guard let i = session.info.breakpoints.firstIndex(where: { $0.breakpointID == breakpointID }) else {
                throw RPCErrorInfo(message: "중단점을 찾을 수 없습니다", code: "NOT_FOUND")
            }
            return (session.info.hostID, session.info.breakpoints.remove(at: i))
        }
        emitBreakpoint(bp, debugID: debugID, hostID: hostID, removed: true)
    }

    public func debugBreakpointsActivate(debugID: String, active: Bool) async throws {
        let info = try debugger.with(debugID) { session -> DebugSession in
            session.info.breakpointsActive = active
            return session.info
        }
        emit(kind: EventKind.debugSessionChanged, hostID: info.hostID, payload: ["debug_id": .string(debugID), "session": Self.payload(info)])
    }

    public func debugExceptions(debugID: String, mode: ExceptionPauseMode) async throws {
        let info = try debugger.with(debugID) { session -> DebugSession in
            session.info.pauseOnExceptions = mode.rawValue
            return session.info
        }
        emit(kind: EventKind.debugSessionChanged, hostID: info.hostID, payload: ["debug_id": .string(debugID), "session": Self.payload(info)])
    }

    private func emitBreakpoint(_ breakpoint: DebugBreakpoint, debugID: String, hostID: String, removed: Bool = false) {
        emit(kind: EventKind.debugBreakpointChanged, hostID: hostID, payload: [
            "debug_id": .string(debugID), "breakpoint": Self.payload(breakpoint), "removed": .bool(removed),
        ])
    }

    /// Where `breakpoint` binds in the loaded scripts: the first line with code at or after its line.
    static func locations(of breakpoint: DebugBreakpoint, in loaded: Set<Program.Script>) -> [BreakpointLocation] {
        Program.Script.allCases.filter { loaded.contains($0) && BreakpointPattern.matches(breakpoint, url: $0.url) }.compactMap { script in
            guard let line = script.breakableLine(atOrAfter: breakpoint.line) else { return nil }
            return BreakpointLocation(targetID: script.targetID, scriptID: script.scriptID, url: script.url, line: line,
                                      column: script.column(line))
        }
    }

    // MARK: Execution

    public func debugPause(debugID: String, targetID: String) async throws -> DebugTarget? {
        try debugger.with(debugID) { session in
            guard let target = session.info.targets.first(where: { $0.id == targetID }) else {
                throw RPCErrorInfo(message: "대상을 찾을 수 없습니다", code: "NOT_FOUND")
            }
            if session.pauses[targetID] == nil { session.pauseRequested.insert(targetID) }
            return Self.target(target, in: session)
        }
    }

    public func debugResume(debugID: String, targetID: String) async throws -> DebugTarget? {
        try await proceed(debugID: debugID, targetID: targetID, .resume, requirePaused: false)
    }

    public func debugStep(debugID: String, targetID: String, kind: DebugStepKind) async throws -> DebugTarget? {
        try await proceed(debugID: debugID, targetID: targetID, .step(kind), requirePaused: true)
    }

    /// Ends the pause of `targetID` with `command`: bumps its gen, emits `debug.resumed` and lets
    /// the flow continue.
    private func proceed(debugID: String, targetID: String, _ command: PreviewResume, requirePaused: Bool) async throws -> DebugTarget? {
        let (hostID, target, continuation, gen) = try debugger.with(debugID) { session in
            guard let target = session.info.targets.first(where: { $0.id == targetID }) else {
                throw RPCErrorInfo(message: "대상을 찾을 수 없습니다", code: "NOT_FOUND")
            }
            guard session.pauses[targetID] != nil, let continuation = session.waiting.removeValue(forKey: targetID) else {
                if requirePaused {
                    throw OperationError(code: "STALE_TARGET", message: "대상이 멈춰 있지 않아 한 단계 실행할 수 없습니다", nextAction: "check_state")
                }
                return (session.info.hostID, Self.target(target, in: session), nil as CheckedContinuation<PreviewResume, Never>?, nil as Int?)
            }
            session.pauses[targetID] = nil
            let gen = (session.gens[targetID] ?? 0) + 1
            session.gens[targetID] = gen
            return (session.info.hostID, Self.target(target, in: session), Optional(continuation), Optional(gen))
        }
        if let gen {
            emit(kind: EventKind.debugResumed, hostID: hostID,
                 payload: ["debug_id": .string(debugID), "target_id": .string(targetID), "gen": .int(gen)])
        }
        continuation?.resume(returning: command)
        return target
    }

    static func target(_ target: DebugTarget, in session: PreviewDebugStore.Session) -> DebugTarget {
        var copy = target
        copy.paused = session.pauses[target.id] != nil
        copy.gen = session.gens[target.id] ?? 0
        return copy
    }

    public func debugState(debugID: String, targetID: String) async throws -> DebugPauseState {
        try debugger.with(debugID) { session in
            guard let pause = session.pauses[targetID] else {
                return DebugPauseState(targetID: targetID, gen: session.gens[targetID] ?? 0, paused: false)
            }
            return Program.pauseState(flow: pause.flow, step: pause.step, gen: pause.gen, reason: pause.reason, hitBreakpoints: pause.hits)
        }
    }

    public func debugVariables(debugID: String, targetID: String, objectID: String, offset: Int, limit: Int) async throws -> DebugPropertyPage {
        let stale = RPCErrorInfo(message: "이 개체는 대상이 다시 실행되어 더 이상 유효하지 않습니다", code: "STALE_TARGET")
        guard let path = Program.ObjectPath(objectID) else { throw stale }
        try debugger.with(debugID) { session in
            guard path.gen >= 0 else { return }
            guard let pause = session.pauses[targetID], pause.gen == path.gen, pause.flow == path.flow, pause.step == path.step else { throw stale }
        }
        guard let all = Program.properties(objectID: objectID) else { throw stale }
        await pause(fraction: 0.15)
        let start = min(max(0, offset), all.count)
        let end = min(all.count, start + min(max(limit, 1), 1000))
        return DebugPropertyPage(properties: Array(all[start..<end]), total: all.count)
    }

    public func debugEvaluate(debugID: String, targetID: String, expression: String, frameID: String?) async throws -> DebugEvaluation {
        let flow = try debugger.with(debugID) { session -> Program.Flow in
            session.info.targets.first { $0.id == targetID }?.targetRole == .main ? .print : .submit
        }
        await pause(fraction: 0.2)
        guard let frameID else { return Program.evaluate(expression, frame: nil, flow: flow) }
        let parts = frameID.components(separatedBy: ":")
        guard parts.count == 5, parts[0] == "cf", let frameFlow = Program.Flow(rawValue: parts[1]), let step = Int(parts[2]),
              let frame = Int(parts[3]), let gen = Int(parts[4]) else {
            throw OperationError(code: "STALE_TARGET", message: "호출 프레임을 찾을 수 없습니다", nextAction: "check_state")
        }
        try debugger.with(debugID) { session in
            guard let pause = session.pauses[targetID], pause.gen == gen, pause.flow == frameFlow, pause.step == step else {
                throw OperationError(code: "STALE_TARGET", message: "대상이 이미 다시 실행되어 이 프레임에서 평가할 수 없습니다", nextAction: "check_state")
            }
        }
        return Program.evaluate(expression, frame: Program.ObjectPath(gen: gen, flow: frameFlow, step: step, frame: frame, scope: 0),
                                flow: frameFlow)
    }

    public func debugScripts(debugID: String, targetID: String?) async throws -> [DebugScript] {
        let loaded = try debugger.with(debugID) { $0.loaded }
        return Program.Script.allCases.filter { loaded.contains($0) && (targetID == nil || $0.targetID == targetID) }
            .map { DebugScript(scriptID: $0.scriptID, targetID: $0.targetID, url: $0.url) }
    }

    public func debugSource(debugID: String, targetID: String, scriptID: String) async throws -> DebugSource {
        let loaded = try debugger.with(debugID) { $0.loaded }
        guard let script = Program.Script.script(id: scriptID), loaded.contains(script), script.targetID == targetID else {
            throw RPCErrorInfo(message: "스크립트를 찾을 수 없습니다 (ID \(scriptID))", code: "NOT_FOUND")
        }
        await pause(fraction: 0.2)
        return DebugSource(scriptID: scriptID, url: script.url, text: script.source)
    }

    public func debugConsole(_ query: ConsoleQuery) async throws -> ConsolePage {
        guard let session = debugger.state.withLock({ $0.sessions[query.debugID] }) else {
            throw RPCErrorInfo(message: "디버그 세션을 찾을 수 없습니다 (\(query.debugID))", code: "NOT_FOUND")
        }
        return Self.page(session.console, query)
    }

    /// `debug.console` over `entries` (ascending seq) with the cursor rules of local-api.md §9.
    static func page(_ entries: [ConsoleEntry], _ query: ConsoleQuery) -> ConsolePage {
        let limit = min(max(query.limit, 1), ConsoleQuery.maximumLimit)
        var slice: [ConsoleEntry]
        var hasMore = false
        if let cursor = query.cursor {
            let after = entries.filter { $0.seq > cursor }
            slice = Array(after.prefix(limit))
            hasMore = after.count > limit
        } else if let before = query.before {
            slice = Array(entries.filter { $0.seq < before }.suffix(limit))
        } else {
            slice = Array(entries.suffix(limit))
        }
        let hasEarlier = slice.first.map { first in entries.contains { $0.seq < first.seq } } ?? false
        return ConsolePage(entries: slice, nextCursor: slice.last?.seq ?? query.cursor ?? 0, hasMore: hasMore, hasEarlier: hasEarlier)
    }

    // MARK: Simulation

    private func appendConsole(_ debugID: String, target: String, source: ConsoleSource = .console, level: ConsoleLevel, text: String,
                               script: Program.Script?, line: Int?, column: Int? = nil, stack: [StackFrameInfo]? = nil) {
        let made = try? debugger.with(debugID) { session -> (String, ConsoleEntry) in
            let entry = ConsoleEntry(seq: (session.console.last?.seq ?? 0) + 1, time: Timestamp(date: .now), targetID: target, source: source,
                                     level: level, text: text, scriptID: script?.scriptID, url: script?.url, line: line,
                                     column: column ?? line.map { script?.column($0) ?? 1 }, stack: stack)
            session.console.append(entry)
            if session.console.count > 5000 { session.console.removeFirst(session.console.count - 5000) }
            return (session.info.hostID, entry)
        }
        guard let (hostID, entry) = made else { return }
        emit(kind: EventKind.debugConsole, hostID: hostID, payload: ["debug_id": .string(debugID), "entry": Self.payload(entry)])
    }

    /// Prints the launch messages and, while time passes in the preview, runs the submit and print
    /// flows every few seconds and the coupon flow every third round.
    private func startSimulation(_ debugID: String, pauseOnStart: Bool) {
        guard simulatesTime else {
            for item in Program.launchConsole() {
                appendConsole(debugID, target: item.target, source: item.source, level: item.level, text: item.text, script: item.script,
                              line: item.line)
            }
            return
        }
        let task = Task { [weak self] in
            guard let self else { return }
            var elapsed = 0.0
            for item in Program.launchConsole() {
                await pause(fraction: (item.after - elapsed) / 0.7)
                elapsed = item.after
                guard !Task.isCancelled else { return }
                appendConsole(debugID, target: item.target, source: item.source, level: item.level, text: item.text, script: item.script,
                              line: item.line)
            }
            if pauseOnStart { _ = try? await debugPause(debugID: debugID, targetID: Program.mainTargetID) }
            while !Task.isCancelled {
                for _ in 0..<40 {
                    let requested = (try? debugger.with(debugID) { !$0.pauseRequested.isEmpty }) ?? false
                    if requested || Task.isCancelled { break }
                    await pause(fraction: 0.15)
                }
                guard !Task.isCancelled else { return }
                let round = (try? debugger.with(debugID) { session -> Int in
                    session.iteration += 1
                    return session.iteration
                }) ?? 0
                await debugRun(debugID: debugID, flow: .submit)
                await debugRun(debugID: debugID, flow: .print)
                if round == 1 { loadReceiptScript(debugID) }
                if round % 3 == 2 { await debugRun(debugID: debugID, flow: .coupon) }
            }
        }
        _ = try? debugger.with(debugID) { $0.runner = task }
    }

    /// Loads `receipt.js` and binds the breakpoints that match it.
    private func loadReceiptScript(_ debugID: String) {
        let changed = (try? debugger.with(debugID) { session -> (String, [DebugBreakpoint]) in
            session.loaded.insert(.receipt)
            var changed: [DebugBreakpoint] = []
            for i in session.info.breakpoints.indices {
                let locations = Self.locations(of: session.info.breakpoints[i], in: session.loaded)
                guard locations != session.info.breakpoints[i].locations else { continue }
                session.info.breakpoints[i].locations = locations
                session.info.breakpoints[i].resolved = !locations.isEmpty
                changed.append(session.info.breakpoints[i])
            }
            return (session.info.hostID, changed)
        })
        guard let (hostID, breakpoints) = changed else { return }
        for bp in breakpoints { emitBreakpoint(bp, debugID: debugID, hostID: hostID) }
    }

    /// Walks `flow` once: stops where a breakpoint, a step or a pause request says, prints its
    /// console messages, and stops on the coupon flow's exception unless exceptions are ignored.
    func debugRun(debugID: String, flow: Program.Flow) async {
        let steps = Program.steps(flow)
        let target = flow.targetID
        var stepping: (kind: DebugStepKind, depth: Int)?
        for (index, step) in steps.enumerated() {
            guard let snapshot = try? debugger.with(debugID, { $0 }), snapshot.simulated else { return }
            var reason: DebugPauseReason?
            var hits: [String] = []
            if snapshot.pauseRequested.contains(target) {
                reason = .pauseRequest
            } else if let stepping, Self.stepStops(stepping.kind, from: stepping.depth, at: step.depth) {
                reason = .step
            } else if snapshot.info.breakpointsActive, let top = step.frames.first {
                hits = snapshot.info.breakpoints.filter { bp in
                    bp.enabled && bp.locations.contains { $0.scriptID == top.script.scriptID && $0.line == top.line }
                        && Self.conditionHolds(bp.condition, flow: flow, step: index)
                }.map(\.breakpointID)
                if !hits.isEmpty { reason = .breakpoint }
            }
            if let reason {
                switch await stop(debugID, flow: flow, step: index, reason: reason, hits: hits) {
                case .close: return
                case .resume: stepping = nil
                case .step(let kind): stepping = (kind, step.depth)
                }
            }
            if let log = step.log, let top = step.frames.first {
                appendConsole(debugID, target: target, level: log.level, text: log.text, script: top.script, line: top.line)
            }
            if step.throwing != nil {
                let mode = (try? debugger.with(debugID) { $0.info.exceptionMode }) ?? .uncaught
                if mode != .none, await stop(debugID, flow: flow, step: index, reason: .exception, hits: []) == .close { return }
                let entry = Program.couponException(seq: 0, time: Timestamp(date: .now))
                appendConsole(debugID, target: target, source: .exception, level: .error, text: entry.text, script: .api, line: entry.line,
                              column: entry.column, stack: entry.stack)
                return
            }
            if flow == .submit, step.frames.first?.function == "request", step.depth > 1 { await pause(fraction: 0.6) }
        }
    }

    /// Whether a step of `kind` started at `depth` stops at a statement of `next` depth.
    static func stepStops(_ kind: DebugStepKind, from depth: Int, at next: Int) -> Bool {
        switch kind {
        case .into: true
        case .over: next <= depth
        case .out: next < depth
        }
    }

    /// `path > number` style conditions evaluated in the step's innermost frame; anything else holds.
    static func conditionHolds(_ condition: String?, flow: Program.Flow, step: Int) -> Bool {
        guard let condition = condition?.trimmingCharacters(in: .whitespaces), !condition.isEmpty else { return true }
        let pattern = try? NSRegularExpression(pattern: #"^(.+?)\s*(>=|<=|===|!==|==|!=|>|<)\s*(-?\d+(\.\d+)?)$"#)
        guard let match = pattern?.firstMatch(in: condition, range: NSRange(condition.startIndex..., in: condition)),
              let pathRange = Range(match.range(at: 1), in: condition), let opRange = Range(match.range(at: 2), in: condition),
              let numberRange = Range(match.range(at: 3), in: condition), let bound = Double(condition[numberRange]) else { return true }
        let frame = Program.ObjectPath(gen: -1, flow: flow, step: step, frame: 0, scope: 0)
        let result = Program.evaluate(String(condition[pathRange]), frame: frame, flow: flow)
        guard result.exception == nil, let value = Double(result.value.valuePreview) else { return false }
        switch String(condition[opRange]) {
        case ">": return value > bound
        case "<": return value < bound
        case ">=": return value >= bound
        case "<=": return value <= bound
        case "==", "===": return value == bound
        default: return value != bound
        }
    }

    /// Pauses `flow` at `step`: bumps the target's gen, emits `debug.paused`, and waits for resume,
    /// step or close.
    private func stop(_ debugID: String, flow: Program.Flow, step: Int, reason: DebugPauseReason, hits: [String]) async -> PreviewResume {
        let target = flow.targetID
        guard let (hostID, gen) = try? debugger.with(debugID, { session -> (String, Int) in
            let gen = (session.gens[target] ?? 0) + 1
            session.gens[target] = gen
            session.pauseRequested.remove(target)
            session.pauses[target] = PreviewDebugStore.Pause(flow: flow, step: step, gen: gen, reason: reason, hits: hits)
            return (session.info.hostID, gen)
        }) else { return .close }
        return await withCheckedContinuation { continuation in
            let stored = (try? debugger.with(debugID) { session -> Bool in
                guard session.pauses[target]?.gen == gen else { return false }
                session.waiting[target] = continuation
                return true
            }) ?? false
            guard stored else { return continuation.resume(returning: .close) }
            let state = Program.pauseState(flow: flow, step: step, gen: gen, reason: reason, hitBreakpoints: hits)
            emit(kind: EventKind.debugPaused, hostID: hostID, payload: ["debug_id": .string(debugID), "state": Self.payload(state)])
        }
    }
}

extension PreviewDebugStore {
    func nextBreakpointID() -> String {
        state.withLock { s in
            s.breakpointCount += 1
            return "bp-\(s.breakpointCount)"
        }
    }
}

extension PreviewResume: Equatable {}
