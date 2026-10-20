import DeskhandClient
import Foundation

// MARK: QA

private struct QARunParams: Encodable {
    let hostID: String
    let scenarioPath: String
    let vars: [String: String]?
    let timeoutMs: Int?
}

private struct QARunIDParams: Encodable {
    let runID: String
}

private struct QAListParams: Encodable {
    let hostID: String?
    let limit: Int
    let cursor: String?
}

private struct ExportParams: Encodable {
    let jobID: String
    let dir: String
}

/// `run_id` and `run` beside the envelope of `qa.run` and `qa.cancel`.
private struct QARunResult: Decodable {
    let run: QARun
}

extension LiveBackend {
    public func qaRun(_ request: QARunRequest) async throws -> QARun {
        let params = QARunParams(hostID: request.hostID, scenarioPath: request.scenarioPath,
                                 vars: request.vars.isEmpty ? nil : request.vars, timeoutMs: request.timeoutMS)
        let envelope: Envelope<QARunResult> = try await client.call("qa.run", params)
        try Self.check(envelope)
        return envelope.payload.run
    }

    public func qaGet(runID: String) async throws -> QARun {
        try await client.call("qa.get", QARunIDParams(runID: runID))
    }

    /// `limit` is clamped to 1–500 (local-api.md §10).
    public func qaList(hostID: String?, limit: Int, cursor: String?) async throws -> QARunPage {
        try await client.call("qa.list", QAListParams(hostID: hostID, limit: min(max(limit, 1), 500), cursor: cursor))
    }

    /// A `running` envelope while teardown goes on and a `cancelled` one for a run that has ended
    /// cancelled both carry the run.
    public func qaCancel(runID: String) async throws -> QARun {
        let envelope: Envelope<QARunResult> = try await client.call("qa.cancel", QARunIDParams(runID: runID))
        if envelope.status != .cancelled { try Self.check(envelope) }
        return envelope.payload.run
    }

    public func artifactsExport(jobID: String, dir: String) async throws -> ArtifactExport {
        try await client.call("artifacts.export", ExportParams(jobID: jobID, dir: dir))
    }

    /// Reads the file off the main thread.
    public func evidenceData(path: String, maxBytes: Int) async throws -> Data {
        try await Task.detached(priority: .userInitiated) {
            let handle = try FileHandle(forReadingFrom: URL(fileURLWithPath: path))
            defer { try? handle.close() }
            return try handle.read(upToCount: max(maxBytes, 0)) ?? Data()
        }.value
    }
}
