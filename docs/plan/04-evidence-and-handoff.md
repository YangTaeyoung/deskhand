# 04. 현재 실측·공식 레퍼런스·인계 정보

기준일: 2026-10-20. 외부 문서는 변경될 수 있으므로 구현 시작 때 설치 버전과 함께 재확인한다. 아래 “문서 확인”은 현재 머신의 기능 검증을 뜻하지 않는다.

## 1. 실측 근거

| ID | 확인한 사실 | 근거/상태 |
|---|---|---|
| E1 | SSH 실행은 Session 0, 사용자 RDP 세션은 1인 Windows 11 Pro x64 실기기 | 이번 세션 SSH/PowerShell/quser 출력. SSH 성공과 UI 실행 가능을 구분 |
| E2 | Windows-MCP 0.8.8, uv 0.13.3, Python 3.14.8 설치 | `~/dev/_runtime/windows-mcp/README.md:9` |
| E3 | Interactive/Limited 예약 작업, localhost:18765 + Bearer, 무인증 요청 401 | 같은 README:10-13,25 및 작업/리스너 조회 |
| E4 | 메모장 실행·영어/한글 입력·저장·파일 read 일치·본문 스크린샷 성공 | 같은 README:19-26, `capture-1792464898895232000-1.png` |
| E5 | PowerShell stdout과 의도한 exit 7 확인; 독립 MCP 연결 2회에서 21개 도구 확인 | 같은 README:23-24 |
| E6 | GetCursorPos error 5로 CONTROL_PREEMPTED/unavailable 반복. Jump Raise 후 진단 성공, MCP 재시작 뒤 입력 성공 | 같은 README:31-35. 정확한 전체 발생 조건/뷰어 종료 후 운용 미검증 |
| E7 | SSH 끊김 이후 자동 터널 재생성 없음 | `~/dev/_runtime/windows-mcp/proxy.py:43-58`: 상태 확인은 시작 시에만 수행 |
| E8 | 현 macOS Jump Desktop 앱 버전 8.8.28; jump-cli는 현재 shell PATH에서 발견되지 않음 | `/Applications/Jump Desktop.app/Contents/Info.plist` CFBundleShortVersionString 및 `command -v jump-cli`. 시스템 전체 미설치 단정은 아님 |
| E9 | Windows-MCP는 화면·파일·명령·프로세스 등을 한 서버에 등록 | `~/dev/_runtime/windows-mcp/package-source/windows_mcp/tools/__init__.py:19-44` |
| E10 | 표시기에서 GetCursorPos 실패 시 예외 발생 | `~/dev/_runtime/windows-mcp/package-source/windows_mcp/desktop/control_overlay.py:195-197` |

E6을 Windows-MCP 전체 또는 Jump 모든 버전의 일반 결함으로 단정하지 않는다. 지금 실험 구성의 관측이다. 공식 Jump CLI는 이번 기획 조사에서 새로 발견했으며 설치/인증/원격 조작은 수행하지 않았다.

## 2. 공식 레퍼런스와 설계 영향

### R1. Jump CLI와 MCP — 우선 검증 후보

- [Jump CLI 시작](https://docs.jumpdesktop.com/cli/): Beta, macOS/Windows/Linux 클라이언트와 계정 인증 경로를 문서화한다. viewer와 관리용 API token이 구분된다.
- [Jump CLI MCP](https://docs.jumpdesktop.com/cli/mcp/): 화면·마우스/키보드·클립보드·디스플레이 제어와 Mission Control의 관찰/인계를 문서화한다. MCP 사용에는 Teams 구독이 필수가 아니라고 명시한다. 원격 호스트에는 Connect가 필요하다.
- [명령 레퍼런스](https://docs.jumpdesktop.com/cli/reference/): 실제 설치 버전의 `--help`, `version`, tool schema와 대조한다.

설계 영향: 자체 화면·입력 서버부터 새로 만들지 않는다. 별도 파일·명령·디버거 기능이 있는지는 실제 스키마를 확인한다. 공식 MCP의 저장 파일이 세션 종료 때 정리되는 조건을 고려해 QA 증거를 작업 저장소로 명시적으로 반출한다.

### R2. Jump 연결 실행

- [Launch links and connection files](https://docs.jumpdesktop.com/viewer/launch-links/): 외부 앱에서 Jump 연결을 열 수 있다. 현재 문서의 RDP/VNC 링크와 Fluid 링크 생성 경로를 구분한다. Fluid URL은 임의 조립하지 않는다.
- [Cloudless Fluid CLI](https://docs.jumpdesktop.com/deployment/cloudless-fluid-cli/): Connect 버전·Enterprise 조건이 있는 별도 경로다. 일반 Jump CLI MCP 사용 조건과 섞지 않는다.

설계 영향: “Jump 내부 플러그인”은 미확인, 외부 연동은 공식 인터페이스 우선. 현재 설치된 8.8.28이 최신 문서의 모든 기능을 지원한다고 간주하지 않는다.

### R3. 파일은 별도 경로

[공식 Fluid 클립보드 문서](https://docs.jumpdesktop.com/viewer/session/clipboard/)는 양방향 텍스트 공유를 설명하며 이미지·파일 복사는 제외한다. 파일 전송/이동 요구를 이 기능에 맡기지 않는다. RDP 리다이렉션 여부와 본 제품의 재개·hash·move 계약은 별도로 검증한다.

### R4. 디스플레이 유지

[Fluid 디스플레이·가상 화면](https://support.jumpdesktop.com/hc/en-us/articles/45768818796813-General-Using-the-Display-menu-and-virtual-displays-in-Fluid-sessions)에는 가상 화면과 연결 종료 후 유지 옵션, 버전·플랜 조건이 설명돼 있다. 이것이 Windows UI helper의 모든 입력 세션 문제를 해결한다는 증거는 아니다. 실제 세션 상태와 A03을 확인한다.

### R5. Windows 서비스와 UI 세션

[Microsoft Interactive Services](https://learn.microsoft.com/en-us/windows/win32/services/interactive-services)는 서비스의 직접 UI 상호작용 제한과 사용자 세션의 별도 프로세스/IPC 방식을 설명한다. UI helper를 Session 0의 shell과 동일시하지 않는다.

### R6. Windows 관찰·입력

- [GetCursorPos](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getcursorpos): window station 권한 및 input desktop 조건.
- [SendInput](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-sendinput): UIPI에 따른 입력 제한.
- [UI Automation](https://learn.microsoft.com/en-us/dotnet/framework/ui-automation/ui-automation-overview): UI 요소/패턴 기반 접근 근거.
- [Desktop Duplication](https://learn.microsoft.com/en-us/windows/win32/direct3ddxgi/desktop-dup-api): 자체 캡처가 필요할 경우의 후보. 단독으로 원격 연결/입력/세션 유지 전체를 제공한다고 간주하지 않는다.

### R7. Electron 디버깅

[Electron main process debugging](https://www.electronjs.org/docs/latest/tutorial/debugging-main-process): main 프로세스 디버깅은 renderer DevTools와 구분된다. 기존 포트 전달을 삭제하기 전에 대상 앱 실행 인수와 실제 역할을 확인한다.

### R8. Electron 자동화

[Playwright Electron API](https://playwright.dev/docs/api/class-electron): Electron 지원은 experimental이다. 패키지된 대상 앱에 대한 동작은 실제 검증이 필요하다.

### R9. 디버거 프로토콜

[DAP overview](https://microsoft.github.io/debug-adapter-protocol/overview): 공통 디버거 인터페이스 후보. DAP 자체가 모든 런타임의 디버거를 제공하지 않으므로 실제 어댑터와 기능 협상이 필요하다.

### R10. Windows-MCP

[프로젝트](https://github.com/CursorTouch/Windows-MCP), [설치 버전 0.8.8](https://pypi.org/project/windows-mcp/0.8.8/). 현재 실험의 소스 E9/E10과 근거를 함께 읽는다. main 문서와 설치 wheel 기능이 다를 수 있다.

## 3. 기존 자산 인계

| 자산 | 위치/역할 | 주의 |
|---|---|---|
| 실험 런타임 | `~/dev/_runtime/windows-mcp/` | 제품 repo 아님. README/proxy.py/call.py/샘플 소스/테스트 캡처 |
| Codex 등록 | `~/.codex/config.toml`의 `mcp_servers.windows-pc` | 다른 MCP 설정 보존. 전체 config/token 출력 금지 |
| Windows 구성 | 사용자 홈 `.windows-mcp/`, 예약 작업 `windows-mcp-server` | 진단 task는 제거됨. 서버는 사용자 로그인 시 실행하도록 등록 |
| 현재 SSH | `ssh windows` 프로필 | 기존 9222/9229 전달 존재. 임의 삭제/중복 바인딩하지 않는다 |
| 개인 VM 프로젝트 | `~/dev/personal/alcove` | 과거 메모에서 재사용 후보로 식별했을 뿐 이번에 최신 코드 감사하지 않음 |
| 기존 제품 테스트 | Chainshift Desktop | 현재 작업 대상 앱 후보이며 본 기획과 별개 제품. 변경은 별도 범위로 수행 |

비밀값은 이 문서에 없다. `auth-token`, 원격 config, SSH 키, Jump 계정 자격증명은 인계 문서·스크린샷·Git에 복사하지 않는다. 기존 실험의 고정 사용자/IP/경로는 제품 설정 모델로 옮긴다.

## 4. 구현 에이전트 시작 지시

1. 이 폴더의 01→02→03→04를 읽고 사용자 요구를 유지한다. 현재 작업은 문서만 완료된 상태다.
2. 실제 코드 저장소를 정할 때 해당 경로 지침과 현재 git 상태를 확인한다. Alcove를 고치거나 새 repo를 만드는 것은 아직 결정되지 않았다.
3. M0에서 Jump CLI와 현재 설치 환경을 검증한다. 가장 먼저 현재 기능/버전/권한/플랜 차이를 기록한다.
4. M0 결과를 바탕으로 작은 보완 계층을 설계한다. 못 쓰는 API를 가정하거나 Windows-MCP 설치 성공을 전체 제품 성공으로 대체하지 않는다.
5. 이 문서의 P0/제어권/재연결/파일 이동 무결성 기준을 구현 계획·테스트에 매핑한다.
6. 문서만으로 결정 가능한 구현 선택은 자율 진행한다. 비용 발생·새 계정/권한·사용자 로그인 등 실제 개입이 필요한 지점은 준비된 화면과 이유를 제시한다.

## 5. 미확정 결정 목록

| 결정 | 현재 권장값 | 확정 근거 |
|---|---|---|
| 제품명/저장소 | Deskhand(확정), 독립 저장소 여부 미정 | 기존 코드 재사용 비용과 제품 경계 |
| 화면/입력 provider | 공식 Jump CLI 우선 | M0 실측, 세션 유지/인계/버전 |
| 원격 helper 필요 범위 | 파일·명령은 기존 SSH부터, UIA/작업 영속성 부족 시 helper | capability 차이와 복구 시험 |
| 서비스/로그온 실행 구조 | 일반 사용자 UI helper, 필요 시 별도 supervisor | 재부팅·로그인 전 요구와 최소 권한 |
| 앱 프레임워크/언어 | CLI·공유 코어는 Go(사용자 선호), GUI 프레임워크 미정 | CLI/MCP 코어 공유, 패키징, 기존 자산 재사용 |
| UI 임베딩 | 외부 Jump/Mission Control 기본 | 공개 지원 인터페이스와 라이선스 검증 |
| 파일 transport | SSH/SFTP 또는 agent streaming 후보 | SFTP 실측, 재개/무결성·성능 |
| macOS/Linux 원격·ARM64 | 후속 지원 | 첫 Windows x64 성공 후 별도 matrix |
| WAN/중계·오프라인 | LAN/VPN 우선 | 비용/네트워크 요구 및 공식 provider 범위 |

**문서의 제안은 구현 완료 증거가 아니다. 구현자는 검증한 범위와 남은 제약을 나누어 보고한다.**
