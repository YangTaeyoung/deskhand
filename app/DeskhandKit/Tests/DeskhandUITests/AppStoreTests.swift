import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

@MainActor
final class AppStoreTests: XCTestCase {
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUp() async throws {
        suiteName = "deskhand-tests-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)
    }

    override func tearDown() async throws {
        defaults.removePersistentDomain(forName: suiteName)
    }

    func testEventUpdatesHostState() async throws {
        let backend = PreviewBackend(scenario: .standard, stepDelay: .zero)
        let store = AppStore(backend: backend, defaults: defaults)
        await store.start()
        XCTAssertEqual(store.phase, .ready)
        XCTAssertEqual(store.connection(for: PreviewFixtures.devPCID), .ready)

        backend.emit(kind: EventKind.connectionState, hostID: PreviewFixtures.devPCID, payload: ["state": .string("reconnecting")])

        try await waitUntil { store.connection(for: PreviewFixtures.devPCID) == .reconnecting }
        XCTAssertEqual(store.host(PreviewFixtures.devPCID)?.state, "reconnecting")
    }

    func testControlEventUpdatesControl() async throws {
        let backend = PreviewBackend(scenario: .standard, stepDelay: .zero)
        let store = AppStore(backend: backend, defaults: defaults)
        await store.start()
        XCTAssertEqual(store.control(for: PreviewFixtures.devPCID)?.owner, "ai")

        backend.emit(kind: EventKind.controlChanged, hostID: PreviewFixtures.devPCID, payload: [
            "owner": .string("human"), "gen": .int(9), "ai_paused": .bool(true),
        ])

        try await waitUntil { store.control(for: PreviewFixtures.devPCID)?.owner == "human" }
        XCTAssertEqual(store.control(for: PreviewFixtures.devPCID)?.aiPaused, true)
        XCTAssertEqual(store.host(PreviewFixtures.devPCID)?.owner, "human")
    }

    func testSelectingHostPersists() async throws {
        let first = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)
        await first.start()
        first.selectedHostID = PreviewFixtures.qaLaptopID
        XCTAssertEqual(defaults.string(forKey: AppStore.selectionKey), PreviewFixtures.qaLaptopID)

        let second = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)
        await second.start()
        XCTAssertEqual(second.selectedHostID, PreviewFixtures.qaLaptopID)
    }

    func testUnknownPersistedSelectionFallsBackToFirstHost() async throws {
        defaults.set("removed-host", forKey: AppStore.selectionKey)
        let store = AppStore(backend: PreviewBackend(scenario: .standard, stepDelay: .zero), defaults: defaults)
        await store.start()
        XCTAssertEqual(store.selectedHostID, store.hosts.first?.hostID)
    }

    func testDaemonFailureShowsUnavailable() async throws {
        let store = AppStore(backend: PreviewBackend(scenario: .daemonUnavailable, stepDelay: .zero), defaults: defaults)
        await store.start()
        guard case .daemonUnavailable(let failure) = store.phase else {
            return XCTFail("phase \(store.phase)")
        }
        XCTAssertEqual(failure.logPath, Paths.daemonLog)
    }

    func testAddHostReachesConnected() async throws {
        let backend = PreviewBackend(scenario: .empty, stepDelay: .zero)
        let store = AppStore(backend: backend, defaults: defaults)
        await store.start()
        XCTAssertTrue(store.hosts.isEmpty)

        let flow = store.presentAddHost()
        flow.alias = "windows"
        flow.name = "개발 PC"
        flow.start()

        try await waitUntil { if case .succeeded = flow.stage { true } else { false } }
        XCTAssertEqual(flow.steps.map(\.status), Array(repeating: .done, count: InstallStep.allCases.count))
        XCTAssertEqual(store.hosts.map(\.name), ["개발 PC"])
        flow.finish()
        XCTAssertNil(store.addHost)
        XCTAssertEqual(store.selectedHostID, store.hosts.first?.hostID)
    }

    func testAddHostFailureShowsFailedStep() async throws {
        let backend = PreviewBackend(scenario: .empty, stepDelay: .zero)
        let store = AppStore(backend: backend, defaults: defaults)
        await store.start()

        let flow = store.presentAddHost()
        flow.alias = PreviewFixtures.failingAlias
        flow.start()

        try await waitUntil { if case .failed = flow.stage { true } else { false } }
        guard case .failed(let failure) = flow.stage else { return XCTFail() }
        XCTAssertEqual(failure.code, "ELEVATION_REQUIRED")
        XCTAssertEqual(flow.steps.first { $0.status == .failed }?.id, InstallStep.install.rawValue)
        XCTAssertTrue(store.hosts.isEmpty)
    }
}
