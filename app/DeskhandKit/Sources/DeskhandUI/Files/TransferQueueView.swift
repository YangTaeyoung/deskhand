import DeskhandClient
import SwiftUI

extension TransferTone {
    var color: Color {
        switch self {
        case .active: DS.Color.accent
        case .success: DS.Color.success
        case .warning: DS.Color.warning
        case .danger: DS.Color.danger
        case .neutral: DS.Color.neutral
        }
    }
}

/// The transfer queue drawer at the bottom of the files tab. Its header always shows the
/// transfers in progress with their combined progress, speed and time left; expanded, it lists
/// every transfer with its files.
struct TransferQueueView: View {
    let model: FilesModel
    /// Tallest the expanded list may be.
    var maxHeight: CGFloat = 520
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var dragStart: Double?

    private var queue: TransferQueue { model.transfers }
    private var preferences: FilesPreferences { model.preferences }

    var body: some View {
        VStack(spacing: 0) {
            if preferences.queueExpanded { resizeHandle }
            header
            if preferences.queueExpanded {
                Divider()
                content
                    .frame(height: min(preferences.queueHeight, maxHeight))
                    .transition(.opacity)
            }
        }
        .background(DS.Color.surfaceRaised)
        .overlay(alignment: .top) {
            Rectangle().fill(DS.Color.separator).frame(height: 1)
        }
        .animation(reduceMotion ? nil : .snappy(duration: 0.2), value: preferences.queueExpanded)
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("queue.a11y", "전송 대기열"))
    }

    private var resizeHandle: some View {
        Color.clear
            .frame(height: 6)
            .contentShape(Rectangle())
            .onHover { inside in
                if inside { NSCursor.resizeUpDown.push() } else { NSCursor.pop() }
            }
            .gesture(DragGesture(minimumDistance: 1, coordinateSpace: .global)
                .onChanged { value in
                    let start = dragStart ?? min(preferences.queueHeight, Double(maxHeight))
                    dragStart = start
                    preferences.queueHeight = min(Double(maxHeight), max(120, start - value.translation.height))
                }
                .onEnded { _ in dragStart = nil })
            .accessibilityHidden(true)
    }

    private var header: some View {
        let summary = queue.summary
        return HStack(spacing: DS.Space.m) {
            Button {
                preferences.queueExpanded.toggle()
            } label: {
                HStack(spacing: DS.Space.s) {
                    Image(systemName: "chevron.up")
                        .font(.system(size: 10, weight: .bold))
                        .rotationEffect(.degrees(preferences.queueExpanded ? 180 : 0))
                        .foregroundStyle(.secondary)
                    Image(systemName: "arrow.up.arrow.down.circle")
                        .foregroundStyle(summary.active > 0 ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary))
                    Text(String.ui("queue.title", "전송"))
                        .font(DS.Font.headline)
                    if !queue.visible.isEmpty {
                        Text(FileFormat.count(queue.visible.count))
                            .font(DS.Font.caption.weight(.semibold).monospacedDigit())
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1)
                            .background(DS.Color.neutral.opacity(0.16), in: Capsule())
                    }
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(preferences.queueExpanded ? String.ui("queue.collapse", "전송 목록 접기")
                                                          : String.ui("queue.expand", "전송 목록 펼치기"))
            if let text = summary.text {
                Text(text)
                    .font(DS.Font.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
            } else if queue.loaded, !queue.visible.isEmpty {
                Text(String.ui("queue.idle", "진행 중인 전송 없음"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            } else if queue.loaded, !preferences.queueExpanded {
                Text(String.ui("queue.none", "전송 기록 없음"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            if let progress = summary.progress {
                ProgressView(value: progress)
                    .progressViewStyle(.linear)
                    .tint(DS.Color.accent)
                    .frame(maxWidth: 160)
                    .accessibilityLabel(String.ui("queue.a11y.progress", "전체 진행률"))
            }
            Spacer(minLength: DS.Space.s)
            if queue.hasFinished {
                Button(String.ui("queue.clear", "완료 항목 지우기")) { queue.clearFinished() }
                    .buttonStyle(.borderless)
                    .font(DS.Font.caption)
            }
        }
        .padding(.horizontal, DS.Space.l)
        .frame(height: 40)
    }

    @ViewBuilder
    private var content: some View {
        if !queue.loaded {
            HStack(spacing: DS.Space.s) {
                ProgressView().controlSize(.small)
                Text(String.ui("queue.loading", "전송 목록을 불러오는 중…"))
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let error = queue.loadError, queue.visible.isEmpty {
            VStack {
                ErrorBanner(message: String.ui("queue.loadFailed", "전송 목록을 불러오지 못했습니다"), fix: error.message, code: error.code) {
                    Task { await queue.reload() }
                }
                .frame(maxWidth: 520)
            }
            .padding(DS.Space.l)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if queue.visible.isEmpty {
            VStack(spacing: DS.Space.s) {
                Image(systemName: "tray")
                    .font(.system(size: 24))
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
                Text(String.ui("queue.empty", "전송 기록이 없습니다"))
                    .font(DS.Font.headline)
                Text(String.ui("queue.emptyMessage", "파일을 다른 패널로 끌어다 놓으면 여기에서 진행률과 결과를 확인할 수 있습니다. ⌥를 누른 채 놓으면 이동합니다."))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 420)
            }
            .accessibilityElement(children: .combine)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollView {
                LazyVStack(spacing: 0) {
                    ForEach(queue.visible, id: \.transferID) { transfer in
                        TransferRow(model: model, transfer: transfer)
                        Divider().padding(.leading, 56)
                    }
                }
            }
        }
    }
}

/// One transfer: direction, name, route, state, progress with speed and time left, the exact
/// outcome with its explanation, actions, and its files when expanded.
struct TransferRow: View {
    let model: FilesModel
    let transfer: Transfer

    private var queue: TransferQueue { model.transfers }
    private var row: TransferPresentation { TransferPresentation(transfer, hostName: model.sideName(.remote)) }
    private var isExpanded: Bool { queue.expanded.contains(transfer.transferID) }

    var body: some View {
        let row = self.row
        HStack(alignment: .top, spacing: DS.Space.m) {
            Image(systemName: row.direction.isUpload ? "arrow.up.circle.fill" : "arrow.down.circle.fill")
                .font(.system(size: 22))
                .foregroundStyle(row.tone == .neutral ? AnyShapeStyle(.secondary) : AnyShapeStyle(row.tone.color))
                .frame(width: 28)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                titleLine(row)
                routeLine(row)
                if !row.isFinished { progressLine(row) }
                if let explanation = row.explanation {
                    explanationLine(explanation, row: row)
                }
                if let error = queue.actionErrors[transfer.transferID] {
                    Label(error.message, systemImage: "exclamationmark.octagon.fill")
                        .font(DS.Font.caption)
                        .foregroundStyle(DS.Color.danger)
                }
                if transfer.files.count > 1 || hasFileDetail { filesSection }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            actions(row)
        }
        .padding(.horizontal, DS.Space.l)
        .padding(.vertical, DS.Space.m)
        .contentShape(Rectangle())
        .accessibilityElement(children: .contain)
        .accessibilityLabel(row.accessibilitySummary)
    }

    /// A single-file transfer whose file has its own error or a non-written outcome.
    private var hasFileDetail: Bool {
        guard let file = transfer.files.first, transfer.files.count == 1 else { return false }
        return file.error != nil && row.explanation?.contains(file.error ?? "") == false
    }

    private func titleLine(_ row: TransferPresentation) -> some View {
        HStack(spacing: DS.Space.s) {
            Text(row.name)
                .font(DS.Font.body.weight(.semibold))
                .lineLimit(1)
                .truncationMode(.middle)
                .help(row.name)
            if row.direction.isMove {
                Text(String.ui("files.drop.moveBadge", "이동"))
                    .font(DS.Font.caption.weight(.semibold))
                    .foregroundStyle(DS.Color.warning)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 1)
                    .overlay(Capsule().strokeBorder(DS.Color.warning.opacity(0.6), lineWidth: 1))
                    .help(String.ui("transfer.moveHelp", "대상에서 SHA-256을 확인한 뒤에만 원본을 지웁니다"))
            }
            Spacer(minLength: DS.Space.s)
            StateChip(row: row)
        }
    }

    private func routeLine(_ row: TransferPresentation) -> some View {
        HStack(spacing: DS.Space.xs) {
            Text(row.route)
                .lineLimit(1)
                .layoutPriority(1)
            Text("·")
                .accessibilityHidden(true)
            Text(row.destinationFolder)
                .font(DS.Font.mono)
                .lineLimit(1)
                .truncationMode(.middle)
                .help(row.destinationPath)
                .textSelection(.enabled)
        }
        .font(DS.Font.caption)
        .foregroundStyle(.secondary)
    }

    private func progressLine(_ row: TransferPresentation) -> some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            if let progress = row.progress, row.state != .verifying {
                ProgressView(value: progress)
                    .progressViewStyle(.linear)
                    .tint(row.state == .paused ? DS.Color.neutral : DS.Color.accent)
            } else {
                ProgressView()
                    .progressViewStyle(.linear)
                    .tint(DS.Color.accent)
            }
            Text([row.progress.map(FileFormat.percent), row.bytesText, row.rateText, row.etaText, row.filesText]
                .compactMap { $0 }.joined(separator: " · "))
                .font(DS.Font.caption.monospacedDigit())
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .accessibilityElement(children: .combine)
    }

    private func explanationLine(_ text: String, row: TransferPresentation) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.xs) {
            if row.tone == .warning || row.tone == .danger {
                Image(systemName: row.tone == .danger ? "xmark.octagon.fill" : "exclamationmark.triangle.fill")
                    .foregroundStyle(row.tone.color)
                    .accessibilityHidden(true)
            }
            Text(text)
                .foregroundStyle(row.tone == .danger || row.tone == .warning ? AnyShapeStyle(.primary) : AnyShapeStyle(.secondary))
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
        }
        .font(DS.Font.caption)
    }

    @ViewBuilder
    private var filesSection: some View {
        Button {
            if isExpanded {
                queue.expanded.remove(transfer.transferID)
            } else {
                queue.expanded.insert(transfer.transferID)
            }
        } label: {
            HStack(spacing: DS.Space.xs) {
                Image(systemName: "chevron.right")
                    .font(.system(size: 9, weight: .bold))
                    .rotationEffect(.degrees(isExpanded ? 90 : 0))
                Text(filesTitle)
            }
            .font(DS.Font.caption.weight(.medium))
            .foregroundStyle(DS.Color.accent)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(filesTitle)
        .accessibilityValue(isExpanded ? String.ui("a11y.expanded", "펼침") : String.ui("a11y.collapsed", "접힘"))
        if isExpanded {
            VStack(spacing: 0) {
                ForEach(Array(orderedFiles.enumerated()), id: \.offset) { _, file in
                    TransferFileRow(file: file)
                }
            }
            .padding(.vertical, DS.Space.xs)
            .background(DS.Color.neutral.opacity(0.06), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        }
    }

    /// Failed files first, then files with another outcome than written, then the rest, each
    /// group in transfer order.
    private var orderedFiles: [TransferFile] {
        func rank(_ file: TransferFile) -> Int {
            if file.transferState == .failed || file.transferOutcome == .failed { return 0 }
            if let outcome = file.transferOutcome, outcome != .written { return 1 }
            return 2
        }
        return transfer.files.enumerated().sorted { a, b in
            rank(a.element) != rank(b.element) ? rank(a.element) < rank(b.element) : a.offset < b.offset
        }.map(\.element)
    }

    private var filesTitle: String {
        let total = FileFormat.count(transfer.files.count)
        let failed = transfer.files.filter { $0.transferState == .failed || $0.transferOutcome == .failed }.count
        if failed > 0 {
            let bad = FileFormat.count(failed)
            return .ui("transfer.filesFailed", "파일 \(total)개 · 실패 \(bad)개")
        }
        return .ui("transfer.filesAll", "파일 \(total)개")
    }

    @ViewBuilder
    private func actions(_ row: TransferPresentation) -> some View {
        HStack(spacing: DS.Space.xs) {
            if queue.isRunning(transfer.transferID) {
                ProgressView().controlSize(.small)
            } else {
                ForEach(row.actions, id: \.self) { action in
                    actionButton(action)
                }
            }
        }
        .frame(minHeight: 22)
    }

    @ViewBuilder
    private func actionButton(_ action: TransferAction) -> some View {
        switch action {
        case .showDestination, .showSource:
            Button(action.title) {
                Task { await model.showLocation(action, for: transfer) }
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
        default:
            Button {
                queue.perform(action, on: transfer.transferID)
            } label: {
                Image(systemName: action.symbol)
                    .frame(width: 18, height: 18)
            }
            .buttonStyle(.borderless)
            .foregroundStyle(action == .dismiss ? AnyShapeStyle(.tertiary) : AnyShapeStyle(.secondary))
            .help(action.title)
            .accessibilityLabel(action.title)
        }
    }
}

/// State or outcome of a transfer as a symbol and a word in its tone.
private struct StateChip: View {
    let row: TransferPresentation

    private var symbol: String {
        switch row.state {
        case .queued: "clock"
        case .running: row.direction.isUpload ? "arrow.up" : "arrow.down"
        case .paused: "pause.circle.fill"
        case .verifying: "checkmark.seal"
        case .cancelled: "minus.circle"
        case .failed: row.tone == .warning ? "exclamationmark.triangle.fill" : "xmark.octagon.fill"
        case .done:
            switch row.tone {
            case .success: "checkmark.circle.fill"
            case .warning: "exclamationmark.triangle.fill"
            case .danger: "xmark.octagon.fill"
            default: "arrow.uturn.right.circle"
            }
        }
    }

    var body: some View {
        Label(row.headline, systemImage: symbol)
            .font(DS.Font.caption.weight(.semibold))
            .foregroundStyle(row.tone == .neutral ? AnyShapeStyle(.secondary) : AnyShapeStyle(row.tone.color))
            .lineLimit(1)
            .fixedSize()
    }
}

/// One file of a transfer: its relative path, size, state and hash, and its error.
private struct TransferFileRow: View {
    let file: TransferFile

    var body: some View {
        let row = TransferFilePresentation(file)
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: DS.Space.s) {
                Image(systemName: FileKind(name: file.rel, isDirectory: false).symbol)
                    .foregroundStyle(.secondary)
                    .frame(width: 16)
                    .accessibilityHidden(true)
                Text(file.rel)
                    .font(DS.Font.mono)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .help(file.rel)
                Spacer(minLength: DS.Space.s)
                if let hash = row.hashText {
                    Text(hash)
                        .font(DS.Font.caption.monospaced())
                        .foregroundStyle(.tertiary)
                        .lineLimit(1)
                        .fixedSize()
                }
                Text(FileFormat.size(file.size))
                    .font(DS.Font.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .frame(minWidth: 64, alignment: .trailing)
                    .fixedSize()
                if row.state == .running, let progress = row.progress {
                    ProgressView(value: progress)
                        .progressViewStyle(.linear)
                        .frame(width: 56)
                        .tint(DS.Color.accent)
                }
                Text(row.title)
                    .font(DS.Font.caption.weight(.semibold))
                    .foregroundStyle(row.tone == .neutral ? AnyShapeStyle(.secondary) : AnyShapeStyle(row.tone.color))
                    .frame(minWidth: 52, alignment: .trailing)
                    .fixedSize()
            }
            if let error = file.error, !error.isEmpty {
                Text(error)
                    .font(DS.Font.caption)
                    .foregroundStyle(row.tone == .danger ? DS.Color.danger : DS.Color.warning)
                    .padding(.leading, 24)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
            }
        }
        .padding(.horizontal, DS.Space.s)
        .padding(.vertical, DS.Space.xs)
        .accessibilityElement(children: .combine)
    }
}
