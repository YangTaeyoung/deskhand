import DeskhandClient
import Foundation
import struct DeskhandClient.Host

/// Sample computers, control states, timelines and diagnostics used by `PreviewBackend`,
/// the snapshot scenes and `--preview` launches. Times are relative to `now`.
public enum PreviewFixtures {
    public static let devPCID = "windows-dev"
    public static let qaLaptopID = "qa-laptop"
    public static let buildServerID = "build-server"
    public static let designID = "design-ws"
    public static let labID = "lab-pc"

    /// SSH alias whose simulated installation fails at the `install` step.
    public static let failingAlias = "media-pc"

    public static let sshAliases = ["windows", "qa-laptop", "build", "lab-pc", "media-pc", "nas"]

    static let allCapabilities = ["screen", "input", "uia", "exec", "files", "processes", "pty", "tunnel", "diag", "clipboard"]

    static func caps(_ state: String, reason: String? = nil, except overrides: [String: Capability] = [:]) -> HostCaps {
        var items: [String: Capability] = [:]
        for name in allCapabilities { items[name] = Capability(state: state, reason: reason) }
        for (name, cap) in overrides { items[name] = cap }
        return HostCaps(items: items)
    }

    static func display(width: Int = 3840, height: Int = 2160, scale: Double = 1.5, adapter: String) -> DisplayInfo {
        DisplayInfo(index: 0, name: #"\\.\DISPLAY1"#, adapter: adapter, x: 0, y: 0, width: width, height: height,
                    dpi: Int(96 * scale), scale: scale, primary: true, refreshHz: 60)
    }

    public static func hosts(now: Date = .now) -> [Host] {
        [
            Host(hostID: devPCID, name: "개발 PC", os: "windows", arch: "amd64", address: "192.168.0.24", port: 47600,
                 sshAlias: "windows", state: "ready", caps: caps("ready"),
                 session: SessionInfo(sessionID: 1, user: "taeyoung", connectState: "active", protocolKind: "console",
                                      locked: false, inputDesktop: "Default"),
                 displays: [display(adapter: "NVIDIA GeForce RTX 2070")],
                 owner: "ai", ownerClient: "Claude Code", agentVersion: "0.1.0", connectionGeneration: 4,
                 lastSeen: Timestamp(date: now)),
            Host(hostID: qaLaptopID, name: "QA 노트북", os: "windows", arch: "arm64", address: "192.168.0.31", port: 47600,
                 sshAlias: "qa-laptop", state: "ready",
                 caps: caps("ready", except: [
                     "uia": Capability(state: "unavailable", reason: "UI 자동화 도우미가 응답하지 않습니다"),
                     "tunnel": Capability(state: "unsupported", reason: "이 에이전트 버전은 터널을 지원하지 않습니다"),
                 ]),
                 session: SessionInfo(sessionID: 2, user: "qa", connectState: "active", protocolKind: "console",
                                      locked: false, inputDesktop: "Default"),
                 displays: [display(width: 2880, height: 1920, scale: 2, adapter: "Qualcomm Adreno")],
                 owner: "human", agentVersion: "0.1.0", connectionGeneration: 2,
                 lastSeen: Timestamp(date: now)),
            Host(hostID: buildServerID, name: "빌드 서버", os: "windows", arch: "amd64", address: "10.0.4.12", port: 47600,
                 sshAlias: "build", state: "reconnecting",
                 caps: caps("unavailable", reason: "다시 연결하는 중입니다"),
                 owner: "none", agentVersion: "0.1.0", connectionGeneration: 7,
                 lastSeen: Timestamp(date: now.addingTimeInterval(-90))),
            Host(hostID: designID, name: "디자인 워크스테이션", os: "windows", arch: "amd64", address: "192.168.0.40", port: 47600,
                 sshAlias: "design", state: "offline",
                 caps: caps("unavailable", reason: "컴퓨터가 오프라인입니다"),
                 owner: "none", agentVersion: "0.1.0", connectionGeneration: 1,
                 lastSeen: Timestamp(date: now.addingTimeInterval(-3 * 3600))),
            Host(hostID: labID, name: "실험실 PC", os: "windows", arch: "amd64", address: "192.168.0.77", port: 47600,
                 sshAlias: "lab-pc", state: "blocked",
                 caps: caps("unavailable", reason: "인증서가 페어링 때와 다릅니다"),
                 owner: "none", agentVersion: "0.1.0", connectionGeneration: 3,
                 lastSeen: Timestamp(date: now.addingTimeInterval(-26 * 3600))),
        ]
    }

    public static func controls(for hosts: [Host]) -> [String: ControlState] {
        var controls: [String: ControlState] = [:]
        for host in hosts {
            controls[host.hostID] = ControlState(owner: host.owner, ownerClient: host.ownerClient, gen: host.connectionGeneration, aiPaused: false)
        }
        return controls
    }

    public static func timeline(hostID: String, now: Date = .now) -> [TimelineEntry] {
        guard hostID == devPCID else { return [] }
        func at(_ minutesAgo: Double) -> Timestamp { Timestamp(date: now.addingTimeInterval(-minutesAgo * 60)) }
        return [
            TimelineEntry(seq: 101, ts: at(42), hostID: hostID, actor: "system", kind: "connection",
                          summary: "연결됨 · 세대 4", outcome: "ok"),
            TimelineEntry(seq: 102, ts: at(38), hostID: hostID, actor: "human", clientName: "Deskhand", kind: "control",
                          summary: "AI에게 제어권을 넘겼습니다", outcome: "ok"),
            TimelineEntry(seq: 103, ts: at(31), hostID: hostID, actor: "ai", clientName: "Claude Code", kind: "exec",
                          summary: "dotnet test C:\\src\\Shop.sln", outcome: "failed", jobID: "j-01JB7Q"),
            TimelineEntry(seq: 104, ts: at(12), hostID: hostID, actor: "ai", clientName: "Claude Code", kind: "input",
                          summary: "‘주문 확인’ 버튼 클릭", outcome: "ok"),
            TimelineEntry(seq: 105, ts: at(3), hostID: hostID, actor: "ai", clientName: "Claude Code", kind: "file",
                          summary: "C:\\qa\\report.html 받기", outcome: "ok"),
        ]
    }

    public static func doctor(hostID: String) -> [DoctorCheck] {
        switch hostID {
        case devPCID:
            [
                DoctorCheck(id: "ssh", status: "pass", detail: "ssh windows 접속 성공 · OpenSSH_for_Windows_9.5"),
                DoctorCheck(id: "agent_version", status: "pass", detail: "deskhand-agent 0.1.0 · 이 Mac의 데몬과 같은 버전"),
                DoctorCheck(id: "quic", status: "pass", detail: "UDP 47600 연결 · 왕복 3 ms"),
                DoctorCheck(id: "fingerprint", status: "pass", detail: "SHA256:4f9c2b7e…e21a · 페어링 때와 같음"),
                DoctorCheck(id: "session", status: "warn", detail: "콘솔 세션이 잠겨 있어 화면과 입력을 쓸 수 없습니다",
                            fix: "원격 컴퓨터에서 Windows 잠금을 해제하세요"),
                DoctorCheck(id: "firewall", status: "fail", detail: "인바운드 규칙 ‘Deskhand Agent’가 없습니다",
                            fix: #"New-NetFirewallRule -DisplayName "Deskhand Agent" -Direction Inbound -Protocol UDP -LocalPort 47600 -RemoteAddress LocalSubnet -Action Allow"#),
                DoctorCheck(id: "screen", status: "skip", detail: "세션이 잠겨 있어 화면 캡처 검사를 건너뛰었습니다"),
            ]
        case labID:
            [
                DoctorCheck(id: "ssh", status: "pass", detail: "ssh lab-pc 접속 성공"),
                DoctorCheck(id: "agent_version", status: "pass", detail: "deskhand-agent 0.1.0"),
                DoctorCheck(id: "fingerprint", status: "fail", detail: "에이전트 인증서 지문이 페어링 때와 다릅니다",
                            fix: "deskhand host repair 실험실 PC"),
                DoctorCheck(id: "quic", status: "skip", detail: "지문이 맞지 않아 연결하지 않았습니다"),
            ]
        default:
            [
                DoctorCheck(id: "ssh", status: "pass", detail: "SSH 접속 성공"),
                DoctorCheck(id: "agent_version", status: "pass", detail: "deskhand-agent 0.1.0"),
                DoctorCheck(id: "quic", status: "pass", detail: "UDP 47600 연결"),
                DoctorCheck(id: "fingerprint", status: "pass", detail: "페어링 때와 같음"),
                DoctorCheck(id: "session", status: "pass", detail: "콘솔 세션 활성"),
                DoctorCheck(id: "firewall", status: "pass", detail: "인바운드 규칙 ‘Deskhand Agent’ 있음"),
            ]
        }
    }

    /// Detail lines the simulated installation reports while a step runs and when it finishes.
    public static func installDetail(_ step: InstallStep, alias: String, done: Bool) -> String {
        switch (step, done) {
        case (.probe, false): "ssh \(alias) 접속 중"
        case (.probe, true): "Windows 11 Pro 24H2 · x64"
        case (.copy, false): "deskhand-agent.exe 보내는 중"
        case (.copy, true): "deskhand-agent.exe · 14.2 MB"
        case (.initialize, false): "에이전트 키를 만들고 이 Mac의 인증서 지문을 기록하는 중"
        case (.initialize, true): "에이전트 지문 SHA256:9c1f07d2…4ab2"
        case (.install, false): "예약 작업과 방화벽 규칙을 등록하는 중"
        case (.install, true): "예약 작업 ‘Deskhand Agent’ 등록"
        case (.verify, false): "보안 연결로 에이전트에 접속하는 중"
        case (.verify, true): "연결됨 · 왕복 3 ms"
        }
    }

    public static func installFailure(alias: String) -> OperationError {
        OperationError(code: "ELEVATION_REQUIRED",
                       message: "\(alias) 계정에 관리자 권한이 없어 예약 작업을 등록하지 못했습니다",
                       nextAction: "use_admin_account")
    }

    public static func newHost(id: String, name: String, alias: String, now: Date = .now) -> Host {
        Host(hostID: id, name: name, os: "windows", arch: "amd64", address: "192.168.0.52", port: 47600,
             sshAlias: alias, state: "ready",
             caps: caps("ready", except: ["uia": Capability(state: "needs_user", reason: "UI 자동화 도우미를 처음 실행하려면 원격 컴퓨터에서 승인해야 합니다")]),
             session: SessionInfo(sessionID: 1, user: "taeyoung", connectState: "active", protocolKind: "console",
                                  locked: false, inputDesktop: "Default"),
             displays: [display(width: 2560, height: 1440, scale: 1, adapter: "Intel UHD Graphics 770")],
             owner: "none", agentVersion: "0.1.0", connectionGeneration: 1, lastSeen: Timestamp(date: now))
    }
}
