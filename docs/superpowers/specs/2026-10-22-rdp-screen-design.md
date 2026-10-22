# Deskhand RDP 화면 전환

2026-10-22 사용자가 RDP 화면 연결 + 자체 에이전트 통합 방향을 승인하고 실행을 요청했다. 기존 docs/design.md의 RDP 배제·자체 영상 전송 필수 조건을 대체한다.

## 목표

Mac Deskhand 안에서 Windows 내장 RDP 서버로 로그인하고 화면을 표시한다. 별도 Jump/Windows App 실행이나 커스텀 디스플레이 드라이버 없이 모니터가 없는 Windows PC를 사용한다. 기존 CLI/MCP 명령·파일·디버깅과 사람/AI 제어권을 유지한다.

## 구현 결정

- FreeRDP 3.32.2 (97eff6ddfbbfaa8e42a126d362686bd001ff8bdc, Apache-2.0)를 라이브러리로 사용한다. macOS용 자체 엔진과 SwiftUI 화면을 제공한다. 의존 소스는 build/rdp에 고정 체크아웃하고 재현 빌드한다.
- RDP는 화면·NLA 사용자 인증을 담당한다. 데스크톱 조작은 기존 에이전트 입력 스트림을 사용한다. RDP로 입력을 직접 보내면 에이전트의 writer generation/긴급 정지 검사를 우회하므로 초기 통합에서는 금지한다.
- 영상 캡처를 열지 않는 입력 전용 로컬 스트림을 추가한다. 기존 screen.Viewer의 CheckHuman/세대 재확인/키 해제/연결 종료 처리를 재사용한다. AI 호출자는 입력 스트림을 사람 권한으로 사용할 수 없다.
- Windows 로그인 정보는 앱 SecureField로 받고 연결 수명 내 메모리에서만 보유한다. CLI argv·파일·로그·UserDefaults·Keychain에 암호를 저장하지 않는다. 사용자에게 채팅으로 암호를 요구하지 않는다.
- 서버 인증서는 페어링된 에이전트가 조회한 RDP 리스너 인증서 SHA-256과 대조한다. 신원 조회 실패·변경·불일치 시 연결을 거부한다. 무조건 신뢰/cert-ignore/NLA 끄기는 금지한다.
- RDP의 사용자 SID·세션 ID와 에이전트 세션을 확인한 뒤 입력을 활성화한다. 다른 계정으로 로그인하거나 세션이 불일치하면 화면 관찰만 허용하고 입력은 차단한다. 화면 연결과 에이전트 연결 상태를 구분한다.
- 기존 영상 경로·스냅샷은 회귀 비교용으로 유지하되 기본 실제 화면은 RDP로 전환한다. 합성 스냅샷은 실기기 RDP 검증으로 해석하지 않는다.
- 이전 시험 드라이버·시험 인증서는 제거하고 부팅 정책을 바꾸지 않는다. preexisting spikes/는 건드리지 않는다.

## 검증과 완료 조건

라이브러리 빌드, 인증서 불일치 거부, 취소/재접속 수명, 입력 전용 스트림 무영상/권한/해제 회귀 테스트를 통과한다. 실제 Windows RDP 로그인 후 에이전트와 같은 세션임을 확인하고 움직임·입력 지연·제어권 선점·재연결을 측정한다. 프레임 수신과 Mac 표시 완료를 구분한다. 실제 측정·독립 UI 검수 전 상업용 완료로 표시하지 않는다.

## 근거

- https://learn.microsoft.com/en-us/windows/win32/termserv/consoles-vs-terminals
- https://github.com/FreeRDP/FreeRDP/releases/tag/3.32.2
- 고정 소스 client/Sample/tf_freerdp.c, client/common/client.c, include/freerdp/freerdp.h
- 기존 internal/daemon/screen/{viewer,input}.go: 사람 입력 세대와 연결 종료 해제
