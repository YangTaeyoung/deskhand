import DeskhandClient
import Foundation

/// Chooses the backend for a launch: `PreviewBackend` with `--preview[=<scenario>]` or
/// `DESKHAND_PREVIEW=1|<scenario>`, playing the bundled `sample.h264` on screen streams,
/// otherwise `LiveBackend`.
public enum AppLaunch {
    /// Separate defaults domain for preview launches, so they do not change the real selection.
    public static let previewDefaultsSuite = "io.github.yangtaeyoung.Deskhand.preview"

    @MainActor
    public static func makeStore(
        arguments: [String] = CommandLine.arguments,
        environment: [String: String] = Foundation.ProcessInfo.processInfo.environment
    ) -> AppStore {
        if let scenario = previewScenario(arguments: arguments, environment: environment) {
            let defaults = UserDefaults(suiteName: previewDefaultsSuite) ?? .standard
            let video = Bundle.main.url(forResource: "sample", withExtension: "h264")
            return AppStore(backend: PreviewBackend(scenario: scenario, screenVideo: video), defaults: defaults)
        }
        return AppStore(backend: LiveBackend())
    }

    /// The preview scenario requested by the arguments or environment, or nil for a live launch.
    public static func previewScenario(arguments: [String], environment: [String: String]) -> PreviewScenario? {
        for (index, argument) in arguments.enumerated() {
            if argument == "--preview" {
                let next = arguments.indices.contains(index + 1) ? arguments[index + 1] : nil
                return next.flatMap(PreviewScenario.init(rawValue:)) ?? .standard
            }
            if argument.hasPrefix("--preview=") {
                return PreviewScenario(rawValue: String(argument.dropFirst("--preview=".count))) ?? .standard
            }
        }
        guard let value = environment["DESKHAND_PREVIEW"], !value.isEmpty, value != "0" else { return nil }
        return PreviewScenario(rawValue: value) ?? .standard
    }
}
