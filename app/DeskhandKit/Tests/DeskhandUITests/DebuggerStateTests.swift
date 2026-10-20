import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

/// The debugger tab's value types: console log, evaluate history, variable tree, pause order,
/// breakpoint patterns and argument splitting.
final class DebuggerStateTests: XCTestCase {
    private let t0 = Date(timeIntervalSince1970: 1_790_000_000)

    private func entry(_ seq: Int, _ level: ConsoleLevel = .log, _ text: String = "줄", target: String = "page") -> ConsoleEntry {
        ConsoleEntry(seq: seq, time: Timestamp(date: t0.addingTimeInterval(Double(seq))), targetID: target, level: level, text: text)
    }

    // MARK: Console

    func testConsoleMergeKeepsSeqOrderAndDropsDuplicates() {
        var log = ConsoleLog()
        log.merge([entry(3), entry(1)])
        log.merge([entry(2), entry(3, .error, "다시 온 3")])
        XCTAssertEqual(log.entries.map(\.seq), [1, 2, 3])
        XCTAssertEqual(log.entries.last?.text, "줄")
        XCTAssertEqual(log.cursor, 3)
    }

    func testConsoleTrimsToCapacityAndRemembersEarlierEntries() {
        var log = ConsoleLog(capacity: 3)
        log.merge((1...5).map { entry($0) })
        XCTAssertEqual(log.entries.map(\.seq), [3, 4, 5])
        XCTAssertTrue(log.hasEarlier)
    }

    func testConsolePagesSetCursorAndEarlierFlag() {
        var log = ConsoleLog()
        log.apply(ConsolePage(entries: [entry(41), entry(42)], nextCursor: 42, hasEarlier: true, dropped: 2))
        XCTAssertEqual(log.cursor, 42)
        XCTAssertEqual(log.firstSeq, 41)
        XCTAssertTrue(log.hasEarlier)
        XCTAssertEqual(log.dropped, 2)
        log.apply(ConsolePage(entries: [entry(40)], nextCursor: 40, hasEarlier: false), earlier: true)
        XCTAssertEqual(log.entries.map(\.seq), [40, 41, 42])
        XCTAssertFalse(log.hasEarlier)
        XCTAssertEqual(log.cursor, 42)

        var empty = ConsoleLog()
        empty.apply(ConsolePage(entries: [], nextCursor: 17))
        XCTAssertEqual(empty.cursor, 17)
    }

    func testConsoleFilterByLevelTextAndTargetAndClear() {
        var log = ConsoleLog()
        log.merge([entry(1, .info, "앱 시작", target: "main"), entry(2, .warning, "쿠폰 응답이 느립니다"), entry(3, .error, "TypeError"),
                   entry(4, .debug, "장바구니 3개")])
        var filter = ConsoleFilter()
        XCTAssertEqual(log.visible(filter).map(\.seq), [1, 2, 3])
        filter.levels = [.error, .warning]
        XCTAssertEqual(log.visible(filter).map(\.seq), [2, 3])
        filter.levels = Set(ConsoleLevel.allCases)
        filter.query = "쿠폰"
        XCTAssertEqual(log.visible(filter).map(\.seq), [2])
        filter.query = ""
        filter.targetID = "main"
        XCTAssertEqual(log.visible(filter).map(\.seq), [1])
        XCTAssertEqual(log.counts(ConsoleFilter())[.error], 1)

        log.clear()
        XCTAssertEqual(log.visible(ConsoleFilter()), [])
        log.merge([entry(5, .log, "새 줄")])
        XCTAssertEqual(log.visible(ConsoleFilter()).map(\.seq), [5])
    }

    func testEvaluationsSitAfterTheEntryThatWasLatestWhenTheyRan() {
        var log = ConsoleLog()
        log.merge([entry(1), entry(2)])
        let id = log.addEvaluation(expression: "order.total", targetID: "page", frame: "postOrder")
        log.merge([entry(3)])
        log.resolveEvaluation(id, .value(DebugEvaluation(value: RemoteValue(type: "number", valuePreview: "46800"))))
        let items = log.items(ConsoleFilter())
        XCTAssertEqual(items.map(\.id), ["e1", "e2", "x\(id)", "e3"])
        guard case .evaluation(let record) = items[2] else { return XCTFail("evaluation row") }
        XCTAssertEqual(record.expression, "order.total")
        XCTAssertEqual(record.result, .value(DebugEvaluation(value: RemoteValue(type: "number", valuePreview: "46800"))))
    }

    // MARK: History

    func testHistoryWalksBackAndForthAndRestoresTheDraft() {
        var history = EvaluateHistory()
        history.record("order")
        history.record("  order.total ")
        history.record("order.total")
        history.record("")
        XCTAssertEqual(history.items, ["order", "order.total"])
        XCTAssertEqual(history.previous(current: "ord"), "order.total")
        XCTAssertEqual(history.previous(current: "order.total"), "order")
        XCTAssertNil(history.previous(current: "order"))
        XCTAssertEqual(history.next(), "order.total")
        XCTAssertEqual(history.next(), "ord")
        XCTAssertNil(history.next())
    }

    func testHistoryKeepsTheLatestHundred() {
        var history = EvaluateHistory()
        for i in 0..<120 { history.record("x\(i)") }
        XCTAssertEqual(history.items.count, EvaluateHistory.limit)
        XCTAssertEqual(history.items.first, "x20")
    }

    // MARK: Variables

    private func frame(gen: Int) -> DebugCallFrame {
        DebugCallFrame(frameID: "cf-\(gen)", function: "postOrder", scriptID: "13", url: "app://shop/api.js", line: 20, column: 3,
                       scopeChain: [DebugScope(type: "local", objectID: "L\(gen)"), DebugScope(type: "closure", objectID: "C\(gen)", name: "api.js"),
                                    DebugScope(type: "global", objectID: "G\(gen)")])
    }

    private func object(_ name: String, _ id: String) -> DebugProperty {
        DebugProperty(name: name, value: RemoteValue(type: "object", className: "Object", valuePreview: "Object", objectID: id))
    }

    private func number(_ name: String, _ value: Int) -> DebugProperty {
        DebugProperty(name: name, value: RemoteValue(type: "number", valuePreview: String(value)))
    }

    func testVariableTreeExpandsTheFirstScopeAndLoadsLazily() {
        var tree = VariableTree()
        tree.reset(frame: frame(gen: 1), gen: 1)
        XCTAssertEqual(tree.pendingLoads, ["L1"])
        XCTAssertEqual(tree.rows.map(\.kind), [.scope, .loading, .scope, .scope])
        tree.beginLoading("L1")
        XCTAssertEqual(tree.pendingLoads, [])
        tree.finishLoading("L1", .success(DebugPropertyPage(properties: [object("order", "O1"), number("count", 3)])))
        XCTAssertEqual(tree.rows.map(\.title), ["지역", "order", "count", "클로저 api.js", "전역"])
        let order = tree.rows[1]
        XCTAssertTrue(order.isExpandable)
        XCTAssertFalse(order.isExpanded)
        tree.toggle(order.path)
        XCTAssertEqual(tree.pendingLoads, ["O1"])
        tree.beginLoading("O1")
        tree.finishLoading("O1", .success(DebugPropertyPage(properties: [number("total", 46800)])))
        XCTAssertEqual(tree.rows.map(\.title), ["지역", "order", "total", "count", "클로저 api.js", "전역"])
        XCTAssertEqual(tree.rows[2].depth, 2)
        tree.toggle(order.path)
        XCTAssertEqual(tree.rows.map(\.title), ["지역", "order", "count", "클로저 api.js", "전역"])
        tree.toggle(order.path)
        XCTAssertEqual(tree.pendingLoads, [], "loaded children are kept")
    }

    func testVariableTreeKeepsExpandedPathsAcrossPausesWithNewObjectIDs() {
        var tree = VariableTree()
        tree.reset(frame: frame(gen: 1), gen: 1)
        tree.finishLoading("L1", .success(DebugPropertyPage(properties: [object("order", "O1")])))
        tree.toggle(tree.rows[1].path)
        tree.finishLoading("O1", .success(DebugPropertyPage(properties: [number("total", 1)])))

        tree.reset(frame: frame(gen: 2), gen: 2)
        XCTAssertEqual(tree.pendingLoads, ["L2"])
        tree.finishLoading("L2", .success(DebugPropertyPage(properties: [object("order", "O2")])))
        XCTAssertEqual(tree.pendingLoads, ["O2"])
        XCTAssertTrue(tree.rows[1].isExpanded)
    }

    func testVariableTreeShowsFailuresAndMorePages() {
        var tree = VariableTree()
        tree.reset(frame: frame(gen: 1), gen: 1)
        tree.finishLoading("L1", .failure(OperationError(code: "STALE_TARGET", message: "개체가 무효입니다")))
        XCTAssertEqual(tree.rows.map(\.kind), [.scope, .failure, .scope, .scope])
        tree.retry("L1")
        XCTAssertEqual(tree.pendingLoads, ["L1"])
        tree.finishLoading("L1", .success(DebugPropertyPage(properties: (0..<100).map { number("[\($0)]", $0) }, total: 250)))
        XCTAssertEqual(tree.rows.last(where: { $0.kind == .more })?.remaining, 150)
        XCTAssertEqual(tree.nextOffset("L1"), 100)
        tree.finishLoading("L1", .success(DebugPropertyPage(properties: [number("[100]", 100)], total: 250)), append: true)
        XCTAssertEqual(tree.nextOffset("L1"), 101)
    }

    // MARK: Pause order

    func testPauseBookAppliesOnlyLargerGens() {
        var book = PauseBook()
        let paused = DebugPauseState(targetID: "page", gen: 5, paused: true, reason: .breakpoint)
        XCTAssertTrue(book.apply(paused))
        XCTAssertFalse(book.applyResumed(targetID: "page", gen: 4))
        XCTAssertEqual(book.state(for: "page")?.paused, true)
        XCTAssertFalse(book.apply(DebugPauseState(targetID: "page", gen: 5, paused: true, reason: .step)))
        XCTAssertEqual(book.state(for: "page")?.pauseReason, .breakpoint)
        XCTAssertTrue(book.applyResumed(targetID: "page", gen: 6))
        XCTAssertEqual(book.state(for: "page")?.paused, false)

        let ahead = DebugTarget(id: "page", role: "renderer", type: "page", url: "u", title: "t", paused: true, gen: 9)
        XCTAssertEqual(book.apply(ahead), .needsState)
        XCTAssertEqual(book.apply(DebugTarget(id: "page", role: "renderer", type: "page", url: "u", title: "t", paused: false, gen: 9)), .unchanged)
        XCTAssertEqual(book.apply(DebugTarget(id: "page", role: "renderer", type: "page", url: "u", title: "t", paused: false, gen: 10)), .resumed)
    }

    // MARK: Breakpoint patterns

    func testBreakpointPatternsBecomeURLOrRegexMatches() throws {
        XCTAssertEqual(try BreakpointPattern.fileName("checkout.js").match(), .regex(#"(^|[/\\])checkout\.js$"#))
        XCTAssertEqual(try BreakpointPattern.url("app://shop/api.js").match(), .url("app://shop/api.js"))
        XCTAssertEqual(try BreakpointPattern.regex("api\\.js$").match(), .regex("api\\.js$"))
        XCTAssertThrowsError(try BreakpointPattern.regex("api(").match())
        XCTAssertThrowsError(try BreakpointPattern.fileName("  ").match())

        let byName = DebugBreakpoint(breakpointID: "1", urlRegex: #"(^|[/\\])checkout\.js$"#, line: 42)
        XCTAssertEqual(BreakpointPattern.title(of: byName), "checkout.js:42")
        let byURL = DebugBreakpoint(breakpointID: "2", url: "app://shop/renderer/api.js", line: 7)
        XCTAssertEqual(BreakpointPattern.title(of: byURL), "api.js:7")
        let byRegex = DebugBreakpoint(breakpointID: "3", urlRegex: "renderer/.*\\.js$", line: 1)
        XCTAssertEqual(BreakpointPattern.title(of: byRegex), "/renderer/.*\\.js$/:1")
        XCTAssertTrue(BreakpointPattern.matches(byName, url: "app://shop/renderer/checkout.js"))
        XCTAssertFalse(BreakpointPattern.matches(byName, url: "app://shop/renderer/mycheckout.js"))
        XCTAssertTrue(BreakpointPattern.matches(byURL, url: "app://shop/renderer/api.js"))
    }

    // MARK: Arguments

    func testArgumentsSplitOnSpacesAndKeepQuotedText() {
        XCTAssertEqual(ArgumentSplitter.split(#"--env=staging  --profile "C:\Shop Data\p1" 'a b'"#),
                       ["--env=staging", "--profile", #"C:\Shop Data\p1"#, "a b"])
        XCTAssertEqual(ArgumentSplitter.split("   "), [])
        XCTAssertEqual(ArgumentSplitter.join(["--env=staging", "C:\\Shop Data"]), #"--env=staging "C:\Shop Data""#)
    }

    // MARK: Console input keys

    func testConsoleInputKeysSubmitBreakLinesAndWalkHistory() {
        XCTAssertEqual(ConsoleInputKeys.action(for: .newline, text: "order", caret: 5, shift: false), .submit)
        XCTAssertEqual(ConsoleInputKeys.action(for: .newline, text: "order", caret: 5, shift: true), .insertNewline)
        XCTAssertEqual(ConsoleInputKeys.action(for: .newline, text: "  ", caret: 2, shift: false), .ignore)
        XCTAssertEqual(ConsoleInputKeys.action(for: .up, text: "a\nb", caret: 0, shift: false), .previous)
        XCTAssertEqual(ConsoleInputKeys.action(for: .up, text: "a\nb", caret: 3, shift: false), .standard)
        XCTAssertEqual(ConsoleInputKeys.action(for: .down, text: "a\nb", caret: 3, shift: false), .next)
        XCTAssertEqual(ConsoleInputKeys.action(for: .down, text: "a\nb", caret: 1, shift: false), .standard)
        XCTAssertEqual(ConsoleInputKeys.action(for: .up, text: "", caret: 0, shift: false), .previous)
    }

    // MARK: Attach candidates

    func testAttachCandidatesListElectronAppsAndNodeFirstWithoutHelpersOrBrowsers() {
        let processes = PreviewFixtures.processes(hostID: PreviewFixtures.devPCID, user: "taeyoung")
        let likely = AttachCandidate.list(processes, all: false)
        XCTAssertEqual(likely.map(\.process.name), ["Code.exe", "node.exe", "node.exe"])
        XCTAssertEqual(likely.first?.kind, .electron)
        XCTAssertFalse(likely.contains { $0.process.cmd?.contains("--type=") ?? false })
        let all = AttachCandidate.list(processes, all: true)
        XCTAssertEqual(Array(all.prefix(3)).map(\.process.pid), likely.map(\.process.pid))
        XCTAssertTrue(all.contains { $0.process.name == "chrome.exe" && $0.kind == .other })
        XCTAssertFalse(all.contains { $0.process.isSelf })
        XCTAssertEqual(AttachCandidate.list(processes, all: false, query: "vite").map(\.process.pid), [9272])
    }

    // MARK: Source highlighting

    func testHighlighterSplitsKeywordsStringsNumbersAndComments() {
        let runs = JavaScriptHighlighter.tokens("const total = 'a\\'b' + 42; // 합계")
        XCTAssertEqual(runs.map(\.token), [.keyword, .plain, .string, .plain, .number, .plain, .comment])
        XCTAssertEqual(runs.map(\.text).joined(), "const total = 'a\\'b' + 42; // 합계")
        XCTAssertEqual(JavaScriptHighlighter.tokens("order.return").map(\.token), [.plain])
        XCTAssertEqual(JavaScriptHighlighter.tokens("x2 = item3").map(\.token), [.plain])
    }

    func testSourceAnchorPutsTheLineAThirdDown() {
        XCTAssertEqual(SourceLines.anchor(target: 100, content: 50, viewport: 300), 0)
        XCTAssertEqual(SourceLines.anchor(target: 50, content: 1000, viewport: 300), 0)
        XCTAssertEqual(SourceLines.anchor(target: 600, content: 1000, viewport: 300), 500.0 / 700.0, accuracy: 0.0001)
        XCTAssertEqual(SourceLines.anchor(target: 990, content: 1000, viewport: 300), 1)
    }
}
