import AppKit
import DeskhandClient
import Foundation

/// An evidence file of the preview: a drawn screenshot or a text.
enum PreviewQAEvidence: Sendable, Equatable {
    case screen(PreviewQAImages.Screen)
    case text(String)
}

/// Sample QA scenarios and runs of the preview, newest first: a live fixture run on 개발 PC; a
/// failed run with a screenshot and a UI snapshot; a cancelled one; a passed login run with a
/// masked secret; a blocked run (mouse input unsupported, a person in control); a passed run whose
/// cancel came during teardown; a run ended by a daemon restart; and runs of other computers.
enum PreviewQAFixtures {
    /// One step of a scenario.
    struct Spec: Sendable {
        var phase: QAStepPhase
        var name: String
        var action: String
        var line: Int
        var params: [String: JSONValue]
        var optional = false
        var continueOnFail = false
        /// How long it takes when it passes.
        var ms: Int
    }

    /// What became of one step.
    struct Outcome: Sendable {
        var status: QAStepStatus
        var ms: Int?
        var message: String?
        var error: QAErrorInfo?
        var assertions: [QAAssertion] = []
        var result: [String: JSONValue]?
        /// Evidence as (file name suffix, artifact kind, mime, content).
        var evidence: [(suffix: String, kind: String, mime: String, content: PreviewQAEvidence)] = []
    }

    struct Scenario: Sendable {
        var name: String
        var file: String
        var specs: [Spec]
    }

    static var home: String { FileManager.default.homeDirectoryForCurrentUser.path }
    static func scenarioPath(_ file: String) -> String { home + "/qa/" + file }
    static func reportDir(_ jobID: String) -> String { Paths.home.appendingPathComponent("artifacts/" + jobID).path }

    static let liveRunID = "q-01JBF4T7Z2"

    // MARK: Scenarios

    static func fixtureSmoke(runID: String) -> Scenario {
        let dir = "C:\\deskhand-qa\\" + runID
        return Scenario(name: "fixture-smoke", file: "fixture-smoke.yaml", specs: [
            Spec(phase: .setup, name: "fixture 올리기", action: "push", line: 17,
                 params: ["from": .string(home + "/dev/deskhand/build/deskhand-fixture-windows-amd64.exe"),
                          "to": .string(dir + "\\deskhand-fixture.exe"), "on_conflict": .string("replace"), "mkdir": .bool(true)],
                 ms: 1_840),
            Spec(phase: .setup, name: "fixture 실행", action: "launch", line: 19,
                 params: ["path": .string(dir + "\\deskhand-fixture.exe"), "args": .array([.string("--log"), .string(dir + "\\fixture.log")])],
                 ms: 2_310),
            Spec(phase: .steps, name: "창 기다리기", action: "wait_window", line: 24, params: ["title": .string("Deskhand Fixture")], ms: 640),
            Spec(phase: .steps, name: "한글 라벨 확인", action: "assert_ui", line: 26,
                 params: ["name": .string("한글 라벨"), "role": .string("text")], ms: 420),
            Spec(phase: .steps, name: "메시지 쓰기", action: "ui_act", line: 28,
                 params: ["name": .string("Message"), "role": .string("edit"), "action": .string("set_value"), "value": .string("안녕 QA")],
                 ms: 380),
            Spec(phase: .steps, name: "Send 누르기", action: "ui_act", line: 30,
                 params: ["name": .string("Send"), "role": .string("button"), "action": .string("invoke")], ms: 260),
            Spec(phase: .steps, name: "대답이 끝날 때까지 기다리기", action: "assert_ui", line: 32,
                 params: ["name": .string("Transcript"), "text_contains": .string("[done]"), "text_matches": .string("Bot: echo 안녕 QA")],
                 ms: 2_900),
            Spec(phase: .steps, name: "화면 글 확인", action: "assert_screen_text", line: 35,
                 params: ["contains": .array([.string("Send"), .string("한글 라벨"), .string("Transcript")])], ms: 610),
            Spec(phase: .steps, name: "화면 남기기", action: "screenshot", line: 37, params: ["name": .string("대답 뒤")], optional: true,
                 ms: 1_120),
            Spec(phase: .steps, name: "로그 파일 확인", action: "assert_file", line: 40,
                 params: ["path": .string(dir + "\\fixture.log"), "contains": .array([.string("send 안녕 QA"), .string("done")])], ms: 530),
            Spec(phase: .steps, name: "로그 모으기", action: "collect", line: 42, params: ["file": .string(dir + "\\fixture.log")], ms: 470),
            Spec(phase: .teardown, name: "fixture 닫기", action: "close", line: 45, params: [:], ms: 720),
        ])
    }

    static let loginFlow = Scenario(name: "로그인 흐름", file: "login-flow.yaml", specs: [
        Spec(phase: .setup, name: "Shop Admin 실행", action: "launch", line: 12,
             params: ["path": .string("C:\\Shop\\Shop Admin.exe"), "args": .array([.string("--env=staging")])], ms: 3_420),
        Spec(phase: .steps, name: "로그인 창 기다리기", action: "wait_window", line: 16, params: ["title_contains": .string("로그인")], ms: 1_180),
        Spec(phase: .steps, name: "아이디 입력", action: "ui_act", line: 18,
             params: ["automation_id": .string("userId"), "action": .string("set_value"), "value": .string("qa-bot")], ms: 310),
        Spec(phase: .steps, name: "비밀번호 입력", action: "ui_act", line: 20,
             params: ["automation_id": .string("password"), "action": .string("set_value"), "value": .string("***")], ms: 290),
        Spec(phase: .steps, name: "로그인 누르기", action: "ui_act", line: 22,
             params: ["name": .string("로그인"), "role": .string("button"), "action": .string("invoke")], ms: 240),
        Spec(phase: .steps, name: "대시보드 기다리기", action: "wait_window", line: 24, params: ["title_contains": .string("대시보드")], ms: 2_060),
        Spec(phase: .steps, name: "환영 문구 확인", action: "assert_screen_text", line: 26,
             params: ["contains": .array([.string("환영합니다"), .string("qa-bot")])], ms: 540),
        Spec(phase: .steps, name: "세션 파일 확인", action: "exec", line: 28,
             params: ["argv": .array([.string("powershell"), .string("-NoProfile"), .string("-Command"),
                                      .string("Get-Content $env:APPDATA\\Shop\\session.json")]),
                      "stdout_contains": .array([.string("qa-bot")])], ms: 1_320),
        Spec(phase: .steps, name: "대시보드 남기기", action: "screenshot", line: 32, params: ["name": .string("대시보드")], ms: 980),
        Spec(phase: .teardown, name: "Shop Admin 닫기", action: "close", line: 35, params: [:], ms: 610),
    ])

    static let settingsCheck = Scenario(name: "설정 화면 확인", file: "settings-check.yaml", specs: [
        Spec(phase: .setup, name: "Shop Admin 실행", action: "launch", line: 9, params: ["path": .string("C:\\Shop\\Shop Admin.exe")], ms: 3_100),
        Spec(phase: .steps, name: "도움말 닫기", action: "click", line: 13,
             params: ["element": .object(["name": .string("닫기"), "role": .string("button")])], optional: true, ms: 200),
        Spec(phase: .steps, name: "메인 창 기다리기", action: "wait_window", line: 16, params: ["title_contains": .string("Shop Admin")], ms: 820),
        Spec(phase: .steps, name: "설정 열기", action: "ui_act", line: 18,
             params: ["name": .string("설정"), "role": .string("button"), "action": .string("invoke")], ms: 300),
        Spec(phase: .steps, name: "언어 확인", action: "assert_ui", line: 20,
             params: ["automation_id": .string("language"), "text": .string("한국어")], ms: 400),
        Spec(phase: .steps, name: "설정 화면 남기기", action: "screenshot", line: 23, params: [:], ms: 900),
        Spec(phase: .teardown, name: "Shop Admin 닫기", action: "close", line: 26, params: [:], ms: 500),
    ])

    static let buildCheck = Scenario(name: "빌드 산출물 확인", file: "build-artifacts.yaml", specs: [
        Spec(phase: .steps, name: "빌드 실행", action: "exec", line: 8,
             params: ["cmd": .string("msbuild Shop.sln /p:Configuration=Release"), "cwd": .string("D:\\src\\shop")], ms: 184_000),
        Spec(phase: .steps, name: "설치 파일 확인", action: "assert_file", line: 12,
             params: ["path": .string("D:\\src\\shop\\out\\ShopSetup.msi"), "exists": .bool(true)], ms: 300),
        Spec(phase: .teardown, name: "임시 폴더 정리", action: "exec", line: 16,
             params: ["cmd": .string("Remove-Item D:\\src\\shop\\obj -Recurse -Force")], ms: 2_400),
    ])

    // MARK: Building runs

    /// A run of `scenario` whose steps ended as `outcomes` (missing ones are `pending`), with its
    /// counts, times, report and evidence files.
    static func run(id: String, job: String, host: String, scenario: Scenario, started: Date, status: QARunStatus,
                    outcomes: [Int: Outcome], current: Int? = nil, cancelRequested: Bool = false, error: QAErrorInfo? = nil,
                    unsupported: [QAUnsupported] = [], rev: Int? = nil) -> (run: QARun, evidence: [String: PreviewQAEvidence]) {
        var steps: [QAStep] = []
        var files: [String: PreviewQAEvidence] = [:]
        var clock = started.addingTimeInterval(0.08)
        let dir = reportDir(job)
        for (offset, spec) in scenario.specs.enumerated() {
            let index = offset + 1
            let outcome = outcomes[index] ?? Outcome(status: .pending)
            var step = QAStep(index: index, phase: spec.phase, name: spec.name, action: spec.action, line: spec.line, params: spec.params,
                              optional: spec.optional, continueOnFail: spec.continueOnFail, status: outcome.status)
            switch outcome.status {
            case .pass, .fail, .blocked:
                let ms = outcome.ms ?? spec.ms
                step.startedAt = Timestamp(date: clock)
                step.durationMs = ms
                clock = clock.addingTimeInterval(Double(ms) / 1000 + 0.05)
            case .running:
                step.startedAt = Timestamp(date: clock)
            case .pending, .notRun:
                break
            }
            step.message = outcome.message ?? defaultMessage(spec, outcome.status)
            step.error = outcome.error
            step.assertions = outcome.assertions
            step.result = outcome.result
            for (n, item) in outcome.evidence.enumerated() {
                let number = String(format: "%02d", index)
                let name = number + "-" + spec.action + "-" + item.suffix
                let path = dir + "/evidence/" + name
                files[path] = item.content
                let size: Int
                switch item.content {
                case .screen: size = 182_400 + index * 1_311 + n * 97
                case .text(let text): size = text.utf8.count
                }
                step.evidence.append(Artifact(artifactID: "a-" + job.dropFirst(2) + "-" + number + String(n), jobID: job, kind: item.kind,
                                              name: name, path: path, size: size, mime: item.mime,
                                              sha256: String(repeating: "0", count: 56) + String(format: "%08x", index * 7919 + n),
                                              createdAt: Timestamp(date: clock)))
            }
            steps.append(step)
        }
        func count(_ status: QAStepStatus) -> Int { steps.filter { $0.stepStatus == status }.count }
        let finished = status == .running ? nil : Timestamp(date: clock.addingTimeInterval(0.4))
        let report = status == .running ? nil : QAReportPaths(dir: dir)
        let events = steps.filter { $0.stepStatus != .pending }.count * 2 + (finished == nil ? 1 : 2) + (cancelRequested ? 1 : 0)
        let run = QARun(runID: id, jobID: job, hostID: host, name: scenario.name, scenarioPath: scenarioPath(scenario.file), status: status,
                        rev: rev ?? events, startedAt: Timestamp(date: started), updatedAt: finished ?? Timestamp(date: clock),
                        finishedAt: finished, stepsTotal: steps.count, stepsPassed: count(.pass), stepsFailed: count(.fail),
                        stepsBlocked: count(.blocked), stepsNotRun: count(.notRun), currentStep: current, cancelRequested: cancelRequested,
                        error: error, unsupported: unsupported, reportDir: report == nil ? nil : dir, report: report, steps: steps)
        return (run, files)
    }

    /// The daemon's sentence for a step that ended as `status` without a message of its own.
    static func defaultMessage(_ spec: Spec, _ status: QAStepStatus) -> String? {
        guard status == .pass else { return nil }
        switch spec.action {
        case "push":
            let size = DS.Format.bytes(2_516_992)
            return "파일 1개(\(size))를 보냈습니다"
        case "launch": return "pid 8812로 실행했습니다, 창 \"\(spec.name.contains("Shop") ? "Shop Admin" : "Deskhand Fixture")\""
        case "wait_window":
            let title = spec.params["title"]?.stringValue ?? spec.params["title_contains"]?.stringValue ?? ""
            return "창 \"\(title)\"를 찾았습니다"
        case "screenshot": return "1920×1080 화면을 저장했습니다"
        case "collect":
            let size = DS.Format.bytes(2184)
            return "C:\\deskhand-qa\\fixture.log의 \(size)를 모았습니다"
        case "close": return "창을 닫았습니다"
        case "exec": return "종료 코드 0"
        default: return nil
        }
    }

    /// The daemon's sentence for a step skipped because step `index` (`name`) ended as `status`.
    static func notRunMessage(index: Int, name: String, status: QAStepStatus) -> String {
        let label: String
        switch status {
        case .pass: label = "통과"
        case .fail: label = "실패"
        case .blocked: label = "차단"
        case .notRun: label = "실행 안 함"
        case .running: label = "실행 중"
        case .pending: label = "대기"
        }
        return "\(index)단계(\(name))의 결과(\(label)) 때문에 실행하지 않았습니다"
    }

    static func passes(_ range: ClosedRange<Int>) -> [Int: Outcome] {
        Dictionary(uniqueKeysWithValues: range.map { ($0, Outcome(status: .pass)) })
    }

    static func notRun(_ range: ClosedRange<Int>, message: String) -> [Int: Outcome] {
        Dictionary(uniqueKeysWithValues: range.map { ($0, Outcome(status: .notRun, message: message)) })
    }

    static let humanMessage = "사람이 조작 중입니다. 제어권을 가져오지 않았습니다. 사람이 제어권을 AI에게 넘긴 뒤 다시 실행하세요"
    static let noInputMessage = "이 데몬은 아직 마우스·키보드 입력을 보내지 못합니다. ui_act로 바꾸거나 입력을 지원하는 deskhand로 업데이트하세요"

    /// What the fixture app's window and UI snapshot show while the bot is still typing.
    static let typingTranscript = "Bot: echo 안녕 QA\n입력 중…"

    static let uiTree = """
        {"window":{"id":"0x2A0412","title":"Deskhand Fixture","process":"deskhand-fixture.exe","pid":8812},"truncated":false,\
        "nodes":[{"role":"window","name":"Deskhand Fixture","automation_id":"MainWindow","enabled":true,"children":[\
        {"role":"text","name":"한글 라벨","enabled":true},\
        {"role":"edit","name":"Message","automation_id":"message","value":"","enabled":true},\
        {"role":"button","name":"Send","automation_id":"send","enabled":false},\
        {"role":"edit","name":"Transcript","automation_id":"transcript","value":"Bot: echo 안녕 QA\\n입력 중…","enabled":true}]}]}
        """

    // MARK: Runs

    /// The sample runs, newest first, with their evidence files.
    static func runs(now: Date = .now) -> (runs: [QARun], evidence: [String: PreviewQAEvidence]) {
        var all: [(QARun, [String: PreviewQAEvidence])] = []
        let dev = PreviewFixtures.devPCID
        func ago(_ minutes: Double) -> Date { now.addingTimeInterval(-minutes * 60) }

        // Live: four steps done, the fifth running.
        all.append(run(id: liveRunID, job: "j-01JBF4T7Z3", host: dev, scenario: fixtureSmoke(runID: liveRunID), started: now.addingTimeInterval(-6.2),
                       status: .running, outcomes: passes(1...4).merging([5: Outcome(status: .running)]) { $1 }, current: 5))

        // Failed: the transcript never shows [done].
        let failedID = "q-01JBF2K9QD"
        var failed = passes(1...6)
        failed[7] = Outcome(status: .fail, ms: 15_020,
                            message: "기대와 다릅니다: text_contains 기대 [done], 실제 Bot: echo 안녕 QA 입력 중…", error: nil,
                            assertions: [
                                QAAssertion(kind: "exists", expected: .bool(true), actual: .bool(true), passed: true),
                                QAAssertion(kind: "text_contains", expected: .string("[done]"), actual: .string(typingTranscript), passed: false),
                                QAAssertion(kind: "text_matches", expected: .string("Bot: echo 안녕 QA"), actual: .string(typingTranscript), passed: true),
                            ],
                            evidence: [("screenshot.png", "screenshot", "image/png", .screen(.fixtureTyping)),
                                       ("ui-tree.json", "ui_tree", "application/json", .text(uiTree))])
        failed.merge(notRun(8...11, message: notRunMessage(index: 7, name: "대답이 끝날 때까지 기다리기", status: .fail))) { $1 }
        failed[12] = Outcome(status: .pass)
        all.append(run(id: failedID, job: "j-01JBF2K9QE", host: dev, scenario: fixtureSmoke(runID: failedID), started: ago(38),
                       status: .failed, outcomes: failed))

        // Cancelled while the label was being checked.
        let cancelledID = "q-01JBEZ3M1A"
        var cancelled = passes(1...3)
        cancelled[4] = Outcome(status: .blocked, ms: 1_210, message: "취소로 멈췄습니다")
        cancelled.merge(notRun(5...11, message: "취소로 실행하지 않았습니다")) { $1 }
        cancelled[12] = Outcome(status: .pass)
        all.append(run(id: cancelledID, job: "j-01JBEZ3M1B", host: dev, scenario: fixtureSmoke(runID: cancelledID), started: ago(126),
                       status: .cancelled, outcomes: cancelled, cancelRequested: true))

        // Passed login with a secret password.
        var login = passes(1...10)
        login[2] = Outcome(status: .pass, message: "창 \"Shop Admin 로그인 페이지\"를 찾았습니다",
                           assertions: [QAAssertion(kind: "window", expected: .string("제목에 \"로그인\""), actual: .string("Shop Admin 로그인 페이지"),
                                                    passed: true)])
        login[7] = Outcome(status: .pass, assertions: [
            QAAssertion(kind: "contains", expected: .string("환영합니다"), actual: .string("대시보드\n환영합니다, qa-bot 님\n오늘 주문 42건"), passed: true),
            QAAssertion(kind: "contains", expected: .string("qa-bot"), actual: .string("대시보드\n환영합니다, qa-bot 님\n오늘 주문 42건"), passed: true),
        ])
        login[8] = Outcome(status: .pass, message: "종료 코드 0",
                           assertions: [QAAssertion(kind: "exit_code", expected: .int(0), actual: .int(0), passed: true),
                                        QAAssertion(kind: "stdout_contains", expected: .string("qa-bot"),
                                                    actual: .string("{\"user\":\"qa-bot\",\"token\":\"***\",\"expires\":\"2026-10-21T18:00:00Z\"}"),
                                                    passed: true)],
                           result: ["code": .int(0), "timed_out": .bool(false), "truncated": .bool(false)],
                           evidence: [("stdout.txt", "log", "text/plain",
                                       .text("{\"user\":\"qa-bot\",\"token\":\"***\",\"expires\":\"2026-10-21T18:00:00Z\"}\r\n"))])
        login[9] = Outcome(status: .pass, result: ["width": .int(1920), "height": .int(1080)],
                           evidence: [("대시보드.png", "screenshot", "image/png", .screen(.shopDashboard))])
        all.append(run(id: "q-01JBEW8C4F", job: "j-01JBEW8C4G", host: dev, scenario: loginFlow, started: ago(5 * 60 + 12), status: .passed,
                       outcomes: login))

        // Blocked: mouse input unsupported (optional), then a person held control.
        var blocked = passes(1...1)
        blocked[2] = Outcome(status: .blocked, ms: 2, message: noInputMessage,
                             error: QAErrorInfo(code: "CAPABILITY_UNSUPPORTED", message: noInputMessage))
        blocked[3] = Outcome(status: .pass)
        blocked[4] = Outcome(status: .blocked, ms: 3, message: humanMessage, error: QAErrorInfo(code: "CONTROL_HELD_BY_HUMAN", message: humanMessage))
        blocked.merge(notRun(5...6, message: notRunMessage(index: 4, name: "설정 열기", status: .blocked))) { $1 }
        blocked[7] = Outcome(status: .blocked, ms: 2, message: humanMessage, error: QAErrorInfo(code: "CONTROL_HELD_BY_HUMAN", message: humanMessage))
        all.append(run(id: "q-01JBDQ5R7K", job: "j-01JBDQ5R7M", host: dev, scenario: settingsCheck, started: ago(23 * 60 + 5), status: .blocked,
                       outcomes: blocked, unsupported: [QAUnsupported(action: "click", code: "CAPABILITY_UNSUPPORTED", reason: noInputMessage)]))

        // Passed although a cancel came during teardown.
        let lateID = "q-01JBDM0H2S"
        var late = passes(1...12)
        late[9] = Outcome(status: .pass, result: ["width": .int(1920), "height": .int(1080)],
                          evidence: [("대답 뒤.png", "screenshot", "image/png", .screen(.fixtureDone))])
        all.append(run(id: lateID, job: "j-01JBDM0H2T", host: dev, scenario: fixtureSmoke(runID: lateID), started: ago(26 * 60 + 40),
                       status: .passed, outcomes: late, cancelRequested: true))

        // Ended by a daemon restart.
        let restartID = "q-01JBA7N3XW"
        var restart = passes(1...2)
        restart[3] = Outcome(status: .blocked, ms: 4_210, message: "데몬이 다시 시작돼 이 단계의 결과를 알 수 없습니다",
                             error: QAErrorInfo(code: "EXECUTION_OUTCOME_UNKNOWN", message: "데몬이 다시 시작돼 이 단계의 결과를 알 수 없습니다"))
        restart.merge(notRun(4...12, message: "실행이 멈춰 실행하지 않았습니다")) { $1 }
        all.append(run(id: restartID, job: "j-01JBA7N3XX", host: dev, scenario: fixtureSmoke(runID: restartID), started: ago(3 * 24 * 60 + 70),
                       status: .error, outcomes: restart,
                       error: QAErrorInfo(code: "EXECUTION_OUTCOME_UNKNOWN", message: "데몬이 다시 시작돼 실행 결과를 알 수 없습니다. PC 상태를 확인한 뒤 다시 실행하세요")))

        // QA 노트북: a person held control from the start.
        let laptopID = "q-01JBF0V6PB"
        var laptop = passes(1...1)
        laptop[2] = Outcome(status: .blocked, ms: 2, message: humanMessage, error: QAErrorInfo(code: "CONTROL_HELD_BY_HUMAN", message: humanMessage))
        laptop.merge(notRun(3...11, message: notRunMessage(index: 2, name: "fixture 실행", status: .blocked))) { $1 }
        laptop[12] = Outcome(status: .blocked, ms: 2, message: humanMessage, error: QAErrorInfo(code: "CONTROL_HELD_BY_HUMAN", message: humanMessage))
        all.append(run(id: laptopID, job: "j-01JBF0V6PC", host: PreviewFixtures.qaLaptopID, scenario: fixtureSmoke(runID: laptopID),
                       started: ago(64), status: .blocked, outcomes: laptop))

        // 회의실 PC: passed two days ago.
        let meetingID = "q-01JBC1D8EH"
        all.append(run(id: meetingID, job: "j-01JBC1D8EJ", host: PreviewFixtures.meetingPCID, scenario: fixtureSmoke(runID: meetingID),
                       started: ago(2 * 24 * 60 + 15), status: .passed, outcomes: passes(1...12)))

        // 빌드 서버: the build ran past the run's limit.
        let timeoutMessage = "실행 제한 시간(3분)이 지나 이 단계를 멈췄습니다. timeout_ms를 늘리거나 시나리오를 나누세요"
        all.append(run(id: "q-01JBCX2T9N", job: "j-01JBCX2T9P", host: PreviewFixtures.buildServerID, scenario: buildCheck,
                       started: ago(30 * 60 + 3), status: .failed,
                       outcomes: [
                           1: Outcome(status: .fail, ms: 180_000, message: timeoutMessage, error: QAErrorInfo(code: "TIMEOUT", message: timeoutMessage),
                                      evidence: [("stdout.txt", "log", "text/plain", .text(buildLog))]),
                           2: Outcome(status: .notRun, message: "실행 제한 시간이 지나 실행하지 않았습니다"),
                           3: Outcome(status: .pass),
                       ],
                       error: QAErrorInfo(code: "TIMEOUT", message: "실행 제한 시간(3분)이 지났습니다. timeout_ms를 늘리거나 시나리오를 나누세요")))

        var evidence: [String: PreviewQAEvidence] = [:]
        for (_, files) in all { evidence.merge(files) { $1 } }
        return (all.map(\.0).sorted { $0.startedAt.date > $1.startedAt.date }, evidence)
    }

    static let buildLog = """
        MSBuild version 17.11.2+c078802d4 for .NET Framework
          Shop.Core -> D:\\src\\shop\\Shop.Core\\bin\\Release\\Shop.Core.dll
          Shop.Data -> D:\\src\\shop\\Shop.Data\\bin\\Release\\Shop.Data.dll
          Shop.Admin -> D:\\src\\shop\\Shop.Admin\\bin\\Release\\Shop Admin.exe
          패키지 복원 중: Shop.Setup.wixproj
          WiX Toolset v4 — light.exe 링크 중…

        """
}

/// The preview's sample QA runs and their evidence as the QA tab shows them, for snapshot scenes.
public enum PreviewQASamples {
    public static let liveRunID = PreviewQAFixtures.liveRunID
    public static let failedRunID = "q-01JBF2K9QD"
    public static let cancelledRunID = "q-01JBEZ3M1A"
    public static let loginRunID = "q-01JBEW8C4F"
    public static let blockedRunID = "q-01JBDQ5R7K"
    public static let lateCancelRunID = "q-01JBDM0H2S"
    public static let restartRunID = "q-01JBA7N3XW"
    public static let laptopRunID = "q-01JBF0V6PB"
    public static let meetingRunID = "q-01JBC1D8EH"
    public static let timeoutRunID = "q-01JBCX2T9N"

    /// The sample runs with their steps, newest first.
    public static func runs(now: Date = .now) -> [QARun] {
        PreviewQAFixtures.runs(now: now).runs
    }

    /// Every sample evidence file read as the viewer reads it.
    @MainActor
    public static func evidence(now: Date = .now) -> [String: QAEvidenceContent] {
        var out: [String: QAEvidenceContent] = [:]
        for (path, content) in PreviewQAFixtures.runs(now: now).evidence {
            switch content {
            case .screen(let screen):
                if let decoded = QAEvidenceStore.decodeImage(PreviewQAImages.png(screen), maxPixels: QAEvidenceStore.fullPixels) {
                    out[path] = .image(NSImage(cgImage: decoded.image, size: .zero), pixels: decoded.pixels)
                }
            case .text(let text):
                let read = QAEvidenceStore.text(Data(text.utf8), limit: QAEvidenceStore.textLimit, json: path.hasSuffix(".json"))
                out[path] = .text(read.text, truncated: read.truncated)
            }
        }
        return out
    }

    /// The scenario file path of a sample scenario file name.
    public static func scenarioPath(_ file: String) -> String { PreviewQAFixtures.scenarioPath(file) }
}
