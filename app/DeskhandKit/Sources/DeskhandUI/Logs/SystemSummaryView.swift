import DeskhandClient
import SwiftUI

/// The system summary of `diag.system`: Windows edition and build, processor with its use,
/// memory, each disk's free space, display adapters and uptime, as a row of cards under a line
/// with the time of the summary, or that its last refresh failed. Placeholder cards while it
/// loads and a one-line error with retry when it fails.
struct SystemSummaryView: View {
    let model: LogsModel

    var body: some View {
        switch model.systemPhase {
        case .failed(let error) where model.system == nil:
            SummaryErrorStrip(error: error) { Task { await model.reloadSystem() } }
        default:
            VStack(alignment: .leading, spacing: DS.Space.s) {
                SummaryHeader(model: model)
                if let system = model.system {
                    SummaryCards(system: system)
                } else {
                    SummaryCards(system: Self.placeholder)
                        .redacted(reason: .placeholder)
                        .accessibilityLabel(String.ui("logs.system.loading", "시스템 요약을 불러오는 중"))
                }
            }
        }
    }

    static let placeholder = DiagSystem(
        os: DiagOS(name: "Windows 11 Pro", version: "24H2", build: "26100.0000", edition: "Professional"),
        computerName: "WINDOWS-PC", cpu: DiagCPU(name: "Processor name placeholder", cores: 8, threads: 16, usagePct: 0),
        memory: DiagMemory(totalBytes: 16 << 30, usedBytes: 8 << 30),
        disks: [DiagDisk(root: "C:\\", label: "Windows", fs: "NTFS", totalBytes: 512 << 30, freeBytes: 256 << 30)],
        gpus: [DiagGPU(name: "Display adapter placeholder", driverVersion: "00.0.00.0000")],
        bootTime: Timestamp(date: .now), uptimeS: 3_600)
}

/// `시스템 요약` and the time of the summary, or a warning when its last refresh failed.
private struct SummaryHeader: View {
    let model: LogsModel

    var body: some View {
        HStack(spacing: DS.Space.xs) {
            Text(String.ui("logs.summary.title", "시스템 요약"))
                .font(DS.Font.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            Spacer(minLength: DS.Space.s)
            if let updated = model.systemUpdatedAt {
                let time = DS.Format.clock(updated, seconds: true)
                if let error = model.systemRefreshError {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(DS.Color.warning)
                        .accessibilityHidden(true)
                    Text(String.ui("logs.summary.stale", "새로 고치지 못했습니다 · \(time) 기준"))
                        .foregroundStyle(.secondary)
                        .help(error.sentences)
                } else {
                    Text(String.ui("logs.updated", "\(time) 기준"))
                        .foregroundStyle(.secondary)
                }
            }
        }
        .font(DS.Font.caption)
        .accessibilityElement(children: .combine)
    }
}

/// The summary failed before any arrived: what failed, the reason, the code and retry, in one row.
private struct SummaryErrorStrip: View {
    let error: OperationError
    let retry: () -> Void

    var body: some View {
        HStack(alignment: .center, spacing: DS.Space.m) {
            Image(systemName: "exclamationmark.octagon.fill")
                .foregroundStyle(DS.Color.danger)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(String.ui("logs.system.failed", "시스템 요약을 불러오지 못했습니다"))
                    .font(DS.Font.body.weight(.semibold))
                Text(LogsModel.explanation(error))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .help(error.sentences)
            }
            Spacer(minLength: DS.Space.s)
            if let code = error.code {
                ErrorCodeLine(code: code)
            }
            Button(action: retry) {
                Label(String.ui("action.retry", "다시 시도"), systemImage: "arrow.clockwise")
            }
            .controlSize(.small)
        }
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s)
        .background(DS.Color.danger.opacity(0.08), in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(DS.Color.danger.opacity(0.28), lineWidth: 1)
        )
        .accessibilityElement(children: .contain)
    }
}

/// Six cards that share a row when the tab is wide and wrap to two rows of three otherwise.
private struct SummaryCards: View {
    let system: DiagSystem

    var body: some View {
        ViewThatFits(in: .horizontal) {
            Grid(horizontalSpacing: DS.Space.s, verticalSpacing: DS.Space.s) {
                GridRow { cards }
            }
            .frame(minWidth: 1_040)
            .fixedSize(horizontal: false, vertical: true)
            Grid(horizontalSpacing: DS.Space.s, verticalSpacing: DS.Space.s) {
                GridRow {
                    osCard
                    cpuCard
                    memoryCard
                }
                GridRow {
                    diskCard
                    gpuCard
                    uptimeCard
                }
            }
            .fixedSize(horizontal: false, vertical: true)
        }
    }

    @ViewBuilder
    private var cards: some View {
        osCard
        cpuCard
        memoryCard
        diskCard
        gpuCard
        uptimeCard
    }

    private var osCard: some View {
        SummaryCard(symbol: "pc", title: String.ui("logs.system.os", "운영 체제")) {
            Text(system.os.name)
                .font(DS.Font.headline)
                .lineLimit(1)
            Text(String.ui("logs.system.build", "\(system.os.version) · 빌드 \(system.os.build)"))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Text(system.computerName)
                .font(DS.Font.caption.monospaced())
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .textSelection(.enabled)
        }
        .help([system.os.name, system.os.version, system.os.build, system.os.edition].filter { !$0.isEmpty }.joined(separator: " · "))
    }

    private var cpuCard: some View {
        let usage = min(100, max(0, system.cpu.usagePct))
        let percent = DS.Format.percent(points: usage)
        let cores = DS.Format.count(system.cpu.cores)
        let threads = DS.Format.count(system.cpu.threads)
        return SummaryCard(symbol: "cpu", title: String.ui("logs.system.cpu", "CPU")) {
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.xs) {
                Text(percent)
                    .font(DS.Font.headline.monospacedDigit())
                Text(String.ui("logs.system.cpu.use", "사용 중"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            UsageBar(fraction: usage / 100, tint: SummaryFormat.tint(usedFraction: usage / 100))
            Text(system.cpu.name)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.middle)
            Text(String.ui("logs.system.cpu.cores", "\(cores)코어 · \(threads)스레드"))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .help(system.cpu.name)
        .accessibilityValue(String.ui("logs.a11y.cpu", "\(system.cpu.name), \(percent) 사용 중"))
    }

    private var memoryCard: some View {
        let total = max(1, system.memory.totalBytes)
        let used = min(total, system.memory.usedBytes)
        let fraction = Double(used) / Double(total)
        let usedText = DS.Format.bytes(used)
        let totalText = DS.Format.bytes(total)
        let free = DS.Format.bytes(total - used)
        return SummaryCard(symbol: "memorychip", title: String.ui("logs.system.memory", "메모리")) {
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.xs) {
                Text(usedText)
                    .font(DS.Font.headline.monospacedDigit())
                Text(String.ui("logs.system.memory.of", "/ \(totalText)"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            UsageBar(fraction: fraction, tint: SummaryFormat.tint(usedFraction: fraction))
            Text(String.ui("logs.system.memory.free", "사용 가능 \(free)"))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
    }

    private var diskCard: some View {
        SummaryCard(symbol: "internaldrive", title: String.ui("logs.system.disks", "디스크")) {
            if system.disks.isEmpty {
                Text(String.ui("logs.system.disks.none", "디스크 정보가 없습니다"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            ForEach(Array(system.disks.prefix(3).enumerated()), id: \.offset) { _, disk in
                DiskRow(disk: disk)
            }
            if system.disks.count > 3 {
                let more = DS.Format.count(system.disks.count - 3)
                Text(String.ui("logs.system.disks.more", "외 \(more)개"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var gpuCard: some View {
        SummaryCard(symbol: "display", title: String.ui("logs.system.gpu", "그래픽")) {
            if let gpu = system.gpus.first {
                Text(gpu.name)
                    .font(DS.Font.headline)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
                Text(gpuDetail(gpu))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                if system.gpus.count > 1 {
                    let more = DS.Format.count(system.gpus.count - 1)
                    Text(String.ui("logs.system.gpu.more", "어댑터 \(more)개 더"))
                        .font(DS.Font.caption)
                        .foregroundStyle(.secondary)
                }
            } else {
                Text(String.ui("logs.system.gpu.none", "그래픽 어댑터 정보가 없습니다"))
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .help(system.gpus.map { [$0.name, $0.driverVersion].compactMap { $0 }.joined(separator: " · ") }.joined(separator: "\n"))
    }

    private func gpuDetail(_ gpu: DiagGPU) -> String {
        var parts: [String] = []
        if let driver = gpu.driverVersion { parts.append(.ui("logs.system.gpu.driver", "드라이버 \(driver)")) }
        if let memory = gpu.memoryBytes { parts.append(DS.Format.bytes(memory)) }
        return parts.isEmpty ? String.ui("logs.system.gpu.noDriver", "드라이버 정보 없음") : parts.joined(separator: " · ")
    }

    private var uptimeCard: some View {
        let uptime = DS.Format.uptime(seconds: system.uptimeS)
        let boot = DS.Format.dateTime(system.bootTime.date, relative: false)
        return SummaryCard(symbol: "clock.arrow.circlepath", title: String.ui("logs.system.uptime", "가동 시간")) {
            Text(uptime)
                .font(DS.Font.headline.monospacedDigit())
                .lineLimit(1)
            Text(String.ui("logs.system.boot", "\(boot) 부팅"))
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
    }
}

/// One card: a symbol and title over its lines, the same height as the others in its row.
private struct SummaryCard<Content: View>: View {
    let symbol: String
    let title: String
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.xs) {
            Label(title, systemImage: symbol)
                .font(DS.Font.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .labelStyle(SummaryLabelStyle())
            content
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .padding(.horizontal, DS.Space.m)
        .padding(.vertical, DS.Space.s + 2)
        .background(DS.Color.surfaceRaised, in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous)
                .strokeBorder(DS.Color.separator, lineWidth: 1)
        )
        .accessibilityElement(children: .combine)
    }
}

private struct SummaryLabelStyle: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: DS.Space.xs) {
            configuration.icon
                .imageScale(.small)
                .frame(width: 14)
            configuration.title
        }
    }
}

/// `C:\ Windows` with its free space and a bar of the used part.
private struct DiskRow: View {
    let disk: DiagDisk

    var body: some View {
        let total = max(1, disk.totalBytes)
        let free = min(total, disk.freeBytes)
        let used = Double(total - free) / Double(total)
        let freeText = DS.Format.bytes(free)
        let totalText = DS.Format.bytes(total)
        let tint = SummaryFormat.tint(usedFraction: used)
        let label = disk.label ?? ""
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: DS.Space.xs) {
                Text(disk.root)
                    .font(DS.Font.caption.monospaced().weight(.semibold))
                if let label = disk.label {
                    Text(label)
                        .font(DS.Font.caption)
                        .lineLimit(1)
                }
                Spacer(minLength: DS.Space.xs)
                Text(String.ui("logs.system.disk.free", "\(freeText) 남음"))
                    .font(DS.Font.caption.monospacedDigit())
                    .foregroundStyle(used >= 0.8 ? AnyShapeStyle(tint) : AnyShapeStyle(.secondary))
                    .lineLimit(1)
            }
            UsageBar(fraction: used, tint: tint)
        }
        .help(String.ui("logs.system.disk.help", "\(disk.root) \(label) · \(disk.fs) · \(totalText) 중 \(freeText) 남음"))
        .accessibilityElement(children: .combine)
    }
}

/// A thin bar of a used fraction, drawn in a neutral gray while its view is redacted.
struct UsageBar: View {
    let fraction: Double
    let tint: Color
    @Environment(\.redactionReasons) private var redaction

    var body: some View {
        GeometryReader { proxy in
            ZStack(alignment: .leading) {
                Capsule().fill(DS.Color.neutral.opacity(0.18))
                Capsule().fill(redaction.isEmpty ? tint : DS.Color.neutral.opacity(0.3))
                    .frame(width: max(fraction > 0 ? 3 : 0, proxy.size.width * min(1, max(0, fraction))))
            }
        }
        .frame(height: 4)
        .accessibilityHidden(true)
    }
}

enum SummaryFormat {
    /// Accent below 80 % used, orange to 92 %, red above.
    static func tint(usedFraction: Double) -> Color {
        if usedFraction >= 0.92 { return DS.Color.danger }
        if usedFraction >= 0.8 { return DS.Color.warning }
        return DS.Color.accent
    }
}
