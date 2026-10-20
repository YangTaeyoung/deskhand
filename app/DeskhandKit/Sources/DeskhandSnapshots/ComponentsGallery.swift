import DeskhandUI
import SwiftUI

/// Design-system board: every component in every state, plus the tokens, with Korean sample copy.
struct ComponentsGallery: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.xl) {
            header
            HStack(alignment: .top, spacing: DS.Space.xl) {
                VStack(alignment: .leading, spacing: DS.Space.xl) {
                    connectionSection
                    controlSection
                    capabilitySection
                }
                .frame(maxWidth: .infinity)
                VStack(alignment: .leading, spacing: DS.Space.xl) {
                    stepSection
                    errorSection
                    keyValueSection
                }
                .frame(maxWidth: .infinity)
                VStack(alignment: .leading, spacing: DS.Space.xl) {
                    emptySection
                    tokenSection
                }
                .frame(maxWidth: .infinity)
            }
        }
        .padding(DS.Space.xxl)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    // MARK: Header

    private var header: some View {
        HStack(alignment: .center, spacing: DS.Space.l) {
            RoundedRectangle(cornerRadius: DS.Radius.l, style: .continuous)
                .fill(LinearGradient(colors: [DS.Color.accent, DS.Color.ai], startPoint: .topLeading, endPoint: .bottomTrailing))
                .frame(width: 48, height: 48)
                .overlay {
                    Image(systemName: "hand.point.up.left.fill")
                        .font(.system(size: 22, weight: .semibold))
                        .foregroundStyle(.white)
                }
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                Text("Deskhand 디자인 시스템")
                    .font(.largeTitle.weight(.bold))
                Text("컴포넌트와 상태 · 모든 화면이 같은 토큰과 컴포넌트를 씁니다")
                    .font(DS.Font.body)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Label(colorScheme == .dark ? "다크 모드" : "라이트 모드",
                  systemImage: colorScheme == .dark ? "moon.fill" : "sun.max.fill")
                .font(DS.Font.caption.weight(.medium))
                .foregroundStyle(.secondary)
                .padding(.horizontal, DS.Space.m)
                .padding(.vertical, DS.Space.xs)
                .background(DS.Color.surfaceRaised, in: Capsule())
                .overlay(Capsule().strokeBorder(DS.Color.separator))
        }
    }

    // MARK: Status

    private var connectionSection: some View {
        GallerySection(title: "연결 상태", component: "StatusDot",
                       note: "빈 원은 오프라인, 번지는 원은 연결 진행 중입니다.") {
            Grid(alignment: .leading, horizontalSpacing: DS.Space.xl, verticalSpacing: DS.Space.m) {
                GridRow {
                    stateCell(.ready)
                    stateCell(.offline)
                }
                GridRow {
                    stateCell(.degraded)
                    stateCell(.connecting)
                }
                GridRow {
                    stateCell(.blocked)
                    stateCell(.reconnecting)
                }
            }
            Divider()
            VStack(spacing: 0) {
                hostRow(name: "개발 워크스테이션", detail: "Windows 11 · 콘솔 세션", symbol: "desktopcomputer", state: .ready, selected: true)
                hostRow(name: "QA 노트북", detail: "Windows 11 · 연결 품질 낮음", symbol: "laptopcomputer", state: .degraded, selected: false)
                hostRow(name: "빌드 서버", detail: "마지막 연결 3시간 전", symbol: "server.rack", state: .offline, selected: false)
            }
        }
    }

    private func stateCell(_ state: ConnectionState) -> some View {
        HStack(spacing: DS.Space.s) {
            StatusDot(state: state, showsLabel: true)
            Text(state.rawValue)
                .font(DS.Font.mono)
                .foregroundStyle(.tertiary)
        }
    }

    private func hostRow(name: String, detail: String, symbol: String, state: ConnectionState, selected: Bool) -> some View {
        HStack(spacing: DS.Space.m) {
            Image(systemName: symbol)
                .font(.system(size: 18))
                .foregroundStyle(selected ? DS.Color.accent : .secondary)
                .frame(width: DS.Space.xl)
            VStack(alignment: .leading, spacing: 0) {
                Text(name)
                    .font(DS.Font.body.weight(.medium))
                Text(detail)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            StatusDot(state: state)
        }
        .padding(.horizontal, DS.Space.s)
        .padding(.vertical, DS.Space.s)
        .background(selected ? DS.Color.accent.opacity(0.14) : .clear, in: RoundedRectangle(cornerRadius: DS.Radius.m, style: .continuous))
    }

    // MARK: Control

    private var controlSection: some View {
        GallerySection(title: "제어권", component: "ControlPill",
                       note: "입력 주체는 한 번에 하나입니다. 사람은 파랑, AI는 보라입니다.") {
            Grid(alignment: .leading, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.m) {
                pillRow(ControlPill(owner: .none), note: "아무도 입력하지 않습니다")
                pillRow(ControlPill(owner: .human), note: "이 Mac의 키보드·마우스가 원격 PC로 갑니다")
                pillRow(ControlPill(owner: .ai, clientName: "Claude Code"), note: "AI 입력만 원격 PC로 갑니다")
                pillRow(ControlPill(owner: .human, aiPaused: true), note: "긴급 정지 후 AI 입력이 멈춰 있습니다")
                pillRow(ControlPill(owner: .none, sessionLocked: true), note: "Windows 잠금 화면이라 아무도 입력할 수 없습니다")
            }
            Divider()
            HStack(spacing: DS.Space.s) {
                StatusDot(state: .ready)
                Text("개발 워크스테이션")
                    .font(DS.Font.headline)
                Spacer(minLength: DS.Space.s)
                ControlPill(owner: .ai, clientName: "Claude Code")
            }
            HStack(spacing: DS.Space.s) {
                Button("제어권 가져오기", systemImage: "hand.raised.fill") {}
                Spacer()
                Button("긴급 정지", systemImage: "stop.fill") {}
                    .buttonStyle(.borderedProminent)
                    .tint(DS.Color.danger)
            }
        }
    }

    private func pillRow(_ pill: ControlPill, note: String) -> some View {
        GridRow {
            pill
            Text(note)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
        }
    }

    // MARK: Capabilities

    private var capabilitySection: some View {
        GallerySection(title: "기능 상태", component: "CapabilityChip",
                       note: "에이전트가 보고한 기능별 상태입니다. 칩에 포인터를 올리면 이유가 보입니다.") {
            Grid(alignment: .leading, horizontalSpacing: DS.Space.m, verticalSpacing: DS.Space.s) {
                ForEach(CapabilityState.allCases, id: \.self) { state in
                    GridRow {
                        CapabilityChip(name: sampleName(for: state), state: state)
                        Text(state.title)
                            .font(DS.Font.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            Divider()
            FlowLayout {
                CapabilityChip(name: "화면", state: .ready)
                CapabilityChip(name: "입력", state: .ready)
                CapabilityChip(name: "UI 자동화", state: .ready)
                CapabilityChip(name: "명령 실행", state: .ready)
                CapabilityChip(name: "파일", state: .ready)
                CapabilityChip(name: "프로세스", state: .ready)
                CapabilityChip(name: "터미널", state: .ready)
                CapabilityChip(name: "클립보드", state: .needsUser)
                CapabilityChip(name: "진단", state: .unavailable)
                CapabilityChip(name: "터널", state: .unsupported)
            }
        }
    }

    private func sampleName(for state: CapabilityState) -> String {
        switch state {
        case .ready: "화면"
        case .unavailable: "진단"
        case .unsupported: "터널"
        case .needsUser: "클립보드"
        }
    }

    // MARK: Steps

    private var stepSection: some View {
        GallerySection(title: "단계 진행", component: "StepProgressView",
                       note: "원격 작업은 단계마다 상태를 보여 줍니다. 실패한 단계에는 원인이 붙습니다.") {
            HStack(alignment: .top, spacing: DS.Space.l) {
                VStack(alignment: .leading, spacing: DS.Space.m) {
                    caption("설치 진행 중")
                    StepProgressView(steps: [
                        StepItem(id: "probe", title: "SSH 연결 확인", detail: "windows · 192.168.0.24", status: .done),
                        StepItem(id: "copy", title: "에이전트 복사", detail: "18.4 MB", status: .done),
                        StepItem(id: "init", title: "초기화", detail: "인증서 만드는 중…", status: .active),
                        StepItem(id: "install", title: "예약 작업 등록", status: .pending),
                        StepItem(id: "verify", title: "연결 확인", status: .pending),
                    ])
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                VStack(alignment: .leading, spacing: DS.Space.m) {
                    caption("설치 실패")
                    StepProgressView(steps: [
                        StepItem(id: "probe", title: "SSH 연결 확인", detail: "windows · 192.168.0.24", status: .done),
                        StepItem(id: "copy", title: "에이전트 복사", detail: "디스크 공간이 부족합니다. 필요 120\u{00A0}MB, 남은 공간 34\u{00A0}MB", status: .failed),
                        StepItem(id: "init", title: "초기화", status: .pending),
                        StepItem(id: "install", title: "예약 작업 등록", status: .pending),
                        StepItem(id: "verify", title: "연결 확인", status: .pending),
                    ])
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    // MARK: Errors

    private var errorSection: some View {
        GallerySection(title: "오류", component: "ErrorBanner",
                       note: "무엇이 실패했는지, 어떻게 고치는지, 오류 코드를 함께 보여 줍니다.") {
            ErrorBanner(
                message: "원격 PC 세션이 잠겨 있습니다",
                fix: "Windows에서 잠금을 해제하면 자동으로 다시 연결합니다.",
                code: "SESSION_LOCKED",
                retry: {}
            )
            ErrorBanner(
                message: "데몬을 시작할 수 없습니다",
                fix: "로그를 확인한 뒤 다시 시도하세요.",
                retry: {}
            )
            ErrorBanner(message: "전송한 파일의 해시가 일치하지 않습니다", code: "HASH_MISMATCH")
        }
    }

    // MARK: Key-value

    private var keyValueSection: some View {
        GallerySection(title: "속성", component: "KeyValueGrid",
                       note: "경로·ID는 고정폭 글꼴로 표시하고 선택해 복사할 수 있습니다.") {
            KeyValueGrid([
                KeyValueRow("운영체제", "Windows 11 Pro 24H2 (x64)"),
                KeyValueRow("세션", "콘솔 · yang (세션 1)"),
                KeyValueRow("해상도", "3840 × 2160 · 150%"),
                KeyValueRow("에이전트", "0.1.0 (6836a47)", monospaced: true),
                KeyValueRow("호스트 ID", "h_7f3a9c21e4b0", monospaced: true),
                KeyValueRow("작업 루트", #"C:\Users\yang\Deskhand\work\qa-2026-10-20"#, monospaced: true),
            ])
        }
    }

    // MARK: Empty

    private var emptySection: some View {
        GallerySection(title: "빈 상태", component: "EmptyStateView",
                       note: "내용이 없을 때 다음 행동을 바로 제안합니다.") {
            EmptyStateView(
                symbol: "desktopcomputer",
                title: "아직 연결한 컴퓨터가 없습니다",
                message: "SSH로 접속할 수 있는 Windows PC를 추가하면 화면, 터미널, 파일을 한곳에서 다룰 수 있습니다.",
                actionTitle: "컴퓨터 추가",
                action: {}
            )
            .frame(height: 216)
        }
    }

    // MARK: Tokens

    private var tokenSection: some View {
        GallerySection(title: "토큰", component: "DS", note: "화면 코드는 고정 색·크기 대신 이 토큰만 씁니다.") {
            Grid(alignment: .leading, horizontalSpacing: DS.Space.l, verticalSpacing: DS.Space.s) {
                GridRow {
                    swatch(DS.Color.accent, "accent", "강조")
                    swatch(DS.Color.success, "success", "성공")
                }
                GridRow {
                    swatch(DS.Color.human, "human", "사람 조작")
                    swatch(DS.Color.warning, "warning", "경고")
                }
                GridRow {
                    swatch(DS.Color.ai, "ai", "AI 조작")
                    swatch(DS.Color.danger, "danger", "위험·정지")
                }
                GridRow {
                    swatch(DS.Color.observe, "observe", "관찰")
                    swatch(DS.Color.separator, "separator", "구분선")
                }
                GridRow {
                    swatch(DS.Color.surface, "surface", "배경", bordered: true)
                    swatch(DS.Color.surfaceRaised, "surfaceRaised", "패널", bordered: true)
                }
            }
            Divider()
            HStack(alignment: .top, spacing: DS.Space.xl) {
                VStack(alignment: .leading, spacing: DS.Space.s) {
                    caption("간격")
                    HStack(alignment: .bottom, spacing: DS.Space.s) {
                        spaceBar(DS.Space.xs, "xs")
                        spaceBar(DS.Space.s, "s")
                        spaceBar(DS.Space.m, "m")
                        spaceBar(DS.Space.l, "l")
                        spaceBar(DS.Space.xl, "xl")
                        spaceBar(DS.Space.xxl, "xxl")
                    }
                }
                VStack(alignment: .leading, spacing: DS.Space.s) {
                    caption("모서리")
                    HStack(alignment: .bottom, spacing: DS.Space.s) {
                        radiusTile(DS.Radius.s, "s")
                        radiusTile(DS.Radius.m, "m")
                        radiusTile(DS.Radius.l, "l")
                    }
                }
            }
            Divider()
            caption("글꼴")
            VStack(alignment: .leading, spacing: DS.Space.s) {
                typeRow("display", Text("작업 공간").font(DS.Font.display))
                typeRow("title", Text("원격 PC 제어").font(DS.Font.title))
                typeRow("headline", Text("세션 정보").font(DS.Font.headline))
                typeRow("body", Text("연결이 준비되었습니다.").font(DS.Font.body))
                typeRow("caption", Text("3분 전 마지막 확인").font(DS.Font.caption).foregroundStyle(.secondary))
                typeRow("mono", Text(#"C:\ProgramData\Deskhand"#).font(DS.Font.mono))
            }
        }
    }

    private func swatch(_ color: Color, _ token: String, _ meaning: String, bordered: Bool = false) -> some View {
        HStack(spacing: DS.Space.s) {
            RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
                .fill(color)
                .frame(width: DS.Space.xl, height: DS.Space.xl)
                .overlay {
                    if bordered {
                        RoundedRectangle(cornerRadius: DS.Radius.s, style: .continuous)
                            .strokeBorder(DS.Color.separator)
                    }
                }
            VStack(alignment: .leading, spacing: 0) {
                Text(token)
                    .font(DS.Font.mono)
                Text(meaning)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func spaceBar(_ value: CGFloat, _ name: String) -> some View {
        VStack(spacing: DS.Space.xs) {
            RoundedRectangle(cornerRadius: 2)
                .fill(DS.Color.accent.opacity(0.7))
                .frame(width: value, height: value)
            Text(name)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
            Text("\(Int(value))")
                .font(DS.Font.mono)
                .foregroundStyle(.tertiary)
        }
    }

    private func radiusTile(_ radius: CGFloat, _ name: String) -> some View {
        VStack(spacing: DS.Space.xs) {
            RoundedRectangle(cornerRadius: radius, style: .continuous)
                .fill(DS.Color.surface)
                .overlay(RoundedRectangle(cornerRadius: radius, style: .continuous).strokeBorder(DS.Color.separator, lineWidth: 1))
                .frame(width: DS.Space.xxl, height: DS.Space.xxl)
            Text(name)
                .font(DS.Font.caption)
                .foregroundStyle(.secondary)
            Text("\(Int(radius))")
                .font(DS.Font.mono)
                .foregroundStyle(.tertiary)
        }
    }

    private func typeRow(_ token: String, _ sample: some View) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: DS.Space.m) {
            Text(token)
                .font(DS.Font.mono)
                .foregroundStyle(.tertiary)
                .frame(width: 72, alignment: .leading)
            sample
        }
    }

    private func caption(_ text: String) -> some View {
        Text(text)
            .font(DS.Font.caption.weight(.semibold))
            .foregroundStyle(.secondary)
            .textCase(.uppercase)
    }
}

/// Card with a section title, the component name and a one-line note.
private struct GallerySection<Content: View>: View {
    let title: String
    let component: String
    let note: String
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: DS.Space.m) {
            VStack(alignment: .leading, spacing: DS.Space.xs) {
                HStack(alignment: .firstTextBaseline) {
                    Text(title)
                        .font(DS.Font.headline)
                    Spacer()
                    Text(component)
                        .font(DS.Font.mono)
                        .foregroundStyle(.tertiary)
                }
                Text(note)
                    .font(DS.Font.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            content
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .dsCard()
    }
}
