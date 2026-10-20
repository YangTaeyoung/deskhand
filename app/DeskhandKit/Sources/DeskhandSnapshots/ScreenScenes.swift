import DeskhandClient
import DeskhandMedia
import DeskhandUI
import SwiftUI
import struct DeskhandClient.Host

/// Screen tab scenes at 1440×900: the live screen with the fixture's first frame in place of
/// the video, and each loading, paused, reconnecting and failure state.
@MainActor
enum ScreenScenes {
    static let stats = StreamStats(fps: 60, bitrate: 17_400_000, dropped: 0, encodeMsP50: 3.6, encodeMsP95: 7.1, captureMsP50: 8.4)
    static let metrics = FrameClock.Snapshot(fps: 60, bitsPerSecond: 17_400_000, appMS: 0.8, captureToReceiveMS: 17.2)

    /// Stream info of the host's display, encoded at 1920×1080. The source size is the display's
    /// when it has the still frame's 16:9 shape, otherwise the still frame's own size.
    static func info(for host: Host, display: Int = 0) -> StreamInfo {
        let target = host.displays.first { $0.index == display } ?? host.displays.first
        var width = 1920
        var height = 1080
        if let target, target.width * 9 == target.height * 16 {
            width = target.width
            height = target.height
        }
        return StreamInfo(width: 1920, height: 1080, srcWidth: width, srcHeight: height,
                          fps: 60, bitrate: 20_000_000, encoder: "NVIDIA H.264 Encoder MFT", hardware: true, capture: "dd",
                          display: display)
    }

    static func standIn(_ host: Host, phase: ScreenSession.Phase = .live, hasFrame: Bool = true, captured: Bool = false,
                        markedText: String? = nil, connectError: OperationError? = nil, display: Int = 0,
                        cursor: CGPoint? = CGPoint(x: 0.46, y: 0.42)) -> ScreenStandIn {
        ScreenStandIn(session: .standIn(phase: phase, hasFrame: hasFrame, info: info(for: host, display: display), stats: stats,
                                        metrics: metrics, captured: captured, markedText: markedText, connectError: connectError),
                      still: FixtureFrame.first, cursor: cursor)
    }

    /// A live stand-in for every computer, replaced by `overrides`.
    static func standIns(for hosts: [Host], overrides: [String: ScreenStandIn] = [:]) -> ScreenStandIns {
        var byHost = Dictionary(uniqueKeysWithValues: hosts.map { ($0.hostID, standIn($0)) })
        byHost.merge(overrides) { _, new in new }
        return ScreenStandIns(byHost)
    }

    static var all: [SnapshotScene] {
        let hosts = PreviewFixtures.hosts()
        func host(_ id: String) -> Host { hosts.first { $0.hostID == id }! }
        let dev = PreviewFixtures.devPCID
        let human = ControlState(owner: "human", gen: 6, aiPaused: false)
        typealias Setup = WorkspaceScenes.Setup
        return [
            WorkspaceScenes.scene("screen-live", Setup(selected: dev, controls: [dev: human],
                                                       screens: [dev: standIn(host(dev), captured: true)])),
            WorkspaceScenes.scene("screen-ai-control", Setup(selected: dev)),
            WorkspaceScenes.scene("screen-hud", Setup(selected: dev, inspector: nil, controls: [dev: human], statistics: true,
                                                      screens: [dev: standIn(host(dev), captured: true)])),
            WorkspaceScenes.scene("screen-connecting", Setup(
                selected: PreviewFixtures.meetingPCID,
                screens: [PreviewFixtures.meetingPCID: standIn(host(PreviewFixtures.meetingPCID), phase: .connecting, hasFrame: false)])),
            WorkspaceScenes.scene("screen-connecting-retry", Setup(
                selected: PreviewFixtures.meetingPCID,
                screens: [PreviewFixtures.meetingPCID: standIn(
                    host(PreviewFixtures.meetingPCID), phase: .connecting, hasFrame: false,
                    connectError: OperationError(code: "TIMEOUT", message: "에이전트가 5초 안에 화면 스트림을 열지 않았습니다"))])),
            WorkspaceScenes.scene("screen-display-unavailable", Setup(
                selected: dev, controls: [dev: human], display: 1,
                screens: [dev: standIn(host(dev), phase: .displayUnavailable(OperationError(
                    code: "DISPLAY_UNAVAILABLE", message: "요청한 디스플레이가 이 PC에 없습니다")),
                    hasFrame: false, display: 1, cursor: nil)])),
            WorkspaceScenes.scene("screen-no-output", Setup(
                selected: dev, controls: [dev: human], display: 1,
                screens: [dev: standIn(host(dev), phase: .paused(.noOutput), hasFrame: false, display: 1, cursor: nil)])),
            WorkspaceScenes.scene("screen-session-disconnected", Setup(
                selected: PreviewFixtures.stagingID, inspector: nil,
                screens: [PreviewFixtures.stagingID: standIn(host(PreviewFixtures.stagingID), phase: .paused(.sessionDisconnected), cursor: nil)])),
            WorkspaceScenes.scene("screen-access-lost", Setup(
                selected: dev, controls: [dev: human],
                screens: [dev: standIn(host(dev), phase: .paused(.accessLost), cursor: nil)])),
            WorkspaceScenes.scene("screen-session0", Setup(
                selected: PreviewFixtures.meetingPCID,
                screens: [PreviewFixtures.meetingPCID: standIn(host(PreviewFixtures.meetingPCID), phase: .paused(.session0), hasFrame: false)])),
            WorkspaceScenes.scene("screen-no-encoder", Setup(
                selected: PreviewFixtures.qaLaptopID,
                screens: [PreviewFixtures.qaLaptopID: standIn(host(PreviewFixtures.qaLaptopID), phase: .paused(.noEncoder), hasFrame: false)])),
            WorkspaceScenes.scene("screen-secure-desktop", Setup(
                selected: PreviewFixtures.qaLaptopID,
                screens: [PreviewFixtures.qaLaptopID: standIn(host(PreviewFixtures.qaLaptopID), phase: .paused(.secureDesktop), cursor: nil)])),
            WorkspaceScenes.scene("screen-rdp-suppressed", Setup(
                selected: PreviewFixtures.stagingID, inspector: nil,
                screens: [PreviewFixtures.stagingID: standIn(host(PreviewFixtures.stagingID), phase: .paused(.rdpOutputSuppressed), cursor: nil)])),
            WorkspaceScenes.scene("screen-reconnecting", Setup(
                selected: dev, controls: [dev: human],
                screens: [dev: standIn(host(dev), phase: .reconnecting, cursor: nil)])),
            WorkspaceScenes.scene("screen-error", Setup(
                selected: PreviewFixtures.qaLaptopID,
                screens: [PreviewFixtures.qaLaptopID: standIn(
                    host(PreviewFixtures.qaLaptopID), phase: .failed(OperationError(
                        code: "CAPABILITY_UNSUPPORTED", message: "이 에이전트 버전은 화면 스트리밍을 지원하지 않습니다")),
                    hasFrame: false)])),
            WorkspaceScenes.scene("screen-mac-input", Setup(selected: dev, controls: [dev: human],
                                                            screens: [dev: standIn(host(dev), captured: true, markedText: "한")])),
            WorkspaceScenes.scene("screen-offline", Setup(selected: PreviewFixtures.designID)),
        ]
    }
}
