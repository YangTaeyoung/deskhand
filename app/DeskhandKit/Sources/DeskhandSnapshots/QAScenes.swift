import DeskhandClient
import DeskhandUI
import SwiftUI
import struct DeskhandClient.Host

/// QA tab scenes at 1440×900 and narrow variants. The tab is seeded from the preview's sample runs
/// and evidence; nothing is read from the backend.
@MainActor
enum QAScenes {
    typealias Setup = WorkspaceScenes.Setup
    typealias Samples = PreviewQASamples

    static let dev = PreviewFixtures.devPCID
    static let narrowSize = CGSize(width: 1024, height: 720)

    struct Tab {
        /// The runs, built when the scene renders; the sample runs when nil.
        var runs: (@MainActor () -> [QARun])? = nil
        var showsAllHosts = false
        var selected: String?
        var selectedSteps: [String: Int] = [:]
        var phase: LoadPhase = .loaded
        var cancels: [String: QACancelProgress] = [:]
        var exports: [String: QAExportState] = [:]
        var viewer: QAEvidenceSelection?
        var notice: DiagnosticsNotice?
        var listShown: Bool?
        var filter: QARunFilter = .all
        var search = ""
        var stepsSplit = 0.46
        var reloadError: OperationError?
        var refreshErrors: [String: OperationError] = [:]
    }

    static func seed(_ store: AppStore, hostID: String, _ tab: Tab) {
        let runs = tab.runs?() ?? Samples.runs()
        let host = QAScope.host(hostID)
        var listings: [QAScope: QAModel.Listing] = [
            .all: QAModel.Listing(ids: Set(runs.map(\.runID)), phase: tab.phase),
            host: QAModel.Listing(ids: Set(runs.filter { $0.hostID == hostID }.map(\.runID)), phase: tab.phase),
        ]
        if tab.phase != .loaded {
            listings[.all]?.ids = []
            listings[host]?.ids = []
        }
        listings[host]?.reloadError = tab.reloadError
        var selection: [QAScope: String] = [:]
        if let selected = tab.selected {
            selection[.all] = selected
            selection[host] = selected
        }
        store.qa.seed(runs: runs, listings: listings, selection: selection, selectedSteps: tab.selectedSteps, refreshErrors: tab.refreshErrors,
                      cancels: tab.cancels,
                      exports: tab.exports, evidence: Samples.evidence(), viewer: tab.viewer, notice: tab.notice,
                      recent: recent, showsAllHosts: tab.showsAllHosts, filter: tab.filter, search: tab.search, listShown: tab.listShown)
        store.qa.stepsSplit = tab.stepsSplit
    }

    static let recent = [
        QARecentScenario(path: Samples.scenarioPath("fixture-smoke.yaml"), hostID: dev,
                         vars: [QAVarRow(name: "qa_root", value: "C:\\deskhand-qa")], timeout: .minutes(10)),
        QARecentScenario(path: Samples.scenarioPath("login-flow.yaml"), hostID: dev,
                         vars: [QAVarRow(name: "secret_password", value: "")], timeout: .scenario),
    ]

    static func scene(_ name: String, _ setup: Setup, _ tab: Tab, size: CGSize = WorkspaceScenes.size,
                      sheet: ((AppStore) -> AnyView)? = nil) -> SnapshotScene {
        SnapshotScene(name: name, size: size) {
            let store = WorkspaceScenes.store(setup)
            let _ = seed(store, hostID: setup.selected, tab)
            if let sheet {
                SheetStandIn(base: SplitStandIn(store: store)) { sheet(store) }
            } else {
                SplitStandIn(store: store)
            }
        }
    }

    // MARK: Runs

    /// The live run with its cancel taken while teardown runs.
    static func cancellingInTeardown() -> [QARun] {
        Samples.runs().map { run in
            guard run.runID == Samples.liveRunID, var steps = run.steps else { return run }
            var run = run
            let started = run.startedAt.date
            for i in steps.indices where steps[i].index <= 11 {
                steps[i].status = i == 3 ? QAStepStatus.blocked.rawValue : (i < 3 ? QAStepStatus.pass.rawValue : QAStepStatus.notRun.rawValue)
                steps[i].message = i == 3 ? "취소로 멈췄습니다" : (i < 3 ? steps[i].message : "취소로 실행하지 않았습니다")
                steps[i].durationMs = i <= 3 ? [1_840, 2_310, 640, 830][i] : nil
                steps[i].startedAt = i <= 3 ? Timestamp(date: started.addingTimeInterval(Double(i) * 1.5)) : nil
            }
            steps[11].status = QAStepStatus.running.rawValue
            steps[11].startedAt = Timestamp(date: Date.now.addingTimeInterval(-2))
            run.steps = steps
            run.currentStep = 12
            run.cancelRequested = true
            run.stepsPassed = 3
            run.stepsBlocked = 1
            run.stepsNotRun = 7
            run.rev += 12
            return run
        }
    }

    /// The live run with a cancel taken while its fifth step still stops.
    static func cancelling() -> [QARun] {
        Samples.runs().map { run in
            guard run.runID == Samples.liveRunID else { return run }
            var run = run
            run.cancelRequested = true
            run.rev += 1
            return run
        }
    }

    /// A finished run of 1,000 steps: setup, 998 steps with a failure at 731, and teardown.
    static func thousandSteps() -> [QARun] {
        let started = Date.now.addingTimeInterval(-25 * 60)
        var steps: [QAStep] = []
        for index in 1...1000 {
            let phase: QAStepPhase = index == 1 ? .setup : (index == 1000 ? .teardown : .steps)
            var step = QAStep(index: index, phase: phase, name: index == 1 ? "fixture 실행" : (index == 1000 ? "fixture 닫기" : "주문 \(index - 1) 확인"),
                              action: index == 1 ? "launch" : (index == 1000 ? "close" : "assert_ui"), line: 8 + index * 3,
                              params: ["name": .string("주문 \(index - 1)"), "role": .string("text")], status: .pass,
                              startedAt: Timestamp(date: started.addingTimeInterval(Double(index) * 1.2)), durationMs: 380 + index % 7 * 41)
            if index == 731 {
                step.status = QAStepStatus.fail.rawValue
                step.message = "기대와 다릅니다: text 기대 주문 730 완료, 실제 주문 730 보류"
                step.assertions = [QAAssertion(kind: "text", expected: .string("주문 730 완료"), actual: .string("주문 730 보류"), passed: false)]
            } else if index > 731, index < 1000 {
                step.status = QAStepStatus.notRun.rawValue
                step.message = "731단계(주문 730 확인)의 결과(실패) 때문에 실행하지 않았습니다"
                step.startedAt = nil
                step.durationMs = nil
            }
            steps.append(step)
        }
        let run = QARun(runID: "q-01JBF9K1000", jobID: "j-01JBF9K1000", hostID: dev, name: "주문 1,000건 확인",
                        scenarioPath: Samples.scenarioPath("orders-1000.yaml"), status: .failed, rev: 2004, startedAt: Timestamp(date: started),
                        finishedAt: Timestamp(date: started.addingTimeInterval(14 * 60 + 33)), stepsTotal: 1000, stepsPassed: 731,
                        stepsFailed: 1, stepsBlocked: 0, stepsNotRun: 268, reportDir: "/Users/qa/Library/Application Support/Deskhand/artifacts/j-01JBF9K1000",
                        steps: steps)
        return [run] + Samples.runs()
    }

    /// The meeting room run as if it ran on the offline design workstation.
    static func offlineRuns() -> [QARun] {
        Samples.runs().compactMap { run in
            guard run.runID == Samples.meetingRunID else { return run }
            var moved = run
            moved.hostID = PreviewFixtures.designID
            return moved
        }
    }

    // MARK: Sheets

    static func runSheet(_ draft: QARunDraft, error: OperationError? = nil, attempted: Bool = false) -> (AppStore) -> AnyView {
        { store in
            AnyView(RunSheet(store: store, model: store.qa, draft: draft, error: error, attempted: attempted, dismiss: {}))
        }
    }

    static var sheetDraft: QARunDraft {
        QARunDraft(scenarioPath: Samples.scenarioPath("fixture-smoke.yaml"), hostID: PreviewFixtures.meetingPCID,
                   vars: QAVarsDraft(rows: [QAVarRow(name: "qa_root", value: "C:\\deskhand-qa"), QAVarRow(name: "message", value: "안녕 QA"),
                                            QAVarRow(name: "secret_password", value: "hunter2-staging")]),
                   timeout: .minutes(10))
    }

    // MARK: Scenes

    static var all: [SnapshotScene] {
        let none = Setup(selected: dev, tab: .qa, inspector: nil)
        let failedEvidence = Samples.runs().first { $0.runID == Samples.failedRunID }?.steps?.first { $0.index == 7 }?.evidence ?? []
        let screenshot = failedEvidence.first { $0.kind == "screenshot" }?.artifactID ?? ""
        let uiTree = failedEvidence.first { $0.kind == "ui_tree" }?.artifactID ?? ""
        let brokenPath = Samples.scenarioPath("checkout-broken.yaml")
        return [
            scene("qa-empty", none, Tab(runs: { [] })),
            scene("qa-empty-all", none, Tab(runs: { [] }, showsAllHosts: true)),
            scene("qa-loading", none, Tab(phase: .loading)),
            scene("qa-error", none, Tab(phase: .failed(OperationError(ClientError.timeout(method: "qa.list"))))),
            scene("qa-runs", none, Tab(showsAllHosts: true, selected: Samples.loginRunID)),
            scene("qa-runs-filtered", none, Tab(selected: Samples.failedRunID, filter: .failed, search: "fixture")),
            scene("qa-run-passed", none, Tab(selected: Samples.loginRunID, selectedSteps: [Samples.loginRunID: 4])),
            scene("qa-run-failed", none, Tab(selected: Samples.failedRunID)),
            scene("qa-run-blocked", none, Tab(selected: Samples.blockedRunID)),
            scene("qa-run-blocked-unsupported", none, Tab(selected: Samples.blockedRunID, selectedSteps: [Samples.blockedRunID: 2])),
            scene("qa-run-running", none, Tab(selected: Samples.liveRunID)),
            scene("qa-run-cancelling", none, Tab(runs: cancelling, selected: Samples.liveRunID,
                                                cancels: [Samples.liveRunID: QACancelProgress(firstSent: true)])),
            scene("qa-run-cancel-teardown", none, Tab(runs: cancellingInTeardown, selected: Samples.liveRunID,
                                                     cancels: [Samples.liveRunID: QACancelProgress(firstSent: true)])),
            scene("qa-run-cancelled", none, Tab(selected: Samples.cancelledRunID)),
            scene("qa-run-cancel-late", none, Tab(selected: Samples.lateCancelRunID)),
            scene("qa-run-error", none, Tab(selected: Samples.restartRunID)),
            scene("qa-run-timeout", Setup(selected: PreviewFixtures.buildServerID, tab: .qa, inspector: nil), Tab(selected: Samples.timeoutRunID)),
            scene("qa-step-evidence", none, Tab(selected: Samples.failedRunID,
                                               viewer: QAEvidenceSelection(runID: Samples.failedRunID, stepIndex: 7, artifactID: screenshot))),
            scene("qa-step-evidence-text", none, Tab(selected: Samples.failedRunID,
                                                    viewer: QAEvidenceSelection(runID: Samples.failedRunID, stepIndex: 7, artifactID: uiTree))),
            scene("qa-run-sheet", none, Tab(selected: Samples.failedRunID), sheet: runSheet(sheetDraft)),
            scene("qa-run-sheet-error", none, Tab(selected: Samples.failedRunID), sheet: runSheet(
                QARunDraft(scenarioPath: brokenPath, hostID: PreviewFixtures.meetingPCID, vars: QAVarsDraft(rows: [QAVarRow(name: "qa_root", value: "C:\\deskhand-qa")])),
                error: OperationError(code: "INVALID_ARGUMENT", message: brokenPath + ":14: 모르는 단계 종류 asert_ui입니다. assert_ui처럼 쓰세요"))),
            scene("qa-run-sheet-busy", none, Tab(selected: Samples.liveRunID), sheet: runSheet(
                QARunDraft(scenarioPath: Samples.scenarioPath("login-flow.yaml"), hostID: dev,
                           vars: QAVarsDraft(rows: [QAVarRow(name: "secret_password", value: ""), QAVarRow(name: "2nd_user", value: "qa-bot2")])))),
            scene("qa-run-sheet-invalid", none, Tab(selected: Samples.failedRunID), sheet: runSheet(
                QARunDraft(scenarioPath: Samples.scenarioPath("login-flow.yaml"), hostID: PreviewFixtures.meetingPCID,
                           vars: QAVarsDraft(rows: [QAVarRow(name: "secret_password", value: ""), QAVarRow(name: "2nd_user", value: "qa-bot2"),
                                                    QAVarRow(name: "user", value: "qa-bot"), QAVarRow(name: "user", value: "qa-bot3")])),
                attempted: true)),
            scene("qa-run-1000-steps", none, Tab(runs: thousandSteps, selected: "q-01JBF9K1000")),
            scene("qa-reload-failed", none, Tab(selected: Samples.failedRunID,
                                               reloadError: OperationError(ClientError.timeout(method: "qa.list")),
                                               refreshErrors: [Samples.failedRunID: OperationError(ClientError.timeout(method: "qa.get"))])),
            scene("qa-export-failed", none, Tab(selected: Samples.blockedRunID, exports: [Samples.blockedRunID: .failed(OperationError(
                code: "HASH_MISMATCH",
                message: "report.html의 sha256이 기록과 다릅니다. 산출물이 바뀌었을 수 있으니 원본 폴더 ~/Library/Application Support/Deskhand/artifacts/j-01JBDQ5R7M를 확인하세요"),
                dir: "/Users/qa/Desktop")])),
            scene("qa-export-done", none, Tab(selected: Samples.failedRunID, exports: [Samples.failedRunID: .exported(ArtifactExport(
                jobID: "j-01JBF2K9QE", dir: FileManager.default.homeDirectoryForCurrentUser.path + "/Desktop/QA 증거/j-01JBF2K9QE", files: 6,
                bytes: 412_380, manifest: FileManager.default.homeDirectoryForCurrentUser.path + "/Desktop/QA 증거/j-01JBF2K9QE/manifest.json"))])),
            scene("qa-offline", Setup(selected: PreviewFixtures.designID, tab: .qa, inspector: nil),
                  Tab(runs: offlineRuns, selected: Samples.meetingRunID)),
            scene("qa-human-blocked-laptop", Setup(selected: PreviewFixtures.qaLaptopID, tab: .qa, inspector: nil), Tab(selected: Samples.laptopRunID)),
            scene("qa-run-failed-narrow", Setup(selected: dev, tab: .qa, inspector: nil), Tab(selected: Samples.failedRunID), size: narrowSize),
            scene("qa-run-failed-narrow-list", Setup(selected: dev, tab: .qa, inspector: nil), Tab(selected: Samples.failedRunID, listShown: true),
                  size: narrowSize),
            scene("qa-run-running-narrow", Setup(selected: dev, tab: .qa, inspector: nil), Tab(selected: Samples.liveRunID), size: narrowSize),
            scene("qa-run-failed-inspector", Setup(selected: dev, tab: .qa), Tab(selected: Samples.failedRunID)),
        ]
    }
}
