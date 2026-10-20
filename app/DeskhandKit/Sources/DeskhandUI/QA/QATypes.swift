import AppKit
import DeskhandClient
import Foundation

/// The runs the list shows: one computer's, or every computer's.
public enum QAScope: Hashable, Sendable {
    case host(String)
    case all

    var hostID: String? {
        if case .host(let id) = self { return id }
        return nil
    }
}

/// The status filter of the run list.
public enum QARunFilter: String, CaseIterable, Identifiable, Sendable {
    case all
    case running
    /// Failed runs and runs that ended in an error.
    case failed
    case blocked
    case passed
    case cancelled

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .all: .ui("qa.filter.all", "모든 상태")
        case .running: .ui("qa.filter.running", "실행 중")
        case .failed: .ui("qa.filter.failed", "실패·오류")
        case .blocked: .ui("qa.filter.blocked", "차단")
        case .passed: .ui("qa.filter.passed", "통과")
        case .cancelled: .ui("qa.filter.cancelled", "취소됨")
        }
    }

    func matches(_ status: QARunStatus?) -> Bool {
        switch self {
        case .all: true
        case .running: status == .running
        case .failed: status == .failed || status == .error
        case .blocked: status == .blocked
        case .passed: status == .passed
        case .cancelled: status == .cancelled
        }
    }
}

/// The limit of setup and steps a run sheet asks for.
public enum QATimeout: Hashable, Sendable, Codable {
    /// The scenario's `timeout_ms`, or none.
    case scenario
    case minutes(Int)

    public static let choices: [QATimeout] = [.scenario, .minutes(1), .minutes(5), .minutes(10), .minutes(30), .minutes(60)]

    public var milliseconds: Int? {
        if case .minutes(let minutes) = self { return minutes * 60_000 }
        return nil
    }

    public var title: String {
        switch self {
        case .scenario: return .ui("qa.timeout.scenario", "시나리오 값 사용")
        case .minutes(let minutes):
            if minutes >= 60, minutes % 60 == 0 {
                let hours = String(minutes / 60)
                return .ui("qa.timeout.hours", "\(hours)시간")
            }
            let count = String(minutes)
            return .ui("qa.timeout.minutes", "\(count)분")
        }
    }
}

/// What the run sheet starts: a scenario file, the computer, variables and a time limit.
public struct QARunDraft: Equatable, Sendable, Identifiable {
    public var scenarioPath: String
    public var hostID: String
    public var vars: QAVarsDraft
    public var timeout: QATimeout

    public var id: String { "run-" + hostID + "-" + scenarioPath }

    public init(scenarioPath: String = "", hostID: String, vars: QAVarsDraft = QAVarsDraft(), timeout: QATimeout = .scenario) {
        self.scenarioPath = scenarioPath
        self.hostID = hostID
        self.vars = vars
        self.timeout = timeout
    }

    /// The request, or nil while the path is empty or a variable has an issue.
    public var request: QARunRequest? {
        let path = scenarioPath.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !path.isEmpty, let vars = vars.vars() else { return nil }
        return QARunRequest(hostID: hostID, scenarioPath: path, vars: vars, timeoutMS: timeout.milliseconds)
    }
}

/// A scenario run from this Mac before, offered again in the run sheet. Secret values are not kept.
public struct QARecentScenario: Codable, Equatable, Sendable, Identifiable {
    public var path: String
    public var hostID: String?
    public var vars: [QAVarRow]
    public var timeout: QATimeout

    public var id: String { path }

    public init(path: String, hostID: String? = nil, vars: [QAVarRow] = [], timeout: QATimeout = .scenario) {
        self.path = path
        self.hostID = hostID
        self.vars = vars
        self.timeout = timeout
    }
}

/// An export of a run's result bundle.
public enum QAExportState: Equatable, Sendable {
    case exporting(dir: String)
    case exported(ArtifactExport)
    case failed(OperationError, dir: String)
}

/// The evidence file open in the viewer.
public struct QAEvidenceSelection: Equatable, Sendable, Identifiable {
    public var runID: String
    public var stepIndex: Int
    public var artifactID: String

    public var id: String { runID + "/" + String(stepIndex) + "/" + artifactID }

    public init(runID: String, stepIndex: Int, artifactID: String) {
        self.runID = runID
        self.stepIndex = stepIndex
        self.artifactID = artifactID
    }
}

/// An evidence file as the viewer has read it.
public enum QAEvidenceContent: Equatable {
    case loading
    case image(NSImage)
    /// Text with JSON laid out one member per line; `truncated` when the file was longer than read.
    case text(String, truncated: Bool)
    case failed(OperationError)

    public static func == (lhs: QAEvidenceContent, rhs: QAEvidenceContent) -> Bool {
        switch (lhs, rhs) {
        case (.loading, .loading): true
        case (.image(let a), .image(let b)): a === b
        case (.text(let a, let ta), .text(let b, let tb)): a == b && ta == tb
        case (.failed(let a), .failed(let b)): a == b
        default: false
        }
    }
}
