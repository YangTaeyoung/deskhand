import DeskhandClient
import SwiftUI

/// Asks what to do with an item whose destination already has one of the same name: replace,
/// keep both, skip, or stop. With more conflicts waiting, the choice can apply to all of them.
public struct ConflictSheet: View {
    let prompt: ConflictPrompt
    let resolve: (ConflictPolicy, Bool) -> Void
    @State private var applyToAll = false

    public init(prompt: ConflictPrompt, resolve: @escaping (ConflictPolicy, Bool) -> Void) {
        self.prompt = prompt
        self.resolve = resolve
    }

    public var body: some View {
        if let item = prompt.current {
            content(item)
        }
    }

    private func content(_ item: ConflictItem) -> some View {
        let candidate = item.candidate
        let remaining = prompt.items.count
        return VStack(alignment: .leading, spacing: DS.Space.l) {
            HStack(alignment: .top, spacing: DS.Space.m) {
                Image(systemName: candidate.isDirectory ? "folder.fill" : "doc.on.doc.fill")
                    .font(.system(size: 30))
                    .foregroundStyle(DS.Color.warning)
                    .frame(width: 40)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    Text(title(candidate))
                        .font(DS.Font.title)
                        .lineLimit(3)
                        .truncationMode(.middle)
                        .fixedSize(horizontal: false, vertical: true)
                        .help(candidate.name)
                        .accessibilityAddTraits(.isHeader)
                    Text(subtitle)
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            comparison(item)
            if candidate.isDirectory {
                Label(String.ui("conflict.folderNote", "폴더는 안의 파일마다 같은 이름이 있을 때 이 선택을 적용합니다."), systemImage: "info.circle")
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                legend(String.ui("conflict.replace", "바꾸기"), String.ui("conflict.replace.detail", "새 사본을 SHA-256으로 확인한 뒤 기존 항목을 바꿉니다."))
                legend(String.ui("conflict.keepBoth", "둘 다 유지"), keepBothDetail(candidate))
                legend(String.ui("conflict.skip", "건너뛰기"), String.ui("conflict.skip.detail", "이 항목은 보내지 않고 기록만 남깁니다."))
                legend(String.ui("conflict.stop", "중단"), String.ui("conflict.stop.detail", "이 항목과 남은 충돌 항목을 모두 보내지 않습니다."))
            }
            if remaining > 1 {
                let others = FileFormat.count(remaining - 1)
                Toggle(String.ui("conflict.applyToAll", "남은 충돌 \(others)개에도 같은 선택 적용"), isOn: $applyToAll)
                    .toggleStyle(.checkbox)
            }
            HStack(spacing: DS.Space.s) {
                Button(String.ui("conflict.stop", "중단")) { resolve(.fail, applyToAll) }
                    .keyboardShortcut(.cancelAction)
                if remaining > 1 {
                    let total = FileFormat.count(remaining)
                    Text(String.ui("conflict.remaining", "충돌 \(total)개 남음"))
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: DS.Space.s)
                Button(String.ui("conflict.skip", "건너뛰기")) { resolve(.skip, applyToAll) }
                Button(String.ui("conflict.replace", "바꾸기")) { resolve(.replace, applyToAll) }
                Button(String.ui("conflict.keepBoth", "둘 다 유지")) { resolve(.rename, applyToAll) }
                    .keyboardShortcut(.defaultAction)
            }
            .controlSize(.large)
        }
        .padding(DS.Space.xl)
        .frame(width: 560)
        .interactiveDismissDisabled()
    }

    private func title(_ candidate: TransferCandidate) -> String {
        let name = candidate.name
        return candidate.isDirectory
            ? .ui("conflict.title.folder", "‘\(name)’ 폴더가 이미 있습니다")
            : .ui("conflict.title.file", "‘\(name)’ 파일이 이미 있습니다")
    }

    private var subtitle: String {
        let place = prompt.destinationName
        let folder = FilePath.lastComponent(prompt.folder, style: prompt.direction.isUpload ? PathStyle.windows : .posix)
        return .ui("conflict.subtitle", "\(place)의 ‘\(folder)’ 폴더에 같은 이름의 항목이 있습니다. 어떻게 할까요?")
    }

    private func keepBothDetail(_ candidate: TransferCandidate) -> String {
        let example = FileNameRule.keepBothName(candidate.name)
        return .ui("conflict.keepBoth.detail", "보내는 항목을 ‘\(example)’처럼 다른 이름으로 저장합니다.")
    }

    private func legend(_ title: String, _ detail: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Text(title)
                .font(DS.Font.caption.weight(.semibold))
                .frame(width: 72, alignment: .leading)
            Text(detail)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .combine)
    }

    private func comparison(_ item: ConflictItem) -> some View {
        let incoming = item.candidate
        let existing = item.existing
        let incomingNewer = (incoming.modified ?? .distantPast) > (existing.modified ?? .distantPast)
        let source = prompt.sourceName
        return HStack(alignment: .top, spacing: DS.Space.m) {
            ConflictSide(title: String.ui("conflict.incoming", "보내는 항목"), place: source,
                         size: incoming.isDirectory ? nil : incoming.size, modified: incoming.modified, newer: incomingNewer && existing.modified != nil)
            Image(systemName: "arrow.right")
                .foregroundStyle(.tertiary)
                .padding(.top, DS.Space.l)
                .accessibilityHidden(true)
            ConflictSide(title: String.ui("conflict.existing", "이미 있는 항목"), place: prompt.destinationName,
                         size: existing.isDirectory ? nil : existing.size, modified: existing.modified,
                         newer: !incomingNewer && incoming.modified != nil && existing.modified != nil)
        }
    }
}

/// One side of the conflict comparison: where it is, its size and modification time.
private struct ConflictSide: View {
    let title: String
    let place: String
    let size: Int?
    let modified: Date?
    let newer: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            HStack(spacing: DS.Space.xs) {
                Text(title)
                    .font(DS.Font.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                if newer {
                    Text(String.ui("conflict.newer", "더 최근"))
                        .font(DS.Font.caption.weight(.semibold))
                        .foregroundStyle(DS.Color.accent)
                        .padding(.horizontal, 6)
                        .background(DS.Color.accent.opacity(0.12), in: Capsule())
                }
            }
            Text(place)
                .font(DS.Font.headline)
                .lineLimit(1)
            Text(size.map(FileFormat.size) ?? String.ui("kind.folder", "폴더"))
                .font(DS.Font.body.monospacedDigit())
            Text(modified.map { FileFormat.date($0) } ?? "—")
                .font(DS.Font.caption.monospacedDigit())
                .foregroundStyle(.secondary)
        }
        .padding(DS.Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DS.Color.neutral.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .accessibilityElement(children: .combine)
    }
}

/// Details of one item: kind, exact size, location, modification time and attributes, with
/// copy path and, for this Mac, Finder.
struct FileInfoSheet: View {
    let state: FileInfoState
    let sideName: String
    let copyPath: () -> Void
    let reveal: (() -> Void)?
    let close: () -> Void

    var body: some View {
        let item = state.item
        VStack(alignment: .leading, spacing: DS.Space.l) {
            HStack(alignment: .top, spacing: DS.Space.m) {
                Image(systemName: item.fileKind.symbol)
                    .font(.system(size: 30))
                    .foregroundStyle(item.isFolder ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary))
                    .frame(width: 40)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    Text(item.name)
                        .font(DS.Font.title)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityAddTraits(.isHeader)
                    Text(item.kind)
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
                if state.isLoading {
                    ProgressView().controlSize(.small)
                        .accessibilityLabel(String.ui("info.loading", "최신 정보를 읽는 중"))
                }
            }
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.s) {
                row(String.ui("info.computer", "컴퓨터"), sideName)
                row(String.ui("info.location", "위치"), FilePath.parent(item.path, style: item.path.contains("\\") ? .windows : .posix) ?? item.path, mono: true)
                row(String.ui("info.size", "크기"), item.size.map { "\(FileFormat.size($0)) (\(FileFormat.exactBytes($0)))" } ?? "—")
                row(String.ui("info.modified", "수정한 날짜"), item.modified.map {
                    $0.formatted(.dateTime.year().month().day().hour().minute().second().locale(Locale(identifier: "ko_KR")))
                } ?? "—")
                row(String.ui("info.attributes", "속성"), attributes(item))
            }
            if let error = state.error {
                Label(String.ui("info.statFailed", "최신 정보를 읽지 못해 목록의 정보를 보여 줍니다: \(error.message)"),
                      systemImage: "exclamationmark.triangle.fill")
                    .font(DS.Font.caption)
                    .foregroundStyle(DS.Color.warning)
            }
            HStack(spacing: DS.Space.s) {
                Button(String.ui("files.menu.copyPath", "경로 복사"), action: copyPath)
                if let reveal {
                    Button(String.ui("files.menu.showInFinder", "Finder에서 보기"), action: reveal)
                }
                Spacer()
                Button(String.ui("action.close", "닫기"), action: close)
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(DS.Space.xl)
        .frame(width: 460)
    }

    private func attributes(_ item: FileItem) -> String {
        var parts: [String] = []
        if item.isReadOnly { parts.append(.ui("info.readOnly", "읽기 전용")) }
        if item.isHidden { parts.append(.ui("info.hidden", "숨김")) }
        if item.isLink { parts.append(.ui("info.link", "링크")) }
        return parts.isEmpty ? String.ui("info.none", "없음") : parts.joined(separator: " · ")
    }

    private func row(_ key: String, _ value: String, mono: Bool = false) -> some View {
        GridRow {
            Text(key)
                .foregroundStyle(.secondary)
            Text(value)
                .font(mono ? DS.Font.mono : DS.Font.body)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .accessibilityElement(children: .combine)
    }
}

/// Confirms a move from a menu or the toolbar: the sources are deleted only after the copies
/// are verified. "다시 묻지 않기" turns the confirmation off.
public struct MoveConfirmSheet: View {
    let request: MoveRequest
    let confirm: (Bool) -> Void
    let cancel: () -> Void
    @State private var dontAskAgain = false

    public init(request: MoveRequest, confirm: @escaping (Bool) -> Void, cancel: @escaping () -> Void) {
        self.request = request
        self.confirm = confirm
        self.cancel = cancel
    }

    private var title: String {
        if request.candidates.count == 1, let name = request.candidates.first?.name {
            return .ui("move.title.one", "‘\(name)’ 항목을 이동할까요?")
        }
        let count = FileFormat.count(request.candidates.count)
        return .ui("move.title.many", "\(count)개 항목을 이동할까요?")
    }

    public var body: some View {
        let source = request.sourceName
        let destination = request.destinationName
        VStack(alignment: .leading, spacing: DS.Space.l) {
            HStack(alignment: .top, spacing: DS.Space.m) {
                Image(systemName: request.direction.isUpload ? "arrow.right.circle.fill" : "arrow.left.circle.fill")
                    .font(.system(size: 30))
                    .foregroundStyle(DS.Color.warning)
                    .frame(width: 40)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.s) {
                    Text(title)
                        .font(DS.Font.title)
                        .lineLimit(3)
                        .truncationMode(.middle)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityAddTraits(.isHeader)
                    Text(String.ui("move.message", "\(destination)에 복사하고 SHA-256으로 확인한 뒤에만 \(source)의 원본을 지웁니다. 원본을 지우지 못하면 원본은 그대로 두고 알려 드립니다."))
                        .font(DS.Font.body)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            HStack {
                Toggle(String.ui("move.dontAsk", "다시 묻지 않기"), isOn: $dontAskAgain)
                    .toggleStyle(.checkbox)
                Spacer()
                Button(String.ui("action.cancel", "취소"), action: cancel)
                    .keyboardShortcut(.cancelAction)
                Button(String.ui("move.confirm", "이동")) { confirm(dontAskAgain) }
                    .keyboardShortcut(.defaultAction)
            }
            .controlSize(.large)
        }
        .padding(DS.Space.xl)
        .frame(width: 480)
    }
}
