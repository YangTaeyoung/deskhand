import SwiftUI

/// The quick switch palette (⌘K) over the window: the window dimmed behind it, the panel
/// centered under the workspace tab bar. Clicking outside the panel closes it.
public struct QuickSwitchOverlay: View {
    let store: AppStore
    let chrome: WorkspaceChrome

    public init(store: AppStore, chrome: WorkspaceChrome = .native) {
        self.store = store
        self.chrome = chrome
    }

    /// Distance from the top of the content to the panel: below the tab bar, and below the
    /// toolbar strip when the toolbar is drawn as part of the content.
    private var topInset: CGFloat {
        let toolbar = chrome == .standIn ? WorkspaceToolbarStrip.height : 0
        return toolbar + WorkspaceTabBar.height + DS.Space.s
    }

    public var body: some View {
        if store.quickSwitch.isPresented {
            ZStack(alignment: .top) {
                DS.Color.scrim.opacity(0.5)
                    .ignoresSafeArea()
                    .contentShape(Rectangle())
                    .onTapGesture { store.quickSwitch.dismiss() }
                    .accessibilityHidden(true)
                QuickSwitchPalette(store: store, model: store.quickSwitch, chrome: chrome)
                    .padding(.top, topInset)
            }
            .transition(.opacity)
        }
    }
}

/// The palette panel: the search field, the ranked rows, and the keys it takes.
public struct QuickSwitchPalette: View {
    let store: AppStore
    @Bindable var model: QuickSwitchModel
    let chrome: WorkspaceChrome
    @Environment(\.openSettings) private var openSettings
    @Environment(\.colorScheme) private var colorScheme

    static let width: CGFloat = 640
    /// Height the result list takes at most.
    static let listLimit: CGFloat = 400
    static let emptyHeight: CGFloat = 280
    static let rowHeight: CGFloat = 44
    static let headerHeight: CGFloat = 32
    static let listPadding: CGFloat = DS.Space.s

    public init(store: AppStore, model: QuickSwitchModel, chrome: WorkspaceChrome = .native) {
        self.store = store
        self.model = model
        self.chrome = chrome
    }

    /// The list's height for `sections`: its headers and rows up to `listLimit`, ending after a
    /// whole row.
    static func height(of sections: [QuickSwitchSection]) -> CGFloat {
        var height = listPadding * 2
        for section in sections {
            if section.title != nil {
                guard height + headerHeight + rowHeight <= listLimit else { return height }
                height += headerHeight
            }
            for _ in section.rows {
                guard height + rowHeight <= listLimit else { return height }
                height += rowHeight
            }
        }
        return height
    }

    public var body: some View {
        let items = QuickSwitchCatalog.items(store: store)
        VStack(spacing: 0) {
            searchBar
            Divider()
            if model.rows.isEmpty {
                QuickSwitchEmpty(query: model.query)
                    .frame(height: Self.emptyHeight)
            } else {
                list
            }
            Divider()
            QuickSwitchFooter(hasRows: !model.rows.isEmpty)
        }
        .frame(width: Self.width)
        .background { panelBackground }
        .clipShape(RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
        .shadow(color: DS.Color.overlayShadow, radius: 28, y: 12)
        .onChange(of: items, initial: true) { _, items in model.update(items: items) }
        .onChange(of: model.selectedRow?.id) { _, _ in
            guard let row = model.selectedRow else { return }
            AccessibilityNotification.Announcement(QuickSwitchRowView.accessibilityText(row)).post()
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("quickSwitch.a11y", "빠른 전환"))
        .accessibilityAddTraits(.isModal)
    }

    @ViewBuilder
    private var panelBackground: some View {
        switch chrome {
        case .native: Rectangle().fill(.regularMaterial)
        case .standIn: DS.Color.floatingSurface
        }
    }

    private var searchBar: some View {
        HStack(spacing: DS.Space.m) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 18, weight: .medium))
                .foregroundStyle(DS.Status.neutralText)
                .accessibilityHidden(true)
            QuickSwitchField(text: $model.query,
                             placeholder: String.ui("quickSwitch.placeholder", "PC, 탭, 명령 검색"),
                             clearRequests: model.clearRequests) { key in
                handle(key)
            }
            .frame(height: 28)
            if !model.query.isEmpty {
                Button {
                    model.clearQuery()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(DS.Status.neutralText)
                }
                .buttonStyle(.plain)
                .help(String.ui("quickSwitch.clear", "검색어 지우기"))
                .accessibilityLabel(String.ui("quickSwitch.clear", "검색어 지우기"))
            }
        }
        .padding(.horizontal, DS.Space.l)
        .frame(height: 56)
    }

    private var list: some View {
        let sections = model.sections
        let selectedID = model.selectedRow?.id
        let indexByID = Dictionary(model.rows.enumerated().map { ($1.id, $0) }, uniquingKeysWith: { first, _ in first })
        return ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(sections) { section in
                        if let title = section.title {
                            Text(title)
                                .font(DS.Font.caption.weight(.semibold))
                                .foregroundStyle(DS.Status.neutralText)
                                .padding(.horizontal, DS.Space.m)
                                .padding(.bottom, DS.Space.xs)
                                .frame(maxWidth: .infinity, alignment: .bottomLeading)
                                .frame(height: Self.headerHeight, alignment: .bottomLeading)
                                .accessibilityAddTraits(.isHeader)
                        }
                        ForEach(section.rows) { row in
                            let index = indexByID[row.id] ?? 0
                            QuickSwitchRowView(row: row, selected: row.id == selectedID, showsKind: section.kind == .results) {
                                open(row.item)
                            }
                            .onHover { inside in
                                if inside { model.selection = index }
                            }
                            .id(row.id)
                        }
                    }
                }
                .padding(Self.listPadding)
            }
            .frame(height: Self.height(of: sections))
            .onChange(of: selectedID) { _, id in
                guard let id else { return }
                proxy.scrollTo(id)
            }
        }
    }

    private func handle(_ key: QuickSwitchKey) {
        if let item = model.handle(key) { open(item) }
    }

    private func open(_ item: QuickSwitchItem) {
        store.activate(item) { openSettings() }
    }
}

/// One result: the item's symbol tile, its title with the matched characters in bold, its
/// subtitle, the keyword that matched when the title did not, and the item's own shortcut.
struct QuickSwitchRowView: View {
    let row: QuickSwitchRow
    let selected: Bool
    let showsKind: Bool
    let open: () -> Void

    private var item: QuickSwitchItem { row.item }
    private var secondary: AnyShapeStyle { selected ? AnyShapeStyle(DS.Color.onFill) : AnyShapeStyle(DS.Status.neutralText) }

    var body: some View {
        Button(action: open) {
            HStack(spacing: DS.Space.m) {
                tile
                VStack(alignment: .leading, spacing: 0) {
                    Text(title)
                        .lineLimit(1)
                    if let subtitle = subtitleText {
                        Text(subtitle)
                            .font(DS.Font.caption)
                            .foregroundStyle(secondary)
                            .lineLimit(1)
                    }
                }
                Spacer(minLength: DS.Space.s)
                if let keyword = row.keyword {
                    Text(keyword)
                        .font(DS.Font.caption)
                        .foregroundStyle(secondary)
                        .padding(.horizontal, DS.Space.s)
                        .frame(height: 20)
                        .background(selected ? .clear : DS.Color.neutral.opacity(0.12), in: Capsule())
                        .overlay(Capsule().strokeBorder(selected ? DS.Color.onFill.opacity(0.6) : .clear, lineWidth: 1))
                        .accessibilityHidden(true)
                }
                if let shortcut = item.shortcut {
                    Text(shortcut)
                        .font(DS.Font.caption.monospacedDigit())
                        .foregroundStyle(secondary)
                        .frame(minWidth: DS.Space.xxl, alignment: .trailing)
                        .accessibilityHidden(true)
                }
            }
            .padding(.horizontal, DS.Space.m)
            .frame(height: QuickSwitchPalette.rowHeight)
            .background(selected ? DS.Status.selectionFill : .clear, in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(Self.accessibilityText(row))
        .accessibilityAddTraits(selected ? [.isSelected] : [])
    }

    private var subtitleText: String? {
        guard showsKind else { return item.subtitle }
        return [item.kind.title, item.subtitle].compactMap { $0 }.joined(separator: " · ")
    }

    /// The title with the matched characters in bold, in the accent text color unless selected.
    private var title: AttributedString {
        var text = AttributedString(item.title)
        text.font = DS.Font.body
        text.foregroundColor = selected ? DS.Color.onFill : .primary
        let matched = Set(row.highlights)
        var cursor = text.startIndex
        for offset in 0..<item.title.count {
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
                        .background(Circle().fill(DS.Color.surface).frame(width: 12, height: 12))
                        .offset(x: DS.Space.xs, y: DS.Space.xs)
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

    /// `터미널, 탭, 개발 PC, ⌘T`, with the matched keyword when the title did not match.
    static func accessibilityText(_ row: QuickSwitchRow) -> String {
        let item = row.item
        var parts = [item.title, item.kind.title]
        if let subtitle = item.subtitle { parts.append(subtitle) }
        if let keyword = row.keyword { parts.append(keyword) }
        if let shortcut = item.shortcut { parts.append(shortcut) }
        return parts.joined(separator: ", ")
    }
}

/// What the palette shows when nothing matches: the query and how to search.
struct QuickSwitchEmpty: View {
    let query: String

    var body: some View {
        VStack(spacing: DS.Space.l) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 28, weight: .regular))
                .foregroundStyle(DS.Status.neutralText)
                .accessibilityHidden(true)
            VStack(spacing: DS.Space.xs) {
                Text(String.ui("quickSwitch.empty.title", "‘\(query)’ 검색 결과가 없습니다"))
                    .font(DS.Font.headline)
                    .lineLimit(2)
                    .multilineTextAlignment(.center)
                Text(String.ui("quickSwitch.empty.message", "PC 이름, 탭, 명령, 최근 전송·디버그 세션·QA 실행을 찾습니다."))
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Status.neutralText)
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
                .foregroundStyle(DS.Status.neutralText)
                .frame(width: 120, alignment: .leading)
            Text(verbatim: example)
                .font(DS.Font.caption.weight(.medium))
        }
    }
}

/// The keys the palette takes; with no rows, only esc.
struct QuickSwitchFooter: View {
    let hasRows: Bool

    var body: some View {
        HStack(spacing: DS.Space.l) {
            if hasRows {
                hint("↑↓", String.ui("quickSwitch.key.move", "이동"))
                hint("↩", String.ui("quickSwitch.key.open", "열기"))
            }
            hint("esc", String.ui("quickSwitch.key.close", "닫기"))
            Spacer(minLength: 0)
        }
        .padding(.horizontal, DS.Space.l)
        .frame(height: 36)
        .accessibilityElement(children: .combine)
    }

    private func hint(_ key: String, _ title: String) -> some View {
        HStack(spacing: DS.Space.xs) {
            Text(verbatim: key)
                .font(DS.Font.caption.monospacedDigit())
                .foregroundStyle(DS.Status.neutralText)
                .padding(.horizontal, DS.Space.xs)
                .frame(minHeight: 20)
                .background(DS.Color.neutral.opacity(0.12), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
            Text(title)
                .font(DS.Font.caption)
                .foregroundStyle(DS.Status.neutralText)
        }
    }
}
