import DeskhandClient
import Foundation
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

@MainActor
final class AppStoreConsistencyTests: XCTestCase {
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUp() async throws {
        suiteName = "deskhand-tests-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)
    }

    override func tearDown() async throws {
        defaults.removePersistentDomain(forName: suiteName)
    }

    private func startedStore(_ scenario: PreviewScenario = .standard) async -> (AppStore, GatedBackend) {
        let backend = GatedBackend(PreviewBackend(scenario: scenario, stepDelay: .zero))
        let store = AppStore(backend: backend, defaults: defaults,
                             backoff: .init(initial: .milliseconds(100), maximum: .milliseconds(200)))
        await store.start()
        return (store, backend)
    }

    /// Emits a marker event and waits for it, so every event emitted before it has been handled.
    private func drainEvents(_ store: AppStore, _ backend: GatedBackend) async throws {
        backend.base.emit(kind: EventKind.connectionState, hostID: PreviewFixtures.qaLaptopID, payload: ["state": .string("degraded")])
        try await waitUntil { store.connection(for: PreviewFixtures.qaLaptopID) == .degraded }
    }

    // MARK: Host list

    func testOlderHostListReplyArrivingLastIsIgnored() async throws {
        let (store, backend) = await startedStore()
        let all = try await backend.base.hostList()
        backend.holdHostList(true)

        let older = Task { await store.reloadHosts() }
        try await waitUntil { backend.pendingHostListCount == 1 }
        let newer = Task { await store.reloadHosts() }
        try await waitUntil { backend.pendingHostListCount == 2 }

        backend.resumeHostList(at: 1, with: .success(Array(all.prefix(2))))
        _ = await newer.value
        XCTAssertFalse(store.isLoadingHosts)
        backend.resumeHostList(at: 0, with: .success(all))
        _ = await older.value

        XCTAssertEqual(store.hosts.map(\.hostID), all.prefix(2).map(\.hostID))
        XCTAssertFalse(store.isLoadingHosts)
    }

    /// The development PC as `host.list` would answer, in `state` at `generation`.
    private func dev(_ store: AppStore, state: String, generation: Int) throws -> Host {
        var host = try XCTUnwrap(store.host(PreviewFixtures.devPCID))
        host.state = state
        host.connectionGeneration = generation
        return host
    }

    private func connectionState(_ backend: GatedBackend, _ state: String, generation: Int) {
        backend.base.emit(kind: EventKind.connectionState, hostID: PreviewFixtures.devPCID,
                          payload: ["state": .string(state), "connection_generation": .int(generation)])
    }

    func testAHostListReplyBuiltBeforeANewerConnectionEventKeepsTheEventsState() async throws {
        let (store, backend) = await startedStore()
        let generation = try XCTUnwrap(store.host(PreviewFixtures.devPCID)).connectionGeneration
        let others = try await backend.base.hostList().filter { $0.hostID != PreviewFixtures.devPCID }
        backend.holdHostList(true)
        let reload = Task { await store.reloadHosts() }
        try await waitUntil { backend.pendingHostListCount == 1 }
        connectionState(backend, "ready", generation: generation + 1)
        try await waitUntil { store.host(PreviewFixtures.devPCID)?.connectionGeneration == generation + 1 }

        backend.resumeHostList(at: 0, with: .success([try dev(store, state: "reconnecting", generation: generation)] + others))
        _ = await reload.value
        XCTAssertEqual(store.host(PreviewFixtures.devPCID)?.connectionGeneration, generation + 1)
        XCTAssertEqual(store.connection(for: PreviewFixtures.devPCID), .ready)
    }

    func testAHostListReplyOfANewerGenerationReplacesTheEventsState() async throws {
        let (store, backend) = await startedStore()
        let generation = try XCTUnwrap(store.host(PreviewFixtures.devPCID)).connectionGeneration
        let others = try await backend.base.hostList().filter { $0.hostID != PreviewFixtures.devPCID }
        backend.holdHostList(true)
        let reload = Task { await store.reloadHosts() }
        try await waitUntil { backend.pendingHostListCount == 1 }
        connectionState(backend, "reconnecting", generation: generation)
        try await waitUntil { store.connection(for: PreviewFixtures.devPCID) == .reconnecting }

        backend.resumeHostList(at: 0, with: .success([try dev(store, state: "ready", generation: generation + 1)] + others))
        _ = await reload.value
        XCTAssertEqual(store.host(PreviewFixtures.devPCID)?.connectionGeneration, generation + 1)
        XCTAssertEqual(store.connection(for: PreviewFixtures.devPCID), .ready)
    }

    func testAConnectionEventOfAnOlderGenerationIsIgnored() async throws {
        let (store, backend) = await startedStore()
        let generation = try XCTUnwrap(store.host(PreviewFixtures.devPCID)).connectionGeneration
        connectionState(backend, "ready", generation: generation + 2)
        try await waitUntil { store.host(PreviewFixtures.devPCID)?.connectionGeneration == generation + 2 }
        connectionState(backend, "offline", generation: generation + 1)
        try await drainEvents(store, backend)
        XCTAssertEqual(store.connection(for: PreviewFixtures.devPCID), .ready)
        XCTAssertEqual(store.host(PreviewFixtures.devPCID)?.connectionGeneration, generation + 2)

        connectionState(backend, "degraded", generation: generation + 2)
        try await waitUntil { store.connection(for: PreviewFixtures.devPCID) == .degraded }
    }

    func testAfterTheDaemonComesBackItsGenerationsAreTakenAsTheyAre() async throws {
        let (store, backend) = await startedStore()
        connectionState(backend, "ready", generation: 40)
        try await waitUntil { store.host(PreviewFixtures.devPCID)?.connectionGeneration == 40 }
        let others = try await backend.base.hostList().filter { $0.hostID != PreviewFixtures.devPCID }
        backend.holdHostList(true)
        store.daemonReconnected()
        try await waitUntil { backend.pendingHostListCount == 1 }
        backend.resumeHostList(at: 0, with: .success([try dev(store, state: "ready", generation: 1)] + others))
        try await waitUntil { store.host(PreviewFixtures.devPCID)?.connectionGeneration == 1 }
        connectionState(backend, "degraded", generation: 1)
        try await waitUntil { store.connection(for: PreviewFixtures.devPCID) == .degraded }
    }

    func testCancelledReloadSetsNoError() async throws {
        let (store, backend) = await startedStore()
        backend.holdHostList(true)
        let reload = Task { await store.reloadHosts() }
        try await waitUntil { backend.pendingHostListCount == 1 }
        reload.cancel()
        backend.resumeHostList(at: 0, with: .failure(CancellationError()))
        _ = await reload.value

        XCTAssertNil(store.hostsError)
        XCTAssertFalse(store.isLoadingHosts)
    }

    func testEmptyListKeepsPersistedSelection() async throws {
        let (store, backend) = await startedStore()
        store.selectedHostID = PreviewFixtures.qaLaptopID
        backend.holdHostList(true)
        let reload = Task { await store.reloadHosts() }
        try await waitUntil { backend.pendingHostListCount == 1 }
        backend.resumeHostList(at: 0, with: .success([]))
        _ = await reload.value

        XCTAssertTrue(store.hosts.isEmpty)
        XCTAssertEqual(store.selectedHostID, PreviewFixtures.qaLaptopID)
        XCTAssertEqual(defaults.string(forKey: AppStore.selectionKey), PreviewFixtures.qaLaptopID)
    }

    func testRemovingSelectedHostSelectsFirstRemaining() async throws {
        let (store, _) = await startedStore()
        store.selectedHostID = PreviewFixtures.devPCID
        store.remove(PreviewFixtures.devPCID, uninstall: false)

        try await waitUntil { store.host(PreviewFixtures.devPCID) == nil }
        XCTAssertEqual(store.selectedHostID, PreviewFixtures.qaLaptopID)
        XCTAssertEqual(defaults.string(forKey: AppStore.selectionKey), PreviewFixtures.qaLaptopID)
    }

    // MARK: Events

    func testEventsForRemovedHostAreIgnored() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        backend.base.emit(kind: EventKind.hostRemoved, hostID: dev)
        let entry = TimelineEntry(seq: 900, ts: Timestamp(date: .now), hostID: dev, actor: "ai", kind: "input",
                                  summary: "클릭", outcome: "ok")
        backend.base.emit(kind: EventKind.controlChanged, hostID: dev, payload: ["owner": .string("human"), "gen": .int(5), "ai_paused": .bool(false)])
        backend.base.emit(kind: EventKind.timelineAppended, hostID: dev, payload: ["entry": PreviewBackend.payload(entry)])
        backend.base.emit(kind: EventKind.connectionState, hostID: dev, payload: ["state": .string("ready")])
        try await drainEvents(store, backend)

        XCTAssertNil(store.host(dev))
        XCTAssertNil(store.control(for: dev))
        XCTAssertTrue(store.timeline(for: dev).isEmpty)
        await store.loadTimeline(for: dev)
        XCTAssertTrue(store.timeline(for: dev).isEmpty)
    }

    func testHostAddedEventAppliesHostWithoutReload() async throws {
        let (store, backend) = await startedStore()
        let calls = backend.hostListCalls
        let host = PreviewFixtures.newHost(id: "host-event", name: "새 PC", alias: "new-pc")
        backend.base.emit(kind: EventKind.hostAdded, hostID: host.hostID, payload: ["host": PreviewBackend.payload(host)])
        try await drainEvents(store, backend)

        XCTAssertEqual(store.host("host-event")?.name, "새 PC")
        XCTAssertEqual(store.host("host-event")?.caps.uia?.state, "needs_user")
        XCTAssertEqual(backend.hostListCalls, calls)
    }

    func testDisplayChangedUpdatesDisplays() async throws {
        let (store, backend) = await startedStore()
        let display = DisplayInfo(index: 0, name: "DISPLAY2", adapter: "Intel", x: 0, y: 0, width: 1920, height: 1080,
                                  dpi: 96, scale: 1, primary: true, refreshHz: 144)
        backend.base.emit(kind: EventKind.displayChanged, hostID: PreviewFixtures.devPCID,
                          payload: ["displays": PreviewBackend.payload([display])])
        try await drainEvents(store, backend)

        XCTAssertEqual(store.host(PreviewFixtures.devPCID)?.displays, [display])
    }

    func testEventStreamEndResubscribesAndReloadsHosts() async throws {
        let (store, backend) = await startedStore()
        XCTAssertEqual(backend.hostListCalls, 1)

        backend.base.endEventStreams(throwing: ClientError.disconnected)
        try await waitUntil { store.phase == .reconnecting }
        try await waitUntil { store.phase == .ready }
        XCTAssertEqual(backend.hostListCalls, 2)

        backend.base.emit(kind: EventKind.connectionState, hostID: PreviewFixtures.devPCID, payload: ["state": .string("degraded")])
        try await waitUntil { store.connection(for: PreviewFixtures.devPCID) == .degraded }
    }

    // MARK: Timeline

    func testTimelineIsNewestFirstAndAppendedEntriesSurviveReload() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        let fixtureSeqs = PreviewFixtures.timeline(hostID: dev).map(\.seq)
        await store.loadTimeline(for: dev)
        XCTAssertEqual(store.timeline(for: dev).map(\.seq), fixtureSeqs)

        let newSeq = fixtureSeqs[0] + 1
        let entry = TimelineEntry(seq: newSeq, ts: Timestamp(date: .now), hostID: dev, actor: "human", kind: "control",
                                  summary: "제어권을 가져왔습니다", outcome: "ok")
        backend.base.emit(kind: EventKind.timelineAppended, hostID: dev, payload: ["entry": PreviewBackend.payload(entry)])
        try await waitUntil { store.timeline(for: dev).first?.seq == newSeq }

        await store.loadTimeline(for: dev)
        XCTAssertEqual(store.timeline(for: dev).map(\.seq), [newSeq] + fixtureSeqs)
    }

    func testTimelineEventsOfAComputerWhoseTimelineWasNeverShownAreNotKept() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        let entry = TimelineEntry(seq: 900, ts: Timestamp(date: .now), hostID: dev, actor: "ai", kind: "input",
                                  summary: "클릭", outcome: "ok")
        backend.base.emit(kind: EventKind.timelineAppended, hostID: dev, payload: ["entry": PreviewBackend.payload(entry)])
        try await drainEvents(store, backend)
        XCTAssertNil(store.timelineState(for: dev))
        XCTAssertTrue(store.timeline(for: dev).isEmpty)
    }

    func testOlderTimelineEntriesLoadFromTheNextCursor() async throws {
        let (store, _) = await startedStore()
        let dev = PreviewFixtures.devPCID
        let fixtureSeqs = PreviewFixtures.timeline(hostID: dev).map(\.seq)
        XCTAssertGreaterThan(fixtureSeqs.count, 2)
        await store.loadTimeline(for: dev, limit: 2)
        XCTAssertEqual(store.timeline(for: dev).map(\.seq), Array(fixtureSeqs.prefix(2)))
        XCTAssertEqual(store.timelineState(for: dev)?.olderCursor, String(fixtureSeqs[1]))

        await store.loadOlderTimeline(for: dev)

        XCTAssertEqual(store.timeline(for: dev).map(\.seq), fixtureSeqs)
        XCTAssertEqual(store.timelineState(for: dev)?.hasOlder, false)
        XCTAssertEqual(store.timelineState(for: dev)?.isLoadingOlder, false)
    }

    func testTimelineFailureStaysInline() async throws {
        let (store, backend) = await startedStore()
        let dev = PreviewFixtures.devPCID
        backend.failTimeline(with: OperationError(code: "OFFLINE", message: "연결되어 있지 않습니다"))
        await store.loadTimeline(for: dev)
        XCTAssertEqual(store.timelineError(for: dev)?.code, "OFFLINE")
        XCTAssertNil(store.actionError)

        backend.failTimeline(with: nil)
        await store.loadTimeline(for: dev)
        XCTAssertNil(store.timelineError(for: dev))
        XCTAssertEqual(store.timeline(for: dev).count, PreviewFixtures.timeline(hostID: dev).count)
    }

    // MARK: Disconnect

    func testDisconnectAsksFirstAndClosesOnlyAfterConfirmation() async throws {
        let (store, _) = await startedStore()
        let dev = try XCTUnwrap(store.host(PreviewFixtures.devPCID))
        store.requestDisconnect(dev)
        XCTAssertEqual(store.pendingDisconnect?.hostID, dev.hostID)
        XCTAssertEqual(store.connection(for: dev.hostID), .ready)
        store.disconnect(dev.hostID)
        XCTAssertNil(store.pendingDisconnect)
        try await waitUntil { store.connection(for: dev.hostID) == .offline }
    }

    // MARK: Add host

    func testFinishDoesNotSelectHostRemovedMeanwhile() async throws {
        let (store, backend) = await startedStore()
        store.selectedHostID = PreviewFixtures.devPCID
        let flow = store.presentAddHost()
        flow.alias = "media"
        flow.name = "미디어 PC"
        flow.start()
        try await waitUntil { if case .succeeded = flow.stage { true } else { false } }
        guard case .succeeded(let added) = flow.stage else { return XCTFail() }

        backend.base.emit(kind: EventKind.hostRemoved, hostID: added.hostID)
        try await waitUntil { store.host(added.hostID) == nil }
        flow.finish()

        XCTAssertEqual(store.selectedHostID, PreviewFixtures.devPCID)
        XCTAssertNil(store.addHost)
    }
}
