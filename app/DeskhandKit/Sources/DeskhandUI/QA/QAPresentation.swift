import DeskhandClient
import Foundation
import SwiftUI

/// The one-line verdict of a run, its explanation, and the note of a cancel that came too late.
public struct QARunVerdict: Equatable, Sendable {
    public enum Tone: Equatable, Sendable {
        case running
        case passed
        case failed
        case blocked
        case cancelled
        case error
    }

    public var tone: Tone
    public var title: String
    public var detail: String?
    /// 취소를 요청했지만 실행이 끝났습니다: the run ended by its steps although a cancel was taken.
    public var cancelNote: String?
    public var cancelDetail: String?

    public init(_ run: QARun, steps: [QAStep]? = nil) {
        let failedOrBlocked = run.stepsFailed + run.stepsBlocked
        cancelNote = nil
        cancelDetail = nil
        detail = nil
        switch run.runStatus {
        case .running, nil:
            tone = .running
            if run.cancelRequested {
                title = .ui("qa.verdict.cancelling", "취소하는 중")
                let teardown = steps?.contains { $0.stepPhase == .teardown && $0.stepStatus != .pending } ?? false
                detail = teardown
                    ? .ui("qa.verdict.cancelling.teardown", "정리 단계를 실행하고 있습니다. 정리 단계도 멈추려면 ‘정리 단계도 중단’을 누르세요.")
                    : .ui("qa.verdict.cancelling.steps", "진행 중인 단계를 멈추고 정리 단계를 실행합니다.")
            } else {
                let total = String(run.stepsTotal)
                let done = String(run.stepsPassed + run.stepsFailed + run.stepsBlocked + run.stepsNotRun)
                title = .ui("qa.verdict.running", "실행 중 · \(total)단계 중 \(done)단계 끝남")
                if let index = run.currentStep, let step = steps?.first(where: { $0.index == index }) {
                    let name = step.name
                    detail = .ui("qa.verdict.current", "지금 단계: \(name)")
                }
            }
        case .passed:
            tone = .passed
            if failedOrBlocked > 0 {
                let count = String(failedOrBlocked)
                title = .ui("qa.verdict.passedRequired", "필수 단계를 모두 통과했습니다")
                detail = .ui("qa.verdict.passedOptional", "선택 단계 \(count)개는 통과하지 못했습니다. 선택 단계는 실행 결과에 들어가지 않습니다.")
            } else {
                title = .ui("qa.verdict.passed", "모든 단계를 통과했습니다")
            }
        case .failed:
            tone = .failed
            if run.error?.code == "TIMEOUT" {
                title = .ui("qa.verdict.timeout", "실행 제한 시간이 지났습니다")
                detail = run.error?.message
            } else {
                let count = String(max(run.stepsFailed, 1))
                title = .ui("qa.verdict.failed", "\(count)개 단계가 실패했습니다")
            }
        case .blocked:
            tone = .blocked
            let count = String(max(run.stepsBlocked, 1))
            title = .ui("qa.verdict.blocked", "\(count)개 단계를 실행하지 못했습니다")
        case .cancelled:
            tone = .cancelled
            title = .ui("qa.verdict.cancelled", "취소했습니다")
            detail = Self.cancelled(steps ?? [])
        case .error:
            tone = .error
            if run.error?.code == "EXECUTION_OUTCOME_UNKNOWN" {
                title = .ui("qa.verdict.restart", "데몬이 다시 시작돼 실행이 중단되었습니다")
                detail = .ui("qa.verdict.restart.detail", "실행 중이던 단계의 결과를 확인하지 못했고 남은 단계는 실행하지 않았습니다. PC 상태를 확인한 뒤 다시 실행하세요.")
            } else {
                title = .ui("qa.verdict.error", "실행을 끝내지 못했습니다")
                detail = run.error?.message
            }
        }
        if run.cancelRequested, run.runStatus != .cancelled, run.runStatus != .running {
            cancelNote = .ui("qa.verdict.cancelLate", "취소를 요청했지만 실행이 끝났습니다")
            cancelDetail = .ui("qa.verdict.cancelLate.detail", "취소가 도착했을 때 멈출 단계가 없었습니다. 실행 결과는 각 단계의 결과를 따릅니다.")
        }
    }

    /// What a cancel stopped, from where the cancel-stopped and skipped steps are.
    private static func cancelled(_ steps: [QAStep]) -> String {
        func stopped(_ step: QAStep) -> Bool { QAFormat.isCancelStopped(step) || step.stepStatus == .notRun }
        let main = steps.filter { $0.stepPhase != .teardown }
        let teardown = steps.filter { $0.stepPhase == .teardown }
        let mainStopped = main.contains(where: stopped)
        let teardownStopped = teardown.contains(where: stopped)
        switch (mainStopped, teardownStopped) {
        case (true, true):
            return .ui("qa.verdict.cancelled.both", "진행 중이던 단계를 멈추고 남은 단계를 건너뛰었고, 정리 단계도 멈췄습니다. PC에 앱이나 파일이 남았는지 확인하세요.")
        case (false, true):
            return .ui("qa.verdict.cancelled.teardown", "준비·본 단계는 모두 실행했고, 정리 단계 중에 취소해 정리 단계를 끝까지 실행하지 않았습니다. PC에 앱이나 파일이 남았는지 확인하세요.")
        case (true, false) where !teardown.isEmpty:
            return .ui("qa.verdict.cancelled.detail", "진행 중이던 단계를 멈추고 남은 단계를 건너뛴 뒤 정리 단계를 끝까지 실행했습니다.")
        default:
            return .ui("qa.verdict.cancelled.noTeardown", "진행 중이던 단계를 멈추고 남은 단계를 건너뛰었습니다.")
        }
    }
}

/// How a run's steps ended. Cancel-stopped steps are counted apart from blocked ones when the
/// steps are known; otherwise the counts are the daemon's.
public struct QAStepCounts: Equatable, Sendable {
    public var passed: Int
    public var failed: Int
    public var blocked: Int
    public var cancelStopped: Int
    public var notRun: Int

    public init(passed: Int, failed: Int, blocked: Int, cancelStopped: Int, notRun: Int) {
        self.passed = passed
        self.failed = failed
        self.blocked = blocked
        self.cancelStopped = cancelStopped
        self.notRun = notRun
    }

    public init(run: QARun, steps: [QAStep]?) {
        guard let steps else {
            self.init(passed: run.stepsPassed, failed: run.stepsFailed, blocked: run.stepsBlocked, cancelStopped: 0, notRun: run.stepsNotRun)
            return
        }
        self.init(steps: steps)
    }

    public init(steps: [QAStep]) {
        var counts = QAStepCounts(passed: 0, failed: 0, blocked: 0, cancelStopped: 0, notRun: 0)
        for step in steps {
            switch step.stepStatus {
            case .pass: counts.passed += 1
            case .fail: counts.failed += 1
            case .blocked:
                if QAFormat.isCancelStopped(step) { counts.cancelStopped += 1 } else { counts.blocked += 1 }
            case .notRun: counts.notRun += 1
            default: break
            }
        }
        self = counts
    }
}

/// Labels, symbols and colors of runs, steps and assertions.
enum QAFormat {
    // MARK: Status

    static func title(_ status: QARunStatus?) -> String {
        switch status {
        case .running: .ui("qa.status.running", "실행 중")
        case .passed: .ui("qa.status.passed", "통과")
        case .failed: .ui("qa.status.failed", "실패")
        case .blocked: .ui("qa.status.blocked", "차단")
        case .cancelled: .ui("qa.status.cancelled", "취소됨")
        case .error: .ui("qa.status.error", "오류")
        case nil: .ui("qa.status.unknown", "알 수 없음")
        }
    }

    static func symbol(_ status: QARunStatus?) -> String {
        switch status {
        case .running: "circle.dotted"
        case .passed: "checkmark.circle.fill"
        case .failed: "xmark.circle.fill"
        case .blocked: "nosign"
        case .cancelled: "stop.circle.fill"
        case .error, nil: "exclamationmark.triangle.fill"
        }
    }

    /// Fill of status marks and badge backgrounds.
    static func tint(_ status: QARunStatus?) -> Color {
        switch status {
        case .running: DS.Color.accent
        case .passed: DS.Color.success
        case .failed: DS.Color.danger
        case .blocked: DS.Color.warning
        case .cancelled, nil: DS.Color.neutral
        case .error: DS.Color.danger
        }
    }

    /// Text in the status color, legible on every background status text sits on.
    static func text(_ status: QARunStatus?) -> Color {
        switch status {
        case .running: DS.Status.accentText
        case .passed: DS.Status.successText
        case .failed, .error: DS.Status.dangerText
        case .blocked: DS.Status.warningText
        case .cancelled, nil: DS.Status.neutralText
        }
    }

    static func title(_ status: QAStepStatus?) -> String {
        switch status {
        case .pending: .ui("qa.step.pending", "대기")
        case .running: .ui("qa.step.running", "실행 중")
        case .pass: .ui("qa.step.pass", "통과")
        case .fail: .ui("qa.step.fail", "실패")
        case .blocked: .ui("qa.step.blocked", "차단")
        case .notRun: .ui("qa.step.notRun", "실행 안 함")
        case nil: .ui("qa.status.unknown", "알 수 없음")
        }
    }

    static func symbol(_ status: QAStepStatus?) -> String {
        switch status {
        case .pending: "circle"
        case .running: "circle.dotted"
        case .pass: "checkmark.circle.fill"
        case .fail: "xmark.circle.fill"
        case .blocked: "nosign"
        case .notRun: "minus.circle"
        case nil: "questionmark.circle"
        }
    }

    static func tint(_ status: QAStepStatus?) -> Color {
        switch status {
        case .pass: DS.Color.success
        case .fail: DS.Color.danger
        case .blocked: DS.Color.warning
        case .running: DS.Color.accent
        case .pending, .notRun, nil: DS.Color.neutral
        }
    }

    static func text(_ status: QAStepStatus?) -> Color {
        switch status {
        case .pass: DS.Status.successText
        case .fail: DS.Status.dangerText
        case .blocked: DS.Status.warningText
        case .running: DS.Status.accentText
        case .pending, .notRun, nil: DS.Status.neutralText
        }
    }

    // MARK: Steps

    /// A blocked step whose request a cancel stopped: the daemon gives it no error code.
    static func isCancelStopped(_ step: QAStep) -> Bool {
        step.stepStatus == .blocked && step.error == nil
    }

    static func title(step: QAStep) -> String {
        isCancelStopped(step) ? .ui("qa.step.cancelStopped", "취소로 멈춤") : title(step.stepStatus)
    }

    static func symbol(step: QAStep) -> String {
        isCancelStopped(step) ? "stop.circle" : symbol(step.stepStatus)
    }

    static func tint(step: QAStep) -> Color {
        isCancelStopped(step) ? DS.Color.neutral : tint(step.stepStatus)
    }

    static func text(step: QAStep) -> Color {
        isCancelStopped(step) ? DS.Status.neutralText : text(step.stepStatus)
    }

    static func phase(_ phase: QAStepPhase?) -> String {
        switch phase {
        case .setup: .ui("qa.phase.setup", "준비")
        case .steps, nil: .ui("qa.phase.steps", "본 단계")
        case .teardown: .ui("qa.phase.teardown", "정리")
        }
    }

    // MARK: Step kinds

    static func action(_ action: String) -> String {
        switch action {
        case "push": .ui("qa.action.push", "파일 보내기")
        case "launch": .ui("qa.action.launch", "앱 실행")
        case "wait_window": .ui("qa.action.waitWindow", "창 기다리기")
        case "click": .ui("qa.action.click", "클릭")
        case "type": .ui("qa.action.type", "글자 입력")
        case "key": .ui("qa.action.key", "키 입력")
        case "ui_act": .ui("qa.action.uiAct", "UI 동작")
        case "assert_ui": .ui("qa.action.assertUI", "UI 확인")
        case "assert_screen_text": .ui("qa.action.assertScreenText", "화면 글 확인")
        case "assert_file": .ui("qa.action.assertFile", "파일 확인")
        case "exec": .ui("qa.action.exec", "명령 실행")
        case "screenshot": .ui("qa.action.screenshot", "스크린샷")
        case "collect": .ui("qa.action.collect", "증거 모으기")
        case "sleep": .ui("qa.action.sleep", "기다리기")
        case "close": .ui("qa.action.close", "창 닫기")
        default: action
        }
    }

    static func actionSymbol(_ action: String) -> String {
        switch action {
        case "push": "arrow.up.doc"
        case "launch": "play.rectangle"
        case "wait_window": "macwindow.badge.plus"
        case "click": "cursorarrow.click"
        case "type": "character.cursor.ibeam"
        case "key": "command"
        case "ui_act": "hand.tap"
        case "assert_ui": "checkmark.rectangle"
        case "assert_screen_text": "text.viewfinder"
        case "assert_file": "doc.text.magnifyingglass"
        case "exec": "terminal"
        case "screenshot": "camera.viewfinder"
        case "collect": "tray.and.arrow.down"
        case "sleep": "hourglass"
        case "close": "xmark.rectangle"
        default: "square.dashed"
        }
    }

    /// The capability a step kind needs, in words.
    static func feature(of action: String) -> String {
        switch action {
        case "click": .ui("qa.feature.mouse", "마우스 입력")
        case "type", "key": .ui("qa.feature.keyboard", "키보드 입력")
        case "launch", "exec": .ui("qa.feature.exec", "프로그램 실행")
        case "push", "assert_file": .ui("qa.feature.files", "파일 접근")
        case "screenshot": .ui("qa.feature.screen", "화면 캡처")
        case "collect": .ui("qa.feature.collect", "파일·진단 수집")
        default: .ui("qa.feature.uia", "UI 자동화")
        }
    }

    // MARK: Assertions

    static func assertion(_ kind: String) -> String {
        switch kind {
        case "window": .ui("qa.assert.window", "창")
        case "exists": .ui("qa.assert.exists", "있음")
        case "count": .ui("qa.assert.count", "개수")
        case "enabled": .ui("qa.assert.enabled", "사용 가능")
        case "text": .ui("qa.assert.text", "글 일치")
        case "text_contains", "contains": .ui("qa.assert.contains", "글 포함")
        case "text_matches", "matches": .ui("qa.assert.matches", "정규식")
        case "not_contains": .ui("qa.assert.notContains", "글 없음")
        case "size": .ui("qa.assert.size", "크기")
        case "sha256": .ui("qa.assert.sha256", "SHA-256")
        case "exit_code": .ui("qa.assert.exitCode", "종료 코드")
        case "stdout_contains": .ui("qa.assert.stdout", "stdout 포함")
        case "stderr_contains": .ui("qa.assert.stderr", "stderr 포함")
        default: kind
        }
    }

    /// A value of an assertion, parameter or result as text: strings as they are, `exists` and
    /// `enabled` in words, lists one item per line, objects as `key: value` lines.
    static func value(_ value: JSONValue, kind: String? = nil) -> String {
        switch value {
        case .null: return "—"
        case .bool(let flag):
            switch kind {
            case "exists": return flag ? .ui("qa.value.exists", "있음") : .ui("qa.value.missing", "없음")
            case "enabled": return flag ? .ui("qa.value.enabled", "사용 가능") : .ui("qa.value.disabled", "사용 불가")
            default: return flag ? "true" : "false"
            }
        case .int(let number): return String(number)
        case .double(let number): return String(number)
        case .string(let text): return text.isEmpty ? .ui("qa.value.empty", "(빈 글)") : text
        case .array(let items): return items.map { Self.value($0) }.joined(separator: "\n")
        case .object(let members):
            return members.keys.sorted().map { $0 + ": " + Self.value(members[$0] ?? .null) }.joined(separator: "\n")
        }
    }

    /// The expected value of an assertion; a `not_contains` value reads 없어야 함.
    static func expectedText(_ expected: JSONValue, kind: String) -> String {
        let text = value(expected, kind: kind)
        return kind == "not_contains" ? .ui("qa.assert.mustNotContain", "없어야 함: \(text)") : text
    }

    /// Parts of a failed assertion's actual text to mark: from the first difference of an exact
    /// `text` check, and every occurrence the `not_contains` value appears.
    static func highlights(kind: String, expected: JSONValue, actual: JSONValue, passed: Bool) -> [Range<String.Index>] {
        guard !passed, case .string(let want) = expected, case .string(let got) = actual, !want.isEmpty else { return [] }
        switch kind {
        case "text":
            var a = got.startIndex
            var b = want.startIndex
            while a < got.endIndex, b < want.endIndex, got[a] == want[b] {
                a = got.index(after: a)
                b = want.index(after: b)
            }
            return a < got.endIndex ? [a..<got.endIndex] : []
        case "not_contains":
            var ranges: [Range<String.Index>] = []
            var from = got.startIndex
            while from < got.endIndex, let found = got.range(of: want, range: from..<got.endIndex) {
                ranges.append(found)
                from = found.upperBound
            }
            return ranges
        default:
            return []
        }
    }

    /// The line under a failed `contains` check saying what was missing.
    static func missingNote(kind: String, expected: JSONValue, passed: Bool) -> String? {
        guard !passed, ["contains", "text_contains", "stdout_contains", "stderr_contains"].contains(kind),
              case .string(let want) = expected else { return nil }
        return .ui("qa.assert.missing", "없는 글: \(want)")
    }

    // MARK: JSON

    /// `text` laid out one member per line with two-space indents and `"key": value`, in the
    /// order the text has it; nil when it is not JSON.
    static func prettyJSON(_ text: String) -> String? {
        guard let data = text.data(using: .utf8), (try? JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed])) != nil else {
            return nil
        }
        var out = ""
        var depth = 0
        var inString = false
        var escaped = false
        let chars = Array(text)
        var i = 0
        func newline() {
            out.append("\n")
            out.append(String(repeating: "  ", count: depth))
        }
        while i < chars.count {
            let c = chars[i]
            if inString {
                out.append(c)
                if escaped { escaped = false } else if c == "\\" { escaped = true } else if c == "\"" { inString = false }
                i += 1
                continue
            }
            switch c {
            case "\"":
                inString = true
                out.append(c)
            case "{", "[":
                var j = i + 1
                while j < chars.count, chars[j].isWhitespace { j += 1 }
                if j < chars.count, chars[j] == (c == "{" ? "}" : "]") {
                    out.append(c)
                    out.append(chars[j])
                    i = j
                } else {
                    out.append(c)
                    depth += 1
                    newline()
                }
            case "}", "]":
                depth -= 1
                newline()
                out.append(c)
            case ",":
                out.append(c)
                newline()
            case ":":
                out.append(": ")
            case " ", "\n", "\r", "\t":
                break
            default:
                out.append(c)
            }
            i += 1
        }
        return out
    }

    // MARK: Times and sizes

    /// `0.1초 미만`, `0.4초`, `12초`, `1분 12초`, `1시간 2분`.
    static func duration(ms: Int) -> String {
        if ms < 100 { return .ui("qa.duration.underTenth", "0.1초 미만") }
        if ms < 10_000 {
            let text = String(format: "%.1f", Double(ms) / 1000)
            return .ui("qa.duration.seconds", "\(text)초")
        }
        let total = ms / 1000
        if total < 60 {
            let seconds = String(total)
            return .ui("qa.duration.wholeSeconds", "\(seconds)초")
        }
        if total < 3600 {
            let minutes = String(total / 60)
            let seconds = String(total % 60)
            return .ui("qa.duration.minutes", "\(minutes)분 \(seconds)초")
        }
        let hours = String(total / 3600)
        let minutes = String(total % 3600 / 60)
        return .ui("qa.duration.hours", "\(hours)시간 \(minutes)분")
    }

    static func duration(from start: Date, to end: Date) -> String {
        duration(ms: Int(end.timeIntervalSince(start) * 1000))
    }

    /// `오늘 오후 3:12`, `어제 오후 6:40`, otherwise `10월 18일 오후 5:12`, as the processes tab writes them.
    static func started(_ date: Date, now: Date = .now, calendar: Calendar = .current) -> String {
        ProcessFormat.started(date, now: now, calendar: calendar)
    }

    /// `563 B`, `192.0 KB`, `4.2 MB`: binary units, one decimal, as the processes tab writes memory.
    static func bytes(_ count: Int) -> String {
        if count < 1024 { return String(max(count, 0)) + " B" }
        return ProcessFormat.memory(kb: count / 1024 + (count % 1024 >= 512 ? 1 : 0))
    }

    // MARK: Blocked steps

    /// Why a blocked step did not run, in plain words, and what to do next.
    static func blockedReason(_ step: QAStep) -> (title: String, next: String?) {
        switch step.error?.code {
        case "CONTROL_HELD_BY_HUMAN", "CONTROL_PREEMPTED":
            return (.ui("qa.blocked.human", "사람이 조작 중이라 실행하지 않았습니다"),
                    .ui("qa.blocked.human.next", "AI에게 넘긴 뒤 다시 실행하세요. 실행기는 제어권을 가져오거나 기다리지 않습니다."))
        case "CAPABILITY_UNSUPPORTED":
            switch step.action {
            case "click":
                return (.ui("qa.blocked.noMouse", "Deskhand 데몬이 아직 마우스 입력을 보내지 못해 실행하지 않았습니다"),
                        .ui("qa.blocked.noInput.next", "ui_act(invoke/set_value)로 바꿔 쓰세요."))
            case "type", "key":
                return (.ui("qa.blocked.noKeyboard", "Deskhand 데몬이 아직 키보드 입력을 보내지 못해 실행하지 않았습니다"),
                        .ui("qa.blocked.noInput.next", "ui_act(invoke/set_value)로 바꿔 쓰세요."))
            default:
                let feature = feature(of: step.action)
                return (.ui("qa.blocked.unavailable", "이 PC의 \(feature) 기능을 지금 쓸 수 없어 실행하지 않았습니다"),
                        .ui("qa.blocked.unavailable.next", "PC의 \(feature) 상태를 고친 뒤 다시 실행하세요."))
            }
        case "OFFLINE", "AUTH_REQUIRED":
            return (.ui("qa.blocked.offline", "PC에 연결되어 있지 않아 실행하지 않았습니다"),
                    .ui("qa.blocked.offline.next", "PC를 연결한 뒤 다시 실행하세요."))
        case "SESSION_LOCKED":
            return (.ui("qa.blocked.locked", "PC가 잠겨 있어 실행하지 않았습니다"),
                    .ui("qa.blocked.locked.next", "PC에서 로그인하거나 잠금을 해제한 뒤 다시 실행하세요."))
        case "DISPLAY_UNAVAILABLE":
            return (.ui("qa.blocked.secureDesktop", "보안 화면(로그인·권한 확인 창)이 떠 있어 실행하지 않았습니다"),
                    .ui("qa.blocked.secureDesktop.next", "PC에서 그 창을 닫은 뒤 다시 실행하세요."))
        case "ELEVATION_REQUIRED":
            return (.ui("qa.blocked.elevation", "관리자 권한이 필요해 실행하지 않았습니다"),
                    .ui("qa.blocked.elevation.next", "관리자 권한으로 에이전트를 실행한 PC에서 다시 실행하세요."))
        case "EXECUTION_OUTCOME_UNKNOWN":
            return (.ui("qa.blocked.unknown", "실행됐는지 확인하지 못했습니다"),
                    .ui("qa.blocked.unknown.next", "PC에서 실제 상태를 확인한 뒤 다시 실행하세요."))
        case nil:
            return (.ui("qa.blocked.cancel", "취소로 멈췄습니다"),
                    .ui("qa.blocked.cancel.next", "필요하면 다시 실행하세요. 다시 실행하면 처음 단계부터 실행합니다."))
        default:
            return (.ui("qa.blocked.other", "실행하지 못했습니다"),
                    .ui("qa.blocked.other.next", "오류 코드와 데몬 메시지를 확인한 뒤 다시 실행하세요."))
        }
    }
}
