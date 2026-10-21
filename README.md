# Deskhand

Mac에서 Windows PC를 직접 연결해 조작하고, 화면·터미널·파일·프로세스·로그·디버거·QA 작업을 한곳에서 다루는 자체 원격 워크벤치다. Windows의 `deskhand-agent`와 Mac의 `Deskhand.app`/`deskhand` CLI·데몬이 기본적으로 UDP 47600의 QUIC으로 통신한다. AI 도구는 데몬의 MCP 서버를 통해 같은 작업을 요청할 수 있다.

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

실기기 검증 결과와 남은 제한은 [통합 검증 기록](docs/acceptance/integration.md)을 참고한다. 현재 Windows 캡처 경로의 프레임률과 직접 LAN UDP 연결이 릴리스 판정의 미해결 항목이다.
