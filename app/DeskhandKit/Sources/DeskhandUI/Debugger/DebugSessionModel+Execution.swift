import DeskhandClient
import Foundation

// MARK: Execution

extension DebugSessionModel {
    /// Pauses the selected target, or resumes it while paused or while a pause waits for code to
    /// run, which cancels that pause (F8, ⌘\).
    public func togglePause() {
        guard canControl else { return }
        run(isSelectedPaused || pausePending ? .resume : .pause)
    }

    /// Steps the paused target over, into or out of the current call (F10 ⌘', F11 ⌘;, ⇧F11 ⇧⌘;).
    public func step(_ kind: DebugStepKind) {
        guard canControl, pauseState != nil else { return }
        run(.step(kind))
    }

    func run(_ next: Command) {
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
                if next != .close { self.fetchState(targetID) }
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

    /// Reads the pause of a target whose reported `gen` is ahead of the applied pause, trying
    /// again after each of the retry waits; the last failure is kept for the source panel.
    func fetchState(_ targetID: String) {
        guard !fetchingStates.contains(targetID) else { return }
        fetchingStates.insert(targetID)
        stateFailures[targetID] = nil
        let backend = backend
        let debugID = id
        let waits = timings.stateRetries
        Task { [weak self] in
            var attempt = 0
            while true {
                do {
                    let state = try await backend.debugState(debugID: debugID, targetID: targetID)
                    guard let self else { return }
                    self.fetchingStates.remove(targetID)
                    self.apply(paused: state)
                    return
                } catch {
                    guard let self else { return }
                    guard attempt < waits.count, self.pauses.announcedGen(of: targetID) != nil, !self.isClosed else {
                        self.fetchingStates.remove(targetID)
                        if self.pauses.announcedGen(of: targetID) != nil { self.stateFailures[targetID] = OperationError(error) }
                        return
                    }
                    let wait = waits[attempt]
                    attempt += 1
                    try? await Task.sleep(for: wait)
                }
            }
        }
    }

    /// Reads the selected target's pause again after a failure.
    public func retryState() {
        guard let targetID = selectedTargetID else { return }
        fetchState(targetID)
    }

    /// Attaches to a listed target the daemon left detached.
    public func attachTarget(_ targetID: String) {
        Task { [weak self] in
            guard let self else { return }
            do {
                apply(target: try await backend.debugTargetAttach(debugID: id, targetID: targetID))
            } catch {
                show(.error, .ui("debugger.failed.targetAttach", "이 대상에 연결하지 못했습니다"), error: OperationError(error))
            }
        }
    }

    /// Ends the session; a launched app ends too unless `keepProcess`. When a Node inspector
    /// Deskhand turned on stays open on the PC, a notice says so.
    public func close(keepProcess: Bool) async {
        guard !isClosed, command != .close else { return }
        command = .close
        do {
            let result = try await backend.debugClose(debugID: id, keepProcess: keepProcess)
            if let session = result.session { apply(info: session) }
            applyClosed(reason: .requested, exitCode: nil)
            if result.inspectorLeftOpen {
                show(.info, .ui("debugger.inspectorLeftOpen", "PC의 검사기 포트가 열린 채 남았습니다 · 앱을 다시 시작하면 닫힙니다"))
            }
        } catch {
            command = nil
            show(.error, Self.commandFailure(.close), error: OperationError(error))
        }
    }

    // MARK: Variables

    /// Expands or collapses a scope or property, loading its properties the first time; a failure
    /// row tries again and a more row loads the next page once.
    public func toggleVariable(_ row: VariableTree.Row) {
        toggle(row, in: \.variables)
    }

    /// Expands or collapses an evaluation result's object in the console.
    public func toggleConsoleObject(_ row: VariableTree.Row) {
        toggle(row, in: \.consoleObjects)
    }

    private func toggle(_ row: VariableTree.Row, in tree: ReferenceWritableKeyPath<DebugSessionModel, VariableTree>) {
        switch row.kind {
        case .scope, .property:
            guard row.isExpandable else { return }
            self[keyPath: tree].toggle(row.path)
            loadPending(in: tree)
        case .failure:
            if let objectID = row.objectID { self[keyPath: tree].retry(objectID) }
            loadPending(in: tree)
        case .more:
            guard let objectID = row.objectID, self[keyPath: tree].beginLoadingMore(objectID) else { return }
            fetch(VariableTree.Load(objectID: objectID, targetID: row.targetID), offset: self[keyPath: tree].nextOffset(objectID), in: tree)
        case .loading:
            break
        }
    }

    /// Expands (`true`) or collapses the selected variable, or moves the selection by `delta` rows.
    public func moveVariableSelection(by delta: Int) {
        let rows = variables.rows.filter { $0.kind == .scope || $0.kind == .property }
        guard !rows.isEmpty else { return }
        let index = rows.firstIndex { $0.id == selectedVariable } ?? (delta > 0 ? -1 : rows.count)
        selectedVariable = rows[min(max(index + delta, 0), rows.count - 1)].id
    }

    public func setSelectedVariableExpanded(_ open: Bool) {
        guard let row = variables.rows.first(where: { $0.id == selectedVariable }), row.isExpandable, row.isExpanded != open else { return }
        toggleVariable(row)
    }

    func loadPending(in tree: ReferenceWritableKeyPath<DebugSessionModel, VariableTree>) {
        for load in self[keyPath: tree].pendingLoads {
            self[keyPath: tree].beginLoading(load.objectID)
            fetch(load, offset: 0, in: tree)
        }
    }

    private func fetch(_ load: VariableTree.Load, offset: Int, in tree: ReferenceWritableKeyPath<DebugSessionModel, VariableTree>) {
        let epoch = epoch(of: tree)
        let backend = backend
        let debugID = id
        Task { [weak self] in
            let result: Result<DebugPropertyPage, OperationError>
            do {
                result = .success(try await backend.debugVariables(debugID: debugID, targetID: load.targetID, objectID: load.objectID,
                                                                   offset: offset, limit: VariableTree.pageSize))
            } catch {
                result = .failure(OperationError(error))
            }
            guard let self else { return }
            guard epoch == self.epoch(of: tree) else { return }
            self[keyPath: tree].finishLoading(load.objectID, result, append: offset > 0)
            self.loadPending(in: tree)
        }
    }

    private func epoch(of tree: ReferenceWritableKeyPath<DebugSessionModel, VariableTree>) -> Int {
        tree == \DebugSessionModel.variables ? variablesEpoch : consoleEpoch
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
                self?.sources[key] = .loaded(LoadedSource(source))
            } catch {
                self?.sources[key] = .failed(OperationError(error))
            }
        }
    }

    /// Reads the source shown again after a failure.
    public func retrySource() {
        guard let location = shownLocation else { return }
        let key = Self.sourceKey(location.targetID, location.scriptID)
        if case .failed = sources[key] { sources[key] = nil }
        loadSource(targetID: location.targetID, scriptID: location.scriptID)
    }

    /// Reads the loaded scripts for the script navigator and the add-breakpoint sheet.
    public func loadScripts() async {
        do {
            scripts = try await backend.debugScripts(debugID: id, targetID: nil)
            scriptsError = nil
        } catch is CancellationError {
        } catch {
            scriptsError = OperationError(error)
        }
    }

    // MARK: Breakpoints

    /// Sets a breakpoint and returns why it was refused.
    public func setBreakpoint(_ pattern: BreakpointPattern, line: Int, condition: String?) async -> OperationError? {
        let spec: BreakpointSpec
        do {
            spec = try pattern.spec(line: line, condition: condition)
        } catch {
            return OperationError(code: "INVALID_ARGUMENT", message: error.localizedDescription)
        }
        do {
            apply(breakpoint: try await backend.debugBreakpointSet(debugID: id, spec: spec), removed: false)
            return nil
        } catch {
            return OperationError(error)
        }
    }

    public func setBreakpointEnabled(_ breakpointID: String, _ enabled: Bool) {
        update(breakpointID, enabled: enabled, condition: nil)
    }

    /// Changes a breakpoint's condition and returns why it was refused; `""` removes it.
    public func setBreakpointCondition(_ breakpointID: String, _ condition: String) async -> OperationError? {
        guard !busyBreakpoints.contains(breakpointID) else { return nil }
        busyBreakpoints.insert(breakpointID)
        defer { busyBreakpoints.remove(breakpointID) }
        do {
            let cleaned = condition.trimmingCharacters(in: .whitespacesAndNewlines)
            apply(breakpoint: try await backend.debugBreakpointUpdate(debugID: id, breakpointID: breakpointID, enabled: nil,
                                                                      condition: cleaned),
                  removed: false)
            return nil
        } catch {
            return OperationError(error)
        }
    }

    private func update(_ breakpointID: String, enabled: Bool?, condition: String?) {
        guard !busyBreakpoints.contains(breakpointID) else { return }
        busyBreakpoints.insert(breakpointID)
        Task { [weak self] in
            guard let self else { return }
            defer { busyBreakpoints.remove(breakpointID) }
            do {
                apply(breakpoint: try await backend.debugBreakpointUpdate(debugID: id, breakpointID: breakpointID, enabled: enabled,
                                                                          condition: condition),
                      removed: false)
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
            await remove(breakpointID)
            busyBreakpoints.remove(breakpointID)
        }
    }

    private func remove(_ breakpointID: String) async {
        do {
            let removed = try await backend.debugBreakpointRemove(debugID: id, breakpointID: breakpointID)
            if let removed {
                apply(breakpoint: removed, removed: true)
            } else if let known = info.breakpoints.first(where: { $0.breakpointID == breakpointID }) {
                var gone = known
                gone.rev += 1
                apply(breakpoint: gone, removed: true)
            }
        } catch {
            show(.error, .ui("debugger.failed.breakpointRemove", "중단점을 지우지 못했습니다"), error: OperationError(error))
        }
    }

    /// Removes the selected breakpoint (⌫).
    public func removeSelectedBreakpoint() {
        guard let id = selectedBreakpointID else { return }
        removeBreakpoint(id)
    }

    /// Removes the breakpoints at `url`:`line`, or sets one there by exact URL (the source
    /// gutter); a click while the change for the line is in flight does nothing.
    public func toggleBreakpoint(url: String, line: Int) {
        let key = url + ":" + String(line)
        guard !pendingLines.contains(key) else { return }
        pendingLines.insert(key)
        let existing = breakpoints(url: url, line: line)
        Task { [weak self] in
            guard let self else { return }
            defer { pendingLines.remove(key) }
            if existing.isEmpty {
                if let error = await setBreakpoint(.url(url), line: line, condition: nil) {
                    show(.error, .ui("debugger.failed.breakpointSet", "중단점을 걸지 못했습니다"), error: error)
                }
            } else {
                for bp in existing { await remove(bp.breakpointID) }
            }
        }
    }

    /// Turns every breakpoint on or off. The reply's session applies by its `rev`; without one the
    /// field changes only when no newer session arrived meanwhile.
    public func setBreakpointsActive(_ active: Bool) {
        let asked = info.rev
        Task { [weak self] in
            guard let self else { return }
            do {
                if let session = try await backend.debugBreakpointsActivate(debugID: id, active: active) {
                    apply(info: session)
                } else if info.rev == asked {
                    info.breakpointsActive = active
                }
            } catch {
                show(.error, .ui("debugger.failed.breakpoint", "중단점을 바꾸지 못했습니다"), error: OperationError(error))
            }
        }
    }

    /// Changes when exceptions pause, applied like `setBreakpointsActive`.
    public func setExceptionMode(_ mode: ExceptionPauseMode) {
        let asked = info.rev
        Task { [weak self] in
            guard let self else { return }
            do {
                if let session = try await backend.debugExceptions(debugID: id, mode: mode) {
                    apply(info: session)
                } else if info.rev == asked {
                    info.pauseOnExceptions = mode.rawValue
                }
            } catch {
                show(.error, .ui("debugger.failed.exceptions", "예외에서 멈춤 설정을 바꾸지 못했습니다"), error: OperationError(error))
            }
        }
    }

    /// The launch that starts this session again: the same app, arguments, folder, start pause,
    /// breakpoints and exception setting.
    public var relaunchRequest: DebugLaunchRequest? {
        guard info.sessionKind == .launch, let path = info.path else { return nil }
        return DebugLaunchRequest(hostID: info.hostID, path: path, args: info.args, cwd: info.cwd, pauseOnStart: info.pauseOnStart,
                                  breakpoints: info.breakpoints.map(\.spec), pauseOnExceptions: info.exceptionMode)
    }
}
