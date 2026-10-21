import DeskhandClient
import Foundation
import XCTest
@testable import DeskhandUI

@MainActor
final class AddHostRepairTests: XCTestCase {
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUp() async throws {
        suiteName = "deskhand-tests-\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suiteName)
    }

    override func tearDown() async throws {
        defaults.removePersistentDomain(forName: suiteName)
    }

    private var store: AppStore?

    private func flow(alias: String) async -> (AddHostFlow, GatedBackend) {
        let backend = GatedBackend(PreviewBackend(scenario: .standard, stepDelay: .zero))
        let store = AppStore(backend: backend, defaults: defaults,
                             backoff: .init(initial: .milliseconds(100), maximum: .milliseconds(200)))
        self.store = store
        await store.start()
        let flow = store.presentAddHost()
        flow.alias = alias
        flow.name = "새 PC"
        return (flow, backend)
    }

    private func isConfirming(_ flow: AddHostFlow) -> Bool {
        if case .confirmRepair = flow.stage { true } else { false }
    }

    func testAgentPairedElsewhereAsksForConfirmationWithoutForcing() async throws {
        let (flow, backend) = await flow(alias: PreviewFixtures.pairedElsewhereAlias)
        flow.start()
        try await waitUntil { isConfirming(flow) }

        XCTAssertEqual(backend.hostAddCalls, [HostAddCall(name: "새 PC", ssh: PreviewFixtures.pairedElsewhereAlias, force: false)])
        guard case .confirmRepair(let error) = flow.stage else { return XCTFail() }
        XCTAssertEqual(error.nextAction, OperationError.hostAddForce)
    }

    func testConfirmingRepairsWithForceAndConnects() async throws {
        let (flow, backend) = await flow(alias: PreviewFixtures.pairedElsewhereAlias)
        flow.start()
        try await waitUntil { isConfirming(flow) }
        flow.confirmRepair()
        try await waitUntil { if case .succeeded = flow.stage { true } else { false } }

        XCTAssertEqual(backend.hostAddCalls.map(\.force), [false, true])
    }

    func testDecliningReturnsToFormWithoutAnotherCall() async throws {
        let (flow, backend) = await flow(alias: PreviewFixtures.pairedElsewhereAlias)
        flow.start()
        try await waitUntil { isConfirming(flow) }
        flow.edit()

        XCTAssertEqual(flow.stage, .form)
        XCTAssertEqual(flow.alias, PreviewFixtures.pairedElsewhereAlias)
        XCTAssertEqual(backend.hostAddCalls.count, 1)
    }

    func testConfirmOutsideTheConfirmationStageDoesNothing() async throws {
        let (flow, backend) = await flow(alias: "windows")
        flow.confirmRepair()

        XCTAssertEqual(flow.stage, .form)
        XCTAssertTrue(backend.hostAddCalls.isEmpty)
    }

    func testOrdinaryInstallFailureDoesNotOfferRepair() async throws {
        let (flow, backend) = await flow(alias: PreviewFixtures.failingAlias)
        flow.start()
        try await waitUntil { if case .failed = flow.stage { true } else { false } }

        XCTAssertFalse(isConfirming(flow))
        flow.confirmRepair()
        XCTAssertEqual(backend.hostAddCalls.map(\.force), [false])
    }

    func testInvalidArgumentWithoutForceActionDoesNotOfferRepair() async throws {
        let (flow, backend) = await flow(alias: "windows")
        backend.failHostAdd(with: JobReceipt(
            jobID: "j", status: .failed, error: EnvelopeError(code: "INVALID_ARGUMENT", message: "이름이 비었습니다")))
        flow.start()
        try await waitUntil { if case .failed = flow.stage { true } else { false } }

        XCTAssertFalse(isConfirming(flow))
    }

    func testRetryAfterOrdinaryFailureNeverForces() async throws {
        let (flow, backend) = await flow(alias: PreviewFixtures.failingAlias)
        flow.start()
        try await waitUntil { if case .failed = flow.stage { true } else { false } }
        flow.start()
        try await waitUntil { backend.hostAddCalls.count == 2 }

        XCTAssertEqual(backend.hostAddCalls.map(\.force), [false, false])
    }

    func testRepairFailureAfterConfirmationShowsTheFailureNotAnotherConfirmation() async throws {
        let (flow, backend) = await flow(alias: PreviewFixtures.pairedElsewhereAlias)
        flow.start()
        try await waitUntil { isConfirming(flow) }
        backend.failHostAdd(with: JobReceipt(
            jobID: "j", status: .failed, error: EnvelopeError(code: "TIMEOUT", message: "PC가 응답하지 않습니다", retryable: true)))
        flow.confirmRepair()
        try await waitUntil { if case .failed = flow.stage { true } else { false } }

        XCTAssertFalse(isConfirming(flow))
    }

    func testForcedRequestStillConfirmsNothingWhenDaemonAsksForForceAgain() async throws {
        let (flow, backend) = await flow(alias: "windows")
        backend.failHostAdd(with: JobReceipt(
            jobID: "j", status: .failed,
            error: EnvelopeError(code: "INVALID_ARGUMENT", message: "이미 페어링", nextAction: OperationError.hostAddForce)))
        flow.start()
        try await waitUntil { isConfirming(flow) }
        flow.confirmRepair()
        try await waitUntil { backend.hostAddCalls.count == 2 }
        try await waitUntil { if case .failed = flow.stage { true } else { false } }

        XCTAssertEqual(backend.hostAddCalls.map(\.force), [false, true])
    }
}
