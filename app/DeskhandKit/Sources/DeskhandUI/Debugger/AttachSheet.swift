import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// A running process the attach sheet offers, with what makes it debuggable.
public struct AttachCandidate: Identifiable, Equatable, Sendable {
    public enum Kind: Equatable, Sendable {
        case electron
        case node
        case other
    }

    public var process: RemoteProcess
    public var kind: Kind
    /// The process already listens with `--inspect` or `--remote-debugging-port`.
    public var inspectorOpen: Bool

    public var id: Int { process.pid }

    /// Electron apps (a process with `--type=renderer` children, other than browsers) and Node
    /// processes first, then the rest when `all`; helper processes (`--type=…`) and the system are left out.
    public static func list(_ processes: [RemoteProcess], all: Bool, query: String = "") -> [AttachCandidate] {
        let browsers: Set<String> = ["chrome.exe", "msedge.exe", "msedgewebview2.exe", "brave.exe", "firefox.exe", "opera.exe"]
        let rendererParents = Set(processes.filter { $0.cmd?.contains("--type=renderer") ?? false }.map(\.ppid))
        let needle = query.trimmingCharacters(in: .whitespaces)
        let candidates = processes.compactMap { process -> AttachCandidate? in
            guard process.pid > 4, !(process.cmd?.contains("--type=") ?? false), !process.isSelf else { return nil }
            let name = process.name.lowercased()
            let kind: Kind
            if name == "node.exe" {
                kind = .node
            } else if rendererParents.contains(process.pid), !browsers.contains(name) {
                kind = .electron
            } else {
                kind = .other
            }
            guard all || kind != .other else { return nil }
            if !needle.isEmpty {
                let matches = process.name.localizedCaseInsensitiveContains(needle) || String(process.pid).contains(needle)
                    || (process.cmd?.localizedCaseInsensitiveContains(needle) ?? false)
                guard matches else { return nil }
            }
            let cmd = process.cmd ?? ""
            return AttachCandidate(process: process, kind: kind,
                                   inspectorOpen: cmd.contains("--inspect") || cmd.contains("--remote-debugging-port"))
        }
        return candidates.sorted { a, b in
            if (a.kind == .other) != (b.kind == .other) { return b.kind == .other }
            return a.process.name.localizedStandardCompare(b.process.name) == .orderedAscending
        }
    }
}

/// Attaches to a running app: by process (Electron apps and Node processes, or every process),
/// or by a remote loopback port with an optional role.
public struct AttachSheet: View {
    public enum Mode: String, CaseIterable, Identifiable, Sendable {
        case process
        case port

        public var id: String { rawValue }
    }

    let model: DebuggerModel
    let host: Host
    let processes: any ProcessesBackend
    let dismiss: () -> Void
    @State private var mode: Mode
    @State private var list: [RemoteProcess]?
    @State private var listError: OperationError?
    @State private var showsAll = false
    @State private var query = ""
    @State private var selectedPID: Int?
    @State private var port: String
    @State private var role: DebugTargetRole?
    @State private var error: OperationError?
    @FocusState private var searchFocused: Bool

    public init(model: DebuggerModel, host: Host, processes: any ProcessesBackend, mode: Mode = .process, list: [RemoteProcess]? = nil,
                selectedPID: Int? = nil, port: String = "9229", error: OperationError? = nil, dismiss: @escaping () -> Void) {
        self.model = model
        self.host = host
        self.processes = processes
        self.dismiss = dismiss
        _mode = State(initialValue: mode)
        _list = State(initialValue: list)
        _selectedPID = State(initialValue: selectedPID)
        _port = State(initialValue: port)
        _error = State(initialValue: error)
    }

    private var attaching: Bool { model.starting?.kind == .attach }

    public var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.l) {
            HStack(alignment: .top, spacing: DS.Space.m) {
                Image(systemName: "cable.connector")
                    .font(.system(.largeTitle))
                    .foregroundStyle(DS.Color.accent)
                    .frame(width: 36)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    Text(String.ui("debugger.attach.title", "실행 중인 앱에 연결"))
                        .font(DS.Font.title)
                        .accessibilityAddTraits(.isHeader)
                    Text(String.ui("debugger.attach.subtitle", "\(host.name)에서 실행 중인 Electron·Node 앱에 디버거를 연결합니다.\n앱은 다시 시작하지 않습니다."))
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Picker(String.ui("debugger.attach.mode", "연결 방법"), selection: $mode) {
                Text(String.ui("debugger.attach.byProcess", "프로세스")).tag(Mode.process)
                Text(String.ui("debugger.attach.byPort", "포트")).tag(Mode.port)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            switch mode {
            case .process: processPane
            case .port: portPane
            }
            if attaching {
                StatusNotice(symbol: nil, tint: DS.Color.accent, title: String.ui("debugger.attach.running", "연결하는 중…"),
                             message: String.ui("debugger.attach.running.detail", "검사기를 켜고 대상 목록을 읽습니다."))
            } else if let error {
                StatusNotice(symbol: "exclamationmark.octagon.fill", tint: DS.Color.danger,
                             title: String.ui("debugger.attach.failed", "연결하지 못했습니다"),
                             message: [error.message.asSentence, Self.fix(error)].compactMap { $0 }.joined(separator: "\n"), code: error.code)
            }
            HStack {
                Spacer()
                Button(String.ui("action.cancel", "취소"), action: dismiss)
                    .keyboardShortcut(.cancelAction)
                Button {
                    attach()
                } label: {
                    Text(String.ui("debugger.attach.connect", "연결"))
                        .frame(minWidth: 56)
                }
                .keyboardShortcut(.defaultAction)
                .disabled(!canAttach || attaching)
            }
            .controlSize(.large)
        }
        .padding(DS.Space.xl)
        .frame(width: 600)
        .task {
            if list == nil { await loadProcesses() }
        }
    }

    static func fix(_ error: OperationError) -> String? {
        switch error.code {
        case "CAPABILITY_UNSUPPORTED": .ui("debugger.fix.notNode", "Node나 Electron 앱의 메인 프로세스를 고르세요. 브라우저와 일반 Windows 앱은 연결할 수 없습니다.")
        case "NOT_FOUND": .ui("debugger.fix.port", "앱을 --inspect=포트 또는 --remote-debugging-port=포트로 실행했는지, 번호가 맞는지 확인하세요.")
        default: error.fix
        }
    }

    private var canAttach: Bool {
        switch mode {
        case .process: selectedPID != nil
        case .port: Int(port).map { (1...65535).contains($0) } ?? false
        }
    }

    private var processPane: some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            HStack(spacing: DS.Space.s) {
                SearchField(text: $query, prompt: String.ui("debugger.attach.search", "이름, PID, 명령줄 검색"), focused: $searchFocused)
                    .frame(maxWidth: .infinity)
                Toggle(String.ui("debugger.attach.all", "모든 프로세스"), isOn: $showsAll)
                    .toggleStyle(.checkbox)
                    .fixedSize()
                Button {
                    Task { await loadProcesses() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .help(String.ui("processes.refresh", "새로 고침"))
                .accessibilityLabel(String.ui("processes.refresh", "새로 고침"))
            }
            processList
                .frame(height: 260)
                .background(DS.Code.background)
                .clipShape(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
            Text(String.ui("debugger.attach.pidNote", "프로세스로 연결하면 메인 프로세스의 Node 검사기를 켜고 그 포트를 찾아 연결합니다. 연결을 해제하면 켠 검사기를 끌 수 있을 때 끕니다. 렌더러는 앱을 --remote-debugging-port로 시작했을 때만 함께 보입니다."))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    @ViewBuilder
    private var processList: some View {
        if let list {
            let candidates = AttachCandidate.list(list, all: showsAll, query: query)
            if candidates.isEmpty {
                DebugPanePlaceholder(symbol: "magnifyingglass",
                                     title: query.isEmpty ? String.ui("debugger.attach.none", "Electron이나 Node 앱이 실행 중이 아닙니다")
                                                          : String.ui("debugger.attach.searchNone", "일치하는 프로세스가 없습니다"),
                                     message: showsAll ? nil : String.ui("debugger.attach.none.hint", "‘모든 프로세스’를 켜면 다른 프로세스도 보입니다."))
            } else {
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(candidates) { candidate in
                            CandidateRow(candidate: candidate, selected: selectedPID == candidate.process.pid) {
                                selectedPID = candidate.process.pid
                            }
                        }
                    }
                    .padding(.vertical, DS.Space.xs)
                }
            }
        } else if let listError {
            DebugPanePlaceholder(symbol: "exclamationmark.triangle", title: String.ui("processes.failed", "프로세스 목록을 불러오지 못했습니다"),
                                 message: listError.message)
        } else {
            DebugLoadingLine(text: String.ui("processes.loading", "프로세스 목록을 불러오는 중…"))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private var portPane: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.m) {
                GridRow {
                    Text(String.ui("debugger.attach.port", "포트"))
                        .foregroundStyle(.secondary)
                        .gridColumnAlignment(.trailing)
                    TextField("9229", text: $port)
                        .font(DS.Font.mono)
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 120)
                        .accessibilityLabel(String.ui("debugger.attach.port", "포트"))
                }
                GridRow {
                    Text(String.ui("debugger.attach.role", "종류"))
                        .foregroundStyle(.secondary)
                    Picker(String.ui("debugger.attach.role", "종류"), selection: $role) {
                        Text(String.ui("debugger.attach.role.auto", "자동으로 알아내기")).tag(DebugTargetRole?.none)
                        Text(String.ui("debugger.attach.role.main", "메인 (Node 검사기, --inspect)")).tag(DebugTargetRole?.some(.main))
                        Text(String.ui("debugger.attach.role.renderer", "렌더러 (DevTools, --remote-debugging-port)")).tag(DebugTargetRole?.some(.renderer))
                    }
                    .labelsHidden()
                    .fixedSize()
                }
            }
            Text(String.ui("debugger.attach.portNote", "원격 PC의 127.0.0.1 포트입니다. Deskhand가 이 포트로 터널을 열고, 연결을 해제하면 터널만 닫습니다."))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.vertical, DS.Space.xs)
    }

    private func loadProcesses() async {
        listError = nil
        do {
            list = try await processes.procList(hostID: host.hostID)
        } catch {
            listError = OperationError(error)
        }
    }

    private func attach() {
        let request: DebugAttachRequest
        let name: String
        switch mode {
        case .process:
            guard let pid = selectedPID else { return }
            request = DebugAttachRequest(hostID: host.hostID, target: .pid(pid))
            let processName = list?.first { $0.pid == pid }?.name ?? "PID \(pid)"
            name = processName.lowercased().hasSuffix(".exe") ? String(processName.dropLast(4)) : processName
        case .port:
            guard let number = Int(port) else { return }
            request = DebugAttachRequest(hostID: host.hostID, target: .port(number, role: role))
            name = .ui("debugger.attach.portName", "포트 \(port)")
        }
        error = nil
        Task {
            if let failure = await model.attach(request, name: name) {
                error = failure
            } else {
                dismiss()
            }
        }
    }
}

private struct CandidateRow: View {
    let candidate: AttachCandidate
    let selected: Bool
    let select: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: select) {
            HStack(alignment: .center, spacing: DS.Space.s) {
                Image(systemName: candidate.kind == .node ? "terminal" : "macwindow")
                    .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.secondary))
                    .frame(width: 20)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 0) {
                    HStack(spacing: DS.Space.s) {
                        Text(candidate.process.name)
                            .font(DS.Font.body.weight(.medium))
                            .lineLimit(1)
                        Text(String.ui("processes.pidLabel", "PID \(String(candidate.process.pid))"))
                            .font(.system(.subheadline, design: .monospaced))
                            .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill.opacity(0.85)) : AnyShapeStyle(.secondary))
                        badge
                    }
                    Text(candidate.process.cmd ?? candidate.process.path ?? "")
                        .font(.system(.subheadline, design: .monospaced))
                        .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill.opacity(0.85)) : AnyShapeStyle(.secondary))
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                Spacer(minLength: 0)
            }
            .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.primary))
            .padding(.horizontal, DS.Space.m)
            .padding(.vertical, DS.Space.xs)
            .background(selected ? AnyShapeStyle(DS.Status.selectionFill) : (hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.1)) : AnyShapeStyle(.clear)))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .accessibilityAddTraits(selected ? [.isSelected] : [])
    }

    @ViewBuilder
    private var badge: some View {
        switch candidate.kind {
        case .electron: tag("Electron")
        case .node: tag("Node")
        case .other: EmptyView()
        }
        if candidate.inspectorOpen {
            tag(String.ui("debugger.attach.inspectorOpen", "검사기 열림"))
        }
    }

    private func tag(_ text: String) -> some View {
        Text(text)
            .font(.system(.caption, weight: .semibold))
            .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(DS.Status.accentText))
            .padding(.horizontal, DS.Space.s)
            .frame(minHeight: 16)
            .background(selected ? DS.Color.onFill.opacity(0.2) : DS.Color.accent.opacity(0.14), in: Capsule())
    }
}
