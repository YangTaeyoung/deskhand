import DeskhandClient
import Foundation

/// What became of a run or step handed to `QARunRecord`.
public enum QAApplyResult: Equatable, Sendable {
    case ignored
    case applied
    /// Applied, and the rev skipped values: events in between were missed.
    case appliedWithGap
}

/// What the app knows of one run: its summary at `run.rev`, and its steps by `index`, each kept
/// with the rev it was last set at. A summary is replaced only by a higher rev (local-api.md §10);
/// a step from an event is replaced only by a later event or a read of at least its rev.
public struct QARunRecord: Equatable, Sendable {
    private struct Entry: Equatable, Sendable {
        var step: QAStep
        var rev: Int
    }

    /// The summary, without steps.
    public private(set) var run: QARun
    private var entries: [Int: Entry] = [:]
    /// A read listed every step.
    private var listed = false

    public init(_ run: QARun) {
        self.run = run.summary
        if let steps = run.steps { take(steps, rev: run.rev) }
    }

    /// Every step in index order once a read has listed them, otherwise nil.
    public var steps: [QAStep]? {
        guard listed else { return nil }
        return entries.values.map(\.step).sorted { $0.index < $1.index }
    }

    public func step(_ index: Int) -> QAStep? {
        listed ? entries[index]?.step : nil
    }

    /// The phase of the running step, when the steps are known.
    public var currentPhase: QAStepPhase? {
        guard let index = run.currentStep else { return nil }
        return entries[index]?.step.stepPhase
    }

    /// A read with steps (`qa.run`, `qa.get`, `qa.cancel`): the summary when its rev is higher,
    /// and every step that no later event set.
    @discardableResult
    public mutating func apply(read: QARun) -> QAApplyResult {
        var changed = false
        if read.rev > run.rev {
            run = read.summary
            changed = true
        }
        if let steps = read.steps {
            let before = entries
            let wasListed = listed
            take(steps, rev: read.rev)
            changed = changed || before != entries || !wasListed
        }
        return changed ? .applied : .ignored
    }

    /// A run of `qa.list`: the summary when its rev is higher; the steps stay.
    @discardableResult
    public mutating func apply(listed incoming: QARun) -> QAApplyResult {
        guard incoming.rev > run.rev else { return .ignored }
        run = incoming.summary
        return .applied
    }

    /// A `qa.updated` event: the run and its changed step when the rev is higher.
    @discardableResult
    public mutating func apply(event incoming: QARun, step: QAStep?) -> QAApplyResult {
        guard incoming.rev > run.rev else { return .ignored }
        let gap = incoming.rev > run.rev + 1
        run = incoming.summary
        if let step {
            entries[step.index] = Entry(step: step, rev: incoming.rev)
        }
        return gap ? .appliedWithGap : .applied
    }

    private mutating func take(_ steps: [QAStep], rev: Int) {
        for step in steps {
            if let entry = entries[step.index], entry.rev > rev { continue }
            entries[step.index] = Entry(step: step, rev: rev)
        }
        listed = true
    }
}

/// The cancels this app sent for a run.
public struct QACancelProgress: Equatable, Sendable {
    public var firstSent = false
    /// The cancel sent while teardown ran, which stops it too.
    public var secondSent = false
    public var inFlight = false

    public init(firstSent: Bool = false, secondSent: Bool = false, inFlight: Bool = false) {
        self.firstSent = firstSent
        self.secondSent = secondSent
        self.inFlight = inFlight
    }
}

/// The cancel button of a run (local-api.md §10 취소): the first cancel stops the running setup or
/// steps step and lets teardown run; a cancel sent while teardown runs stops teardown too.
public enum QACancelState: Equatable, Sendable {
    /// The run has ended.
    case hidden
    case available
    /// The first cancel is on its way.
    case sending
    /// The daemon took the cancel; the running step stops and teardown follows.
    case stopping
    /// Teardown runs after a cancel; another cancel stops it.
    case teardownStoppable
    case sendingTeardownStop
    case stoppingTeardown

    public init(run: QARun, phase: QAStepPhase?, progress: QACancelProgress) {
        guard run.isRunning else {
            self = .hidden
            return
        }
        if progress.secondSent {
            self = progress.inFlight ? .sendingTeardownStop : .stoppingTeardown
        } else if !run.cancelRequested {
            if progress.inFlight { self = .sending } else { self = progress.firstSent ? .stopping : .available }
        } else if phase == .teardown {
            self = progress.inFlight ? .sending : .teardownStoppable
        } else {
            self = .stopping
        }
    }

    /// Pressing the button sends `qa.cancel`.
    public var canSend: Bool { self == .available || self == .teardownStoppable }
}

/// One variable row of the run sheet.
public struct QAVarRow: Identifiable, Equatable, Sendable, Codable {
    public var id = UUID()
    public var name: String
    public var value: String

    public init(id: UUID = UUID(), name: String = "", value: String = "") {
        self.id = id
        self.name = name
        self.value = value
    }

    public var trimmedName: String { name.trimmingCharacters(in: .whitespaces) }

    /// The value is a secret: hidden in the field and replaced by `***` in results.
    public var isSecret: Bool { trimmedName.hasPrefix(QAVarsDraft.secretPrefix) }
}

/// The variables of the run sheet: rows with blank names are left out; names follow
/// `[A-Za-z_][A-Za-z0-9_]*` and appear once, and a secret needs a value.
public struct QAVarsDraft: Equatable, Sendable {
    public enum Issue: Equatable, Sendable {
        case invalidName
        case repeated
        /// A secret has no value; secret values are not kept between runs.
        case missingSecret
    }

    public static let secretPrefix = "secret_"
    public var rows: [QAVarRow] = []

    public init(rows: [QAVarRow] = []) {
        self.rows = rows
    }

    public static func isValidName(_ name: String) -> Bool {
        guard let first = name.unicodeScalars.first, first.isASCII, first == "_" || CharacterSet.letters.contains(first) else { return false }
        return name.unicodeScalars.allSatisfy { $0.isASCII && ($0 == "_" || CharacterSet.alphanumerics.contains($0)) }
    }

    public func issue(for row: QAVarRow) -> Issue? {
        let name = row.trimmedName
        guard !name.isEmpty else { return nil }
        guard Self.isValidName(name) else { return .invalidName }
        let first = rows.first { $0.trimmedName == name }
        guard first?.id == row.id else { return .repeated }
        return row.isSecret && row.value.isEmpty ? .missingSecret : nil
    }

    public var hasIssues: Bool { rows.contains { issue(for: $0) != nil } }

    /// `vars` of `qa.run`, or nil while a row has an issue.
    public func vars() -> [String: String]? {
        guard !hasIssues else { return nil }
        var out: [String: String] = [:]
        for row in rows where !row.trimmedName.isEmpty { out[row.trimmedName] = row.value }
        return out
    }

    /// The rows to offer again next time: secret values are dropped.
    public func remembered() -> [QAVarRow] {
        rows.filter { !$0.trimmedName.isEmpty }.map { row in
            QAVarRow(name: row.trimmedName, value: row.isSecret ? "" : row.value)
        }
    }

    /// The value as the sheet shows it outside its field.
    public func display(_ row: QAVarRow) -> String {
        row.isSecret ? "***" : row.value
    }
}

/// A scenario problem of `qa.run`'s `INVALID_ARGUMENT`: `<file>:<line>: <sentence>`.
public struct QAScenarioProblem: Equatable, Sendable {
    public var file: String
    public var line: Int
    public var message: String

    public init(file: String, line: Int, message: String) {
        self.file = file
        self.line = line
        self.message = message
    }

    /// Splits `message`; the file is `path` when the message starts with it.
    public init?(message: String, path: String?) {
        if let path, message.hasPrefix(path + ":"), let parsed = Self.split(String(message.dropFirst(path.count + 1))) {
            self.init(file: path, line: parsed.line, message: parsed.message)
            return
        }
        guard let colon = message.firstRange(of: #/:(\d+):\s/#),
              let line = Int(message[colon].dropFirst().prefix { $0.isNumber }) else { return nil }
        let file = String(message[..<colon.lowerBound])
        guard !file.isEmpty, !file.contains(" ") || file.hasPrefix("/") else { return nil }
        self.init(file: file, line: line, message: String(message[colon.upperBound...]))
    }

    /// `14: 문장` → (14, 문장).
    private static func split(_ rest: String) -> (line: Int, message: String)? {
        let digits = rest.prefix { $0.isNumber }
        guard let line = Int(digits), rest.dropFirst(digits.count).hasPrefix(": ") else { return nil }
        return (line, String(rest.dropFirst(digits.count + 2)))
    }
}
