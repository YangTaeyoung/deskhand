import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Whether a computer can serve a tab, from its connection and one capability.
enum HostFeatureAvailability: Equatable {
    case ready
    case connecting
    case reconnecting
    case offline
    case blocked
    /// The agent reports the capability as not ready.
    case unavailable(CapabilityState, reason: String?)

    init(host: Host, capability: String) {
        switch host.connection {
        case .offline: self = .offline
        case .blocked: self = .blocked
        case .connecting: self = .connecting
        case .reconnecting: self = .reconnecting
        case .ready, .degraded:
            if let cap = host.caps[capability], cap.capabilityState != .ready {
                self = .unavailable(cap.capabilityState, reason: cap.reason)
            } else {
                self = .ready
            }
        }
    }
}

/// A tab whose computer cannot serve it: connecting, offline, blocked, or the capability not
/// ready. Connect and re-pair stay in the workspace banner above.
struct HostFeatureUnavailableView: View {
    enum Feature {
        case processes
        case diagnostics
    }

    let host: Host
    let availability: HostFeatureAvailability
    let feature: Feature

    var body: some View {
        switch availability {
        case .connecting, .reconnecting:
            VStack(spacing: DS.Space.m) {
                ProgressView()
                Text(availability == .connecting
                     ? String.ui("feature.connecting", "\(host.name)에 연결하는 중…")
                     : String.ui("feature.reconnecting", "\(host.name)에 다시 연결하는 중…"))
                    .font(DS.Font.headline)
                Text(feature == .processes
                     ? String.ui("processes.waiting", "연결되면 프로세스 목록을 불러와 2초마다 새로 고칩니다.")
                     : String.ui("logs.waiting", "연결되면 시스템 요약, 이벤트 로그, 크래시 덤프를 불러옵니다."))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 360)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(DS.Space.l)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .accessibilityElement(children: .combine)
        case .offline:
            EmptyStateView(symbol: "bolt.horizontal.circle", title: String.ui("feature.offline", "연결되어 있지 않습니다"),
                           message: feature == .processes
                               ? String.ui("processes.offline", "위 배너의 ‘연결’로 \(host.name)에 연결하면 실행 중인 프로세스를 보고 종료할 수 있습니다.")
                               : String.ui("logs.offline", "위 배너의 ‘연결’로 \(host.name)에 연결하면 시스템 요약, 이벤트 로그, 크래시 덤프를 볼 수 있습니다."))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .blocked:
            EmptyStateView(symbol: "lock.trianglebadge.exclamationmark", title: String.ui("feature.blocked", "다시 페어링해야 합니다"),
                           message: String.ui("feature.blockedMessage", "에이전트 인증서가 페어링 때와 달라 연결을 막았습니다. 위 배너에서 다시 페어링하면 이 탭을 쓸 수 있습니다."))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .unavailable(let state, let reason):
            EmptyStateView(symbol: feature == .processes ? "cpu" : "doc.text.magnifyingglass",
                           title: title(state),
                           message: reason ?? String.ui("feature.unavailableMessage", "에이전트가 이 기능이 준비되지 않았다고 알렸습니다. 진단을 실행해 원인을 확인하세요."))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .ready:
            EmptyView()
        }
    }

    private func title(_ state: CapabilityState) -> String {
        switch (feature, state == .unsupported) {
        case (.processes, true): .ui("processes.unsupported", "이 PC는 프로세스 목록을 지원하지 않습니다")
        case (.processes, false): .ui("processes.unavailable", "지금은 프로세스를 볼 수 없습니다")
        case (.diagnostics, true): .ui("logs.unsupported", "이 PC는 진단 정보를 지원하지 않습니다")
        case (.diagnostics, false): .ui("logs.unavailable", "지금은 진단 정보를 볼 수 없습니다")
        }
    }
}

/// A short success, warning or error message with a close button, over the bottom of a tab.
struct DiagnosticsNoticeToast: View {
    let notice: DiagnosticsNotice
    let dismiss: () -> Void

    private var symbol: String {
        switch notice.tone {
        case .success: "checkmark.circle.fill"
        case .warning: "exclamationmark.triangle.fill"
        case .error: "exclamationmark.octagon.fill"
        }
    }

    private var tint: Color {
        switch notice.tone {
        case .success: DS.Color.success
        case .warning: DS.Color.warning
        case .error: DS.Color.danger
        }
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Image(systemName: symbol)
                .foregroundStyle(tint)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(notice.message)
                    .font(DS.Font.body.weight(.semibold))
                    .fixedSize(horizontal: false, vertical: true)
                if let detail = notice.detail, !detail.isEmpty {
                    Text(detail)
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let code = notice.code {
                    ErrorCodeLine(code: code)
                }
            }
            .textSelection(.enabled)
            Spacer(minLength: DS.Space.s)
            Button(action: dismiss) {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundStyle(.secondary)
                    .frame(width: 18, height: 18)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String.ui("action.close", "닫기"))
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .frame(maxWidth: 560, alignment: .leading)
        .background(tint.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .background(DS.Color.surface, in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(tint.opacity(0.35), lineWidth: 1)
        )
        .shadow(color: .black.opacity(0.18), radius: 12, y: 4)
        .accessibilityElement(children: .contain)
        .onAppear {
            AccessibilityNotification.Announcement(notice.message).post()
        }
    }
}

/// Reports whether the window that shows it is visible on screen: not minimized, hidden or
/// fully covered.
struct WindowVisibilityReader: NSViewRepresentable {
    let onChange: @MainActor (Bool) -> Void

    func makeNSView(context: Context) -> VisibilityView {
        let view = VisibilityView()
        view.onChange = onChange
        return view
    }

    func updateNSView(_ view: VisibilityView, context: Context) {
        view.onChange = onChange
    }

    final class VisibilityView: NSView {
        var onChange: (@MainActor (Bool) -> Void)?
        private weak var observed: NSWindow?
        private var last: Bool?

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            if let observed {
                NotificationCenter.default.removeObserver(self, name: NSWindow.didChangeOcclusionStateNotification, object: observed)
            }
            observed = window
            if let window {
                NotificationCenter.default.addObserver(self, selector: #selector(occlusionChanged(_:)),
                                                       name: NSWindow.didChangeOcclusionStateNotification, object: window)
            }
            report()
        }

        @objc private func occlusionChanged(_ notification: Notification) {
            report()
        }

        /// Reports a change on the next turn of the main run loop, outside SwiftUI's view update.
        private func report() {
            let visible = window?.occlusionState.contains(.visible) ?? false
            guard visible != last else { return }
            last = visible
            DispatchQueue.main.async { [weak self] in
                guard let self, self.last == visible else { return }
                self.onChange?(visible)
            }
        }
    }
}
