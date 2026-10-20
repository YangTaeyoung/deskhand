# Deskhand

Mac 컨트롤러에서 Windows 에이전트를 원격으로 제어하고 AI 작업을 실행하는 사내용 원격 데스크톱·운영 도구다. Windows에는 `deskhand-agent`가 서비스처럼 상주하고, Mac의 `deskhand` CLI/데몬이 UDP 47600으로 연결해 화면·입력·파일·프로세스 작업을 수행한다.

## 구성

- `cmd/deskhand-fixture` — UI 테스트용 Win32 픽스처 앱
- `internal/proto` — 줄 단위 JSON 메시지 프로토콜
- `internal/agent/*` — 에이전트 구현 (`server`, `uia`, `session`, `clipboard`, `logfile`, `sysfolder`)
- `docs/` — 설계, 프로토콜, 로컬 API, 단계별 계획
- `scripts/` — 빌드·테스트 스크립트

## 빌드

```
scripts/build.sh
```

`build/` 아래에 `deskhand`(darwin/arm64), `deskhand-agent-windows-{amd64,arm64}.exe`, `deskhand-fixture-windows-amd64.exe`를 만든다. 에이전트는 `CGO_ENABLED=0`으로 Mac에서 교차 빌드한다.

## 테스트

```
scripts/test.sh
```

`go vet`, `go test`(darwin), Windows 대상 `go vet`와 패키지별 테스트 바이너리 컴파일을 차례로 실행한다.
