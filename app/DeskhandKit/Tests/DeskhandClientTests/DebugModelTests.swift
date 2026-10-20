import Foundation
import XCTest
@testable import DeskhandClient

/// Debugger models decode the shapes of local-api.md §5 and §9.
final class DebugModelTests: XCTestCase {
    func testSessionDecodesTargetsBreakpointsAndSettings() throws {
        let session = try Wire.decode(DebugSession.self, from: Data(#"""
        {"debug_id":"d-1","host_id":"windows-dev","job_id":"j-9","kind":"launch","name":"Shop Admin",
         "path":"C:\\Shop\\desktop\\Shop Admin.exe","args":["--env=staging"],"pid":10244,
         "ports":{"inspect":52011,"remote_debugging":52012},"state":"paused",
         "targets":[{"id":"main-1","role":"main","type":"node","url":"file:///C:/Shop/main.js","title":"main","attached":true,"paused":false,"gen":0},
                    {"id":"page-1","role":"renderer","type":"page","url":"app://shop/index.html","title":"결제","attached":true,"paused":true,"gen":3}],
         "breakpoints":[{"breakpoint_id":"bp-1","url_regex":"checkout\\.js$","line":42,"condition":"order.total > 0","enabled":true,"resolved":true,
                         "locations":[{"target_id":"page-1","script_id":"88","url":"app://shop/checkout.js","line":42,"column":5}]}],
         "breakpoints_active":true,"pause_on_exceptions":"uncaught","created_at":"2026-10-20T10:00:00.000Z"}
        """#.utf8))
        XCTAssertEqual(session.debugID, "d-1")
        XCTAssertEqual(session.sessionKind, .launch)
        XCTAssertEqual(session.sessionState, .paused)
        XCTAssertEqual(session.ports.inspect, 52011)
        XCTAssertEqual(session.ports.remoteDebugging, 52012)
        XCTAssertEqual(session.targets.map(\.targetRole), [.main, .renderer])
        XCTAssertEqual(session.targets.last?.gen, 3)
        XCTAssertEqual(session.targets.last?.paused, true)
        XCTAssertEqual(session.breakpoints.first?.urlRegex, #"checkout\.js$"#)
        XCTAssertNil(session.breakpoints.first?.url)
        XCTAssertEqual(session.breakpoints.first?.locations.first?.scriptID, "88")
        XCTAssertEqual(session.exceptionMode, .uncaught)
        XCTAssertNil(session.closedAt)

        let minimal = try Wire.decode(DebugSession.self, from: Data(#"""
        {"debug_id":"d-2","host_id":"h","kind":"attach","name":"node","ports":{},"state":"closed","created_at":"2026-10-20T10:00:00.000Z",
         "close_reason":"process_exited","exit_code":1,"closed_at":"2026-10-20T10:05:00.000Z"}
        """#.utf8))
        XCTAssertEqual(minimal.args, [])
        XCTAssertEqual(minimal.targets, [])
        XCTAssertEqual(minimal.breakpointsActive, true)
        XCTAssertEqual(minimal.exceptionMode, .uncaught)
        XCTAssertEqual(minimal.closeKind, .processExited)
        XCTAssertEqual(minimal.exitCode, 1)
    }

    func testPauseStateDecodesFramesScopesAndException() throws {
        let state = try Wire.decode(DebugPauseState.self, from: Data(#"""
        {"target_id":"page-1","gen":5,"paused":true,"reason":"exception","hit_breakpoints":[],
         "call_frames":[{"frame_id":"cf-0","function":"","script_id":"89","url":"app://shop/api.js","line":33,"column":24,
                         "scope_chain":[{"type":"local","object_id":"o-1"},{"type":"closure","object_id":"o-2","name":"applyDiscount"}],
                         "this":{"type":"undefined","value_preview":"undefined","has_children":false}}],
         "exception":{"type":"object","subtype":"error","class_name":"TypeError","value_preview":"TypeError: Cannot read properties of undefined (reading 'total')",
                      "object_id":"o-9","has_children":true}}
        """#.utf8))
        XCTAssertEqual(state.pauseReason, .exception)
        XCTAssertEqual(state.callFrames.first?.function, "")
        XCTAssertEqual(state.callFrames.first?.scopeChain.map(\.type), ["local", "closure"])
        XCTAssertEqual(state.callFrames.first?.scopeChain.last?.name, "applyDiscount")
        XCTAssertEqual(state.callFrames.first?.thisValue?.type, "undefined")
        XCTAssertEqual(state.exception?.className, "TypeError")
        XCTAssertEqual(state.exception?.objectID, "o-9")

        let running = try Wire.decode(DebugPauseState.self, from: Data(#"{"target_id":"page-1","gen":6,"paused":false}"#.utf8))
        XCTAssertEqual(running.callFrames, [])
        XCTAssertEqual(running.hitBreakpoints, [])
        XCTAssertNil(running.pauseReason)
    }

    func testPropertyAndEvaluationDecodeFlattenedRemoteValues() throws {
        let page = try Wire.decode(DebugPropertyPage.self, from: Data(#"""
        {"properties":[{"name":"total","type":"number","value_preview":"46800","has_children":false,"kind":"property","writable":true},
                       {"name":"[[Prototype]]","type":"object","class_name":"Object","value_preview":"Object","object_id":"o-3","has_children":true,"kind":"internal"}],
         "total":7}
        """#.utf8))
        XCTAssertEqual(page.total, 7)
        XCTAssertEqual(page.properties.first?.name, "total")
        XCTAssertEqual(page.properties.first?.value.valuePreview, "46800")
        XCTAssertEqual(page.properties.last?.propertyKind, .internal)
        XCTAssertEqual(page.properties.last?.value.objectID, "o-3")

        let evaluation = try Wire.decode(DebugEvaluation.self, from: Data(#"""
        {"status":"succeeded","type":"object","subtype":"error","class_name":"ReferenceError","value_preview":"ReferenceError: totl is not defined",
         "object_id":"o-5","has_children":true,
         "exception":{"text":"Uncaught ReferenceError: totl is not defined","line":1,"column":1,"stack":[]}}
        """#.utf8))
        XCTAssertEqual(evaluation.value.className, "ReferenceError")
        XCTAssertEqual(evaluation.exception?.text, "Uncaught ReferenceError: totl is not defined")

        let property = DebugProperty(name: "n", kind: "property", value: RemoteValue(type: "number", valuePreview: "1"))
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: encoder.encode(property)) as? [String: Any])
        XCTAssertEqual(json["name"] as? String, "n")
        XCTAssertEqual(json["value_preview"] as? String, "1")
        XCTAssertEqual(json["has_children"] as? Bool, false)
        XCTAssertNil(json["value"])
    }

    func testConsolePageAndSourceDecode() throws {
        let page = try Wire.decode(ConsolePage.self, from: Data(#"""
        {"entries":[{"seq":12,"time":"2026-10-20T10:00:01.250Z","target_id":"page-1","source":"exception","level":"error",
                     "text":"Uncaught TypeError: x","url":"app://shop/api.js","line":33,"column":24,
                     "stack":[{"function":"applyDiscount","url":"app://shop/api.js","line":33,"column":24}]}],
         "next_cursor":12,"has_more":false,"has_earlier":true,"dropped":0}
        """#.utf8))
        XCTAssertEqual(page.nextCursor, 12)
        XCTAssertTrue(page.hasEarlier)
        XCTAssertEqual(page.entries.first?.consoleLevel, .error)
        XCTAssertEqual(page.entries.first?.consoleSource, .exception)
        XCTAssertEqual(page.entries.first?.stack?.first?.function, "applyDiscount")

        let source = try Wire.decode(DebugSource.self, from: Data(#"{"script_id":"88","url":"app://shop/checkout.js","text":"a\nb","truncated":false}"#.utf8))
        XCTAssertEqual(source.lines, ["a", "b"])
    }
}
