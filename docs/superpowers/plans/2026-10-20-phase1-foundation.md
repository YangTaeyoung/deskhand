# Deskhand 1단계: 기반 (프로토콜·전송·에이전트·페어링·데몬·CLI·MCP) 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mac에서 `deskhand host add windows-dev --ssh windows`로 Windows PC에 에이전트를 설치·페어링하고, QUIC 상호 TLS로 명령·파일·프로세스 작업을 CLI와 MCP에서 실행한다. 요청 ID로 결과 불명을 확정한다.

**Architecture:** 에이전트는 Alcove `guest-tools`를 가져와 rpc 처리기를 QUIC 스트림 위로 옮긴다. 데몬은 호스트 연결·재연결·요청 ID·작업 저장을 소유하고 unix socket JSON-RPC로 CLI와 MCP를 받는다.

**Tech Stack:** Go 1.25.0, quic-go v0.64.x, modernc.org/sqlite, github.com/modelcontextprotocol/go-sdk v1.8.x, golang.org/x/sys.

**Spec:** `docs/design.md`, `docs/protocol.md`, `docs/local-api.md`, 요구사항 `docs/plan/`.

## Global Constraints

- 모듈 경로 `github.com/YangTaeyoung/deskhand`, `go 1.25.0`. 에이전트는 `CGO_ENABLED=0 GOOS=windows GOARCH=amd64`(와 arm64)로 Mac에서 교차 빌드된다. cgo 금지.
- Alcove 원본은 `~/dev/personal/alcove/guest-tools`(커밋 `23ec34d`). 읽기만 하고 수정하지 않는다. MIT, 같은 저작자. `NOTICE.md`에 출처를 적는다.
- 주석은 코드가 지금 하는 일만 쓴다. 히스토리·해명·측정치·검수 경위를 주석과 테스트 이름에 넣지 않는다.
- 사용자에게 보이는 문장(CLI 오류, MCP 오류 `error`)은 다음 행동을 알려 주는 한 문장. CLI 사람용 출력은 한국어, `--json` 키는 영어 snake_case.
- 비밀값(키 파일 내용, 토큰)을 argv·로그·출력·테스트 산출물에 넣지 않는다. OS 키체인·Credential Manager를 쓰지 않는다. 키는 권한 0600 파일.
- 테스트는 `DESKHAND_HOME`을 임시 디렉터리로 지정한다. 사용자 실제 홈의 Deskhand 데이터를 건드리지 않는다.
- SSH는 항상 `-o BatchMode=yes -o ClearAllForwardings=yes`. `~/.ssh`를 수정하지 않는다. SSH 호스트 키 검증을 끄지 않는다.
- Windows 실기기(`ssh windows`)에서 사용자 세션을 끊는 동작(로그오프, 재부팅, tscon, RDP 끊기)과 기존 `windows-mcp-server` 예약 작업 변경은 금지.
- 에이전트가 시작하는 자식 프로세스는 사용자 환경·PATH·AutoRun을 바꾸지 않는다(Alcove `childenv` 규칙 유지).
- 기본 포트: 에이전트 UDP 47600. 방화벽 규칙 이름 `Deskhand Agent`, `remoteip=LocalSubnet`, 프로토콜 UDP.
- 경로: Windows `C:\Program Files\Deskhand\deskhand-agent.exe`, `C:\ProgramData\Deskhand\{agent.json,agent.key,agent.crt,logs\,jobs\}`, 예약 작업 이름 `Deskhand Agent`. Mac `~/Library/Application Support/Deskhand/`(`DESKHAND_HOME`로 재정의), 소켓 `run/d.sock`.

## Review Focus

1. **job 실행 중 에이전트 재시작** — 재시작 뒤 `exec.list`·`req.status`가 그 job을 `exited`(코드 보존) 또는 `unknown`으로 보고해야 한다. 조용히 사라지면 안 된다. → Task 4 `TestJournalSurvivesRestart`.
2. **응답 직전 연결 끊김** — 데몬은 같은 `rid`를 재전송하지 않고 재연결 후 `req.status`로 확정한다. 이미 실행된 명령이 두 번 돌면 안 된다. → Task 6 `TestOutcomeResolvedAfterReconnect`.
3. **한글·공백·이모지 경로와 CP949 출력** — `fs.*`와 `exec.run` 결과가 UTF-8로 손상 없이 돌아와야 한다. → Task 9 E2E 단계 `korean-path`.
4. **재설치로 바뀐 인증서 지문** — 연결이 거절되고 오류가 `deskhand host repair <이름>`을 안내해야 한다. 조용한 재시도 루프 금지. → Task 3 `TestPinnedMismatchRejected`, Task 6 `TestFingerprintMismatchStopsRetry`.
5. **여러 클라이언트가 동시에 데몬을 띄움** — CLI 두 개가 동시에 실행돼도 데몬은 하나만 뜨고 둘 다 같은 데몬에 붙는다. → Task 6 `TestSingleDaemonUnderRace`.

---

### Task 1: 저장소 골격과 Alcove 코드 이식

**Files:**
- Create: `go.mod`, `.gitignore`, `LICENSE`(MIT, © 2026 YangTaeyoung), `NOTICE.md`, `README.md`(한국어 개요·빌드 방법), `scripts/build.sh`, `scripts/test.sh`
- Create(복사 후 import 경로 변경): `internal/proto/` ← `guest-tools/internal/proto`, `internal/agent/server/` ← `internal/server`, `internal/agent/logfile/`, `internal/agent/sysfolder/`, `internal/agent/clipboard/`, `internal/agent/session/`, `internal/agent/uia/`(assets 포함), `cmd/deskhand-fixture/` ← `cmd/alcove-fixture`
- 복사하지 않음: `internal/token`, `internal/mitm`, `cmd/alcove-proxy`, `cmd/alcove-agent`

**Interfaces:**
- Produces: 패키지 `github.com/YangTaeyoung/deskhand/internal/proto`(`Message`, `NewReader`, `NewWriter`, `Encode`), `.../internal/agent/server`(`New(Options) *Server`, `Server.Serve(net.Listener) error`, `Server.Close()`), `.../internal/agent/uia`, `.../internal/agent/session`.

- [ ] **Step 1:** 위 디렉터리를 복사하고 import 경로를 `github.com/YangTaeyoung/deskhand/internal/...`로 바꾼다. 사용자에게 보이는 이름(`alcove-agent`, `Alcove` 폴더·예약 작업·로그 문구)을 `deskhand-agent`, `Deskhand`로 바꾼다. `server`가 `token`·`mitm`에 의존하면 그 부분을 제거하거나 Task 4에서 대체할 수 있게 인터페이스로 끊는다(동작 변경은 Task 4).
- [ ] **Step 2:** `scripts/build.sh`: `build/deskhand`(darwin/arm64), `build/deskhand-agent-windows-amd64.exe`, `build/deskhand-agent-windows-arm64.exe`, `build/deskhand-fixture-windows-amd64.exe`를 만든다. 각 빌드에 `-ldflags "-X main.version=$(git describe --always --dirty)"`.
- [ ] **Step 3:** `scripts/test.sh`: `go vet ./...`, `go test ./...`(darwin), `GOOS=windows GOARCH=amd64 go vet ./...`, `GOOS=windows GOARCH=amd64 go test -c -o /dev/null` 각 패키지.
- [ ] **Step 4:** Run `scripts/test.sh` — Expected: 모두 통과(가져온 Alcove 테스트 포함). 실패하는 테스트가 VM 전용 전제(10.0.2.2, SMBIOS)라면 그 전제를 제거한 뒤 통과시키고, 무엇을 제거했는지 커밋 메시지에 적는다.
- [ ] **Step 5:** Commit `chore: 저장소 골격과 Alcove guest-tools 이식`.

### Task 2: `internal/wire` — 프레이밍과 메시지 타입

**Files:**
- Create: `internal/wire/frame.go`, `internal/wire/header.go`, `internal/wire/video.go`, `internal/wire/cursor.go`, `internal/wire/input.go`, `internal/wire/pty.go`, `internal/wire/errors.go`
- Test: `internal/wire/*_test.go`

**Interfaces:**
- Produces:
  - `const ALPN = "deskhand/1"`, `const MaxFrame = 32 << 20`, `const MaxHeader = 64 << 10`
  - `func WriteFrame(w io.Writer, kind byte, payload []byte) error` — `u32 BE (1+len(payload))`, kind, payload
  - `func ReadFrame(r io.Reader, max int) (kind byte, payload []byte, err error)` — 길이 0 또는 `> max`는 `ErrFrameTooLarge`/`ErrEmptyFrame`
  - `func WriteHeader(w io.Writer, v any) error`, `func ReadHeader(r *bufio.Reader, v any) error` — JSON 한 줄, `MaxHeader` 초과 시 `ErrHeaderTooLarge`
  - `type StreamHeader struct { S string; Display int; MaxFPS int; Bitrate int; MaxWidth int; Codec string; Gen uint64; Job string; New *PTYNew; Port int; TransferID string; Path string; Offset int64; Size int64; Length int64; MTime string }` (JSON 키는 protocol.md §2 이름, omitempty)
  - 종류 상수: `KindAU=0x01, KindVideoInfo=0x02, KindVideoStats=0x03, KindVideoPaused=0x04, KindCursorShape=0x10, KindCursorPos=0x11, KindPTYData=0x20, KindPTYResize=0x21, KindPTYExit=0x22, KindPTYAttached=0x23, KindNotice=0x30, KindClientControl=0x40, KindClientInput=0x41`
  - `type AU struct { Key, HasConfig bool; Width, Height uint32; Seq, CaptureMonoUS, EncodeDoneMonoUS uint64; Data []byte }` + `func (a AU) Marshal() []byte`, `func UnmarshalAU([]byte) (AU, error)`
  - `type CursorShape struct { ID uint32; W, H, HotX, HotY uint16; BGRA []byte }`, `type CursorPos struct { X, Y int32; Visible bool; ShapeID uint32 }` + Marshal/Unmarshal (BGRA 길이 ≠ W·H·4면 오류)
  - `type InputEvent struct { Seq, Gen uint64; K string; X, Y, Display int; B string; Down bool; DX, DY int; SC uint16; Ext bool; VK uint16; Text, Method string }` (JSON 키 protocol.md §6), `type InputAck struct { Seq uint64; OK bool; DoneMonoUS uint64; Code, Error string }`
  - 오류 코드 문자열 상수: protocol.md §8 전부 (`CodeOffline = "OFFLINE"` …)
- [ ] **Step 1: failing tests** — `TestFrameRoundTrip`(kind·payload 보존), `TestReadFrameRejectsOversize`(max+1 → `ErrFrameTooLarge`), `TestReadFrameRejectsZeroLength`, `TestHeaderRoundTrip`(StreamHeader 모든 필드), `TestHeaderTooLarge`, `TestAURoundTrip`(Key=true, HasConfig=true, 3840×2160, seq, 시각, 데이터 1 MiB), `TestCursorShapeLengthMismatch`, `TestInputEventJSONKeys`(`{"seq":1,"gen":2,"k":"key","sc":30,"ext":false,"down":true}` 직렬화가 정확히 이 키를 쓴다).
- [ ] **Step 2:** Run `go test ./internal/wire/` — Expected: FAIL(미정의).
- [ ] **Step 3:** 구현.
- [ ] **Step 4:** Run `go test ./internal/wire/` — Expected: PASS.
- [ ] **Step 5:** Commit `feat(wire): 스트림 프레이밍과 메시지 타입`.

### Task 3: `internal/transport` — 인증서와 QUIC 상호 TLS

**Files:**
- Create: `internal/transport/cert.go`, `internal/transport/quic.go`
- Test: `internal/transport/transport_test.go`

**Interfaces:**
- Consumes: `wire.ALPN`.
- Produces:
  - `type Identity struct { CertPEM, KeyPEM []byte; Fingerprint string }`
  - `func NewIdentity(commonName string) (Identity, error)` — Ed25519, 자체 서명, 유효 20년, SAN 없음
  - `func LoadIdentity(certPath, keyPath string) (Identity, error)`, `func (Identity) Save(certPath, keyPath string) error`(키 0600)
  - `func Fingerprint(der []byte) string` — SHA-256 소문자 hex
  - `func Listen(addr string, id Identity, allowedPeers func(fp string) bool) (*quic.Listener, error)`
  - `func Dial(ctx context.Context, addr string, id Identity, expectedPeer string) (*quic.Conn, error)` — quic-go v0.64의 연결 타입 이름을 그대로 쓴다
  - `var ErrPeerMismatch = errors.New(...)` — 지문 불일치 시 Dial/Accept가 이 오류(래핑)를 돌려준다
  - QUIC 설정: `KeepAlivePeriod: 5s`, `MaxIdleTimeout: 20s`, `MaxIncomingStreams: 1024`
- [ ] **Step 1: failing tests** — `TestMutualTLSConnects`(127.0.0.1:0에서 Listen, 지문 서로 고정, 스트림 하나 열어 "ping"→"pong"), `TestPinnedMismatchRejected`(클라이언트가 다른 지문을 기대 → `errors.Is(err, ErrPeerMismatch)`), `TestUnknownClientRejected`(서버 allowedPeers가 false → 클라이언트 쪽 연결이 핸드셰이크 오류로 끝남), `TestIdentitySaveLoad`(키 파일 권한 0600, 지문 동일).
- [ ] **Step 2:** Run `go test ./internal/transport/` — Expected: FAIL.
- [ ] **Step 3:** 구현. `tls.Config{ClientAuth: tls.RequireAnyClientCert, InsecureSkipVerify: true, VerifyPeerCertificate: <지문 검사>, NextProtos: []string{wire.ALPN}, MinVersion: tls.VersionTLS13}`.
- [ ] **Step 4:** Run — Expected: PASS.
- [ ] **Step 5:** Commit `feat(transport): QUIC 상호 TLS와 인증서 고정`.

### Task 4: 에이전트 — QUIC 위 rpc, 요청 ID 저널, 설치

**Files:**
- Modify: `internal/agent/server/server.go`, `rpc.go`(연결 처리기를 `io.ReadWriteCloser` 기반으로; Alcove 핸드셰이크·peer 검사·SMBIOS 토큰 제거)
- Create: `internal/agent/server/journal.go`(rid 저널), `internal/agent/server/hello.go`, `internal/agent/config/config.go`, `internal/agent/quicserve/serve.go`, `cmd/deskhand-agent/main.go`, `cmd/deskhand-agent/install_windows.go`, `cmd/deskhand-agent/install_other.go`
- Test: `internal/agent/server/journal_test.go`, `internal/agent/quicserve/serve_test.go`, `internal/agent/config/config_test.go`

**Interfaces:**
- Consumes: `transport.Listen`, `transport.Identity`, `wire.ReadHeader`, `proto.*`.
- Produces:
  - `type config.Agent struct { Listen string; ControllerFingerprints []string; DataDir string; Name string }` + `config.Load(path) (Agent, error)`, `config.Save(path, Agent) error`. 기본 `Listen=":47600"`, Windows 기본 `DataDir=C:\ProgramData\Deskhand`.
  - `func (s *server.Server) ServeConn(ctx context.Context, rw io.ReadWriteCloser, peer string)` — rpc 스트림 하나를 처리(hello 전송 → welcome 대기 → req/res/ev). 기존 `Serve(net.Listener)`는 테스트용으로 남겨도 되며 TCP 연결마다 `ServeConn`을 부른다.
  - `func quicserve.Run(ctx context.Context, cfg config.Agent, id transport.Identity, srv *server.Server, streams StreamHandlers) error` — 연결마다 첫 스트림을 `ServeConn`으로, 이후 스트림은 헤더의 `s`로 `StreamHandlers`(map[string]func(ctx, *quic.Stream, wire.StreamHeader))에 넘긴다. 이 단계에서 등록된 처리기는 없고 모르는 `s`는 `{"ok":false,"code":"CAPABILITY_UNSUPPORTED"}`로 닫는다.
  - 저널: `rid`가 있는 비멱등 op(`exec.run`, `exec.start`, `exec.write`, `exec.kill`, `proc.kill`, `fs.write`, `fs.rename`, `fs.mkdir`, `fs.delete`, `app.launch`, `clipboard.set`, `win.action`, `ui.act`)의 결과를 `DataDir/journal/<rid>.json`에 기록, 24시간 지난 항목 정리. `req.status{rid}` op 추가.
  - exec job 영속: `exec.start` job의 메타와 출력 끝 1 MiB를 `DataDir/jobs/<job>/`에 기록, 에이전트 재시작 시 읽어 `exec.list`에 `state:"exited"`(코드가 기록됐으면) 또는 `state:"lost"`로 보인다.
  - hello: protocol.md §3의 필드(`caps`는 이 단계에서 `exec`, `files`, `processes`, `uia`, `clipboard`만 실제 판정, 나머지는 `unsupported`).
  - CLI: `deskhand-agent run [--config PATH]`, `deskhand-agent init --name N --controller-fp FP [--listen :47600]`(키 생성, agent.json 작성, 지문을 JSON `{"fingerprint":"…","name":"…"}`로 stdout), `deskhand-agent install`(Windows: Program Files로 자기 복사, ProgramData ACL, 예약 작업 `Deskhand Agent` ONLOGON 최고 권한 대화형 `run`, 방화벽 UDP 47600 LocalSubnet, 즉시 `schtasks /Run`), `deskhand-agent uninstall`(작업·방화벽·Program Files 제거, `--purge`면 ProgramData까지), `deskhand-agent version`.
- [ ] **Step 1: failing tests** — `TestJournalReplaysSameRid`(같은 rid로 `exec.run` 두 번 → 두 번째는 `replayed:true`이고 명령은 한 번만 실행: 임시 파일에 한 줄씩 추가하는 명령으로 줄 수 1 확인), `TestJournalInProgress`(실행 중 같은 rid → `IN_PROGRESS`), `TestReqStatusUnknownRid`(`state:"unknown"`), `TestJournalSurvivesRestart`(서버 A에서 `exec.start`로 종료 코드 3 명령 실행·완료 → 서버 A 닫음 → 같은 DataDir로 서버 B → `exec.list`에 그 job이 `exited`, `code:3`), `TestServeOverQUIC`(Listen→Dial→첫 스트림에서 hello 수신, welcome 송신, `ping` 왕복), `TestUnknownStreamKindRefused`, `TestConfigRoundTrip`.
- [ ] **Step 2:** Run `go test ./internal/agent/...` — Expected: 새 테스트 FAIL.
- [ ] **Step 3:** 구현.
- [ ] **Step 4:** Run `scripts/test.sh` — Expected: PASS(darwin 테스트 + windows 컴파일).
- [ ] **Step 5:** Commit `feat(agent): QUIC rpc, 요청 ID 저널, 설치 명령`.

### Task 5: `internal/install` — SSH 설치·페어링

**Files:**
- Create: `internal/install/ssh.go`, `internal/install/pair.go`
- Test: `internal/install/pair_test.go`

**Interfaces:**
- Consumes: `transport.Identity`(데몬 측), `config` 필드 이름.
- Produces:
  - `type SSH struct { Alias string }`, `func (s SSH) Run(ctx, script string) (stdout, stderr string, err error)` — `ssh -o BatchMode=yes -o ClearAllForwardings=yes <alias> powershell -NoProfile -Command -`에 script를 stdin으로; 출력은 UTF-8로 받도록 스크립트 앞에 `[Console]::OutputEncoding=[Text.Encoding]::UTF8`를 붙인다. `func (s SSH) Copy(ctx, local, remote string) error`(scp 같은 옵션), `func (s SSH) ResolveAddress(ctx) (string, error)`(`ssh -G alias`의 `hostname`).
  - `type PairResult struct { Name, Address string; Port int; AgentFingerprint, OS, Arch string }`
  - `func Pair(ctx, s SSH, name string, controller transport.Identity, agentBinary func(arch string) (path string, err error), progress func(step string)) (PairResult, error)` — 단계: 원격 OS·아키텍처 확인 → 바이너리 복사(`%TEMP%\deskhand-install\`) → `deskhand-agent.exe init --name … --controller-fp …` → `install` → `version` 확인 → 결과. 각 단계 이름: `probe`, `copy`, `init`, `install`, `verify`.
  - `func Unpair(ctx, s SSH, purge bool) error`
- [ ] **Step 1: failing tests** — 가짜 실행기(`SSH`가 쓰는 명령 실행 함수를 주입 가능하게)로 `TestPairStepsInOrder`(progress 순서), `TestPairPassesNoSecretsInArgv`(실행된 argv에 키 PEM이 없음 — 지문만), `TestPairArchSelection`(원격 `ARM64` → arm64 바이너리 요청).
- [ ] **Step 2:** Run `go test ./internal/install/` — FAIL.
- [ ] **Step 3:** 구현.
- [ ] **Step 4:** Run — PASS.
- [ ] **Step 5:** Commit `feat(install): SSH 기반 에이전트 설치와 페어링`.

### Task 6: 데몬 — 저장소, 연결 관리, 로컬 API

**Files:**
- Create: `internal/daemon/store/store.go`(+`schema.sql` embed), `internal/daemon/conn/conn.go`(호스트 연결·재연결·rpc 클라이언트), `internal/daemon/conn/rpcclient.go`, `internal/daemon/hosts/hosts.go`, `internal/daemon/jobs/jobs.go`, `internal/daemon/localapi/server.go`, `internal/daemon/localapi/methods_*.go`, `internal/daemon/daemon.go`, `internal/daemon/paths.go`, `internal/client/client.go`(Go 클라이언트 + 데몬 자동 시작)
- Test: 각 패키지 `_test.go`, `internal/daemon/integration_test.go`(실제 에이전트 서버를 같은 프로세스에서 QUIC로 띄워 데몬과 연결)

**Interfaces:**
- Consumes: `transport.Dial`, `wire.*`, `install.Pair`, `proto`.
- Produces:
  - `paths.Home() string`(`DESKHAND_HOME` 또는 `~/Library/Application Support/Deskhand`), `paths.Socket()`, `paths.DB()`, `paths.Artifacts()`, `paths.HostDir(hostID)`.
  - SQLite 테이블: `hosts(host_id PK, name UNIQUE, address, port, agent_fp, os, arch, ssh_alias, created_at)`, `jobs(job_id PK, host_id, kind, request_id, status, params_json, result_json, created_at, updated_at)`, `events(seq PK AUTOINCREMENT, ts, host_id, job_id, kind, payload_json)`, `requests(request_id PK, host_id, op, state, result_json, created_at)`.
  - `conn.Manager`: `Get(hostID) *conn.Host`, `Host.Call(ctx, op string, params proto.Message, opts CallOpts) (proto.Message, error)` — `CallOpts{RID string}`; 비멱등 op면 rid 자동 발급(`r-` + ULID). 연결이 응답 전에 끊기면 `requests.state='unknown'`, 재연결 후 `req.status`로 확정 → 확정 결과 반환 또는 `EXECUTION_OUTCOME_UNKNOWN` 오류(`retryable:false`, `next_action:"check_state"`).
  - `Host.State() ConnState`(`offline|connecting|ready|degraded|reconnecting|blocked`), `Host.Generation() uint64`, 상태 변화 이벤트 `connection.state`.
  - 지문 불일치는 `blocked` 상태로 멈추고 재시도하지 않는다(오류 문장: `"<이름>의 인증서가 페어링 때와 다릅니다. 재설치했다면 deskhand host repair <이름>을 실행하세요"`).
  - 로컬 API(local-api.md §1–3): 이 단계 범위 `hello`, `host.list`, `host.add`, `host.remove`, `host.connect`, `host.disconnect`, `host.doctor`(SSH 도달·에이전트 버전·QUIC 연결·지문·세션·방화벽 규칙 검사), `host.repair`(재페어링), `session.inspect`, `exec.run`, `exec.start`, `jobs.list`, `jobs.get`, `jobs.output`, `jobs.cancel`, `files.list`, `files.stat`, `files.read`, `files.write`, `files.mkdir`, `files.rename`, `files.delete`, `proc.list`, `proc.kill`, `app.launch`, `events.subscribe`. 결과 봉투는 local-api.md §2.
  - `client.Dial(ctx, clientKind, name string) (*client.Client, error)` — 소켓이 없거나 응답이 없으면 같은 실행 파일로 `deskhand daemon run`을 분리 실행하고 최대 5초 대기. 동시 시작 경쟁은 `run/daemon.lock`(flock)으로 하나만 뜨게 한다. `Client.Call(ctx, method string, params any, out any) error`, `Client.Subscribe(ctx, kinds []string) (<-chan Event, error)`.
- [ ] **Step 1: failing tests** — `TestStoreMigrates`(빈 DB → 테이블 생성, 두 번 열어도 동일), `TestCallAssignsRidForMutatingOps`, `TestOutcomeResolvedAfterReconnect`(가짜 에이전트가 `exec.run`을 실행하고 응답 직전 연결을 끊음 → 재연결 후 데몬이 `req.status`로 결과를 얻어 반환, 가짜 에이전트의 실행 횟수 1), `TestOutcomeUnknownWhenAgentLostIt`(에이전트가 `unknown` → `EXECUTION_OUTCOME_UNKNOWN`), `TestFingerprintMismatchStopsRetry`(상태 `blocked`, 재시도 0회), `TestReconnectBackoffIncreases`, `TestSingleDaemonUnderRace`(고루틴 둘이 동시에 `client.Dial` → 데몬 프로세스 하나 — 테스트에서는 데몬 시작 함수를 주입해 호출 횟수 1 확인), `TestLocalAPIExecRunEnvelope`(통합: 실제 에이전트 서버(darwin에서 `sh`) + 데몬 → `exec.run{argv:["echo","hi"]}` → 봉투 필드 전부 존재, `code:0`).
- [ ] **Step 2:** Run `go test ./internal/daemon/... ./internal/client/...` — FAIL.
- [ ] **Step 3:** 구현.
- [ ] **Step 4:** Run — PASS. `go test -race`도 PASS.
- [ ] **Step 5:** Commit `feat(daemon): 저장소, 호스트 연결·재연결, 결과 불명 확정, 로컬 API`.

### Task 7: CLI

**Files:**
- Create: `cmd/deskhand/main.go`, `internal/cli/root.go`, `internal/cli/host.go`, `internal/cli/exec.go`, `internal/cli/jobs.go`, `internal/cli/files.go`, `internal/cli/proc.go`, `internal/cli/daemon.go`, `internal/cli/output.go`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: `client.Dial`, 로컬 API 메서드.
- Produces 명령(인수 파싱은 표준 `flag` 또는 가벼운 하위 명령 분기, 외부 CLI 프레임워크 추가는 재량):
  - `deskhand host add <이름> --ssh <별칭> [--address A] [--port 47600]`, `host list`, `host remove <이름> [--uninstall]`, `host doctor <이름>`, `host repair <이름>`
  - `deskhand exec <호스트> [--cwd D] [--shell powershell|cmd|none] [--timeout 120s] [--detach] -- <argv…>` — 종료 코드를 그대로 프로세스 종료 코드로, stdout/stderr 분리 출력
  - `deskhand jobs list [--host H]`, `jobs watch <job-id> [--jsonl]`, `jobs cancel <job-id>`
  - `deskhand files ls|stat|cat|mkdir|mv|rm <호스트>:<경로> …` (`rm`은 휴지통, `--permanent`)
  - `deskhand proc ls <호스트> [--name N]`, `proc kill <호스트> <pid>`
  - `deskhand daemon run|start|stop|status`
  - 전역 `--json`(stdout에 `schema_version` 포함 JSON, 진단은 stderr), `--quiet`
  - 비대화 모드에서 사람 입력이 필요하면 대기하지 않고 상태 JSON과 종료 코드 3
- [ ] **Step 1: failing tests** — 가짜 로컬 API 서버로 `TestExecExitCodePassthrough`(원격 `code:7` → 종료 코드 7), `TestJSONHasSchemaVersion`, `TestHostPathParsing`(`windows-dev:C:\qa\a b.txt`, `windows-dev:'C:\한글\파일.txt'`), `TestDiagnosticsOnStderr`.
- [ ] **Step 2:** Run — FAIL. **Step 3:** 구현. **Step 4:** Run — PASS.
- [ ] **Step 5:** Commit `feat(cli): 호스트·명령·job·파일·프로세스 명령`.

### Task 8: MCP 서버 (기반 도구)

**Files:**
- Create: `internal/mcpserver/server.go`, `internal/mcpserver/tools_hosts.go`, `tools_exec.go`, `tools_files.go`, `tools_proc.go`, `internal/cli/mcp.go`(`deskhand mcp`)
- Test: `internal/mcpserver/server_test.go`

**Interfaces:**
- Consumes: `client.Client`, MCP go-sdk(`mcp.NewServer`, `mcp.AddTool`, `mcp.StdioTransport`, 테스트는 `mcp.NewInMemoryTransports`).
- Produces 도구(이름 고정): `hosts_list`, `host_doctor`, `session_inspect`, `exec_run`, `exec_start`, `job_get`, `job_output`, `job_cancel`, `files_list`, `files_stat`, `files_read`, `files_write`, `files_mkdir`, `files_move`, `files_delete`, `proc_list`, `proc_kill`, `app_launch`. 결과는 구조화 출력(봉투 그대로)과 짧은 텍스트 요약. 서버 instructions에 사용 순서(hosts_list → session_inspect → …)와 결과 불명 시 `job_get`/`session_inspect`로 먼저 확인하라는 규칙을 적는다.
- [ ] **Step 1: failing tests** — 인메모리 MCP 클라이언트로 `TestToolsListed`(위 이름 전부), `TestExecRunToolEnvelope`(가짜 로컬 API → 결과에 `host_id`, `status`, `action.executed`), `TestErrorCarriesCodeAndNextAction`(`CONTROL_HELD_BY_HUMAN` 오류가 도구 오류 결과로 code·next_action 포함).
- [ ] **Step 2–4:** FAIL → 구현 → PASS.
- [ ] **Step 5:** Commit `feat(mcp): 기반 MCP 도구`.

### Task 9: 실기기 E2E와 인수 기록

**Files:**
- Create: `scripts/e2e/phase1.sh`, `docs/acceptance/phase1.md`

**Interfaces:**
- Consumes: 전부.

- [ ] **Step 1:** `scripts/build.sh` → `DESKHAND_HOME=$(mktemp -d)`에서 `deskhand host add windows-dev --ssh windows`. Expected: 단계 `probe→copy→init→install→verify` 출력, 사용자 세션에서 에이전트 실행 중(`Get-Process deskhand-agent`의 SessionId가 1).
- [ ] **Step 2:** `deskhand exec windows-dev -- cmd /c "echo 안녕 & exit 7"` — Expected: stdout `안녕`, 종료 코드 7.
- [ ] **Step 3 (`korean-path`):** `deskhand files mkdir windows-dev:'C:\deskhand-e2e\한글 폴더 😀'` → `files write`/`cat` 왕복 → 내용 일치, `files ls` 이름 일치.
- [ ] **Step 4 (A05 근사):** 데몬에 테스트 훅 환경변수 `DESKHAND_TEST_DROP_AFTER_SEND=exec.run`을 두고 `exec`로 원격 파일에 한 줄 추가 → 데몬이 `req.status`로 결과 확정, 원격 파일 줄 수 1.
- [ ] **Step 5 (A04 일부):** 원격에서 에이전트 프로세스를 종료(`proc kill` 아님, SSH로 `Stop-Process`) 후 예약 작업으로 재시작 → 데몬 상태가 `reconnecting→ready`, generation +1, 이전 `exec.start` job이 `jobs.get`에서 결과 유지.
- [ ] **Step 6:** `deskhand mcp`를 MCP 클라이언트(go-sdk 클라이언트로 짠 짧은 Go 테스트 프로그램 `scripts/e2e/mcpprobe`)로 붙여 `hosts_list`, `exec_run` 호출.
- [ ] **Step 7:** `docs/acceptance/phase1.md`에 각 단계 명령·시각·기대·실제·증거(출력 일부) 기록. 실패는 FAIL로 남긴다.
- [ ] **Step 8:** E2E가 만든 원격 테스트 폴더 정리(에이전트는 남긴다). Commit `test: 1단계 실기기 E2E 기록`.
