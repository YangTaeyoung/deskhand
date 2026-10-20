@testable import DeskhandClient
import Foundation
import XCTest

final class QAModelsTests: XCTestCase {
    func testRunWithStepsDecodesTheDaemonSpelling() throws {
        let run = try Wire.decode(QARun.self, from: Data(#"""
        {"run_id":"q-1","job_id":"j-1","host_id":"windows-dev","name":"fixture-smoke","scenario_path":"/Users/qa/fixture-smoke.yaml",
         "status":"failed","rev":14,"started_at":"2026-10-20T10:00:00.000Z","updated_at":"2026-10-20T10:01:12.000Z",
         "finished_at":"2026-10-20T10:01:12.000Z","steps_total":3,"steps_passed":1,"steps_failed":1,"steps_blocked":0,"steps_not_run":1,
         "cancel_requested":false,"unsupported":null,"report_dir":"/h/artifacts/j-1",
         "report":{"json":"/h/artifacts/j-1/report.json","md":"/h/artifacts/j-1/report.md","html":"/h/artifacts/j-1/report.html","manifest":"/h/artifacts/j-1/manifest.json"},
         "steps":[
          {"index":1,"phase":"setup","name":"fixture 올리기","action":"push","line":17,"params":{"from":"a.exe","on_conflict":"replace","mkdir":true},
           "optional":false,"continue_on_fail":false,"status":"pass","started_at":"2026-10-20T10:00:00.100Z","duration_ms":1200,
           "message":"파일 1개(4096바이트)를 보냈습니다","assertions":null,"result":{"transfer_id":"t-1","files_total":1},"evidence":null},
          {"index":2,"phase":"steps","name":"대답 확인","action":"assert_ui","line":31,"params":{"name":"Transcript","text_contains":"[done]"},
           "optional":false,"continue_on_fail":false,"status":"fail","duration_ms":5000,"message":"기대와 다릅니다",
           "assertions":[{"kind":"exists","expected":true,"actual":true,"passed":true},
                         {"kind":"count","expected":1,"actual":2,"passed":false},
                         {"kind":"text_contains","expected":"[done]","actual":"Bot: echo","passed":false}],
           "evidence":[{"artifact_id":"a-1","job_id":"j-1","kind":"screenshot","name":"02-assert_ui-screenshot.png",
                        "path":"/h/artifacts/j-1/evidence/02-assert_ui-screenshot.png","size":81234,"mime":"image/png","sha256":"ab",
                        "created_at":"2026-10-20T10:01:05.000Z"}]},
          {"index":3,"phase":"teardown","name":"닫기","action":"close","line":40,"params":{},"optional":false,"continue_on_fail":false,
           "status":"not_run","message":"2단계(대답 확인)가 실패여서 실행하지 않았습니다","assertions":[],"evidence":[]}
         ]}
        """#.utf8))
        XCTAssertEqual(run.runStatus, .failed)
        XCTAssertEqual(run.rev, 14)
        XCTAssertEqual(run.stepsNotRun, 1)
        XCTAssertEqual(run.unsupported, [])
        XCTAssertEqual(run.report?.html, "/h/artifacts/j-1/report.html")
        let steps = try XCTUnwrap(run.steps)
        XCTAssertEqual(steps.map(\.stepPhase), [.setup, .steps, .teardown])
        XCTAssertEqual(steps[0].params["on_conflict"], .string("replace"))
        XCTAssertEqual(steps[0].result?["transfer_id"], .string("t-1"))
        XCTAssertEqual(steps[0].assertions, [])
        XCTAssertEqual(steps[0].evidence, [])
        XCTAssertEqual(steps[1].assertions.map(\.expected), [.bool(true), .int(1), .string("[done]")])
        XCTAssertEqual(steps[1].assertions.map(\.passed), [true, false, false])
        XCTAssertEqual(steps[1].evidence.first?.kind, "screenshot")
        XCTAssertEqual(steps[2].stepStatus, .notRun)
        XCTAssertNil(steps[2].durationMs)
    }

    func testListedRunWithoutStepsLeavesStepsNil() throws {
        let page = try Wire.decode(QARunPage.self, from: Data(#"""
        {"runs":[{"run_id":"q-2","job_id":"j-2","host_id":"h","name":"n","status":"running","rev":3,
                  "started_at":"2026-10-20T10:00:00.000Z","updated_at":"2026-10-20T10:00:01.000Z","steps_total":12,"steps_passed":2,
                  "steps_failed":0,"steps_blocked":0,"steps_not_run":0,"current_step":3,"cancel_requested":true,
                  "unsupported":[{"action":"click","code":"CAPABILITY_UNSUPPORTED","reason":"마우스 입력 없음"}],
                  "error":{"code":"TIMEOUT","message":"제한 시간"}}],
         "next_cursor":"c-50"}
        """#.utf8))
        let run = try XCTUnwrap(page.runs.first)
        XCTAssertNil(run.steps)
        XCTAssertEqual(run.currentStep, 3)
        XCTAssertTrue(run.cancelRequested)
        XCTAssertNil(run.scenarioPath)
        XCTAssertEqual(run.unsupported.first?.action, "click")
        XCTAssertEqual(run.error, QAErrorInfo(code: "TIMEOUT", message: "제한 시간"))
        XCTAssertEqual(page.nextCursor, "c-50")
    }

    func testExportResultDecodes() throws {
        let export = try Wire.decode(ArtifactExport.self, from: Data(#"""
        {"job_id":"j-1","dir":"/Users/qa/Desktop/j-1 (1)","files":23,"bytes":4200000,"manifest":"/Users/qa/Desktop/j-1 (1)/manifest.json"}
        """#.utf8))
        XCTAssertEqual(export, ArtifactExport(jobID: "j-1", dir: "/Users/qa/Desktop/j-1 (1)", files: 23, bytes: 4_200_000,
                                              manifest: "/Users/qa/Desktop/j-1 (1)/manifest.json"))
    }
}
