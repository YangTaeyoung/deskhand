import SwiftUI

/// The quick switch palette (⌘K) over the window: a search field, the matching computers,
/// tabs, commands and recent work in sections or best first, and the keys it takes. Clicking
/// outside the palette closes it.
public struct QuickSwitchOverlay: View {
    let store: AppStore
    @Environment(\.colorScheme) private var colorScheme

    public init(store: AppStore) {
        self.store = store
    }

    public var body: some View {
        if store.quickSwitch.isPresented {
            ZStack(alignment: .top) {
                Color.black.opacity(colorScheme == .dark ? 0.24 : 0.1)
                    .contentShape(Rectangle())
                    .onTapGesture { store.quickSwitch.dismiss() }
                    .accessibilityHidden(true)
                QuickSwitchPalette(store: store, model: store.quickSwitch)
                    .padding(.top, 72)
            }
            .transition(.opacity)
        }
    }
}

/// The palette panel itself.
public struct QuickSwitchPalette: View {
    let store: AppStore
    @Bindable var model: QuickSwitchModel
    @Environment(\.openSettings) private var openSettings
    @Environment(\.colorScheme) private var colorScheme

    static let width: CGFloat = 640
    /// Height of the result list at most; shorter lists take their rows' height.
    static let listHeight: CGFloat = 400
    static let emptyHeight: CGFloat = 300
    static let rowHeight: CGFloat = 44
    static let headerHeight: CGFloat = 32

    /// The list's height for `sections`: its rows, headers and padding, up to `listHeight`.
    static func height(of sections: [QuickSwitchSection]) -> CGFloat {
        let rows = sections.reduce(0) { $0 + $1.rows.count }
        let headers = sections.filter { $0.title != nil }.count
        return min(CGFloat(rows) * rowHeight + CGFloat(headers) * headerHeight + DS.Space.s * 2, listHeight)
    }

    public init(store: AppStore, model: QuickSwitchModel) {
        self.store = store
        self.model = model
    }

    public var body: some View {
        let sections = QuickSwitchRanking.sections(query: model.query, items: QuickSwitchCatalog.items(store: store),
                                                   recents: model.recents)
        let rows = sections.flatMap(\.rows)
        VStack(spacing: 0) {
            searchBar(rows: rows)
            Divider()
            if rows.isEmpty {
                QuickSwitchEmpty(query: model.query)
                    .frame(height: Self.emptyHeight)
            } else {
                list(sections: sections, rows: rows)
            }
            Divider()
            QuickSwitchFooter()
        }
        .frame(width: Self.width)
        .background(DS.Color.floatingSurface)
        .clipShape(RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
        .shadow(color: .black.opacity(colorScheme == .dark ? 0.5 : 0.22), radius: 28, y: 14)
        .onChange(of: model.pendingKey) {
            guard let key = model.takePendingKey() else { return }
            handle(key, rows: rows)
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("quickSwitch.a11y", "빠른 전환"))
        .accessibilityAddTraits(.isModal)
    }

    private func searchBar(rows: [QuickSwitchRow]) -> some View {
        HStack(spacing: DS.Space.m) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 18, weight: .medium))
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            QuickSwitchField(text: $model.query,
                             placeholder: String.ui("quickSwitch.placeholder", "컴퓨터, 탭, 명령 검색")) { key in
                handle(key, rows: rows)
            }
            .frame(height: 28)
            if !model.query.isEmpty {
                Button {
                    model.query = ""
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
                .help(String.ui("quickSwitch.clear", "검색어 지우기"))
                .accessibilityLabel(String.ui("quickSwitch.clear", "검색어 지우기"))
            }
        }
        .padding(.horizontal, DS.Space.l)
        .frame(height: 56)
    }

    private func list(sections: [QuickSwitchSection], rows: [QuickSwitchRow]) -> some View {
        let selectedID = rows.indices.contains(model.selection) ? rows[model.selection].id : nil
        let indexByID = Dictionary(rows.enumerated().map { ($1.id, $0) }, uniquingKeysWith: { first, _ in first })
        return ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(sections) { section in
                        if let title = section.title {
                            Text(title)
                                .font(DS.Font.caption.weight(.semibold))
                                .foregroundStyle(.secondary)
                                .padding(.horizontal, DS.Space.m)
                                .padding(.bottom, DS.Space.xs)
                                .frame(maxWidth: .infinity, alignment: .bottomLeading)
                                .frame(height: QuickSwitchPalette.headerHeight, alignment: .bottomLeading)
                                .accessibilityAddTraits(.isHeader)
                        }
                        ForEach(section.rows) { row in
                            let index = indexByID[row.id] ?? 0
                            QuickSwitchRowView(row: row, index: index, selected: row.id == selectedID,
                                               showsKind: section.kind == .results) {
                                open(row.item)
                            }
                            .id(row.id)
                        }
                    }
                }
                .padding(DS.Space.s)
            }
            .frame(height: Self.height(of: sections))
            .onChange(of: selectedID) { _, id in
                guard let id else { return }
                proxy.scrollTo(id)
            }
        }
    }

    private func handle(_ key: QuickSwitchKey, rows: [QuickSwitchRow]) {
        if let item = model.handle(key, rows: rows) { open(item) }
    }

    private func open(_ item: QuickSwitchItem) {
        store.activate(item) { openSettings() }
    }
}

/// One result: the item's symbol tile, its title with the matched characters in bold, its
/// subtitle, its own shortcut and, for the first nine rows, the ⌘ digit that opens it.
struct QuickSwitchRowView: View {
    let row: QuickSwitchRow
    let index: Int
    let selected: Bool
    let showsKind: Bool
    let open: () -> Void
    @State private var hovered = false

    private var item: QuickSwitchItem { row.item }

    var body: some View {
        Button(action: open) {
            HStack(spacing: DS.Space.m) {
                tile
                VStack(alignment: .leading, spacing: 1) {
                    Text(title)
                        .lineLimit(1)
                    if let subtitle = subtitleText {
                        Text(subtitle)
                            .font(DS.Font.caption)
                            .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.secondary))
                            .lineLimit(1)
                    }
                }
                Spacer(minLength: DS.Space.s)
                if let shortcut = item.shortcut {
                    Text(shortcut)
                        .font(DS.Font.caption)
                        .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.secondary))
                }
                if index < 9 {
                    QuickSwitchKeycap(text: "⌘\(index + 1)", inverted: selected)
                }
            }
            .padding(.horizontal, DS.Space.m)
            .frame(height: QuickSwitchPalette.rowHeight)
            .background(background, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovered = $0 }
        .accessibilityLabel(accessibilityText)
        .accessibilityAddTraits(selected ? [.isSelected] : [])
    }

    private var background: Color {
        if selected { return DS.Status.selectionFill }
        return hovered ? DS.Color.neutral.opacity(0.1) : .clear
    }

    private var subtitleText: String? {
        guard showsKind else { return item.subtitle }
        return [item.kind.title, item.subtitle].compactMap { $0 }.joined(separator: " · ")
    }

    /// The title with the matched characters in semibold, in the accent text color unless selected.
    private var title: AttributedString {
        var text = AttributedString(item.title)
        text.font = DS.Font.body
        text.foregroundColor = selected ? DS.Color.onFill : .primary
        let characters = Array(item.title)
        let matched = Set(row.highlights)
        var cursor = text.startIndex
        for offset in characters.indices {
            let next = text.characters.index(after: cursor)
            if matched.contains(offset) {
                text[cursor..<next].font = DS.Font.body.weight(.bold)
                if !selected { text[cursor..<next].foregroundColor = DS.Status.accentText }
            }
            cursor = next
        }
        return text
    }

    private var tile: some View {
        RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
            .fill(selected ? AnyShapeStyle(DS.Color.onFill.opacity(0.2)) : AnyShapeStyle(tint.opacity(0.14)))
            .frame(width: 28, height: 28)
            .overlay {
                Image(systemName: item.symbol)
                    .font(.system(size: 14, weight: .medium))
                    .foregroundStyle(selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(tint))
            }
            .overlay(alignment: .bottomTrailing) {
                if let connection = item.connection {
                    StatusDot(state: connection)
                        .background(Circle().fill(DS.Color.surface).frame(width: 10, height: 10))
                        .offset(x: 4, y: 4)
                }
            }
            .accessibilityHidden(true)
    }

    private var tint: Color {
        switch item.tone {
        case .accent: DS.Color.accent
        case .neutral: DS.Color.neutral
        case .danger: DS.Color.danger
        case .human: DS.Color.human
        case .ai: DS.Color.ai
        case .success: DS.Color.success
        case .warning: DS.Color.warning
        }
    }

    private var accessibilityText: String {
        var parts = [item.title, item.kind.title]
        if let subtitle = item.subtitle { parts.append(subtitle) }
        if let shortcut = item.shortcut { parts.append(shortcut) }
        return parts.joined(separator: ", ")
    }
}

/// A key in a rounded box, as the palette shows shortcuts.
struct QuickSwitchKeycap: View {
    let text: String
    var inverted = false

    var body: some View {
        Text(text)
            .font(DS.Font.caption.monospacedDigit())
            .foregroundStyle(inverted ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(.secondary))
            .padding(.horizontal, 5)
            .frame(minWidth: 22, minHeight: 18)
            .background(inverted ? DS.Color.onFill.opacity(0.18) : DS.Color.neutral.opacity(0.12),
                        in: RoundedRectangle(cornerRadius: 4, style: .continuous))
            .accessibilityHidden(true)
    }
}

/// What the palette shows when nothing matches: the query and how to search.
struct QuickSwitchEmpty: View {
    let query: String

    var body: some View {
        VStack(spacing: DS.Space.l) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 32, weight: .regular))
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            VStack(spacing: DS.Space.xs) {
                Text(String.ui("quickSwitch.empty.title", "‘\(query)’ 검색 결과가 없습니다"))
                    .font(DS.Font.headline)
                    .lineLimit(2)
                    .multilineTextAlignment(.center)
                Text(String.ui("quickSwitch.empty.message", "컴퓨터 이름, 탭, 명령, 최근 전송·디버그 세션·QA 실행을 찾습니다."))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            VStack(alignment: .leading, spacing: DS.Space.s) {
                hint(String.ui("quickSwitch.empty.hint.chosung", "초성으로 찾기"), example: "ㅌㅁㄴ → 터미널")
                hint(String.ui("quickSwitch.empty.hint.english", "영어 이름으로 찾기"), example: "term → 터미널")
                hint(String.ui("quickSwitch.empty.hint.words", "여러 단어로 좁히기"), example: "개발 파일")
            }
            .padding(DS.Space.m)
            .background(DS.Color.neutral.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        }
        .padding(DS.Space.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .combine)
    }

    private func hint(_ title: String, example: String) -> some View {
        HStack(spacing: DS.Space.m) {
            Text(title)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .frame(width: 120, alignment: .leading)
            Text(verbatim: example)
                .font(DS.Font.caption.weight(.medium))
        }
    }
}

/// The keys the palette takes.
struct QuickSwitchFooter: View {
    var body: some View {
        HStack(spacing: DS.Space.l) {
            hint("↑↓", String.ui("quickSwitch.key.move", "이동"))
            hint("↩", String.ui("quickSwitch.key.open", "열기"))
            hint("⌘1–9", String.ui("quickSwitch.key.direct", "바로 열기"))
            hint("esc", String.ui("quickSwitch.key.close", "닫기"))
            Spacer(minLength: 0)
        }
        .padding(.horizontal, DS.Space.l)
        .frame(height: 36)
        .background(DS.Color.surfaceRaised.opacity(0.5))
        .accessibilityElement(children: .combine)
    }

    private func hint(_ key: String, _ title: String) -> some View {
        HStack(spacing: DS.Space.xs) {
            QuickSwitchKeycap(text: key)
            Text(title)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
        }
    }
}
