import DeskhandClient
import Foundation
import Observation
import struct DeskhandClient.Host

/// State of the add-computer sheet: the form, then `host.add` progress through the five
/// installation steps, ending connected or failed.
@MainActor
@Observable
public final class AddHostFlow: Identifiable {
    public enum Stage: Equatable {
        case form
        case running
        case failed(OperationError)
        case succeeded(Host)
    }

    /// Longest wait for the next progress event before the flow reports a stall.
    public static let stallTimeout: Duration = .seconds(120)

    public let id = UUID()
    public var alias = ""
    public var name = ""
    public private(set) var aliases: [String] = []
    public private(set) var aliasesLoaded = false
    public private(set) var stage: Stage = .form
    public private(set) var steps: [StepItem] = AddHostFlow.pendingSteps()
    /// Rejection of the form values by the daemon (for example a duplicate name).
    public private(set) var formError: OperationError?

    @ObservationIgnored private weak var store: AppStore?
    @ObservationIgnored private let backend: any Backend
    @ObservationIgnored private var task: Task<Void, Never>?
    @ObservationIgnored private var watchdog: Task<Void, Never>?
    @ObservationIgnored private var lastProgress = ContinuousClock.now

    init(store: AppStore) {
        self.store = store
        self.backend = store.backend
        Task { [weak self] in await self?.loadAliases() }
    }

    /// Sheet state for previews and snapshots.
    public init(backend: any Backend, alias: String, name: String, aliases: [String], stage: Stage, steps: [StepItem]) {
        self.backend = backend
        self.alias = alias
        self.name = name
        self.aliases = aliases
        self.aliasesLoaded = true
        self.stage = stage
        self.steps = steps
    }

    /// The five steps, pending, each with what it will do as its detail line.
    public static func pendingSteps() -> [StepItem] {
        InstallStep.allCases.map { StepItem(id: $0.rawValue, title: $0.title, detail: $0.activeDescription, status: .pending) }
    }

    public var trimmedAlias: String { alias.trimmingCharacters(in: .whitespacesAndNewlines) }

    /// The display name, falling back to the alias.
    public var effectiveName: String {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? trimmedAlias : trimmed
    }

    public var canStart: Bool {
        guard !trimmedAlias.isEmpty, !trimmedAlias.contains(where: \.isWhitespace) else { return false }
        switch stage {
        case .form, .failed: return true
        case .running, .succeeded: return false
        }
    }

    func loadAliases() async {
        aliases = await backend.sshAliases()
        aliasesLoaded = true
    }

    // MARK: Actions

    public func start() {
        guard canStart else { return }
        let alias = trimmedAlias
        let name = effectiveName
        formError = nil
        stage = .running
        steps = Self.pendingSteps()
        apply(InstallProgress(name: name, step: .probe, status: .active))
        task?.cancel()
        let events = backend.events(kinds: [EventKind.installProgress], hostID: nil)
        let backend = self.backend
        task = Task { [weak self] in
            do {
                let receipt = try await backend.hostAdd(name: name, ssh: alias)
                switch receipt.status {
                case .failed, .cancelled:
                    self?.fail(receipt.error.map(OperationError.init) ?? OperationError(message: .ui("error.installFailed", "설치하지 못했습니다")))
                    return
                case .succeeded:
                    await self?.complete(hostID: receipt.hostID, name: name)
                    return
                case .running, .waitingForUser, .unknown:
                    break
                }
                for try await event in events {
                    guard let self else { return }
                    guard let progress = InstallProgress(event: event), progress.name == name else { continue }
                    self.apply(progress)
                    if progress.status == .failed {
                        self.fail(progress.error ?? OperationError(message: .ui("error.installFailed", "설치하지 못했습니다")))
                        return
                    }
                    if progress.step == .verify, progress.status == .done {
                        await self.complete(hostID: progress.hostID, name: name)
                        return
                    }
                }
                guard !Task.isCancelled else { return }
                self?.fail(OperationError(message: .ui("error.installStreamEnded", "데몬에서 설치 진행 상황을 더 받지 못했습니다")))
            } catch is CancellationError {
            } catch {
                self?.reject(OperationError(error))
            }
        }
        startWatchdog()
    }

    /// Returns to the form, keeping the entered values.
    public func edit() {
        stop()
        stage = .form
        steps = Self.pendingSteps()
    }

    /// Closes the sheet. A running installation continues in the daemon.
    public func dismiss() {
        stop()
        store?.addHost = nil
    }

    /// Closes the sheet after success and selects the new computer when it is still in the list.
    public func finish() {
        if case .succeeded(let host) = stage, let store, store.host(host.hostID) != nil {
            store.selectedHostID = host.hostID
        }
        dismiss()
    }

    // MARK: Progress

    func apply(_ progress: InstallProgress) {
        guard let index = InstallStep.allCases.firstIndex(of: progress.step) else { return }
        lastProgress = .now
        for i in steps.indices where i < index && steps[i].status != .done {
            steps[i].status = .done
        }
        switch progress.status {
        case .active:
            steps[index].status = .active
            steps[index].detail = progress.detail ?? progress.step.activeDescription
        case .done:
            steps[index].status = .done
            steps[index].detail = progress.detail ?? steps[index].detail
        case .failed:
            steps[index].status = .failed
            steps[index].detail = progress.error?.message ?? progress.detail
        }
    }

    private func complete(hostID: String?, name: String) async {
        guard let store else { return }
        await store.reloadHosts()
        let host = hostID.flatMap(store.host) ?? store.hosts.first { $0.name == name }
        guard let host else {
            return fail(OperationError(message: .ui("error.addedHostMissing", "PC를 추가했지만 목록에서 찾지 못했습니다")))
        }
        for i in steps.indices { steps[i].status = .done }
        stopWatchdog()
        stage = .succeeded(host)
    }

    /// Marks the running step failed and shows the error.
    private func fail(_ error: OperationError) {
        if !steps.contains(where: { $0.status == .failed }), let i = steps.firstIndex(where: { $0.status == .active }) {
            steps[i].status = .failed
            steps[i].detail = error.message
        }
        stopWatchdog()
        stage = .failed(error)
    }

    /// The daemon refused the request before any step ran: back to the form with the error.
    private func reject(_ error: OperationError) {
        let started = steps.contains { $0.status == .done || $0.status == .failed }
        if started { return fail(error) }
        stopWatchdog()
        steps = Self.pendingSteps()
        formError = error
        stage = .form
    }

    private func startWatchdog() {
        watchdog?.cancel()
        lastProgress = .now
        watchdog = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                guard let self, self.stage == .running else { return }
                if ContinuousClock.now - self.lastProgress > Self.stallTimeout {
                    self.task?.cancel()
                    self.fail(OperationError(code: "TIMEOUT", message: .ui("error.installStalled", "2분 동안 설치 진행 상황이 바뀌지 않았습니다")))
                    return
                }
            }
        }
    }

    private func stopWatchdog() {
        watchdog?.cancel()
        watchdog = nil
    }

    private func stop() {
        task?.cancel()
        task = nil
        stopWatchdog()
    }
}

extension InstallStep {
    public var title: String {
        switch self {
        case .probe: .ui("install.probe", "원격 PC 확인")
        case .copy: .ui("install.copy", "에이전트 복사")
        case .initialize: .ui("install.init", "에이전트 초기화")
        case .install: .ui("install.install", "에이전트 설치")
        case .verify: .ui("install.verify", "연결 확인")
        }
    }

    /// What the step does, shown while it runs and the daemon has sent no detail.
    public var activeDescription: String {
        switch self {
        case .probe: .ui("install.probe.detail", "SSH로 접속해 OS와 아키텍처를 확인합니다")
        case .copy: .ui("install.copy.detail", "에이전트 실행 파일을 원격 임시 폴더로 보냅니다")
        case .initialize: .ui("install.init.detail", "에이전트 키를 만들고 이 Mac과 페어링합니다")
        case .install: .ui("install.install.detail", "로그인할 때 시작하는 예약 작업과 방화벽 규칙을 등록합니다")
        case .verify: .ui("install.verify.detail", "보안 연결로 에이전트에 접속합니다")
        }
    }
}
