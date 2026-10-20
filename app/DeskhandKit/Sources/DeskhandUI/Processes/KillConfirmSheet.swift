import DeskhandClient
import SwiftUI

/// Confirms ending a process, or a process and its descendants: the name and PID, the path,
/// what ending it means, a note for a process Deskhand started, and the descendants of a tree.
public struct KillConfirmSheet: View {
    let request: KillRequest
    let hostName: String
    let confirm: () -> Void
    let cancel: () -> Void

    public init(request: KillRequest, hostName: String, confirm: @escaping () -> Void, cancel: @escaping () -> Void) {
        self.request = request
        self.hostName = hostName
        self.confirm = confirm
        self.cancel = cancel
    }

    private var process: RemoteProcess { request.process }

    private var title: String {
        let name = process.name
        if request.tree {
            let count = FileFormat.count(request.descendants.count)
            return .ui("processes.confirm.treeTitle", "‘\(name)’과(와) 하위 프로세스 \(count)개를 종료할까요?")
        }
        return .ui("processes.confirm.title", "‘\(name)’을(를) 종료할까요?")
    }

    private var startedByDeskhand: Bool {
        process.startedByDeskhand || request.descendants.contains(where: \.startedByDeskhand)
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.l) {
            HStack(alignment: .top, spacing: DS.Space.m) {
                Image(systemName: request.tree ? "xmark.octagon.fill" : "xmark.octagon")
                    .font(.system(size: 30))
                    .foregroundStyle(DS.Color.danger)
                    .frame(width: 40)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.s) {
                    Text(title)
                        .font(DS.Font.title)
                        .lineLimit(3)
                        .truncationMode(.middle)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityAddTraits(.isHeader)
                    HStack(spacing: DS.Space.s) {
                        Text(String.ui("processes.pidLabel", "PID \(String(process.pid))"))
                        if let user = process.user { Text(ProcessFormat.user(user)) }
                        Text(ProcessFormat.memory(kb: process.memKb))
                    }
                    .font(DS.Font.mono)
                    .foregroundStyle(.secondary)
                    if let path = process.path {
                        Text(path)
                            .font(DS.Font.mono)
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                            .truncationMode(.middle)
                            .textSelection(.enabled)
                    }
                    Text(String.ui("processes.confirm.message", "\(hostName)에서 바로 끝냅니다. 저장하지 않은 작업은 사라지고 되돌릴 수 없습니다."))
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.top, DS.Space.xs)
                }
            }
            if startedByDeskhand {
                HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                    Image(systemName: "info.circle.fill")
                        .foregroundStyle(DS.Color.accent)
                        .accessibilityHidden(true)
                    Text(process.startedByDeskhand
                         ? String.ui("processes.confirm.deskhandOne", "Deskhand가 시작한 프로세스입니다. 터미널 탭의 셸이나 AI가 실행한 작업이면 함께 끝납니다.")
                         : String.ui("processes.confirm.deskhand", "Deskhand가 시작한 프로세스가 들어 있습니다. 터미널 탭의 셸이나 AI가 실행한 작업이면 함께 끝납니다."))
                        .font(DS.Font.body)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(DS.Space.m)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(DS.Color.accent.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
            }
            if request.tree, !request.descendants.isEmpty {
                descendantList
            }
            if request.survivors > 0 {
                let count = FileFormat.count(request.survivors)
                HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                    Image(systemName: "arrow.triangle.branch")
                        .foregroundStyle(.secondary)
                        .accessibilityHidden(true)
                    Text(String.ui("processes.confirm.survivors", "하위 프로세스 \(count)개는 끝나지 않고 계속 실행됩니다. 함께 끝내려면 트리 종료를 쓰세요."))
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(.horizontal, DS.Space.m)
            }
            HStack {
                Spacer()
                Button(String.ui("action.cancel", "취소"), action: cancel)
                    .keyboardShortcut(.cancelAction)
                Button(role: .destructive, action: confirm) {
                    Text(request.tree ? String.ui("processes.confirm.killTree", "트리 종료") : String.ui("processes.confirm.kill", "종료"))
                }
                .keyboardShortcut(.defaultAction)
                .tint(DS.Color.danger)
            }
            .controlSize(.large)
        }
        .padding(DS.Space.xl)
        .frame(width: 500)
    }

    private var descendantList: some View {
        let shown = request.descendants.prefix(6)
        return VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(shown.enumerated()), id: \.element.pid) { index, child in
                if index > 0 { Divider() }
                HStack(spacing: DS.Space.s) {
                    Image(systemName: ProcessFormat.symbol(for: child))
                        .foregroundStyle(.secondary)
                        .frame(width: 16)
                        .accessibilityHidden(true)
                    Text(child.name)
                        .font(DS.Font.body)
                        .lineLimit(1)
                        .layoutPriority(1)
                    if child.startedByDeskhand { DeskhandTag() }
                    if let arguments = ProcessFormat.arguments(of: child) {
                        Text(arguments)
                            .font(DS.Font.caption.monospaced())
                            .foregroundStyle(.tertiary)
                            .lineLimit(1)
                            .truncationMode(.tail)
                    }
                    Spacer(minLength: DS.Space.s)
                    Text(String(child.pid))
                        .font(DS.Font.mono)
                        .foregroundStyle(.secondary)
                }
                .padding(.horizontal, DS.Space.m)
                .frame(height: 28)
            }
            if request.descendants.count > shown.count {
                Divider()
                let more = FileFormat.count(request.descendants.count - shown.count)
                Text(String.ui("processes.confirm.more", "외 \(more)개"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, DS.Space.m)
                    .frame(height: 26)
            }
        }
        .background(DS.Color.neutral.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(DS.Color.separator, lineWidth: 1)
        )
        .accessibilityElement(children: .combine)
        .accessibilityLabel(String.ui("processes.a11y.descendants", "함께 종료할 하위 프로세스"))
    }
}
