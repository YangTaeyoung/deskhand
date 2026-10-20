import DeskhandClient
import Foundation
import Observation

/// What the debugger tab shows for the selected session.
public enum DebuggerPhase: Equatable, Sendable {
    /// No session yet.
    case idle
    /// A launch or attach of the named app is in flight.
    case starting(String)
    case running
    case paused(DebugPauseReason)
    case closed(DebugCloseReason?)
}

/// A script location the source panel shows.
public struct SourceFocus: Hashable, Sendable {
    public var targetID: String
    public var scriptID: String
    public var url: String
    public var line: Int
    public var column: Int?

    public init(targetID: String, scriptID: String, url: String, line: Int, column: Int? = nil) {
        self.targetID = targetID
        self.scriptID = scriptID
        self.url = url
        self.line = line
        self.column = column
    }
}

/// A script's source as read from the daemon, split into lines once.
public struct LoadedSource: Equatable, Sendable {
    public var source: DebugSource
    public var text: SourceText

    public init(_ source: DebugSource) {
        self.source = source
        text = SourceText(text: source.text)
    }

    public static func == (a: LoadedSource, b: LoadedSource) -> Bool {
        a.source.scriptID == b.source.scriptID && a.source.url == b.source.url && a.text === b.text
    }
}

/// Timings of a session model: how long console events gather before they show, and the waits
/// between attempts to read a pause state.
public struct DebugTimings: Sendable {
    public var consoleFrame: Duration
    public var stateRetries: [Duration]

    public init(consoleFrame: Duration, stateRetries: [Duration]) {
        self.consoleFrame = consoleFrame
        self.stateRetries = stateRetries
    }

    public static let standard = DebugTimings(consoleFrame: .milliseconds(16),
                                              stateRetries: [.milliseconds(500), .seconds(1), .seconds(2)])
}

/// One debugging session as the tab sees it: its targets and their pauses ordered by `gen`, its
/// fields and breakpoints ordered by `rev`, the selected target and call frame, the scopes of
/// that frame with lazily loaded properties, the script sources it read, and its console with
/// evaluations, their objects and their history. Console events gather for one display frame.
@MainActor
@Observable
public final class DebugSessionModel: Identifiable {
    public enum Command: Equatable, Sendable {
        case pause
        case resume
        case step(DebugStepKind)
        case close
    }

    public enum SourceState: Equatable, Sendable {
        case loading
        case loaded(LoadedSource)
        case failed(OperationError)
    }

    public let id: String
    public internal(set) var info: DebugSession
    public internal(set) var selectedTargetID: String?
    public internal(set) var pauses = PauseBook()
    public internal(set) var selectedFrameIndex = 0
    public internal(set) var variables = VariableTree()
    /// Objects of evaluation results, one root per result.
    public internal(set) var consoleObjects = VariableTree()
    public internal(set) var console = ConsoleLog()
    public var consoleFilter = ConsoleFilter()
    public var history = EvaluateHistory()
    /// The expression being typed in the console.
    public var draft = ""
    /// Console entries whose stack is shown.
    public var expandedEntries: Set<Int> = []
    /// Selected console rows, by item id.
    public var consoleSelection: Set<String> = []
    /// The selected variable row.
    public var selectedVariable: VariableTree.Row.ID?
    /// The selected breakpoint.
    public var selectedBreakpointID: String?
    public internal(set) var sources: [String: SourceState] = [:]
    public internal(set) var scripts: [DebugScript] = []
    public internal(set) var scriptsError: OperationError?
    /// The execution command in flight.
    public internal(set) var command: Command?
    public internal(set) var consolePhase: LoadPhase = .idle
    public internal(set) var isLoadingEarlier = false
    public internal(set) var busyBreakpoints: Set<String> = []
    /// `url:line` of gutter clicks whose breakpoint change is in flight.
    public internal(set) var pendingLines: Set<String> = []
    public internal(set) var notice: DiagnosticsNotice?
    /// A location the person opened from the breakpoints or the console; a new pause replaces it.
    public internal(set) var focus: SourceFocus?
    /// Why the call stack of an announced pause could not be read, per target.
    public internal(set) var stateFailures: [String: OperationError] = [:]

    @ObservationIgnored let backend: any DebuggerBackend
    @ObservationIgnored let timings: DebugTimings
    @ObservationIgnored var noticeTask: Task<Void, Never>?
    @ObservationIgnored var fetchingStates: Set<String> = []
    /// Breakpoints and targets removed, with the `rev` they were removed at.
    @ObservationIgnored var removedBreakpoints: [String: Int] = [:]
    @ObservationIgnored var removedTargets: [String: Int] = [:]
    /// A counter that rises on every local breakpoint change, and the value per breakpoint.
    @ObservationIgnored var changeClock = 0
    @ObservationIgnored var breakpointChanged: [String: Int] = [:]
    @ObservationIgnored var pendingEntries: [ConsoleEntry] = []
    @ObservationIgnored var flushTask: Task<Void, Never>?
    /// Console flushes so far.
    @ObservationIgnored var consoleFlushes = 0
    /// Rise when the variables tree or the console objects are replaced; replies for an older
    /// tree are dropped.
    @ObservationIgnored var variablesEpoch = 0
    @ObservationIgnored var consoleEpoch = 0

    init(info: DebugSession, backend: any DebuggerBackend, timings: DebugTimings = .standard) {
        id = info.debugID
        self.info = info
        self.backend = backend
        self.timings = timings
        selectedTargetID = Self.defaultTarget(info.targets)
    }

    /// Reads the pauses its targets report and the console.
    func begin() async {
        for target in info.targets { applyPause(of: target) }
        await loadConsole()
    }

    /// The renderer page when there is one, otherwise the first target.
    static func defaultTarget(_ targets: [DebugTarget]) -> String? {
        (targets.first { $0.targetRole == .renderer && $0.type == "page" } ?? targets.first)?.id
    }

    // MARK: Queries

    public var isClosed: Bool { info.sessionState == .closed }

    public var selectedTarget: DebugTarget? { info.targets.first { $0.id == selectedTargetID } }

    /// The selected target's pause, while it is paused.
    public var pauseState: DebugPauseState? {
        guard let id = selectedTargetID, let state = pauses.state(for: id), state.paused else { return nil }
        return state
    }

    public var selectedFrame: DebugCallFrame? {
        guard let frames = pauseState?.callFrames, frames.indices.contains(selectedFrameIndex) else { return nil }
        return frames[selectedFrameIndex]
    }

    public var phase: DebuggerPhase {
        if isClosed { return .closed(info.closeKind) }
        if let state = pauseState { return .paused(state.pauseReason ?? .other) }
        return .running
    }

    /// Execution commands apply: the session is open and the selected target is attached.
    public var canControl: Bool { !isClosed && (selectedTarget?.attached ?? false) }

    /// The target is paused, its call stack read or not.
    public func isPaused(_ targetID: String) -> Bool { pauses.isPaused(targetID) || pauses.announcedGen(of: targetID) != nil }

    /// The selected target reported a pause whose call stack is still being read or failed to read.
    public var awaitingState: Bool {
        guard let id = selectedTargetID else { return false }
        return pauses.announcedGen(of: id) != nil
    }

    /// Why the call stack of the selected target's pause could not be read.
    public var stateFailure: OperationError? {
        guard awaitingState, let id = selectedTargetID else { return nil }
        return stateFailures[id]
    }

    /// The selected target is paused, its call stack read or not.
    public var isSelectedPaused: Bool { pauseState != nil || awaitingState }

    /// Targets that are paused, their call stacks read or not.
    public var pausedTargets: [DebugTarget] { info.targets.filter { isPaused($0.id) } }

    /// A pause was asked for on the selected target and waits for it to run code.
    public var pausePending: Bool { !isSelectedPaused && (selectedTarget?.pausePending ?? false) }

    public func sourceState(targetID: String, scriptID: String) -> SourceState? {
        sources[Self.sourceKey(targetID, scriptID)]
    }

    /// The location the source panel shows: the opened location, else the selected frame.
    public var shownLocation: SourceFocus? {
        if let focus { return focus }
        guard let frame = selectedFrame, let targetID = selectedTargetID else { return nil }
        return SourceFocus(targetID: targetID, scriptID: frame.scriptID, url: frame.url, line: frame.line, column: frame.column)
    }

    /// Breakpoints bound at `url`:`line`, or set for it and not bound anywhere yet.
    public func breakpoints(url: String, line: Int) -> [DebugBreakpoint] {
        info.breakpoints.filter { bp in
            bp.locations.contains { $0.url == url && $0.line == line }
                || (bp.locations.isEmpty && bp.line == line && BreakpointPattern.matches(bp, url: url))
        }
    }

    static func sourceKey(_ targetID: String, _ scriptID: String) -> String { targetID + "|" + scriptID }

    // MARK: Session updates

    /// Applies the session as `debug.list`, `debug.get`, a reply or `debug.session_changed`
    /// reports it: its own fields and target set when its `rev` is newer, each breakpoint by its
    /// `rev`. An `authoritative` list also drops breakpoints it no longer has that did not change
    /// since `since`.
    func apply(info newInfo: DebugSession, authoritative: Bool = false, since: Int = .max) {
        if newInfo.rev > info.rev || newInfo.rev == 0 {
            let wasClosed = isClosed
            var merged = newInfo
            merged.breakpoints = info.breakpoints
            merged.targets = newInfo.targets.filter { target in (removedTargets[target.id] ?? -1) < newInfo.rev }
                .map { incoming in mergedTarget(incoming) }
            info = merged
            if wasClosed { info.state = DebugSessionState.closed.rawValue }
            if selectedTargetID.map({ id in !info.targets.contains { $0.id == id } }) ?? true {
                selectedTargetID = Self.defaultTarget(info.targets)
            }
            for target in info.targets { applyPause(of: target) }
        }
        for breakpoint in newInfo.breakpoints { apply(breakpoint: breakpoint, removed: false) }
        if authoritative {
            let listed = Set(newInfo.breakpoints.map(\.breakpointID))
            info.breakpoints.removeAll { bp in !listed.contains(bp.breakpointID) && (breakpointChanged[bp.breakpointID] ?? 0) < since }
        }
    }

    /// `incoming` with the pause fields of the known target when `incoming` is older.
    private func mergedTarget(_ incoming: DebugTarget) -> DebugTarget {
        guard let known = info.targets.first(where: { $0.id == incoming.id }), incoming.gen < known.gen else { return incoming }
        var target = incoming
        target.paused = known.paused
        target.pausePending = known.pausePending
        target.gen = known.gen
        return target
    }

    /// Applies one target from an event or reply: its fields always, its pause fields by `gen`.
    func apply(target: DebugTarget, removed: Bool = false, rev: Int? = nil) {
        if let rev { info.rev = max(info.rev, rev) }
        if removed {
            removedTargets[target.id] = rev ?? info.rev
            info.targets.removeAll { $0.id == target.id }
            pauses.remove(target.id)
            if selectedTargetID == target.id { select(target: Self.defaultTarget(info.targets)) }
            return
        }
        if let i = info.targets.firstIndex(where: { $0.id == target.id }) {
            info.targets[i] = mergedTarget(target)
        } else {
            if let gone = removedTargets[target.id], (rev ?? gone) <= gone { return }
            info.targets.append(target)
            info.targets.sort { ($0.targetRole == .main ? 0 : 1) < ($1.targetRole == .main ? 0 : 1) }
        }
        if selectedTargetID == nil { selectedTargetID = target.id }
        applyPause(of: target)
    }

    func applyPause(of target: DebugTarget) {
        switch pauses.apply(target) {
        case .unchanged: break
        case .needsState: fetchState(target.id)
        case .resumed: if target.id == selectedTargetID { clearPause() }
        }
    }

    /// Applies a pause or the state of `debug.state`; a newer pause selects its target and frame 0.
    func apply(paused state: DebugPauseState) {
        guard pauses.apply(state) else { return }
        stateFailures[state.targetID] = nil
        if let i = info.targets.firstIndex(where: { $0.id == state.targetID }) {
            info.targets[i].paused = state.paused
            info.targets[i].pausePending = false
            info.targets[i].gen = max(info.targets[i].gen, state.gen)
        }
        guard state.paused else {
            if state.targetID == selectedTargetID { clearPause() }
            return
        }
        selectedTargetID = state.targetID
        selectedFrameIndex = 0
        focus = nil
        showFrame()
    }

    func apply(resumedTarget targetID: String, gen: Int) {
        guard pauses.applyResumed(targetID: targetID, gen: gen) else { return }
        if let i = info.targets.firstIndex(where: { $0.id == targetID }) {
            info.targets[i].paused = false
            info.targets[i].pausePending = false
            info.targets[i].gen = max(info.targets[i].gen, gen)
        }
        if targetID == selectedTargetID { clearPause() }
    }

    func clearPause() {
        selectedFrameIndex = 0
        selectedVariable = nil
        variablesEpoch += 1
        variables.clear()
    }

    /// Applies a breakpoint by its `rev`; a removal leaves its `rev` behind.
    func apply(breakpoint: DebugBreakpoint, removed: Bool) {
        if let gone = removedBreakpoints[breakpoint.breakpointID], gone >= breakpoint.rev { return }
        if let i = info.breakpoints.firstIndex(where: { $0.breakpointID == breakpoint.breakpointID }) {
            guard breakpoint.rev > info.breakpoints[i].rev || (breakpoint.rev == 0 && info.breakpoints[i].rev == 0) else { return }
            if removed {
                info.breakpoints.remove(at: i)
            } else {
                info.breakpoints[i] = breakpoint
            }
        } else if !removed {
            info.breakpoints.append(breakpoint)
        }
        changeClock += 1
        breakpointChanged[breakpoint.breakpointID] = changeClock
        if removed {
            removedBreakpoints[breakpoint.breakpointID] = breakpoint.rev
            if selectedBreakpointID == breakpoint.breakpointID { selectedBreakpointID = nil }
        }
    }

    /// Queues a console entry for the next display frame.
    func apply(entry: ConsoleEntry) {
        pendingEntries.append(entry)
        guard flushTask == nil else { return }
        let frame = timings.consoleFrame
        flushTask = Task { [weak self] in
            try? await Task.sleep(for: frame)
            self?.flushConsole()
        }
    }

    /// Merges the queued console entries at once.
    func flushConsole() {
        flushTask = nil
        guard !pendingEntries.isEmpty else { return }
        let batch = pendingEntries
        pendingEntries = []
        console.merge(batch)
        consoleFlushes += 1
    }

    func applyClosed(reason: DebugCloseReason?, exitCode: Int?, rev: Int? = nil) {
        info.state = DebugSessionState.closed.rawValue
        info.closeReason = reason?.rawValue
        info.exitCode = exitCode ?? info.exitCode
        info.closedAt = info.closedAt ?? Timestamp(date: .now)
        if let rev { info.rev = max(info.rev, rev) }
        command = nil
        pauses = PauseBook()
        clearPause()
    }

    func applyConsoleCleared(through seq: Int) {
        flushConsole()
        console.clear(through: seq)
        consoleEpoch += 1
        consoleObjects = VariableTree()
        consoleSelection = []
        expandedEntries = []
    }

    // MARK: Selection

    public func select(target targetID: String?) {
        guard selectedTargetID != targetID else { return }
        selectedTargetID = targetID
        selectedFrameIndex = 0
        focus = nil
        if pauseState != nil { showFrame() } else { clearPause() }
    }

    public func selectFrame(_ index: Int) {
        guard let frames = pauseState?.callFrames, frames.indices.contains(index) else { return }
        selectedFrameIndex = index
        focus = nil
        showFrame()
    }

    /// Selects the frame above (−1) or below (+1) the selected one.
    public func moveFrameSelection(by delta: Int) {
        selectFrame(selectedFrameIndex + delta)
    }

    /// Shows a script location in the source panel, reading its source when needed.
    public func open(_ location: SourceFocus) {
        focus = location
        loadSource(targetID: location.targetID, scriptID: location.scriptID)
    }

    /// Shows the scopes and source of the selected frame.
    func showFrame() {
        variablesEpoch += 1
        selectedVariable = nil
        variables.reset(frame: selectedFrame, gen: pauseState?.gen ?? 0, targetID: selectedTargetID ?? "")
        if let exception = pauseState?.exception, selectedFrameIndex == 0 {
            variables.addRoot(path: ["exception"], title: String.ui("debugger.variables.exception", "예외"), value: exception,
                              targetID: selectedTargetID ?? "", first: true)
        }
        loadPending(in: \.variables)
        if let frame = selectedFrame, let targetID = selectedTargetID {
            loadSource(targetID: targetID, scriptID: frame.scriptID)
        }
    }

    // MARK: Notices

    func show(_ tone: DiagnosticsNotice.Tone, _ message: String, error: OperationError? = nil) {
        noticeTask?.cancel()
        let notice = DiagnosticsNotice(tone: tone, message: message, detail: error.map { $0.detail }, code: error?.code)
        self.notice = notice
        noticeTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(tone == .success ? 4 : 8))
            guard !Task.isCancelled, self?.notice?.id == notice.id else { return }
            self?.notice = nil
        }
    }

    public func dismissNotice() {
        noticeTask?.cancel()
        notice = nil
    }
}
