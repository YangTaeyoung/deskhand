import DeskhandClient
import SwiftUI

/// Confirms ending a process, or a process and its descendants: the name, PID, user, memory and
/// path, that it ends at once, which Deskhand-started processes end with it, and the descendants
/// of a tree. When the process ends while the sheet is open, the sheet says so and only closes.
public struct KillConfirmSheet: View {
    let request: KillRequest
    let hostName: String
    let ended: Bool
    /// The process as the latest list shows it, for the user and memory on the sheet.
    let current: RemoteProcess?
    let confirm: () -> Void
    let cancel: () -> Void

    public init(request: KillRequest, hostName: String, ended: Bool = false, current: RemoteProcess? = nil,
                confirm: @escaping () -> Void, cancel: @escaping () -> Void) {
        self.request = request
        self.hostName = hostName
        self.ended = ended
        self.current = current
        self.confirm = confirm
        self.cancel = cancel
    }

    private var process: RemoteProcess { request.process }

    private var title: String {
        let name = process.name
        if ended { return .ui("processes.confirm.endedTitle", "‘\(name)’ 프로세스가 이미 종료되었습니다") }
        if request.tree {
            let count = DS.Format.count(request.descendants.count)
            return .ui("processes.confirm.treeTitle", "‘\(name)’ 및 하위 프로세스 \(count)개를 종료할까요?")
        }
        return .ui("processes.confirm.title", "‘\(name)’ 프로세스를 종료할까요?")
    }

    /// `PID 6100 · taeyoung · 410.3 MB`, from the latest list while the process is listed.
    private var meta: String {
        let shown = current ?? process
        var parts = [String.ui("processes.pidLabel", "PID \(String(shown.pid))")]
        if let user = shown.user { parts.append(ProcessFormat.user(user)) }
        parts.append(DS.Format.memory(kb: shown.memKb))
        return parts.joined(separator: " · ")
    }

    /// What the Deskhand-started processes among those that end mean for the person.
    private var deskhandNote: String? {
        let started = ([process] + request.descendants).filter(\.startedByDeskhand).count
        guard started > 0 else { return nil }
        if !request.tree {
            return .ui("processes.confirm.deskhandOne", "Deskhand가 터미널 탭이나 AI 작업으로 시작한 프로세스입니다. 이 셸이나 명령이 중단됩니다.")
        }
        let count = DS.Format.count(started)
        return .ui("processes.confirm.deskhand", "함께 끝나는 프로세스 중 \(count)개는 Deskhand가 터미널 탭이나 AI 작업으로 시작한 것입니다. 그 셸과 명령도 중단됩니다.")
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.l) {
            HStack(alignment: .top, spacing: DS.Space.m) {
                Image(systemName: ended ? "checkmark.circle.fill" : "xmark.octagon.fill")
                    .font(.system(size: 30))
                    .foregroundStyle(ended ? AnyShapeStyle(.secondary) : AnyShapeStyle(DS.Color.danger))
                    .frame(width: 40)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.s) {
                    Text(title)
                        .font(DS.Font.title)
                        .lineLimit(3)
                        .truncationMode(.middle)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityAddTraits(.isHeader)
                    Text(meta)
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
                    if !ended {
                        Text(String.ui("processes.confirm.message", "\(hostName)에서 즉시 종료합니다. 저장하지 않은 작업은 사라지고 되돌릴 수 없습니다."))
                            .font(DS.Font.body)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                            .padding(.top, DS.Space.xs)
                    }
                }
            }
            if ended {
                note(symbol: "info.circle.fill", tint: DS.Color.neutral,
                     text: .ui("processes.confirm.ended", "이미 종료된 프로세스입니다. 새로 읽은 목록에 더 이상 없습니다."))
            } else {
                if let deskhandNote {
                    note(symbol: "info.circle.fill", tint: DS.Color.accent, text: deskhandNote)
                }
                if request.tree, !request.descendants.isEmpty {
                    descendantList
                }
                if request.survivors > 0 {
                    let count = DS.Format.count(request.survivors)
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
            }
            HStack {
                Spacer()
                if ended {
                    Button(String.ui("action.close", "닫기"), action: cancel)
                        .keyboardShortcut(.defaultAction)
                } else {
                    Button(String.ui("action.cancel", "취소"), action: cancel)
                        .keyboardShortcut(.cancelAction)
                    Button(role: .destructive, action: confirm) {
                        Text(request.tree ? String.ui("processes.confirm.killTree", "트리 종료") : String.ui("processes.confirm.kill", "종료"))
                    }
                    .keyboardShortcut(.defaultAction)
                    .tint(DS.Color.danger)
                }
            }
            .controlSize(.large)
        }
        .padding(DS.Space.xl)
        .frame(width: 500)
    }

    private func note(symbol: String, tint: Color, text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Image(systemName: symbol)
                .foregroundStyle(tint)
                .accessibilityHidden(true)
            Text(text)
                .font(DS.Font.body)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(DS.Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(tint.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
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
                let more = DS.Format.count(request.descendants.count - shown.count)
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
