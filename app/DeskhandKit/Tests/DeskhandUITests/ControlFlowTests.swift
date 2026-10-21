import DeskhandClient
import Foundation
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

@MainActor
final class ControlFlowTests: XCTestCase {
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUp() async throws {
        suiteName = "deskhand-tests-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)
    }

    override func tearDown() async throws {
        defaults.removePersistentDomain(forName: suiteName)
    }

    private func startedStore() async -> (AppStore, GatedBackend) {
        let backend = GatedBackend(PreviewBackend(scenario: .standard, stepDelay: .zero))
        let store = AppStore(backend: backend, defaults: defaults,
                             backoff: .init(initial: .milliseconds(50), maximum: .milliseconds(100)))
        await store.start()
        return (store, backend)
    }

    private func bannerKind(_ store: AppStore, _ hostID: String) -> ControlBannerKind? {
        store.host(hostID).map { ControlBannerKind(host: $0, control: store.control(for: hostID)) }
    }

    // MARK: Brief

    func testAIBannerStopsPromisingEmergencyStopAfterItFails() {
        XCTAssertTrue(ControlBannerKind.aiMessage(stopFailed: false).contains("긴급 정지"))
        let failed = ControlBannerKind.aiMessage(stopFailed: true)
        XCTAssertTrue(failed.contains("실패"))
        XCTAssertNotEqual(failed, ControlBannerKind.aiMessage(stopFailed: false))
    }

    func testEmergencyStopCallsControlStop() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        XCTAssertEqual(store.control(for: dev)?.owner, "ai")

        await store.controlActions.stop(dev)?.value

        XCTAssertEqual(backend.controlCalls, [ControlCall(method: "control.stop", hostID: dev)])
        XCTAssertEqual(store.control(for: dev)?.owner, "human")
        XCTAssertEqual(store.control(for: dev)?.aiPaused, true)
        XCTAssertFalse(store.controlActions.isRunning(.stop, on: dev))
        XCTAssertTrue(store.controlActions.failures(for: dev).isEmpty)
    }

    func testTakeoverRequiresConfirmationWhenAIOwns() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID

        let task = store.controlActions.takeOver(dev)

        XCTAssertNil(task)
        XCTAssertEqual(store.controlActions.pendingTakeover(for: dev), ControlActions.PendingTakeover(hostID: dev, client: "Claude Code"))
        XCTAssertTrue(backend.controlCalls.isEmpty)

        await store.controlActions.confirmTakeover(dev)?.value

        XCTAssertNil(store.controlActions.pendingTakeover(for: dev))
        XCTAssertEqual(backend.controlCalls, [ControlCall(method: "control.acquire", hostID: dev, owner: .human, force: true)])
        XCTAssertEqual(store.control(for: dev)?.owner, "human")
    }

    func testGiveBackCallsResumeAI() async throws {
        let (store, backend) = await startedStore()
        let qa = PreviewFixtures.qaLaptopID
        XCTAssertEqual(store.control(for: qa)?.owner, "human")

        await store.controlActions.giveBack(qa)?.value

        XCTAssertEqual(backend.controlCalls, [ControlCall(method: "control.resume_ai", hostID: qa)])
        XCTAssertEqual(store.control(for: qa)?.owner, "none")
        XCTAssertEqual(store.control(for: qa)?.aiPaused, false)
    }

    // MARK: Takeover

    func testTakeoverWhileObservingAcquiresWithoutConfirmation() async throws {
        let (store, backend) = await startedStore()
        let meeting = PreviewFixtures.meetingPCID
        XCTAssertEqual(store.control(for: meeting)?.owner, "none")

        await store.controlActions.takeOver(meeting)?.value

        XCTAssertNil(store.controlActions.pendingTakeover(for: meeting))
        XCTAssertEqual(backend.controlCalls, [ControlCall(method: "control.acquire", hostID: meeting, owner: .human, force: false)])
        XCTAssertEqual(store.control(for: meeting)?.owner, "human")
    }

    func testCancelledTakeoverSendsNothing() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        store.controlActions.takeOver(dev)
        store.controlActions.cancelTakeover(dev)

        XCTAssertNil(store.controlActions.pendingTakeover(for: dev))
        XCTAssertNil(store.controlActions.confirmTakeover(dev))
        XCTAssertTrue(backend.controlCalls.isEmpty)
        XCTAssertEqual(store.control(for: PreviewFixtures.devPCID)?.owner, "ai")
    }

    // MARK: Progress and failures

    func testCallInFlightIsReportedUntilItFinishes() async throws {
        let (store, backend) = await startedStore()
        let qa = PreviewFixtures.qaLaptopID
        backend.holdControl("control.resume_ai")

        let task = store.controlActions.giveBack(qa)
        try await waitUntil { backend.pendingControlCount == 1 }
        XCTAssertTrue(store.controlActions.isRunning(.giveBack, on: qa))
        XCTAssertNil(store.controlActions.giveBack(qa), "a second give-back while one is in flight starts nothing")

        backend.releaseControl("control.resume_ai")
        await task?.value
        XCTAssertFalse(store.controlActions.isRunning(.giveBack, on: qa))
        XCTAssertEqual(backend.controlCalls.count, 1)
    }

    func testAFailedControlReadIsKeptUntilAReadSucceeds() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        backend.failControl("control.get", with: OperationError(code: "TIMEOUT", message: "데몬이 제때 답하지 않았습니다"))
        await store.loadControl(for: dev)
        XCTAssertEqual(store.controlError(for: dev)?.code, "TIMEOUT")

        backend.failControl("control.get", with: nil)
        await store.loadControl(for: dev)
        XCTAssertNil(store.controlError(for: dev))
    }

    func testEmergencyStopSendsAgainWhileAStopIsInFlight() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        backend.holdControl("control.stop")
        let first = store.controlActions.stop(dev)
        try await waitUntil { backend.pendingControlCount == 1 }
        XCTAssertTrue(store.controlActions.isRunning(.stop, on: dev))

        let second = store.controlActions.stop(dev)

        XCTAssertNotNil(second, "a second press sends another stop")
        try await waitUntil { backend.pendingControlCount == 2 }
        backend.releaseControl("control.stop")
        await first?.value
        await second?.value
        XCTAssertEqual(backend.controlCalls.map(\.method), ["control.stop", "control.stop"])
        XCTAssertFalse(store.controlActions.isRunning(.stop, on: dev))
        XCTAssertEqual(store.control(for: dev)?.aiPaused, true)
    }

    func testEmergencyStopRunsWhileTakeoverIsInFlight() async throws {
        let (store, backend) = await startedStore()
        let meeting = PreviewFixtures.meetingPCID
        backend.holdControl("control.acquire")
        let takeover = store.controlActions.takeOver(meeting)
        try await waitUntil { backend.pendingControlCount == 1 }

        await store.controlActions.stop(meeting)?.value

        XCTAssertEqual(backend.controlCalls.map(\.method), ["control.acquire", "control.stop"])
        XCTAssertTrue(store.controlActions.isRunning(.takeOver, on: meeting))
        backend.releaseControl("control.acquire")
        await takeover?.value
        XCTAssertEqual(store.control(for: meeting)?.aiPaused, true)
    }

    func testControlFailureStaysInlineAndRetrySucceeds() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        backend.failControl("control.stop", with: OperationError(code: "OFFLINE", message: "개발 PC에 연결되어 있지 않습니다"))

        await store.controlActions.stop(dev)?.value

        XCTAssertEqual(store.controlActions.failures(for: dev).map(\.action), [.stop])
        XCTAssertEqual(store.controlActions.failures(for: dev).first?.error.code, "OFFLINE")
        XCTAssertFalse(store.controlActions.isRunning(.stop, on: dev))
        XCTAssertNil(store.actionError)
        XCTAssertEqual(store.control(for: dev)?.owner, "ai")

        backend.failControl("control.stop", with: nil)
        await store.controlActions.retry(.stop, on: dev)?.value

        XCTAssertTrue(store.controlActions.failures(for: dev).isEmpty)
        XCTAssertEqual(backend.controlCalls.map(\.method), ["control.stop", "control.stop"])
        XCTAssertEqual(store.control(for: dev)?.aiPaused, true)
    }

    func testReplyOlderThanLaterEventDoesNotOverrideIt() async throws {
        let (store, backend) = await startedStore()
        let qa = PreviewFixtures.qaLaptopID
        backend.holdControl("control.resume_ai", afterApplying: true)
        let task = store.controlActions.giveBack(qa)
        try await waitUntil { backend.pendingControlCount == 1 }

        let aiControl = try await backend.base.controlAcquire(hostID: qa, as: .ai, force: false)
        try await waitUntil { store.control(for: qa)?.owner == "ai" }
        backend.releaseControl("control.resume_ai")
        await task?.value

        XCTAssertEqual(store.control(for: qa), aiControl)
        XCTAssertEqual(store.host(qa)?.owner, "ai")
    }

    func testFailureOfOneActionSurvivesAnotherAction() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        backend.failControl("control.stop", with: OperationError(code: "TIMEOUT", message: "데몬이 응답하지 않았습니다"))
        await store.controlActions.stop(dev)?.value

        await store.controlActions.giveBack(dev)?.value

        XCTAssertEqual(store.controlActions.failures(for: dev).map(\.action), [.stop])
        store.controlActions.dismissFailure(.stop, for: dev)
        XCTAssertTrue(store.controlActions.failures(for: dev).isEmpty)
    }

    func testEmergencyStopAppliesToOfflineHost() async throws {
        let (store, backend) = await startedStore()
        let design = PreviewFixtures.designID
        XCTAssertEqual(store.connection(for: design), .offline)

        await store.controlActions.stop(design)?.value

        XCTAssertEqual(backend.controlCalls, [ControlCall(method: "control.stop", hostID: design)])
        XCTAssertEqual(store.control(for: design)?.aiPaused, true)
    }

    // MARK: Pending takeover

    func testPendingTakeoverIsPerHostAndClearedOnSelectionChange() async throws {
        let (store, _) = await startedStore()
        let dev = PreviewFixtures.devPCID
        store.selectedHostID = dev
        store.controlActions.takeOver(dev)
        XCTAssertNotNil(store.controlActions.pendingTakeover(for: dev))
        XCTAssertNil(store.controlActions.pendingTakeover(for: PreviewFixtures.qaLaptopID))

        store.selectedHostID = PreviewFixtures.qaLaptopID

        XCTAssertNil(store.controlActions.pendingTakeover(for: dev))
    }

    func testPendingTakeoverClearedWhenHostIsRemoved() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        store.controlActions.takeOver(dev)

        backend.base.emit(kind: EventKind.hostRemoved, hostID: dev)

        try await waitUntil { store.host(dev) == nil }
        XCTAssertNil(store.controlActions.pendingTakeover(for: dev))
        XCTAssertNil(store.controlActions.confirmTakeover(dev))
    }

    // MARK: Generation order

    func testLateControlEventWithOlderGenIsIgnored() async throws {
        let (store, backend) = await startedStore()
        let qa = PreviewFixtures.qaLaptopID
        backend.base.emit(kind: EventKind.controlChanged, hostID: qa, payload: [
            "owner": .string("human"), "gen": .int(50), "ai_paused": .bool(true),
        ])
        try await waitUntil { store.control(for: qa)?.gen == 50 }

        backend.base.emit(kind: EventKind.controlChanged, hostID: qa, payload: [
            "owner": .string("ai"), "owner_client": .string("Codex"), "gen": .int(49), "ai_paused": .bool(false),
        ])
        backend.base.emit(kind: EventKind.controlChanged, hostID: qa, payload: [
            "owner": .string("none"), "gen": .int(50), "ai_paused": .bool(false),
        ])
        backend.base.emit(kind: EventKind.connectionState, hostID: qa, payload: ["state": .string("degraded")])
        try await waitUntil { store.connection(for: qa) == .degraded }

        XCTAssertEqual(store.control(for: qa), ControlState(owner: "human", gen: 50, aiPaused: true))
        XCTAssertEqual(store.host(qa)?.owner, "human")
    }

    func testStaleControlGetReplyIsIgnored() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        backend.holdControl("control.get", afterApplying: true)
        let load = Task { await store.loadControl(for: dev) }
        try await waitUntil { backend.pendingControlCount == 1 }

        await store.controlActions.stop(dev)?.value
        backend.releaseControl("control.get")
        await load.value

        XCTAssertEqual(store.control(for: dev)?.owner, "human")
        XCTAssertEqual(store.control(for: dev)?.aiPaused, true)
    }

    func testTakeoverReplyArrivingAfterStopLeavesAIPaused() async throws {
        let (store, backend) = await startedStore()
        let meeting = PreviewFixtures.meetingPCID
        backend.holdControl("control.acquire", afterApplying: true)
        let takeover = store.controlActions.takeOver(meeting)
        try await waitUntil { backend.pendingControlCount == 1 }

        await store.controlActions.stop(meeting)?.value
        backend.releaseControl("control.acquire")
        await takeover?.value

        XCTAssertEqual(store.control(for: meeting)?.aiPaused, true)
        XCTAssertEqual(bannerKind(store, meeting), .aiPaused)
        XCTAssertEqual(bannerKind(store, meeting)?.title, "AI 일시정지됨")
    }

    func testControlStateIsReadAgainWhateverItsGenAfterResubscribe() async throws {
        let (store, backend) = await startedStore()
        let qa = PreviewFixtures.qaLaptopID
        backend.base.emit(kind: EventKind.controlChanged, hostID: qa, payload: [
            "owner": .string("ai"), "owner_client": .string("Codex"), "gen": .int(50), "ai_paused": .bool(false),
        ])
        try await waitUntil { store.control(for: qa)?.gen == 50 }

        backend.base.endEventStreams(throwing: ClientError.disconnected)
        try await waitUntil { store.phase == .reconnecting }
        try await waitUntil { store.phase == .ready }

        let daemon = try await backend.base.controlGet(hostID: qa)
        try await waitUntil { store.control(for: qa) == daemon }
        XCTAssertEqual(store.host(qa)?.owner, daemon.owner)
    }

    // MARK: Banner

    func testBannerFollowsConnectionThenSessionThenControl() {
        let hosts = Dictionary(uniqueKeysWithValues: PreviewFixtures.hosts().map { ($0.hostID, $0) })
        let controls = PreviewFixtures.controls(for: Array(hosts.values))
        func kind(_ id: String, _ control: ControlState? = nil) -> ControlBannerKind {
            ControlBannerKind(host: hosts[id]!, control: control ?? controls[id])
        }

        XCTAssertEqual(kind(PreviewFixtures.devPCID), .ai(client: "Claude Code"))
        XCTAssertEqual(kind(PreviewFixtures.qaLaptopID), .human)
        XCTAssertEqual(kind(PreviewFixtures.meetingPCID), .observing)
        XCTAssertEqual(kind(PreviewFixtures.stagingID), .aiPaused)
        XCTAssertEqual(kind(PreviewFixtures.kioskID), .sessionLocked)
        XCTAssertEqual(kind(PreviewFixtures.buildServerID), .reconnecting)
        XCTAssertEqual(kind(PreviewFixtures.designID), .offline)
        XCTAssertEqual(kind(PreviewFixtures.labID), .blocked)

        var lockedAI = hosts[PreviewFixtures.devPCID]!
        lockedAI.session?.locked = true
        XCTAssertEqual(ControlBannerKind(host: lockedAI, control: controls[PreviewFixtures.devPCID]), .sessionLocked)
        var reconnectingAI = hosts[PreviewFixtures.devPCID]!
        reconnectingAI.state = ConnectionState.reconnecting.rawValue
        XCTAssertEqual(ControlBannerKind(host: reconnectingAI, control: controls[PreviewFixtures.devPCID]), .reconnecting)
        let pausedWhileObserving = ControlState(owner: "none", gen: 3, aiPaused: true)
        XCTAssertEqual(kind(PreviewFixtures.meetingPCID, pausedWhileObserving), .aiPaused)
    }

    func testToolbarOffersTakeOverOnlyWhileObserving() async throws {
        let backend = PreviewBackend(scenario: .standard, stepDelay: .zero)
        let hosts = PreviewFixtures.hosts()
        let store = AppStore.seeded(backend: backend, defaults: defaults, hosts: hosts, selectedHostID: PreviewFixtures.devPCID)
        var offering: [String] = []
        var pills: [String] = []
        for host in hosts {
            let model = WorkspaceToolbarModel(store: store, host: host)
            if model.showsTakeOver { offering.append(host.hostID) }
            if model.showsPill { pills.append(host.hostID) }
        }
        XCTAssertEqual(offering, [PreviewFixtures.meetingPCID])
        XCTAssertFalse(pills.contains(PreviewFixtures.designID))
        XCTAssertFalse(pills.contains(PreviewFixtures.labID))
        XCTAssertTrue(pills.contains(PreviewFixtures.buildServerID))
    }

    // MARK: Tabs and layout

    func testTabsAreNumberedOneToSevenInOrder() {
        XCTAssertEqual(WorkspaceTab.allCases, [.screen, .terminal, .files, .processes, .logs, .debugger, .qa])
        XCTAssertEqual(WorkspaceTab.allCases.map(\.number), Array(1...7))
        XCTAssertEqual(Set(WorkspaceTab.allCases.map(\.title)).count, 7)
    }

    func testTabAndInspectorChoicesPersist() async throws {
        let first = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)
        XCTAssertEqual(first.layout.tab, .screen)
        XCTAssertTrue(first.layout.showsInspector)
        first.layout.tab = .files
        first.layout.showsInspector = false
        first.layout.inspectorPane = .info

        let second = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)
        XCTAssertEqual(second.layout.tab, .files)
        XCTAssertFalse(second.layout.showsInspector)
        XCTAssertEqual(second.layout.inspectorPane, .info)
    }
}
