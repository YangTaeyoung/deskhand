import Foundation
import Observation

/// What one pane shows: the open folder, its items sorted and filtered, the rows revealed so
/// far, the selection and the back/forward history. Rows are revealed `pageSize` at a time;
/// when the loaded items run out, the next page is fetched from the source. The sorted,
/// filtered order is kept and rebuilt only when the items, sort, search or hidden filter
/// change; revealing more rows only takes a longer prefix of it.
@MainActor
@Observable
public final class FileBrowser {
    public enum Phase: Equatable, Sendable {
        case idle
        /// The first page of a newly opened folder is loading.
        case loading
        case loaded
        case failed(OperationError)
    }

    /// Rows revealed at a time and the page size asked of the source.
    public static let pageSize = 500

    public let side: FileSide
    public let style: PathStyle
    @ObservationIgnored public let source: any FileListingSource

    public private(set) var path: String
    public private(set) var phase: Phase = .idle
    /// The open folder's loaded items in source order.
    public private(set) var items: [FileItem] = []
    public private(set) var nextCursor: String?
    /// Items in the open folder, when the source reports it.
    public private(set) var total: Int?
    /// The source listed only part of the folder.
    public private(set) var truncated = false
    /// A reload of the open folder is in flight while its rows stay shown.
    public private(set) var isRefreshing = false
    public private(set) var isLoadingMore = false
    public private(set) var loadMoreError: OperationError?
    /// Rows the table may show; grows by `pageSize`.
    public private(set) var revealed = FileBrowser.pageSize
    /// Sorted, filtered rows, at most `revealed`.
    public private(set) var rows: [FileItem] = []
    /// Loaded items that pass the search and hidden-file filters.
    public private(set) var matchCount = 0
    public private(set) var backStack: [String] = []
    public private(set) var forwardStack: [String] = []

    public var sort: FileSort {
        didSet {
            guard sort != oldValue else { return }
            reorder()
            fetchRemainingWhenFiltering()
        }
    }

    /// Search text; rows whose names contain it.
    public var query = "" {
        didSet {
            guard query != oldValue else { return }
            revealed = Self.pageSize
            reorder()
            fetchRemainingWhenFiltering()
        }
    }

    public var showsHidden: Bool {
        didSet {
            guard showsHidden != oldValue else { return }
            reorder()
        }
    }

    /// Paths of the selected rows.
    public var selection: Set<String> = []
    /// A row the table starts renaming once it shows it.
    public var renameRequest: String?

    @ObservationIgnored private var token = 0
    @ObservationIgnored private var remainingTask: Task<Void, Never>?
    /// `items` with their sort keys, in source order.
    @ObservationIgnored private var keyed: [KeyedFileItem] = []
    /// Items that pass the filters, in sort order.
    @ObservationIgnored private var ordered: [FileItem] = []

    public init(side: FileSide, source: any FileListingSource, path: String, sort: FileSort = .standard, showsHidden: Bool = false) {
        self.side = side
        self.source = source
        self.style = source.style
        self.path = FilePath.normalized(path, style: source.style)
        self.sort = sort
        self.showsHidden = showsHidden
    }

    // MARK: Queries

    public var hasMore: Bool { revealed < matchCount || nextCursor != nil }

    public var parentPath: String? { FilePath.parent(path, style: style) }

    public var crumbs: [PathCrumb] {
        FilePath.crumbs(path, style: style) { [side] root in
            side == .local && root == "/" ? LocalFileSource.displayName("/") : root
        }
    }

    /// Selected rows in row order.
    public var selectedItems: [FileItem] {
        rows.filter { selection.contains($0.path) }
    }

    /// The loaded item named `name`, compared the way the folder's file system does.
    public func item(named name: String) -> FileItem? {
        items.first { style.sameName($0.name, name) }
    }

    /// Every item of the folder is loaded.
    public var isComplete: Bool { nextCursor == nil && phase == .loaded }

    // MARK: Navigation

    /// Opens `path`, recording the current folder for Back.
    public func open(_ path: String) async {
        let target = FilePath.normalized(path, style: style)
        if !style.sameName(target, self.path) || phase == .idle {
            if phase != .idle { backStack.append(self.path) }
            forwardStack = []
        }
        await load(target)
    }

    public func goUp() async {
        guard let parent = parentPath else { return }
        let child = path
        await open(parent)
        selection = [child]
    }

    public func goBack() async {
        guard let previous = backStack.popLast() else { return }
        forwardStack.append(path)
        await load(previous)
    }

    public func goForward() async {
        guard let next = forwardStack.popLast() else { return }
        backStack.append(path)
        await load(next)
    }

    /// Lists the open folder again, keeping the selection of rows that still exist.
    public func reload() async {
        await load(path, keepRows: true)
    }

    /// Reveals the next `pageSize` rows, fetching the next page when the loaded items run out.
    public func loadMore() async {
        guard phase == .loaded else { return }
        if revealed < matchCount {
            revealed += Self.pageSize
            reslice()
            return
        }
        guard nextCursor != nil else { return }
        revealed += Self.pageSize
        await fetchNextPage()
    }

    // MARK: Loading

    /// Lists `target`, then shows the folder as the source spells it. With `keepRows`, the open
    /// folder stays shown while it reloads, and as many pages are fetched as were loaded before so
    /// the revealed rows and the selection survive.
    private func load(_ target: String, keepRows: Bool = false) async {
        token += 1
        let current = token
        remainingTask?.cancel()
        remainingTask = nil
        let refreshing = keepRows && phase == .loaded
        let previousCount = items.count
        if refreshing {
            isRefreshing = true
        } else {
            path = target
            phase = .loading
            setItems([])
            nextCursor = nil
            total = nil
            truncated = false
            selection = []
            revealed = Self.pageSize
        }
        loadMoreError = nil
        do {
            let first = try await source.list(path: target, cursor: nil, limit: Self.pageSize)
            var page = first
            var collected = page.items
            if refreshing {
                while collected.count < previousCount, let cursor = page.nextCursor {
                    let next = try await source.list(path: target, cursor: cursor, limit: Self.pageSize)
                    guard current == token else { return }
                    let known = Set(collected.map(\.path))
                    collected.append(contentsOf: next.items.filter { !known.contains($0.path) })
                    page = FileListPage(items: [], nextCursor: next.nextCursor, total: next.total ?? page.total,
                                        truncated: page.truncated || next.truncated)
                }
            }
            guard current == token else { return }
            path = first.path.map { FilePath.normalized($0, style: style) } ?? target
            nextCursor = page.nextCursor
            total = page.total
            truncated = page.truncated
            phase = .loaded
            isRefreshing = false
            if refreshing {
                let paths = Set(collected.map(\.path))
                selection = selection.intersection(paths)
            }
            setItems(collected)
            fetchRemainingWhenFiltering()
        } catch {
            guard current == token else { return }
            isRefreshing = false
            path = target
            if error is CancellationError { return }
            phase = .failed(OperationError(error))
        }
    }

    private func fetchNextPage() async {
        guard let cursor = nextCursor, !isLoadingMore else { return }
        let current = token
        isLoadingMore = true
        loadMoreError = nil
        defer { if current == token { isLoadingMore = false } }
        do {
            let page = try await source.list(path: path, cursor: cursor, limit: Self.pageSize)
            guard current == token else { return }
            let known = Set(items.map(\.path))
            appendItems(page.items.filter { !known.contains($0.path) })
            nextCursor = page.nextCursor
            total = page.total ?? total
            truncated = truncated || page.truncated
        } catch {
            guard current == token else { return }
            if error is CancellationError { return }
            loadMoreError = OperationError(error)
        }
    }

    /// While a search or a non-default sort is active, fetches every remaining page so the
    /// rows cover the whole folder.
    private func fetchRemainingWhenFiltering() {
        guard nextCursor != nil, remainingTask == nil, query.isEmpty == false || sort != .standard else { return }
        remainingTask = Task { [weak self] in
            while let self, self.nextCursor != nil, self.loadMoreError == nil, !Task.isCancelled {
                await self.fetchNextPage()
            }
            self?.remainingTask = nil
        }
    }

    /// Retries the page that failed to load.
    public func retryLoadMore() async {
        loadMoreError = nil
        await fetchNextPage()
    }

    /// Replaces the items and their keys, then rebuilds the order.
    private func setItems(_ new: [FileItem]) {
        items = new
        keyed = new.map(KeyedFileItem.init)
        reorder()
    }

    /// Adds a page of items and their keys, then rebuilds the order.
    private func appendItems(_ new: [FileItem]) {
        items.append(contentsOf: new)
        keyed.append(contentsOf: new.map(KeyedFileItem.init))
        reorder()
    }

    /// Filters and sorts the keyed items, then reslices the rows.
    private func reorder() {
        let needle = query.trimmingCharacters(in: .whitespaces)
        let visible = keyed.filter { entry in
            (showsHidden || !entry.item.isHidden) && (needle.isEmpty || entry.item.name.localizedStandardContains(needle))
        }
        matchCount = visible.count
        ordered = sort.sorted(visible).map(\.item)
        reslice()
    }

    /// The first `revealed` rows of the kept order.
    private func reslice() {
        let next = Array(ordered.prefix(revealed))
        if next != rows { rows = next }
    }

    // MARK: Seeding

    /// Puts the browser in a loaded state with `items` without asking the source. Snapshot
    /// scenes use it to render a folder deterministically.
    public func seed(path: String, items: [FileItem], nextCursor: String? = nil, total: Int? = nil, selection: Set<String> = [],
                     phase: Phase = .loaded, backStack: [String] = []) {
        token += 1
        self.path = FilePath.normalized(path, style: style)
        self.nextCursor = nextCursor
        self.total = total ?? (nextCursor == nil ? items.count : nil)
        self.selection = selection
        self.phase = phase
        self.backStack = backStack
        revealed = Self.pageSize
        setItems(items)
    }
}
