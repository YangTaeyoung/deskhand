import AppKit
import DeskhandClient
import SwiftUI

extension EventLevel {
    /// The level as Windows Event Viewer names it in Korean.
    public var title: String {
        switch self {
        case .critical: .ui("logs.level.critical", "위험")
        case .error: .ui("logs.level.error", "오류")
        case .warning: .ui("logs.level.warning", "경고")
        case .information: .ui("logs.level.information", "정보")
        case .verbose: .ui("logs.level.verbose", "자세한 정보")
        }
    }

    public var symbol: String {
        switch self {
        case .critical: "exclamationmark.octagon.fill"
        case .error: "xmark.circle.fill"
        case .warning: "exclamationmark.triangle.fill"
        case .information: "info.circle"
        case .verbose: "text.alignleft"
        }
    }

    public var tint: Color {
        switch self {
        case .critical, .error: DS.Color.danger
        case .warning: DS.Color.warning
        case .information, .verbose: DS.Color.neutral
        }
    }

    /// Text in the level's hue, legible on rows and tints.
    var textColorNS: NSColor {
        switch self {
        case .critical, .error: DS.Status.dangerTextNS
        case .warning: DS.Status.warningTextNS
        case .information, .verbose: DS.Status.neutralTextNS
        }
    }
}

extension EventLogName {
    /// The log as Windows Event Viewer names it in Korean.
    public var title: String {
        switch self {
        case .system: .ui("logs.log.system", "시스템")
        case .application: .ui("logs.log.application", "응용 프로그램")
        }
    }
}

/// How an event message reads.
enum EventMessage {
    /// Three or more lines, at least half of them indented or `key: value`, as in stack traces and
    /// Windows Error Reporting summaries.
    static func isStructured(_ message: String) -> Bool {
        let lines = message.split(whereSeparator: \.isNewline).filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }
        guard lines.count >= 3 else { return false }
        let structured = lines.filter { line in
            if line.first == " " || line.first == "\t" { return true }
            guard let colon = line.firstIndex(of: ":") else { return false }
            let key = line[..<colon]
            let rest = line[line.index(after: colon)...]
            return !key.isEmpty && key.count <= 40 && (rest.isEmpty || rest.first == " ")
        }
        return structured.count * 2 >= lines.count
    }
}

extension EventLogEntry {
    /// The first non-empty line of the message.
    var preview: String {
        message.split(whereSeparator: \.isNewline).first.map(String.init)?.trimmingCharacters(in: .whitespaces) ?? ""
    }

    /// The event as plain text: header lines, then the message.
    var copyText: String {
        let level = eventLevel?.title ?? level
        let time = time.date.formatted(.iso8601)
        let id = String(eventID)
        let record = String(recordID)
        return "\(level)  \(time)\n\(provider)  ID \(id)  #\(record)\n\n\(message)"
    }
}

/// The events section: the filter bar (log, time range, levels, provider, search), the event
/// table with its status line and older pages, and the selected event's full message below.
struct EventLogPane: View {
    @Bindable var model: LogsModel
    let autosave: Bool
    @State private var detailHeight: CGFloat = 220
    @State private var dragStart: CGFloat?

    var body: some View {
        VStack(spacing: 0) {
            EventFilterBar(model: model)
            GeometryReader { proxy in
                let maxDetail = max(120, proxy.size.height - 160)
                let height = min(max(120, detailHeight), maxDetail)
                VStack(spacing: 0) {
                    eventList
                        .frame(maxHeight: .infinity)
                    if let event = model.selectedEvent {
                        divider(maxDetail: maxDetail, current: height)
                        EventDetailView(model: model, event: event)
                            .frame(height: height)
                    }
                }
            }
        }
    }

    @ViewBuilder
    private var eventList: some View {
        switch model.eventsPhase {
        case .idle, .loading:
            VStack(spacing: DS.Space.m) {
                ProgressView()
                Text(String.ui("logs.events.loading", "이벤트를 불러오는 중…"))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .accessibilityElement(children: .combine)
        case .failed(let error):
            ErrorBanner(message: String.ui("logs.events.failed", "이벤트 로그를 불러오지 못했습니다"),
                        fix: LogsModel.explanation(error), code: error.code) {
                Task { await model.reloadEvents() }
            }
            .frame(maxWidth: 520)
            .padding(DS.Space.xl)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .loaded:
            VStack(spacing: 0) {
                EventTableView(model: model, autosave: autosave)
                    .opacity(model.isReloadingEvents ? 0.55 : 1)
                    .overlay {
                        if model.isReloadingEvents {
                            ProgressView()
                                .controlSize(.small)
                                .padding(DS.Space.m)
                                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
                        } else if model.visibleEvents.isEmpty {
                            emptyEvents
                        }
                    }
                EventStatusBar(model: model)
            }
        }
    }

    private func divider(maxDetail: CGFloat, current: CGFloat) -> some View {
        Rectangle()
            .fill(DS.Color.separator)
            .frame(height: 1)
            .overlay {
                Color.clear
                    .frame(height: 9)
                    .contentShape(Rectangle())
                    .onHover { inside in
                        if inside { NSCursor.resizeUpDown.push() } else { NSCursor.pop() }
                    }
                    .gesture(DragGesture(minimumDistance: 1, coordinateSpace: .global)
                        .onChanged { value in
                            let start = dragStart ?? current
                            dragStart = start
                            detailHeight = min(max(120, start - value.translation.height), maxDetail)
                        }
                        .onEnded { _ in dragStart = nil })
            }
            .accessibilityHidden(true)
    }

    @ViewBuilder
    private var emptyEvents: some View {
        let searching = !model.search.trimmingCharacters(in: .whitespaces).isEmpty
        VStack(spacing: DS.Space.s) {
            Image(systemName: searching ? "magnifyingglass" : "checkmark.seal")
                .font(.system(size: 32, weight: .regular))
                .foregroundStyle(.tertiary)
                .accessibilityHidden(true)
            Text(searching ? String.ui("logs.events.searchNone", "일치하는 이벤트가 없습니다")
                           : String.ui("logs.events.none", "이 조건의 이벤트가 없습니다"))
                .font(DS.Font.headline)
            Text(searching ? String.ui("logs.events.searchHint", "메시지, 공급자 이름, 이벤트 ID로 찾습니다. 불러온 이벤트 안에서만 찾습니다.")
                           : String.ui("logs.events.noneHint", "기간을 넓히거나 수준과 공급자 필터를 바꿔 보세요."))
                .font(DS.Font.body)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 340)
                .fixedSize(horizontal: false, vertical: true)
            if !searching, model.filter.range != .lastWeek, !model.filter.range.isCustom {
                Button(String.ui("logs.events.widen", "최근 7일로 넓히기")) {
                    var filter = model.filter
                    filter.range = .lastWeek
                    Task { await model.setFilter(filter) }
                }
                .padding(.top, DS.Space.xs)
            }
        }
        .padding(DS.Space.l)
        .accessibilityElement(children: .contain)
    }
}

// MARK: Filters

/// Log, time range, level chips (⌥-click shows one level only), provider and search.
private struct EventFilterBar: View {
    @Bindable var model: LogsModel
    @State private var customOpen = false
    @FocusState private var searchFocused: Bool

    var body: some View {
        ViewThatFits(in: .horizontal) {
            bar(chips: .full)
            bar(chips: .icons)
        }
        .padding(.horizontal, DS.Space.m)
        .frame(height: 40)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
    }

    private func bar(chips style: LevelChip.Style) -> some View {
        HStack(spacing: DS.Space.s) {
            Picker(String.ui("logs.log", "로그"), selection: Binding(
                get: { model.filter.log },
                set: { log in update { $0.log = log; $0.provider = nil } })) {
                ForEach(EventLogName.allCases) { log in Text(log.title).tag(log) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            .help(String.ui("logs.log.help", "Windows 로그: 시스템 또는 응용 프로그램"))
            rangeMenu
            HStack(spacing: DS.Space.xs) {
                ForEach(EventLevel.allCases, id: \.self) { level in
                    LevelChip(level: level, isOn: model.filter.levels.contains(level), style: style) {
                        let solo = NSEvent.modifierFlags.contains(.option)
                        Task { await model.toggleLevel(level, solo: solo) }
                    }
                }
            }
            .fixedSize()
            providerMenu
            Spacer(minLength: DS.Space.xs)
            SearchField(text: $model.search, prompt: String.ui("logs.search.prompt", "메시지 검색"), focused: $searchFocused)
                .frame(minWidth: 120, idealWidth: 200, maxWidth: 220)
            Button("") { searchFocused = true }
                .keyboardShortcut("f", modifiers: .command)
                .opacity(0)
                .frame(width: 0, height: 0)
                .accessibilityHidden(true)
        }
    }

    private func update(_ change: (inout EventFilter) -> Void) {
        var filter = model.filter
        change(&filter)
        Task { await model.setFilter(filter) }
    }

    private var rangeMenu: some View {
        Menu {
            ForEach(EventTimeRange.presets, id: \.self) { range in
                Button {
                    update { $0.range = range }
                } label: {
                    if model.filter.range == range { Label(range.title, systemImage: "checkmark") } else { Text(range.title) }
                }
            }
            Divider()
            Button(String.ui("logs.range.customMenu", "사용자 지정…")) { customOpen = true }
        } label: {
            Label(model.filter.range.title, systemImage: "calendar")
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
        .help(String.ui("logs.range.help", "이벤트를 불러올 기간"))
        .popover(isPresented: $customOpen, arrowEdge: .bottom) {
            CustomRangeForm(range: model.filter.range) { range in
                customOpen = false
                update { $0.range = range }
            } cancel: {
                customOpen = false
            }
        }
    }

    private var providerMenu: some View {
        Menu {
            Button {
                update { $0.provider = nil }
            } label: {
                if model.filter.provider == nil {
                    Label(String.ui("logs.provider.all", "모든 공급자"), systemImage: "checkmark")
                } else {
                    Text(String.ui("logs.provider.all", "모든 공급자"))
                }
            }
            if !model.providers.isEmpty { Divider() }
            ForEach(model.providers, id: \.self) { provider in
                Button {
                    update { $0.provider = provider }
                } label: {
                    if model.filter.provider == provider { Label(provider, systemImage: "checkmark") } else { Text(provider) }
                }
            }
        } label: {
            Label(model.filter.provider ?? String.ui("logs.provider.all", "모든 공급자"), systemImage: "line.3.horizontal.decrease.circle")
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .menuStyle(.borderlessButton)
        .frame(maxWidth: 190)
        .fixedSize(horizontal: model.filter.provider == nil, vertical: false)
        .help(String.ui("logs.provider.help", "이벤트를 남긴 공급자로 거릅니다"))
    }
}

/// A level filter that is on (tinted) or off (plain), with its name or, where space is short,
/// its symbol only.
private struct LevelChip: View {
    enum Style {
        case full
        case icons
    }

    let level: EventLevel
    let isOn: Bool
    let style: Style
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 3) {
                Image(systemName: level.symbol)
                    .imageScale(.small)
                    .foregroundStyle(isOn ? AnyShapeStyle(level.tint) : AnyShapeStyle(.tertiary))
                if style == .full {
                    Text(level.title)
                        .foregroundStyle(isOn ? AnyShapeStyle(.primary) : AnyShapeStyle(.secondary))
                        .fixedSize()
                }
            }
            .font(DS.Font.caption.weight(.medium))
            .padding(.horizontal, DS.Space.s)
            .frame(height: 22)
            .background(background, in: Capsule())
            .overlay(Capsule().strokeBorder(isOn ? level.tint.opacity(0.35) : DS.Color.separator, lineWidth: 1))
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(String.ui("logs.level.chip.help", "\(level.title) 이벤트 보기 · ⌥-클릭하면 이 수준만 봅니다"))
        .accessibilityLabel(level.title)
        .accessibilityAddTraits(isOn ? [.isSelected] : [])
    }

    private var background: AnyShapeStyle {
        if isOn { return AnyShapeStyle(level.tint.opacity(DS.Status.badgeOpacity)) }
        return hovering ? AnyShapeStyle(DS.Color.neutral.opacity(0.12)) : AnyShapeStyle(.clear)
    }
}

/// Start and end of a custom range, no later than now.
private struct CustomRangeForm: View {
    let apply: (EventTimeRange) -> Void
    let cancel: () -> Void
    @State private var start: Date
    @State private var end: Date

    init(range: EventTimeRange, apply: @escaping (EventTimeRange) -> Void, cancel: @escaping () -> Void) {
        let bounds = range.bounds(now: .now)
        _start = State(initialValue: bounds.since)
        _end = State(initialValue: bounds.until ?? .now)
        self.apply = apply
        self.cancel = cancel
    }

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            Text(String.ui("logs.range.customTitle", "기간 지정"))
                .font(DS.Font.headline)
            Grid(alignment: .leading, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.s) {
                GridRow {
                    Text(String.ui("logs.range.start", "시작"))
                        .foregroundStyle(.secondary)
                    DatePicker("", selection: $start, in: ...end, displayedComponents: [.date, .hourAndMinute])
                        .labelsHidden()
                }
                GridRow {
                    Text(String.ui("logs.range.end", "끝"))
                        .foregroundStyle(.secondary)
                    DatePicker("", selection: $end, in: start...Date.now, displayedComponents: [.date, .hourAndMinute])
                        .labelsHidden()
                }
            }
            HStack {
                Spacer()
                Button(String.ui("action.cancel", "취소"), action: cancel)
                    .keyboardShortcut(.cancelAction)
                Button(String.ui("logs.range.apply", "적용")) { apply(.custom(start: start, end: end)) }
                    .keyboardShortcut(.defaultAction)
                    .disabled(start >= end)
            }
        }
        .padding(DS.Space.l)
        .frame(width: 320)
    }
}

// MARK: Table

/// The event table: level, time, provider, event ID and the first line of the message.
struct EventTableView: View {
    let model: LogsModel
    var autosave = true

    static let columns: [DataTableColumn] = [
        DataTableColumn(id: EventColumn.level.rawValue, title: EventColumn.level.title, width: 92, minWidth: 72, firstSortAscending: true),
        DataTableColumn(id: EventColumn.time.rawValue, title: EventColumn.time.title, width: 148, minWidth: 110, firstSortAscending: false),
        DataTableColumn(id: EventColumn.provider.rawValue, title: EventColumn.provider.title, width: 190, minWidth: 90, firstSortAscending: true),
        DataTableColumn(id: EventColumn.eventID.rawValue, title: EventColumn.eventID.title, width: 66, minWidth: 52, alignment: .right,
                        firstSortAscending: true, hideBelow: 640),
        DataTableColumn(id: EventColumn.message.rawValue, title: EventColumn.message.title, width: 320, minWidth: 140, fills: true),
    ]

    var body: some View {
        DataTable(
            columns: Self.columns,
            rows: model.visibleEvents,
            selection: model.selectedRecordID,
            sort: DataTableSort(column: model.sort.column.rawValue, ascending: model.sort.ascending),
            autosaveName: autosave ? "logs.events.table" : nil,
            accessibilityLabel: String.ui("logs.a11y.table", "이벤트 로그"),
            cell: { event, column, table in Self.cell(event, column: column, table: table) },
            onSelect: { [model] id in model.selectedRecordID = id },
            onSort: { [model] sort in
                guard let column = EventColumn(rawValue: sort.column) else { return }
                model.sort = EventSort(column: column, ascending: sort.ascending)
            },
            menu: { [model] event in Self.menu(event, model: model) },
            onKey: { key, event in
                guard key == .copy else { return false }
                ProcessMenu.put(event.copyText)
                return true
            })
    }

    @MainActor
    static func menu(_ event: EventLogEntry, model: LogsModel) -> [DataTableMenuEntry] {
        [
            DataTableMenuEntry(title: .ui("logs.copyEvent", "이벤트 복사"), symbol: "doc.on.doc", key: "c") {
                ProcessMenu.put(event.copyText)
            },
            DataTableMenuEntry(title: .ui("logs.copyMessage", "메시지만 복사"), symbol: "text.quote") {
                ProcessMenu.put(event.message)
            },
            .separator,
            DataTableMenuEntry(title: .ui("logs.onlyProvider", "이 공급자만 보기"), symbol: "line.3.horizontal.decrease.circle",
                               isEnabled: model.filter.provider != event.provider) {
                var filter = model.filter
                filter.provider = event.provider
                Task { await model.setFilter(filter) }
            },
        ]
    }

    @MainActor
    static func cell(_ event: EventLogEntry, column: String, table: NSTableView) -> NSView {
        switch EventColumn(rawValue: column) ?? .message {
        case .level:
            let cell = table.makeView(withIdentifier: EventLevelCell.identifier, owner: nil) as? EventLevelCell ?? EventLevelCell()
            cell.configure(event)
            return cell
        case .time:
            let cell = DataTextCell.make(table, "event.time", monospacedDigits: true)
            cell.configure(DS.Format.dateTime(event.time.date, seconds: true), style: .secondary,
                           help: DS.Format.fullDateTime(event.time.date))
            return cell
        case .provider:
            let cell = DataTextCell.make(table, "event.provider", truncation: .byTruncatingMiddle)
            cell.configure(event.provider, style: .secondary, help: event.provider)
            return cell
        case .eventID:
            let cell = DataTextCell.make(table, "event.id", alignment: .right, monospacedDigits: true)
            cell.configure(String(event.eventID), style: .secondary)
            return cell
        case .message:
            let cell = DataTextCell.make(table, "event.message")
            let level = event.eventLevel
            cell.configure(event.preview, style: .primary, help: String(event.message.prefix(600)),
                           weight: level == .critical ? .semibold : .regular)
            return cell
        }
    }
}

/// Level cell: the colored level symbol and its name.
final class EventLevelCell: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("event.level")

    private let icon = NSImageView()
    private let label = NSTextField(labelWithString: "")
    private var tint: NSColor = .secondaryLabelColor
    private var textTint: NSColor = .labelColor
    private var critical = false

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        icon.translatesAutoresizingMaskIntoConstraints = false
        icon.symbolConfiguration = NSImage.SymbolConfiguration(pointSize: 12, weight: .regular)
        label.translatesAutoresizingMaskIntoConstraints = false
        label.lineBreakMode = .byTruncatingTail
        label.usesSingleLineMode = true
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        addSubview(icon)
        addSubview(label)
        imageView = icon
        textField = label
        NSLayoutConstraint.activate([
            icon.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            icon.centerYAnchor.constraint(equalTo: centerYAnchor),
            icon.widthAnchor.constraint(equalToConstant: 16),
            label.leadingAnchor.constraint(equalTo: icon.trailingAnchor, constant: 5),
            label.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -2),
            label.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    required init?(coder: NSCoder) { nil }

    func configure(_ event: EventLogEntry) {
        let level = event.eventLevel
        icon.image = NSImage(systemSymbolName: level?.symbol ?? "questionmark.circle", accessibilityDescription: nil)
        tint = level.map { NSColor($0.tint) } ?? .secondaryLabelColor
        textTint = level?.textColorNS ?? .labelColor
        critical = level == .critical
        label.stringValue = level?.title ?? event.level
        label.font = .systemFont(ofSize: NSFont.systemFontSize, weight: critical ? .semibold : .regular)
        recolor()
    }

    override var backgroundStyle: NSView.BackgroundStyle {
        didSet { recolor() }
    }

    private func recolor() {
        let emphasized = backgroundStyle == .emphasized
        icon.contentTintColor = emphasized ? .alternateSelectedControlTextColor : tint
        label.textColor = emphasized ? .alternateSelectedControlTextColor : (critical ? textTint : .labelColor)
    }
}

// MARK: Status

/// Counts, the range, when the list loaded, and whether older events remain.
private struct EventStatusBar: View {
    let model: LogsModel

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Text(summary)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: DS.Space.s)
            if model.isLoadingMore {
                ProgressView().controlSize(.mini)
                Text(String.ui("logs.more.loading", "이전 이벤트를 불러오는 중…"))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            } else if let error = model.loadMoreError {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(DS.Color.warning)
                    .accessibilityHidden(true)
                Text(String.ui("logs.more.failed", "이전 이벤트를 불러오지 못했습니다"))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .help(error.sentences)
                Button(String.ui("action.retry", "다시 시도")) { Task { await model.loadMoreEvents() } }
                    .buttonStyle(.link)
                    .tint(DS.Color.accent)
            } else if model.eventsTruncated {
                Text(String.ui("logs.more.available", "이 기간에 이벤트가 더 있습니다"))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                Button(String.ui("logs.more", "더 보기")) { Task { await model.loadMoreEvents() } }
                    .buttonStyle(.link)
                    .tint(DS.Color.accent)
                    .help(String.ui("logs.more.help", "불러온 것보다 오래된 이벤트를 200개 더 불러옵니다"))
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

    private var summary: String {
        let count = DS.Format.count(model.events.count)
        let range = model.filter.range.title
        if !model.search.trimmingCharacters(in: .whitespaces).isEmpty {
            let matches = DS.Format.count(model.visibleEvents.count)
            return .ui("logs.status.matches", "검색 결과 \(matches)개 · 불러온 이벤트 \(count)개")
        }
        let log = model.filter.log.title
        if model.selectedEvent == nil, !model.visibleEvents.isEmpty {
            return .ui("logs.status.summaryHint", "\(log) 로그 · \(range) · 이벤트 \(count)개 · 행을 선택하면 전체 메시지를 볼 수 있습니다")
        }
        return .ui("logs.status.summary", "\(log) 로그 · \(range) · 이벤트 \(count)개")
    }
}

// MARK: Detail

/// The selected event: level, provider, ID, time and record, then the whole message, with copy
/// and a provider filter.
private struct EventDetailView: View {
    let model: LogsModel
    let event: EventLogEntry

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: DS.Space.m) {
                header(event)
                Text(event.message.isEmpty ? String.ui("logs.detail.noMessage", "메시지가 없는 이벤트입니다") : event.message)
                    .font(EventMessage.isStructured(event.message) ? DS.Font.mono : DS.Font.body)
                    .lineSpacing(2)
                    .foregroundStyle(event.message.isEmpty ? .secondary : .primary)
                    .textSelection(.enabled)
                    .frame(maxWidth: 680, alignment: .leading)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(DS.Space.l)
        }
        .background(DS.Color.surfaceRaised.opacity(0.5))
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("logs.a11y.detail", "선택한 이벤트"))
    }

    private func header(_ event: EventLogEntry) -> some View {
        let level = event.eventLevel
        let id = String(event.eventID)
        let record = String(event.recordID)
        let time = DS.Format.fullDateTime(event.time.date)
        let log = model.filter.log.title
        return HStack(alignment: .top, spacing: DS.Space.m) {
            Image(systemName: level?.symbol ?? "questionmark.circle")
                .font(.system(size: 20))
                .foregroundStyle(level?.tint ?? DS.Color.neutral)
                .frame(width: 24)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: DS.Space.s) {
                    Text(event.provider)
                        .font(DS.Font.headline)
                        .lineLimit(1)
                        .textSelection(.enabled)
                    Text(level?.title ?? event.level)
                        .font(DS.Font.caption.weight(.semibold))
                        .foregroundStyle(level?.tint ?? DS.Color.neutral)
                }
                Text(String.ui("logs.detail.facts", "\(time) · \(log) 로그 · 이벤트 ID \(id) · 레코드 \(record)"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
            Spacer(minLength: DS.Space.s)
            HStack(spacing: DS.Space.s) {
                Button {
                    var filter = model.filter
                    filter.provider = event.provider
                    Task { await model.setFilter(filter) }
                } label: {
                    Label(String.ui("logs.onlyProvider", "이 공급자만 보기"), systemImage: "line.3.horizontal.decrease.circle")
                }
                .disabled(model.filter.provider == event.provider)
                Button {
                    ProcessMenu.put(event.copyText)
                } label: {
                    Label(String.ui("logs.copy", "복사"), systemImage: "doc.on.doc")
                }
                .help(String.ui("logs.copy.help", "수준, 시간, 공급자, ID와 전체 메시지를 복사합니다"))
            }
            .controlSize(.small)
        }
    }
}
