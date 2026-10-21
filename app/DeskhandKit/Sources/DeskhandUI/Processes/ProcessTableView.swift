import AppKit
import DeskhandClient
import SwiftUI

/// SF Symbol and formatted values of a process as the processes tab shows them.
enum ProcessFormat {
    private static let shells: Set<String> = ["powershell.exe", "pwsh.exe", "cmd.exe", "conhost.exe", "openconsole.exe",
                                              "windowsterminal.exe", "bash.exe", "wsl.exe"]
    private static let browsers: Set<String> = ["chrome.exe", "msedge.exe", "firefox.exe", "msedgewebview2.exe", "brave.exe"]
    private static let developer: Set<String> = ["code.exe", "devenv.exe", "rider64.exe", "node.exe", "dotnet.exe", "python.exe",
                                                 "java.exe", "esbuild.exe", "testhost.exe", "git.exe"]
    private static let serviceAccounts = ["NT AUTHORITY\\", "NT SERVICE\\", "WINDOW MANAGER\\", "FONT DRIVER HOST\\"]

    /// A symbol for the kind of program.
    static func symbol(for process: RemoteProcess) -> String {
        let name = process.name.lowercased()
        if ProcessGuard.agentNames.contains(name) { return "dot.radiowaves.left.and.right" }
        if shells.contains(name) { return "terminal" }
        if browsers.contains(name) { return "globe" }
        if developer.contains(name) { return "chevron.left.forwardslash.chevron.right" }
        if name == "explorer.exe" { return "folder" }
        if name.hasPrefix("vmmem") { return "shippingbox" }
        if isService(process) || ProcessGuard.block(for: process) == .critical || name == "svchost.exe" { return "gearshape" }
        return "macwindow"
    }

    static func isService(_ process: RemoteProcess) -> Bool {
        guard let user = process.user?.uppercased() else { return true }
        return serviceAccounts.contains { user.hasPrefix($0) }
    }

    /// The command line without the program, such as `--type=renderer`, or nil when there is none.
    static func arguments(of process: RemoteProcess) -> String? {
        guard var cmd = process.cmd?.trimmingCharacters(in: .whitespaces), !cmd.isEmpty else { return nil }
        if cmd.hasPrefix("\""), let close = cmd.dropFirst().firstIndex(of: "\"") {
            cmd = String(cmd[cmd.index(after: close)...])
        } else if let space = cmd.firstIndex(of: " ") {
            cmd = String(cmd[space...])
        } else {
            return nil
        }
        let rest = cmd.trimmingCharacters(in: .whitespaces)
        return rest.isEmpty ? nil : rest
    }

    /// The user without the `NT AUTHORITY\` style prefix of service accounts.
    static func user(_ value: String?) -> String {
        guard let value else { return "—" }
        if let slash = value.lastIndex(of: "\\") { return String(value[value.index(after: slash)...]) }
        return value
    }

    /// `오늘 15:12`, `어제 18:40`, otherwise `10월 18일 17:12`, or `—` when the agent did not report it.
    static func started(_ date: Date?) -> String {
        date.map { DS.Format.dateTime($0) } ?? "—"
    }

    /// `12.4`, or `—` when the agent did not measure it.
    static func cpu(_ value: Double?) -> String {
        value.map { DS.Format.decimal($0) } ?? "—"
    }

    /// `12.4%`, or `—` when the agent did not measure it.
    static func cpuPercent(_ value: Double?) -> String {
        value.map { DS.Format.percent(points: $0, decimals: 1) } ?? "—"
    }
}

/// The process table: name with kind symbol, tree disclosure, `Deskhand가 시작함` tag and a
/// lock on protected processes, then PID, CPU, memory, user, start and path.
struct ProcessTableView: View {
    let model: ProcessesModel
    let hostName: String
    var autosave = true

    /// Columns of the list, with a wider name column in the tree.
    static func columns(tree: Bool) -> [DataTableColumn] {
        var columns = columns
        columns[0].width = tree ? 380 : 270
        return columns
    }

    static let columns: [DataTableColumn] = [
        DataTableColumn(id: ProcessColumn.name.rawValue, title: ProcessColumn.name.title, width: 270, minWidth: 140,
                        firstSortAscending: ProcessColumn.name.firstSortAscending),
        DataTableColumn(id: ProcessColumn.pid.rawValue, title: ProcessColumn.pid.title, width: 56, minWidth: 44, alignment: .right,
                        firstSortAscending: ProcessColumn.pid.firstSortAscending),
        DataTableColumn(id: ProcessColumn.cpu.rawValue, title: ProcessColumn.cpu.title, width: 68, minWidth: 56, alignment: .right,
                        firstSortAscending: ProcessColumn.cpu.firstSortAscending,
                        headerHelp: String.ui("processes.column.cpu.help", "모든 논리 코어를 합친 사용률. 0.5초 간격 두 표본으로 잽니다.")),
        DataTableColumn(id: ProcessColumn.memory.rawValue, title: ProcessColumn.memory.title, width: 78, minWidth: 60, alignment: .right,
                        firstSortAscending: ProcessColumn.memory.firstSortAscending,
                        headerHelp: String.ui("processes.column.memory.help", "작업 집합(물리 메모리)")),
        DataTableColumn(id: ProcessColumn.user.rawValue, title: ProcessColumn.user.title, width: 124, minWidth: 64,
                        firstSortAscending: ProcessColumn.user.firstSortAscending, hideBelow: 760),
        DataTableColumn(id: ProcessColumn.started.rawValue, title: ProcessColumn.started.title, width: 138, minWidth: 80,
                        firstSortAscending: ProcessColumn.started.firstSortAscending, hideBelow: 960),
        DataTableColumn(id: ProcessColumn.path.rawValue, title: ProcessColumn.path.title, width: 260, minWidth: 120,
                        firstSortAscending: ProcessColumn.path.firstSortAscending, fills: true),
    ]

    var body: some View {
        DataTable(
            columns: Self.columns(tree: model.showsTree),
            rows: rows,
            selection: model.selectedPID,
            sort: DataTableSort(column: model.sort.column.rawValue, ascending: model.sort.ascending),
            autosaveName: autosave ? (model.showsTree ? "processes.tree" : "processes.table") : nil,
            accessibilityLabel: String.ui("processes.a11y.table", "\(hostName)의 프로세스 목록"),
            cell: { [model] item, column, table in Self.cell(item, column: column, table: table, model: model) },
            onSelect: { [model] pid in model.selectedPID = pid },
            onSort: { [model] sort in
                guard let column = ProcessColumn(rawValue: sort.column) else { return }
                model.sort = ProcessSort(column: column, ascending: sort.ascending)
            },
            onDoubleClick: { [model] item in
                if model.showsTree, item.row.hasChildren { model.toggleCollapsed(item.id) }
            },
            menu: { [model] item in ProcessMenu.entries(for: item.row.process, model: model) },
            onKey: { [model] key, item in Self.handle(key, item: item, model: model) })
        .id(model.showsTree)
    }

    /// Rows with what their cells draw beyond the process, so a change redraws the row.
    private var rows: [ProcessTableItem] {
        let tree = model.showsTree
        return model.rows.map { row in
            ProcessTableItem(row: row, tree: tree, protected: ProcessGuard.block(for: row.process) != nil,
                             killing: model.killing.contains(row.id))
        }
    }

    static func handle(_ key: DataTableKey, item: ProcessTableItem, model: ProcessesModel) -> Bool {
        switch key {
        case .delete:
            guard model.killBlock(pid: item.id, tree: false) == nil else { NSSound.beep(); return true }
            model.requestKill(pid: item.id, tree: false)
        case .deleteTree:
            guard model.killBlock(pid: item.id, tree: true) == nil else { NSSound.beep(); return true }
            model.requestKill(pid: item.id, tree: true)
        case .copy:
            ProcessMenu.copy(item.row.process)
        case .left:
            guard model.showsTree, item.row.hasChildren, item.row.isExpanded else { return false }
            model.toggleCollapsed(item.id)
        case .right:
            guard model.showsTree, item.row.hasChildren, !item.row.isExpanded else { return false }
            model.toggleCollapsed(item.id)
        case .open:
            return false
        }
        return true
    }

    @MainActor
    static func cell(_ item: ProcessTableItem, column: String, table: NSTableView, model: ProcessesModel) -> NSView {
        let process = item.row.process
        let style: DataTextCell.Style = item.row.isContext ? .tertiary : .secondary
        switch ProcessColumn(rawValue: column) ?? .name {
        case .name:
            let cell = table.makeView(withIdentifier: ProcessNameCell.identifier, owner: nil) as? ProcessNameCell ?? ProcessNameCell()
            cell.configure(item)
            cell.onToggle = { [weak model] in model?.toggleCollapsed(process.pid) }
            return cell
        case .pid:
            let cell = DataTextCell.make(table, "process.pid", alignment: .right, monospacedDigits: true)
            cell.configure(String(process.pid), style: style)
            return cell
        case .cpu:
            let cell = DataTextCell.make(table, "process.cpu", alignment: .right, monospacedDigits: true)
            if ProcessList.isIdle(process) {
                cell.configure(ProcessFormat.cpu(process.cpuPct), style: .tertiary,
                               help: String.ui("processes.idle.help", "쓰이지 않고 남아 있는 CPU 비율입니다. 정렬에서는 맨 뒤에 둡니다."))
            } else {
                let busy = (process.cpuPct ?? 0) >= 25
                cell.configure(ProcessFormat.cpu(process.cpuPct), style: item.row.isContext ? .tertiary : .primary,
                               weight: busy ? .semibold : .regular)
            }
            return cell
        case .memory:
            let cell = DataTextCell.make(table, "process.memory", alignment: .right, monospacedDigits: true)
            cell.configure(DS.Format.memory(kb: process.memKb), style: style)
            return cell
        case .user:
            let cell = DataTextCell.make(table, "process.user")
            cell.configure(ProcessFormat.user(process.user), style: style, help: process.user)
            return cell
        case .started:
            let cell = DataTextCell.make(table, "process.started", monospacedDigits: true)
            cell.configure(ProcessFormat.started(process.started?.date), style: style)
            return cell
        case .path:
            let cell = DataTextCell.make(table, "process.path", truncation: .byTruncatingMiddle)
            cell.configure(process.path ?? "—", style: item.row.isContext ? .tertiary : .secondary,
                           help: [process.path, process.cmd].compactMap { $0 }.joined(separator: "\n\n"))
            return cell
        }
    }
}

/// A row of the process table.
struct ProcessTableItem: Identifiable, Equatable {
    var row: ProcessRow
    var tree: Bool
    var protected: Bool
    var killing: Bool

    var id: Int { row.id }
}

/// The context menu and copy action of a process row.
@MainActor
enum ProcessMenu {
    static func entries(for process: RemoteProcess, model: ProcessesModel) -> [DataTableMenuEntry] {
        let pid = process.pid
        let single = model.killBlock(pid: pid, tree: false)
        let tree = model.killBlock(pid: pid, tree: true)
        let hasChildren = !model.forest.children(of: pid).isEmpty
        var entries = [
            DataTableMenuEntry(title: .ui("processes.kill", "종료…"), symbol: "xmark.octagon", key: "⌫",
                               isEnabled: single == nil && !model.killing.contains(pid), help: single?.reason) {
                model.requestKill(pid: pid, tree: false)
            },
            DataTableMenuEntry(title: .ui("processes.killTree", "트리 종료…"), symbol: "xmark.octagon.fill", key: "⌫",
                               modifiers: [.command, .option], isEnabled: hasChildren && tree == nil && !model.killing.contains(pid),
                               help: hasChildren ? tree?.reason : String.ui("processes.killTree.none", "하위 프로세스가 없습니다.")) {
                model.requestKill(pid: pid, tree: true)
            },
            .separator,
            DataTableMenuEntry(title: .ui("processes.copyLine", "프로세스 정보 복사"), symbol: "doc.on.doc", key: "c") {
                copy(process)
            },
        ]
        if let path = process.path {
            entries.append(DataTableMenuEntry(title: .ui("processes.copyPath", "경로 복사"), symbol: "folder") {
                put(path)
            })
        }
        if let cmd = process.cmd {
            entries.append(DataTableMenuEntry(title: .ui("processes.copyCommand", "명령줄 복사"), symbol: "terminal") {
                put(cmd)
            })
        }
        return entries
    }

    /// Copies `name · PID · path` and the command line.
    static func copy(_ process: RemoteProcess) {
        let pid = String(process.pid)
        var lines = ["\(process.name)  PID \(pid)"]
        if let path = process.path { lines.append(path) }
        if let cmd = process.cmd, cmd != process.path { lines.append(cmd) }
        put(lines.joined(separator: "\n"))
    }

    static func put(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }
}

/// Name cell: tree indentation and disclosure, the kind symbol, the name, the
/// `Deskhand가 시작함` tag (a dot when the name needs the room), and a lock or a spinner.
final class ProcessNameCell: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("process.name")
    static let indent: CGFloat = 16

    private let stack = NSStackView()
    private let disclosure = NSButton()
    private let icon = NSImageView()
    private let label = NSTextField(labelWithString: "")
    private let deskhandTag = TagView()
    private let lock = NSImageView()
    private let spinner = NSProgressIndicator()
    private var leading: NSLayoutConstraint?
    private var item: ProcessTableItem?
    var onToggle: (() -> Void)?

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        disclosure.bezelStyle = .disclosure
        disclosure.setButtonType(.pushOnPushOff)
        disclosure.title = ""
        disclosure.target = self
        disclosure.action = #selector(toggle(_:))
        disclosure.setAccessibilityLabel(String.ui("processes.a11y.disclosure", "하위 프로세스 펼치기"))
        icon.symbolConfiguration = NSImage.SymbolConfiguration(pointSize: 12, weight: .regular)
        icon.imageScaling = .scaleProportionallyDown
        label.lineBreakMode = .byTruncatingMiddle
        label.usesSingleLineMode = true
        label.font = .systemFont(ofSize: NSFont.systemFontSize)
        label.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(500), for: .horizontal)
        label.setContentHuggingPriority(.defaultLow, for: .horizontal)
        deskhandTag.text = String.ui("processes.tag.deskhand", "Deskhand가 시작함")
        deskhandTag.tint = NSColor(DS.Color.accent)
        deskhandTag.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(400), for: .horizontal)
        deskhandTag.setContentHuggingPriority(.required, for: .horizontal)
        lock.image = NSImage(systemSymbolName: "lock.fill", accessibilityDescription: String.ui("processes.a11y.protected", "보호된 프로세스"))
        lock.symbolConfiguration = NSImage.SymbolConfiguration(pointSize: 10, weight: .regular)
        lock.setContentCompressionResistancePriority(.required, for: .horizontal)
        spinner.style = .spinning
        spinner.controlSize = .small
        spinner.isDisplayedWhenStopped = false
        stack.orientation = .horizontal
        stack.alignment = .centerY
        stack.distribution = .fill
        stack.spacing = 5
        stack.setHuggingPriority(.defaultHigh, for: .horizontal)
        stack.translatesAutoresizingMaskIntoConstraints = false
        for view in [disclosure, icon, label, deskhandTag, lock, spinner] as [NSView] { stack.addArrangedSubview(view) }
        stack.setCustomSpacing(3, after: disclosure)
        stack.setCustomSpacing(6, after: label)
        addSubview(stack)
        imageView = icon
        textField = label
        let leading = stack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2)
        self.leading = leading
        NSLayoutConstraint.activate([
            leading,
            stack.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -2),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
            icon.widthAnchor.constraint(equalToConstant: 16),
            disclosure.widthAnchor.constraint(equalToConstant: 13),
            deskhandTag.widthAnchor.constraint(greaterThanOrEqualToConstant: TagView.dotWidth),
        ])
    }

    required init?(coder: NSCoder) { nil }

    func configure(_ item: ProcessTableItem) {
        self.item = item
        let row = item.row
        let process = row.process
        leading?.constant = 2 + (item.tree ? CGFloat(row.depth) * Self.indent : 0)
        disclosure.isHidden = !item.tree
        disclosure.alphaValue = row.hasChildren ? 1 : 0
        disclosure.isEnabled = row.hasChildren
        disclosure.state = row.isExpanded ? .on : .off
        disclosure.setAccessibilityLabel(row.isExpanded ? String.ui("processes.a11y.collapse", "하위 프로세스 접기")
                                                        : String.ui("processes.a11y.disclosure", "하위 프로세스 펼치기"))
        icon.image = NSImage(systemSymbolName: ProcessFormat.symbol(for: process), accessibilityDescription: nil)
        label.stringValue = process.name
        deskhandTag.isHidden = !process.startedByDeskhand
        lock.isHidden = !item.protected || item.killing
        spinner.isHidden = !item.killing
        if item.killing { spinner.startAnimation(nil) } else { spinner.stopAnimation(nil) }
        var tips = [process.name]
        if let block = ProcessGuard.block(for: process) { tips.append(block.reason) }
        if process.startedByDeskhand {
            tips.append(String.ui("processes.tag.deskhand.help", "Deskhand가 이 PC에서 실행한 명령·앱·셸이거나 그 하위 프로세스입니다."))
        }
        toolTip = tips.joined(separator: "\n")
        setAccessibilityLabel([process.name, process.startedByDeskhand ? deskhandTag.text : nil,
                               item.protected ? String.ui("processes.a11y.protected", "보호된 프로세스") : nil]
            .compactMap { $0 }.joined(separator: ", "))
        recolor()
    }

    @objc private func toggle(_ sender: NSButton) {
        onToggle?()
    }

    override var backgroundStyle: NSView.BackgroundStyle {
        didSet { recolor() }
    }

    private func recolor() {
        let emphasized = backgroundStyle == .emphasized
        let context = item?.row.isContext ?? false
        label.textColor = emphasized ? .alternateSelectedControlTextColor : (context ? .tertiaryLabelColor : .labelColor)
        icon.contentTintColor = emphasized ? .alternateSelectedControlTextColor : (context ? .tertiaryLabelColor : .secondaryLabelColor)
        lock.contentTintColor = emphasized ? .alternateSelectedControlTextColor : .tertiaryLabelColor
        deskhandTag.emphasized = emphasized
    }
}
