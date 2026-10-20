import DeskhandMedia
import SwiftUI

/// Stream statistics over the remote screen: received fps, capture-to-display latency and
/// bitrate, then the latency split, sizes, encoder, encode times and dropped frames, and the
/// chord that releases input while it is captured. Values the stream has not reported show as —.
struct ScreenHUD: View {
    let metrics: FrameClock.Snapshot?
    let info: StreamInfo?
    let stats: StreamStats?
    let captured: Bool
    let remoteSize: CGSize

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: DS.Space.l) {
                figure(metrics.map { "\(Int($0.fps.rounded()))" }, unit: "fps")
                figure(metrics?.latencyMS.map { "\(Int($0.rounded()))" }, unit: "ms")
                figure(metrics.map { Self.megabits($0.bitsPerSecond) }, unit: "Mbps")
            }
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.xs) {
                row(String.ui("hud.latency", "지연"), latencyDetail)
                row(String.ui("hud.size", "해상도"), sizeDetail)
                row(String.ui("hud.encoder", "인코더"), encoderDetail)
                row(String.ui("hud.encode", "인코딩"), encodeDetail)
                row(String.ui("hud.dropped", "손실"), stats?.dropped.map { String.ui("hud.dropped.value", "\($0)프레임") } ?? "—")
            }
            .font(DS.Font.caption)
            if captured {
                Divider()
                Label(String.ui("hud.release", "⌃⌥를 눌렀다 떼면 입력을 돌려받습니다"), systemImage: "keyboard")
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(DS.Space.m)
        .background(DS.Color.overlayPanel, in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous).strokeBorder(.white.opacity(0.12)))
        .environment(\.colorScheme, .dark)
        .allowsHitTesting(false)
        .accessibilityElement(children: .combine)
    }

    private func figure(_ value: String?, unit: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 2) {
            Text(verbatim: value ?? "—")
                .font(DS.Font.title.monospacedDigit())
                .foregroundStyle(.primary)
            Text(verbatim: unit)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
        }
    }

    private func row(_ title: String, _ value: String) -> some View {
        GridRow {
            Text(title)
                .foregroundStyle(.secondary)
            Text(value)
                .monospacedDigit()
                .foregroundStyle(.primary)
        }
    }

    static func megabits(_ bitsPerSecond: Double) -> String {
        (bitsPerSecond / 1_000_000).formatted(.number.precision(.fractionLength(1)))
    }

    private static func ms(_ value: Double) -> String {
        value.formatted(.number.precision(.fractionLength(value < 10 ? 1 : 0)))
    }

    private var latencyDetail: String {
        guard let daemon = metrics?.captureToReceiveMS else {
            return String.ui("hud.latency.pending", "데몬 측정을 기다리는 중")
        }
        let received = Self.ms(daemon)
        let app = Self.ms(metrics?.appMS ?? 0)
        return String.ui("hud.latency.detail", "PC→Mac \(received) ms · 앱 \(app) ms")
    }

    private var sizeDetail: String {
        guard let info else { return "—" }
        let encoded = "\(info.width)×\(info.height)"
        guard remoteSize.width > 0, Int(remoteSize.width) != info.width || Int(remoteSize.height) != info.height else { return encoded }
        let source = "\(Int(remoteSize.width))×\(Int(remoteSize.height))"
        return String.ui("hud.size.scaled", "\(encoded) · 원본 \(source)")
    }

    private var encoderDetail: String {
        guard let info else { return "—" }
        let name = info.encoder ?? info.codec?.uppercased() ?? "H.264"
        switch info.hardware {
        case true?: return String.ui("hud.encoder.hardware", "\(name) · 하드웨어")
        case false?: return String.ui("hud.encoder.software", "\(name) · 소프트웨어")
        case nil: return name
        }
    }

    private var encodeDetail: String {
        guard let p50 = stats?.encodeMsP50 else { return "—" }
        let median = Self.ms(p50)
        guard let p95 = stats?.encodeMsP95 else { return String.ui("hud.encode.median", "중앙값 \(median) ms") }
        let high = Self.ms(p95)
        return String.ui("hud.encode.detail", "중앙값 \(median) ms · p95 \(high) ms")
    }
}
