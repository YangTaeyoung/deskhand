import DeskhandClient
import SwiftUI

extension TimelineEntry {
    /// Commands and paths are shown in the monospaced font.
    var isCommandLike: Bool { kind == "exec" || kind == "file" }

    var actorTitle: String {
        switch actor {
        case "human": .ui("actor.human", "사람")
        case "ai": .ui("actor.ai", "AI")
        default: .ui("actor.system", "시스템")
        }
    }

    var actorSymbol: String {
        switch actor {
        case "human": ControlOwner.human.symbol
        case "ai": ControlOwner.ai.symbol
        default: "gearshape.fill"
        }
    }

    var actorTint: Color {
        switch actor {
        case "human": DS.Color.human
        case "ai": DS.Color.ai
        default: DS.Color.neutral
        }
    }

    var kindTitle: String {
        switch kind {
        case "input": .ui("timeline.kind.input", "입력")
        case "exec": .ui("timeline.kind.exec", "명령")
        case "file": .ui("timeline.kind.file", "파일")
        case "control": .ui("timeline.kind.control", "제어권")
        case "qa": .ui("timeline.kind.qa", "QA")
        case "debug": .ui("timeline.kind.debug", "디버그")
        case "connection": .ui("timeline.kind.connection", "연결")
        case "install": .ui("timeline.kind.install", "설치")
        default: kind
        }
    }

    /// Who acted: the client name, or the actor when the entry has none.
    var actorName: String { clientName ?? actorTitle }
}

/// One timeline entry: the actor badge on a rail, the action summary and time, who did it and
/// what kind of action it was, a badge when it did not succeed, and its artifacts.
struct TimelineRow: View {
    let entry: TimelineEntry
    /// Draws the rail down to the next row.
    let continuesRail: Bool
    /// Reads and caches the screenshot thumbnails.
    let evidence: QAEvidenceStore

    var body: some View {
        HStack(alignment: .top, spacing: DS.Space.m) {
            ActorBadge(entry: entry)
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                HStack(alignment: .firstTextBaseline, spacing: DS.Space.s) {
                    Text(entry.summary)
                        .font(entry.isCommandLike ? DS.Font.mono : DS.Font.body)
                        .lineLimit(entry.isCommandLike ? 2 : 3)
                        .truncationMode(.middle)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Text(DS.Format.clock(entry.ts.date))
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                        .fixedSize()
                }
                HStack(spacing: DS.Space.s) {
                    Text(verbatim: "\(entry.actorName) · \(entry.kindTitle)")
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                    if let outcome = OutcomeBadge.Outcome(rawValue: entry.outcome) {
                        OutcomeBadge(outcome: outcome)
                    }
                }
                if !entry.artifacts.isEmpty {
                    ArtifactStrip(artifacts: entry.artifacts, evidence: evidence)
                        .padding(.top, DS.Space.xs)
                }
            }
        }
        .padding(.bottom, DS.Space.l)
        .background(alignment: .topLeading) {
            if continuesRail {
                Rectangle()
                    .fill(DS.Color.separator)
                    .frame(width: 2)
                    .padding(.top, ActorBadge.size + DS.Space.xs)
                    .padding(.bottom, DS.Space.xs)
                    .padding(.leading, ActorBadge.size / 2 - 1)
            }
        }
        .contentShape(Rectangle())
        .contextMenu {
            Button(String.ui("timeline.copySummary", "동작 요약 복사"), systemImage: "doc.on.doc") {
                Pasteboard.copy(entry.summary)
            }
            if let jobID = entry.jobID {
                Button(String.ui("timeline.copyJob", "작업 ID 복사"), systemImage: "number") {
                    Pasteboard.copy(jobID)
                }
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(accessibilityText)
    }

    private var accessibilityText: String {
        var parts = [entry.actorName, entry.summary, DS.Format.clock(entry.ts.date)]
        if let outcome = OutcomeBadge.Outcome(rawValue: entry.outcome) { parts.append(outcome.title) }
        if !entry.artifacts.isEmpty {
            parts.append(String.ui("timeline.a11y.artifacts", "산출물 \(entry.artifacts.count)개"))
        }
        return parts.joined(separator: ", ")
    }
}

/// The actor symbol on a tinted circle: human blue, AI purple, system gray.
struct ActorBadge: View {
    static let size: CGFloat = 24
    let entry: TimelineEntry

    var body: some View {
        Circle()
            .fill(entry.actorTint.opacity(0.16))
            .frame(width: Self.size, height: Self.size)
            .overlay {
                Image(systemName: entry.actorSymbol)
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(entry.actorTint)
            }
            .help(entry.actorTitle)
            .accessibilityHidden(true)
    }
}

/// Capsule naming an outcome other than success.
struct OutcomeBadge: View {
    enum Outcome: String {
        case failed
        case denied
        case unknown

        var title: String {
            switch self {
            case .failed: .ui("outcome.failed", "실패")
            case .denied: .ui("outcome.denied", "거부됨")
            case .unknown: .ui("outcome.unknown", "결과 불명")
            }
        }

        var tint: Color {
            switch self {
            case .failed, .denied: DS.Color.danger
            case .unknown: DS.Color.warning
            }
        }

        var textColor: Color {
            switch self {
            case .failed, .denied: DS.Status.dangerText
            case .unknown: DS.Status.warningText
            }
        }

        var help: String {
            switch self {
            case .failed: .ui("outcome.failed.help", "동작이 실행됐지만 실패했습니다")
            case .denied: .ui("outcome.denied.help", "제어권이 없어 실행하지 않았습니다")
            case .unknown: .ui("outcome.unknown.help", "연결이 끊겨 실행 여부를 확인하지 못했습니다")
            }
        }
    }

    let outcome: Outcome

    var body: some View {
        Text(outcome.title)
            .font(DS.Font.caption.weight(.semibold))
            .foregroundStyle(outcome.textColor)
            .padding(.horizontal, DS.Space.s)
            .background(outcome.tint.opacity(DS.Status.badgeOpacity), in: Capsule())
            .fixedSize()
            .help(outcome.help)
    }
}

/// Artifacts of an entry: screenshots as thumbnail tiles of the image file, other files as
/// chips. Each reveals its file in Finder.
struct ArtifactStrip: View {
    let artifacts: [Artifact]
    let evidence: QAEvidenceStore
    static let maxShown = 3

    var body: some View {
        FlowLayout(spacing: DS.Space.s, lineSpacing: DS.Space.s) {
            ForEach(artifacts.prefix(Self.maxShown), id: \.artifactID) { artifact in
                Button {
                    FinderReveal.reveal(path: artifact.path)
                } label: {
                    if QAEvidenceStore.isImage(artifact) {
                        ScreenshotTile(artifact: artifact, evidence: evidence)
                    } else {
                        ArtifactChip(artifact: artifact)
                    }
                }
                .buttonStyle(.plain)
                .help(String.ui("timeline.artifact.help", "Finder에서 보기 · \(artifact.path.abbreviatingHome)"))
                .accessibilityLabel(String.ui("timeline.artifact.a11y", "산출물 \(artifact.name), Finder에서 보기"))
            }
            if artifacts.count > Self.maxShown {
                Text(String.ui("timeline.artifact.more", "+\(artifacts.count - Self.maxShown)"))
                    .font(DS.Font.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, DS.Space.s)
                    .padding(.vertical, DS.Space.xs)
                    .background(DS.Color.neutral.opacity(0.12), in: Capsule())
            }
        }
    }
}

/// Thumbnail of a screenshot artifact, read and downsampled off the main thread by `evidence`:
/// a spinner while it loads, the photo symbol when the file cannot be read.
private struct ScreenshotTile: View {
    let artifact: Artifact
    let evidence: QAEvidenceStore
    static let size = CGSize(width: 96, height: 60)

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
        shape
            .fill(DS.Color.neutral.opacity(0.12))
            .frame(width: Self.size.width, height: Self.size.height)
            .overlay {
                switch evidence.content(of: artifact, size: .thumbnail) {
                case .image(let image, _):
                    Image(nsImage: image)
                        .resizable()
                        .interpolation(.high)
                        .scaledToFill()
                        .frame(width: Self.size.width, height: Self.size.height)
                case .loading:
                    ProgressView()
                        .controlSize(.small)
                default:
                    Image(systemName: "photo")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                }
            }
            .clipShape(shape)
            .overlay(shape.strokeBorder(DS.Color.separator, lineWidth: 1))
            .onAppear { evidence.load(artifact, size: .thumbnail) }
    }
}

/// Chip with the artifact kind symbol and file name.
private struct ArtifactChip: View {
    let artifact: Artifact

    private var symbol: String {
        switch artifact.kind {
        case "log": "doc.text"
        case "report": "chart.bar.doc.horizontal"
        case "ui_tree": "list.bullet.indent"
        case "trace": "waveform.path"
        default: "doc"
        }
    }

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Image(systemName: symbol)
                .foregroundStyle(.secondary)
            Text(artifact.name)
                .font(.system(.caption, design: .monospaced))
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .font(DS.Font.caption)
        .padding(.horizontal, DS.Space.s)
        .padding(.vertical, DS.Space.xs)
        .frame(maxWidth: 200, alignment: .leading)
        .background(DS.Color.neutral.opacity(0.10), in: RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
                .strokeBorder(DS.Color.separator, lineWidth: 1)
        )
    }
}
