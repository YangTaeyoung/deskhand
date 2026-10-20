import AppKit
import DeskhandClient
import SwiftUI

extension CrashDumpKind {
    public var title: String {
        switch self {
        case .minidump: .ui("logs.dump.minidump", "미니 덤프")
        case .full: .ui("logs.dump.full", "전체 덤프")
        case .report: .ui("logs.dump.report", "오류 보고서")
        }
    }

    public var symbol: String {
        switch self {
        case .minidump, .full: "ladybug"
        case .report: "doc.text"
        }
    }
}

/// The crash dumps of the last 30 days, newest first: app, kind, time, size and path, each
/// with a download into `~/Downloads/Deskhand` through the transfer queue and, once received,
/// Finder에서 보기.
struct CrashDumpList: View {
    let model: LogsModel
    let hostName: String

    var body: some View {
        switch model.crashesPhase {
        case .idle, .loading:
            VStack(spacing: DS.Space.m) {
                ProgressView()
                Text(String.ui("logs.dumps.loading", "크래시 덤프를 찾는 중…"))
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .accessibilityElement(children: .combine)
        case .failed(let error):
            ErrorBanner(message: String.ui("logs.dumps.failed", "크래시 덤프 목록을 불러오지 못했습니다"),
                        fix: LogsModel.explanation(error), code: error.code) {
                Task { await model.reloadCrashes() }
            }
            .frame(maxWidth: 520)
            .padding(DS.Space.xl)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .loaded where model.crashes.isEmpty:
            EmptyStateView(symbol: "checkmark.shield", title: String.ui("logs.dumps.none", "크래시 덤프가 없습니다"),
                           message: String.ui("logs.dumps.noneMessage", "최근 30일 동안 \(hostName)에서 앱이 비정상 종료한 기록이 없습니다."))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .loaded:
            ScrollView {
                VStack(alignment: .leading, spacing: DS.Space.m) {
                    header
                    VStack(spacing: 0) {
                        ForEach(Array(model.crashes.enumerated()), id: \.element.id) { index, dump in
                            if index > 0 { Divider().padding(.leading, 52) }
                            CrashDumpRow(model: model, dump: dump)
                        }
                    }
                    .background(DS.Color.surfaceRaised, in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
                    .overlay(
                        RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                            .strokeBorder(DS.Color.separator, lineWidth: 1)
                    )
                }
                .padding(DS.Space.l)
            }
        }
    }

    private var header: some View {
        let count = FileFormat.count(model.crashes.count)
        let folder = (LogsModel.downloadFolder as NSString).abbreviatingWithTildeInPath
        return HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
            Text(String.ui("logs.dumps.title", "최근 30일 · \(count)개"))
                .font(DS.Font.headline)
            Spacer(minLength: DS.Space.s)
            Label(String.ui("logs.dumps.folder", "받는 위치 \(folder)"), systemImage: "arrow.down.circle")
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.middle)
        }
    }
}

/// One crash dump with its download state.
private struct CrashDumpRow: View {
    let model: LogsModel
    let dump: CrashDump
    @State private var hovering = false

    var body: some View {
        let kind = dump.dumpKind
        let transfer = model.transfer(for: dump)
        let startError = model.downloadErrors[dump.path]
        HStack(alignment: .center, spacing: DS.Space.m) {
            Image(systemName: kind?.symbol ?? "doc")
                .font(.system(size: 18))
                .foregroundStyle(DS.Color.danger.opacity(kind == .report ? 0.7 : 0.9))
                .frame(width: 28)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: DS.Space.s) {
                    Text(dump.app)
                        .font(DS.Font.headline)
                        .lineLimit(1)
                    Text(kind?.title ?? dump.kind)
                        .font(DS.Font.caption.weight(.medium))
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 6)
                        .frame(height: 18)
                        .background(DS.Color.neutral.opacity(0.14), in: Capsule())
                }
                Text(dump.path)
                    .font(DS.Font.mono)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .textSelection(.enabled)
                    .help(dump.path)
                if let message = errorText(transfer: transfer, startError: startError) {
                    Text(message)
                        .font(DS.Font.caption)
                        .foregroundStyle(DS.Color.danger)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            VStack(alignment: .trailing, spacing: 3) {
                Text(ProcessFormat.started(dump.time.date))
                    .font(DS.Font.caption.monospacedDigit())
                Text(SummaryFormat.storage(dump.sizeBytes))
                    .font(DS.Font.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            .frame(minWidth: 112, alignment: .trailing)
            DownloadControl(model: model, dump: dump, transfer: transfer, starting: model.downloadStarting.contains(dump.path))
                .frame(width: 196, alignment: .trailing)
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s + 2)
        .background(hovering ? DS.Color.neutral.opacity(0.06) : .clear)
        .onHover { hovering = $0 }
        .contextMenu {
            Button(String.ui("logs.dump.copyPath", "원격 경로 복사")) { ProcessMenu.put(dump.path) }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(String.ui("logs.a11y.dump", "\(dump.app), \(kind?.title ?? dump.kind), \(ProcessFormat.started(dump.time.date)), \(SummaryFormat.storage(dump.sizeBytes))"))
    }

    private func errorText(transfer: Transfer?, startError: OperationError?) -> String? {
        if let startError { return [startError.message, startError.fix].compactMap { $0 }.joined(separator: " ") }
        guard let transfer, transfer.transferState == .failed else { return nil }
        let presentation = TransferPresentation(transfer, hostName: "")
        return [presentation.errorMessage, presentation.fix].compactMap { $0 }.joined(separator: " ")
    }
}

/// 다운로드, the progress of a pull with cancel, and once received Finder에서 보기; failed and
/// cancelled pulls offer 다시 받기.
private struct DownloadControl: View {
    let model: LogsModel
    let dump: CrashDump
    let transfer: Transfer?
    let starting: Bool

    var body: some View {
        if starting {
            HStack(spacing: DS.Space.s) {
                ProgressView().controlSize(.small)
                Text(String.ui("logs.download.starting", "시작하는 중…"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
        } else if let transfer {
            state(transfer)
        } else {
            downloadButton(String.ui("logs.download", "다운로드"))
        }
    }

    @ViewBuilder
    private func state(_ transfer: Transfer) -> some View {
        let presentation = TransferPresentation(transfer, hostName: "")
        switch transfer.transferState {
        case .queued, .running, .verifying, .paused:
            HStack(spacing: DS.Space.s) {
                VStack(alignment: .trailing, spacing: 3) {
                    ProgressView(value: presentation.progress ?? 0)
                        .progressViewStyle(.linear)
                        .frame(width: 120)
                    Text(progressText(presentation))
                        .font(DS.Font.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                if transfer.transferState == .paused {
                    Button {
                        model.perform(.resume, on: transfer)
                    } label: {
                        Image(systemName: "play.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                    .help(String.ui("logs.download.resume", "이어 받기"))
                    .accessibilityLabel(String.ui("logs.download.resume", "이어 받기"))
                }
                Button {
                    model.perform(.cancel, on: transfer)
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
                .help(String.ui("logs.download.cancel", "받기 취소"))
                .accessibilityLabel(String.ui("logs.download.cancel", "받기 취소"))
            }
        case .done:
            HStack(spacing: DS.Space.s) {
                Label(String.ui("logs.download.done", "받음"), systemImage: "checkmark.circle.fill")
                    .font(DS.Font.caption.weight(.medium))
                    .foregroundStyle(DS.Color.success)
                    .labelStyle(.titleAndIcon)
                Button(String.ui("logs.download.reveal", "Finder에서 보기")) { model.reveal(transfer) }
                    .controlSize(.small)
                    .help(LogsModel.savedPath(of: transfer))
            }
        case .failed, .cancelled:
            HStack(spacing: DS.Space.s) {
                Text(transfer.transferState == .failed ? String.ui("logs.download.failed", "받지 못함")
                                                       : String.ui("logs.download.cancelled", "취소됨"))
                    .font(DS.Font.caption.weight(.medium))
                    .foregroundStyle(transfer.transferState == .failed ? DS.Color.danger : .secondary)
                downloadButton(String.ui("logs.download.again", "다시 받기"))
            }
        }
    }

    private func downloadButton(_ title: String) -> some View {
        Button {
            Task { await model.download(dump) }
        } label: {
            Label(title, systemImage: "arrow.down.circle")
        }
        .controlSize(.small)
        .help(String.ui("logs.download.help", "~/Downloads/Deskhand로 받습니다. 같은 이름이 있으면 새 이름으로 저장합니다."))
    }

    private func progressText(_ presentation: TransferPresentation) -> String {
        let percent = FileFormat.percent(presentation.progress ?? 0)
        switch presentation.state {
        case .verifying: return .ui("logs.download.verifying", "확인 중")
        case .paused: return .ui("logs.download.paused", "\(percent) · 일시정지됨")
        case .queued: return .ui("logs.download.queued", "대기 중")
        default:
            if let rate = presentation.rateText { return .ui("logs.download.rate", "\(percent) · \(rate)") }
            return percent
        }
    }
}
