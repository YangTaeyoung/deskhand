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
        let store = AppStore(backend: backend, defaults: defaults)
        await store.start()
        return (store, backend)
    }

    // MARK: Brief

    func testEmergencyStopCallsControlStop() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        XCTAssertEqual(store.control(for: dev)?.owner, "ai")

        await store.controlActions.stop(dev)?.value

        XCTAssertEqual(backend.controlCalls, [ControlCall(method: "control.stop", hostID: dev)])
        XCTAssertEqual(store.control(for: dev)?.owner, "human")
        XCTAssertEqual(store.control(for: dev)?.aiPaused, true)
        XCTAssertFalse(store.controlActions.isRunning(.stop, on: dev))
        XCTAssertNil(store.controlActions.failure(for: dev))
    }

    func testTakeoverRequiresConfirmationWhenAIOwns() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID

        let task = store.controlActions.takeOver(dev)

        XCTAssertNil(task)
        XCTAssertEqual(store.controlActions.pendingTakeover, ControlActions.PendingTakeover(hostID: dev, client: "Claude Code"))
        XCTAssertTrue(backend.controlCalls.isEmpty)

        await store.controlActions.confirmTakeover()?.value

        XCTAssertNil(store.controlActions.pendingTakeover)
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

        XCTAssertNil(store.controlActions.pendingTakeover)
        XCTAssertEqual(backend.controlCalls, [ControlCall(method: "control.acquire", hostID: meeting, owner: .human, force: false)])
        XCTAssertEqual(store.control(for: meeting)?.owner, "human")
    }

    func testCancelledTakeoverSendsNothing() async throws {
        let (store, backend) = await startedStore()
        store.controlActions.takeOver(PreviewFixtures.devPCID)
        store.controlActions.cancelTakeover()

        XCTAssertNil(store.controlActions.pendingTakeover)
        XCTAssertNil(store.controlActions.confirmTakeover())
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

        XCTAssertEqual(store.controlActions.failure(for: dev)?.action, .stop)
        XCTAssertEqual(store.controlActions.failure(for: dev)?.error.code, "OFFLINE")
        XCTAssertFalse(store.controlActions.isRunning(.stop, on: dev))
        XCTAssertNil(store.actionError)
        XCTAssertEqual(store.control(for: dev)?.owner, "ai")

        backend.failControl("control.stop", with: nil)
        await store.controlActions.retry(dev)?.value

        XCTAssertNil(store.controlActions.failure(for: dev))
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
