import DeskhandClient
import Foundation
import Synchronization
import struct DeskhandClient.Host

/// In-memory remote folders of the preview computers and the transfers the preview daemon runs.
final class PreviewFileStore: Sendable {
    /// Remote folders of one computer, keyed by lowercased normalized folder path.
    struct Tree: Sendable {
        var folders: [String: [FileEntry]] = [:]
        var denied: [String: String] = [:]
        /// Files another process holds open: they can be copied but not deleted.
        var locked: Set<String> = []

        static func key(_ path: String) -> String {
            FilePath.normalized(path, style: .windows).lowercased()
        }

        func list(_ path: String) throws -> [FileEntry] {
            let key = Self.key(path)
            if let message = denied[key] { throw RPCErrorInfo(message: message, code: "PERMISSION_DENIED") }
            guard let entries = folders[key] else {
                let name = FilePath.lastComponent(path, style: .windows)
                if let entry = entry(at: path), !entry.dir {
                    throw RPCErrorInfo(message: "‘\(name)’ 항목은 폴더가 아닙니다", code: "INVALID_ARGUMENT")
                }
                throw RPCErrorInfo(message: "‘\(name)’ 폴더가 없습니다", code: "NOT_FOUND")
            }
            return entries.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
        }

        func entry(at path: String) -> FileEntry? {
            guard let parent = FilePath.parent(path, style: .windows) else {
                let root = FilePath.normalized(path, style: .windows)
                return folders[Self.key(root)] == nil ? nil
                    : FileEntry(name: root, path: root, dir: true, size: 0, mtime: Timestamp(date: .now), link: false)
            }
            let name = FilePath.lastComponent(path, style: .windows)
            return folders[Self.key(parent)]?.first { PathStyle.windows.sameName($0.name, name) }
        }

        mutating func insert(_ entry: FileEntry) {
            guard let parent = FilePath.parent(entry.path, style: .windows) else { return }
            ensureFolder(parent)
            let key = Self.key(parent)
            folders[key, default: []].removeAll { PathStyle.windows.sameName($0.name, entry.name) }
            folders[key, default: []].append(entry)
            if entry.dir, folders[Self.key(entry.path)] == nil { folders[Self.key(entry.path)] = [] }
        }

        mutating func ensureFolder(_ path: String) {
            let key = Self.key(path)
            guard folders[key] == nil else { return }
            folders[key] = []
            if let parent = FilePath.parent(path, style: .windows) {
                ensureFolder(parent)
                let name = FilePath.lastComponent(path, style: .windows)
                if folders[Self.key(parent)]?.contains(where: { PathStyle.windows.sameName($0.name, name) }) != true {
                    folders[Self.key(parent), default: []].append(FileEntry(
                        name: name, path: FilePath.normalized(path, style: .windows), dir: true, size: 0,
                        mtime: Timestamp(date: .now), link: false))
                }
            }
        }

        mutating func remove(_ path: String) {
            if let parent = FilePath.parent(path, style: .windows) {
                let name = FilePath.lastComponent(path, style: .windows)
                folders[Self.key(parent)]?.removeAll { PathStyle.windows.sameName($0.name, name) }
            }
            let key = Self.key(path)
            for folder in folders.keys where folder == key || folder.hasPrefix(key + "\\") {
                folders[folder] = nil
            }
        }

        mutating func rename(_ from: String, to: String) {
            guard var entry = entry(at: from) else { return }
            let fromKey = Self.key(from)
            let moved = folders.filter { $0.key == fromKey || $0.key.hasPrefix(fromKey + "\\") }
            remove(from)
            entry.name = FilePath.lastComponent(to, style: .windows)
            entry.path = FilePath.normalized(to, style: .windows)
            insert(entry)
            let fromPath = FilePath.normalized(from, style: .windows)
            for (key, entries) in moved {
                let newKey = Self.key(to) + key.dropFirst(fromKey.count)
                folders[newKey] = entries.map { child in
                    var child = child
                    child.path = entry.path + child.path.dropFirst(fromPath.count)
                    return child
                }
            }
        }

        /// Files under `path` with paths relative to it, at most `limit`.
        func files(under path: String, limit: Int = 400) -> [(rel: String, entry: FileEntry)] {
            var result: [(String, FileEntry)] = []
            var stack: [(String, String)] = [(path, "")]
            while let (folder, prefix) = stack.popLast(), result.count < limit {
                for entry in (try? list(folder)) ?? [] {
                    let rel = prefix.isEmpty ? entry.name : prefix + "\\" + entry.name
                    if entry.dir {
                        stack.append((entry.path, rel))
                    } else if result.count < limit {
                        result.append((rel, entry))
                    }
                }
            }
            return result
        }
    }

    /// What a running transfer needs beyond its `Transfer`.
    struct Plan: Sendable {
        /// Where the item is written; differs from the request after a rename.
        var destination: String
        var renamed: Bool
        /// The pull adds its files to the local overlay.
        var toOverlay: Bool
    }

    private struct State: Sendable {
        var trees: [String: Tree] = [:]
        var transfers: [String: Transfer] = [:]
        /// Transfer IDs, newest first.
        var order: [String] = []
        var plans: [String: Plan] = [:]
        var tasks: [String: Task<Void, Never>] = [:]
        var fixturesToRun: [String] = []
        var next = 0
    }

    private let state: Mutex<State>

    init(hosts: [Host], includesTransfers: Bool) {
        var s = State()
        for host in hosts where PathStyle(os: host.os) == .windows {
            var tree = Tree()
            tree.folders = Dictionary(PreviewFixtures.remoteFolders(user: host.session?.user ?? "user").map { (Tree.key($0.key), $0.value) },
                                      uniquingKeysWith: { a, _ in a })
            tree.denied = Dictionary(uniqueKeysWithValues: PreviewFixtures.deniedFolders.map { (Tree.key($0.key), $0.value) })
            tree.locked = [Tree.key(PreviewFixtures.lockedLog)]
            s.trees[host.hostID] = tree
        }
        if includesTransfers, let dev = hosts.first(where: { $0.hostID == PreviewFixtures.devPCID }) {
            let fixtures = PreviewFixtures.transfers(hostID: dev.hostID, user: dev.session?.user ?? "user",
                                                     localHome: FileManager.default.homeDirectoryForCurrentUser.path)
            for transfer in fixtures {
                s.transfers[transfer.transferID] = transfer
                s.order.append(transfer.transferID)
                s.plans[transfer.transferID] = Plan(destination: transfer.direction.hasSuffix("push") ? transfer.remote : transfer.local,
                                                    renamed: false, toOverlay: false)
            }
            s.fixturesToRun = fixtures.filter { $0.state == "running" }.map(\.transferID)
        }
        state = Mutex(s)
    }

    /// Runs `body` on the host's folders under the lock.
    func tree<T: Sendable, E: Error>(_ hostID: String, _ body: (inout Tree) throws(E) -> T) throws(E) -> T {
        try state.withLock { (s: inout State) throws(E) -> T in
            var tree = s.trees[hostID] ?? Tree()
            defer { s.trees[hostID] = tree }
            return try body(&tree)
        }
    }

    func transfer(_ id: String) -> Transfer? { state.withLock { $0.transfers[id] } }
    func plan(_ id: String) -> Plan? { state.withLock { $0.plans[id] } }

    func transfers(hostID: String) -> [Transfer] {
        state.withLock { s in s.order.compactMap { s.transfers[$0] }.filter { $0.hostID == hostID } }
    }

    /// Stores a new transfer at `rev` 1 and returns it as stored.
    func add(_ transfer: Transfer, plan: Plan) -> Transfer {
        var stamped = transfer
        stamped.rev = 1
        stamped.updatedAt = Timestamp(date: .now)
        state.withLock { s in
            s.transfers[stamped.transferID] = stamped
            s.order.insert(stamped.transferID, at: 0)
            s.plans[stamped.transferID] = plan
        }
        return stamped
    }

    /// Stores a change with the next `rev` and the current time, and returns it as stored.
    @discardableResult
    func update(_ transfer: Transfer) -> Transfer {
        state.withLock { s in
            var stamped = transfer
            stamped.rev = max(s.transfers[transfer.transferID]?.rev ?? 0, transfer.rev) + 1
            stamped.updatedAt = Timestamp(date: .now)
            s.transfers[transfer.transferID] = stamped
            return stamped
        }
    }

    func nextID() -> String {
        state.withLock { s in
            s.next += 1
            return "t-preview-\(s.next)"
        }
    }

    func setTask(_ task: Task<Void, Never>?, for id: String) {
        let old = state.withLock { s -> Task<Void, Never>? in
            let old = s.tasks[id]
            s.tasks[id] = task
            return old
        }
        if old != task { old?.cancel() }
    }

    /// Running fixture transfers that have not started simulating yet; empty after the first call.
    func takeFixturesToRun() -> [String] {
        state.withLock { s in
            defer { s.fixturesToRun = [] }
            return s.fixturesToRun
        }
    }

    /// `name (2).ext`, `name (3).ext` … — the first name for which `exists` is false.
    static func copyName(_ name: String, exists: (String) -> Bool) -> String {
        let ext = FileKind.pathExtension(of: name)
        let base = ext.isEmpty ? name : String(name.dropLast(ext.count + 1))
        var n = 2
        while true {
            let candidate = ext.isEmpty ? "\(base) (\(n))" : "\(base) (\(n)).\(name.suffix(ext.count))"
            if !exists(candidate) { return candidate }
            n += 1
        }
    }
}

// MARK: Files

extension PreviewBackend {
    private func readyHost(_ hostID: String) throws -> Host {
        let host = try listedHost(hostID)
        guard host.connection == .ready || host.connection == .degraded else {
            throw RPCErrorInfo(message: "\(host.name)에 연결되어 있지 않습니다", code: "OFFLINE", retryable: true)
        }
        return host
    }

    public func filesList(hostID: String, path: String, cursor: String?, limit: Int) async throws -> FileEntryPage {
        _ = try readyHost(hostID)
        await pause(fraction: 0.35)
        let all = try files.tree(hostID) { try $0.list(path) }
        let start = min(all.count, max(0, Int(cursor ?? "") ?? 0))
        let end = start + min(max(1, limit), all.count - start)
        return FileEntryPage(entries: Array(all[start..<end]), nextCursor: end < all.count ? String(end) : nil, total: all.count)
    }

    public func filesStat(hostID: String, path: String) async throws -> FileEntry {
        _ = try readyHost(hostID)
        guard let entry = files.tree(hostID, { $0.entry(at: path) }) else {
            let name = FilePath.lastComponent(path, style: .windows)
            throw RPCErrorInfo(message: "‘\(name)’ 항목이 없습니다", code: "NOT_FOUND")
        }
        return entry
    }

    public func filesMkdir(hostID: String, path: String) async throws {
        _ = try readyHost(hostID)
        await pause(fraction: 0.3)
        try files.tree(hostID) { tree in
            let name = FilePath.lastComponent(path, style: .windows)
            if tree.entry(at: path) != nil {
                throw OperationError(code: "INVALID_ARGUMENT", message: "‘\(name)’ 이름의 항목이 이미 있습니다")
            }
            tree.insert(FileEntry(name: name, path: FilePath.normalized(path, style: .windows), dir: true, size: 0,
                                  mtime: Timestamp(date: .now), link: false))
        }
    }

    public func filesRename(hostID: String, from: String, to: String) async throws {
        _ = try readyHost(hostID)
        await pause(fraction: 0.3)
        try files.tree(hostID) { tree in
            let name = FilePath.lastComponent(from, style: .windows)
            guard tree.entry(at: from) != nil else { throw OperationError(code: "NOT_FOUND", message: "‘\(name)’ 항목이 없습니다") }
            if tree.locked.contains(PreviewFileStore.Tree.key(from)) {
                throw OperationError(code: "PERMISSION_DENIED", message: "다른 프로세스가 ‘\(name)’ 파일을 사용 중이라 이름을 바꿀 수 없습니다")
            }
            if let existing = tree.entry(at: to), !PathStyle.windows.sameName(existing.path, from) {
                throw OperationError(code: "INVALID_ARGUMENT", message: "‘\(existing.name)’ 이름의 항목이 이미 있습니다")
            }
            tree.rename(from, to: to)
        }
    }

    public func filesTrash(hostID: String, path: String) async throws {
        try await removeRemote(hostID: hostID, path: path)
    }

    public func filesDelete(hostID: String, path: String) async throws {
        try await removeRemote(hostID: hostID, path: path)
    }

    private func removeRemote(hostID: String, path: String) async throws {
        _ = try readyHost(hostID)
        await pause(fraction: 0.3)
        try files.tree(hostID) { tree in
            let name = FilePath.lastComponent(path, style: .windows)
            guard tree.entry(at: path) != nil else { throw OperationError(code: "NOT_FOUND", message: "‘\(name)’ 항목이 없습니다") }
            if tree.locked.contains(PreviewFileStore.Tree.key(path)) {
                throw OperationError(code: "PERMISSION_DENIED", message: "Shop.exe가 ‘\(name)’ 파일을 사용 중입니다")
            }
            tree.remove(path)
        }
    }

    public func appLaunch(hostID: String, path: String, args: [String]?, cwd: String?) async throws -> Int? {
        _ = try readyHost(hostID)
        await pause(fraction: 0.5)
        guard let entry = files.tree(hostID, { $0.entry(at: path) }) else {
            throw OperationError(code: "NOT_FOUND", message: "‘\(FilePath.lastComponent(path, style: .windows))’ 항목이 없습니다")
        }
        if entry.dir { throw OperationError(code: "INVALID_ARGUMENT", message: "폴더는 실행할 수 없습니다") }
        let extensionName = FileKind.pathExtension(of: entry.name)
        if extensionName == "dmp" || extensionName == "dat" {
            throw OperationError(code: "INVALID_ARGUMENT", message: "원격 PC에 ‘\(entry.name)’ 파일을 열 프로그램이 없습니다")
        }
        return 4_000 + abs(entry.name.hashValue % 20_000)
    }

    // MARK: Transfers

    public func transferStart(_ request: TransferRequest) async throws -> String {
        let host = try readyHost(request.hostID)
        await pause(fraction: 0.2)
        let id = files.nextID()
        var plan = PreviewFileStore.Plan(destination: request.direction.isUpload ? request.remote : request.local,
                                         renamed: false, toOverlay: !request.direction.isUpload)
        let items = try transferItems(request, host: host)
        let exists = request.direction.isUpload
            ? files.tree(request.hostID) { $0.entry(at: request.remote) != nil }
            : existsOnMac(request.local)
        let name = request.direction.isUpload
            ? FilePath.lastComponent(request.local, style: .posix)
            : FilePath.lastComponent(request.remote, style: .windows)
        var transfer = Transfer(
            transferID: id, hostID: request.hostID, direction: request.direction.rawValue, local: request.local,
            remote: request.remote, onConflict: request.onConflict.rawValue, state: TransferState.queued.rawValue,
            bytesDone: 0, bytesTotal: items.reduce(0) { $0 + $1.size }, filesDone: 0, filesTotal: items.count, rateBps: 0,
            files: items.map { TransferFile(rel: $0.rel, size: $0.size, bytesDone: 0, state: TransferState.queued.rawValue) })
        if exists {
            switch request.onConflict {
            case .fail:
                transfer.state = TransferState.failed.rawValue
                transfer.outcome = TransferOutcome.failed.rawValue
                transfer.error = EnvelopeError(code: "TRANSFER_CONFLICT", message: "대상에 같은 이름의 ‘\(name)’ 항목이 있어 보내지 않았습니다")
                transfer.files = transfer.files.map { file in
                    var file = file
                    file.state = TransferState.failed.rawValue
                    file.outcome = TransferOutcome.failed.rawValue
                    file.error = EnvelopeError(code: "TRANSFER_CONFLICT", message: "대상에 같은 이름의 항목이 있습니다")
                    return file
                }
            case .skip:
                transfer.state = TransferState.done.rawValue
                transfer.outcome = TransferOutcome.skipped.rawValue
                transfer.files = transfer.files.map { file in
                    var file = file
                    file.state = TransferState.done.rawValue
                    file.outcome = TransferOutcome.skipped.rawValue
                    return file
                }
            case .rename:
                let folder = FilePath.parent(plan.destination, style: request.direction.isUpload ? .windows : .posix) ?? plan.destination
                let free = PreviewFileStore.copyName(name) { candidate in
                    request.direction.isUpload
                        ? self.files.tree(request.hostID) { $0.entry(at: FilePath.join(folder, candidate, style: .windows)) != nil }
                        : self.existsOnMac(FilePath.join(folder, candidate, style: .posix))
                }
                plan.destination = FilePath.join(folder, free, style: request.direction.isUpload ? .windows : .posix)
                plan.renamed = true
                if transfer.files.count == 1 { transfer.files[0].rel = free }
            case .replace:
                break
            }
        }
        transfer = files.add(transfer, plan: plan)
        emitTransfer(transfer)
        if !transfer.transferState.isFinished { runTransfer(id) }
        return id
    }

    /// The files a request moves, with sizes and paths relative to the transferred item.
    private func transferItems(_ request: TransferRequest, host: Host) throws -> [(rel: String, size: Int)] {
        if request.direction.isUpload {
            let url = URL(fileURLWithPath: request.local)
            var isDirectory: ObjCBool = false
            guard FileManager.default.fileExists(atPath: url.path, isDirectory: &isDirectory) else {
                throw RPCErrorInfo(message: "이 Mac에 ‘\(url.lastPathComponent)’ 항목이 없습니다", code: "NOT_FOUND")
            }
            guard isDirectory.boolValue else {
                let size = (try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0
                return [(url.lastPathComponent, size)]
            }
            var items: [(String, Int)] = []
            let enumerator = FileManager.default.enumerator(at: url, includingPropertiesForKeys: [.fileSizeKey, .isDirectoryKey])
            while let child = enumerator?.nextObject() as? URL, items.count < 400 {
                let values = try? child.resourceValues(forKeys: [.fileSizeKey, .isDirectoryKey])
                guard values?.isDirectory != true else { continue }
                items.append((String(child.path.dropFirst(url.path.count + 1)), values?.fileSize ?? 0))
            }
            return items.sorted { $0.0.localizedStandardCompare($1.0) == .orderedAscending }
        }
        let name = FilePath.lastComponent(request.remote, style: .windows)
        guard let entry = files.tree(request.hostID, { $0.entry(at: request.remote) }) else {
            throw RPCErrorInfo(message: "\(host.name)에 ‘\(name)’ 항목이 없습니다", code: "NOT_FOUND")
        }
        guard entry.dir else { return [(entry.name, entry.size)] }
        return files.tree(request.hostID) { $0.files(under: request.remote) }.map { ($0.rel, $0.entry.size) }
    }

    private func emitTransfer(_ transfer: Transfer) {
        emit(kind: EventKind.transferUpdated, hostID: transfer.hostID, payload: ["transfer": Self.payload(transfer)])
    }

    private func runTransfer(_ id: String) {
        let task = Task { [weak self] in
            guard let self else { return }
            await self.simulate(id)
        }
        files.setTask(task, for: id)
    }

    /// Waits `seconds` scaled to the step delay.
    private func simulatedSleep(_ seconds: Double) async {
        let scaled = seconds * delayScale
        if scaled > 0 {
            try? await Task.sleep(for: .seconds(scaled))
        } else {
            await Task.yield()
        }
    }

    /// Moves bytes at a plausible rate, then verifies and finishes the transfer.
    private func simulate(_ id: String) async {
        let tick = 0.25
        while !Task.isCancelled {
            guard var transfer = files.transfer(id), transfer.transferState == .queued || transfer.transferState == .running else { return }
            transfer.state = TransferState.running.rawValue
            let base = transfer.transferDirection.isUpload ? 46_137_344.0 : 29_360_128.0
            let jitter = 0.94 + Double((transfer.bytesDone / 7_919) % 13) / 100
            let remaining = transfer.bytesTotal - transfer.bytesDone
            let step = Double(transfer.bytesTotal) < base * 1.5 ? max(transfer.bytesTotal / 6, 1) : Int(base * jitter * tick)
            let advance = min(remaining, step)
            transfer.bytesDone += advance
            transfer.rateBps = Int(Double(step) / tick)
            var budget = advance
            for i in transfer.files.indices where budget > 0 || transfer.files[i].size == 0 {
                guard transfer.files[i].transferState != .done else { continue }
                let need = transfer.files[i].size - transfer.files[i].bytesDone
                let take = min(need, budget)
                transfer.files[i].bytesDone += take
                budget -= take
                transfer.files[i].state = transfer.files[i].bytesDone >= transfer.files[i].size
                    ? TransferState.verifying.rawValue : TransferState.running.rawValue
                if transfer.files[i].bytesDone < transfer.files[i].size { break }
            }
            transfer.filesDone = transfer.files.filter { $0.bytesDone >= $0.size }.count
            transfer = files.update(transfer)
            emitTransfer(transfer)
            if transfer.bytesDone >= transfer.bytesTotal { break }
            await simulatedSleep(tick)
        }
        guard !Task.isCancelled, var transfer = files.transfer(id), transfer.transferState == .running else { return }
        transfer.state = TransferState.verifying.rawValue
        transfer.rateBps = 0
        transfer = files.update(transfer)
        emitTransfer(transfer)
        await simulatedSleep(0.8)
        guard !Task.isCancelled, files.transfer(id)?.transferState == .verifying else { return }
        finish(id)
    }

    private func finish(_ id: String) {
        guard var transfer = files.transfer(id), let plan = files.plan(id) else { return }
        let direction = transfer.transferDirection
        var failures = 0
        for i in transfer.files.indices {
            transfer.files[i].state = TransferState.done.rawValue
            transfer.files[i].bytesDone = transfer.files[i].size
            transfer.files[i].outcome = plan.renamed ? TransferOutcome.renamed.rawValue : TransferOutcome.written.rawValue
            transfer.files[i].sha256 = String(format: "%016lx", UInt(bitPattern: transfer.files[i].rel.hashValue ^ transfer.files[i].size))
        }
        if direction.isUpload {
            let name = FilePath.lastComponent(plan.destination, style: .windows)
            let localIsFolder = (try? URL(fileURLWithPath: transfer.local).resourceValues(forKeys: [.isDirectoryKey]).isDirectory) ?? false
            let isFolder = localIsFolder || transfer.files.count != 1 || transfer.files.first?.rel.contains("/") == true
            files.tree(transfer.hostID) { tree in
                tree.insert(FileEntry(name: name, path: plan.destination, dir: isFolder, size: isFolder ? 0 : transfer.bytesTotal,
                                      mtime: Timestamp(date: .now), link: false))
                guard isFolder else { return }
                for file in transfer.files {
                    let rel = file.rel.replacingOccurrences(of: "/", with: "\\")
                    let path = FilePath.join(plan.destination, rel, style: .windows)
                    tree.insert(FileEntry(name: FilePath.lastComponent(path, style: .windows), path: path, dir: false,
                                          size: file.size, mtime: Timestamp(date: .now), link: false))
                }
            }
        } else {
            let protected = PreviewFileStore.Tree.key("C:\\Windows") + "\\"
            let temp = PreviewFileStore.Tree.key("C:\\Windows\\Temp") + "\\"
            for i in transfer.files.indices {
                let source = PreviewFileStore.Tree.key(transfer.files.count == 1 ? transfer.remote
                    : FilePath.join(transfer.remote, transfer.files[i].rel, style: .windows))
                guard source.hasPrefix(protected), !source.hasPrefix(temp) else { continue }
                transfer.files[i].state = TransferState.failed.rawValue
                transfer.files[i].outcome = TransferOutcome.failed.rawValue
                transfer.files[i].sha256 = nil
                transfer.files[i].bytesDone = 0
                transfer.files[i].error = EnvelopeError(code: "ELEVATION_REQUIRED", message: "관리자 권한이 필요한 파일입니다.")
                failures += 1
            }
            if plan.toOverlay {
                addToOverlay(transfer, plan: plan)
            }
        }
        transfer.filesDone = transfer.files.count - failures
        transfer.rateBps = 0
        if failures == transfer.files.count, failures > 0 {
            transfer.state = TransferState.failed.rawValue
            transfer.outcome = TransferOutcome.failed.rawValue
            transfer.error = transfer.files.first?.error
        } else if failures > 0 {
            transfer.state = TransferState.done.rawValue
            transfer.outcome = TransferOutcome.partial.rawValue
        } else if direction == .movePush {
            transfer.state = TransferState.done.rawValue
            transfer.outcome = TransferOutcome.copiedSourceRetained.rawValue
            transfer.error = EnvelopeError(message: "미리보기에서는 이 Mac의 원본 파일을 지우지 않습니다.")
        } else if direction == .movePull {
            let locked = files.tree(transfer.hostID) { $0.locked.contains(PreviewFileStore.Tree.key(transfer.remote)) }
            transfer.state = TransferState.done.rawValue
            if locked {
                transfer.outcome = TransferOutcome.copiedSourceRetained.rawValue
                transfer.error = EnvelopeError(code: "PERMISSION_DENIED", message: "Shop.exe가 이 파일을 열어 두고 있어 원격 PC의 원본을 지우지 못했습니다.")
            } else {
                files.tree(transfer.hostID) { $0.remove(transfer.remote) }
                transfer.outcome = plan.renamed ? TransferOutcome.renamed.rawValue : TransferOutcome.written.rawValue
            }
        } else {
            transfer.state = TransferState.done.rawValue
            transfer.outcome = plan.renamed ? TransferOutcome.renamed.rawValue : TransferOutcome.written.rawValue
        }
        transfer = files.update(transfer)
        files.setTask(nil, for: id)
        emitTransfer(transfer)
    }

    /// Whether `path` exists on this Mac's disk or in the local overlay.
    func existsOnMac(_ path: String) -> Bool {
        FileManager.default.fileExists(atPath: path) || localOverlay.item(at: path) != nil
    }

    /// Shows a finished pull in the local pane: the item, and for a folder its files.
    private func addToOverlay(_ transfer: Transfer, plan: PreviewFileStore.Plan) {
        let now = Date.now
        let single = transfer.files.count == 1 && !(transfer.files.first?.rel.contains("\\") ?? false)
            && files.tree(transfer.hostID, { $0.entry(at: transfer.remote) })?.dir != true
        if single {
            if let file = transfer.files.first, file.transferState != .failed {
                localOverlay.add(FileItem(name: FilePath.lastComponent(plan.destination, style: .posix), path: plan.destination,
                                          isDirectory: false, size: file.size, modified: now))
            }
            return
        }
        localOverlay.add(FileItem(name: FilePath.lastComponent(plan.destination, style: .posix), path: plan.destination,
                                  isDirectory: true, modified: now))
        for file in transfer.files where file.transferState != .failed {
            let path = FilePath.join(plan.destination, file.rel.replacingOccurrences(of: "\\", with: "/"), style: .posix)
            localOverlay.add(FileItem(name: FilePath.lastComponent(path, style: .posix), path: path, isDirectory: false,
                                      size: file.size, modified: now))
        }
    }

    public func transferList(hostID: String) async throws -> [Transfer] {
        _ = try listedHost(hostID)
        for id in files.takeFixturesToRun() { runTransfer(id) }
        return files.transfers(hostID: hostID)
    }

    public func transferGet(transferID: String) async throws -> Transfer {
        guard let transfer = files.transfer(transferID) else {
            throw RPCErrorInfo(message: "전송 \(transferID) 항목을 찾을 수 없습니다", code: "NOT_FOUND")
        }
        return transfer
    }

    public func transferPause(transferID: String) async throws -> Transfer {
        var transfer = try await transferGet(transferID: transferID)
        guard [.queued, .running].contains(transfer.transferState) else { return transfer }
        files.setTask(nil, for: transferID)
        transfer.state = TransferState.paused.rawValue
        transfer.rateBps = 0
        transfer.error = nil
        for i in transfer.files.indices where transfer.files[i].transferState == .running {
            transfer.files[i].state = TransferState.paused.rawValue
        }
        transfer = files.update(transfer)
        emitTransfer(transfer)
        return transfer
    }

    public func transferResume(transferID: String) async throws -> Transfer {
        var transfer = try await transferGet(transferID: transferID)
        guard [.paused, .failed, .cancelled].contains(transfer.transferState) else { return transfer }
        _ = try readyHost(transfer.hostID)
        transfer.state = TransferState.queued.rawValue
        transfer.outcome = nil
        transfer.error = nil
        for i in transfer.files.indices where transfer.files[i].transferState != .done || transfer.files[i].transferOutcome == .failed {
            if transfer.files[i].transferState == .failed { transfer.files[i].bytesDone = 0 }
            transfer.files[i].state = TransferState.queued.rawValue
            transfer.files[i].outcome = nil
            transfer.files[i].error = nil
        }
        transfer.bytesDone = transfer.files.reduce(0) { $0 + $1.bytesDone }
        transfer = files.update(transfer)
        emitTransfer(transfer)
        runTransfer(transferID)
        return transfer
    }

    public func transferCancel(transferID: String) async throws -> Transfer {
        var transfer = try await transferGet(transferID: transferID)
        guard !transfer.transferState.isFinished else { return transfer }
        files.setTask(nil, for: transferID)
        transfer.state = TransferState.cancelled.rawValue
        transfer.rateBps = 0
        for i in transfer.files.indices where transfer.files[i].transferState != .done {
            transfer.files[i].state = TransferState.cancelled.rawValue
        }
        transfer.filesDone = transfer.files.filter { $0.transferState == .done }.count
        transfer = files.update(transfer)
        emitTransfer(transfer)
        return transfer
    }
}
