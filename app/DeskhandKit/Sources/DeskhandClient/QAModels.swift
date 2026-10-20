import Foundation

// QA models of local-api.md §5 and §10.

/// `status` of a QARun.
public enum QARunStatus: String, Codable, Sendable, CaseIterable {
    case running
    case passed
    case failed
    case blocked
    case cancelled
    case error
}

/// `status` of a QAStep. `pending` and `running` do not remain once the run has ended.
public enum QAStepStatus: String, Codable, Sendable, CaseIterable {
    case pending
    case running
    case pass
    case fail
    case blocked
    case notRun = "not_run"
}

/// `phase` of a QAStep, in execution order.
public enum QAStepPhase: String, Codable, Sendable, CaseIterable {
    case setup
    case steps
    case teardown
}

/// `{code, message}` of a step or run that did not go as planned.
public struct QAErrorInfo: Codable, Sendable, Equatable, Hashable {
    public var code: String
    public var message: String

    public init(code: String, message: String) {
        self.code = code
        self.message = message
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        code = try c.decodeIfPresent(String.self, forKey: AnyKey("code")) ?? ""
        message = try c.decodeIfPresent(String.self, forKey: AnyKey("message")) ?? ""
    }
}

/// One checked expectation of a step (`assertions[]`): what was expected and the last value seen.
public struct QAAssertion: Codable, Sendable, Equatable {
    /// `exists`, `count`, `enabled`, `text`, `text_contains`, `text_matches`, `contains`, `not_contains`,
    /// `matches`, `size`, `sha256`, `window`, `exit_code`, `stdout_contains`, `stderr_contains`.
    public var kind: String
    public var expected: JSONValue
    /// Strings are cut at 2000 characters and end with `…`.
    public var actual: JSONValue
    public var passed: Bool

    public init(kind: String, expected: JSONValue, actual: JSONValue, passed: Bool) {
        self.kind = kind
        self.expected = expected
        self.actual = actual
        self.passed = passed
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        kind = try c.decode(String.self, forKey: AnyKey("kind"))
        expected = try c.decodeIfPresent(JSONValue.self, forKey: AnyKey("expected")) ?? .null
        actual = try c.decodeIfPresent(JSONValue.self, forKey: AnyKey("actual")) ?? .null
        passed = try c.decodeIfPresent(Bool.self, forKey: AnyKey("passed")) ?? false
    }
}

/// One step of a QA run.
public struct QAStep: Codable, Sendable, Equatable, Identifiable {
    /// From 1, in execution order across setup, steps and teardown.
    public var index: Int
    public var phase: String
    public var name: String
    /// The step kind: `push`, `launch`, `wait_window`, `click`, `type`, `key`, `ui_act`, `assert_ui`,
    /// `assert_screen_text`, `assert_file`, `exec`, `screenshot`, `collect`, `sleep`, `close`.
    public var action: String
    /// Line of the scenario file.
    public var line: Int
    /// The step's values after substitution, `secret_` variables as `***`, with the scenario's key spelling.
    public var params: [String: JSONValue]
    public var optional: Bool
    public var continueOnFail: Bool
    public var status: String
    public var startedAt: Timestamp?
    public var durationMs: Int?
    public var message: String?
    public var error: QAErrorInfo?
    public var assertions: [QAAssertion]
    /// Per-kind result fields with the daemon's key spelling.
    public var result: [String: JSONValue]?
    public var evidence: [Artifact]

    public var id: Int { index }
    public var stepStatus: QAStepStatus? { QAStepStatus(rawValue: status) }
    public var stepPhase: QAStepPhase? { QAStepPhase(rawValue: phase) }

    public init(index: Int, phase: QAStepPhase, name: String, action: String, line: Int = 0, params: [String: JSONValue] = [:],
                optional: Bool = false, continueOnFail: Bool = false, status: QAStepStatus = .pending, startedAt: Timestamp? = nil,
                durationMs: Int? = nil, message: String? = nil, error: QAErrorInfo? = nil, assertions: [QAAssertion] = [],
                result: [String: JSONValue]? = nil, evidence: [Artifact] = []) {
        self.index = index
        self.phase = phase.rawValue
        self.name = name
        self.action = action
        self.line = line
        self.params = params
        self.optional = optional
        self.continueOnFail = continueOnFail
        self.status = status.rawValue
        self.startedAt = startedAt
        self.durationMs = durationMs
        self.message = message
        self.error = error
        self.assertions = assertions
        self.result = result
        self.evidence = evidence
    }

    private enum CodingKeys: String, CodingKey {
        case index, phase, name, action, line, params, optional, continueOnFail, status, startedAt, durationMs, message, error,
             assertions, result, evidence
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        index = try c.decode(Int.self, forKey: .index)
        phase = try c.decodeIfPresent(String.self, forKey: .phase) ?? QAStepPhase.steps.rawValue
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        action = try c.decodeIfPresent(String.self, forKey: .action) ?? ""
        line = try c.decodeIfPresent(Int.self, forKey: .line) ?? 0
        params = (try? c.decodeIfPresent([String: JSONValue].self, forKey: .params)) ?? [:]
        optional = try c.decodeIfPresent(Bool.self, forKey: .optional) ?? false
        continueOnFail = try c.decodeIfPresent(Bool.self, forKey: .continueOnFail) ?? false
        status = try c.decode(String.self, forKey: .status)
        startedAt = try c.decodeIfPresent(Timestamp.self, forKey: .startedAt)
        durationMs = try c.decodeIfPresent(Int.self, forKey: .durationMs)
        message = try c.decodeIfPresent(String.self, forKey: .message)
        error = try c.decodeIfPresent(QAErrorInfo.self, forKey: .error)
        assertions = try c.decodeIfPresent([QAAssertion].self, forKey: .assertions) ?? []
        result = try? c.decodeIfPresent([String: JSONValue].self, forKey: .result)
        evidence = try c.decodeIfPresent([Artifact].self, forKey: .evidence) ?? []
    }
}

/// A step kind the computer or the daemon could not do (`unsupported[]`), once per kind.
public struct QAUnsupported: Codable, Sendable, Equatable, Hashable {
    public var action: String
    public var code: String
    public var reason: String

    public init(action: String, code: String, reason: String) {
        self.action = action
        self.code = code
        self.reason = reason
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        action = try c.decodeIfPresent(String.self, forKey: AnyKey("action")) ?? ""
        code = try c.decodeIfPresent(String.self, forKey: AnyKey("code")) ?? ""
        reason = try c.decodeIfPresent(String.self, forKey: AnyKey("reason")) ?? ""
    }
}

/// Absolute paths of the files of a result bundle (`report`).
public struct QAReportPaths: Codable, Sendable, Equatable {
    public var json: String
    public var md: String
    public var html: String
    public var manifest: String

    public init(json: String, md: String, html: String, manifest: String) {
        self.json = json
        self.md = md
        self.html = html
        self.manifest = manifest
    }

    /// The bundle's files under `dir`.
    public init(dir: String) {
        let base = dir.hasSuffix("/") ? dir : dir + "/"
        self.init(json: base + "report.json", md: base + "report.md", html: base + "report.html", manifest: base + "manifest.json")
    }
}

/// A run of a QA scenario (`qa.run`, `qa.get`, `qa.list`, `qa.cancel`, `qa.updated`).
public struct QARun: Codable, Sendable, Equatable, Identifiable {
    public var runID: String
    public var jobID: String
    public var hostID: String
    public var name: String
    /// Absolute path of the scenario file on this Mac; nil for a scenario sent as text.
    public var scenarioPath: String?
    public var status: String
    /// Rises by one on every change of the run; only a larger rev replaces what a client knows.
    public var rev: Int
    public var startedAt: Timestamp
    public var updatedAt: Timestamp
    public var finishedAt: Timestamp?
    public var stepsTotal: Int
    public var stepsPassed: Int
    public var stepsFailed: Int
    public var stepsBlocked: Int
    public var stepsNotRun: Int
    /// `index` of the step that is running.
    public var currentStep: Int?
    public var cancelRequested: Bool
    /// What stopped the whole run: `TIMEOUT`, or `EXECUTION_OUTCOME_UNKNOWN` after the daemon restarted.
    public var error: QAErrorInfo?
    public var unsupported: [QAUnsupported]
    public var reportDir: String?
    public var report: QAReportPaths?
    /// Present in `qa.run`, `qa.get` and `qa.cancel`; nil in `qa.list` and `qa.updated`.
    public var steps: [QAStep]?

    public var id: String { runID }
    public var runStatus: QARunStatus? { QARunStatus(rawValue: status) }
    public var isRunning: Bool { runStatus == .running }

    public init(runID: String, jobID: String, hostID: String, name: String, scenarioPath: String? = nil, status: QARunStatus, rev: Int,
                startedAt: Timestamp, updatedAt: Timestamp? = nil, finishedAt: Timestamp? = nil, stepsTotal: Int = 0, stepsPassed: Int = 0,
                stepsFailed: Int = 0, stepsBlocked: Int = 0, stepsNotRun: Int = 0, currentStep: Int? = nil, cancelRequested: Bool = false,
                error: QAErrorInfo? = nil, unsupported: [QAUnsupported] = [], reportDir: String? = nil, report: QAReportPaths? = nil,
                steps: [QAStep]? = nil) {
        self.runID = runID
        self.jobID = jobID
        self.hostID = hostID
        self.name = name
        self.scenarioPath = scenarioPath
        self.status = status.rawValue
        self.rev = rev
        self.startedAt = startedAt
        self.updatedAt = updatedAt ?? startedAt
        self.finishedAt = finishedAt
        self.stepsTotal = stepsTotal
        self.stepsPassed = stepsPassed
        self.stepsFailed = stepsFailed
        self.stepsBlocked = stepsBlocked
        self.stepsNotRun = stepsNotRun
        self.currentStep = currentStep
        self.cancelRequested = cancelRequested
        self.error = error
        self.unsupported = unsupported
        self.reportDir = reportDir
        self.report = report
        self.steps = steps
    }

    private enum CodingKeys: String, CodingKey {
        case runID = "runId"
        case jobID = "jobId"
        case hostID = "hostId"
        case name, scenarioPath, status, rev, startedAt, updatedAt, finishedAt, stepsTotal, stepsPassed, stepsFailed, stepsBlocked,
             stepsNotRun, currentStep, cancelRequested, error, unsupported, reportDir, report, steps
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runID = try c.decode(String.self, forKey: .runID)
        jobID = try c.decodeIfPresent(String.self, forKey: .jobID) ?? ""
        hostID = try c.decode(String.self, forKey: .hostID)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        scenarioPath = try c.decodeIfPresent(String.self, forKey: .scenarioPath).flatMap { $0.isEmpty ? nil : $0 }
        status = try c.decode(String.self, forKey: .status)
        rev = try c.decodeIfPresent(Int.self, forKey: .rev) ?? 0
        startedAt = try c.decode(Timestamp.self, forKey: .startedAt)
        updatedAt = try c.decodeIfPresent(Timestamp.self, forKey: .updatedAt) ?? startedAt
        finishedAt = try c.decodeIfPresent(Timestamp.self, forKey: .finishedAt)
        stepsTotal = try c.decodeIfPresent(Int.self, forKey: .stepsTotal) ?? 0
        stepsPassed = try c.decodeIfPresent(Int.self, forKey: .stepsPassed) ?? 0
        stepsFailed = try c.decodeIfPresent(Int.self, forKey: .stepsFailed) ?? 0
        stepsBlocked = try c.decodeIfPresent(Int.self, forKey: .stepsBlocked) ?? 0
        stepsNotRun = try c.decodeIfPresent(Int.self, forKey: .stepsNotRun) ?? 0
        currentStep = try c.decodeIfPresent(Int.self, forKey: .currentStep)
        cancelRequested = try c.decodeIfPresent(Bool.self, forKey: .cancelRequested) ?? false
        error = try c.decodeIfPresent(QAErrorInfo.self, forKey: .error)
        unsupported = try c.decodeIfPresent([QAUnsupported].self, forKey: .unsupported) ?? []
        reportDir = try c.decodeIfPresent(String.self, forKey: .reportDir).flatMap { $0.isEmpty ? nil : $0 }
        report = try c.decodeIfPresent(QAReportPaths.self, forKey: .report)
        steps = try c.decodeIfPresent([QAStep].self, forKey: .steps)
    }

    /// The run without its steps, as `qa.list` and `qa.updated` carry it.
    public var summary: QARun {
        var copy = self
        copy.steps = nil
        return copy
    }
}

/// One page of `qa.list`, newest first.
public struct QARunPage: Codable, Sendable, Equatable {
    public var runs: [QARun]
    /// The cursor of the next page when there is one.
    public var nextCursor: String?

    public init(runs: [QARun], nextCursor: String? = nil) {
        self.runs = runs
        self.nextCursor = nextCursor
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        runs = try c.decodeIfPresent([QARun].self, forKey: AnyKey("runs")) ?? []
        nextCursor = try c.decodeIfPresent(String.self, forKey: AnyKey("nextCursor")).flatMap { $0.isEmpty ? nil : $0 }
    }
}

/// Result of `artifacts.export`: the folder it made, how many files and bytes it copied, and the
/// copied `manifest.json` when the bundle has one.
public struct ArtifactExport: Codable, Sendable, Equatable {
    public var jobID: String
    public var dir: String
    public var files: Int
    public var bytes: Int
    public var manifest: String?

    public init(jobID: String, dir: String, files: Int, bytes: Int, manifest: String? = nil) {
        self.jobID = jobID
        self.dir = dir
        self.files = files
        self.bytes = bytes
        self.manifest = manifest
    }

    private enum CodingKeys: String, CodingKey {
        case jobID = "jobId"
        case dir, files, bytes, manifest
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        jobID = try c.decodeIfPresent(String.self, forKey: .jobID) ?? ""
        dir = try c.decode(String.self, forKey: .dir)
        files = try c.decodeIfPresent(Int.self, forKey: .files) ?? 0
        bytes = try c.decodeIfPresent(Int.self, forKey: .bytes) ?? 0
        manifest = try c.decodeIfPresent(String.self, forKey: .manifest)
    }
}

extension Artifact: Identifiable {
    public var id: String { artifactID }
}
