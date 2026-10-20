import AppKit
import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

/// The debugger tab's value types: console log, evaluate history, variable tree, pause order,
/// breakpoint patterns, argument splitting, source text, previews, script search and keys.
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
        log.merge([entry(4), entry(5)])
        XCTAssertEqual(log.entries.map(\.seq), [1, 2, 3, 4, 5])
    }

    func testConsoleTrimsToCapacityAndRemembersEarlierEntries() {
        var log = ConsoleLog(capacity: 3)
        log.merge((1...5).map { entry($0) })
        XCTAssertEqual(log.entries.map(\.seq), [3, 4, 5])
        XCTAssertTrue(log.hasEarlier)
        log.merge([entry(6, .error)])
        XCTAssertEqual(log.entries.map(\.seq), [4, 5, 6])
        XCTAssertEqual(log.counts(ConsoleFilter()), [.log: 2, .error: 1])
    }

    func testConsolePagesSetCursorEarlierFlagAndGaps() {
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

        log.apply(ConsolePage(entries: [entry(90), entry(91)], nextCursor: 91, firstSeq: 90, gap: true))
        XCTAssertEqual(log.gaps, [90])
        let items = log.items(ConsoleFilter())
        XCTAssertEqual(items.map(\.id), ["e40", "e41", "e42", "gap90", "e90", "e91"])

        var empty = ConsoleLog()
        empty.apply(ConsolePage(entries: [], nextCursor: 17))
        XCTAssertEqual(empty.cursor, 17)
    }

    func testConsoleFilterByLevelTextAndTargetWithIncrementalCounts() {
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
        filter.query = "TYPEERROR"
        XCTAssertEqual(log.visible(filter).map(\.seq), [3])
        filter.query = ""
        filter.targetID = "main"
        XCTAssertEqual(log.visible(filter).map(\.seq), [1])
        XCTAssertEqual(log.counts(ConsoleFilter()), [.info: 1, .warning: 1, .error: 1, .debug: 1])
        XCTAssertEqual(log.counts(ConsoleFilter(targetID: "main")), [.info: 1])
    }

    func testClearHidesEverythingAndEarlierPagesDoNotBringItBack() {
        var log = ConsoleLog()
        log.apply(ConsolePage(entries: [entry(41), entry(42)], nextCursor: 42, hasEarlier: true, dropped: 5))
        _ = log.addEvaluation(expression: "1", targetID: "page", frame: nil)
        log.clear(through: 42)
        XCTAssertEqual(log.items(ConsoleFilter()), [])
        XCTAssertFalse(log.hasEarlier)
        XCTAssertEqual(log.dropped, 0)
        XCTAssertEqual(log.counts(ConsoleFilter()), [:])
        log.apply(ConsolePage(entries: [entry(39), entry(40)], nextCursor: 40, hasEarlier: true), earlier: true)
        XCTAssertEqual(log.entries, [])
        XCTAssertFalse(log.hasEarlier)
        log.merge([entry(43, .log, "새 줄")])
        XCTAssertEqual(log.visible(ConsoleFilter()).map(\.seq), [43])
        XCTAssertEqual(log.cursor, 43)
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
        XCTAssertEqual(record.targetID, "page")
        XCTAssertEqual(record.result, .value(DebugEvaluation(value: RemoteValue(type: "number", valuePreview: "46800"))))
    }

    func testAppendingTwoHundredEntriesASecondForTenSecondsStaysCheapPerFrame() {
        var log = ConsoleLog()
        let filter = ConsoleFilter(query: "주문")
        var seq = 0
        var worst = 0.0
        let clock = ContinuousClock()
        for frame in 0..<600 {
            var batch: [ConsoleEntry] = []
            for _ in 0..<(frame % 3 == 0 ? 4 : 3) {
                seq += 1
                batch.append(entry(seq, seq % 7 == 0 ? .error : .log, seq % 5 == 0 ? "주문 제출 \(seq)" : "렌더 \(seq)"))
            }
            let elapsed = clock.measure {
                log.merge(batch)
                _ = log.counts(filter)
                _ = log.items(filter).count
            }
            worst = max(worst, Double(elapsed.components.attoseconds) / 1e15 + Double(elapsed.components.seconds) * 1000)
        }
        XCTAssertEqual(seq, 2000)
        XCTAssertEqual(log.entries.count, 2000)
        XCTAssertLessThan(worst, 16, "worst frame \(worst) ms")
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
        tree.reset(frame: frame(gen: 1), gen: 1, targetID: "page")
        XCTAssertEqual(tree.pendingLoads.map(\.objectID), ["L1"])
        XCTAssertEqual(tree.pendingLoads.first?.targetID, "page")
        XCTAssertEqual(tree.rows.map(\.kind), [.scope, .loading, .scope, .scope])
        tree.beginLoading("L1")
        XCTAssertEqual(tree.pendingLoads.map(\.objectID), [])
        tree.finishLoading("L1", .success(DebugPropertyPage(properties: [object("order", "O1"), number("count", 3)])))
        XCTAssertEqual(tree.rows.map(\.title), ["지역", "order", "count", "클로저 api.js", "전역"])
        let order = tree.rows[1]
        XCTAssertTrue(order.isExpandable)
        XCTAssertFalse(order.isExpanded)
        tree.toggle(order.path)
        XCTAssertEqual(tree.pendingLoads.map(\.objectID), ["O1"])
        tree.beginLoading("O1")
        tree.finishLoading("O1", .success(DebugPropertyPage(properties: [number("total", 46800)])))
        XCTAssertEqual(tree.rows.map(\.title), ["지역", "order", "total", "count", "클로저 api.js", "전역"])
        XCTAssertEqual(tree.rows[2].depth, 2)
        tree.toggle(order.path)
        XCTAssertEqual(tree.rows.map(\.title), ["지역", "order", "count", "클로저 api.js", "전역"])
        tree.toggle(order.path)
        XCTAssertEqual(tree.pendingLoads.map(\.objectID), [], "loaded children are kept")
    }

    func testVariablePathsDoNotCollideOnNamesWithSlashes() {
        var tree = VariableTree()
        tree.reset(frame: frame(gen: 1), gen: 1, targetID: "page")
        tree.finishLoading("L1", .success(DebugPropertyPage(properties: [object("a/b", "AB"), object("a", "A")])))
        let slash = tree.rows[1]
        let plain = tree.rows[2]
        tree.toggle(plain.path)
        tree.finishLoading("A", .success(DebugPropertyPage(properties: [object("b", "B")])))
        XCTAssertNotEqual(slash.path, tree.rows.first { $0.title == "b" }?.path)
        XCTAssertFalse(tree.rows.first { $0.title == "a/b" }?.isExpanded ?? true)
        XCTAssertEqual(Set(tree.rows.map(\.id)).count, tree.rows.count)
    }

    func testVariableTreeKeepsExpandedPathsAcrossPausesWithNewObjectIDs() {
        var tree = VariableTree()
        tree.reset(frame: frame(gen: 1), gen: 1, targetID: "page")
        tree.finishLoading("L1", .success(DebugPropertyPage(properties: [object("order", "O1")])))
        tree.toggle(tree.rows[1].path)
        tree.finishLoading("O1", .success(DebugPropertyPage(properties: [number("total", 1)])))

        tree.reset(frame: frame(gen: 2), gen: 2, targetID: "page")
        XCTAssertEqual(tree.pendingLoads.map(\.objectID), ["L2"])
        tree.finishLoading("L2", .success(DebugPropertyPage(properties: [object("order", "O2")])))
        XCTAssertEqual(tree.pendingLoads.map(\.objectID), ["O2"])
        XCTAssertTrue(tree.rows[1].isExpanded)
    }

    func testVariableTreeShowsFailuresAndMorePages() {
        var tree = VariableTree()
        tree.reset(frame: frame(gen: 1), gen: 1, targetID: "page")
        tree.finishLoading("L1", .failure(OperationError(code: "STALE_TARGET", message: "개체가 무효입니다")))
        XCTAssertEqual(tree.rows.map(\.kind), [.scope, .failure, .scope, .scope])
        tree.retry("L1")
        XCTAssertEqual(tree.pendingLoads.map(\.objectID), ["L1"])
        tree.finishLoading("L1", .success(DebugPropertyPage(properties: (0..<100).map { number(String($0), $0) }, total: 250)))
        XCTAssertEqual(tree.rows.last(where: { $0.kind == .more })?.remaining, 150)
        XCTAssertEqual(tree.nextOffset("L1"), 100)
        XCTAssertTrue(tree.beginLoadingMore("L1"))
        XCTAssertFalse(tree.beginLoadingMore("L1"), "a page in flight is not asked for twice")
        tree.finishLoading("L1", .success(DebugPropertyPage(properties: [number("100", 100)], total: 250)), append: true)
        XCTAssertEqual(tree.nextOffset("L1"), 101)
        XCTAssertTrue(tree.beginLoadingMore("L1"))
    }

    func testConsoleRootsLoadFromTheTargetTheyWereEvaluatedIn() {
        var tree = VariableTree()
        tree.addRoot(path: ["x1"], title: "order", value: RemoteValue(type: "object", valuePreview: "{…}", objectID: "R1"), targetID: "main")
        tree.toggle(["x1"])
        XCTAssertEqual(tree.pendingLoads, [VariableTree.Load(objectID: "R1", targetID: "main")])
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
        XCTAssertEqual(book.gen(of: "page"), 6, "an announced pause counts once its state arrives")
        XCTAssertEqual(book.apply(ahead), .needsState)
        XCTAssertFalse(book.applyResumed(targetID: "page", gen: 8))
        XCTAssertTrue(book.apply(DebugPauseState(targetID: "page", gen: 9, paused: true, reason: .pauseRequest)))
        XCTAssertEqual(book.gen(of: "page"), 9)
        XCTAssertEqual(book.apply(DebugTarget(id: "page", role: "renderer", type: "page", url: "u", title: "t", paused: false, gen: 9)), .unchanged)
        XCTAssertEqual(book.apply(DebugTarget(id: "page", role: "renderer", type: "page", url: "u", title: "t", paused: false, gen: 10)), .resumed)
    }

    // MARK: Breakpoint patterns

    func testBreakpointPatternsBecomeSpecsAndMatchLikeTheDaemon() throws {
        XCTAssertEqual(try BreakpointPattern.fileName("checkout.js").spec(line: 42),
                       BreakpointSpec(match: .fileName, pattern: "checkout.js", line: 42))
        XCTAssertEqual(try BreakpointPattern.url("app://shop/api.js").spec(line: 1).matchKind, .url)
        XCTAssertEqual(try BreakpointPattern.regex("api(").spec(line: 1).pattern, "api(", "the daemon validates url_regex")
        XCTAssertThrowsError(try BreakpointPattern.fileName("  ").spec(line: 1))

        let byName = DebugBreakpoint(breakpointID: "1", match: .fileName, pattern: "checkout.js", line: 42)
        XCTAssertEqual(BreakpointPattern.title(of: byName), "checkout.js:42")
        XCTAssertTrue(BreakpointPattern.matches(byName, url: "app://shop/renderer/checkout.js"))
        XCTAssertTrue(BreakpointPattern.matches(byName, url: "app://shop/renderer/checkout.js?v=3#top"))
        XCTAssertTrue(BreakpointPattern.matches(byName, url: "C:\\Shop\\checkout.js"))
        XCTAssertFalse(BreakpointPattern.matches(byName, url: "app://shop/renderer/mycheckout.js"))
        let byPath = DebugBreakpoint(breakpointID: "4", match: .fileName, pattern: "renderer/checkout.js", line: 1)
        XCTAssertTrue(BreakpointPattern.matches(byPath, url: "app://shop/renderer/checkout.js"))
        XCTAssertFalse(BreakpointPattern.matches(byPath, url: "app://shop/main/checkout.js"))
        let byURL = DebugBreakpoint(breakpointID: "2", match: .url, pattern: "app://shop/renderer/api.js", line: 7)
        XCTAssertEqual(BreakpointPattern.title(of: byURL), "api.js:7")
        XCTAssertTrue(BreakpointPattern.matches(byURL, url: "app://shop/renderer/api.js"))
        let byRegex = DebugBreakpoint(breakpointID: "3", match: .urlRegex, pattern: "renderer/.*\\.js$", line: 1)
        XCTAssertEqual(BreakpointPattern.title(of: byRegex), "/renderer/.*\\.js$/:1")
        var moved = byName
        moved.locations = [BreakpointLocation(targetID: "t", scriptID: "88", url: "app://shop/renderer/checkout.js", line: 44, column: 3)]
        XCTAssertEqual(BreakpointPattern.title(of: moved), "checkout.js:44")
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

    // MARK: Debugger keys

    func testCommandAlternatesMapToExecutionControlsUnlessTheConsoleIsTyping() {
        XCTAssertEqual(DebuggerKeys.action(characters: "\\", modifiers: [.command], consoleFocused: false), .togglePause)
        XCTAssertEqual(DebuggerKeys.action(characters: "'", modifiers: [.command], consoleFocused: false), .step(.over))
        XCTAssertEqual(DebuggerKeys.action(characters: ";", modifiers: [.command], consoleFocused: false), .step(.into))
        XCTAssertEqual(DebuggerKeys.action(characters: ";", modifiers: [.command, .shift], consoleFocused: false), .step(.out))
        XCTAssertEqual(DebuggerKeys.action(characters: ":", modifiers: [.command, .shift], consoleFocused: false), .step(.out))
        XCTAssertNil(DebuggerKeys.action(characters: ";", modifiers: [.command], consoleFocused: true))
        XCTAssertNil(DebuggerKeys.action(characters: ";", modifiers: [.command, .option], consoleFocused: false))
        XCTAssertNil(DebuggerKeys.action(characters: "k", modifiers: [.command], consoleFocused: false))
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

    // MARK: Source text

    func testSourceLinesSplitOnEveryECMAScriptTerminator() {
        let text = SourceText(text: "a\nb\r\nc\rd\u{2028}e\u{2029}f")
        XCTAssertEqual(text.lines, ["a", "b", "c", "d", "e", "f"])
        XCTAssertEqual(SourceText(text: "x\n").lines, ["x", ""])
    }

    func testLongLinesShowAWindowAroundTheColumnWithElisionCounts() {
        let line = String(repeating: "a", count: 10_000) + "PAUSE" + String(repeating: "b", count: 10_000)
        let window = SourceText.window(line, around: 10_001, width: 2000)
        XCTAssertEqual(window.hiddenBefore, 9_001)
        XCTAssertEqual(window.hiddenAfter, 20_005 - 9_001 - 2000)
        XCTAssertTrue(window.text.contains("PAUSE"))
        XCTAssertEqual(window.text.utf16.count, 2000)
        XCTAssertEqual(window.column, 10_001 - 9_001)
        let short = SourceText.window("const a = 1;", around: 3, width: 2000)
        XCTAssertEqual(short.hiddenBefore, 0)
        XCTAssertEqual(short.hiddenAfter, 0)
        XCTAssertEqual(short.text, "const a = 1;")
    }

    func testAPausedLineOfATwoMegabyteScriptRendersWithoutTokenizingTheWholeLine() {
        let body = (0..<105_000).map { "var v\($0)=\"s\($0)\";" }.joined()
        let source = SourceText(text: body)
        XCTAssertGreaterThan(source.lines[0].utf16.count, 2_000_000)
        let column = source.lines[0].utf16.count / 2
        _ = source.highlightedWindow(line: 1, around: column, width: 2000)
        var best = Double.infinity
        let clock = ContinuousClock()
        for run in 0..<5 {
            let other = SourceText(text: body)
            let elapsed = clock.measure { _ = other.highlightedWindow(line: 1, around: column + run, width: 2000) }
            best = min(best, Double(elapsed.components.attoseconds) / 1e15 + Double(elapsed.components.seconds) * 1000)
        }
        XCTAssertLessThan(best, 50, "best of five \(best) ms")
        XCTAssertTrue(source.highlightedWindow(line: 1, around: column, width: 2000) == source.highlightedWindow(line: 1, around: column, width: 2000))
    }

    // MARK: Highlighting

    func testHighlighterSplitsKeywordsStringsNumbersAndComments() {
        let runs = JavaScriptHighlighter.tokens("const total = 'a\\'b' + 42; // 합계")
        XCTAssertEqual(runs.map(\.token), [.keyword, .plain, .string, .plain, .number, .plain, .comment])
        XCTAssertEqual(runs.map(\.text).joined(), "const total = 'a\\'b' + 42; // 합계")
        XCTAssertEqual(JavaScriptHighlighter.tokens("order.return").map(\.token), [.plain])
        XCTAssertEqual(JavaScriptHighlighter.tokens("x2 = item3").map(\.token), [.plain])
    }

    func testHighlighterHandlesBlockCommentsRegexLiteralsAndNumberForms() {
        let doc = JavaScriptHighlighter.tokens("/** Order total.", inComment: false)
        XCTAssertEqual(doc.runs.map(\.token), [.comment])
        XCTAssertTrue(doc.endsInComment)
        let middle = JavaScriptHighlighter.tokens(" * @param {Order} order", inComment: true)
        XCTAssertEqual(middle.runs.map(\.token), [.comment])
        let end = JavaScriptHighlighter.tokens(" */ const x = 0x1F + 12n;", inComment: true)
        XCTAssertEqual(end.runs.map(\.token), [.comment, .plain, .keyword, .plain, .number, .plain, .number, .plain])
        XCTAssertFalse(end.endsInComment)
        let regex = JavaScriptHighlighter.tokens("const re = /a\\/b[/]c/gi; const d = a / b / c;")
        XCTAssertEqual(regex.filter { $0.token == .regex }.map(\.text), ["/a\\/b[/]c/gi"])
        XCTAssertTrue(regex.contains { $0.text == " d = a / b / c;" && $0.token == .plain })
        XCTAssertEqual(JavaScriptHighlighter.tokens("1_000 + 1e-3 + 0b101").filter { $0.token == .number }.map(\.text), ["1_000", "1e-3", "0b101"])
    }

    func testSourceAnchorPutsTheLineAThirdDownOnARowBoundary() {
        XCTAssertEqual(SourceLines.offset(target: 100, content: 50, viewport: 300, row: 18), 0)
        XCTAssertEqual(SourceLines.offset(target: 50, content: 1000, viewport: 300, row: 18), 0)
        XCTAssertEqual(SourceLines.offset(target: 600, content: 1000, viewport: 300, row: 18), 486)
        XCTAssertEqual(SourceLines.offset(target: 990, content: 1000, viewport: 300, row: 18), 700)
    }

    // MARK: Previews

    func testPreviewCandidatesDropTrailingFieldsWithoutDoubleEllipses() {
        XCTAssertEqual(ValuePreview.candidates("{id: \"O-1\", customer: {…}, lines: Array(3), total: 46800}"), [
            "{id: \"O-1\", customer: {…}, lines: Array(3), total: 46800}",
            "{id: \"O-1\", customer: {…}, lines: Array(3), …}",
            "{id: \"O-1\", customer: {…}, …}",
            "{id: \"O-1\", …}",
            "{…}",
        ])
        XCTAssertEqual(ValuePreview.candidates("Array(3) [{…}, {…}, {…}]"),
                       ["Array(3) [{…}, {…}, {…}]", "Array(3) [{…}, {…}, …]", "Array(3) [{…}, …]", "Array(3) […]", "Array(3)"])
        XCTAssertEqual(ValuePreview.candidates("{a: \"x, y\", b: 1}"), ["{a: \"x, y\", b: 1}", "{a: \"x, y\", …}", "{…}"])
        XCTAssertEqual(ValuePreview.candidates("{id: 1, …}"), ["{id: 1, …}", "{…}"])
        XCTAssertEqual(ValuePreview.candidates("46800"), ["46800"])
        XCTAssertEqual(ValuePreview.candidates("\"서울\""), ["\"서울\""])
        for candidate in ValuePreview.candidates("Order {id: \"O-1\", nested: {a: [1, 2]}, …}") {
            XCTAssertFalse(candidate.contains("……"), candidate)
        }
    }

    // MARK: Script search

    func testScriptSearchMatchesFileNamesFuzzilyAndRanksTightMatchesFirst() {
        let urls = ["app://shop/renderer/checkout.js", "app://shop/renderer/api.js", "app://shop/renderer/cart-helpers.js",
                    "node:internal/modules/cjs/loader", "file:///C:/Shop/desktop/resources/app/main.js"]
        XCTAssertEqual(ScriptSearch.rank(urls, query: "chk").map(\.url), ["app://shop/renderer/checkout.js"])
        XCTAssertEqual(ScriptSearch.rank(urls, query: "c").map(\.url), ["app://shop/renderer/checkout.js", "app://shop/renderer/cart-helpers.js"])
        XCTAssertEqual(ScriptSearch.rank(urls, query: "api").map(\.url), ["app://shop/renderer/api.js"])
        XCTAssertEqual(ScriptSearch.rank(urls, query: "").count, 4, "node:internal scripts stay hidden")
        XCTAssertEqual(ScriptSearch.rank(urls, query: "", includeInternal: true).count, 5)
        XCTAssertEqual(ScriptSearch.rank(urls, query: "CHECK").first?.url, "app://shop/renderer/checkout.js")
    }
}
