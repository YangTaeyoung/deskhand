import AppKit
import DeskhandClient
import SwiftUI
import struct DeskhandClient.Host

/// One pane of the files tab: the header with the computer, search and options, the path bar,
/// the file table or the folder's loading, empty and error states, the status line, and the
/// banner that says what a drop will do.
struct FilePane: View {
    let model: FilesModel
    let side: FileSide
    let host: Host
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var browser: FileBrowser { model.browser(side) }
    private var preferences: FilesPreferences { model.preferences }
    private var isActive: Bool { model.activeSide == side }
    private var availability: RemoteFilesAvailability { side == .local ? .ready : RemoteFilesAvailability(host: host) }

    var body: some View {
        VStack(spacing: 0) {
            FilePaneHeader(model: model, side: side, host: host, availability: availability)
            if availability == .ready {
                PathBar(model: model, side: side, host: host)
                if let error = model.paneErrors[side] {
                    PaneErrorStrip(notice: error) { model.dismissError(side) }
                }
                content
                    .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
                FilePaneStatus(model: model, side: side)
            } else {
                RemoteUnavailableView(host: host, availability: availability)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .background(DS.Color.surface)
        .overlay(alignment: .bottom) {
            if let hover = model.dropHover[side] {
                DropBanner(text: dropText(hover), move: hover.move, moveDetail: moveDetail)
                    .padding(.bottom, 36)
                    .transition(reduceMotion ? .opacity : .move(edge: .bottom).combined(with: .opacity))
                    .allowsHitTesting(false)
            }
        }
        .animation(reduceMotion ? nil : .snappy(duration: 0.18), value: model.dropHover[side])
        .simultaneousGesture(TapGesture().onEnded { model.activeSide = side })
    }

    @ViewBuilder
    private var content: some View {
        switch browser.phase {
        case .idle, .loading:
            VStack(spacing: DS.Space.m) {
                ProgressView()
                    .controlSize(.regular)
                Text(String.ui("files.loading", "폴더를 여는 중…"))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                Text(browser.path)
                    .font(DS.Font.mono)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .frame(maxWidth: 360)
            }
            .padding(DS.Space.l)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .accessibilityElement(children: .combine)
        case .failed(let error):
            FolderErrorView(side: side, error: error, canGoUp: browser.parentPath != nil) {
                Task { await model.reload(side) }
            } goUp: {
                Task { await model.goUp(side) }
            }
        case .loaded:
            table
                .overlay {
                    if browser.rows.isEmpty {
                        emptyFolder
                            .allowsHitTesting(false)
                    }
                }
        }
    }

    private var table: some View {
        FileTableView(
            side: side,
            rows: browser.rows,
            selection: browser.selection,
            sort: browser.sort,
            isActive: isActive,
            renameRequest: browser.renameRequest,
            widths: Dictionary(uniqueKeysWithValues: FileColumn.allCases.compactMap { column in
                preferences.columnWidth(column, side: side).map { (column, $0) }
            }),
            hiddenColumns: Set(FileColumn.allCases.filter { preferences.isColumnHidden($0, side: side) }),
            accessibilityLabel: side == .local
                ? String.ui("files.a11y.localTable", "이 Mac의 파일 목록")
                : String.ui("files.a11y.remoteTable", "\(host.name)의 파일 목록"),
            handlers: handlers)
    }

    private var handlers: FileTableHandlers {
        var h = FileTableHandlers()
        let model = self.model
        let side = self.side
        let browser = self.browser
        h.activate = { if model.activeSide != side { model.activeSide = side } }
        h.open = { item in Task { await model.activate(item, side: side) } }
        h.selectionChanged = { browser.selection = $0 }
        h.sortChanged = { model.setSort($0, for: side) }
        h.menu = { model.menu(for: $0, side: side) }
        h.command = { command, items in FilePane.perform(command, items: items, model: model, side: side) }
        h.nearEnd = {
            guard browser.hasMore, !browser.isLoadingMore, browser.loadMoreError == nil else { return }
            Task { await browser.loadMore() }
        }
        h.renameStarted = { browser.renameRequest = nil }
        h.commitRename = { item, name in Task { await model.rename(item, to: name) } }
        h.dragWriter = { item in
            guard !item.isPreviewOnly else { return nil }
            switch side {
            case .local:
                return URL(fileURLWithPath: item.path) as NSURL
            case .remote:
                let pasteboardItem = NSPasteboardItem()
                guard let data = try? JSONEncoder().encode(RemoteDragItem(hostID: model.hostID, item: item)) else { return nil }
                pasteboardItem.setData(data, forType: .deskhandRemoteItem)
                return pasteboardItem
            }
        }
        h.canDrop = { payload, _ in model.canAccept(payload, on: side) }
        h.dropHover = { hover in model.dropHover[side] = hover }
        h.drop = { payload, folder, move in Task { await model.handleDrop(payload, on: side, into: folder, move: move) } }
        h.columnWidthChanged = { column, width in model.preferences.setColumnWidth(width, column, side: side) }
        h.columnHiddenChanged = { column, hidden in model.preferences.setColumn(column, hidden: hidden, side: side) }
        return h
    }

    /// Runs a keyboard command of the table on `items`.
    static func perform(_ command: FileTableCommand, items: [FileItem], model: FilesModel, side: FileSide) -> Bool {
        let single = items.count == 1 ? items.first : nil
        switch command {
        case .returnKey, .open:
            guard let folder = single, folder.isFolder else { return false }
            Task { await model.activate(folder, side: side) }
        case .up:
            Task { await model.goUp(side) }
        case .back:
            Task { await model.goBack(side) }
        case .forward:
            Task { await model.goForward(side) }
        case .trash:
            guard side == .remote, !items.isEmpty else { return false }
            Task { await model.trash(items) }
        case .deletePermanently:
            guard side == .remote, !items.isEmpty else { return false }
            model.requestPermanentDelete(items)
        case .info:
            guard let item = single else { return false }
            model.showInfo(item, side: side)
        case .copyPath:
            guard !items.isEmpty else { return false }
            model.copyPaths(items)
        case .newFolder:
            guard side == .remote else { return false }
            Task { await model.newFolder() }
        case .refresh:
            Task { await model.reload(side) }
        case .search:
            model.focusSearch(side)
        }
        return true
    }

    @ViewBuilder
    private var emptyFolder: some View {
        let searching = !browser.query.trimmingCharacters(in: .whitespaces).isEmpty
        VStack(spacing: DS.Space.s) {
            Image(systemName: searching ? "magnifyingglass" : "folder")
                .font(.system(size: 32, weight: .regular))
                .foregroundStyle(.tertiary)
                .accessibilityHidden(true)
            Text(searching ? String.ui("files.search.none", "일치하는 항목이 없습니다") : String.ui("files.empty.title", "빈 폴더"))
                .font(DS.Font.headline)
            Text(emptyMessage(searching: searching))
                .font(DS.Font.body)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 300)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(DS.Space.l)
        .accessibilityElement(children: .combine)
    }

    private func emptyMessage(searching: Bool) -> String {
        if searching {
            let query = browser.query
            return .ui("files.search.noneMessage", "이 폴더에 ‘\(query)’ 검색어가 들어간 이름이 없습니다.")
        }
        if side == .remote {
            return .ui("files.empty.remote", "이 Mac 패널이나 Finder에서 파일을 끌어다 놓으면 여기에 보냅니다.")
        }
        return .ui("files.empty.local", "원격 패널에서 파일을 끌어다 놓으면 이 폴더로 받습니다.")
    }

    // MARK: Drop banner

    private var moveDetail: String {
        let source = model.sideName(side.other)
        return .ui("files.drop.moveDetail", "확인이 끝난 뒤에만 \(source)의 원본을 지웁니다")
    }

    private func dropText(_ hover: FileDropHover) -> String {
        let folderName = hover.folder?.name ?? FilePath.lastComponent(browser.path, style: browser.style)
        let target = model.sideName(side)
        let count = DS.Format.count(hover.count)
        switch (side, hover.move) {
        case (.remote, false): return .ui("files.drop.send", "\(target)의 ‘\(folderName)’ 폴더에 \(count)개 보내기")
        case (.remote, true): return .ui("files.drop.sendMove", "\(target)의 ‘\(folderName)’ 폴더로 \(count)개 이동")
        case (.local, false): return .ui("files.drop.receive", "이 Mac의 ‘\(folderName)’ 폴더에 \(count)개 받기")
        case (.local, true): return .ui("files.drop.receiveMove", "이 Mac의 ‘\(folderName)’ 폴더로 \(count)개 이동")
        }
    }
}

/// Pane title with the computer, its connection, the transfer button toward the other pane,
/// search and the pane options.
private struct FilePaneHeader: View {
    let model: FilesModel
    let side: FileSide
    let host: Host
    let availability: RemoteFilesAvailability
    @FocusState private var searchFocused: Bool

    private var browser: FileBrowser { model.browser(side) }
    private var isActive: Bool { model.activeSide == side }

    var body: some View {
        @Bindable var browser = browser
        HStack(spacing: DS.Space.s) {
            Image(systemName: side == .local ? "laptopcomputer" : "desktopcomputer")
                .foregroundStyle(isActive ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary))
                .accessibilityHidden(true)
            Text(model.sideName(side))
                .font(DS.Font.headline)
                .lineLimit(1)
                .truncationMode(.tail)
                .layoutPriority(1)
            if side == .remote {
                StatusDot(state: host.connection)
            }
            Spacer(minLength: DS.Space.s)
            if availability == .ready {
                transferButton
                SearchField(text: $browser.query, prompt: String.ui("files.search.prompt", "이름 검색"), focused: $searchFocused)
                    .frame(minWidth: 96, idealWidth: 168, maxWidth: 200)
                optionsMenu
            }
        }
        .padding(.horizontal, DS.Space.m)
        .frame(height: 40)
        .background(isActive ? DS.Color.accent.opacity(0.06) : .clear)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .onChange(of: model.searchFocusRequest[side]) { _, _ in searchFocused = true }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(side == .local ? String.ui("files.a11y.localPane", "이 Mac 패널") : String.ui("files.a11y.remotePane", "원격 PC 패널"))
    }

    @ViewBuilder
    private var transferButton: some View {
        let selection = browser.selectedItems
        let otherReady = model.browser(side.other).phase == .loaded
        Button {
            Task {
                if side == .local {
                    await model.sendSelection(move: NSEvent.modifierFlags.contains(.command))
                } else {
                    await model.receiveSelection(move: NSEvent.modifierFlags.contains(.command))
                }
            }
        } label: {
            Label(side == .local ? String.ui("files.send", "보내기") : String.ui("files.receive", "받기"),
                  systemImage: side == .local ? "arrow.right.circle" : "arrow.left.circle")
                .labelStyle(.iconOnly)
        }
        .buttonStyle(.borderless)
        .disabled(selection.isEmpty || !otherReady)
        .help(side == .local
              ? String.ui("files.send.help", "선택한 항목을 원격 PC의 열린 폴더로 보냅니다. ⌘를 누른 채 누르면 확인한 뒤 이동합니다.")
              : String.ui("files.receive.help", "선택한 항목을 이 Mac의 열린 폴더로 받습니다. ⌘를 누른 채 누르면 확인한 뒤 이동합니다."))
        .accessibilityLabel(side == .local ? String.ui("files.send.a11y", "선택한 항목을 원격 PC로 보내기")
                                           : String.ui("files.receive.a11y", "선택한 항목을 이 Mac으로 받기"))
    }

    private var optionsMenu: some View {
        Menu {
            Toggle(String.ui("files.showHidden", "숨김 항목 보기"), isOn: Binding(
                get: { browser.showsHidden },
                set: { model.setShowsHidden($0, for: side) }))
            Divider()
            if side == .remote {
                Button(String.ui("files.menu.newFolder", "새 폴더")) { Task { await model.newFolder() } }
                    .disabled(browser.phase != .loaded)
            } else {
                Button(String.ui("files.menu.showInFinder", "Finder에서 보기")) {
                    NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: browser.path)])
                }
            }
            Button(String.ui("files.menu.refresh", "새로 고침")) { Task { await model.reload(side) } }
        } label: {
            Image(systemName: "ellipsis.circle")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help(String.ui("files.options", "보기 옵션"))
        .accessibilityLabel(String.ui("files.options", "보기 옵션"))
    }
}

/// Rounded search field with a clear button.
struct SearchField: View {
    @Binding var text: String
    let prompt: String
    var focused: FocusState<Bool>.Binding

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Image(systemName: "magnifyingglass")
                .foregroundStyle(.secondary)
                .imageScale(.small)
                .accessibilityHidden(true)
            TextField(prompt, text: $text)
                .textFieldStyle(.plain)
                .focused(focused)
                .accessibilityLabel(prompt)
                .onKeyPress(.escape) {
                    guard !text.isEmpty else { return .ignored }
                    text = ""
                    return .handled
                }
            if !text.isEmpty {
                Button {
                    text = ""
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(String.ui("files.search.clear", "검색어 지우기"))
            }
        }
        .font(DS.Font.body)
        .padding(.horizontal, DS.Space.s)
        .frame(height: 24)
        .background(DS.Color.neutral.opacity(0.12), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .accessibilityElement(children: .contain)
    }
}

/// Back, forward, up, places and the breadcrumb of the open folder; a field to type a path.
private struct PathBar: View {
    let model: FilesModel
    let side: FileSide
    let host: Host
    @State private var typing = false
    @State private var typed = ""

    private var browser: FileBrowser { model.browser(side) }

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Group {
                iconButton("chevron.left", String.ui("files.back", "뒤로 (⌘[)"), disabled: browser.backStack.isEmpty) {
                    Task { await model.goBack(side) }
                }
                iconButton("chevron.right", String.ui("files.forward", "앞으로 (⌘])"), disabled: browser.forwardStack.isEmpty) {
                    Task { await model.goForward(side) }
                }
                iconButton("arrow.up", String.ui("files.up", "상위 폴더 (⌘↑)"), disabled: browser.parentPath == nil) {
                    Task { await model.goUp(side) }
                }
            }
            placesMenu
            crumbs
            iconButton("arrow.right.circle", String.ui("files.goTo", "경로로 이동"), disabled: false) {
                typed = browser.path
                typing = true
            }
            .popover(isPresented: $typing, arrowEdge: .bottom) { goToField }
        }
        .padding(.horizontal, DS.Space.s)
        .frame(height: 32)
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
    }

    private func iconButton(_ symbol: String, _ label: String, disabled: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .imageScale(.medium)
                .frame(width: 24, height: 24)
                .contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .disabled(disabled)
        .help(label)
        .accessibilityLabel(label)
    }

    private var placesMenu: some View {
        let places = side == .local ? LocalFileSource.places : RemoteFileSource.places(for: host)
        let roots = side == .remote ? model.preferences.workRoots(for: host.hostID) : []
        return Menu {
            ForEach(places) { place in
                Button(place.title) { Task { await model.open(side, path: place.path) } }
            }
            if !roots.isEmpty {
                Section(String.ui("files.places.workRoots", "작업 루트")) {
                    ForEach(roots, id: \.self) { root in
                        Button(root) { Task { await model.open(side, path: root) } }
                    }
                }
            }
        } label: {
            Image(systemName: side == .local ? "house" : "externaldrive")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help(String.ui("files.places", "자주 쓰는 위치"))
        .accessibilityLabel(String.ui("files.places", "자주 쓰는 위치"))
    }

    private var crumbs: some View {
        let crumbs = browser.crumbs
        return ScrollViewReader { proxy in
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 2) {
                    ForEach(Array(crumbs.enumerated()), id: \.element.id) { index, crumb in
                        if index > 0 {
                            Image(systemName: "chevron.right")
                                .font(.system(size: 9, weight: .semibold))
                                .foregroundStyle(.tertiary)
                                .accessibilityHidden(true)
                        }
                        CrumbButton(crumb: crumb, isLast: index == crumbs.count - 1) {
                            Task { await model.open(side, path: crumb.path) }
                        }
                        .id(crumb.id)
                    }
                }
                .padding(.horizontal, DS.Space.m)
            }
            .mask {
                HStack(spacing: 0) {
                    LinearGradient(colors: [.clear, .black], startPoint: .leading, endPoint: .trailing)
                        .frame(width: DS.Space.m)
                    Color.black
                    LinearGradient(colors: [.black, .clear], startPoint: .leading, endPoint: .trailing)
                        .frame(width: DS.Space.m)
                }
            }
            .onAppear { proxy.scrollTo(crumbs.last?.id, anchor: .trailing) }
            .onChange(of: browser.path) { _, _ in proxy.scrollTo(browser.crumbs.last?.id, anchor: .trailing) }
        }
        .frame(maxWidth: .infinity)
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("files.a11y.path", "경로: \(browser.path)"))
    }

    private var goToField: some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            Text(String.ui("files.goTo.title", "폴더로 이동"))
                .font(DS.Font.headline)
            TextField(String.ui("files.goTo.prompt", "경로"), text: $typed)
                .font(DS.Font.mono)
                .textFieldStyle(.roundedBorder)
                .frame(width: 360)
                .onSubmit(go)
            HStack {
                Spacer()
                Button(String.ui("action.cancel", "취소")) { typing = false }
                    .keyboardShortcut(.cancelAction)
                Button(String.ui("files.goTo.go", "이동"), action: go)
                    .keyboardShortcut(.defaultAction)
                    .disabled(typed.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(DS.Space.l)
    }

    private func go() {
        let path = typed.trimmingCharacters(in: .whitespaces)
        guard !path.isEmpty else { return }
        typing = false
        let expanded = side == .local ? (path as NSString).expandingTildeInPath : path
        Task { await model.open(side, path: expanded) }
    }
}

/// One crumb: the folder name truncated in the middle, the full path as tooltip.
private struct CrumbButton: View {
    let crumb: PathCrumb
    let isLast: Bool
    let action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            Text(crumb.title)
                .font(isLast ? DS.Font.body.weight(.semibold) : DS.Font.body)
                .foregroundStyle(isLast ? AnyShapeStyle(.primary) : AnyShapeStyle(.secondary))
                .lineLimit(1)
                .truncationMode(.middle)
                .frame(maxWidth: 180)
                .fixedSize(horizontal: true, vertical: false)
                .padding(.horizontal, 6)
                .frame(height: 22)
                .background(hovering ? DS.Color.neutral.opacity(0.14) : .clear,
                            in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(crumb.path)
        .accessibilityLabel(crumb.title)
        .accessibilityHint(String.ui("files.a11y.crumbHint", "이 폴더를 엽니다"))
    }
}

/// Item counts, the selection size, paging progress and actions in flight.
private struct FilePaneStatus: View {
    let model: FilesModel
    let side: FileSide

    private var browser: FileBrowser { model.browser(side) }

    var body: some View {
        HStack(spacing: DS.Space.s) {
            Text(summary)
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer(minLength: DS.Space.s)
            if side == .remote, let busy = model.busyMessage {
                ProgressView().controlSize(.mini)
                Text(busy).lineLimit(1)
            } else if let error = browser.loadMoreError {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(DS.Color.warning)
                    .accessibilityHidden(true)
                Text(String.ui("files.page.failed", "나머지를 불러오지 못했습니다"))
                    .lineLimit(1)
                    .help(error.detail)
                Button(String.ui("action.retry", "다시 시도")) { Task { await browser.retryLoadMore() } }
                    .buttonStyle(.link)
            } else if browser.isLoadingMore || browser.isRefreshing {
                ProgressView().controlSize(.mini)
                Text(paging ?? String.ui("files.refreshing", "새로 고치는 중…")).lineLimit(1)
            } else if let paging {
                Text(paging).lineLimit(1)
            } else if side == .local, browser.items.contains(where: \.isPreviewOnly) {
                Text(String.ui("files.preview.note.short", "미리보기에서는 이 Mac에 쓰지 않습니다"))
                    .foregroundStyle(DS.Status.warningText)
                    .lineLimit(1)
                    .help(String.ui("files.preview.note", "미리보기에서는 이 Mac에 쓰지 않습니다. 받은 항목은 앱을 닫으면 사라집니다."))
            } else if browser.truncated {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(DS.Color.warning)
                    .accessibilityHidden(true)
                Text(String.ui("files.truncated", "에이전트가 일부 항목만 보냈습니다"))
                    .lineLimit(1)
            }
        }
        .font(DS.Font.caption)
        .foregroundStyle(.secondary)
        .padding(.horizontal, DS.Space.m)
        .frame(height: 26)
        .overlay(alignment: .top) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .accessibilityElement(children: .combine)
    }

    private var summary: String {
        guard browser.phase == .loaded else { return " " }
        let selected = browser.selectedItems
        if !selected.isEmpty {
            let count = DS.Format.count(selected.count)
            let bytes = selected.compactMap(\.size).reduce(0, +)
            if bytes > 0 {
                let size = DS.Format.bytes(bytes)
                return .ui("files.status.selectedSize", "\(count)개 선택됨 · \(size)")
            }
            return .ui("files.status.selected", "\(count)개 선택됨")
        }
        if !browser.query.trimmingCharacters(in: .whitespaces).isEmpty {
            let count = DS.Format.count(browser.matchCount)
            return .ui("files.status.matches", "검색 결과 \(count)개")
        }
        let total = DS.Format.count(browser.total ?? browser.items.count)
        return .ui("files.status.items", "항목 \(total)개")
    }

    /// `3,000개 중 500개 표시` while rows or pages remain.
    private var paging: String? {
        guard browser.hasMore else { return nil }
        let shown = DS.Format.count(browser.rows.count)
        let total = DS.Format.count(browser.total ?? browser.items.count)
        return .ui("files.status.paging", "\(total)개 중 \(shown)개 표시 · 스크롤하면 더 불러옵니다")
    }
}

/// A failed action of the pane: the message, the remedy chosen by the error code, the code,
/// and a close button.
private struct PaneErrorStrip: View {
    let notice: FilesNotice
    let dismiss: () -> Void

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Image(systemName: "exclamationmark.octagon.fill")
                .foregroundStyle(DS.Color.danger)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(notice.message)
                    .font(DS.Font.body.weight(.semibold))
                if let detail = notice.detail {
                    Text(detail)
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                }
                if let code = notice.code {
                    ErrorCodeLine(code: code)
                }
            }
            .fixedSize(horizontal: false, vertical: true)
            .textSelection(.enabled)
            Spacer(minLength: DS.Space.s)
            Button(action: dismiss) {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundStyle(.secondary)
                    .frame(width: 18, height: 18)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String.ui("action.close", "닫기"))
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(DS.Color.danger.opacity(0.08))
        .overlay(alignment: .bottom) {
            Rectangle().fill(DS.Color.danger.opacity(0.24)).frame(height: 1)
        }
        .accessibilityElement(children: .contain)
        .onAppear { AccessibilityNotification.Announcement(notice.message).post() }
    }
}

/// What a drop does: a capsule with the destination, and for a move a second line saying the
/// source is deleted only after verification.
private struct DropBanner: View {
    let text: String
    let move: Bool
    let moveDetail: String

    var body: some View {
        HStack(spacing: DS.Space.s) {
            if move {
                Text(String.ui("files.drop.moveBadge", "이동"))
                    .font(DS.Font.caption.weight(.bold))
                    .foregroundStyle(DS.Color.onFill)
                    .padding(.horizontal, DS.Space.s)
                    .padding(.vertical, 2)
                    .background(DS.Color.warning, in: Capsule())
            } else {
                Image(systemName: "plus.circle.fill")
                    .foregroundStyle(DS.Color.accent)
                    .accessibilityHidden(true)
            }
            VStack(alignment: .leading, spacing: 2) {
                Text(text)
                    .font(DS.Font.body.weight(.semibold))
                    .lineLimit(1)
                    .truncationMode(.middle)
                if move {
                    Text(moveDetail)
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(move ? DS.Color.warning.opacity(0.5) : DS.Color.accent.opacity(0.4), lineWidth: 1)
        )
        .shadow(color: .black.opacity(0.12), radius: 8, y: 2)
        .padding(.horizontal, DS.Space.l)
        .accessibilityElement(children: .combine)
    }
}

/// A folder that could not be listed: what failed, how to fix it, and retry or go up.
private struct FolderErrorView: View {
    let side: FileSide
    let error: OperationError
    let canGoUp: Bool
    let retry: () -> Void
    let goUp: () -> Void

    private var fix: String? {
        if error.code == LocalFileSource.localPermissionCode {
            return .ui("files.fix.localPermission", "시스템 설정 › 개인정보 보호 및 보안 › 파일 및 폴더에서 Deskhand의 접근을 허용하세요.")
        }
        return error.fix
    }

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            ErrorBanner(message: error.message, fix: fix, code: error.code == LocalFileSource.localPermissionCode ? nil : error.code)
            HStack(spacing: DS.Space.s) {
                Button(action: retry) {
                    Label(String.ui("action.retry", "다시 시도"), systemImage: "arrow.clockwise")
                }
                .buttonStyle(.borderedProminent)
                if canGoUp {
                    Button(action: goUp) {
                        Label(String.ui("files.goUp", "상위 폴더로"), systemImage: "arrow.up")
                    }
                    .buttonStyle(.bordered)
                }
                if side == .local, error.code == LocalFileSource.localPermissionCode {
                    Button(String.ui("files.openPrivacy", "개인정보 보호 설정 열기")) {
                        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_FilesAndFolders") {
                            NSWorkspace.shared.open(url)
                        }
                    }
                    .buttonStyle(.bordered)
                }
            }
            .controlSize(.regular)
        }
        .frame(maxWidth: 480)
        .padding(DS.Space.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// The remote pane while the computer cannot list files: offline, blocked, connecting,
/// reconnecting, or the `files` capability not ready. Connect and re-pair stay in the
/// workspace banner above.
private struct RemoteUnavailableView: View {
    let host: Host
    let availability: RemoteFilesAvailability

    var body: some View {
        let waiting = String.ui("files.remote.waiting", "연결되면 마지막으로 보던 폴더를 다시 엽니다. 연결이 끊겨 멈춘 전송은 다시 연결된 뒤 전송 목록에서 ‘재개’를 누르세요.")
        let repairs = String.ui("files.remote.blocked", "다시 페어링하면 원격 폴더를 열고 파일을 주고받을 수 있습니다.")
        switch availability {
        case .connecting:
            HostStateBody(state: .connecting, symbol: "folder", resumes: waiting, repairs: repairs)
        case .reconnecting:
            HostStateBody(state: .reconnecting, symbol: "folder", resumes: waiting, repairs: repairs)
        case .offline:
            HostStateBody(state: .offline, symbol: "folder",
                          resumes: String.ui("files.remote.offlineMessage", "위 배너의 ‘연결’로 \(host.name)에 연결하면 원격 폴더를 열고 파일을 주고받을 수 있습니다."),
                          repairs: repairs)
        case .blocked:
            HostStateBody(state: .blocked, symbol: "folder", resumes: waiting, repairs: repairs)
        case .unavailable(_, let reason):
            EmptyStateView(symbol: "folder.badge.questionmark", title: String.ui("files.remote.unavailable", "파일 기능을 쓸 수 없습니다"),
                           message: reason ?? String.ui("files.remote.unavailableMessage", "에이전트가 파일 기능이 준비되지 않았다고 알렸습니다."))
        case .ready:
            EmptyView()
        }
    }
}
