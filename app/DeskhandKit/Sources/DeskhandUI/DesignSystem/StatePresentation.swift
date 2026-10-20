import SwiftUI

extension ConnectionState {
    /// Short Korean label shown next to a status dot.
    public var title: String {
        switch self {
        case .offline: .ui("connection.offline", "오프라인")
        case .connecting: .ui("connection.connecting", "연결 중")
        case .ready: .ui("connection.ready", "연결됨")
        case .degraded: .ui("connection.degraded", "연결 불안정")
        case .reconnecting: .ui("connection.reconnecting", "재연결 중")
        case .blocked: .ui("connection.blocked", "차단됨")
        }
    }

    public var tint: Color {
        switch self {
        case .offline, .connecting: DS.Color.neutral
        case .ready: DS.Color.success
        case .degraded, .reconnecting: DS.Color.warning
        case .blocked: DS.Color.danger
        }
    }

    /// True while the daemon is still working toward a connection.
    public var isTransitional: Bool {
        self == .connecting || self == .reconnecting
    }
}

extension CapabilityState {
    public var title: String {
        switch self {
        case .ready: .ui("capability.ready", "준비됨")
        case .unavailable: .ui("capability.unavailable", "사용 불가")
        case .unsupported: .ui("capability.unsupported", "지원 안 함")
        case .needsUser: .ui("capability.needsUser", "사용자 조치 필요")
        }
    }

    public var symbol: String {
        switch self {
        case .ready: "checkmark.circle.fill"
        case .unavailable: "exclamationmark.circle.fill"
        case .unsupported: "minus.circle.fill"
        case .needsUser: "hand.raised.circle.fill"
        }
    }

    public var tint: Color {
        switch self {
        case .ready: DS.Color.success
        case .unavailable: DS.Color.danger
        case .unsupported: DS.Color.neutral
        case .needsUser: DS.Color.warning
        }
    }
}

extension ControlOwner {
    /// Control state as the person at this Mac reads it.
    public var title: String {
        switch self {
        case .none: .ui("control.none", "관찰 중")
        case .human: .ui("control.human", "내가 조작 중")
        case .ai: .ui("control.ai", "AI 조작 중")
        }
    }

    public var symbol: String {
        switch self {
        case .none: "eye.fill"
        case .human: "person.fill"
        case .ai: "sparkles"
        }
    }

    public var tint: Color {
        switch self {
        case .none: DS.Color.observe
        case .human: DS.Color.human
        case .ai: DS.Color.ai
        }
    }

    /// Fill under white text for an owner that holds control.
    public var fill: Color {
        switch self {
        case .none: DS.Color.observe
        case .human: DS.Color.humanFill
        case .ai: DS.Color.aiFill
        }
    }
}

extension StepStatus {
    public var title: String {
        switch self {
        case .pending: .ui("step.pending", "대기")
        case .active: .ui("step.active", "진행 중")
        case .done: .ui("step.done", "완료")
        case .failed: .ui("step.failed", "실패")
        }
    }

    /// Symbol for finished states; pending and active steps draw their own indicator.
    public var symbol: String? {
        switch self {
        case .pending, .active: nil
        case .done: "checkmark.circle.fill"
        case .failed: "xmark.circle.fill"
        }
    }

    public var tint: Color {
        switch self {
        case .pending: DS.Color.neutral
        case .active: DS.Color.accent
        case .done: DS.Color.success
        case .failed: DS.Color.danger
        }
    }
}
