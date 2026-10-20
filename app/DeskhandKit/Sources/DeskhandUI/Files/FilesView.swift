import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The files tab: this Mac and the remote computer side by side, the transfer queue below,
/// the conflict sheet, the info sheet, the permanent-delete confirmation and short notices.
public struct FilesView: View {
    let store: AppStore
    let host: Host

    public init(store: AppStore, host: Host) {
        self.store = store
        self.host = host
    }

    public var body: some View {
        let model = store.files(for: host)
        FilesContent(model: model, host: host)
            .task(id: host.hostID) { await model.start(host: host) }
            .onChange(of: RemoteFilesAvailability(host: host)) { _, availability in
                guard availability == .ready else { return }
                Task { await model.start(host: host) }
            }
    }
}

private struct FilesContent: View {
    @Bindable var model: FilesModel
    let host: Host
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        GeometryReader { proxy in
            layout(queueLimit: max(120, proxy.size.height - 300))
        }
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
    }

    /// The panes over the queue drawer, whose expanded height leaves the panes at least 300 points.
    private func layout(queueLimit: CGFloat) -> some View {
        VStack(spacing: 0) {
            PaneSplit(fraction: model.preferences.splitFraction, commit: { model.preferences.splitFraction = $0 }) {
                FilePane(model: model, side: .local, host: host)
            } trailing: {
                FilePane(model: model, side: .remote, host: host)
            }
            .overlay(alignment: .bottom) {
                if let notice = model.notice {
                    NoticeToast(notice: notice) { model.dismissNotice() }
                        .padding(.bottom, DS.Space.xl + DS.Space.l)
                        .transition(reduceMotion ? .opacity : .move(edge: .bottom).combined(with: .opacity))
                }
            }
            .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: model.notice)
            TransferQueueView(model: model, maxHeight: queueLimit)
        }
        .background {
            Color.clear
                .sheet(item: Binding(get: { model.conflict }, set: { _ in })) { prompt in
                    ConflictSheet(prompt: prompt) { policy, applyToAll in
                        Task { await model.resolveConflict(policy, applyToAll: applyToAll) }
                    }
                }
        }
        .background {
            Color.clear
                .sheet(item: $model.pendingMove) { request in
                    MoveConfirmSheet(request: request) { dontAskAgain in
                        Task { await model.confirmMove(request, dontAskAgain: dontAskAgain) }
                    } cancel: {
                        model.pendingMove = nil
                    }
                }
        }
        .sheet(item: $model.info) { info in
            FileInfoSheet(state: info, sideName: model.sideName(info.side),
                          copyPath: { model.copyPaths([info.item]) },
                          reveal: info.side == .local ? { model.revealInFinder([info.item]) } : nil,
                          close: { model.info = nil })
        }
        .alert(deletionTitle, isPresented: Binding(get: { model.pendingDeletion != nil },
                                                   set: { if !$0 { model.pendingDeletion = nil } }),
               presenting: model.pendingDeletion) { request in
            Button(String.ui("files.delete.confirm", "영구 삭제"), role: .destructive) {
                Task { await model.confirmPermanentDelete(request) }
            }
            Button(String.ui("action.cancel", "취소"), role: .cancel) {}
        } message: { request in
            Text(deletionMessage(request))
        }
    }

    /// The names (the first five and how many more), then what a permanent delete means.
    private func deletionMessage(_ request: DeletionRequest) -> String {
        let host = model.sideName(.remote)
        let consequence = String.ui("files.delete.message", "\(host)의 휴지통을 거치지 않고 바로 지웁니다. 이 작업은 되돌릴 수 없습니다.")
        guard request.items.count > 1 else { return consequence }
        var names = request.items.prefix(5).map { "· \($0.name)" }
        if request.items.count > 5 {
            let more = FileFormat.count(request.items.count - 5)
            names.append(.ui("files.delete.more", "외 \(more)개"))
        }
        return names.joined(separator: "\n") + "\n\n" + consequence
    }

    private var deletionTitle: String {
        guard let items = model.pendingDeletion?.items else { return "" }
        if items.count == 1, let name = items.first?.name {
            return .ui("files.delete.titleOne", "‘\(name)’을(를) 영구 삭제할까요?")
        }
        let count = FileFormat.count(items.count)
        return .ui("files.delete.titleMany", "\(count)개 항목을 영구 삭제할까요?")
    }
}

/// Two panes side by side with a draggable divider. The leading pane takes `fraction` of the
/// width, clamped so both keep at least 300 points.
private struct PaneSplit<Leading: View, Trailing: View>: View {
    let fraction: Double
    let commit: (Double) -> Void
    @ViewBuilder let leading: Leading
    @ViewBuilder let trailing: Trailing
    @State private var dragFraction: Double?

    private static var minimum: CGFloat { 300 }

    var body: some View {
        GeometryReader { geometry in
            let width = geometry.size.width
            let share = dragFraction ?? fraction
            let leadingWidth = width < Self.minimum * 2 ? width / 2 : min(max(width * share, Self.minimum), width - Self.minimum)
            HStack(spacing: 0) {
                leading
                    .frame(width: max(0, leadingWidth - 0.5))
                divider(width: width)
                trailing
                    .frame(maxWidth: .infinity)
            }
            .coordinateSpace(name: "files.split")
        }
        .frame(minWidth: 0, minHeight: 0)
    }

    private func divider(width: CGFloat) -> some View {
        Rectangle()
            .fill(DS.Color.separator)
            .frame(width: 1)
            .overlay {
                Color.clear
                    .frame(width: 9)
                    .contentShape(Rectangle())
                    .onHover { inside in
                        if inside { NSCursor.resizeLeftRight.push() } else { NSCursor.pop() }
                    }
                    .gesture(DragGesture(minimumDistance: 1, coordinateSpace: .named("files.split"))
                        .onChanged { value in
                            guard width > 0 else { return }
                            dragFraction = min(0.8, max(0.2, value.location.x / width))
                        }
                        .onEnded { _ in
                            if let dragFraction { commit(dragFraction) }
                            dragFraction = nil
                        })
            }
            .accessibilityHidden(true)
    }
}

/// A short success, information or error message with a close button.
private struct NoticeToast: View {
    let notice: FilesNotice
    let dismiss: () -> Void

    private var symbol: String {
        switch notice.tone {
        case .info: "info.circle.fill"
        case .success: "checkmark.circle.fill"
        case .error: "exclamationmark.octagon.fill"
        }
    }

    private var tint: Color {
        switch notice.tone {
        case .info: DS.Color.accent
        case .success: DS.Color.success
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
                if let detail = notice.detail {
                    Text(detail)
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let code = notice.code {
                    ErrorCodeLine(code: code)
                }
            }
            Button(action: dismiss) {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundStyle(.secondary)
                    .frame(width: 18, height: 18)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String.ui("action.close", "닫기"))
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .frame(maxWidth: 520, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(tint.opacity(0.35), lineWidth: 1)
        )
        .shadow(color: .black.opacity(0.14), radius: 10, y: 3)
        .accessibilityElement(children: .contain)
        .onAppear {
            AccessibilityNotification.Announcement(notice.message).post()
        }
    }
}
