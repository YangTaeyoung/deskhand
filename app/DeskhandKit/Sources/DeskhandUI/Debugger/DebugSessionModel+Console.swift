import AppKit
import DeskhandClient
import Foundation

// MARK: Console

extension DebugSessionModel {
    /// Reads the latest page the first time, then everything after the last `seq` held.
    func loadConsole() async {
        let first = consolePhase != .loaded && console.entries.isEmpty
        if first { consolePhase = .loading }
        flushConsole()
        var cursor: Int? = first ? nil : console.cursor
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

    /// Clears the console here at once and on the daemon for every client (⌘L).
    public func clearConsole() {
        applyConsoleCleared(through: console.cursor)
        let backend = backend
        let debugID = id
        Task { [weak self] in
            do {
                let through = try await backend.debugConsoleClear(debugID: debugID)
                self?.applyConsoleCleared(through: through)
            } catch {
                self?.show(.error, .ui("debugger.failed.consoleClear", "데몬의 콘솔 기록을 지우지 못했습니다"), error: OperationError(error))
            }
        }
    }

    /// Evaluates `expression` in the selected frame while paused, globally otherwise, and records it.
    public func evaluate(_ expression: String) {
        let trimmed = expression.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, !isClosed, let targetID = selectedTargetID else { return }
        history.record(trimmed)
        flushConsole()
        let frame = pauseState == nil ? nil : selectedFrame
        let frameName = frame.map { DebugFormat.frameName($0.function) }
        let record = console.addEvaluation(expression: trimmed, targetID: targetID, frame: frameName)
        let backend = backend
        let debugID = id
        let epoch = consoleEpoch
        Task { [weak self] in
            do {
                let result = try await backend.debugEvaluate(debugID: debugID, targetID: targetID, expression: trimmed, frameID: frame?.frameID)
                guard let self, epoch == consoleEpoch else { return }
                console.resolveEvaluation(record, .value(result))
                consoleObjects.addRoot(path: ["x\(record)"], title: trimmed, value: result.value, targetID: targetID)
            } catch {
                self?.console.resolveEvaluation(record, .failure(OperationError(error)))
            }
        }
    }

    /// Selects a console row: alone, added or removed with ⌘, or the range from the last
    /// selected row with ⇧.
    public func selectConsoleItem(_ id: String, extending: Bool, toggling: Bool) {
        let ids = console.items(consoleFilter).map(\.id)
        if toggling {
            if consoleSelection.contains(id) { consoleSelection.remove(id) } else { consoleSelection.insert(id) }
        } else if extending, let anchor = ids.lastIndex(where: { consoleSelection.contains($0) }), let index = ids.firstIndex(of: id) {
            consoleSelection.formUnion(ids[min(anchor, index)...max(anchor, index)])
        } else {
            consoleSelection = [id]
        }
    }

    /// The text of the selected console rows in order, one per line.
    public var selectedConsoleText: String {
        console.items(consoleFilter).filter { consoleSelection.contains($0.id) }.map(Self.text(of:)).joined(separator: "\n")
    }

    /// Copies the selected console rows (⌘C).
    public func copyConsoleSelection() {
        let text = selectedConsoleText
        guard !text.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }

    static func text(of item: ConsoleItem) -> String {
        switch item {
        case .entry(let entry):
            var line = entry.text
            if let url = entry.url, let lineNumber = entry.line { line += "  " + DebugFormat.location(url: url, line: lineNumber) }
            for frame in entry.stack ?? [] where entry.consoleLevel == .error {
                line += "\n    " + DebugFormat.stackLine(frame)
            }
            return line
        case .evaluation(let record):
            let result: String = switch record.result {
            case .pending: ""
            case .value(let evaluation): evaluation.exception?.text ?? evaluation.value.valuePreview
            case .failure(let error): error.message
            }
            return "> " + record.expression + "\n< " + result
        case .gap:
            return String.ui("debugger.console.gap", "— 받지 못한 메시지가 있습니다 —")
        }
    }
}
