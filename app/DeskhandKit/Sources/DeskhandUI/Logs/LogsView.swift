import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The logs tab: the system summary cards, then the event log or the crash dumps, and a strip
/// for a remote log file the files tab sent here. Everything loads when the tab first shows a
/// connected computer; the summary refreshes every five seconds while the tab is on screen.
public struct LogsView: View {
    let store: AppStore
    let host: Host
    let chrome: WorkspaceChrome
    @State private var onScreen = false
    @State private var windowVisible = false

    public init(store: AppStore, host: Host, chrome: WorkspaceChrome = .native) {
        self.store = store
        self.host = host
        self.chrome = chrome
    }

    private var availability: HostFeatureAvailability { HostFeatureAvailability(host: host, capability: "diag") }

    public var body: some View {
        let model = store.logs(for: host)
        LogsContent(store: store, model: model, host: host, availability: availability, chrome: chrome)
            .background {
                if chrome == .native {
                    WindowVisibilityReader { visible in
                        windowVisible = visible
                        update(model)
                    }
                }
            }
            .onAppear {
                onScreen = true
                update(model)
            }
            .onDisappear {
                onScreen = false
                update(model)
            }
            .task(id: host.hostID) {
                guard chrome == .native, availability == .ready else { return }
                await model.start()
            }
            .onChange(of: availability) { old, new in
                update(model)
                guard chrome == .native, new == .ready, old != .ready else { return }
                Task {
                    await model.start()
                    if model.systemPhase != .loaded || model.eventsPhase != .loaded || model.crashesPhase != .loaded {
                        await model.reloadAll()
                    }
                }
            }
    }

    private func update(_ model: LogsModel) {
        model.setVisible(chrome == .native && onScreen && windowVisible && availability == .ready)
    }
}

private struct LogsContent: View {
    let store: AppStore
    @Bindable var model: LogsModel
    let host: Host
    let availability: HostFeatureAvailability
    let chrome: WorkspaceChrome
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @AppStorage("logs.showsSummary") private var showsSummary = true

    var body: some View {
        VStack(spacing: 0) {
            if availability == .ready {
                if let selection = store.logSelection, selection.hostID == host.hostID {
                    LogFileStrip(model: model, path: selection.path) { store.logSelection = nil }
                }
                if showsSummary {
                    SystemSummaryView(model: model)
                        .padding(.horizontal, DS.Space.l)
                        .padding(.vertical, DS.Space.m)
                }
                LogsSectionBar(model: model, showsSummary: $showsSummary)
                switch model.section {
                case .events:
                    EventLogPane(model: model, autosave: chrome == .native)
                case .crashes:
                    CrashDumpList(model: model, hostName: host.name)
                }
            } else {
                HostFeatureUnavailableView(host: host, availability: availability, feature: .diagnostics)
            }
        }
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
        .background(DS.Color.surface)
        .overlay(alignment: .bottom) {
            if let notice = model.notice {
                DiagnosticsNoticeToast(notice: notice) { model.dismissNotice() }
                    .padding(.horizontal, DS.Space.l)
                    .padding(.bottom, DS.Space.xl)
                    .transition(reduceMotion ? .opacity : .move(edge: .bottom).combined(with: .opacity))
            }
        }
        .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: model.notice)
    }
}

/// The section switch (event log or crash dumps), when the data loaded, the summary toggle and
/// refresh (⌘R).
private struct LogsSectionBar: View {
    @Bindable var model: LogsModel
    @Binding var showsSummary: Bool

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Picker(String.ui("logs.section", "보기"), selection: $model.section) {
                ForEach(LogsModel.Section.allCases) { section in
                    Text(title(section)).tag(section)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            Spacer(minLength: DS.Space.s)
            if let updated = model.section == .events
                ? (model.eventsPhase == .loaded ? model.eventsUpdatedAt : nil)
                : (model.crashesPhase == .loaded ? model.crashesUpdatedAt : nil) {
                let time = updated.formatted(.dateTime.hour().minute().second().locale(EventFormat.locale))
                Text(String.ui("logs.updated", "\(time) 기준"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            Button {
                showsSummary.toggle()
            } label: {
                Label(String.ui("logs.summary", "요약"), systemImage: showsSummary ? "chevron.up" : "chevron.down")
            }
            .help(showsSummary ? String.ui("logs.summary.hide", "시스템 요약 숨기기") : String.ui("logs.summary.show", "시스템 요약 보기"))
            .accessibilityLabel(showsSummary ? String.ui("logs.summary.hide", "시스템 요약 숨기기") : String.ui("logs.summary.show", "시스템 요약 보기"))
            Button {
                Task { await model.reloadAll() }
            } label: {
                Image(systemName: "arrow.clockwise")
                    .frame(width: 16)
            }
            .keyboardShortcut("r", modifiers: .command)
            .help(String.ui("logs.refresh.help", "시스템 요약, 이벤트, 크래시 덤프를 다시 불러옵니다 (⌘R)"))
            .accessibilityLabel(String.ui("logs.refresh", "새로 고침"))
        }
        .padding(.horizontal, DS.Space.m)
        .frame(height: 40)
        .overlay(alignment: .top) {
            if showsSummary { Rectangle().fill(DS.Color.separator).frame(height: 1) }
        }
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
    }

    private func title(_ section: LogsModel.Section) -> String {
        guard section == .crashes, model.crashesPhase == .loaded, !model.crashes.isEmpty else { return section.title }
        let count = FileFormat.count(model.crashes.count)
        return .ui("logs.section.crashesCount", "크래시 덤프 \(count)")
    }
}

/// A remote log file the files tab sent here: its path, download into the download folder,
/// then open it with the Mac's default app or show it in Finder.
private struct LogFileStrip: View {
    let model: LogsModel
    let path: String
    let close: () -> Void

    var body: some View {
        let transfer = model.transfer(remotePath: path)
        let name = FilePath.lastComponent(path, style: .windows)
        HStack(spacing: DS.Space.m) {
            Image(systemName: "doc.text")
                .font(.system(size: 18))
                .foregroundStyle(DS.Color.accent)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(String.ui("logs.file.title", "파일 탭에서 연 로그 · \(name)"))
                    .font(DS.Font.body.weight(.semibold))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text(path)
                    .font(DS.Font.mono)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .textSelection(.enabled)
                if let error = model.downloadErrors[path] {
                    Text(error.message)
                        .font(DS.Font.caption)
                        .foregroundStyle(DS.Color.danger)
                }
            }
            Spacer(minLength: DS.Space.s)
            actions(transfer)
            Button(action: close) {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundStyle(.secondary)
                    .frame(width: 18, height: 18)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String.ui("action.close", "닫기"))
        }
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, DS.Space.s)
        .background(DS.Color.accent.opacity(0.06))
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .accessibilityElement(children: .contain)
    }

    @ViewBuilder
    private func actions(_ transfer: Transfer?) -> some View {
        if model.downloadStarting.contains(path) {
            ProgressView().controlSize(.small)
        } else if let transfer, !transfer.transferState.isFinished {
            let presentation = TransferPresentation(transfer, hostName: "")
            HStack(spacing: DS.Space.s) {
                ProgressView(value: presentation.progress ?? 0)
                    .frame(width: 100)
                Text(FileFormat.percent(presentation.progress ?? 0))
                    .font(DS.Font.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
        } else if let transfer, transfer.transferState == .done {
            HStack(spacing: DS.Space.s) {
                Button(String.ui("logs.file.open", "열기")) { model.open(transfer) }
                    .help(String.ui("logs.file.open.help", "이 Mac의 기본 앱(콘솔 등)으로 엽니다"))
                Button(String.ui("logs.download.reveal", "Finder에서 보기")) { model.reveal(transfer) }
            }
            .controlSize(.small)
        } else {
            Button {
                Task { await model.download(remotePath: path) }
            } label: {
                Label(String.ui("logs.file.download", "이 Mac으로 받기"), systemImage: "arrow.down.circle")
            }
            .controlSize(.small)
            .help(String.ui("logs.download.help", "\((model.downloadFolder as NSString).abbreviatingWithTildeInPath)에 받습니다. 같은 이름이 있으면 새 이름으로 저장합니다."))
        }
    }
}
