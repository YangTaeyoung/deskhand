/// Screen stream quality presets (design.md §6), sent as `preset` of `stream.attach`.
public enum StreamPreset: String, CaseIterable, Identifiable, Sendable {
    /// Native resolution at a high bitrate.
    case sharp
    /// Scaled to at most 2560 pixels wide.
    case balanced
    /// Scaled to at most 1920 pixels wide at a low bitrate.
    case saver

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .sharp: .ui("preset.sharp", "선명")
        case .balanced: .ui("preset.balanced", "균형")
        case .saver: .ui("preset.saver", "절약")
        }
    }

    public var detail: String {
        switch self {
        case .sharp: .ui("preset.sharp.detail", "원본 해상도 그대로 · 높은 비트레이트")
        case .balanced: .ui("preset.balanced.detail", "최대 2560 폭으로 축소")
        case .saver: .ui("preset.saver.detail", "최대 1920 폭으로 축소 · 낮은 비트레이트")
        }
    }
}
