import DeskhandClient
import Foundation
import XCTest
import struct DeskhandClient.Host
@testable import DeskhandUI

/// 다시 페어링 is one flow wherever it starts — the workspace banner, the sidebar, the PC menu
/// or the settings window: a confirmation, then one `host.repair` at a time with its progress,
/// and its failure kept with the remedy.
@MainActor
final class RepairFlowTests: XCTestCase {
    private let blocked = PreviewFixtures.labID

    private func store() -> (AppStore, GatedBackend) {
        let backend = GatedBackend(PreviewBackend(stepDelay: .zero))
        let store = AppStore.seeded(backend: backend, defaults: UserDefaults(suiteName: "repair-\(UUID())")!,
                                    hosts: PreviewFixtures.hosts(), selectedHostID: PreviewFixtures.labID)
        return (store, backend)
    }

    func testRepairAsksFirstThenRunsOnceWithProgressAndKeepsAFailureWithItsRemedy() async throws {
        let (store, backend) = store()
        let host = try XCTUnwrap(store.host(blocked))
        store.requestRepair(host)
        XCTAssertEqual(store.pendingRepair?.hostID, blocked)
        XCTAssertEqual(backend.repairCalls, 0, "nothing runs before the confirmation")

        backend.holdRepair(true)
        store.confirmRepair(host)
        XCTAssertNil(store.pendingRepair)
        try await waitUntil { backend.pendingRepairCount == 1 }
        XCTAssertTrue(store.isRepairing(blocked))
        XCTAssertEqual(store.settings.hostActions[blocked], .repair, "the settings window shows the same run")
        store.confirmRepair(host)
        store.settings.pendingRepair = host
        store.settings.confirmRepair()
        XCTAssertEqual(backend.repairCalls, 1, "a second request while one runs starts nothing")

        backend.resumeRepair(with: .success(JobReceipt(jobID: "j-1", hostID: blocked, status: .failed,
                                                       error: EnvelopeError(code: "AUTH_REQUIRED", message: "SSH 인증에 실패했습니다"))))
        try await waitUntil { !store.isRepairing(blocked) }
        let failure = try XCTUnwrap(store.repairError(for: blocked))
        XCTAssertEqual(failure.code, "AUTH_REQUIRED")
        XCTAssertEqual(failure.message, "SSH 인증에 실패했습니다")
        XCTAssertNotNil(failure.fix)
        XCTAssertEqual(store.settings.hostErrors[blocked], failure)

        store.dismissRepairError(for: blocked)
        XCTAssertNil(store.repairError(for: blocked))
    }

    func testASuccessfulRepairReadsTheComputersAgain() async throws {
        let (store, backend) = store()
        let host = try XCTUnwrap(store.host(blocked))
        let lists = backend.hostListCalls
        store.confirmRepair(host)
        try await waitUntil { !store.isRepairing(blocked) && backend.hostListCalls > lists }
        XCTAssertNil(store.repairError(for: blocked))
        XCTAssertEqual(backend.repairCalls, 1)
    }

    func testAThrownRepairIsKeptAsItsFailure() async throws {
        let (store, backend) = store()
        let host = try XCTUnwrap(store.host(blocked))
        backend.holdRepair(true)
        store.confirmRepair(host)
        try await waitUntil { backend.pendingRepairCount == 1 }
        backend.resumeRepair(with: .failure(OperationError(code: "TIMEOUT", message: "PC가 답하지 않았습니다")))
        try await waitUntil { store.repairError(for: blocked) != nil }
        XCTAssertEqual(store.repairError(for: blocked)?.code, "TIMEOUT")
    }
}
