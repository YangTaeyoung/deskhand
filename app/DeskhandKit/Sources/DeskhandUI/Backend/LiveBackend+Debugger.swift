import DeskhandClient
import Foundation

// MARK: Debugger

private struct DebugHostParams: Encodable {
    let hostID: String
}

private struct DebugIDParams: Encodable {
    let debugID: String
}

private struct DebugTargetParams: Encodable {
    let debugID: String
    let targetID: String
}

private struct DebugLaunchParams: Encodable {
    let hostID: String
    let path: String
    let args: [String]?
    let cwd: String?
    let pauseOnStart: Bool?
    let breakpoints: [BreakpointSpec]?
    let pauseOnExceptions: String?
}

private struct DebugAttachParams: Encodable {
    let hostID: String
    let port: Int?
    let pid: Int?
    let role: String?
    let breakpoints: [BreakpointSpec]?
    let pauseOnExceptions: String?
}

private struct BreakpointSetParams: Encodable {
    let debugID: String
    let match: String
    let pattern: String
    let line: Int
    let column: Int?
    let condition: String?
}

private struct BreakpointUpdateParams: Encodable {
    let debugID: String
    let breakpointID: String
    let enabled: Bool?
    let condition: String?
}

private struct BreakpointIDParams: Encodable {
    let debugID: String
    let breakpointID: String
}

private struct BreakpointsActivateParams: Encodable {
    let debugID: String
    let active: Bool
}

private struct ExceptionsParams: Encodable {
    let debugID: String
    let mode: String
}

private struct StepParams: Encodable {
    let debugID: String
    let targetID: String
    let kind: String
}

private struct VariablesParams: Encodable {
    let debugID: String
    let targetID: String
    let objectID: String
    let offset: Int
    let limit: Int
}

private struct EvaluateParams: Encodable {
    let debugID: String
    let targetID: String
    let expression: String
    let frameID: String?
}

private struct SourceParams: Encodable {
    let debugID: String
    let targetID: String
    let scriptID: String
}

private struct ScriptsParams: Encodable {
    let debugID: String
    let targetID: String?
}

private struct ScriptsResult: Decodable {
    let scripts: [DebugScript]
}

private struct ConsoleParams: Encodable {
    let debugID: String
    let cursor: Int?
    let before: Int?
    let limit: Int
}

private struct CloseParams: Encodable {
    let debugID: String
    let keepProcess: Bool?
}

private struct SessionListResult: Decodable {
    let sessions: [DebugSession]
}

private struct SessionResult: Decodable {
    let session: DebugSession
}

private struct TargetResult: Decodable {
    let target: DebugTarget?
}

private struct BreakpointResult: Decodable {
    let breakpoint: DebugBreakpoint
}

private struct OptionalBreakpointResult: Decodable {
    let breakpoint: DebugBreakpoint?
}

private struct OptionalSessionResult: Decodable {
    let session: DebugSession?
}

private struct ConsoleClearResult: Decodable {
    let clearedThrough: Int
}

extension LiveBackend {
    public func debugList(hostID: String) async throws -> [DebugSession] {
        let result: SessionListResult = try await client.call("debug.list", DebugHostParams(hostID: hostID))
        return result.sessions
    }

    public func debugGet(debugID: String) async throws -> DebugSession {
        try await client.call("debug.get", DebugIDParams(debugID: debugID))
    }

    public func debugLaunch(_ request: DebugLaunchRequest) async throws -> DebugSession {
        try await debugEnvelope("debug.electron.launch", DebugLaunchParams(
            hostID: request.hostID, path: request.path, args: request.args.isEmpty ? nil : request.args, cwd: request.cwd,
            pauseOnStart: request.pauseOnStart ? true : nil, breakpoints: request.breakpoints.isEmpty ? nil : request.breakpoints,
            pauseOnExceptions: request.pauseOnExceptions?.rawValue), as: SessionResult.self).session
    }

    public func debugAttach(_ request: DebugAttachRequest) async throws -> DebugSession {
        let params: DebugAttachParams
        let breakpoints = request.breakpoints.isEmpty ? nil : request.breakpoints
        let exceptions = request.pauseOnExceptions?.rawValue
        switch request.target {
        case .port(let port, let role):
            params = DebugAttachParams(hostID: request.hostID, port: port, pid: nil, role: role?.rawValue, breakpoints: breakpoints,
                                       pauseOnExceptions: exceptions)
        case .pid(let pid):
            params = DebugAttachParams(hostID: request.hostID, port: nil, pid: pid, role: nil, breakpoints: breakpoints,
                                       pauseOnExceptions: exceptions)
        }
        return try await debugEnvelope("debug.attach", params, as: SessionResult.self).session
    }

    public func debugTargetAttach(debugID: String, targetID: String) async throws -> DebugTarget {
        let result = try await debugEnvelope("debug.target.attach", DebugTargetParams(debugID: debugID, targetID: targetID),
                                             as: TargetResult.self)
        guard let target = result.target else {
            throw OperationError(code: "INTERNAL", message: .ui("error.debugNoTarget", "데몬이 연결한 대상을 돌려주지 않았습니다"))
        }
        return target
    }

    public func debugBreakpointSet(debugID: String, spec: BreakpointSpec) async throws -> DebugBreakpoint {
        try await debugEnvelope("debug.breakpoint.set", BreakpointSetParams(
            debugID: debugID, match: spec.match, pattern: spec.pattern, line: spec.line, column: spec.column,
            condition: spec.condition), as: BreakpointResult.self).breakpoint
    }

    public func debugBreakpointUpdate(debugID: String, breakpointID: String, enabled: Bool?, condition: String?) async throws -> DebugBreakpoint {
        try await debugEnvelope("debug.breakpoint.update", BreakpointUpdateParams(
            debugID: debugID, breakpointID: breakpointID, enabled: enabled, condition: condition), as: BreakpointResult.self).breakpoint
    }

    public func debugBreakpointRemove(debugID: String, breakpointID: String) async throws -> DebugBreakpoint? {
        try await debugEnvelope("debug.breakpoint.remove", BreakpointIDParams(debugID: debugID, breakpointID: breakpointID),
                                as: OptionalBreakpointResult.self).breakpoint
    }

    public func debugBreakpointsActivate(debugID: String, active: Bool) async throws -> DebugSession? {
        try await debugEnvelope("debug.breakpoints.activate", BreakpointsActivateParams(debugID: debugID, active: active),
                                as: OptionalSessionResult.self).session
    }

    public func debugExceptions(debugID: String, mode: ExceptionPauseMode) async throws -> DebugSession? {
        try await debugEnvelope("debug.exceptions", ExceptionsParams(debugID: debugID, mode: mode.rawValue), as: OptionalSessionResult.self).session
    }

    public func debugPause(debugID: String, targetID: String) async throws -> DebugTarget? {
        try await debugEnvelope("debug.pause", DebugTargetParams(debugID: debugID, targetID: targetID), as: TargetResult.self).target
    }

    public func debugResume(debugID: String, targetID: String) async throws -> DebugTarget? {
        try await debugEnvelope("debug.resume", DebugTargetParams(debugID: debugID, targetID: targetID), as: TargetResult.self).target
    }

    public func debugStep(debugID: String, targetID: String, kind: DebugStepKind) async throws -> DebugTarget? {
        try await debugEnvelope("debug.step", StepParams(debugID: debugID, targetID: targetID, kind: kind.rawValue),
                                as: TargetResult.self).target
    }

    public func debugState(debugID: String, targetID: String) async throws -> DebugPauseState {
        try await client.call("debug.state", DebugTargetParams(debugID: debugID, targetID: targetID))
    }

    /// `limit` is clamped to 1–1000 (local-api.md §9).
    public func debugVariables(debugID: String, targetID: String, objectID: String, offset: Int, limit: Int) async throws -> DebugPropertyPage {
        try await client.call("debug.variables", VariablesParams(
            debugID: debugID, targetID: targetID, objectID: objectID, offset: max(0, offset), limit: min(max(limit, 1), 1000)))
    }

    public func debugEvaluate(debugID: String, targetID: String, expression: String, frameID: String?) async throws -> DebugEvaluation {
        try await debugEnvelope("debug.evaluate", EvaluateParams(
            debugID: debugID, targetID: targetID, expression: expression, frameID: frameID), as: DebugEvaluation.self)
    }

    public func debugScripts(debugID: String, targetID: String?) async throws -> [DebugScript] {
        let result: ScriptsResult = try await client.call("debug.scripts", ScriptsParams(debugID: debugID, targetID: targetID))
        return result.scripts
    }

    public func debugSource(debugID: String, targetID: String, scriptID: String) async throws -> DebugSource {
        try await client.call("debug.source", SourceParams(debugID: debugID, targetID: targetID, scriptID: scriptID))
    }

    /// `limit` is clamped to 1–1000 (local-api.md §9).
    public func debugConsole(_ query: ConsoleQuery) async throws -> ConsolePage {
        try await client.call("debug.console", ConsoleParams(
            debugID: query.debugID, cursor: query.cursor, before: query.before,
            limit: min(max(query.limit, 1), ConsoleQuery.maximumLimit)))
    }

    public func debugConsoleClear(debugID: String) async throws -> Int {
        try await debugEnvelope("debug.console.clear", DebugIDParams(debugID: debugID), as: ConsoleClearResult.self).clearedThrough
    }

    public func debugClose(debugID: String, keepProcess: Bool) async throws {
        _ = try await debugEnvelope("debug.close", CloseParams(debugID: debugID, keepProcess: keepProcess ? true : nil), as: Empty.self)
    }

    /// Calls a method whose result is an envelope, throws its error with the envelope's next action
    /// unless it succeeded, then decodes `T` from the same object.
    private func debugEnvelope<T: Decodable>(_ method: String, _ params: some Encodable & Sendable, as type: T.Type) async throws -> T {
        let value: JSONValue = try await client.call(method, params)
        if value["status"] != nil {
            let envelope = try Self.decode(Envelope<Empty>.self, from: value)
            do {
                try Self.check(envelope)
            } catch var error as OperationError {
                if error.nextAction == nil { error.nextAction = envelope.nextAction }
                throw error
            }
        }
        return try Self.decode(T.self, from: value)
    }
}

// MARK: Reconnects

extension LiveBackend {
    public func reconnects() -> AsyncStream<Void> {
        client.reconnects()
    }
}
