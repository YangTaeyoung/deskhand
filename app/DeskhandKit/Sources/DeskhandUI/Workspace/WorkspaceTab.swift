import SwiftUI

/// Tabs of a computer's workspace, in tab bar order. Each is selected with ⌘ and its `number`.
public enum WorkspaceTab: String, CaseIterable, Identifiable, Sendable {
    case screen
    case terminal
    case files
    case processes
    case logs
    case debugger
    case qa

    public var id: String { rawValue }

    /// Position in the tab bar, 1 through 7.
    public var number: Int { (Self.allCases.firstIndex(of: self) ?? 0) + 1 }

    public var keyEquivalent: KeyEquivalent { KeyEquivalent(Character(String(number))) }

    /// Short name in the tab bar and the View menu.
    public var title: String {
        switch self {
        case .screen: .ui("tab.screen", "화면")
        case .terminal: .ui("tab.terminal", "터미널")
        case .files: .ui("tab.files", "파일")
        case .processes: .ui("tab.processes", "프로세스")
        case .logs: .ui("tab.logs", "로그")
        case .debugger: .ui("tab.debugger", "디버거")
        case .qa: .ui("tab.qa", "QA")
        }
    }

    public var symbol: String {
        switch self {
        case .screen: "display"
        case .terminal: "terminal"
        case .files: "folder"
        case .processes: "cpu"
        case .logs: "doc.text.magnifyingglass"
        case .debugger: "ladybug"
        case .qa: "checklist"
        }
    }

    /// Heading of the tab's placeholder.
    public var heading: String {
        switch self {
        case .screen: .ui("tab.screen.heading", "원격 화면")
        case .terminal: .ui("tab.terminal.heading", "원격 터미널")
        case .files: .ui("tab.files.heading", "파일 주고받기")
        case .processes: .ui("tab.processes.heading", "실행 중인 프로세스")
        case .logs: .ui("tab.logs.heading", "로그와 시스템 진단")
        case .debugger: .ui("tab.debugger.heading", "Electron 디버거")
        case .qa: .ui("tab.qa.heading", "QA 시나리오")
        }
    }

    /// What the tab lets the person do.
    public var summary: String {
        switch self {
        case .screen:
            .ui("tab.screen.summary", "원격 PC의 화면을 실시간으로 보고 마우스와 키보드로 조작합니다. AI가 조작하는 동안 화면을 클릭하면 제어권을 가져올지 묻습니다.")
        case .terminal:
            .ui("tab.terminal.summary", "PowerShell과 cmd 세션을 여러 개 엽니다. 연결이 끊겼다가 돌아와도 출력이 이어집니다.")
        case .files:
            .ui("tab.files.summary", "이 Mac과 원격 PC의 폴더를 나란히 보고 끌어다 놓아 주고받습니다. 전송은 중간에 끊겨도 이어서 받습니다.")
        case .processes:
            .ui("tab.processes.summary", "프로세스를 CPU·메모리 사용량과 함께 보고, Deskhand가 시작한 프로세스를 구분해 종료합니다.")
        case .logs:
            .ui("tab.logs.summary", "시스템 요약, Windows 이벤트 로그, 크래시 덤프를 시간과 수준으로 걸러 봅니다.")
        case .debugger:
            .ui("tab.debugger.summary", "Electron 앱을 디버그 모드로 실행하거나 실행 중인 앱에 붙어 중단점, 호출 스택, 변수를 봅니다.")
        case .qa:
            .ui("tab.qa.summary", "시나리오 파일을 실행하고 단계별 결과와 증거 스크린샷을 확인한 뒤 보고서로 내보냅니다.")
        }
    }
}
