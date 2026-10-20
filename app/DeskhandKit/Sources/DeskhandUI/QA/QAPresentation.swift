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

    public init(_ run: QARun, steps: [QAStep]? = nil) {
        let failedOrBlocked = run.stepsFailed + run.stepsBlocked
        cancelNote = nil
        detail = nil
        switch run.runStatus {
        case .running, nil:
            tone = .running
            if run.cancelRequested {
                title = .ui("qa.verdict.cancelling", "취소하는 중")
                let phase = run.currentStep.flatMap { index in steps?.first { $0.index == index } }?.stepPhase
                detail = phase == .teardown
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
            title = .ui("qa.verdict.passed", "모든 단계를 통과했습니다")
            if failedOrBlocked > 0 {
                let count = String(failedOrBlocked)
                detail = .ui("qa.verdict.passedOptional", "선택 단계 \(count)개는 통과하지 못했지만 실행 결과에서 뺐습니다")
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
            detail = .ui("qa.verdict.cancelled.detail", "진행 중이던 단계를 멈추고 남은 단계를 건너뛴 뒤 정리 단계를 실행했습니다.")
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
        }
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

    /// Text in the status color, legible on the window and text backgrounds and on its badge tint.
    static func text(_ status: QARunStatus?) -> Color {
        switch status {
        case .running: DS.Status.accentText
        case .passed: DS.Status.successText
        case .failed, .error: DS.Status.dangerText
        case .blocked: DS.Status.warningText
        case .cancelled, nil: .secondary
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
        case .pending, .notRun, nil: .secondary
        }
    }

    static func phase(_ phase: QAStepPhase?) -> String {
        switch phase {
        case .setup: .ui("qa.phase.setup", "준비")
        case .steps, nil: .ui("qa.phase.steps", "단계")
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

    /// The capability a step kind needs, in words, for blocked steps.
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

    // MARK: Times

    /// `3ms`, `0.4초`, `12.3초`, `1분 12초`, `1시간 2분`.
    static func duration(ms: Int) -> String {
        if ms < 100 {
            let text = String(max(ms, 0))
            return .ui("qa.duration.ms", "\(text)ms")
        }
        if ms < 60_000 {
            let seconds = Double(max(ms, 0)) / 1000
            let text = seconds < 10 ? String(format: "%.1f", seconds) : String(Int(seconds.rounded(.down)))
            return .ui("qa.duration.seconds", "\(text)초")
        }
        let total = ms / 1000
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

    /// `오늘 14:02`, `어제 09:12`, `10월 19일 14:02`, or with the year when it is not this year.
    static func started(_ date: Date, now: Date = .now, calendar: Calendar = .current) -> String {
        let time = date.formatted(Date.VerbatimFormatStyle(
            format: "\(hour: .twoDigits(clock: .twentyFourHour, hourCycle: .zeroBased)):\(minute: .twoDigits)",
            timeZone: calendar.timeZone, calendar: Calendar(identifier: .gregorian)))
        if calendar.isDate(date, inSameDayAs: now) { return .ui("qa.time.today", "오늘 \(time)") }
        if let yesterday = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(date, inSameDayAs: yesterday) {
            return .ui("qa.time.yesterday", "어제 \(time)")
        }
        let parts = calendar.dateComponents([.year, .month, .day], from: date)
        let month = String(parts.month ?? 1)
        let day = String(parts.day ?? 1)
        if parts.year == calendar.component(.year, from: now) {
            return .ui("qa.time.date", "\(month)월 \(day)일 \(time)")
        }
        let year = String(parts.year ?? 0)
        return .ui("qa.time.year", "\(year)년 \(month)월 \(day)일 \(time)")
    }

    static func bytes(_ count: Int) -> String {
        Int64(count).formatted(.byteCount(style: .file).locale(Locale(identifier: "ko_KR")))
    }

    // MARK: Blocked steps

    /// Why a blocked step did not run, in plain words, and what to do next when the contract
    /// gives a next step.
    static func blockedReason(_ step: QAStep) -> (title: String, next: String?) {
        switch step.error?.code {
        case "CONTROL_HELD_BY_HUMAN", "CONTROL_PREEMPTED":
            return (.ui("qa.blocked.human", "사람이 조작 중이라 실행하지 않았습니다"),
                    .ui("qa.blocked.human.next", "사람이 제어권을 AI에게 넘긴 뒤 다시 실행하세요. 실행기는 제어권을 가져오거나 기다리지 않습니다."))
        case "CAPABILITY_UNSUPPORTED":
            let feature = feature(of: step.action)
            return (.ui("qa.blocked.unsupported", "이 PC에서 아직 지원하지 않는 단계입니다 — \(feature)"), nil)
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
            return (.ui("qa.blocked.cancel", "취소로 멈췄습니다"), nil)
        default:
            return (.ui("qa.blocked.other", "실행할 수 없어 건너뛰었습니다"), nil)
        }
    }
}
