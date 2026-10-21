import DeskhandClient
import Foundation
import Synchronization
import struct DeskhandClient.Host

/// QA runs of the preview with their steps and evidence files. Running runs advance about one step
/// a second (scaled by the step delay) once the QA tab first reads them, and follow the cancel rules
/// of local-api.md §10.
final class PreviewQAStore: Sendable {
    struct Live: Sendable {
        var cancels = 0
        /// A cancel stopped the setup or steps.
        var stopSteps = false
        /// A later cancel during teardown stopped teardown too.
        var stopTeardown = false
        /// A step stopped or was skipped because of a cancel.
        var cancelledSomething = false
    }

    struct State: Sendable {
        var runs: [String: QARun] = [:]
        var evidence: [String: PreviewQAEvidence] = [:]
        var live: [String: Live] = [:]
        /// Sample runs that run once the QA tab first reads the runs.
        var waiting: [String] = []
        var count = 0
    }

    let state: Mutex<State>

    init(includesFixtures: Bool) {
        var initial = State()
        if includesFixtures {
            let sample = PreviewQAFixtures.runs()
            for run in sample.runs {
                initial.runs[run.runID] = run
                if run.isRunning {
                    initial.live[run.runID] = Live()
                    initial.waiting.append(run.runID)
                }
            }
            initial.evidence = sample.evidence
        }
        state = Mutex(initial)
    }

    static func notFound(_ runID: String) -> RPCErrorInfo {
        RPCErrorInfo(message: "QA 실행 \(runID)를 찾을 수 없습니다. qa.list로 실행을 확인하세요", code: "NOT_FOUND")
    }
}

extension PreviewBackend {
    // MARK: Reads

    public func qaRun(_ request: QARunRequest) async throws -> QARun {
        let host = try listedHost(request.hostID)
        let path = request.scenarioPath
        guard path.hasPrefix("/") else {
            throw RPCErrorInfo(message: "scenario_path \"\(path)\"는 이 Mac의 절대 경로여야 합니다", code: "INVALID_ARGUMENT")
        }
        await pause(fraction: 0.6)
        let file = (path as NSString).lastPathComponent
        let lower = file.lowercased()
        guard lower.hasSuffix(".yaml") || lower.hasSuffix(".yml") else {
            throw RPCErrorInfo(message: "\(path):1: 시나리오의 최상위가 매핑이 아닙니다. name과 steps를 쓰세요", code: "INVALID_ARGUMENT")
        }
        if lower.contains("broken") || lower.contains("invalid") {
            throw RPCErrorInfo(message: "\(path):14: 모르는 단계 종류 asert_ui입니다. assert_ui처럼 쓰세요", code: "INVALID_ARGUMENT")
        }
        if let bad = request.vars.keys.sorted().first(where: { !QAVarsDraft.isValidName($0) }) {
            throw RPCErrorInfo(message: "\(path):1: 변수 이름 \"\(bad)\"는 쓸 수 없습니다. 영문자·숫자·_로 쓰고 숫자로 시작하지 마세요",
                               code: "INVALID_ARGUMENT")
        }
        let id = "q-" + Self.ulid()
        var run = try qa.state.withLock { s -> QARun in
            if let running = s.runs.values.first(where: { $0.hostID == host.hostID && $0.isRunning }) {
                throw RPCErrorInfo(message: "\(host.name)에서 끝나지 않은 QA 실행 \(running.runID)가 있습니다. 끝난 뒤 다시 실행하세요",
                                   code: "IN_PROGRESS", retryable: true, nextAction: "check_state")
            }
            s.count += 1
            var scenario = PreviewQAFixtures.fixtureSmoke(runID: id)
            scenario.name = Self.scenarioName(path: path) ?? String(file.split(separator: ".").first ?? Substring(file))
            scenario.file = file
            var run = PreviewQAFixtures.run(id: id, job: "j-" + Self.ulid(), host: host.hostID, scenario: scenario, started: .now,
                                            status: .running, outcomes: [:], rev: 1).run
            run.scenarioPath = path
            s.runs[id] = run
            s.live[id] = PreviewQAStore.Live()
            return run
        }
        emitQA(run, step: nil)
        if simulatesTime {
            Task { await self.simulateQA(id) }
        }
        run = qa.state.withLock { $0.runs[id] } ?? run
        return run
    }

    public func qaGet(runID: String) async throws -> QARun {
        resumeLiveRuns()
        guard let run = qa.state.withLock({ $0.runs[runID] }) else { throw PreviewQAStore.notFound(runID) }
        return run
    }

    public func qaList(hostID: String?, limit: Int, cursor: String?) async throws -> QARunPage {
        resumeLiveRuns()
        if let hostID { _ = try listedHost(hostID) }
        let all = qa.state.withLock { s in
            s.runs.values.filter { hostID == nil || $0.hostID == hostID }.sorted { $0.startedAt.date > $1.startedAt.date }
        }
        let offset = cursor.flatMap(Int.init) ?? 0
        let page = Array(all.dropFirst(offset).prefix(max(limit, 1)))
        let next = offset + page.count < all.count ? String(offset + page.count) : nil
        return QARunPage(runs: page.map(\.summary), nextCursor: next)
    }

    public func qaCancel(runID: String) async throws -> QARun {
        let (run, emitted) = try qa.state.withLock { s -> (QARun, Bool) in
            guard var run = s.runs[runID] else { throw PreviewQAStore.notFound(runID) }
            guard run.isRunning else {
                if run.runStatus == .cancelled { return (run, false) }
                throw RPCErrorInfo(message: "QA 실행 \(runID)는 이미 \(QAFormat.title(run.runStatus)) 상태로 끝났습니다. 취소할 필요가 없습니다",
                                   code: "INVALID_ARGUMENT")
            }
            var live = s.live[runID] ?? PreviewQAStore.Live()
            let phase = run.currentStep.flatMap { index in run.steps?.first { $0.index == index } }?.stepPhase
            if phase == .teardown {
                if live.cancels > 0 { live.stopTeardown = true }
            } else {
                live.stopSteps = true
            }
            live.cancels += 1
            s.live[runID] = live
            run.cancelRequested = true
            run.rev += 1
            run.updatedAt = Timestamp(date: .now)
            s.runs[runID] = run
            return (run, true)
        }
        if emitted { emitQA(run, step: nil) }
        return run
    }

    public func artifactsExport(jobID: String, dir: String) async throws -> ArtifactExport {
        guard dir.hasPrefix("/") else {
            throw RPCErrorInfo(message: "dir \"\(dir)\"는 이 Mac의 폴더 절대 경로여야 합니다. /Users/…처럼 주세요", code: "INVALID_ARGUMENT")
        }
        guard let run = qa.state.withLock({ s in s.runs.values.first { $0.jobID == jobID } }) else {
            throw RPCErrorInfo(message: "job \(jobID)를 찾을 수 없습니다", code: "NOT_FOUND")
        }
        await pause(fraction: 1.2)
        guard let reportDir = run.reportDir, !run.isRunning else {
            throw RPCErrorInfo(message: "job \(jobID)에는 내보낼 산출물이 없습니다. artifacts.list로 확인하세요", code: "NOT_FOUND")
        }
        if run.runStatus == .blocked, run.hostID == PreviewFixtures.devPCID {
            throw RPCErrorInfo(message: "report.html의 sha256이 기록과 다릅니다. 산출물이 바뀌었을 수 있으니 원본 폴더 \(reportDir)를 확인하세요",
                               code: "HASH_MISMATCH")
        }
        let evidence = (run.steps ?? []).flatMap(\.evidence)
        let target = (dir as NSString).appendingPathComponent(jobID)
        return ArtifactExport(jobID: jobID, dir: target, files: evidence.count + 4, bytes: evidence.reduce(46_210) { $0 + $1.size },
                              manifest: (target as NSString).appendingPathComponent("manifest.json"))
    }

    public func evidenceData(path: String, maxBytes: Int) async throws -> Data {
        guard let content = qa.state.withLock({ $0.evidence[path] }) else {
            if let screen = PreviewFixtures.timelineScreen(path: path) {
                return await Task.detached { PreviewQAImages.png(screen) }.value
            }
            throw RPCErrorInfo(message: "증거 파일 \(path)를 찾을 수 없습니다", code: "NOT_FOUND")
        }
        switch content {
        case .screen(let screen):
            return await Task.detached { PreviewQAImages.png(screen) }.value
        case .text(let text):
            return Data(text.utf8).prefix(max(maxBytes, 0))
        }
    }

    // MARK: Simulation

    /// Starts the sample's running runs the first time the QA tab reads the runs.
    private func resumeLiveRuns() {
        guard simulatesTime else { return }
        let ids = qa.state.withLock { s -> [String] in
            defer { s.waiting = [] }
            return s.waiting
        }
        for id in ids {
            Task { await self.simulateQA(id) }
        }
    }

    /// Runs the pending steps of a run one by one, then ends it.
    func simulateQA(_ runID: String) async {
        while let next = nextStep(runID) {
            let skip = qa.state.withLock { s -> String? in
                guard let live = s.live[runID] else { return nil }
                if next.stepPhase == .teardown { return live.stopTeardown ? "취소로 정리 단계를 멈춰 실행하지 않았습니다" : nil }
                return live.stopSteps ? "취소로 실행하지 않았습니다" : nil
            }
            if let skip {
                finishStep(runID, next.index, status: .notRun, message: skip, cancelled: true)
                continue
            }
            let started = Date.now
            update(runID, step: next.index) { step in
                step.status = QAStepStatus.running.rawValue
                step.startedAt = Timestamp(date: started)
            } run: { $0.currentStep = next.index }
            let stopped = await work(runID, phase: next.stepPhase ?? .steps, seconds: next.action == "push" ? 1.6 : 1.0)
            let ms = Int(Date.now.timeIntervalSince(started) * 1000)
            if stopped {
                finishStep(runID, next.index, status: .blocked, message: "취소로 멈췄습니다", ms: ms, cancelled: true)
                continue
            }
            let outcome = previewOutcome(runID, next)
            finishStep(runID, next.index, status: outcome.status, message: outcome.message, error: outcome.error, ms: ms,
                       assertions: outcome.assertions, evidence: outcome.evidence)
            if (outcome.status == .fail || outcome.status == .blocked), !next.optional, !next.continueOnFail, next.stepPhase != .teardown {
                skipRemaining(runID, message: PreviewQAFixtures.notRunMessage(index: next.index, name: next.name, status: outcome.status))
            }
        }
        finish(runID)
    }

    /// The first pending step.
    private func nextStep(_ runID: String) -> QAStep? {
        qa.state.withLock { s in s.runs[runID]?.steps?.first { $0.stepStatus == .pending } }
    }

    /// Waits about `seconds` scaled by the step delay; true when a cancel stopped the step.
    private func work(_ runID: String, phase: QAStepPhase, seconds: Double) async -> Bool {
        let total = max(seconds * delayScale, 0.05)
        let end = Date.now.addingTimeInterval(total)
        while Date.now < end {
            try? await Task.sleep(for: .milliseconds(50))
            let stop = qa.state.withLock { s -> Bool in
                guard let live = s.live[runID] else { return true }
                return phase == .teardown ? live.stopTeardown : live.stopSteps
            }
            if stop { return true }
        }
        return false
    }

    private struct PreviewOutcome {
        var status: QAStepStatus
        var message: String?
        var error: QAErrorInfo?
        var assertions: [QAAssertion] = []
        var evidence: [Artifact] = []
    }

    /// What a step does on the computer as it is now: blocked when it is not connected, when the
    /// step needs mouse or keyboard input, when a person holds control or a capability is not ready.
    private func previewOutcome(_ runID: String, _ step: QAStep) -> PreviewOutcome {
        guard step.action != "sleep" else { return PreviewOutcome(status: .pass) }
        let hostID = qa.state.withLock { $0.runs[runID]?.hostID } ?? ""
        let current = hostAndOwner(hostID)
        guard let current, current.host.connection == .ready || current.host.connection == .degraded else {
            let name = current?.host.name ?? hostID
            let message = "\(name) PC의 연결을 찾지 못했습니다. host.list로 PC를 확인하세요"
            return PreviewOutcome(status: .blocked, message: message, error: QAErrorInfo(code: "OFFLINE", message: message))
        }
        if ["click", "type", "key"].contains(step.action) {
            let message = PreviewQAFixtures.noInputMessage
            return PreviewOutcome(status: .blocked, message: message, error: QAErrorInfo(code: "CAPABILITY_UNSUPPORTED", message: message))
        }
        if ["launch", "ui_act", "close"].contains(step.action), current.owner == ControlOwner.human.rawValue {
            let message = PreviewQAFixtures.humanMessage
            return PreviewOutcome(status: .blocked, message: message, error: QAErrorInfo(code: "CONTROL_HELD_BY_HUMAN", message: message))
        }
        let needs: String
        switch step.action {
        case "push", "assert_file", "collect": needs = "files"
        case "launch", "exec": needs = "exec"
        case "screenshot": needs = "screen"
        default: needs = "uia"
        }
        if let cap = current.host.caps[needs], cap.capabilityState != .ready {
            let reason = cap.reason ?? cap.state
            let message = "이 PC는 \(step.action) 단계를 할 수 없습니다(\(needs): \(reason)). 그 기능이 있는 PC나 에이전트에서 실행하세요"
            return PreviewOutcome(status: .blocked, message: message, error: QAErrorInfo(code: "CAPABILITY_UNSUPPORTED", message: message))
        }
        var outcome = PreviewOutcome(status: .pass)
        let spec = PreviewQAFixtures.Spec(phase: step.stepPhase ?? .steps, name: step.name, action: step.action, line: step.line,
                                          params: step.params, ms: 0)
        outcome.message = PreviewQAFixtures.defaultMessage(spec, .pass)
        switch step.action {
        case "wait_window":
            outcome.assertions = [QAAssertion(kind: "window", expected: step.params["title"] ?? .string("Deskhand Fixture"),
                                              actual: .string("Deskhand Fixture"), passed: true)]
        case "assert_ui":
            outcome.assertions = [QAAssertion(kind: "exists", expected: .bool(true), actual: .bool(true), passed: true)]
            if let contains = step.params["text_contains"] {
                outcome.assertions.append(QAAssertion(kind: "text_contains", expected: contains,
                                                      actual: .string("You: 안녕 QA\nBot: echo 안녕 QA\n[done]"), passed: true))
            }
        case "assert_screen_text":
            var items: [JSONValue] = []
            if case .array(let list) = step.params["contains"] { items = list }
            for item in items {
                outcome.assertions.append(QAAssertion(kind: "contains", expected: item,
                                                      actual: .string("Deskhand Fixture\n한글 라벨\nMessage\nSend\nTranscript"), passed: true))
            }
        case "screenshot":
            guard let run = qa.state.withLock({ $0.runs[runID] }) else { break }
            let name = String(format: "%02d", step.index) + "-screenshot-" + (step.params["name"]?.stringValue ?? "screenshot") + ".png"
            let path = PreviewQAFixtures.reportDir(run.jobID) + "/evidence/" + name
            qa.state.withLock { $0.evidence[path] = .screen(.fixtureDone) }
            outcome.evidence = [Artifact(artifactID: "a-" + Self.ulid(), jobID: run.jobID, kind: "screenshot", name: name, path: path,
                                         size: 191_204, mime: "image/png", sha256: String(repeating: "a", count: 64),
                                         createdAt: Timestamp(date: .now))]
        default:
            break
        }
        return outcome
    }

    private func finishStep(_ runID: String, _ index: Int, status: QAStepStatus, message: String?, error: QAErrorInfo? = nil, ms: Int? = nil,
                            assertions: [QAAssertion] = [], evidence: [Artifact] = [], cancelled: Bool = false) {
        if cancelled { qa.state.withLock { $0.live[runID]?.cancelledSomething = true } }
        update(runID, step: index) { step in
            step.status = status.rawValue
            step.message = message
            step.error = error
            step.durationMs = ms
            step.assertions = assertions
            step.evidence = evidence
        } run: { run in
            let steps = run.steps ?? []
            run.stepsPassed = steps.filter { $0.stepStatus == .pass }.count
            run.stepsFailed = steps.filter { $0.stepStatus == .fail }.count
            run.stepsBlocked = steps.filter { $0.stepStatus == .blocked }.count
            run.stepsNotRun = steps.filter { $0.stepStatus == .notRun }.count
        }
    }

    /// Marks the pending setup and steps steps `not_run`.
    private func skipRemaining(_ runID: String, message: String) {
        let pending = qa.state.withLock { s in
            (s.runs[runID]?.steps ?? []).filter { $0.stepStatus == .pending && $0.stepPhase != .teardown }.map(\.index)
        }
        for index in pending {
            finishStep(runID, index, status: .notRun, message: message)
        }
    }

    /// Changes a step and the run, raises the rev and emits `qa.updated` with the step.
    private func update(_ runID: String, step index: Int, _ change: (inout QAStep) -> Void, run changeRun: (inout QARun) -> Void) {
        let changed = qa.state.withLock { s -> (QARun, QAStep)? in
            guard var run = s.runs[runID], var steps = run.steps, let i = steps.firstIndex(where: { $0.index == index }) else { return nil }
            change(&steps[i])
            run.steps = steps
            changeRun(&run)
            run.rev += 1
            run.updatedAt = Timestamp(date: .now)
            s.runs[runID] = run
            return (run, steps[i])
        }
        if let (run, step) = changed { emitQA(run, step: step) }
    }

    /// Ends a run by the contract's status rules and writes its report paths.
    private func finish(_ runID: String) {
        let finished = qa.state.withLock { s -> QARun? in
            guard var run = s.runs[runID], run.isRunning else { return nil }
            let steps = (run.steps ?? []).filter { !$0.optional }
            let status: QARunStatus
            if s.live[runID]?.cancelledSomething == true {
                status = .cancelled
            } else if steps.contains(where: { $0.stepStatus == .fail }) {
                status = .failed
            } else if steps.contains(where: { $0.stepStatus == .blocked }) {
                status = .blocked
            } else {
                status = .passed
            }
            run.status = status.rawValue
            run.currentStep = nil
            run.finishedAt = Timestamp(date: .now)
            run.updatedAt = run.finishedAt ?? run.updatedAt
            let dir = PreviewQAFixtures.reportDir(run.jobID)
            run.reportDir = dir
            run.report = QAReportPaths(dir: dir)
            run.rev += 1
            s.runs[runID] = run
            s.live[runID] = nil
            return run
        }
        if let finished { emitQA(finished, step: nil) }
    }

    private func emitQA(_ run: QARun, step: QAStep?) {
        var payload: [String: JSONValue] = ["run": Self.payload(run.summary)]
        if let step { payload["step"] = Self.payload(step) }
        emit(kind: EventKind.qaUpdated, hostID: run.hostID, payload: payload)
    }

    /// The `name:` of the scenario file at `path`, when it can be read.
    private static func scenarioName(path: String) -> String? {
        guard let handle = FileHandle(forReadingAtPath: path) else { return nil }
        defer { try? handle.close() }
        guard let data = try? handle.read(upToCount: 64 << 10) else { return nil }
        let text = String(decoding: data, as: UTF8.self)
        for line in text.split(separator: "\n") where line.hasPrefix("name:") {
            let value = line.dropFirst(5).trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "'\""))
            return value.isEmpty ? nil : value
        }
        return nil
    }

    /// Ten characters of Crockford base32, as the daemon's IDs end.
    static func ulid() -> String {
        let alphabet = Array("0123456789ABCDEFGHJKMNPQRSTVWXYZ")
        return String((0..<10).map { _ in alphabet[Int.random(in: 0..<alphabet.count)] })
    }
}
