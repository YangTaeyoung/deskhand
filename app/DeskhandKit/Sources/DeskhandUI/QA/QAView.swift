import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The QA tab: a toolbar with 시나리오 실행… (⌘R) and the list scope (this computer or every
/// computer); the runs on the left and the selected run on the right. Below `narrowWidth` the list
/// folds away behind the toolbar's sidebar button and opens as a floating panel. The workspace's
/// control banner shows the PC's connection; while the PC is offline a strip says so, and while it
/// is connected with capabilities that are not ready, a strip names them.
public struct QAView: View {
    static let narrowWidth: CGFloat = 900

    let store: AppStore
    let host: Host
    let chrome: WorkspaceChrome

    public init(store: AppStore, host: Host, chrome: WorkspaceChrome = .native) {
        self.store = store
        self.host = host
        self.chrome = chrome
    }

    public var body: some View {
        let model = store.qa
        let scope = model.scope(for: host.hostID)
        QAContent(store: store, model: model, host: host, scope: scope, chrome: chrome)
            .onAppear {
                model.start()
                model.ensureLoaded(scope)
            }
            .onChange(of: scope) { _, next in model.ensureLoaded(next) }
    }
}

private struct QAContent: View {
    let store: AppStore
    @Bindable var model: QAModel
    let host: Host
    let scope: QAScope
    let chrome: WorkspaceChrome
    @State private var width: CGFloat?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.findRequest) private var findRequest

    private var narrow: Bool { (width ?? QAView.narrowWidth) < QAView.narrowWidth }
    private var showsList: Bool { model.listShown ?? !narrow }

    private func setList(_ shown: Bool) {
        withAnimation(reduceMotion ? nil : .snappy(duration: 0.2)) { model.listShown = shown }
    }

    var body: some View {
        let runs = model.runs(in: scope)
        VStack(spacing: 0) {
            QAToolbar(store: store, model: model, host: host, scope: scope, narrow: narrow, listShown: showsList, hasRuns: !runs.isEmpty) {
                setList(!showsList)
            }
            if host.connection == .offline {
                HostOfflineStrip(reconnecting: false,
                                 detail: String.ui("qa.offline.strip", "실행 기록은 볼 수 있습니다. 지금 실행하면 PC가 필요한 단계는 ‘차단’으로 끝납니다."))
            } else if connected, !QACapabilityStrip.missing(host).isEmpty {
                QACapabilityStrip(host: host)
            }
            main(runs)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .background(DS.Color.surface)
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
        .onChange(of: findRequest) { _, _ in if !showsList { setList(true) } }
        .overlay {
            if let viewer = model.viewer {
                EvidenceViewer(model: model, selection: viewer)
                    .transition(.opacity)
            }
        }
        .overlay(alignment: .bottom) {
            if let notice = model.notice {
                DiagnosticsNoticeToast(notice: notice) { model.notice = nil }
                    .padding(.horizontal, DS.Space.l)
                    .padding(.bottom, DS.Space.l)
                    .transition(reduceMotion ? .opacity : .move(edge: .bottom).combined(with: .opacity))
            }
        }
        .animation(reduceMotion ? nil : .snappy(duration: 0.15), value: model.viewer)
        .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: model.notice)
        .sheet(item: $model.sheet) { sheet in
            switch sheet {
            case .run(let draft):
                RunSheet(store: store, model: model, draft: draft) { model.sheet = nil }
            }
        }
    }

    private var connected: Bool {
        host.connection == .ready || host.connection == .degraded
    }

    @ViewBuilder
    private func main(_ runs: [QARun]) -> some View {
        let listing = model.listing(scope)
        if runs.isEmpty {
            switch listing.phase {
            case .idle, .loading:
                LoadingLine(text: String.ui("qa.loading", "QA 실행 기록을 불러오는 중…"))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            case .failed(let error):
                VStack {
                    ErrorBanner(message: String.ui("qa.listFailed", "QA 실행 기록을 불러오지 못했습니다"), fix: error.sentences, code: error.code) {
                        Task { await model.load(scope) }
                    }
                    .frame(maxWidth: 520)
                }
                .padding(DS.Space.xl)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            case .loaded:
                QAEmptyView(model: model, host: host, scope: scope)
            }
        } else {
            workspace
        }
    }

    private var listWidth: CGFloat { min(max((width ?? 1200) * 0.3, 300), 380) }

    private var workspace: some View {
        HStack(spacing: 0) {
            if showsList, !narrow {
                RunList(store: store, model: model, scope: scope)
                    .frame(width: listWidth)
                    .background(DS.Status.listBackground)
                Rectangle().fill(DS.Color.separator).frame(width: 1)
            }
            detail
                .frame(minWidth: 0, maxWidth: .infinity, maxHeight: .infinity)
                .clipped()
        }
        .overlay(alignment: .leading) {
            if narrow, showsList {
                Color.black.opacity(colorScheme == .dark ? 0.4 : 0.2)
                    .contentShape(Rectangle())
                    .onTapGesture { setList(false) }
                    .transition(.opacity)
                    .accessibilityHidden(true)
            }
        }
        .overlay(alignment: .topLeading) {
            if narrow, showsList {
                RunList(store: store, model: model, scope: scope) { setList(false) }
                    .frame(width: 340)
                    .background(DS.Status.listBackground)
                    .background(DS.Color.surface)
                    .clipShape(RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
                    .shadow(color: .black.opacity(colorScheme == .dark ? 0.55 : 0.25), radius: 20, y: 8)
                    .padding(DS.Space.s)
                    .transition(reduceMotion ? .opacity : .move(edge: .leading).combined(with: .opacity))
            }
        }
    }

    @ViewBuilder
    private var detail: some View {
        if let run = model.selectedRun(in: scope) {
            RunDetail(store: store, model: model, run: run, compact: narrow)
                .id(run.runID)
        } else {
            DebugPanePlaceholder(symbol: "checklist", title: String.ui("qa.noSelection", "실행을 고르세요"),
                                 message: String.ui("qa.noSelection.detail", "왼쪽 목록에서 실행을 고르면 단계별 결과와 증거를 봅니다."))
        }
    }
}

/// 시나리오 실행… (⌘R), the list scope, and the sidebar button of a narrow tab.
private struct QAToolbar: View {
    let store: AppStore
    @Bindable var model: QAModel
    let host: Host
    let scope: QAScope
    let narrow: Bool
    let listShown: Bool
    let hasRuns: Bool
    let toggleList: () -> Void

    var body: some View {
        HStack(spacing: DS.Space.m) {
            if narrow, hasRuns {
                Button(action: toggleList) {
                    Image(systemName: "sidebar.left")
                        .font(.system(.body))
                        .foregroundStyle(listShown ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(DS.Status.neutralText))
                        .frame(width: 28, height: 24)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(listShown ? String.ui("qa.list.hide", "실행 목록 숨기기") : String.ui("qa.list.show", "실행 목록 보기"))
                .accessibilityLabel(listShown ? String.ui("qa.list.hide", "실행 목록 숨기기") : String.ui("qa.list.show", "실행 목록 보기"))
            }
            Button {
                model.presentRunSheet(hostID: host.hostID)
            } label: {
                Label(String.ui("qa.run", "시나리오 실행…"), systemImage: "play.fill")
            }
            .buttonStyle(.borderedProminent)
            .tint(DS.Status.selectionFill)
            .help(String.ui("qa.run.help", "시나리오 YAML 파일을 골라 이 PC에서 실행합니다 (⌘R)"))
            QAScopeSwitch(hostName: host.name, showsAllHosts: $model.showsAllHosts)
            Spacer(minLength: DS.Space.s)
            if let running = runningInScope {
                Button {
                    model.select(running.runID, in: scope)
                } label: {
                    HStack(spacing: DS.Space.s) {
                        ProgressView().controlSize(.small)
                        Text(runningText(running))
                            .font(DS.Font.body)
                            .foregroundStyle(DS.Status.neutralText)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }
                }
                .buttonStyle(.plain)
                .help(String.ui("qa.running.help", "진행 중인 실행을 엽니다"))
                .accessibilityLabel(runningText(running))
            }
        }
        .controlSize(.regular)
        .padding(.horizontal, DS.Space.m)
        .frame(height: WorkspaceMetrics.subToolbarHeight)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("qa.toolbar", "QA 도구 막대"))
    }

    private var runningInScope: QARun? {
        model.runs(in: scope).first { $0.isRunning }
    }

    private func runningText(_ run: QARun) -> String {
        let done = String(run.stepsPassed + run.stepsFailed + run.stepsBlocked + run.stepsNotRun)
        let total = String(run.stepsTotal)
        let hostName = store.host(run.hostID)?.name ?? run.hostID
        return .ui("qa.running", "\(hostName)에서 ‘\(run.name)’ 실행 중 · \(done)/\(total)단계")
    }
}

/// The list scope as two segments, each as wide as its own label: the computer's name (cut in the
/// middle past `maximumNameWidth`) and 모든 PC; the chosen one sits raised on the track.
struct QAScopeSwitch: View {
    static let maximumNameWidth: CGFloat = 200

    let hostName: String
    @Binding var showsAllHosts: Bool
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: 0) {
            segment(hostName, selected: !showsAllHosts, maxWidth: Self.maximumNameWidth) { showsAllHosts = false }
            segment(String.ui("qa.scope.all", "모든 PC"), selected: showsAllHosts, maxWidth: nil) { showsAllHosts = true }
        }
        .padding(DS.Space.xs)
        .background(DS.Color.neutral.opacity(0.14), in: RoundedRectangle(cornerRadius: DS.Radius.s + 1, style: .continuous))
        .fixedSize()
        .animation(reduceMotion ? nil : .snappy(duration: 0.15), value: showsAllHosts)
        .help(String.ui("qa.scope.help", "이 PC의 실행만 보거나 모든 PC의 실행을 봅니다"))
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("qa.scope.a11y", "실행 목록 범위"))
    }

    private func segment(_ title: String, selected: Bool, maxWidth: CGFloat?, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title)
                .font(DS.Font.body.weight(selected ? .semibold : .regular))
                .foregroundStyle(selected ? AnyShapeStyle(.primary) : AnyShapeStyle(DS.Status.neutralText))
                .lineLimit(1)
                .truncationMode(.middle)
                .frame(maxWidth: maxWidth)
                .fixedSize(horizontal: maxWidth == nil, vertical: false)
                .padding(.horizontal, DS.Space.m)
                .frame(minHeight: 22)
                .background {
                    if selected {
                        RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
                            .fill(DS.Color.surface)
                            .shadow(color: .black.opacity(0.12), radius: 1, y: 0.5)
                    }
                }
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(title)
        .accessibilityAddTraits(selected ? [.isSelected, .isButton] : [.isButton])
    }
}

/// The computer is connected but some capabilities QA steps need are not ready: those steps will
/// be blocked.
private struct QACapabilityStrip: View {
    let host: Host

    /// Capabilities QA steps need that the agent reports as not ready, with their reasons.
    static func missing(_ host: Host) -> [(name: String, reason: String?)] {
        let needed: [(String, String)] = [
            ("uia", .ui("qa.feature.uia", "UI 자동화")), ("exec", .ui("qa.feature.exec", "프로그램 실행")),
            ("files", .ui("qa.feature.files", "파일 접근")), ("screen", .ui("qa.feature.screen", "화면 캡처")),
        ]
        return needed.compactMap { key, name in
            guard let cap = host.caps[key], cap.capabilityState != .ready else { return nil }
            return (name, cap.reason)
        }
    }

    var body: some View {
        let missing = Self.missing(host)
        let list = missing.map { item in item.reason.map { item.name + "(" + $0 + ")" } ?? item.name }.joined(separator: ", ")
        HStack(spacing: DS.Space.s) {
            Image(systemName: "exclamationmark.triangle")
                .foregroundStyle(DS.Color.warning)
                .accessibilityHidden(true)
            Text(String.ui("qa.caps.strip", "이 PC에서 지금 쓸 수 없는 기능: \(list). 그 기능이 필요한 단계는 ‘차단’으로 끝납니다."))
                .font(DS.Font.caption)
                .foregroundStyle(DS.Status.neutralText)
                .lineLimit(2)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(DS.Color.warning.opacity(0.08))
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .accessibilityElement(children: .combine)
    }
}

/// No runs in the scope yet: what the tab does and the run button.
private struct QAEmptyView: View {
    let model: QAModel
    let host: Host
    let scope: QAScope

    var body: some View {
        VStack(spacing: DS.Space.xl) {
            EmptyStateView(
                symbol: "checklist",
                title: scope == .all ? String.ui("qa.empty.all", "아직 QA 실행 기록이 없습니다")
                                     : String.ui("qa.empty.host", "\(host.name)에서 실행한 QA가 없습니다"),
                message: String.ui("qa.empty.message", "시나리오 YAML 파일을 골라 실행하면 단계마다 통과·실패·차단과 기대·실제 값, 증거 스크린샷이 남고 보고서로 내보낼 수 있습니다. CLI나 AI가 실행한 것도 여기에 함께 보입니다."),
                actionTitle: String.ui("qa.run", "시나리오 실행…")) {
                    model.presentRunSheet(hostID: host.hostID)
                }
            HStack(spacing: DS.Space.xl) {
                hint("doc.text", String.ui("qa.empty.hint.scenario", "준비 → 본 단계 → 정리 순서로 실행"))
                hint("photo.on.rectangle", String.ui("qa.empty.hint.evidence", "실패하면 화면과 UI 스냅샷"))
                hint("square.and.arrow.up", String.ui("qa.empty.hint.report", "HTML 보고서와 manifest"))
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func hint(_ symbol: String, _ text: String) -> some View {
        Label(text, systemImage: symbol)
            .font(DS.Font.caption)
            .foregroundStyle(DS.Status.neutralText)
    }
}
