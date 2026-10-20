import DeskhandClient
import Foundation

/// Color family of a transfer or file state.
public enum TransferTone: Sendable, Equatable {
    /// In progress.
    case active
    case success
    /// Finished with something the person should look at.
    case warning
    case danger
    /// Paused, cancelled or skipped.
    case neutral
}

/// Buttons a transfer row offers.
public enum TransferAction: Sendable, Equatable, Hashable {
    case pause
    case resume
    case retry
    case cancel
    /// Hides a finished transfer from the queue.
    case dismiss
    /// Shows the destination: Finder for this Mac, the remote pane for the remote computer.
    case showDestination
    /// Shows the source a move could not delete.
    case showSource

    public var title: String {
        switch self {
        case .pause: .ui("transfer.action.pause", "일시정지")
        case .resume: .ui("transfer.action.resume", "재개")
        case .retry: .ui("transfer.action.retry", "다시 시도")
        case .cancel: .ui("transfer.action.cancel", "취소")
        case .dismiss: .ui("transfer.action.dismiss", "목록에서 지우기")
        case .showDestination: .ui("transfer.action.showDestination", "받은 위치 보기")
        case .showSource: .ui("transfer.action.showSource", "원본 보기")
        }
    }

    public var symbol: String {
        switch self {
        case .pause: "pause.fill"
        case .resume: "play.fill"
        case .retry: "arrow.clockwise"
        case .cancel: "xmark"
        case .dismiss: "xmark.circle.fill"
        case .showDestination: "arrow.right.circle"
        case .showSource: "arrow.uturn.backward"
        }
    }
}

/// How a transfer row reads: what moves where, its state, progress, speed, time left, and the
/// exact outcome with an explanation in Korean.
public struct TransferPresentation: Sendable, Equatable {
    public let transfer: Transfer
    public let hostName: String

    public init(_ transfer: Transfer, hostName: String) {
        self.transfer = transfer
        self.hostName = hostName
    }

    public var direction: TransferDirection { transfer.transferDirection }
    public var state: TransferState { transfer.transferState }
    public var outcome: TransferOutcome? { transfer.transferOutcome }

    private var remoteStyle: PathStyle { FilePath.windowsRoot(of: transfer.remote) != nil || transfer.remote.contains("\\") ? .windows : .posix }

    /// Path the data comes from.
    public var sourcePath: String { direction.isUpload ? transfer.local : transfer.remote }
    /// Path the data goes to.
    public var destinationPath: String { direction.isUpload ? transfer.remote : transfer.local }

    /// Name of the transferred file or folder.
    public var name: String {
        direction.isUpload
            ? FilePath.lastComponent(transfer.local, style: .posix)
            : FilePath.lastComponent(transfer.remote, style: remoteStyle)
    }

    /// Folder the item lands in.
    public var destinationFolder: String {
        direction.isUpload
            ? FilePath.parent(transfer.remote, style: remoteStyle) ?? transfer.remote
            : FilePath.parent(transfer.local, style: .posix) ?? transfer.local
    }

    /// Folder the item comes from.
    public var sourceFolder: String {
        direction.isUpload
            ? FilePath.parent(transfer.local, style: .posix) ?? transfer.local
            : FilePath.parent(transfer.remote, style: remoteStyle) ?? transfer.remote
    }

    private var macName: String { .ui("files.side.local", "이 Mac") }

    /// `이 Mac → 개발 PC`.
    public var route: String {
        let mac = macName
        let host = hostName
        return direction.isUpload ? .ui("transfer.route.up", "\(mac) → \(host)") : .ui("transfer.route.down", "\(host) → \(mac)")
    }

    /// Name of the computer the source is on.
    public var sourceSide: String { direction.isUpload ? macName : hostName }

    public var isFinished: Bool { state.isFinished }

    /// 0–1 while the size is known.
    public var progress: Double? {
        guard transfer.bytesTotal > 0 else { return state == .done ? 1 : nil }
        return min(1, Double(transfer.bytesDone) / Double(transfer.bytesTotal))
    }

    /// `243MB / 640MB`, or the total once done.
    public var bytesText: String {
        if state == .done || transfer.bytesTotal == 0 { return FileFormat.size(transfer.bytesTotal) }
        let done = FileFormat.size(transfer.bytesDone)
        let total = FileFormat.size(transfer.bytesTotal)
        return .ui("transfer.bytes", "\(done) / \(total)")
    }

    /// `파일 3/7개` for transfers of more than one file.
    public var filesText: String? {
        guard transfer.filesTotal > 1 else { return nil }
        let done = FileFormat.count(transfer.filesDone)
        let total = FileFormat.count(transfer.filesTotal)
        return .ui("transfer.files", "파일 \(done)/\(total)개")
    }

    /// `42.1MB/s` while bytes move. `rate_bps` is read as bytes per second.
    public var rateText: String? {
        guard state == .running, transfer.rateBps > 0 else { return nil }
        return FileFormat.rate(transfer.rateBps)
    }

    /// `약 9초 남음` while bytes move at a known rate.
    public var etaText: String? {
        guard state == .running else { return nil }
        guard transfer.rateBps > 0 else { return .ui("transfer.eta.unknown", "남은 시간 계산 중") }
        let remaining = Double(max(0, transfer.bytesTotal - transfer.bytesDone)) / Double(transfer.rateBps)
        let text = FileFormat.remaining(seconds: remaining)
        return .ui("transfer.eta", "\(text) 남음")
    }

    private var failedFiles: Int { transfer.files.filter { $0.transferState == .failed || $0.transferOutcome == .failed }.count }

    /// Short state or outcome: `보내는 중`, `복사됨 · 원본 남음`, `일부 완료`.
    public var headline: String {
        switch state {
        case .queued: return .ui("transfer.state.queued", "대기 중")
        case .running:
            if direction.isMove { return .ui("transfer.state.moving", "옮기는 중") }
            return direction.isUpload ? .ui("transfer.state.sending", "보내는 중") : .ui("transfer.state.receiving", "받는 중")
        case .paused: return .ui("transfer.state.paused", "일시정지됨")
        case .verifying: return .ui("transfer.state.verifying", "확인 중")
        case .cancelled: return .ui("transfer.state.cancelled", "취소됨")
        case .failed: return outcome == .partial ? Self.outcomeTitle(.partial) : .ui("transfer.state.failed", "실패")
        case .done: return outcome.map(Self.outcomeTitle) ?? Self.outcomeTitle(.written)
        }
    }

    public static func outcomeTitle(_ outcome: TransferOutcome) -> String {
        switch outcome {
        case .written: .ui("transfer.outcome.written", "완료")
        case .skipped: .ui("transfer.outcome.skipped", "건너뜀")
        case .renamed: .ui("transfer.outcome.renamed", "새 이름으로 저장")
        case .copiedSourceRetained: .ui("transfer.outcome.retained", "복사됨 · 원본 남음")
        case .partial: .ui("transfer.outcome.partial", "일부 완료")
        case .failed: .ui("transfer.outcome.failed", "실패")
        }
    }

    public var tone: TransferTone {
        switch state {
        case .queued, .running, .verifying: return .active
        case .paused, .cancelled: return .neutral
        case .failed: return outcome == .partial ? .warning : .danger
        case .done:
            switch outcome ?? .written {
            case .written, .renamed: return .success
            case .skipped: return .neutral
            case .copiedSourceRetained, .partial: return .warning
            case .failed: return .danger
            }
        }
    }

    /// What happened or is happening, in a sentence or two.
    public var explanation: String? {
        let side = sourceSide
        switch state {
        case .queued, .running:
            if direction.isMove {
                return .ui("transfer.explain.move", "이동: 대상에서 SHA-256을 확인한 뒤에만 \(side)의 원본을 지웁니다.")
            }
            return nil
        case .verifying:
            return .ui("transfer.explain.verifying", "대상에 쓴 파일의 SHA-256을 원본과 비교하고 있습니다.")
        case .paused:
            let resume = direction.isUpload
                ? String.ui("transfer.explain.resumeUp", "재개하면 확인된 부분 다음부터 이어서 보냅니다.")
                : String.ui("transfer.explain.resumeDown", "재개하면 확인된 부분 다음부터 이어서 받습니다.")
            if let error = transfer.error, !error.isEmpty {
                return .ui("transfer.explain.pausedBecause", "\(error) \(resume)")
            }
            return resume
        case .cancelled:
            if transfer.filesDone > 0 {
                let done = FileFormat.count(transfer.filesDone)
                return .ui("transfer.explain.cancelledKept", "취소했습니다. 끝난 파일 \(done)개는 대상에 그대로 있습니다.")
            }
            return .ui("transfer.explain.cancelled", "취소했습니다. 대상에 반영된 파일은 없습니다.")
        case .failed:
            if outcome == .partial { return partialExplanation }
            return transfer.error ?? .ui("transfer.explain.failed", "전송하지 못했습니다.")
        case .done:
            switch outcome ?? .written {
            case .written:
                if direction.isMove {
                    return .ui("transfer.explain.moved", "대상에서 SHA-256을 확인한 뒤 \(side)의 원본을 지웠습니다.")
                }
                return .ui("transfer.explain.written", "대상에 쓰고 SHA-256으로 확인했습니다.")
            case .skipped:
                return .ui("transfer.explain.skipped", "대상에 같은 이름이 있어 보내지 않았습니다.")
            case .renamed:
                return .ui("transfer.explain.renamed", "대상에 같은 이름이 있어 다른 이름으로 저장했습니다.")
            case .copiedSourceRetained:
                let base = String.ui("transfer.explain.retained", "대상에 사본을 쓰고 SHA-256으로 확인했지만 원본을 지우지 못했습니다. 원본은 \(side)에 그대로 있습니다.")
                guard let error = transfer.error, !error.isEmpty else { return base }
                return .ui("transfer.explain.reason", "\(base)\n이유: \(error)")
            case .partial:
                return partialExplanation
            case .failed:
                return transfer.error ?? .ui("transfer.explain.failed", "전송하지 못했습니다.")
            }
        }
    }

    private var partialExplanation: String {
        let failed = max(failedFiles, transfer.filesTotal - transfer.filesDone)
        let total = FileFormat.count(transfer.filesTotal)
        let ok = FileFormat.count(max(0, transfer.filesTotal - failed))
        let bad = FileFormat.count(failed)
        if direction.isMove {
            return .ui("transfer.explain.partialMove", "\(total)개 중 \(ok)개를 옮겼고 \(bad)개는 실패했습니다. 실패한 파일의 원본은 지우지 않았습니다.")
        }
        return .ui("transfer.explain.partial", "\(total)개 중 \(ok)개를 보냈고 \(bad)개는 실패했습니다. 실패한 파일은 아래 목록에 이유가 있습니다.")
    }

    /// Buttons for the current state, most important first.
    public var actions: [TransferAction] {
        switch state {
        case .queued, .running: return [.pause, .cancel]
        case .verifying: return [.cancel]
        case .paused: return [.resume, .cancel]
        case .failed: return outcome == .partial ? [.retry, .showDestination, .dismiss] : [.retry, .dismiss]
        case .cancelled: return [.resume, .dismiss]
        case .done:
            if outcome == .copiedSourceRetained { return [.showSource, .showDestination, .dismiss] }
            if outcome == .skipped { return [.dismiss] }
            return [.showDestination, .dismiss]
        }
    }

    /// One sentence for VoiceOver.
    public var accessibilitySummary: String {
        var parts = [name, route, headline]
        if let progress, !isFinished { parts.append(FileFormat.percent(progress)) }
        if let rateText { parts.append(rateText) }
        if let etaText { parts.append(etaText) }
        return parts.joined(separator: ", ")
    }
}

/// How one file of a transfer reads.
public struct TransferFilePresentation: Sendable, Equatable {
    public let file: TransferFile

    public init(_ file: TransferFile) {
        self.file = file
    }

    public var state: TransferState { file.transferState }

    public var progress: Double? {
        guard file.size > 0 else { return nil }
        return min(1, Double(file.bytesDone) / Double(file.size))
    }

    public var title: String {
        switch state {
        case .queued: return .ui("transferFile.queued", "대기")
        case .running:
            return FileFormat.percent(progress ?? 0)
        case .paused: return .ui("transferFile.paused", "일시정지")
        case .verifying: return .ui("transferFile.verifying", "확인 중")
        case .cancelled: return .ui("transferFile.cancelled", "취소됨")
        case .failed: return .ui("transferFile.failed", "실패")
        case .done:
            switch file.transferOutcome ?? .written {
            case .written: return .ui("transferFile.written", "완료")
            case .skipped: return .ui("transferFile.skipped", "건너뜀")
            case .renamed: return .ui("transferFile.renamed", "새 이름")
            case .copiedSourceRetained: return .ui("transferFile.retained", "원본 남음")
            case .partial: return .ui("transferFile.partial", "일부")
            case .failed: return .ui("transferFile.failed", "실패")
            }
        }
    }

    public var tone: TransferTone {
        switch state {
        case .queued, .running, .verifying: return .active
        case .paused, .cancelled: return .neutral
        case .failed: return .danger
        case .done:
            switch file.transferOutcome ?? .written {
            case .written, .renamed: return .success
            case .skipped: return .neutral
            case .copiedSourceRetained, .partial: return .warning
            case .failed: return .danger
            }
        }
    }

    /// `SHA-256 9c1f07d2…` once verified.
    public var hashText: String? {
        guard let sha = file.sha256, !sha.isEmpty else { return nil }
        let short = String(sha.prefix(8))
        return .ui("transferFile.sha", "SHA-256 \(short)…")
    }
}

/// Totals over the transfers still in progress, for the drawer header.
public struct TransferSummary: Sendable, Equatable {
    public var active = 0
    public var paused = 0
    public var failed = 0
    public var bytesDone = 0
    public var bytesTotal = 0
    public var rate = 0

    public init(_ transfers: [Transfer]) {
        for t in transfers {
            switch t.transferState {
            case .queued, .running, .verifying:
                active += 1
                bytesDone += t.bytesDone
                bytesTotal += t.bytesTotal
                if t.transferState == .running { rate += t.rateBps }
            case .paused:
                paused += 1
            case .failed:
                failed += 1
            case .done:
                if t.transferOutcome == .partial || t.transferOutcome == .copiedSourceRetained || t.transferOutcome == .failed {
                    failed += 1
                }
            case .cancelled:
                break
            }
        }
    }

    public var progress: Double? {
        guard bytesTotal > 0 else { return nil }
        return min(1, Double(bytesDone) / Double(bytesTotal))
    }

    /// `2개 진행 중 · 42.1MB/s · 약 1분 남음`, `일시정지 1개`, or nil with nothing to report.
    public var text: String? {
        var parts: [String] = []
        if active > 0 {
            let count = FileFormat.count(active)
            parts.append(.ui("queue.summary.active", "\(count)개 진행 중"))
            if rate > 0 {
                parts.append(FileFormat.rate(rate))
                let eta = FileFormat.remaining(seconds: Double(max(0, bytesTotal - bytesDone)) / Double(rate))
                parts.append(.ui("transfer.eta", "\(eta) 남음"))
            }
        }
        if paused > 0 {
            let count = FileFormat.count(paused)
            parts.append(.ui("queue.summary.paused", "일시정지 \(count)개"))
        }
        if failed > 0 {
            let count = FileFormat.count(failed)
            parts.append(.ui("queue.summary.attention", "확인 필요 \(count)개"))
        }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}
