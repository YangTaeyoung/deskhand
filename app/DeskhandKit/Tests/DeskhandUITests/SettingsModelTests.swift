import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

@MainActor
final class SettingsModelTests: XCTestCase {
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUp() async throws {
        suiteName = "deskhand-tests-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)
    }

    override func tearDown() async throws {
        defaults.removePersistentDomain(forName: suiteName)
    }

    private func started(_ backend: any Backend, loginItem: MemoryLoginItem? = nil) async -> AppStore {
        let store = AppStore(backend: backend, defaults: defaults, backoff: .init(initial: .milliseconds(10), maximum: .milliseconds(20)),
                             loginItem: loginItem)
        await store.start()
        return store
    }

    func testRestartReturnsToIdleAndReadsTheVersionsAgain() async throws {
        let backend = GatedBackend(PreviewBackend(scenario: .standard, stepDelay: .zero))
        let store = await started(backend)
        let settings = store.settings
        settings.confirmingRestart = true

        await settings.restartDaemon()

        XCTAssertEqual(backend.restartCalls, 1)
        XCTAssertEqual(settings.restart, .idle)
        XCTAssertFalse(settings.confirmingRestart)
        XCTAssertEqual(settings.daemonInfo?.version, "0.1.0")
        try await waitUntil { store.phase == .ready }
    }

    func testRestartFailureStaysOnScreen() async throws {
        let backend = GatedBackend(PreviewBackend(scenario: .standard, stepDelay: .zero))
        let store = await started(backend)
        backend.failRestart(with: OperationError(message: "deskhand 명령을 찾지 못했습니다"))

        await store.settings.restartDaemon()

        XCTAssertEqual(store.settings.restart, .failed(OperationError(message: "deskhand 명령을 찾지 못했습니다")))
        XCTAssertEqual(store.settings.daemonStatus, .running)
    }

    func testRestartFailureShowsTheCommandsOutput() async throws {
        let backend = GatedBackend(PreviewBackend(scenario: .standard, stepDelay: .zero))
        let store = await started(backend)
        backend.failRestart(with: DaemonCommandError.failed(command: "deskhand daemon start", status: 1, stderr: "address already in use"))

        await store.settings.restartDaemon()

        XCTAssertEqual(store.settings.restart,
                       .failed(OperationError(message: "deskhand daemon start 명령이 실패했습니다 (종료 코드 1): address already in use")))
    }

    func testDisconnectWaitsForConfirmation() async throws {
        let store = await started(PreviewBackend(scenario: .standard, stepDelay: .zero))
        let settings = store.settings
        settings.confirmDisconnect()
        XCTAssertTrue(settings.hostActions.isEmpty)

        settings.pendingDisconnect = store.host(PreviewFixtures.meetingPCID)
        settings.confirmDisconnect()
        XCTAssertNil(settings.pendingDisconnect)
        XCTAssertEqual(settings.hostActions[PreviewFixtures.meetingPCID], .disconnect)
        try await waitUntil { settings.hostActions.isEmpty }
    }

    func testAppVersionComesFromTheBundleUnlessSet() {
        let store = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)
        XCTAssertEqual(store.settings.appVersion, LiveBackend.bundleVersion)
    }

    func testDaemonStatusFollowsTheStore() async throws {
        let store = await started(PreviewBackend(scenario: .daemonUnavailable, stepDelay: .zero))
        guard case .unavailable = store.settings.daemonStatus else {
            return XCTFail("expected unavailable, got \(store.settings.daemonStatus)")
        }
        XCTAssertEqual(store.settings.daemonStatus.dot, .blocked)
        XCTAssertEqual(DaemonStatus.running.dot, .ready)
        XCTAssertEqual(DaemonStatus.restarting.dot, .reconnecting)
    }

    func testLaunchAtLoginFollowsTheLoginItem() async throws {
        let item = MemoryLoginItem()
        let store = await started(PreviewBackend(scenario: .standard, stepDelay: .zero), loginItem: item)
        let settings = store.settings
        XCTAssertFalse(settings.launchesAtLogin)

        settings.setLaunchesAtLogin(true)
        XCTAssertTrue(settings.launchesAtLogin)
        XCTAssertEqual(item.status, .enabled)
        XCTAssertNil(settings.loginItemError)

        item.failure = NSError(domain: "SMAppServiceErrorDomain", code: 1, userInfo: [NSLocalizedDescriptionKey: "서명되지 않은 앱입니다"])
        settings.setLaunchesAtLogin(false)
        XCTAssertTrue(settings.launchesAtLogin)
        XCTAssertEqual(settings.loginItemError, "서명되지 않은 앱입니다")
    }

    func testRemovalWaitsForConfirmationThenReloadsTheList() async throws {
        let store = await started(PreviewBackend(scenario: .standard, stepDelay: .zero))
        let settings = store.settings
        settings.confirmRemoval(uninstall: false)
        XCTAssertTrue(settings.hostActions.isEmpty)

        let host = try XCTUnwrap(store.host(PreviewFixtures.meetingPCID))
        settings.pendingRemoval = host
        settings.confirmRemoval(uninstall: false)
        XCTAssertNil(settings.pendingRemoval)
        XCTAssertEqual(settings.hostActions[host.hostID], .remove)

        try await waitUntil { store.host(host.hostID) == nil && settings.hostActions.isEmpty }
    }

    func testFailedRemovalKeepsTheErrorPerComputer() async throws {
        let backend = GatedBackend(PreviewBackend(scenario: .standard, stepDelay: .zero))
        let store = await started(backend)
        let settings = store.settings
        backend.failHostRemove(with: OperationError(code: "AUTH_REQUIRED", message: "SSH로 접속하지 못했습니다"))
        settings.pendingRemoval = store.host(PreviewFixtures.labID)
        settings.confirmRemoval(uninstall: true)

        try await waitUntil { settings.hostErrors[PreviewFixtures.labID] != nil }
        XCTAssertEqual(settings.hostErrors[PreviewFixtures.labID]?.code, "AUTH_REQUIRED")
        XCTAssertNil(settings.hostErrors[PreviewFixtures.devPCID])
        XCTAssertNotNil(store.host(PreviewFixtures.labID))

        settings.dismissError(PreviewFixtures.labID)
        XCTAssertNil(settings.hostErrors[PreviewFixtures.labID])
    }

    func testRepairWithoutConfirmationDoesNothing() async throws {
        let store = await started(PreviewBackend(scenario: .standard, stepDelay: .zero))
        store.settings.confirmRepair()
        XCTAssertTrue(store.settings.hostActions.isEmpty)
    }

    func testPerComputerPanesFallBackToTheSelectedComputer() async throws {
        let store = await started(PreviewBackend(scenario: .standard, stepDelay: .zero))
        store.selectedHostID = PreviewFixtures.qaLaptopID
        XCTAssertEqual(store.settings.host(nil)?.hostID, PreviewFixtures.qaLaptopID)
        XCTAssertEqual(store.settings.host("removed")?.hostID, PreviewFixtures.qaLaptopID)
        XCTAssertEqual(store.settings.host(PreviewFixtures.labID)?.hostID, PreviewFixtures.labID)
    }

    func testPaneIsRemembered() {
        let store = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)
        XCTAssertEqual(store.settings.pane, .general)
        store.settings.pane = .privacy
        let again = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)
        XCTAssertEqual(again.settings.pane, .privacy)
    }
}
