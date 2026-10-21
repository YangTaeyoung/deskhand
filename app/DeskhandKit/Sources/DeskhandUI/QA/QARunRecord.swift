import DeskhandClient
import Foundation

/// What became of a run or step handed to `QARunRecord`.
public enum QAApplyResult: Equatable, Sendable {
    case ignored
    case applied
    /// Applied, and the rev skipped values: events in between were missed.
    case appliedWithGap
}

/// What the app knows of one run: its summary at `run.rev`, and its steps in index order, each
/// kept with the rev of the event or read that last set it. A summary is replaced only by a higher
/// rev (local-api.md §10). A step is replaced by an event or a read whose rev is higher than its
/// own, whatever rev a listing moved the summary to. Gaps are measured against the revs of events
/// and reads.
public struct QARunRecord: Equatable, Sendable {
    /// The summary, without steps.
    public private(set) var run: QARun
    /// Steps in index order, and the rev that set each.
    private var ordered: [QAStep] = []
    private var revs: [Int] = []
    /// Position in `ordered` of each step index.
    private var positions: [Int: Int] = [:]
    /// A read listed every step.
    private var listed = false
    /// The highest rev of an event or read applied.
    private var seenRev: Int

    public init(_ run: QARun) {
        self.run = run.summary
        seenRev = run.rev
        if let steps = run.steps { take(steps, rev: run.rev) }
    }

    /// A run first heard of from a `qa.updated` event.
    public init(event run: QARun, step: QAStep?) {
        var base = run.summary
        base.rev = run.rev - 1
        self.init(base)
        apply(event: run, step: step)
    }

    /// Every step in index order once a read has listed them, otherwise nil.
    public var steps: [QAStep]? {
        listed ? ordered : nil
    }

    public func step(_ index: Int) -> QAStep? {
        guard listed, let position = positions[index] else { return nil }
        return ordered[position]
    }

    /// The phase of the running step, when the steps are known.
    public var currentPhase: QAStepPhase? {
        guard let index = run.currentStep, let position = positions[index] else { return nil }
        return ordered[position].stepPhase
    }

    /// Teardown has begun: the running step is a teardown step, or a teardown step has left
    /// `pending`. It stays true between teardown steps.
    public var inTeardown: Bool {
        if currentPhase == .teardown { return true }
        return ordered.contains { $0.stepPhase == .teardown && $0.stepStatus != .pending }
    }

    /// A read with steps (`qa.run`, `qa.get`, `qa.cancel`): the summary when its rev is higher,
    /// and every step whose own rev is not higher than the read's.
    @discardableResult
    public mutating func apply(read: QARun) -> QAApplyResult {
        var changed = false
        if read.rev > run.rev {
            run = read.summary
            changed = true
        }
        seenRev = max(seenRev, read.rev)
        if let steps = read.steps {
            let wasListed = listed
            changed = take(steps, rev: read.rev) || changed || !wasListed
        }
        return changed ? .applied : .ignored
    }

    /// A run of `qa.list`: the summary when its rev is higher. The steps and the revs that gate
    /// step events stay.
    @discardableResult
    public mutating func apply(listed incoming: QARun) -> QAApplyResult {
        guard incoming.rev > run.rev else { return .ignored }
        run = incoming.summary
        return .applied
    }

    /// A `qa.updated` event: the run when its rev is higher than the summary's, and its step when
    /// the rev is higher than the step's own.
    @discardableResult
    public mutating func apply(event incoming: QARun, step: QAStep?) -> QAApplyResult {
        var changed = false
        if let step, set(step, rev: incoming.rev) { changed = true }
        if incoming.rev > run.rev {
            run = incoming.summary
            changed = true
        }
        guard changed else { return .ignored }
        let gap = incoming.rev > seenRev + 1
        seenRev = max(seenRev, incoming.rev)
        return gap ? .appliedWithGap : .applied
    }

    /// Sets each step whose own rev is not higher than `rev`; returns whether anything changed.
    @discardableResult
    private mutating func take(_ steps: [QAStep], rev: Int) -> Bool {
        var changed = false
        for step in steps where set(step, rev: rev, orEqual: true) { changed = true }
        listed = true
        return changed
    }

    /// Puts `step` in place when `rev` is higher than its own (or equal with `orEqual`); returns
    /// whether the step changed.
    private mutating func set(_ step: QAStep, rev: Int, orEqual: Bool = false) -> Bool {
        if let position = positions[step.index] {
            guard rev > revs[position] || (orEqual && rev == revs[position]) else { return false }
            revs[position] = rev
            guard ordered[position] != step else { return false }
            ordered[position] = step
            return true
        }
        var low = 0
        var high = ordered.count
        while low < high {
            let mid = (low + high) / 2
            if ordered[mid].index < step.index { low = mid + 1 } else { high = mid }
        }
        ordered.insert(step, at: low)
        revs.insert(rev, at: low)
        if low == ordered.count - 1 {
            positions[step.index] = low
        } else {
            for position in low..<ordered.count { positions[ordered[position].index] = position }
        }
        return true
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
