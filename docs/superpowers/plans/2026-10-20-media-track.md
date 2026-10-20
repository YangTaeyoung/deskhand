# Deskhand 미디어 트랙 (에이전트 화면·입력·커서) 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Windows 에이전트가 사용자 세션의 화면을 캡처·인코딩해 `video`·`cursor` 스트림으로 보내고, `input` 스트림과 op로 마우스·키보드·텍스트를 주입하며, AI용 정지 화면(`screen.capture`)과 세션·디스플레이 상태를 정확히 보고한다.

**Architecture:** 순수 Go COM 호출(`syscall.SyscallN`)로 DXGI Desktop Duplication → D3D11 VideoProcessor(NV12) → Media Foundation H.264(하드웨어 비동기 MFT 우선, 소프트웨어 대체)를 구성한다. 스파이크 코드 `spikes/capture/`(com.go, d3d.go, mf.go, gdi.go)와 ADR 0001의 vtable 인덱스·순서를 그대로 출발점으로 쓴다.

**Tech Stack:** Go 1.26(module go 1.26.0), golang.org/x/sys/windows, quic-go 스트림(Task 4의 `quicserve.StreamHandlers`).

**Spec:** `docs/design.md` §4·§6·§7, `docs/protocol.md` §2·§3.1·§4–§6·§8, `docs/adr/0001-screen-pipeline.md`.

## Global Constraints

- 1단계 계획의 Global Constraints 전부(`docs/superpowers/plans/2026-10-20-phase1-foundation.md`).
- Windows 전용 코드는 `_windows.go`, 다른 OS에는 같은 API의 스텁이 `CAPABILITY_UNSUPPORTED`를 돌려준다. `GOOS=darwin go test ./...`와 `GOOS=windows go vet ./...`가 모두 통과한다.
- COM 객체는 만든 고루틴(`runtime.LockOSThread`)에서 해제한다. 모든 `Release`는 소유 범위가 끝날 때 한 번.
- 캡처는 사용자 세션에서만 동작한다. Session 0에서 호출되면 `DISPLAY_UNAVAILABLE`(사유 `session0`).
- 캡처 실패 사유 문자열(고정): `session0`, `rdp_output_suppressed`, `secure_desktop`, `session_locked`, `session_disconnected`, `access_lost`, `no_output`.
- 기본값: `max_fps` 60, 비트레이트 20 Mbps(원본 4K), 12 Mbps(≤2560 폭), 6 Mbps(≤1920 폭). 저지연 모드, CBR, GOP 0, 요청 키프레임, 정지 화면이면 프레임을 보내지 않고 10초마다 1회 반복 프레임(키프레임 아님)으로 생존을 알린다.
- 입력 좌표는 디스플레이 원본 픽셀. SendInput 절대 좌표는 가상 데스크톱 기준 0–65535로 환산(`MOUSEEVENTF_VIRTUALDESK`).
- 사용자 실기기에서 입력을 주입하는 테스트는 `deskhand-fixture` 창에만 한다(포커스 확인 후). 다른 창·바탕화면에 클릭·키 입력 금지.
- 테스트를 실기기 사용자 세션에서 돌릴 때는 `scripts/wintest.sh`(Task M1)를 쓴다. 테스트 후 예약 작업·임시 파일을 정리한다.

## Review Focus

1. **세션이 RDP에 붙고 클라이언트 창이 숨겨짐** — 캡처는 `DISPLAY_UNAVAILABLE`/`rdp_output_suppressed`를 보고하고 `video` 스트림에 `paused` 프레임을 보낸 뒤, 접근이 돌아오면 키프레임부터 재개한다. 무한 오류 로그·CPU 100% 금지. → M2 `TestAccessDeniedClassified`(가짜 HRESULT 주입), M3 `TestPausedThenResumesWithKeyframe`.
2. **해상도·DPI·디스플레이 구성 변경** — `DXGI_ERROR_ACCESS_LOST`/모드 변경 후 복제를 다시 만들고, 인코더를 새 크기로 재초기화해 키프레임과 새 `info`를 보낸다. → M3 `TestReinitOnSizeChange`.
3. **입력 중 스트림 끊김·제어권 이동** — 눌린 키·버튼이 남지 않는다. → M4 `TestReleaseAllOnStreamClose`, `TestStaleGenerationRejected`.
4. **관리자 권한 창(UIPI)·보안 데스크톱** — SendInput이 0을 반환하거나 입력 데스크톱이 Winlogon이면 `ELEVATION_REQUIRED`/`SESSION_LOCKED`로 보고하고 "성공"으로 표시하지 않는다. → M4 `TestInputDesktopWinlogonReported`.
5. **한글·이모지·줄바꿈 텍스트** — `unicode` 방식은 서로게이트 쌍과 `\n`(Enter)을 정확히 보낸다. `paste`는 원래 클립보드를 복원한다. → M4 `TestUnicodeSurrogatePairs`, `TestPasteRestoresClipboard`(실기기, fixture 창).

---

### Task M1: COM 기반·세션·디스플레이·실기기 테스트 도구

**Files:**
- Create: `internal/agent/win/com/{com_windows.go, guid.go, hresult.go}`, `internal/agent/win/d3d/{device_windows.go, texture_windows.go}`, `internal/agent/display/{display.go, display_windows.go, display_other.go}`, `internal/agent/session/wts_windows.go`(기존 패키지 확장), `scripts/wintest.sh`
- Modify: `internal/agent/server/` — op `session.info`, `display.list` 등록; hello의 `session`·`displays` 채움; caps `screen`·`input` 판정 연결
- Test: `internal/agent/win/com/hresult_test.go`, `internal/agent/display/display_windows_test.go`, `internal/agent/session/wts_windows_test.go`

**Interfaces:**
- Produces:
  - `com.HRESULT`(error 구현, `Error()`에 16진수와 알려진 이름), `com.Call(obj uintptr, index int, args ...uintptr) com.HRESULT`, `com.Release(obj uintptr)`, `com.GUID` + `com.MustGUID(s string)`.
  - `d3d.NewDeviceForOutput(outputName string) (*d3d.Device, error)` — 그 출력을 가진 어댑터에 D3D11 장치(+멀티스레드 보호, VIDEO_SUPPORT|BGRA_SUPPORT 플래그).
  - `display.List() ([]display.Info, error)`; `type Info struct { Index int; Name, Adapter string; X, Y, Width, Height, DPI int; Scale float64; Primary bool; RefreshHz int }`(JSON 키 protocol.md §3.1).
  - `session.Current() (session.Info, error)`; `type Info struct { SessionID uint32; User, SID string; ConnectState string; Protocol string; Locked bool; InputDesktop string; Integrity string }` — `ConnectState ∈ {active, connected, disconnected}`, `Protocol ∈ {console, rdp}`, `InputDesktop ∈ {Default, Winlogon, unknown}`.
  - `scripts/wintest.sh <go 패키지> [go test 인수…]`: 교차 빌드한 테스트 바이너리와 `testdata/`를 `C:\deskhand-test\<이름>\`로 복사 → 대화형 예약 작업 `DeskhandTest`로 사용자 세션에서 실행 → 출력·종료 코드를 가져와 표시 → 예약 작업 삭제. 종료 코드를 그대로 반환.
- [ ] **Step 1: failing tests** — `TestHRESULTFormat`(0x80070005 → "E_ACCESSDENIED (0x80070005)"), `TestDisplayListHasPrimary`(실기기: 최소 1개, 하나만 primary, 크기 > 0), `TestSessionCurrentInUserSession`(실기기 사용자 세션: SessionID ≥ 1, Protocol ∈ {console, rdp}), `TestSessionZeroDetected`(SSH 세션에서 실행 시 SessionID 0 — `go test`를 SSH로 직접 실행).
- [ ] **Step 2:** `go test ./internal/agent/win/com/` FAIL, `scripts/wintest.sh ./internal/agent/display/` FAIL.
- [ ] **Step 3:** 구현(스파이크 com.go·d3d.go를 정리해 옮긴다).
- [ ] **Step 4:** 위 명령 PASS, `scripts/test.sh` PASS.
- [ ] **Step 5:** Commit `feat(agent): COM 기반, 세션·디스플레이 정보, 실기기 테스트 도구`.

### Task M2: 캡처 — Desktop Duplication, GDI 대체, 정지 화면

**Files:**
- Create: `internal/agent/capture/{capture.go, dd_windows.go, gdi_windows.go, still_windows.go, classify_windows.go, capture_other.go}`
- Modify: `internal/agent/server/` — op `screen.capture` 등록
- Test: `internal/agent/capture/{classify_windows_test.go, dd_windows_test.go, still_windows_test.go}`

**Interfaces:**
- Consumes: M1 `com`, `d3d`, `display`, `session`.
- Produces:
  - `type Frame struct { Texture uintptr /* ID3D11Texture2D, BGRA, 자체 소유 */; CPU []byte /* GDI 경로 BGRA */; Width, Height int; MonoUS uint64; Dirty bool; Pointer PointerInfo }`, `type PointerInfo struct { X, Y int; Visible bool; ShapeChanged bool; Shape *wire.CursorShape }`
  - `type Source interface { Next(ctx context.Context, timeout time.Duration) (Frame, error); Device() *d3d.Device; Size() (w, h int); Close() }`
  - `func Open(displayIndex int) (Source, error)` — DD 우선, DD가 `E_ACCESSDENIED`·`DXGI_ERROR_UNSUPPORTED`이면 GDI 시도, 둘 다 실패면 `*capture.Unavailable{Reason string; Detail string}` 오류.
  - `Next`: 새 이미지 없으면 `capture.ErrNoNewFrame`; `DXGI_ERROR_ACCESS_LOST`·모드 변경이면 내부에서 복제 재생성 후 `capture.ErrReset`(호출자가 크기 재확인); 접근 거부면 `*Unavailable`.
  - `func Classify(hr com.HRESULT, s session.Info) *Unavailable` — 고정 사유 문자열(Global Constraints) 결정: Session 0 → `session0`; 입력 데스크톱 Winlogon → `secure_desktop`(잠금이면 `session_locked`); disconnected → `session_disconnected`; Protocol rdp + 접근 거부 → `rdp_output_suppressed`; 그 외 접근 거부 → `access_lost`.
  - `func CaptureStill(displayIndex int, format string, maxWidth, quality int) (StillResult, error)` — `StillResult{ CaptureID string; Data []byte; Width, Height int; Scale float64; OriginX, OriginY, DPI int; MonoUS uint64; Method string }`(`method` = `dd`/`gdi`). DD는 첫 프레임을 얻기까지 최대 500 ms 대기(정지 화면이면 마지막 프레임을 쓰기 위해 복제를 만든 직후 프레임을 받는다).
  - `screen.capture` op 결과는 protocol.md §3.1 필드.
- [ ] **Step 1: failing tests** — `TestAccessDeniedClassified`(표 기반: 위 다섯 조건 → 사유), `TestCaptureStillPNG`(실기기: PNG 디코딩 가능, 크기 = 디스플레이 크기 또는 max_width 축소, 전부 검은색이 아님 — 세션이 캡처 불가 상태면 `*Unavailable` 사유를 출력하고 `t.Skip`), `TestNextNoNewFrameOnStaticScreen`(실기기, 100 ms 타임아웃 → ErrNoNewFrame 또는 프레임).
- [ ] **Step 2–4:** FAIL → 구현 → PASS(`go test` darwin, `scripts/wintest.sh ./internal/agent/capture/`).
- [ ] **Step 5:** Commit `feat(agent): 화면 캡처와 정지 화면 op`.

### Task M3: 인코딩과 `video` 스트림

**Files:**
- Create: `internal/agent/encode/{encode.go, vp_windows.go, mft_hw_windows.go, mft_sw_windows.go, codecapi_windows.go, encode_other.go}`, `internal/agent/screen/{pipeline.go, stream.go, stats.go}`
- Modify: `cmd/deskhand-agent/main.go` — `video` 스트림 처리기 등록
- Test: `internal/agent/encode/encode_windows_test.go`, `internal/agent/screen/{pipeline_test.go, stream_test.go}`

**Interfaces:**
- Consumes: M2 `capture.Source`/`Frame`, `wire.AU`·`wire.WriteFrame`·종류 상수, Task 4 `quicserve.StreamHandlers`.
- Produces:
  - `type Encoder interface { Encode(f capture.Frame, forceKey bool) error; Output() <-chan wire.AU; SetBitrate(bps int) error; Info() EncoderInfo; Close() }`, `type EncoderInfo struct { Name string; Hardware bool; Width, Height int }`
  - `func encode.New(dev *d3d.Device, srcW, srcH, outW, outH, fps, bps int) (Encoder, error)` — 하드웨어 MFT(비동기: ASYNC_UNLOCK → SET_D3D_MANAGER → SetOutputType → SetInputType) 실패 시 소프트웨어 MFT(동기, 출력 버퍼 직접 할당). 색 변환은 VideoProcessor, 소프트웨어 경로는 readback 후 NV12 메모리 버퍼. Annex-B 출력 그대로, IDR에 SPS/PPS 포함(`AU.HasConfig`).
  - `screen.Pipeline`: `Start(ctx, params wire.StreamHeader, out func(kind byte, payload []byte) error) error` — 생산자 고루틴(캡처+변환, LockOSThread)과 MFT 이벤트 루프 분리, `max_fps` 페이싱, `max_width` 축소(종횡비 유지, 짝수), 정지 화면 정책(Global Constraints), 1초마다 `stats`, 사유가 생기면 `paused`, 회복 시 새 `info` + 키프레임.
  - `video` 스트림 처리기: 헤더 수락 시 `info` 먼저, 이후 AU. 역방향 `0x40` 제어(`keyframe`, `bitrate`, `config`, `pause`, `resume`) 처리. 스트림 종료 시 파이프라인 정리.
  - caps `screen` = ready(캡처 가능) / needs_user(사유가 secure_desktop·session_locked·rdp_output_suppressed) / unavailable(그 외).
- [ ] **Step 1: failing tests** — `pipeline_test.go`(가짜 Source·Encoder로 OS 무관): `TestPacingCapsFPS`(소스 240 fps → 출력 ≤ max_fps+1), `TestStaticScreenSendsKeepaliveEvery10s`(가짜 시계), `TestPausedThenResumesWithKeyframe`, `TestReinitOnSizeChange`(크기 변경 → 새 info, 다음 AU Key=true), `TestControlKeyframeAndBitrate`; `encode_windows_test.go`(실기기): `TestHardwareEncoderProducesIDR`(합성 텍스처 60 프레임 → 첫 AU Key·HasConfig, 디코더 없이 NAL 타입 5·7·8 확인), `TestSoftwareFallback`(하드웨어 금지 옵션으로 같은 검사).
- [ ] **Step 2–4:** FAIL → 구현 → PASS.
- [ ] **Step 5:** Commit `feat(agent): H.264 인코딩과 video 스트림`.

### Task M4: 입력·커서·제어권 세대

**Files:**
- Create: `internal/agent/input/{input.go, sendinput_windows.go, text_windows.go, state.go, input_other.go}`, `internal/agent/cursor/{cursor.go, cursor_windows.go, cursor_other.go}`
- Modify: `internal/agent/server/` — op `input.state`, `input.release_all`, `control.set_writer` 등록; UI 계열 op(`ui.act`, `win.action`, `clipboard.set`, `app.launch`)에 writer 세대 검사; `cmd/deskhand-agent/main.go` — `input`, `cursor` 스트림 처리기
- Add: `internal/wire/` — `KindInputAck = 0x42`
- Test: `internal/agent/input/{state_test.go, text_test.go, sendinput_windows_test.go}`, `internal/agent/cursor/cursor_windows_test.go`

**Interfaces:**
- Consumes: `wire.InputEvent`, `wire.InputAck`, `wire.CursorShape/Pos`, M1 `session`, `display`.
- Produces:
  - `input.Injector`: `Apply(ev wire.InputEvent) wire.InputAck` — 세대 검사(`ev.Gen < current` → `CONTROL_PREEMPTED`), 입력 데스크톱·잠금 검사, SendInput 반환값 검사(0이면 `ELEVATION_REQUIRED` 또는 `GetLastError` 사유), 눌림 상태 추적, `ReleaseAll()`.
  - `input.SetWriter(gen uint64)` — 증가만 허용, 올라가면 `ReleaseAll()` 후 `input.preempted` 이벤트.
  - 텍스트: `unicode` — UTF-16 단위로 KEYEVENTF_UNICODE down/up, `\n`→Enter, `\t`→Tab; `paste` — 클립보드 저장 → 설정 → Ctrl+V → 300 ms 후 복원.
  - 좌표: `move{x,y,display}` → 디스플레이 원점 + 픽셀 → 가상 데스크톱 0–65535.
  - 커서: `cursor` 스트림 — 모양(고유 `shape_id`, 바뀔 때만), 위치(이동 시, 최대 120 Hz). 흑백·컬러·마스크 컬러 포인터를 BGRA(비곱셈 알파)로 변환.
  - caps `input` = ready / needs_user(secure_desktop·session_locked) / unavailable(session0).
- [ ] **Step 1: failing tests** — `TestStaleGenerationRejected`, `TestReleaseAllOnStreamClose`(가짜 SendInput으로 down만 보낸 키·버튼이 up으로 해제됨), `TestUnicodeSurrogatePairs`("😀" → 4개 INPUT, "가\n" → 유니코드 2 + Enter down/up), `TestAbsoluteCoordinateScaling`(가상 데스크톱 원점이 음수인 다중 모니터 포함), `TestInputDesktopWinlogonReported`(가짜 세션 정보), 실기기: `TestFixtureReceivesKeysAndText`(deskhand-fixture 실행 → 포커스 → "Hello 한글 😀" unicode 입력 → fixture가 받은 텍스트를 UIA로 읽어 일치), `TestPasteRestoresClipboard`, `TestCursorShapeBGRA`(현재 커서 모양 크기 > 0, BGRA 길이 = w·h·4).
- [ ] **Step 2–4:** FAIL → 구현 → PASS.
- [ ] **Step 5:** Commit `feat(agent): 입력 주입, 커서 스트림, 제어권 세대`.

### Task M5: 실기기 화면 실측

**Files:** Create `scripts/e2e/screen-probe/main.go`(데몬 없이 QUIC로 에이전트에 붙는 측정 도구), `docs/acceptance/screen.md`

- [ ] **Step 1:** 실기기에 새 에이전트를 배포(설치된 `Deskhand Agent` 예약 작업의 실행 파일 교체 후 재시작)하고 `screen-probe`로 `video` 스트림 60초 수신: fps, 비트레이트, AU 크기, 캡처→수신 지연(시계 보정) p50/p95 기록. 결과 스트림을 `.h264`로 저장해 Mac ffmpeg로 디코딩 오류 0 확인.
- [ ] **Step 2:** 화면 변화가 있는 상태(deskhand-fixture의 애니메이션 모드 또는 스크롤)로 같은 측정.
- [ ] **Step 3:** 입력 왕복: `input` 스트림으로 fixture에 클릭·키 → ACK 지연 p50/p95, 입력→화면 변화 지연.
- [ ] **Step 4:** 세션 상태별 결과(콘솔/RDP 표시/RDP 숨김)를 `docs/acceptance/screen.md`에 기록. Commit `test: 화면·입력 실기기 실측`.
