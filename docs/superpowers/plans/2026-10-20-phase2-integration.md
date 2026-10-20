# Deskhand 2단계: 통합 (제어권·화면 중계·AI 관찰·입력) 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 1단계 기반, 미디어 트랙(에이전트 캡처·인코딩·입력·커서), 앱 트랙(화면 탭)을 하나로 묶는다. 데몬이 제어권을 중재하고, 에이전트의 `video`/`cursor`/`input` 스트림을 앱의 `stream.attach{kind:"screen"}`로 중계한다. AI는 MCP로 화면을 보고 입력하고 UI Automation을 쓴다. 사람은 언제든 제어권을 가져온다.

**Architecture:** `internal/daemon/control`(제어권 상태·lease·gen 영속), `internal/daemon/screen`(호스트별 스트림 허브: 에이전트 스트림 1개 → 뷰어 N개, 사람 입력 중재), `internal/daemon/observe`(정지 화면·좌표 환산·관찰·settle), 로컬 API 메서드와 MCP 도구가 그 위에 얹힌다. `conn.Host`에 스트림을 여는 메서드를 더한다.

**Tech Stack:** Go 1.26, quic-go, MCP go-sdk v1.8, Swift 앱(이미 구현된 화면 탭).

**Spec:** `docs/design.md` §5.1·§6·§7, `docs/protocol.md` §2–§6, `docs/local-api.md` §3(제어권·관찰·입력·UI)·§4·§5·§6·§7, 요구사항 `docs/plan/` P0 UI-01/02/03, CON-02/03.

## Global Constraints

- 1단계 Global Constraints 전부(`docs/superpowers/plans/2026-10-20-phase1-foundation.md`).
- `ControlState.gen`은 호스트별로 owner 또는 ai_paused가 바뀔 때마다 1씩 오르며 SQLite에 영속한다(데몬 재시작 후에도 감소하지 않는다). 에이전트 writer generation(`control.set_writer`)은 이 gen과 같은 값이다.
- `control.stop`은 연결이 끊긴 호스트에도 적용되어 저장되고, 재연결 시 에이전트에 `control.set_writer`로 다시 반영된다.
- AI lease TTL 60초, AI 입력·UI 계열 op마다 갱신. 만료되면 owner=none, gen+1.
- 사람 입력의 `gen`은 데몬이 덮어쓴다. AI 입력이 `human` 소유 중에 오면 `CONTROL_HELD_BY_HUMAN`, ai_paused 중이면 `CONTROL_HELD_BY_HUMAN`(next_action `human_resume_ai`).
- 화면 허브: 뷰어가 0이면 에이전트 `video` 스트림을 닫는다(5초 유예). 뷰어 프리셋이 다르면 가장 높은 품질로 열고, 다른 프리셋 요청은 `config`로 반영한다. 새 뷰어가 붙으면 `keyframe`을 요청한다. 데몬은 30초마다 `keyframe`을 요청해 손실 회복을 보장한다.
- 지연: 데몬은 연결마다 `time.sync`(에이전트 op, 미디어 트랙 M5에서 구현됨)를 5회 측정해 최소 RTT 표본으로 시계 오프셋을 정하고, 1초마다 `0x30 {"type":"latency","capture_to_receive_ms","clock_offset_ms"}`를 뷰어에 보낸다.
- AI 정지 화면: `screen.capture`는 에이전트 정지 화면을 받아 산출물(`kind:"screenshot"`)로 저장하고 `capture_id`를 발급한다. 좌표 환산 표는 capture_id별로 10분 보존한다. 다른 connection_generation이나 디스플레이 구성 변경 뒤의 capture_id는 `STALE_TARGET`.
- MCP `screen_capture`는 이미지를 MCP 이미지 콘텐츠(PNG/JPEG base64)로 돌려준다(최대 폭 1568).
- 입력 결과 봉투의 `action.executed`는 에이전트 ack(`0x42` ok)가 왔을 때만 true. ack 전에 연결이 끊기면 `unknown`.

## Review Focus

1. **사람이 제어권을 가져오는 순간 AI 입력이 남지 않음** — 진행 중이던 AI `input.type`(긴 문자열)이 즉시 끊기고, 이후 AI 요청은 거절, 에이전트의 눌린 키는 해제. → Task P2 `TestHumanTakeoverCutsInFlightAIInput`.
2. **뷰어 둘(앱 창 두 개)과 MCP 동시 사용** — 하나의 에이전트 스트림을 공유하고, 한 뷰어가 닫혀도 다른 뷰어는 끊기지 않는다. → Task P3 `TestTwoViewersShareOneAgentStream`.
3. **연결 세대가 바뀐 뒤의 오래된 capture_id 클릭** — `STALE_TARGET`으로 거절하고 다시 캡처하라는 next_action. → Task P4 `TestClickWithStaleCaptureRejected`.
4. **데몬 재시작 뒤 gen 감소 금지와 긴급 정지 유지** — 재시작 전 ai_paused=true였으면 재시작 후에도 AI 입력이 막힌다. → Task P2 `TestStopSurvivesDaemonRestart`.
5. **에이전트가 `screen` 사용 불가(secure_desktop 등)일 때 뷰어** — 허브가 `0x04 paused`를 그대로 전달하고 재개 시 키프레임부터 보낸다. → Task P3 `TestPausedForwardedAndResumeStartsWithKeyframe`.

---

### Task P1: 트랙 병합과 통합 빌드

**Files:** 병합 커밋, `scripts/build.sh`, `app/scripts/build-app.sh`, `docs/acceptance/integration.md`
- [ ] **Step 1:** `main`에 `feat/phase1-foundation`을 병합(fast-forward 아님, 병합 커밋), 이어 `feat/media-track`(M4·M5 완료 후), `feat/app-track`(A5 완료 후)을 병합한다. 충돌은 문서 체리픽 중복과 `cmd/deskhand-agent/main.go`(스트림 처리기 등록 + 두 빌드)에서 예상된다.
- [ ] **Step 2:** `scripts/build.sh`가 Go 바이너리 전부(`deskhand`, `deskhand-agent`, `deskhand-agentw` ×2 아키텍처, fixture)를 만들고, `app/scripts/build-app.sh`가 `deskhand`를 `Contents/MacOS/`, 에이전트 4개를 `Contents/Resources/agents/`에 넣는다(코드 서명 검증 통과).
- [ ] **Step 3:** `scripts/test.sh`와 `swift test` 전체 통과, 앱 빌드 경고 0.
- [ ] **Step 4:** Commit(병합 커밋 메시지에 세 트랙 요약).

### Task P2: 제어권 중재 (`internal/daemon/control`)

**Interfaces:**
- Produces: `control.Manager` — `Get(hostID) ControlState`, `Acquire(ctx, hostID, as Owner, client ClientInfo, force bool) (ControlState, error)`, `Release`, `Stop`, `ResumeAI`, `CheckAI(hostID, client) (gen uint64, err error)`(AI 입력 전 호출, lease 갱신), `HumanGen(hostID) uint64`; 상태 변경 시 `control.changed` 이벤트, 타임라인 항목, 연결된 에이전트에 `control.set_writer{gen}`(연결 시에도 재반영). SQLite `control(host_id PK, owner, owner_client, gen, ai_paused, lease_until)`.
- 로컬 API `control.get/acquire/release/stop/resume_ai`(local-api §3), 결과 ControlState.
- [ ] **Tests:** `TestGenIncrementsOnEveryChange`, `TestAILeaseExpires`, `TestHumanTakeoverCutsInFlightAIInput`(가짜 에이전트 input 스트림에서 진행 중 텍스트 입력이 끊기고 release_all 수신), `TestStopSurvivesDaemonRestart`, `TestStopAppliesOfflineAndReplaysOnConnect`, `TestSecondAIClientRejectedWhileFirstHoldsLease`.
- [ ] Commit `feat(daemon): 제어권 중재와 writer generation`.

### Task P3: 화면 허브와 사람 입력 (`internal/daemon/screen`)

**Interfaces:**
- Consumes: `conn.Host.OpenStream(ctx, wire.StreamHeader) (*quic.Stream, error)`(이 태스크에서 추가), 에이전트 `video`/`cursor`/`input` 스트림(protocol §4–§6), 에이전트 `time.sync` op(`{"mono_us"}`, 미디어 트랙에서 구현됨).
- Produces: `screen.Hub` — `Attach(ctx, hostID, display, preset, viewer)`; 뷰어에 `0x01–0x04`, `0x10/0x11`, `0x30` 전달; 뷰어 `0x40`(keyframe, preset) 처리; `0x41` 사람 입력 → control 확인(owner ai면 버리고 `control_denied`, none이면 human으로 자동 획득) → gen 덮어쓰기 → 에이전트 input 스트림 → ack 무시(사람 입력은 화면 피드백이 결과). 뷰어 분리 시 release_all.
- 로컬 API `stream.attach{kind:"screen", host_id, display, preset}`(local-api §4).
- [ ] **Tests:** `TestTwoViewersShareOneAgentStream`, `TestNewViewerGetsKeyframe`, `TestPausedForwardedAndResumeStartsWithKeyframe`, `TestHumanInputBlockedWhileAIOwns`, `TestHumanInputAutoAcquires`, `TestViewerDetachReleasesKeys`, `TestLatencyNoticeUsesClockOffset`, `TestIdleAgentStreamClosedAfterGrace`.
- [ ] Commit `feat(daemon): 화면 허브와 사람 입력 중계`.

### Task P4: AI 관찰·입력·UI (`internal/daemon/observe` + 로컬 API)

**Interfaces:**
- 로컬 API: `screen.capture`, `input.click/move/drag/scroll/key/type`, `observe`, `ui.tree/find/text/rect/act/focused/at`, `win.list/action` (local-api §3). 좌표는 capture 좌표 → 원본 픽셀(capture의 scale·origin). `input.key`의 `keys`는 `ctrl+shift+esc` 형식을 스캔코드 시퀀스로 바꾼다. `input.type`의 `auto`는 짧은 ASCII·한글은 `unicode`, 2000자 초과나 유니코드 실패 시 `paste`.
- `observe{level, settle_ms}`: settle은 에이전트 `video` 없이도 `screen.capture` 두 장 비교 또는 UIA 창 이벤트로 판정(최대 settle_ms).
- [ ] **Tests:** `TestClickMapsCaptureToDisplayPixels`(scale 0.408, origin (3840,0)), `TestClickWithStaleCaptureRejected`, `TestKeysParsing`, `TestTypeAutoChoosesMethod`, `TestInputUnknownWhenAckLost`, `TestUIActGatedByControl`.
- [ ] Commit `feat(daemon): AI 관찰·입력·UI 자동화 API`.

### Task P5: MCP 화면·입력·UI·제어권 도구

**Interfaces:** 도구 `control_status`, `control_request`, `control_release`, `screen_capture`(이미지 콘텐츠 + 메타데이터), `observe`, `input_click`, `input_move`, `input_drag`, `input_scroll`, `input_key`, `input_type`, `ui_tree`, `ui_find`, `ui_text`, `ui_act`, `ui_at`, `win_list`, `win_action`. 서버 instructions 갱신(관찰→행동→재관찰 루프, capture_id 좌표, 사람 제어권 존중, unknown 처리).
- [ ] **Tests:** 인메모리 MCP로 각 도구의 매핑, `screen_capture`가 ImageContent를 돌려줌, `CONTROL_HELD_BY_HUMAN`가 IsError + next_action.
- [ ] Commit `feat(mcp): 화면·입력·UI·제어권 도구`.

### Task P6: 통합 실기기 E2E

- [ ] **Step 1:** 통합 빌드 앱(`build/Deskhand.app` 또는 `app/build/Deskhand.app`)을 실행하고 실제 PC 화면이 뜨는지, fps·지연 수치를 기록한다(사용자 화면을 조작하지 않는다 — 보기만).
- [ ] **Step 2:** MCP 클라이언트(go-sdk 프로브)로 AI가 `deskhand-fixture`를 실행하고 `screen_capture` → 버튼 `input_click` → `ui_text`로 결과 확인. fixture 창에만 입력.
- [ ] **Step 3:** AI가 `input_type`으로 긴 텍스트를 입력하는 중 데몬 API로 `control.acquire{as:"human", force:true}` → AI 입력 즉시 중단, 다음 AI 호출 `CONTROL_HELD_BY_HUMAN`, 키 눌림 없음(A06).
- [ ] **Step 4:** 한글·이모지·줄바꿈 입력 후 fixture 저장 파일 내용 일치(A09).
- [ ] **Step 5:** 인수 결과를 `docs/acceptance/integration.md`에 기록. Commit.
