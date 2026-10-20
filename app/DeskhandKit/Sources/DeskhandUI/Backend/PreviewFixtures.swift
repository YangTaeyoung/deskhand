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
    public static let meetingPCID = "meeting-pc"
    public static let stagingID = "staging-pc"
    public static let kioskID = "kiosk-pc"

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
            Host(hostID: meetingPCID, name: "회의실 PC", os: "windows", arch: "amd64", address: "192.168.0.55", port: 47600,
                 sshAlias: "meeting", state: "ready", caps: caps("ready"),
                 session: SessionInfo(sessionID: 1, user: "meeting", connectState: "active", protocolKind: "console",
                                      locked: false, inputDesktop: "Default"),
                 displays: [display(width: 1920, height: 1080, scale: 1, adapter: "Intel Iris Xe Graphics")],
                 owner: "none", agentVersion: "0.1.0", connectionGeneration: 1,
                 lastSeen: Timestamp(date: now)),
            Host(hostID: stagingID, name: "스테이징 서버", os: "windows", arch: "amd64", address: "10.0.4.30", port: 47600,
                 sshAlias: "staging", state: "ready", caps: caps("ready"),
                 session: SessionInfo(sessionID: 3, user: "deploy", connectState: "active", protocolKind: "rdp",
                                      locked: false, inputDesktop: "Default"),
                 displays: [display(width: 2560, height: 1440, scale: 1, adapter: "Microsoft Remote Display Adapter")],
                 owner: "human", agentVersion: "0.1.0", connectionGeneration: 6,
                 lastSeen: Timestamp(date: now)),
            Host(hostID: kioskID, name: "매장 키오스크", os: "windows", arch: "amd64", address: "192.168.10.8", port: 47600,
                 sshAlias: "kiosk", state: "ready",
                 caps: caps("ready", except: [
                     "screen": Capability(state: "needs_user", reason: "Windows 로그인 화면이 떠 있습니다"),
                     "input": Capability(state: "needs_user", reason: "Windows 로그인 화면이 떠 있습니다"),
                     "uia": Capability(state: "needs_user", reason: "Windows 로그인 화면이 떠 있습니다"),
                 ]),
                 session: SessionInfo(sessionID: 1, user: "kiosk", connectState: "active", protocolKind: "console",
                                      locked: true, inputDesktop: "Winlogon"),
                 displays: [display(width: 1080, height: 1920, scale: 1, adapter: "Intel UHD Graphics 600")],
                 owner: "none", agentVersion: "0.1.0", connectionGeneration: 2,
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

    /// Control states matching the hosts' owners. The staging server is paused by an emergency stop.
    public static func controls(for hosts: [Host]) -> [String: ControlState] {
        var controls: [String: ControlState] = [:]
        for host in hosts {
            controls[host.hostID] = ControlState(owner: host.owner, ownerClient: host.ownerClient, gen: host.connectionGeneration,
                                                 aiPaused: host.hostID == stagingID)
        }
        return controls
    }

    /// Screen stream quality of connected sample computers.
    public static func streamQuality(hostID: String) -> StreamQuality? {
        switch hostID {
        case devPCID: StreamQuality(fps: 60, latencyMS: 18)
        case qaLaptopID: StreamQuality(fps: 30, latencyMS: 41)
        case meetingPCID: StreamQuality(fps: 60, latencyMS: 12)
        case stagingID: StreamQuality(fps: 48, latencyMS: 35)
        default: nil
        }
    }

    /// Timeline entries newest first, as `timeline.list` returns them.
    public static func timeline(hostID: String, now: Date = .now) -> [TimelineEntry] {
        func ago(_ minutes: Double) -> Timestamp { Timestamp(date: now.addingTimeInterval(-minutes * 60)) }
        let today = Calendar.current.startOfDay(for: now)
        func yesterday(_ hour: Int, _ minute: Int) -> Timestamp {
            Timestamp(date: today.addingTimeInterval(TimeInterval(-24 * 3600 + hour * 3600 + minute * 60)))
        }
        func artifact(_ id: String, _ kind: String, _ name: String, _ mime: String, size: Int, at ts: Timestamp, job: String? = nil) -> Artifact {
            Artifact(artifactID: id, jobID: job, kind: kind, name: name,
                     path: Paths.home.appendingPathComponent("artifacts/\(hostID)/\(name)").path, size: size, mime: mime,
                     sha256: "9c1f07d24ab2e8f1", createdAt: ts)
        }
        func entry(_ seq: Int, _ ts: Timestamp, _ actor: String, _ client: String?, _ kind: String, _ summary: String,
                   _ outcome: String = "ok", job: String? = nil, artifacts: [Artifact] = []) -> TimelineEntry {
            TimelineEntry(seq: seq, ts: ts, hostID: hostID, actor: actor, clientName: client, kind: kind, summary: summary,
                          outcome: outcome, jobID: job, artifacts: artifacts)
        }
        switch hostID {
        case devPCID:
            return [
                entry(105, ago(3), "ai", "Claude Code", "file", "C:\\qa\\report.html 받기",
                      artifacts: [artifact("a-105", "file", "report.html", "text/html", size: 48_210, at: ago(3))]),
                entry(104, ago(12), "ai", "Claude Code", "input", "‘주문 확인’ 버튼 클릭",
                      artifacts: [artifact("a-104", "screenshot", "after-click.png", "image/png", size: 412_331, at: ago(12)),
                                  artifact("a-104u", "ui_tree", "order-form.json", "application/json", size: 9_812, at: ago(12))]),
                entry(103, ago(31), "ai", "Claude Code", "exec", "dotnet test C:\\src\\Shop.sln", "failed", job: "j-01JB7Q",
                      artifacts: [artifact("a-103", "log", "dotnet-test.log", "text/plain", size: 88_004, at: ago(31), job: "j-01JB7Q")]),
                entry(102, ago(38), "human", "Deskhand", "control", "AI에게 제어권을 넘겼습니다"),
                entry(101, ago(42), "system", nil, "connection", "연결됨 · 세대 4"),
                entry(100, yesterday(18, 52), "system", nil, "connection", "연결 끊김 · 원격 PC가 절전 모드로 들어갔습니다"),
                entry(99, yesterday(18, 40), "ai", "Claude Code", "input", "‘배포’ 버튼 클릭", "denied"),
                entry(98, yesterday(18, 31), "human", "Deskhand", "control", "제어권을 가져왔습니다"),
                entry(97, yesterday(18, 12), "ai", "Claude Code", "exec", "powershell C:\\deploy\\publish.ps1", "unknown", job: "j-01JB5C"),
                entry(96, yesterday(17, 55), "ai", "Claude Code", "input", "화면 캡처 · 디스플레이 1",
                      artifacts: [artifact("a-96", "screenshot", "display-1.png", "image/png", size: 1_204_551, at: yesterday(17, 55))]),
            ]
        case qaLaptopID:
            return [
                entry(41, ago(6), "human", "Deskhand", "control", "제어권을 가져왔습니다"),
                entry(40, ago(19), "ai", "Codex", "qa", "결제 흐름 시나리오 · 12단계 중 12단계 통과", job: "j-01JB8A",
                      artifacts: [artifact("a-40", "report", "checkout-report.html", "text/html", size: 120_400, at: ago(19), job: "j-01JB8A")]),
            ]
        case meetingPCID:
            return [entry(12, ago(25), "system", nil, "connection", "연결됨 · 세대 1")]
        case stagingID:
            return [
                entry(53, ago(2), "human", "Deskhand", "control", "긴급 정지 · AI 입력을 멈췄습니다"),
                entry(52, ago(3), "ai", "Codex", "input", "‘모든 주문 삭제’ 확인 창 열기",
                      artifacts: [artifact("a-52", "screenshot", "confirm-dialog.png", "image/png", size: 356_120, at: ago(3))]),
                entry(51, ago(9), "ai", "Codex", "exec", "sqlcmd -S . -Q \"SELECT COUNT(*) FROM Orders\""),
                entry(50, ago(11), "system", nil, "connection", "원격 데스크톱 세션 연결됨 · deploy"),
            ]
        case labID:
            return [
                entry(72, ago(26 * 60 - 4), "system", nil, "connection", "인증서 지문이 페어링 때와 달라 연결을 거부했습니다", "denied"),
                entry(71, ago(26 * 60), "system", nil, "connection", "연결 끊김 · 에이전트가 다시 시작됐습니다"),
            ]
        case kioskID:
            return [
                entry(31, ago(6), "system", nil, "connection", "Windows 세션이 잠겼습니다"),
                entry(30, ago(8), "ai", "Claude Code", "input", "‘주문 시작’ 화면에서 메뉴 넘기기", "failed"),
            ]
        default:
            return []
        }
    }

    public static func doctor(hostID: String) -> [DoctorCheck] {
        switch hostID {
        case devPCID:
            [
                DoctorCheck(id: "ssh_reachable", status: "pass", detail: "ssh windows 접속 성공 · OpenSSH_for_Windows_9.5"),
                DoctorCheck(id: "agent_version", status: "pass", detail: "deskhand-agent 0.1.0 · 이 Mac의 데몬과 같은 버전"),
                DoctorCheck(id: "quic_connect", status: "pass", detail: "UDP 47600 연결 · 왕복 3 ms"),
                DoctorCheck(id: "fingerprint", status: "pass", detail: "SHA256:4f9c2b7e…e21a · 페어링 때와 같음"),
                DoctorCheck(id: "session", status: "warn", detail: "콘솔 세션이 잠겨 있어 화면과 입력을 쓸 수 없습니다",
                            fix: "원격 컴퓨터에서 Windows 잠금을 해제하세요"),
                DoctorCheck(id: "firewall_rule", status: "fail", detail: "인바운드 규칙 ‘Deskhand Agent’가 없습니다",
                            fix: #"New-NetFirewallRule -DisplayName "Deskhand Agent" -Direction Inbound -Protocol UDP -LocalPort 47600 -RemoteAddress LocalSubnet -Action Allow"#),
            ]
        case labID:
            [
                DoctorCheck(id: "ssh_reachable", status: "pass", detail: "ssh lab-pc 접속 성공"),
                DoctorCheck(id: "agent_version", status: "pass", detail: "deskhand-agent 0.1.0"),
                DoctorCheck(id: "fingerprint", status: "fail", detail: "에이전트 인증서 지문이 페어링 때와 다릅니다",
                            fix: "deskhand host repair 실험실 PC"),
                DoctorCheck(id: "quic_connect", status: "skip", detail: "지문이 맞지 않아 연결하지 않았습니다"),
                DoctorCheck(id: "session", status: "skip", detail: "연결하지 않아 확인하지 않았습니다"),
                DoctorCheck(id: "firewall_rule", status: "pass", detail: "인바운드 규칙 ‘Deskhand Agent’ 있음"),
            ]
        default:
            [
                DoctorCheck(id: "ssh_reachable", status: "pass", detail: "SSH 접속 성공"),
                DoctorCheck(id: "agent_version", status: "pass", detail: "deskhand-agent 0.1.0"),
                DoctorCheck(id: "quic_connect", status: "pass", detail: "UDP 47600 연결"),
                DoctorCheck(id: "fingerprint", status: "pass", detail: "페어링 때와 같음"),
                DoctorCheck(id: "session", status: "pass", detail: "콘솔 세션 활성"),
                DoctorCheck(id: "firewall_rule", status: "pass", detail: "인바운드 규칙 ‘Deskhand Agent’ 있음"),
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
                       message: "관리자 권한이 없어 예약 작업을 등록하지 못했습니다",
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
