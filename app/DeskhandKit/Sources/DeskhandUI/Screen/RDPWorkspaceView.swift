import AppKit
import DeskhandClient
import DeskhandRDP
import SwiftUI
import struct DeskhandClient.Host

/// The real Windows screen uses the OS RDP server; preview scenes keep their seeded screen.
public struct RDPWorkspaceView: View {
    let store: AppStore
    let host: Host
    @Environment(\.displayScale) private var displayScale
    @State private var viewport: RDPViewport?
    @State private var connection = NativeRDPConnection()
    @State private var input = RDPInputChannel()
    @State private var endpoint: RDPEndpoint?
    @State private var endpointError: OperationError?
    @State private var loadingEndpoint = true
    @State private var username = ""
    @State private var password = ""
    @State private var rememberLogin = true
    @State private var savedLogin = false
    @State private var credentialNotice: String?
    @State private var loadedCredentialKey: String?
    @State private var loginMemory = RDPLoginMemory()
    @State private var retry = 0
    @State private var inputRetry = 0
    @State private var captured = false
    @State private var hasAutoConnected = false
    @State private var recovery = RDPReconnectPolicy()
    @State private var screenRecoveryID: UUID?
    @State private var credentialGeneration = UUID()
    private let standIn: RDPEndpoint?

    /// `standIn` renders the real login layout without network requests or authentication.
    public init(store: AppStore, host: Host, standIn: RDPEndpoint? = nil) {
        self.store = store
        self.host = host
        self.standIn = standIn
        _endpoint = State(initialValue: standIn)
        _username = State(initialValue: standIn?.user ?? "")
        _loadingEndpoint = State(initialValue: standIn == nil)
        _rememberLogin = State(initialValue: standIn == nil
            ? UserDefaults.standard.object(forKey: "rdp.remember.\(host.hostID)") as? Bool ?? true : true)
    }

    private var owner: ControlOwner {
        ControlOwner(rawValue: store.control(for: host.hostID)?.owner ?? host.owner) ?? .none
    }
    private var live: Bool { connection.phase == .live }
    private var connecting: Bool { connection.phase == .connecting }
    private var targetDisplay: DisplayInfo? {
        guard let frame = connection.frame else { return nil }
        let matching = host.displays.filter { $0.width == frame.width && $0.height == frame.height }
        return matching.count == 1 ? matching.first : nil
    }
    private var permitsInput: Bool {
        live && targetDisplay != nil && RDPInputGate.permits(
            host: host, serverSessionID: connection.serverSessionID, expectedSID: endpoint?.sid ?? "")
    }

    public var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: DS.Space.m) {
                Label("Windows 화면", systemImage: "display")
                    .font(DS.Font.headline)
                if let frame = connection.frame {
                    Text("\(frame.width)×\(frame.height) px").font(DS.Font.caption).foregroundStyle(.secondary)
                        .help("Windows 원격 화면의 픽셀 해상도입니다.")
                }
                Spacer()
                if live {
                    Label(captured ? "입력 보내는 중" : "화면 연결됨", systemImage: captured ? "keyboard" : "checkmark.circle")
                        .font(DS.Font.caption).foregroundStyle(.secondary)
                }
                if live, savedLogin {
                    Button("저장한 로그인 삭제") { Task { await forgetLogin() } }.buttonStyle(.bordered)
                }
                if connecting || live || screenRecoveryID != nil {
                    Button(screenRecoveryID != nil ? "자동 연결 중단" : (connecting ? "취소" : "화면 연결 끊기")) { disconnect() }
                        .buttonStyle(.bordered)
                }
            }
            .padding(.horizontal, DS.Space.l).padding(.vertical, DS.Space.s)
            Divider()
            if live, let credentialNotice {
                Label(credentialNotice, systemImage: "key")
                    .font(DS.Font.caption).fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, DS.Space.l).padding(.vertical, DS.Space.s)
            }
            GeometryReader { geometry in
                Group {
                    if live { liveScreen } else { login }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .onChange(of: RDPViewport(width: geometry.size.width, height: geometry.size.height, backingScale: displayScale), initial: true) { _, value in
                    viewport = value
                }
            }
        }
        .task(id: retry) { await loadEndpoint() }
        .task(id: RecoveryKey(phase: connection.phase, address: host.address, generation: host.connectionGeneration)) {
            if case .failed(let code) = connection.phase { await recoverScreen(after: code) }
        }
        .task(id: InputKey(enabled: permitsInput, retry: inputRetry, generation: host.connectionGeneration, sessionID: connection.serverSessionID, sid: endpoint?.sid ?? "")) {
            guard permitsInput, let sessionID = connection.serverSessionID, let sid = endpoint?.sid else { input.close(); return }
            await input.run(backend: store.backend, hostID: host.hostID, sessionID: sessionID, sid: sid, connectionGeneration: host.connectionGeneration)
        }
        .task(id: viewport) {
            guard let viewport else { return }
            do { try await Task.sleep(for: .milliseconds(250)) } catch { return }
            guard !Task.isCancelled else { return }
            connection.resize(to: viewport)
        }
        .onChange(of: connection.phase) { _, phase in
            switch phase {
            case .live:
                recovery.connected()
                let generation = credentialGeneration
                Task {
                    do {
                        let stored = try await loginMemory.connected()
                        guard live, rememberLogin, credentialGeneration == generation else { return }
                        if stored { savedLogin = true; credentialNotice = nil }
                    } catch {
                        if live, credentialGeneration == generation {
                            credentialNotice = "연결됐지만 이번 로그인 정보를 저장하지 못했습니다. 다음 연결 전에 저장 상태를 확인하세요."
                        }
                    }
                }
            case .failed, .stopped: loginMemory.failed()
            default: break
            }
        }
        .onDisappear { disconnect() }
    }

    private struct InputKey: Equatable {
        let enabled: Bool; let retry: Int; let generation: Int; let sessionID: UInt32?; let sid: String
    }

    private struct RecoveryKey: Equatable {
        let phase: NativeRDPConnection.Phase; let address: String; let generation: Int
    }

    private var login: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: DS.Space.l) {
                Image(systemName: "desktopcomputer.and.arrow.down")
                    .font(.system(size: 30, weight: .light)).foregroundStyle(.tint)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: DS.Space.s) {
                    Text("\(host.name)에 로그인").font(.title2.weight(.semibold))
                    Text("이 PC의 Windows 사용자 이름과 계정 암호를 입력하세요.")
                        .font(DS.Font.body).foregroundStyle(.primary).fixedSize(horizontal: false, vertical: true)
                }
                if loadingEndpoint {
                    ProgressView("PC 연결 정보를 확인하는 중…")
                } else if screenRecoveryID != nil {
                    ProgressView("Windows 화면에 다시 연결하는 중…")
                } else if let endpointError {
                    Label(endpointError.message, systemImage: "exclamationmark.triangle")
                        .font(DS.Font.body).fixedSize(horizontal: false, vertical: true)
                    Button("연결 정보 다시 확인") { retry += 1 }.buttonStyle(.bordered)
                } else if endpoint?.enabled == false {
                    Label("이 PC에서 Windows 원격 데스크톱을 켜야 합니다.", systemImage: "desktopcomputer.trianglebadge.exclamationmark")
                        .fixedSize(horizontal: false, vertical: true)
                } else {
                    VStack(alignment: .leading, spacing: DS.Space.m) {
                        VStack(alignment: .leading, spacing: DS.Space.xs) {
                            Text("사용자 이름").font(DS.Font.body.weight(.medium))
                            TextField("PC이름\\사용자", text: $username)
                                .textFieldStyle(.roundedBorder).autocorrectionDisabled()
                                .disabled(connecting)
                        }
                        VStack(alignment: .leading, spacing: DS.Space.xs) {
                            Text("계정 암호").font(DS.Font.body.weight(.medium))
                            SecureField("계정 암호 입력", text: $password)
                                .textFieldStyle(.roundedBorder).disabled(connecting)
                                .onSubmit { connect() }
                        }
                        Text("Windows PIN 대신 계정 암호를 입력하세요.")
                            .font(DS.Font.caption).foregroundStyle(DS.Status.neutralText)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    Toggle("이 Mac에 로그인 정보 저장", isOn: $rememberLogin)
                        .disabled(connecting)
                        .onChange(of: rememberLogin) { _, enabled in
                            guard standIn == nil else { return }
                            UserDefaults.standard.set(enabled, forKey: "rdp.remember.\(host.hostID)")
                            if !enabled { Task { await forgetLogin(clearPassword: false) } }
                        }
                    Text("사용자 이름과 암호를 이 Mac의 키체인에 저장합니다. 해제하면 저장한 정보도 삭제합니다.")
                        .font(DS.Font.caption).foregroundStyle(DS.Status.neutralText)
                        .fixedSize(horizontal: false, vertical: true)
                    if savedLogin {
                        HStack {
                            Label(password.isEmpty ? "이 PC의 로그인 정보가 저장돼 있습니다." : "저장한 로그인 정보를 불러왔습니다.", systemImage: "key.fill")
                            Spacer()
                            if password.isEmpty {
                                Button("불러오기") { Task { await restoreLogin() } }.buttonStyle(.bordered).disabled(connecting)
                            }
                            Button("삭제") { Task { await forgetLogin() } }.buttonStyle(.bordered).disabled(connecting)
                        }
                        .font(DS.Font.caption)
                    }
                    if let credentialNotice {
                        Text(credentialNotice).font(DS.Font.caption).fixedSize(horizontal: false, vertical: true)
                    }
                    if case .failed(let code) = connection.phase {
                        Label(connectionFailureMessage(code),
                              systemImage: "exclamationmark.triangle")
                            .font(DS.Font.body).fixedSize(horizontal: false, vertical: true)
                            Text(String(format: "오류 코드: %08X", code))
                            .font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                        if code == 0xD4000004 {
                            Button("PC 신원 다시 확인") { retry += 1 }.buttonStyle(.bordered)
                        }
                    }
                    Button { connect() } label: {
                        HStack {
                            if connecting { ProgressView().controlSize(.small) }
                            Text(connecting ? "Windows에 연결하는 중…" : "Windows에 연결")
                            Spacer()
                            if !connecting { Image(systemName: password.isEmpty ? "lock.fill" : "arrow.right") }
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(RDPConnectButtonStyle())
                    .disabled(connecting || username.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || password.isEmpty || endpoint == nil)
                    if !connecting, password.isEmpty {
                        Text("암호를 입력하면 연결할 수 있습니다.")
                            .font(DS.Font.caption).foregroundStyle(DS.Status.neutralText)
                    }
                    Divider()
                    VStack(alignment: .leading, spacing: DS.Space.s) {
                        Label("저장한 암호는 이 Mac의 키체인에서 보호합니다.", systemImage: "lock")
                        Label("PC 모니터는 켜지 않아도 됩니다.", systemImage: "display")
                    }
                    .font(DS.Font.caption).foregroundStyle(DS.Status.neutralText)
                }
            }
            .padding(DS.Space.xxl)
            .frame(maxWidth: 440, alignment: .leading)
            .background(.background, in: RoundedRectangle(cornerRadius: DS.Radius.l))
            .overlay(RoundedRectangle(cornerRadius: DS.Radius.l).stroke(.separator))
            .padding(DS.Space.xxl)
            .frame(maxWidth: .infinity, minHeight: 440)
        }
        .background(DS.Color.screenBackdrop.opacity(0.025))
    }

    private var liveScreen: some View {
        VStack(spacing: 0) {
            ZStack {
                Color.black
                if let frame = connection.frame, let image = frame.image {
                    RDPImageSurface(image: image, sequence: frame.sequence, onFrameSubmitted: { connection.frameSubmitted(sequence: $0) }, size: CGSize(width: frame.width, height: frame.height),
                                    inputEnabled: permitsInput && input.ready, owner: owner,
                                    display: targetDisplay?.index ?? 0, preferences: store.screenPreferences,
                                    onEvent: { event in input.send(event, gen: store.control(for: host.hostID)?.gen ?? 0) },
                                    onCaptureChange: { captured = $0 },
                                    onTakeover: { store.controlActions.forceTakeOver(host.hostID) })
                } else {
                    ProgressView("Windows 화면을 불러오는 중…").tint(.white).foregroundStyle(.white)
                }
                if input.denied {
                    VStack { Spacer(); Text("제어권이 바뀌어 입력을 멈췄습니다. 위에서 제어권을 확인하세요.")
                            .font(DS.Font.caption).padding().background(.regularMaterial, in: Capsule()).padding() }
                        .allowsHitTesting(false)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .overlay(alignment: .top) {
            if !permitsInput || !input.ready {
                HStack(spacing: DS.Space.s) {
                    Image(systemName: "eye")
                    Text(input.failure?.message ?? "Windows 세션과 입력 연결을 확인하는 중입니다. 현재는 화면만 표시합니다.")
                        .font(DS.Font.caption).fixedSize(horizontal: false, vertical: true)
                    Spacer()
                    if input.failure != nil {
                        Button("입력 다시 연결") { inputRetry += 1 }.buttonStyle(.bordered)
                    }
                }
                .padding(DS.Space.m).background(.regularMaterial)
            }
            }
        }
    }

    private func loadEndpoint() async {
        guard standIn == nil else { return }
        let generation = credentialGeneration
        loadingEndpoint = true
        endpointError = nil
        defer { loadingEndpoint = false }
        do {
            let value = try await store.backend.rdpEndpoint(hostID: host.hostID)
            guard !Task.isCancelled, credentialGeneration == generation else { return }
            endpoint = value
            if username.isEmpty { username = value.user }
            let key = RDPLoginMemory.key(hostID: host.hostID, certificate: value.certificateSHA256)
            if loadedCredentialKey != key {
                loadedCredentialKey = key
                savedLogin = false
                do {
                    let indexKey = "rdp.saved-key.\(host.hostID)"
                    if let previous = UserDefaults.standard.string(forKey: indexKey), previous != key {
                        try await loginMemory.forget(key: previous)
                        guard !Task.isCancelled else { return }
                    }
                    UserDefaults.standard.set(key, forKey: indexKey)
                    let restored = rememberLogin ? try await loginMemory.restore(key: key) : nil
                    guard !Task.isCancelled, loadedCredentialKey == key, credentialGeneration == generation else { return }
                    if let saved = restored,
                       password.isEmpty, username == value.user || username == saved.username {
                        username = saved.username
                        password = saved.password
                        savedLogin = true
                    }
                } catch {
                    credentialNotice = "저장한 로그인 정보를 불러오지 못했습니다. 계정 암호를 입력해 연결하세요."
                }
            }
            if rememberLogin && savedLogin && !password.isEmpty && value.enabled && !hasAutoConnected && !connecting && !live {
                hasAutoConnected = true
                connect()
            }
        } catch {
            if !Task.isCancelled { endpointError = OperationError(error); endpoint = nil }
        }
    }

    private func connect(recovering: Bool = false) {
        guard standIn == nil, !connecting, let endpoint, endpoint.enabled, !password.isEmpty,
              let port = UInt16(exactly: endpoint.port) else { return }
        let account = username.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !account.isEmpty else { return }
        credentialGeneration = UUID()
        if !recovering { recovery.requested(remembered: savedLogin) }
        let parts = account.split(separator: "\\", maxSplits: 1, omittingEmptySubsequences: false)
        let domain = parts.count == 2 ? String(parts[0]) : ""
        let user = parts.count == 2 ? String(parts[1]) : account
        let secret = password
        password = ""
        let key = RDPLoginMemory.key(hostID: host.hostID, certificate: endpoint.certificateSHA256)
        loginMemory.prepare(key: key, username: account, password: secret, remember: rememberLogin)
        do {
            let configuration = try RDPConfiguration(host: host.address, port: port,
                                                     certificateSHA256: endpoint.certificateSHA256,
                                                     width: viewport?.width ?? 1920, height: viewport?.height ?? 1080)
            try connection.connect(configuration: configuration, username: user, domain: domain, password: secret)
            if let viewport { connection.resize(to: viewport) }
        } catch {
            loginMemory.failed()
            endpointError = OperationError(code: "RDP_CONNECT", message: "화면 연결을 시작하지 못했습니다. 앱 진단을 확인한 뒤 다시 시도하세요")
        }
    }

    private func recoverScreen(after code: UInt32) async {
        guard standIn == nil, let previous = endpoint, let key = loadedCredentialKey else { return }
        let address = host.address
        let account = username, typedPassword = password
        let recoveryID = UUID()
        defer { if screenRecoveryID == recoveryID { screenRecoveryID = nil } }
        while let delay = recovery.nextDelayMS(error: code, remembered: savedLogin && rememberLogin) {
            screenRecoveryID = recoveryID
            do {
                try await Task.sleep(for: .milliseconds(delay))
                let refreshed = try await store.backend.rdpEndpoint(hostID: host.hostID)
                guard !Task.isCancelled, recovery.wanted, savedLogin, rememberLogin,
                      loadedCredentialKey == key, host.address == address,
                      username == account, password == typedPassword else { return }
                guard RDPReconnectPolicy.sameTarget(previous, refreshed) else {
                    recovery.disconnected()
                    endpointError = OperationError(code: "STALE_TARGET", message: "PC의 인증서·계정 또는 세션이 바뀌어 자동 연결을 멈췄습니다. 연결 정보를 다시 확인하세요.")
                    return
                }
                let restored = try await loginMemory.restore(key: key)
                guard !Task.isCancelled, recovery.wanted, savedLogin, rememberLogin,
                      loadedCredentialKey == key, username == account, password == typedPassword else { return }
                guard let saved = restored, saved.username == account,
                      !saved.password.isEmpty else { return }
                endpoint = refreshed
                username = saved.username
                password = saved.password
                connect(recovering: true)
                return
            } catch {
                if Task.isCancelled { return }
                endpointError = OperationError(error)
            }
        }
    }

    private func restoreLogin() async {
        guard let loadedCredentialKey, standIn == nil else { return }
        let generation = credentialGeneration
        let account = username, typedPassword = password
        do {
            let restored = try await loginMemory.restore(key: loadedCredentialKey)
            guard !Task.isCancelled, self.loadedCredentialKey == loadedCredentialKey,
                  credentialGeneration == generation, !live, !connecting else { return }
            guard username == account, password == typedPassword else { return }
            if let saved = restored {
                username = saved.username
                password = saved.password
                savedLogin = true
                credentialNotice = nil
            } else {
                savedLogin = false
                credentialNotice = "저장한 로그인 정보가 없습니다."
            }
        } catch {
            credentialNotice = "저장한 로그인 정보를 불러오지 못했습니다. 계정 암호를 입력해 연결하세요."
        }
    }

    private func forgetLogin(clearPassword: Bool = true) async {
        guard let loadedCredentialKey, standIn == nil else { return }
        credentialGeneration = UUID()
        let generation = credentialGeneration
        recovery.disconnected()
        savedLogin = false
        if clearPassword { password = "" }
        do {
            let indexKey = "rdp.saved-key.\(host.hostID)"
            if let previous = UserDefaults.standard.string(forKey: indexKey), previous != loadedCredentialKey {
                try await loginMemory.forget(key: previous)
            }
            try await loginMemory.forget(key: loadedCredentialKey)
            guard self.loadedCredentialKey == loadedCredentialKey, credentialGeneration == generation else { return }
            UserDefaults.standard.removeObject(forKey: indexKey)
            savedLogin = false
            credentialNotice = clearPassword ? "저장한 로그인 정보를 삭제했습니다." : "로그인 정보를 저장하지 않습니다."
        } catch {
            credentialNotice = "저장한 로그인 정보를 삭제하지 못했습니다. 키체인 접근 상태를 확인한 뒤 다시 시도하세요."
        }
    }

    private func disconnect() {
        credentialGeneration = UUID()
        recovery.disconnected()
        screenRecoveryID = nil
        hasAutoConnected = false
        loginMemory.failed()
        password = ""
        input.close()
        connection.disconnect()
    }

    // Pinned FreeRDP include/freerdp/error.h and native/rdp/deskhand_rdp.h.
    private func connectionFailureMessage(_ code: UInt32) -> String {
        switch code {
        case 0xD4000004:
            "PC의 인증서가 확인한 정보와 다릅니다. PC 신원을 다시 확인한 뒤 연결하세요."
        case 0x00020009, 0x00020014:
            "Windows 로그인에 실패했습니다. 사용자 이름과 계정 암호를 확인하세요."
        case 0x00020018:
            "Windows 계정이 잠겨 있습니다. 계정 잠금이 풀린 뒤 다시 연결하세요."
        case 0x0002000E, 0x0002000F, 0x00020013:
            "Windows 계정 암호를 변경해야 합니다. 암호를 변경한 뒤 다시 연결하세요."
        default:
            "Windows 화면 연결이 종료됐습니다. 아래 오류 코드로 실패 원인을 확인할 수 있습니다."
        }
    }
}

private struct RDPConnectButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var enabled
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(DS.Font.body.weight(.semibold))
            .foregroundStyle(enabled ? Color.white : DS.Status.neutralText)
            .padding(.horizontal, DS.Space.m).padding(.vertical, DS.Space.m)
            .background(enabled ? Color.accentColor : Color(nsColor: .quaternaryLabelColor).opacity(0.5),
                        in: RoundedRectangle(cornerRadius: DS.Radius.s))
            .overlay(RoundedRectangle(cornerRadius: DS.Radius.s).stroke(.separator))
            .opacity(configuration.isPressed && enabled ? 0.8 : 1)
    }
}

private struct RDPImageSurface: NSViewRepresentable {
    let image: CGImage
    let sequence: UInt64
    let onFrameSubmitted: (UInt64) -> Void
    let size: CGSize
    let inputEnabled: Bool
    let owner: ControlOwner
    let display: Int
    let preferences: ScreenPreferences
    let onEvent: (InputEvent) -> Void
    let onCaptureChange: (Bool) -> Void
    let onTakeover: () -> Void

    final class Coordinator {
        let imageLayer = CALayer()
        var lastSequence: UInt64?
    }
    func makeCoordinator() -> Coordinator { Coordinator() }
    func makeNSView(context: Context) -> ScreenSurfaceView {
        let view = ScreenSurfaceView(frame: .zero)
        context.coordinator.imageLayer.contentsGravity = .resize
        view.attach(displayLayer: context.coordinator.imageLayer)
        return view
    }
    func updateNSView(_ view: ScreenSurfaceView, context: Context) {
        if context.coordinator.lastSequence != sequence {
            CATransaction.begin()
            CATransaction.setDisableActions(true)
            context.coordinator.imageLayer.contents = image
            CATransaction.commit()
            context.coordinator.lastSequence = sequence
            onFrameSubmitted(sequence)
        }
        view.remoteSize = size
        let capture = view.capture
        capture.isInputEnabled = inputEnabled
        capture.owner = owner
        capture.display = display
        capture.keyMap = preferences.keyMap
        capture.keyboardMode = preferences.keyboardMode
        capture.forwardsSystemShortcuts = preferences.forwardsSystemShortcuts
        capture.showsLocalCursor = true
        capture.onEvent = onEvent
        capture.onCaptureChange = onCaptureChange
        capture.onTakeoverRequest = onTakeover
        capture.setAccessibilityLabel("Windows 원격 화면")
        capture.setAccessibilityHelp("클릭하면 키보드와 마우스를 PC로 보냅니다. ⌃⌥를 눌렀다 떼면 입력을 돌려받습니다")
    }
    static func dismantleNSView(_ view: ScreenSurfaceView, coordinator: Coordinator) { view.capture.endCapture() }
}
