import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// Sheet that adds a computer: SSH alias and display name, then the installation steps, ending
/// with the connected computer's capabilities or the failure and how to fix it.
public struct AddHostSheet: View {
    @Bindable var flow: AddHostFlow
    @FocusState private var focus: Field?

    private enum Field {
        case alias
        case name
    }

    public static let width: CGFloat = 560
    static let visibleAliasCount = 8

    public init(flow: AddHostFlow) {
        self.flow = flow
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
                .padding([.horizontal, .top], DS.Space.xl)
                .padding(.bottom, DS.Space.l)
            content
                .padding(.horizontal, DS.Space.xl)
                .frame(maxWidth: .infinity, alignment: .topLeading)
            Spacer(minLength: DS.Space.l)
            Divider()
            footer
                .padding(.horizontal, DS.Space.xl)
                .padding(.vertical, DS.Space.l)
        }
        .frame(width: Self.width)
        .frame(minHeight: 400)
        .onAppear { if flow.stage == .form { focus = .alias } }
    }

    // MARK: Header

    private var header: some View {
        HStack(alignment: .center, spacing: DS.Space.m) {
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .fill(headerTint.gradient)
                .frame(width: 40, height: 40)
                .overlay {
                    Image(systemName: headerSymbol)
                        .font(.system(size: 18, weight: .semibold))
                        .foregroundStyle(.white)
                }
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text(headerTitle)
                    .font(DS.Font.title)
                Text(headerSubtitle)
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
    }

    private var headerTitle: String {
        switch flow.stage {
        case .form: .ui("addHost.title", "컴퓨터 추가")
        case .running: .ui("addHost.running.title", "컴퓨터 추가 중")
        case .failed: .ui("addHost.failed.title", "컴퓨터를 추가하지 못했습니다")
        case .succeeded: .ui("addHost.succeeded.title", "연결됨")
        }
    }

    private var headerSubtitle: String {
        switch flow.stage {
        case .form:
            .ui("addHost.subtitle", "SSH로 접속되는 Windows PC에 에이전트를 설치하고 페어링합니다.")
        case .running, .failed:
            .ui("addHost.target", "\(flow.effectiveName) · ssh \(flow.trimmedAlias)")
        case .succeeded(let host):
            "\(host.name) · \(host.platformTitle)"
        }
    }

    private var headerSymbol: String {
        switch flow.stage {
        case .form, .running: "pc"
        case .failed: "exclamationmark.triangle.fill"
        case .succeeded: "checkmark"
        }
    }

    private var headerTint: Color {
        switch flow.stage {
        case .form, .running: DS.Color.accent
        case .failed: DS.Color.danger
        case .succeeded: DS.Color.success
        }
    }

    // MARK: Content

    @ViewBuilder
    private var content: some View {
        switch flow.stage {
        case .form:
            form
        case .running:
            VStack(alignment: .leading, spacing: DS.Space.l) {
                StepProgressView(steps: flow.steps)
                    .dsCard()
                Label(String.ui("addHost.background", "창을 닫아도 설치는 데몬에서 계속됩니다."), systemImage: "info.circle")
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
        case .failed(let error):
            VStack(alignment: .leading, spacing: DS.Space.l) {
                StepProgressView(steps: flow.steps)
                    .dsCard()
                ErrorBanner(message: failureSummary, fix: error.fix ?? String.ui("addHost.failed.fix", "단계에 표시된 오류를 해결한 뒤 다시 시도하세요."), code: error.code)
            }
        case .succeeded(let host):
            succeeded(host)
        }
    }

    private var failureSummary: String {
        guard let step = flow.steps.first(where: { $0.status == .failed }) else {
            return .ui("addHost.failed.summary", "설치를 마치지 못했습니다")
        }
        return .ui("addHost.failed.step", "‘\(step.title)’ 단계에서 실패했습니다")
    }

    private var form: some View {
        VStack(alignment: .leading, spacing: DS.Space.xl) {
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.l) {
                GridRow {
                    fieldLabel(String.ui("addHost.alias", "SSH 별칭"))
                    VStack(alignment: .leading, spacing: DS.Space.s) {
                        TextField(String.ui("addHost.alias", "SSH 별칭"), text: $flow.alias,
                                  prompt: Text(String.ui("addHost.alias.prompt", "예: windows")))
                            .labelsHidden()
                            .textFieldStyle(.roundedBorder)
                            .font(DS.Font.mono)
                            .autocorrectionDisabled()
                            .focused($focus, equals: .alias)
                            .onSubmit { flow.start() }
                            .textInputSuggestions {
                                ForEach(suggestions, id: \.self) { alias in
                                    Text(alias).textInputCompletion(alias)
                                }
                            }
                        aliasChoices
                    }
                }
                GridRow {
                    fieldLabel(String.ui("addHost.name", "표시 이름"))
                    VStack(alignment: .leading, spacing: DS.Space.s) {
                        TextField(String.ui("addHost.name", "표시 이름"), text: $flow.name,
                                  prompt: Text(flow.trimmedAlias.isEmpty ? String.ui("addHost.name.prompt", "예: 개발 PC") : flow.trimmedAlias))
                            .labelsHidden()
                            .textFieldStyle(.roundedBorder)
                            .focused($focus, equals: .name)
                            .onSubmit { flow.start() }
                        Text(String.ui("addHost.name.caption", "사이드바와 CLI에서 쓰는 이름입니다. 비워 두면 별칭을 씁니다."))
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            if let error = flow.formError {
                ErrorBanner(message: error.message, fix: error.fix, code: error.code)
            }
            requirements
        }
    }

    private func fieldLabel(_ text: String) -> some View {
        Text(text)
            .font(DS.Font.body)
            .gridColumnAlignment(.trailing)
            .accessibilityHidden(true)
    }

    private var suggestions: [String] {
        let typed = flow.trimmedAlias.lowercased()
        return flow.aliases.filter { typed.isEmpty || ($0.lowercased().hasPrefix(typed) && $0.lowercased() != typed) }
    }

    @ViewBuilder
    private var aliasChoices: some View {
        if !flow.aliasesLoaded {
            EmptyView()
        } else if flow.aliases.isEmpty {
            Text(String.ui("addHost.alias.none", "~/.ssh/config에서 별칭을 찾지 못했습니다. ssh 명령에 쓰는 호스트 이름을 직접 입력하세요."))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        } else {
            VStack(alignment: .leading, spacing: DS.Space.s) {
                Text(String.ui("addHost.alias.found", "~/.ssh/config에서 찾은 별칭 \(flow.aliases.count)개 · 고르거나 직접 입력하세요"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                FlowLayout(spacing: DS.Space.xs, lineSpacing: DS.Space.xs) {
                    ForEach(flow.aliases.prefix(Self.visibleAliasCount), id: \.self) { alias in
                        AliasChip(alias: alias, selected: alias == flow.trimmedAlias) { choose(alias) }
                    }
                    if flow.aliases.count > Self.visibleAliasCount {
                        Menu {
                            ForEach(flow.aliases.dropFirst(Self.visibleAliasCount), id: \.self) { alias in
                                Button(alias) { choose(alias) }
                            }
                        } label: {
                            Text(String.ui("addHost.alias.more", "\(flow.aliases.count - Self.visibleAliasCount)개 더 보기"))
                                .font(DS.Font.caption)
                        }
                        .menuStyle(.borderlessButton)
                        .fixedSize()
                    }
                }
            }
        }
    }

    private func choose(_ alias: String) {
        flow.alias = alias
        focus = .name
    }

    private var requirements: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Image(systemName: "info.circle")
                .accessibilityHidden(true)
            Text(String.ui("addHost.requirements", "원격 계정에 관리자 권한이 있어야 하고, 이 Mac에서 ssh 별칭으로 비밀번호 없이 접속되어야 합니다. ~/.ssh 설정은 바꾸지 않습니다."))
                .fixedSize(horizontal: false, vertical: true)
        }
        .font(DS.Font.caption)
        .foregroundStyle(.secondary)
        .padding(DS.Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DS.Color.separator.opacity(0.35), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
    }

    private func succeeded(_ host: Host) -> some View {
        VStack(alignment: .leading, spacing: DS.Space.l) {
            KeyValueGrid([
                KeyValueRow(String.ui("host.field.address", "주소"), "\(host.address):\(host.port)", monospaced: true),
                KeyValueRow(String.ui("host.field.alias", "SSH 별칭"), host.sshAlias, monospaced: true),
                KeyValueRow(String.ui("host.field.session", "세션"), host.session.map { "\($0.user) · \(HostOS.sessionTitle($0))" } ?? "—"),
                KeyValueRow(String.ui("host.field.agent", "에이전트"), host.agentVersion ?? "—"),
            ])
            .dsCard()
            CapabilitySummary(host: host)
        }
    }

    // MARK: Footer

    @ViewBuilder
    private var footer: some View {
        HStack(spacing: DS.Space.s) {
            switch flow.stage {
            case .form:
                Spacer()
                Button(String.ui("action.cancel", "취소"), role: .cancel) { flow.dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(String.ui("addHost.start", "설치하고 연결")) { flow.start() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
                    .disabled(!flow.canStart)
            case .running:
                Text(runningStatus)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
                Spacer()
                Button(String.ui("addHost.continueInBackground", "백그라운드에서 계속")) { flow.dismiss() }
                    .keyboardShortcut(.cancelAction)
            case .failed:
                Button(String.ui("action.close", "닫기")) { flow.dismiss() }
                    .keyboardShortcut(.cancelAction)
                Spacer()
                Button(String.ui("addHost.edit", "설정 수정")) { flow.edit() }
                Button(String.ui("action.retry", "다시 시도")) { flow.start() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
            case .succeeded:
                Spacer()
                Button(String.ui("action.done", "완료")) { flow.finish() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
            }
        }
        .controlSize(.large)
    }

    private var runningStatus: String {
        let done = flow.steps.filter { $0.status == .done }.count
        let current = min(done + 1, flow.steps.count)
        return .ui("addHost.progress", "\(current)/\(flow.steps.count)단계 진행 중")
    }
}

/// Capsule button offering one SSH alias.
struct AliasChip: View {
    let alias: String
    let selected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(alias)
                .font(DS.Font.mono)
                .lineLimit(1)
                .padding(.horizontal, DS.Space.s)
                .padding(.vertical, DS.Space.xs)
                .foregroundStyle(selected ? AnyShapeStyle(.white) : AnyShapeStyle(.primary))
                .background(selected ? AnyShapeStyle(DS.Color.accent.gradient) : AnyShapeStyle(DS.Color.separator.opacity(0.5)),
                            in: Capsule())
                .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(String.ui("a11y.aliasChip", "SSH 별칭 \(alias) 사용"))
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// Capability chips of a connected computer with a ready count and the reasons for capabilities
/// that are not ready; a short note instead while the computer is not connected.
public struct CapabilitySummary: View {
    public let host: Host

    public init(host: Host) {
        self.host = host
    }

    private var isConnected: Bool {
        host.connection == .ready || host.connection == .degraded
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            HStack(alignment: .firstTextBaseline) {
                Text(String.ui("host.capabilities", "기능"))
                    .font(DS.Font.headline)
                    .accessibilityAddTraits(.isHeader)
                Spacer()
                if isConnected {
                    Text(host.readyCapabilityCount == host.caps.items.count
                         ? String.ui("host.capabilities.allReady", "모두 준비됨")
                         : String.ui("host.capabilities.count", "\(host.caps.items.count)개 중 \(host.readyCapabilityCount)개 준비됨"))
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                }
            }
            if isConnected {
                FlowLayout(spacing: DS.Space.s, lineSpacing: DS.Space.s) {
                    ForEach(host.orderedCapabilities, id: \.name) { item in
                        CapabilityChip(name: CapabilityCatalog.title(item.name), state: item.capability.capabilityState,
                                       reason: item.capability.reason)
                    }
                }
                ForEach(Array(unreadyReasons.enumerated()), id: \.offset) { _, line in
                    Text(line)
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            } else {
                Text(String.ui("host.capabilities.offline", "연결되면 화면, 입력, 명령, 파일 같은 기능의 준비 상태를 보여 줍니다."))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
            }
        }
    }

    /// `이름, 이름: 이유` for capabilities that are not ready, grouped by reason.
    private var unreadyReasons: [String] {
        var order: [String] = []
        var names: [String: [String]] = [:]
        for item in host.orderedCapabilities where item.capability.capabilityState != .ready {
            guard let reason = item.capability.reason, !reason.isEmpty else { continue }
            if names[reason] == nil { order.append(reason) }
            names[reason, default: []].append(CapabilityCatalog.title(item.name))
        }
        return order.map { reason in
            let joined = names[reason, default: []].joined(separator: ", ")
            return String.ui("host.capabilities.reason", "\(joined): \(reason)")
        }
    }
}
