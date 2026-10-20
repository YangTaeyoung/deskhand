import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// The QA tab: a toolbar with 시나리오 실행… (⌘R) and the list scope (this computer or every
/// computer); the runs on the left and the selected run on the right. Below `narrowWidth` the list
/// folds away behind the toolbar's sidebar button. While the computer is not connected a strip
/// says that a new run's computer steps will be blocked; past runs stay readable.
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

    private var narrow: Bool { (width ?? QAView.narrowWidth) < QAView.narrowWidth }
    private var showsList: Bool { model.listShown ?? !narrow }
    private var availability: HostFeatureAvailability { HostFeatureAvailability(host: host, capability: "exec") }

    private func setList(_ shown: Bool) {
        withAnimation(reduceMotion ? nil : .snappy(duration: 0.2)) { model.listShown = shown }
    }

    var body: some View {
        let runs = model.runs(in: scope)
        VStack(spacing: 0) {
            QAToolbar(store: store, model: model, host: host, scope: scope, narrow: narrow, listShown: showsList, hasRuns: !runs.isEmpty) {
                setList(!showsList)
            }
            if offline {
                QAOfflineStrip(host: host, availability: availability)
            } else if !QACapabilityStrip.missing(host).isEmpty {
                QACapabilityStrip(host: host)
            }
            main(runs)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .background(DS.Color.surface)
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
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

    private var offline: Bool {
        switch availability {
        case .offline, .connecting, .reconnecting, .blocked: true
        case .ready, .unavailable: false
        }
    }

    @ViewBuilder
    private func main(_ runs: [QARun]) -> some View {
        let listing = model.listing(scope)
        if runs.isEmpty {
            switch listing.phase {
            case .idle, .loading:
                DebugLoadingLine(text: String.ui("qa.loading", "QA 실행 기록을 불러오는 중…"))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            case .failed(let error):
                VStack {
                    ErrorBanner(message: String.ui("qa.listFailed", "QA 실행 기록을 불러오지 못했습니다"), fix: DebuggerModel.listFix(error), code: error.code) {
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
                    .background(DS.Color.surfaceRaised.opacity(0.35))
                Rectangle().fill(DS.Color.separator).frame(width: 1)
            }
            detail
                .frame(minWidth: 0, maxWidth: .infinity, maxHeight: .infinity)
                .clipped()
        }
        .overlay(alignment: .leading) {
            if narrow, showsList {
                Color.black.opacity(colorScheme == .dark ? 0.24 : 0.08)
                    .contentShape(Rectangle())
                    .onTapGesture { setList(false) }
                    .transition(.opacity)
                    .accessibilityHidden(true)
            }
        }
        .overlay(alignment: .leading) {
            if narrow, showsList {
                RunList(store: store, model: model, scope: scope) { setList(false) }
                    .frame(width: 320)
                    .background(DS.Color.surfaceRaised.opacity(0.35))
                    .background(DS.Color.surface)
                    .overlay(alignment: .trailing) { Rectangle().fill(DS.Color.separator).frame(width: 1) }
                    .shadow(color: .black.opacity(colorScheme == .dark ? 0.5 : 0.18), radius: 12, x: 4)
                    .transition(reduceMotion ? .opacity : .move(edge: .leading))
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
                        .font(.system(size: 13))
                        .foregroundStyle(listShown ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary))
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
            .keyboardShortcut("r", modifiers: .command)
            .help(String.ui("qa.run.help", "시나리오 YAML 파일을 골라 이 PC에서 실행합니다 (⌘R)"))
            Picker(String.ui("qa.scope", "보기"), selection: $model.showsAllHosts) {
                Text(host.name).tag(false)
                Text(String.ui("qa.scope.all", "모든 PC")).tag(true)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            .help(String.ui("qa.scope.help", "이 PC의 실행만 보거나 모든 PC의 실행을 봅니다"))
            .accessibilityLabel(String.ui("qa.scope.a11y", "실행 목록 범위"))
            Spacer(minLength: DS.Space.s)
            if let running = runningInScope {
                Button {
                    model.select(running.runID, in: scope)
                } label: {
                    HStack(spacing: DS.Space.s) {
                        ProgressView().controlSize(.small)
                        Text(runningText(running))
                            .font(DS.Font.body)
                            .foregroundStyle(.secondary)
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
        .frame(height: 44)
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

/// The computer is not connected: a new run starts but blocks its computer steps; past runs stay.
private struct QAOfflineStrip: View {
    let host: Host
    let availability: HostFeatureAvailability

    var body: some View {
        HStack(spacing: DS.Space.s) {
            if availability == .connecting || availability == .reconnecting {
                ProgressView().controlSize(.small)
            } else {
                Image(systemName: "bolt.horizontal.circle")
                    .foregroundStyle(DS.Color.warning)
                    .accessibilityHidden(true)
            }
            Text(message)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
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

    private var message: String {
        let name = host.name
        switch availability {
        case .connecting, .reconnecting:
            return .ui("qa.offline.reconnecting", "\(name)에 다시 연결하는 중입니다. 진행 중인 실행은 데몬이 이어서 기록하고, 연결이 돌아오면 목록을 다시 읽습니다.")
        case .blocked:
            return .ui("qa.offline.blocked", "\(name)의 페어링을 다시 해야 합니다. 지금 실행하면 PC가 필요한 단계는 ‘차단’으로 끝납니다. 이전 실행 기록과 보고서는 볼 수 있습니다.")
        default:
            return .ui("qa.offline.strip", "\(name)에 연결되어 있지 않습니다. 지금 실행하면 PC가 필요한 단계는 ‘차단’으로 끝납니다. 이전 실행 기록과 보고서는 볼 수 있습니다.")
        }
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
                .foregroundStyle(.secondary)
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
                hint("doc.text", String.ui("qa.empty.hint.scenario", "준비 → 단계 → 정리 순서로 실행"))
                hint("photo.on.rectangle", String.ui("qa.empty.hint.evidence", "실패하면 화면과 UI 스냅샷"))
                hint("square.and.arrow.up", String.ui("qa.empty.hint.report", "HTML 보고서와 manifest"))
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func hint(_ symbol: String, _ text: String) -> some View {
        Label(text, systemImage: symbol)
            .font(DS.Font.caption)
            .foregroundStyle(.secondary)
    }
}
