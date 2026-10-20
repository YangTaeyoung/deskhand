import DeskhandClient
import Foundation

// MARK: Seeding

extension DebugSessionModel {
    /// Puts a state in place without asking the backend. Snapshot scenes use it; `properties`
    /// answers the variable loads of expanded rows. Paths are root path elements then names.
    public func seed(pause: DebugPauseState? = nil, selectedTarget: String? = nil, frame: Int = 0,
                     properties: (String) -> DebugPropertyPage? = { _ in nil }, expanded: [VariableTree.Path] = [],
                     console entries: [ConsoleEntry] = [], consolePhase: LoadPhase = .loaded, hasEarlier: Bool = false,
                     gaps: [Int] = [], evaluations: [(expression: String, frame: String?, result: EvaluationResult)] = [],
                     expandedResults: [Int] = [], expandedEntries: Set<Int> = [], consoleSelection: Set<String> = [],
                     sources: [(targetID: String, source: DebugSource)] = [], failedSources: [(targetID: String, scriptID: String, error: OperationError)] = [],
                     scripts: [DebugScript] = [], history: [String] = [], filter: ConsoleFilter = ConsoleFilter(), focus: SourceFocus? = nil,
                     command: Command? = nil, notice: DiagnosticsNotice? = nil, draft: String = "", selectedVariable: VariableTree.Path? = nil,
                     selectedBreakpointID: String? = nil, stateFailure: OperationError? = nil) {
        pauses = PauseBook()
        for target in info.targets { _ = pauses.apply(target) }
        if let pause { pauses.apply(pause) }
        selectedTargetID = selectedTarget ?? pause?.targetID ?? selectedTargetID
        selectedFrameIndex = frame
        variables = VariableTree()
        variables.reset(frame: selectedFrame, gen: pauseState?.gen ?? 0, targetID: selectedTargetID ?? "")
        if let exception = pauseState?.exception, frame == 0 {
            variables.addRoot(path: ["exception"], title: String.ui("debugger.variables.exception", "예외"), value: exception,
                              targetID: selectedTargetID ?? "", first: true)
        }
        for path in expanded { variables.setExpanded(path, true) }
        Self.fill(&variables, properties)
        self.selectedVariable = selectedVariable.flatMap { path in variables.rows.first { $0.path == path }?.id }
        console = ConsoleLog()
        console.apply(ConsolePage(entries: entries, nextCursor: entries.last?.seq ?? 0, hasEarlier: hasEarlier))
        for seq in gaps {
            console.apply(ConsolePage(entries: entries.filter { $0.seq >= seq }, nextCursor: entries.last?.seq ?? 0, gap: true))
        }
        self.consolePhase = consolePhase
        consoleObjects = VariableTree()
        for (index, evaluation) in evaluations.enumerated() {
            let id = console.addEvaluation(expression: evaluation.expression, targetID: selectedTargetID ?? "", frame: evaluation.frame)
            console.resolveEvaluation(id, evaluation.result)
            if case .value(let result) = evaluation.result {
                consoleObjects.addRoot(path: ["x\(id)"], title: evaluation.expression, value: result.value, targetID: selectedTargetID ?? "")
                if expandedResults.contains(index) { consoleObjects.setExpanded(["x\(id)"], true) }
            }
        }
        Self.fill(&consoleObjects, properties)
        self.expandedEntries = expandedEntries
        self.consoleSelection = consoleSelection
        var states = Dictionary(sources.map { (Self.sourceKey($0.targetID, $0.source.scriptID), SourceState.loaded(LoadedSource($0.source))) },
                                uniquingKeysWith: { first, _ in first })
        for failed in failedSources { states[Self.sourceKey(failed.targetID, failed.scriptID)] = .failed(failed.error) }
        self.sources = states
        self.scripts = scripts
        self.history = EvaluateHistory(items: history)
        consoleFilter = filter
        self.focus = focus
        self.command = command
        self.notice = notice
        self.draft = draft
        self.selectedBreakpointID = selectedBreakpointID
        if let stateFailure, let target = selectedTargetID { stateFailures[target] = stateFailure }
    }

    private static func fill(_ tree: inout VariableTree, _ properties: (String) -> DebugPropertyPage?) {
        var pending = tree.pendingLoads
        var rounds = 0
        while !pending.isEmpty, rounds < 32 {
            var progressed = false
            for load in pending {
                guard let page = properties(load.objectID) else { continue }
                tree.finishLoading(load.objectID, .success(page))
                progressed = true
            }
            guard progressed else { break }
            pending = tree.pendingLoads
            rounds += 1
        }
    }
}
