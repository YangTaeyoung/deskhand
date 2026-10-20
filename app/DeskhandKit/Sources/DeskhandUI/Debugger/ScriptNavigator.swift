import DeskhandClient
import SwiftUI

/// Opens a loaded script in the source panel (⇧⌘O): a search field matching file names fuzzily,
/// the matches grouped by target with their paths below, and Node's own scripts only when the
/// person asks for them. ↑ and ↓ move the highlight and ↩ opens it.
public struct ScriptNavigator: View {
    static let width: CGFloat = 440

    let session: DebugSessionModel
    let close: () -> Void
    @State private var query: String
    @State private var showsInternal: Bool
    @State private var highlighted: String?
    @FocusState private var searchFocused: Bool

    public init(session: DebugSessionModel, query: String = "", showsInternal: Bool = false, close: @escaping () -> Void) {
        self.session = session
        self.close = close
        _query = State(initialValue: query)
        _showsInternal = State(initialValue: showsInternal)
    }

    struct Group: Identifiable {
        var target: DebugTarget
        var scripts: [DebugScript]

        var id: String { target.id }
    }

    /// Matching scripts per target, in the session's target order, best match first; the row
    /// highlighted without a choice is the first.
    var groups: [Group] {
        session.info.targets.compactMap { target in
            let scripts = session.scripts.filter { $0.targetID == target.id }
            let ranked = ScriptSearch.rank(scripts.map(\.url), query: query, includeInternal: showsInternal)
            var seen = Set<String>()
            let ordered = ranked.compactMap { match -> DebugScript? in
                guard seen.insert(match.url).inserted else { return nil }
                return scripts.first { $0.url == match.url }
            }
            return ordered.isEmpty ? nil : Group(target: target, scripts: ordered)
        }
    }

    private var internalCount: Int { session.scripts.filter { ScriptURL.isInternal($0.url) }.count }

    public var body: some View {
        let groups = groups
        let flat = groups.flatMap(\.scripts)
        VStack(spacing: 0) {
            HStack(spacing: DS.Space.s) {
                SearchField(text: $query, prompt: String.ui("debugger.scripts.search", "파일 이름으로 찾기"), focused: $searchFocused)
                    .onKeyPress(.downArrow) {
                        move(by: 1, in: flat)
                        return .handled
                    }
                    .onKeyPress(.upArrow) {
                        move(by: -1, in: flat)
                        return .handled
                    }
                    .onSubmit { open(flat.first { $0.id == highlighted } ?? flat.first) }
            }
            .padding(DS.Space.s)
            Divider()
            list(groups)
                .frame(height: 320)
            Divider()
            HStack(spacing: DS.Space.s) {
                Toggle(String.ui("debugger.scripts.internal", "Node 내부 스크립트 보기"), isOn: $showsInternal)
                    .toggleStyle(.checkbox)
                    .font(DS.Font.caption)
                    .disabled(internalCount == 0)
                Spacer(minLength: DS.Space.s)
                Text(String.ui("debugger.scripts.keys", "↑↓ 고르기 · ↩ 열기"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, DS.Space.m)
            .padding(.vertical, DS.Space.s)
        }
        .frame(width: Self.width)
        .task {
            searchFocused = true
            await session.loadScripts()
        }
        .onChange(of: query) { highlighted = nil }
    }

    @ViewBuilder
    private func list(_ groups: [Group]) -> some View {
        if groups.isEmpty {
            if let error = session.scriptsError {
                RetryPanel(symbol: "doc.questionmark", title: String.ui("debugger.scripts.failed", "스크립트 목록을 불러오지 못했습니다"),
                           error: error) { Task { await session.loadScripts() } }
            } else if session.scripts.isEmpty {
                DebugLoadingLine(text: String.ui("debugger.scripts.loading", "로드된 스크립트를 불러오는 중…"))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                DebugPaneHint(text: String.ui("debugger.scripts.none", "이름이 일치하는 스크립트가 없습니다."))
            }
        } else {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 0, pinnedViews: [.sectionHeaders]) {
                        ForEach(groups) { group in
                            Section {
                                ForEach(group.scripts) { script in
                                    let first = highlighted == nil && script.id == groups.first?.scripts.first?.id
                                    ScriptRow(script: script, highlighted: highlighted == script.id || first) {
                                        open(script)
                                    }
                                    .id(script.id)
                                }
                            } header: {
                                HStack(spacing: DS.Space.s) {
                                    RoleBadge(target: group.target, fixedWidth: true)
                                    Text(DebugFormat.targetTitle(group.target))
                                        .font(DS.Font.caption.weight(.semibold))
                                        .foregroundStyle(.secondary)
                                        .lineLimit(1)
                                    Spacer(minLength: 0)
                                }
                                .padding(.horizontal, DS.Space.m)
                                .frame(height: 24)
                                .background(DS.Color.surface)
                            }
                        }
                    }
                    .padding(.bottom, DS.Space.xs)
                }
                .onChange(of: highlighted) { _, id in
                    if let id { proxy.scrollTo(id) }
                }
            }
        }
    }

    private func move(by delta: Int, in flat: [DebugScript]) {
        guard !flat.isEmpty else { return }
        let index = flat.firstIndex { $0.id == highlighted } ?? 0
        highlighted = flat[min(max(index + delta, 0), flat.count - 1)].id
    }

    private func open(_ script: DebugScript?) {
        guard let script else { return }
        session.open(SourceFocus(targetID: script.targetID, scriptID: script.scriptID, url: script.url, line: 1))
        close()
    }
}

private struct ScriptRow: View {
    let script: DebugScript
    let highlighted: Bool
    let open: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: open) {
            HStack(spacing: DS.Space.s) {
                Image(systemName: "doc.text")
                    .font(.system(size: 12))
                    .foregroundStyle(highlighted ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.secondary))
                    .frame(width: 16)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 0) {
                    Text(ScriptURL.fileName(script.url))
                        .font(DS.Font.body)
                        .foregroundStyle(highlighted ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.primary))
                        .lineLimit(1)
                    Text(ScriptURL.display(script.url))
                        .font(DS.Font.caption)
                        .foregroundStyle(highlighted ? AnyShapeStyle(DS.Color.onFill.opacity(0.85)) : AnyShapeStyle(.secondary))
                        .lineLimit(1)
                        .truncationMode(.head)
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, DS.Space.m)
            .frame(height: 36)
            .background(background)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(script.url)
        .accessibilityLabel(ScriptURL.fileName(script.url))
        .accessibilityHint(script.url)
        .accessibilityAddTraits(highlighted ? [.isSelected] : [])
    }

    private var background: AnyShapeStyle {
        if highlighted { return AnyShapeStyle(DS.Color.accent) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.1)) : AnyShapeStyle(.clear)
    }
}
