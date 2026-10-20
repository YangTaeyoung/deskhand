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

/// One debugging session as the tab sees it: its targets and their pauses ordered by `gen`, the
/// selected target and call frame, the scopes of that frame with lazily loaded properties, the
/// script sources it read, its breakpoints, and its console with evaluations and their history.
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
        case loaded(DebugSource)
        case failed(OperationError)
    }

    public let id: String
    public private(set) var info: DebugSession
    public private(set) var selectedTargetID: String?
    public private(set) var pauses = PauseBook()
    public private(set) var selectedFrameIndex = 0
    public private(set) var variables = VariableTree()
    /// Objects of evaluation results, one root per result.
    public private(set) var consoleObjects = VariableTree()
    public private(set) var console = ConsoleLog()
    public var consoleFilter = ConsoleFilter()
    public var history = EvaluateHistory()
    /// The expression being typed in the console.
    public var draft = ""
    /// Console entries whose stack is shown.
    public var expandedEntries: Set<Int> = []
    public private(set) var sources: [String: SourceState] = [:]
    public private(set) var scripts: [DebugScript] = []
    /// The execution command in flight.
    public private(set) var command: Command?
    public private(set) var consolePhase: LoadPhase = .idle
    public private(set) var isLoadingEarlier = false
    public private(set) var busyBreakpoints: Set<String> = []
    public private(set) var notice: DiagnosticsNotice?
    /// A location the person opened from the breakpoints or the console; a new pause replaces it.
    public private(set) var focus: SourceFocus?

    @ObservationIgnored let backend: any DebuggerBackend
    @ObservationIgnored private var noticeTask: Task<Void, Never>?
    @ObservationIgnored private var fetchingStates: Set<String> = []

    init(info: DebugSession, backend: any DebuggerBackend) {
        id = info.debugID
        self.info = info
        self.backend = backend
        selectedTargetID = Self.defaultTarget(info.targets)
    }

    /// Reads the pauses its targets report and the console.
    func begin() async {
        for target in info.targets { applyListed(target) }
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

    public func isPaused(_ targetID: String) -> Bool { pauses.isPaused(targetID) }

    /// Targets that are paused.
    public var pausedTargets: [DebugTarget] { info.targets.filter { pauses.isPaused($0.id) } }

    public func sourceState(targetID: String, scriptID: String) -> SourceState? {
        sources[Self.sourceKey(targetID, scriptID)]
    }

    /// The loaded source of a frame's script.
    public func source(for frame: DebugCallFrame?) -> DebugSource? {
        guard let frame, let targetID = selectedTargetID,
              case .loaded(let source) = sourceState(targetID: targetID, scriptID: frame.scriptID) else { return nil }
        return source
    }

    /// The location the source panel shows: the opened location, else the selected frame.
    public var shownLocation: SourceFocus? {
        if let focus { return focus }
        guard let frame = selectedFrame, let targetID = selectedTargetID else { return nil }
        return SourceFocus(targetID: targetID, scriptID: frame.scriptID, url: frame.url, line: frame.line, column: frame.column)
    }

    /// Breakpoints bound at `url`:`line`.
    public func breakpoints(url: String, line: Int) -> [DebugBreakpoint] {
        info.breakpoints.filter { bp in
            bp.locations.contains { $0.url == url && $0.line == line } || (bp.locations.isEmpty && bp.url == url && bp.line == line)
        }
    }

    static func sourceKey(_ targetID: String, _ scriptID: String) -> String { targetID + "|" + scriptID }

    // MARK: Session updates

    /// Applies the session as `debug.list`, `debug.get` or `debug.session_changed` reports it.
    func apply(info newInfo: DebugSession) {
        let wasClosed = isClosed
        info = newInfo
        if wasClosed, !isClosed { info.state = DebugSessionState.closed.rawValue }
        if selectedTargetID.map({ id in !newInfo.targets.contains { $0.id == id } }) ?? true {
            selectedTargetID = Self.defaultTarget(newInfo.targets)
        }
        for target in newInfo.targets { applyListed(target) }
    }

    /// Applies one target from a list, event or reply: its fields, and its pause by `gen`.
    func apply(target: DebugTarget, removed: Bool = false) {
        if removed {
            info.targets.removeAll { $0.id == target.id }
            pauses.remove(target.id)
            if selectedTargetID == target.id { select(target: Self.defaultTarget(info.targets)) }
            return
        }
        if let i = info.targets.firstIndex(where: { $0.id == target.id }) {
            info.targets[i] = target
        } else {
            info.targets.append(target)
            info.targets.sort { ($0.targetRole == .main ? 0 : 1) < ($1.targetRole == .main ? 0 : 1) }
        }
        if selectedTargetID == nil { selectedTargetID = target.id }
        applyListed(target)
    }

    private func applyListed(_ target: DebugTarget) {
        switch pauses.apply(target) {
        case .unchanged: break
        case .needsState: fetchState(target.id)
        case .resumed: if target.id == selectedTargetID { clearPause() }
        }
    }

    /// Reads the pause of a target whose `gen` is ahead of the known pause.
    private func fetchState(_ targetID: String) {
        guard !fetchingStates.contains(targetID) else { return }
        fetchingStates.insert(targetID)
        Task { [weak self] in
            guard let self else { return }
            defer { fetchingStates.remove(targetID) }
            do {
                let state = try await backend.debugState(debugID: id, targetID: targetID)
                apply(paused: state)
            } catch {}
        }
    }

    /// Applies a pause or the state of `debug.state`; a newer pause selects its target and frame 0.
    func apply(paused state: DebugPauseState) {
        guard pauses.apply(state) else { return }
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
        guard pauses.applyResumed(targetID: targetID, gen: gen), targetID == selectedTargetID else { return }
        clearPause()
    }

    private func clearPause() {
        selectedFrameIndex = 0
        variables.clear()
    }

    func apply(breakpoint: DebugBreakpoint, removed: Bool) {
        if removed {
            info.breakpoints.removeAll { $0.breakpointID == breakpoint.breakpointID }
        } else if let i = info.breakpoints.firstIndex(where: { $0.breakpointID == breakpoint.breakpointID }) {
            info.breakpoints[i] = breakpoint
        } else {
            info.breakpoints.append(breakpoint)
        }
    }

    func apply(entry: ConsoleEntry) {
        console.merge([entry])
    }

    func applyClosed(reason: DebugCloseReason?, exitCode: Int?) {
        info.state = DebugSessionState.closed.rawValue
        info.closeReason = reason?.rawValue
        info.exitCode = exitCode ?? info.exitCode
        info.closedAt = info.closedAt ?? Timestamp(date: .now)
        command = nil
        pauses = PauseBook()
        variables.clear()
    }

    // MARK: Selection

    public func select(target targetID: String?) {
        guard selectedTargetID != targetID else { return }
        selectedTargetID = targetID
        selectedFrameIndex = 0
        focus = nil
        if pauseState != nil { showFrame() } else { variables.clear() }
    }

    public func selectFrame(_ index: Int) {
        guard let frames = pauseState?.callFrames, frames.indices.contains(index) else { return }
        selectedFrameIndex = index
        focus = nil
        showFrame()
    }

    /// Shows a script location in the source panel, reading its source when needed.
    public func open(_ location: SourceFocus) {
        focus = location
        loadSource(targetID: location.targetID, scriptID: location.scriptID)
    }

    /// Shows the scopes and source of the selected frame.
    private func showFrame() {
        variables.reset(frame: selectedFrame, gen: pauseState?.gen ?? 0)
        loadPendingVariables()
        if let frame = selectedFrame, let targetID = selectedTargetID {
            loadSource(targetID: targetID, scriptID: frame.scriptID)
        }
    }

    /// Attaches to a listed target the daemon left detached.
    public func attachTarget(_ targetID: String) {
        Task { [weak self] in
            guard let self else { return }
            do {
                apply(target: try await backend.debugTargetAttach(debugID: id, targetID: targetID))
            } catch {
                show(.error, .ui("debugger.failed.targetAttach", "이 대상에 붙지 못했습니다"), error: OperationError(error))
            }
        }
    }

    // MARK: Execution

    /// Pauses the selected target, or resumes it while paused (F8).
    public func togglePause() {
        guard canControl else { return }
        run(pauseState == nil ? .pause : .resume)
    }

    /// Steps the paused target over, into or out of the current call (F10, F11, ⇧F11).
    public func step(_ kind: DebugStepKind) {
        guard canControl, pauseState != nil else { return }
        run(.step(kind))
    }

    private func run(_ next: Command) {
        guard command == nil, let targetID = selectedTargetID else { return }
        command = next
        let backend = backend
        let debugID = id
        Task { [weak self] in
            do {
                let reply: DebugTarget?
                switch next {
                case .pause: reply = try await backend.debugPause(debugID: debugID, targetID: targetID)
                case .resume: reply = try await backend.debugResume(debugID: debugID, targetID: targetID)
                case .step(let kind): reply = try await backend.debugStep(debugID: debugID, targetID: targetID, kind: kind)
                case .close: reply = nil
                }
                guard let self else { return }
                if self.command == next { self.command = nil }
                if let reply { self.apply(target: reply) }
            } catch {
                guard let self else { return }
                if self.command == next { self.command = nil }
                let failure = OperationError(error)
                self.show(.error, Self.commandFailure(next), error: failure)
                if failure.code == "STALE_TARGET" { self.fetchState(targetID) }
            }
        }
    }

    static func commandFailure(_ command: Command) -> String {
        switch command {
        case .pause: .ui("debugger.failed.pause", "일시정지하지 못했습니다")
        case .resume: .ui("debugger.failed.resume", "재개하지 못했습니다")
        case .step: .ui("debugger.failed.step", "한 단계 실행하지 못했습니다")
        case .close: .ui("debugger.failed.close", "세션을 닫지 못했습니다")
        }
    }

    /// Ends the session; a launched app ends too unless `keepProcess`.
    public func close(keepProcess: Bool) async {
        guard !isClosed, command != .close else { return }
        command = .close
        do {
            try await backend.debugClose(debugID: id, keepProcess: keepProcess)
            applyClosed(reason: .requested, exitCode: nil)
        } catch {
            command = nil
            show(.error, Self.commandFailure(.close), error: OperationError(error))
        }
    }

    // MARK: Variables

    /// Expands or collapses a scope or property, loading its properties the first time; a failure
    /// row tries again and a more row loads the next page.
    public func toggleVariable(_ row: VariableTree.Row) {
        switch row.kind {
        case .scope, .property:
            guard row.isExpandable else { return }
            variables.toggle(row.path)
            loadPendingVariables()
        case .failure:
            if let objectID = row.objectID { variables.retry(objectID) }
            loadPendingVariables()
        case .more:
            if let objectID = row.objectID { loadMore(objectID, in: \.variables) }
        case .loading:
            break
        }
    }

    /// Expands or collapses an evaluation result's object in the console.
    public func toggleConsoleObject(_ row: VariableTree.Row) {
        switch row.kind {
        case .scope, .property:
            guard row.isExpandable else { return }
            consoleObjects.toggle(row.path)
            loadPending(in: \.consoleObjects)
        case .failure:
            if let objectID = row.objectID { consoleObjects.retry(objectID) }
            loadPending(in: \.consoleObjects)
        case .more:
            if let objectID = row.objectID { loadMore(objectID, in: \.consoleObjects) }
        case .loading:
            break
        }
    }

    private func loadPendingVariables() {
        loadPending(in: \.variables)
    }

    private func loadPending(in tree: ReferenceWritableKeyPath<DebugSessionModel, VariableTree>) {
        guard let targetID = selectedTargetID ?? info.targets.first?.id else { return }
        for objectID in self[keyPath: tree].pendingLoads {
            self[keyPath: tree].beginLoading(objectID)
            fetch(objectID, offset: 0, targetID: targetID, in: tree)
        }
    }

    private func loadMore(_ objectID: String, in tree: ReferenceWritableKeyPath<DebugSessionModel, VariableTree>) {
        guard let targetID = selectedTargetID else { return }
        fetch(objectID, offset: self[keyPath: tree].nextOffset(objectID), targetID: targetID, in: tree)
    }

    private func fetch(_ objectID: String, offset: Int, targetID: String, in tree: ReferenceWritableKeyPath<DebugSessionModel, VariableTree>) {
        let gen = self[keyPath: tree].gen
        let frameID = self[keyPath: tree].frameID
        let backend = backend
        let debugID = id
        Task { [weak self] in
            let result: Result<DebugPropertyPage, OperationError>
            do {
                result = .success(try await backend.debugVariables(debugID: debugID, targetID: targetID, objectID: objectID, offset: offset,
                                                                   limit: VariableTree.pageSize))
            } catch {
                result = .failure(OperationError(error))
            }
            guard let self, self[keyPath: tree].gen == gen, self[keyPath: tree].frameID == frameID else { return }
            self[keyPath: tree].finishLoading(objectID, result, append: offset > 0)
            self.loadPending(in: tree)
        }
    }

    // MARK: Sources

    func loadSource(targetID: String, scriptID: String) {
        let key = Self.sourceKey(targetID, scriptID)
        switch sources[key] {
        case .loading, .loaded: return
        case .failed, nil: break
        }
        sources[key] = .loading
        let backend = backend
        let debugID = id
        Task { [weak self] in
            do {
                let source = try await backend.debugSource(debugID: debugID, targetID: targetID, scriptID: scriptID)
                self?.sources[key] = .loaded(source)
            } catch {
                self?.sources[key] = .failed(OperationError(error))
            }
        }
    }

    /// Reads the loaded scripts for the add-breakpoint sheet.
    public func loadScripts() async {
        guard let list = try? await backend.debugScripts(debugID: id, targetID: nil) else { return }
        scripts = list
    }

    // MARK: Breakpoints

    /// Sets a breakpoint and returns why it was refused.
    public func setBreakpoint(_ pattern: BreakpointPattern, line: Int, condition: String?) async -> OperationError? {
        let match: BreakpointRequest.Match
        do {
            match = try pattern.match()
        } catch {
            return OperationError(code: "INVALID_ARGUMENT", message: error.localizedDescription)
        }
        let trimmed = condition?.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            let breakpoint = try await backend.debugBreakpointSet(BreakpointRequest(
                debugID: id, match: match, line: line, condition: trimmed?.isEmpty == false ? trimmed : nil))
            apply(breakpoint: breakpoint, removed: false)
            return nil
        } catch {
            return OperationError(error)
        }
    }

    public func setBreakpointEnabled(_ breakpointID: String, _ enabled: Bool) {
        guard !busyBreakpoints.contains(breakpointID) else { return }
        busyBreakpoints.insert(breakpointID)
        Task { [weak self] in
            guard let self else { return }
            defer { busyBreakpoints.remove(breakpointID) }
            do {
                let breakpoint = try await backend.debugBreakpointEnable(debugID: id, breakpointID: breakpointID, enabled: enabled)
                apply(breakpoint: breakpoint, removed: false)
            } catch {
                show(.error, .ui("debugger.failed.breakpoint", "중단점을 바꾸지 못했습니다"), error: OperationError(error))
            }
        }
    }

    public func removeBreakpoint(_ breakpointID: String) {
        guard !busyBreakpoints.contains(breakpointID) else { return }
        busyBreakpoints.insert(breakpointID)
        Task { [weak self] in
            guard let self else { return }
            defer { busyBreakpoints.remove(breakpointID) }
            do {
                try await backend.debugBreakpointRemove(debugID: id, breakpointID: breakpointID)
                info.breakpoints.removeAll { $0.breakpointID == breakpointID }
            } catch {
                show(.error, .ui("debugger.failed.breakpointRemove", "중단점을 지우지 못했습니다"), error: OperationError(error))
            }
        }
    }

    /// Removes the breakpoints at `url`:`line`, or sets one there by exact URL (the source gutter).
    public func toggleBreakpoint(url: String, line: Int) {
        let existing = breakpoints(url: url, line: line)
        guard existing.isEmpty else {
            for bp in existing { removeBreakpoint(bp.breakpointID) }
            return
        }
        Task { [weak self] in
            guard let self else { return }
            if let error = await setBreakpoint(.url(url), line: line, condition: nil) {
                show(.error, .ui("debugger.failed.breakpointSet", "중단점을 걸지 못했습니다"), error: error)
            }
        }
    }

    public func setBreakpointsActive(_ active: Bool) {
        Task { [weak self] in
            guard let self else { return }
            do {
                try await backend.debugBreakpointsActivate(debugID: id, active: active)
                info.breakpointsActive = active
            } catch {
                show(.error, .ui("debugger.failed.breakpoint", "중단점을 바꾸지 못했습니다"), error: OperationError(error))
            }
        }
    }

    public func setExceptionMode(_ mode: ExceptionPauseMode) {
        Task { [weak self] in
            guard let self else { return }
            do {
                try await backend.debugExceptions(debugID: id, mode: mode)
                info.pauseOnExceptions = mode.rawValue
            } catch {
                show(.error, .ui("debugger.failed.exceptions", "예외에서 멈춤 설정을 바꾸지 못했습니다"), error: OperationError(error))
            }
        }
    }

    // MARK: Console

    /// Reads the latest page the first time, then everything after the last `seq` held.
    func loadConsole() async {
        let first = consolePhase != .loaded
        if first, console.entries.isEmpty { consolePhase = .loading }
        var cursor: Int? = first && console.entries.isEmpty ? nil : console.cursor
        do {
            for _ in 0..<20 {
                let page = try await backend.debugConsole(ConsoleQuery(debugID: id, cursor: cursor,
                                                                       limit: cursor == nil ? 200 : ConsoleQuery.maximumLimit))
                console.apply(page)
                consolePhase = .loaded
                guard page.hasMore else { break }
                cursor = page.nextCursor
            }
        } catch {
            if consolePhase != .loaded { consolePhase = .failed(OperationError(error)) }
        }
    }

    /// Reads the page before the oldest entry held.
    public func loadEarlierConsole() async {
        guard console.hasEarlier, let first = console.firstSeq, !isLoadingEarlier else { return }
        isLoadingEarlier = true
        defer { isLoadingEarlier = false }
        do {
            let page = try await backend.debugConsole(ConsoleQuery(debugID: id, before: first, limit: 200))
            console.apply(page, earlier: true)
        } catch {
            show(.error, .ui("debugger.failed.console", "이전 콘솔 기록을 불러오지 못했습니다"), error: OperationError(error))
        }
    }

    public func clearConsole() {
        console.clear()
        consoleObjects = VariableTree()
    }

    /// Evaluates `expression` in the selected frame while paused, globally otherwise, and records it.
    public func evaluate(_ expression: String) {
        let trimmed = expression.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, !isClosed, let targetID = selectedTargetID else { return }
        history.record(trimmed)
        let frame = pauseState == nil ? nil : selectedFrame
        let frameName = frame.map { $0.function.isEmpty ? String.ui("debugger.anonymous", "(익명 함수)") : $0.function }
        let record = console.addEvaluation(expression: trimmed, targetID: targetID, frame: frameName)
        let backend = backend
        let debugID = id
        Task { [weak self] in
            do {
                let result = try await backend.debugEvaluate(debugID: debugID, targetID: targetID, expression: trimmed, frameID: frame?.frameID)
                guard let self else { return }
                console.resolveEvaluation(record, .value(result))
                consoleObjects.addRoot(path: "x\(record)", title: trimmed, value: result.value)
            } catch {
                self?.console.resolveEvaluation(record, .failure(OperationError(error)))
            }
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

    // MARK: Seeding

    /// Puts a state in place without asking the backend. Snapshot scenes use it; `properties`
    /// answers the variable loads of expanded rows.
    public func seed(pause: DebugPauseState? = nil, selectedTarget: String? = nil, frame: Int = 0,
                     properties: (String) -> DebugPropertyPage? = { _ in nil }, expanded: [String] = [],
                     console entries: [ConsoleEntry] = [], consolePhase: LoadPhase = .loaded, hasEarlier: Bool = false,
                     evaluations: [(expression: String, frame: String?, result: EvaluationResult)] = [], expandedResults: [Int] = [],
                     expandedEntries: Set<Int> = [], sources: [(targetID: String, source: DebugSource)] = [], scripts: [DebugScript] = [],
                     history: [String] = [], filter: ConsoleFilter = ConsoleFilter(), focus: SourceFocus? = nil,
                     command: Command? = nil, notice: DiagnosticsNotice? = nil, draft: String = "") {
        pauses = PauseBook()
        for target in info.targets { _ = pauses.apply(target) }
        if let pause { pauses.apply(pause) }
        selectedTargetID = selectedTarget ?? pause?.targetID ?? selectedTargetID
        selectedFrameIndex = frame
        variables = VariableTree()
        variables.reset(frame: selectedFrame, gen: pauseState?.gen ?? 0)
        for path in expanded { variables.setExpanded(path, true) }
        Self.fill(&variables, properties)
        console = ConsoleLog()
        console.apply(ConsolePage(entries: entries, nextCursor: entries.last?.seq ?? 0, hasEarlier: hasEarlier))
        self.consolePhase = consolePhase
        consoleObjects = VariableTree()
        for (index, evaluation) in evaluations.enumerated() {
            let id = console.addEvaluation(expression: evaluation.expression, targetID: selectedTargetID ?? "", frame: evaluation.frame)
            console.resolveEvaluation(id, evaluation.result)
            if case .value(let result) = evaluation.result {
                consoleObjects.addRoot(path: "x\(id)", title: evaluation.expression, value: result.value)
                if expandedResults.contains(index) { consoleObjects.setExpanded("x\(id)", true) }
            }
        }
        Self.fill(&consoleObjects, properties)
        self.expandedEntries = expandedEntries
        self.sources = Dictionary(sources.map { (Self.sourceKey($0.targetID, $0.source.scriptID), SourceState.loaded($0.source)) },
                                  uniquingKeysWith: { first, _ in first })
        self.scripts = scripts
        self.history = EvaluateHistory(items: history)
        consoleFilter = filter
        self.focus = focus
        self.command = command
        self.notice = notice
        self.draft = draft
    }

    private static func fill(_ tree: inout VariableTree, _ properties: (String) -> DebugPropertyPage?) {
        var pending = tree.pendingLoads
        var rounds = 0
        while !pending.isEmpty, rounds < 32 {
            var progressed = false
            for objectID in pending {
                guard let page = properties(objectID) else { continue }
                tree.finishLoading(objectID, .success(page))
                progressed = true
            }
            guard progressed else { break }
            pending = tree.pendingLoads
            rounds += 1
        }
    }
}
