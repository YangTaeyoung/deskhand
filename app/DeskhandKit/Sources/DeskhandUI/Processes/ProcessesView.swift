import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The processes tab: a toolbar with the list or tree view, kill, live refresh and search; the
/// process table; the selected process's details; and a status line with totals and the live
/// refresh state. The list refreshes every two seconds while the tab is on screen in a visible
/// window. It also presents the kill confirmation and kill results.
public struct ProcessesView: View {
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

    private var availability: HostFeatureAvailability { HostFeatureAvailability(host: host, capability: "processes") }

    public var body: some View {
        let model = store.processes(for: host)
        ProcessesContent(model: model, host: host, availability: availability, chrome: chrome)
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
            .onChange(of: availability) { _, _ in update(model) }
            .onChange(of: store.layout.refreshRequests) { _, _ in
                guard availability == .ready else { return }
                Task { await model.refresh() }
            }
    }

    private func update(_ model: ProcessesModel) {
        model.setVisible(chrome == .native && onScreen && windowVisible && availability == .ready)
    }
}

private struct ProcessesContent: View {
    @Bindable var model: ProcessesModel
    let host: Host
    let availability: HostFeatureAvailability
    let chrome: WorkspaceChrome
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(spacing: 0) {
            if availability == .ready {
                ProcessesToolbar(model: model, enabled: model.phase == .loaded)
                content
                    .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
            } else {
                HostFeatureUnavailableView(host: host, availability: availability, feature: .processes)
            }
        }
        .background(DS.Color.surface)
        .sheet(item: $model.pendingKill) { request in
            KillConfirmSheet(request: request, hostName: host.name, ended: model.pendingKillEnded,
                             current: model.pendingKillEnded ? nil : model.forest.process(request.process.pid)) {
                Task { await model.confirmKill(request) }
            } cancel: {
                model.cancelKill()
            }
        }
    }

    @ViewBuilder
    private var content: some View {
        switch model.phase {
        case .idle, .loading:
            TableSkeleton(label: String.ui("processes.loading", "프로세스 목록을 불러오는 중…"),
                          columns: [.flexible, .fixed(56), .fixed(68), .fixed(78), .fixed(124), .fixed(138), .fixed(200)], leadingSymbol: true)
        case .failed(let error) where error.code == "CAPABILITY_UNSUPPORTED":
            HostFeatureUnavailableView(host: host, availability: .unavailable(.unsupported, reason: error.message.asSentence),
                                       feature: .processes)
        case .failed(let error):
            VStack(alignment: .leading, spacing: DS.Space.m) {
                ErrorBanner(message: String.ui("processes.failed", "프로세스 목록을 불러오지 못했습니다"),
                            fix: error.sentences, code: error.code) {
                    Task { await model.refresh() }
                }
            }
            .frame(maxWidth: 520)
            .padding(DS.Space.xl)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .loaded:
            VStack(spacing: 0) {
                ProcessTableView(model: model, hostName: host.name, autosave: chrome == .native)
                    .overlay {
                        if model.rows.isEmpty { emptySearch.allowsHitTesting(false) }
                    }
                    .overlay(alignment: .bottom) {
                        if let notice = model.notice {
                            DiagnosticsNoticeToast(notice: notice) { model.dismissNotice() }
                                .padding(.horizontal, DS.Space.l)
                                .padding(.bottom, DS.Space.m)
                                .transition(reduceMotion ? .opacity : .move(edge: .bottom).combined(with: .opacity))
                        }
                    }
                    .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: model.notice)
                if let process = model.selected {
                    ProcessDetailBar(model: model, process: process)
                        .transition(.opacity)
                }
                ProcessesStatusBar(model: model)
            }
        }
    }

    private var emptySearch: some View {
        let query = model.query.trimmingCharacters(in: .whitespaces)
        return VStack(spacing: DS.Space.s) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 32, weight: .regular))
                .foregroundStyle(.tertiary)
                .accessibilityHidden(true)
            Text(query.isEmpty ? String.ui("processes.empty", "프로세스가 없습니다")
                               : String.ui("processes.search.none", "‘\(query)’ 검색 결과가 없습니다"))
                .font(DS.Font.headline)
            Text(String.ui("processes.search.hint", "이름, 경로 또는 PID로 찾습니다."))
                .font(DS.Font.body)
                .foregroundStyle(.secondary)
        }
        .padding(DS.Space.l)
        .accessibilityElement(children: .combine)
    }
}

// MARK: Toolbar

/// The list or tree view, kill and tree kill of the selection, pause and refresh, and search (⌘F).
private struct ProcessesToolbar: View {
    @Bindable var model: ProcessesModel
    let enabled: Bool
    @FocusState private var searchFocused: Bool

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Picker(String.ui("processes.view", "보기"), selection: $model.showsTree) {
                Label(String.ui("processes.view.list", "목록"), systemImage: "list.bullet").tag(false)
                Label(String.ui("processes.view.tree", "트리"), systemImage: "list.bullet.indent").tag(true)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            .help(String.ui("processes.view.help", "목록: 정렬한 순서대로 / 트리: 상위 프로세스 아래에 묶어 보기"))
            .disabled(!enabled)
            killButtons
            Spacer(minLength: DS.Space.s)
            Button {
                model.isPaused.toggle()
            } label: {
                Image(systemName: model.isPaused ? "play.fill" : "pause.fill")
                    .frame(width: 16)
            }
            .help(model.isPaused ? String.ui("processes.resume.help", "2초마다 새로 고치기를 다시 시작합니다")
                                 : String.ui("processes.pause.help", "목록을 멈춰 두고 살펴봅니다. 다시 누르면 이어서 새로 고칩니다."))
            .accessibilityLabel(model.isPaused ? String.ui("processes.resume", "새로 고침 재개") : String.ui("processes.pause", "새로 고침 일시정지"))
            .disabled(!enabled)
            Button {
                Task { await model.refresh() }
            } label: {
                Image(systemName: "arrow.clockwise")
                    .frame(width: 16)
            }
            .help(String.ui("processes.refresh.help", "지금 새로 고침 (⌘R)"))
            .accessibilityLabel(String.ui("processes.refresh", "새로 고침"))
            .disabled(!enabled)
            SearchField(text: $model.query, prompt: String.ui("processes.search.prompt", "이름, 경로, PID 검색"), focused: $searchFocused)
                .frame(minWidth: 140, idealWidth: 220, maxWidth: 240)
                .disabled(!enabled)
                .focusesOnFind($searchFocused)
        }
        .controlSize(.regular)
        .padding(.horizontal, DS.Space.m)
        .frame(height: WorkspaceMetrics.subToolbarHeight)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
    }

    @ViewBuilder
    private var killButtons: some View {
        let pid = model.selected?.pid
        let single = pid.flatMap { model.killBlock(pid: $0, tree: false) }
        let tree = pid.flatMap { model.killBlock(pid: $0, tree: true) }
        let hasChildren = pid.map { !model.forest.children(of: $0).isEmpty } ?? false
        let busy = pid.map { model.killing.contains($0) } ?? false
        Button(role: .destructive) {
            if let pid { Task { await model.killDirectly(pid: pid, tree: false) } }
        } label: {
            Label(String.ui("processes.kill.button", "종료"), systemImage: "xmark.octagon")
        }
        .disabled(!enabled || pid == nil || single != nil || busy)
        .help(single?.reason ?? (pid == nil ? String.ui("processes.kill.select", "종료할 프로세스를 선택하세요")
                                             : String.ui("processes.kill.help", "선택한 프로세스를 종료합니다 (⌘⌫)")))
        Button(role: .destructive) {
            if let pid { Task { await model.killDirectly(pid: pid, tree: true) } }
        } label: {
            Label(String.ui("processes.killTree.button", "트리 종료"), systemImage: "xmark.octagon.fill")
        }
        .disabled(!enabled || pid == nil || !hasChildren || tree != nil || busy)
        .help(treeHelp(pid: pid, hasChildren: hasChildren, block: tree))
    }

    private func treeHelp(pid: Int?, hasChildren: Bool, block: KillBlock?) -> String {
        if pid == nil { return .ui("processes.kill.select", "종료할 프로세스를 선택하세요") }
        if !hasChildren { return .ui("processes.killTree.none", "하위 프로세스가 없습니다.") }
        return block?.reason ?? .ui("processes.killTree.help", "선택한 프로세스와 모든 하위 프로세스를 종료합니다 (⌥⌘⌫)")
    }
}

// MARK: Detail

/// The selected process: name, PID and tags, the command line, and its parent, user, start,
/// CPU and memory, with copy and kill.
private struct ProcessDetailBar: View {
    let model: ProcessesModel
    let process: RemoteProcess

    var body: some View {
        let block = ProcessGuard.block(for: process)
        HStack(alignment: .top, spacing: DS.Space.m) {
            Image(systemName: ProcessFormat.symbol(for: process))
                .font(.system(.title))
                .foregroundStyle(.secondary)
                .frame(width: 28, height: 28)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                HStack(spacing: DS.Space.s) {
                    Text(process.name)
                        .font(DS.Font.headline)
                        .lineLimit(1)
                    Text(String.ui("processes.pidLabel", "PID \(String(process.pid))"))
                        .font(DS.Font.mono)
                        .foregroundStyle(.secondary)
                    if process.startedByDeskhand { DeskhandTag() }
                    if let block {
                        Label(block == .agent ? String.ui("processes.protected.agent", "에이전트") : String.ui("processes.protected", "보호됨"),
                              systemImage: "lock.fill")
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                            .help(block.reason)
                    }
                }
                Text(process.cmd ?? process.path ?? String.ui("processes.noCommand", "명령줄을 읽을 수 없는 프로세스입니다"))
                    .font(DS.Font.mono)
                    .foregroundStyle(process.cmd == nil && process.path == nil ? .tertiary : .secondary)
                    .lineLimit(2)
                    .truncationMode(.middle)
                    .textSelection(.enabled)
                    .help(process.cmd ?? "")
                Text(facts)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
                if let reason = blockReason {
                    Label(reason, systemImage: "lock.fill")
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer(minLength: DS.Space.s)
            HStack(spacing: DS.Space.s) {
                Button {
                    ProcessMenu.copy(process)
                } label: {
                    Label(String.ui("processes.copy", "복사"), systemImage: "doc.on.doc")
                }
                .help(String.ui("processes.copy.help", "이름, PID, 경로와 명령줄을 복사합니다"))
                Button(role: .destructive) {
                    Task { await model.killDirectly(pid: process.pid, tree: false) }
                } label: {
                    Text(String.ui("processes.kill", "종료…"))
                }
                .disabled(block != nil || model.killing.contains(process.pid))
                .help(block?.reason ?? String.ui("processes.kill.help", "선택한 프로세스를 종료합니다 (⌘⌫)"))
            }
            .controlSize(.small)
        }
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, DS.Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DS.Color.surfaceRaised.opacity(0.6))
        .overlay(alignment: .top) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("processes.a11y.detail", "선택한 프로세스"))
    }

    /// Why 종료 or 트리 종료 is off for this process: its own block, or for a parent whose tree holds
    /// a protected process, the tree's.
    private var blockReason: String? {
        if let block = ProcessGuard.block(for: process) { return block.reason }
        guard !model.forest.children(of: process.pid).isEmpty, let tree = model.killBlock(pid: process.pid, tree: true) else { return nil }
        let reason = tree.reason
        return .ui("processes.block.treeOnly", "트리 종료 불가: \(reason)")
    }

    /// `상위 explorer.exe (5520) · taeyoung · 오늘 오전 9:12 시작 · CPU 4.2% · 메모리 296.0 MB`.
    private var facts: String {
        var parts: [String] = []
        if let parent = model.forest.parent(of: process.pid) {
            let name = parent.name
            let pid = String(parent.pid)
            parts.append(.ui("processes.fact.parent", "상위 \(name) (\(pid))"))
        } else {
            let pid = String(process.ppid)
            parts.append(.ui("processes.fact.orphan", "상위 PID \(pid) (끝남)"))
        }
        if let user = process.user { parts.append(user) }
        if let started = process.started?.date {
            let text = ProcessFormat.started(started)
            parts.append(.ui("processes.fact.started", "\(text) 시작"))
        }
        let cpu = ProcessFormat.cpuPercent(process.cpuPct)
        parts.append(.ui("processes.fact.cpu", "CPU \(cpu)"))
        let memory = DS.Format.memory(kb: process.memKb)
        parts.append(.ui("processes.fact.memory", "메모리 \(memory)"))
        return parts.joined(separator: " · ")
    }
}

/// The `Deskhand가 시작함` tag in SwiftUI.
struct DeskhandTag: View {
    var body: some View {
        Text(String.ui("processes.tag.deskhand", "Deskhand가 시작함"))
            .font(.system(.caption, weight: .semibold))
            .foregroundStyle(DS.Status.accentText)
            .padding(.horizontal, DS.Space.s)
            .frame(minHeight: 16)
            .background(DS.Color.accent.opacity(0.14), in: Capsule())
            .help(String.ui("processes.tag.deskhand.help", "Deskhand가 이 PC에서 실행한 명령·앱·셸이거나 그 하위 프로세스입니다."))
    }
}

// MARK: Status

/// Counts and totals, the kill in flight, and whether the list refreshes live, is paused or
/// failed to refresh.
private struct ProcessesStatusBar: View {
    let model: ProcessesModel

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Text(summary)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: DS.Space.s)
            if let pid = model.killing.first, let process = model.forest.process(pid) {
                ProgressView().controlSize(.mini)
                Text(String.ui("processes.killing", "‘\(process.name)’ 프로세스를 종료하는 중…"))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            } else if let error = model.refreshError {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(DS.Color.warning)
                    .accessibilityHidden(true)
                Text(String.ui("processes.refreshFailed", "새로 고치지 못했습니다 · \(updated) 기준"))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .help(error.sentences)
                Button(String.ui("action.retry", "다시 시도")) { Task { await model.refresh() } }
                    .buttonStyle(.link)
                    .tint(DS.Status.accentText)
            } else if model.isPaused {
                Image(systemName: "pause.circle.fill")
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                Text(String.ui("processes.paused", "일시정지됨 · \(updated) 기준"))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            } else {
                LiveDot(live: model.isLive)
                Text(model.isLive
                     ? String.ui("processes.live", "2초마다 새로 고침 · \(updated) 기준")
                     : String.ui("processes.updated", "\(updated) 기준"))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .font(DS.Font.caption)
        .padding(.horizontal, DS.Space.m)
        .frame(height: 26)
        .overlay(alignment: .top) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .accessibilityElement(children: .contain)
    }

    private var updated: String {
        DS.Format.clock(model.updatedAt ?? .now, seconds: true)
    }

    /// `프로세스 82개 · CPU 23.4% · 메모리 18.4 GB`, or the search count.
    private var summary: String {
        let all = model.processes
        let count = DS.Format.count(all.count)
        if !model.query.trimmingCharacters(in: .whitespaces).isEmpty {
            let matches = DS.Format.count(model.rows.filter { !$0.isContext }.count)
            return .ui("processes.status.matches", "검색 결과 \(matches)개 · 전체 \(count)개")
        }
        let busy = all.filter { $0.pid != 0 }.compactMap(\.cpuPct).reduce(0, +)
        let cpu = ProcessFormat.cpuPercent(min(100, busy))
        let memory = DS.Format.memory(kb: all.reduce(0) { $0 + $1.memKb })
        return .ui("processes.status.summary", "프로세스 \(count)개 · CPU \(cpu) · 메모리 \(memory)")
    }
}

/// A dot that is green and pulses while the list refreshes live, and gray otherwise.
private struct LiveDot: View {
    let live: Bool
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Image(systemName: "circle.fill")
            .font(.system(size: 7))
            .foregroundStyle(live ? DS.Color.success : DS.Color.neutral)
            .symbolEffect(.pulse, options: .repeating, isActive: live && !reduceMotion)
            .accessibilityHidden(true)
    }
}
