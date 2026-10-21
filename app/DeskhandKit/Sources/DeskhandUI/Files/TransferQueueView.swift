import DeskhandClient
import SwiftUI

extension TransferTone {
    /// The tone's hue, for symbols and progress.
    var color: Color {
        switch self {
        case .active: DS.Color.accent
        case .success: DS.Color.success
        case .warning: DS.Color.warning
        case .danger: DS.Color.danger
        case .neutral: DS.Color.neutral
        }
    }

    /// Text in the tone, legible on the window and on status tints.
    var textColor: Color {
        switch self {
        case .active: DS.Status.accentText
        case .success: DS.Status.successText
        case .warning: DS.Status.warningText
        case .danger: DS.Status.dangerText
        case .neutral: DS.Status.neutralText
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
                        .font(.system(.caption, weight: .bold))
                        .rotationEffect(.degrees(preferences.queueExpanded ? 180 : 0))
                        .foregroundStyle(.secondary)
                    Image(systemName: "arrow.up.arrow.down.circle")
                        .foregroundStyle(summary.active > 0 ? AnyShapeStyle(DS.Color.accent) : AnyShapeStyle(.secondary))
                    Text(String.ui("queue.title", "전송"))
                        .font(DS.Font.headline)
                    if !queue.visible.isEmpty {
                        Text(DS.Format.count(queue.visible.count))
                            .font(DS.Font.caption.weight(.semibold).monospacedDigit())
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, DS.Space.s)
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
                    .font(.system(.largeTitle))
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
                Text(String.ui("queue.empty", "전송 기록이 없습니다"))
                    .font(DS.Font.headline)
                Text(String.ui("queue.emptyMessage", "파일을 다른 패널로 끌어다 놓으면 여기에서 진행률과 결과를 확인할 수 있습니다. ⌘를 누른 채 놓으면 이동합니다."))
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
                .font(.system(.title))
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
                if let fix = row.fix {
                    Text(fix)
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if row.state == .failed || row.outcome == .failed, let code = transfer.error?.code {
                    ErrorCodeLine(code: code)
                }
                if !row.direction.isUpload, row.state == .done, model.overlay?.item(at: row.destinationPath) != nil {
                    Label(String.ui("files.preview.note", "미리보기에서는 이 Mac에 쓰지 않습니다. 받은 항목은 앱을 닫으면 사라집니다."),
                          systemImage: "eye")
                        .font(DS.Font.caption)
                        .foregroundStyle(DS.Status.warningText)
                }
                if let error = queue.actionErrors[transfer.transferID] {
                    Label(error.message, systemImage: "exclamationmark.octagon.fill")
                        .font(DS.Font.caption)
                        .foregroundStyle(DS.Status.dangerText)
                }
                if transfer.filesTotal > 1 { filesSection }
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
                    .foregroundStyle(DS.Status.warningText)
                    .padding(.horizontal, DS.Space.s)
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
                WorkingBar(tint: row.state == .verifying ? DS.Color.success : DS.Color.accent)
                    .accessibilityLabel(row.state == .verifying ? verifyingText(row) : String.ui("transfer.a11y.working", "진행 중"))
            }
            Text((row.state == .verifying ? [verifyingText(row), row.bytesText, row.filesText]
                  : [row.progress.map(DS.Format.percent), row.bytesText, row.rateText, row.etaText, row.filesText])
                .compactMap { $0 }.joined(separator: " · "))
                .font(DS.Font.caption.monospacedDigit())
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .accessibilityElement(children: .combine)
    }

    /// `원본 정리 중` for a move removing its source, `SHA-256 확인 중` otherwise.
    private func verifyingText(_ row: TransferPresentation) -> String {
        row.direction.isMove ? .ui("transfer.removingDetail", "원본 정리 중") : .ui("transfer.verifyingDetail", "SHA-256 확인 중")
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
            queue.toggleFiles(transfer.transferID)
        } label: {
            HStack(spacing: DS.Space.xs) {
                Image(systemName: "chevron.right")
                    .font(.system(size: 9, weight: .bold))
                    .rotationEffect(.degrees(isExpanded ? 90 : 0))
                Text(filesTitle)
            }
            .font(DS.Font.caption.weight(.medium))
            .foregroundStyle(DS.Status.accentText)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(filesTitle)
        .accessibilityValue(isExpanded ? String.ui("a11y.expanded", "펼침") : String.ui("a11y.collapsed", "접힘"))
        if isExpanded {
            Group {
                if let files = queue.files[transfer.transferID] {
                    LazyVStack(spacing: 0) {
                        ForEach(Array(Self.ordered(files).enumerated()), id: \.offset) { _, file in
                            TransferFileRow(file: file)
                        }
                    }
                } else if let error = queue.fileErrors[transfer.transferID], !queue.loadingFiles.contains(transfer.transferID) {
                    HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                        Label(String.sentences(String.ui("transfer.files.failed", "파일 목록을 읽지 못했습니다"), error.message),
                              systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(DS.Status.dangerText)
                            .fixedSize(horizontal: false, vertical: true)
                        Spacer(minLength: DS.Space.s)
                        Button(String.ui("action.retry", "다시 시도")) {
                            Task { await queue.loadFiles(transfer.transferID) }
                        }
                        .buttonStyle(.link)
                    }
                    .font(DS.Font.caption)
                    .padding(DS.Space.s)
                } else {
                    HStack(spacing: DS.Space.s) {
                        ProgressView().controlSize(.small)
                        Text(String.ui("transfer.files.loading", "파일 목록을 읽는 중…"))
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                    }
                    .padding(DS.Space.s)
                }
            }
            .padding(.vertical, DS.Space.xs)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(DS.Color.neutral.opacity(0.06), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        }
    }

    /// Failed files first, then files with another outcome than written, then the rest, each
    /// group in transfer order.
    static func ordered(_ files: [TransferFile]) -> [TransferFile] {
        func rank(_ file: TransferFile) -> Int {
            if file.transferState == .failed || file.transferOutcome == .failed { return 0 }
            if let outcome = file.transferOutcome, outcome != .written { return 1 }
            return 2
        }
        return files.enumerated().sorted { a, b in
            rank(a.element) != rank(b.element) ? rank(a.element) < rank(b.element) : a.offset < b.offset
        }.map(\.element)
    }

    private var filesTitle: String {
        let total = DS.Format.count(transfer.filesTotal)
        if transfer.filesFailed > 0 {
            let bad = DS.Format.count(transfer.filesFailed)
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
            .foregroundStyle(row.tone.textColor)
            .lineLimit(1)
            .fixedSize()
    }
}

/// One file of a transfer: its relative path, size, state and hash, and its error.
private struct TransferFileRow: View {
    let file: TransferFile

    var body: some View {
        let row = TransferFilePresentation(file)
        VStack(alignment: .leading, spacing: DS.Space.xs) {
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
                Text(DS.Format.bytes(file.size))
                    .font(DS.Font.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .frame(minWidth: 64, alignment: .trailing)
                    .fixedSize()
                Text(row.title)
                    .font(DS.Font.caption.weight(.semibold))
                    .foregroundStyle(row.tone.textColor)
                    .frame(minWidth: 52, alignment: .trailing)
                    .fixedSize()
            }
            if let message = row.errorMessage {
                VStack(alignment: .leading, spacing: DS.Space.xs) {
                    Text(message)
                        .foregroundStyle(row.tone == .danger ? DS.Status.dangerText : DS.Status.warningText)
                    if let fix = row.fix {
                        Text(fix)
                            .foregroundStyle(.secondary)
                    }
                    if let code = file.error?.code {
                        ErrorCodeLine(code: code)
                    }
                }
                .font(DS.Font.caption)
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

/// A full-width bar with moving stripes for work without a known fraction, such as hashing.
private struct WorkingBar: View {
    let tint: Color
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        TimelineView(.animation(paused: reduceMotion)) { context in
            Canvas { canvas, size in
                let rect = CGRect(origin: .zero, size: size)
                let track = Path(roundedRect: rect, cornerRadius: size.height / 2)
                canvas.fill(track, with: .color(tint.opacity(0.35)))
                canvas.clip(to: track)
                let stripe: CGFloat = 8
                let phase = reduceMotion ? 0 : CGFloat(context.date.timeIntervalSinceReferenceDate * 24)
                    .truncatingRemainder(dividingBy: stripe * 2)
                var x = -size.height - stripe * 2 + phase
                while x < size.width + size.height {
                    var path = Path()
                    path.move(to: CGPoint(x: x, y: size.height))
                    path.addLine(to: CGPoint(x: x + size.height, y: 0))
                    path.addLine(to: CGPoint(x: x + size.height + stripe, y: 0))
                    path.addLine(to: CGPoint(x: x + stripe, y: size.height))
                    path.closeSubpath()
                    canvas.fill(path, with: .color(tint.opacity(0.8)))
                    x += stripe * 2
                }
            }
        }
        .frame(height: 6)
    }
}
