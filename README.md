# Deskhand

Mac에서 Windows PC를 직접 연결해 조작하고, 화면·터미널·파일·프로세스·로그·디버거·QA 작업을 한곳에서 다루는 자체 원격 워크벤치다. 화면은 앱에 포함된 FreeRDP로 Windows 내장 RDP 서버에 연결한다. Windows의 `deskhand-agent`와 Mac의 `Deskhand.app`/`deskhand` CLI·데몬은 기본적으로 UDP 47600의 QUIC으로 작업·제어권·입력을 주고받는다. AI 도구는 데몬의 MCP 서버를 통해 같은 작업을 요청할 수 있다.

## Mac 설치

첫 배포는 **0.1.0 Preview 1**, Apple Silicon·macOS 26 이상을 대상으로 한다. GitHub Release의 DMG에서 `Deskhand.app`을 `Applications`로 드래그한다. Windows PC에는 SSH 연결과 원격 데스크톱 호스트 기능이 준비돼 있어야 한다. [설치 안내](docs/releases/INSTALL-ko.txt)와 [릴리즈 노트](docs/releases/0.1.0-preview.1.md)를 참고한다.

화면 탭에서 Windows 계정 암호로 로그인하며 PIN은 사용하지 않는다. 암호를 저장하지 않는다. 모니터 전원과 별도 가상 디스플레이 드라이버는 기본 연결 요건이 아니다.

## 구성

- `cmd/deskhand-fixture` — UI 테스트용 Win32 픽스처 앱
- `app/` — macOS SwiftUI 앱, 로컬 API 클라이언트, 화면 디코더 및 스냅샷
- `internal/transport` — 인증된 QUIC 연결
- `internal/daemon` — Mac 로컬 API, 연결·제어권·스트림·QA 관리
- `internal/agent/*` — 에이전트 구현 (`server`, `uia`, `session`, `clipboard`, `logfile`, `sysfolder`)
- `docs/` — 설계, 프로토콜, 로컬 API, 단계별 계획
- `scripts/` — 빌드·테스트 스크립트

## 빌드

```
scripts/build.sh
app/scripts/build-app.sh
```

`build/` 아래에 CLI와 Windows 에이전트·테스트 픽스처를, `app/build/Deskhand.app`에 데몬과 에이전트를 포함한 Mac 앱을 만든다. Windows 에이전트는 `CGO_ENABLED=0`으로 Mac에서 교차 빌드한다.

## 테스트

```
scripts/test.sh
cd app/DeskhandKit && swift test
```

`go vet`, `go test`(darwin), Windows 대상 `go vet`와 패키지별 테스트 바이너리 컴파일을 차례로 실행한다.

실기기 검증 결과와 남은 제한은 [통합 검증 기록](docs/acceptance/integration.md)을 참고한다. 공용 PC는 모니터를 켜 두지 않는 사용 조건이며 [헤드리스 화면 설계](docs/design/headless-display.md)에 검증 문턱을 기록했다. 직접 LAN QUIC 연결과 내장 RDP 로그인·자동 해상도는 실기기에서 확인했다. [성능 실측](docs/acceptance/rdp-performance.md)에서는 Windows 출력 약 30fps와 큰 창의 높은 비용을 확인했다. Mac 모니터 표시 완료 FPS·60fps·장기 복구는 검증하지 않았다. [AI 범위 제한](docs/local-api.md)은 같은 사용자 프로세스에 대한 보안 샌드박스가 아니며 개인정보·보존 설정의 전체 강제도 남아 있다. 따라서 이번 배포는 신뢰하는 개인 PC 환경용 프리뷰다.

## DMG 재현 빌드

```sh
app/scripts/package-release.sh 0.1.0-preview.1 \
  'Developer ID Application: YOUR_NAME (YOUR_TEAM)' YOUR_NOTARY_PROFILE
```

추적 중인 변경을 커밋한 뒤 실행한다. `Release` 구성은 `build/release-app/`에서 별도로 빌드하며 개발용 앱을 덮어쓰지 않는다. 앱·DMG의 Developer ID 서명·공증·staple과 SHA-256 생성을 완료한 결과는 `build/releases/<version>/`에 남는다. 자세한 절차는 [릴리즈 관리](docs/releases/README.md)에 기록한다.
