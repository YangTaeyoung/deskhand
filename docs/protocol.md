# 에이전트 ↔ 데몬 프로토콜 v1

코드: `internal/wire`(타입·프레이밍), `internal/transport`(QUIC·TLS). 이 문서와 코드가 다르면 코드를 고치거나 이 문서를 같은 변경에서 고친다.

## 1. 연결

- QUIC(quic-go), UDP 47600(기본), ALPN `deskhand/1`, TLS 1.3.
- 양쪽 모두 자체 서명 Ed25519 인증서를 쓴다. 상대 인증서의 SHA-256 지문(DER 기준, 소문자 hex)이 페어링 때 저장한 값과 같을 때만 연결을 유지한다. 일반 CA 검증은 쓰지 않는다.
- 데몬이 연결하고(클라이언트), 에이전트가 듣는다(서버).
- keep-alive 5초, idle timeout 20초. 데몬은 연결이 끊기면 지수 backoff(0.5초에서 시작, 최대 10초, ±20% jitter)로 재연결하고 `connection_generation`을 1 올린다.

## 2. 스트림

데몬이 연결 직후 여는 **첫 양방향 스트림이 `rpc` 스트림**이다. 그 밖의 스트림은 연 쪽이 첫 줄에 JSON 헤더 한 줄(`\n` 종료, 최대 64 KiB)을 쓴다.

```
{"s":"video", ...}\n   이후 바이너리 프레임
```

| `s` | 연 쪽 | 헤더 필드 | 본문 |
|---|---|---|---|
| `video` | 데몬 | `display`(인덱스, 기본 0), `max_fps`(기본 60), `bitrate`(bps), `max_width`(0=원본), `codec`(`h264`) | 프레임(§4) |
| `cursor` | 데몬 | `display` | 프레임(§5) |
| `input` | 데몬 | `gen`(writer generation) | 프레임(§6) |
| `pty` | 데몬 | `job`(재부착) 또는 `new`: {`shell`, `cwd`, `cols`, `rows`, `env`} | 프레임(§7) |
| `tunnel` | 데몬 | `port`(1–65535, 루프백만) | 원시 바이트 |
| `put` | 데몬 | `transfer_id`, `path`(최종 경로), `offset`, `size`(전체), `mtime` | 원시 바이트 후 FIN, 에이전트가 결과 JSON 한 줄 응답 |
| `get` | 데몬 | `path`, `offset`, `length`(0=끝까지) | 에이전트가 원시 바이트를 쓰고 FIN |

에이전트가 헤더를 거절하면 `{"ok":false,"code":"…","error":"…"}` 한 줄을 쓰고 스트림을 닫는다. 수락하면 `{"ok":true, …}` 한 줄을 먼저 쓴다(`video`는 `info` 프레임으로 대신한다).

## 3. `rpc` 스트림

Alcove 에이전트 프로토콜 2층과 같은 NDJSON(`t`로 구분, 한 줄 최대 8 MiB).

```
데몬 → 에이전트  {"t":"req","id":7,"op":"exec.run","rid":"r-01J…","argv":["hostname"]}
에이전트 → 데몬  {"t":"res","id":7,"ok":true,"code":0,"stdout":"…"}
에이전트 → 데몬  {"t":"res","id":8,"ok":false,"code":"STALE_TARGET","error":"element e12 no longer exists; call ui.tree again","retryable":false}
에이전트 → 데몬  {"t":"ev","name":"session.changed","locked":true}
```

- 연결 직후 에이전트가 `{"t":"hello","v":1,"agent":"<버전>","os":"windows","arch":"amd64","host":"<컴퓨터 이름>","caps":{…},"session":{…},"displays":[…],"boot_id":"…"}`를 보낸다. 데몬은 `{"t":"welcome","v":1,"controller":"<이름>"}`으로 답한다.
- `rid`(요청 ID): 비멱등 op에는 필수. 에이전트는 `rid` → 결과를 24시간 보존하고(job 저널과 함께 디스크에 영속), 같은 `rid`가 다시 오면 실행하지 않고 저장한 결과를 `"replayed":true`와 함께 돌려준다. 실행 중이면 `{"ok":false,"code":"IN_PROGRESS"}`.
- 오류: `ok:false`, `code`(대문자 상수), `error`(다음 행동을 알려 주는 문장), `retryable`.
- `caps`: 기능별 `{"state":"ready|unavailable|unsupported|needs_user","reason":"…"}`. 키: `screen`, `input`, `uia`, `exec`, `files`, `processes`, `pty`, `tunnel`, `diag`, `clipboard`.

### 3.1 op 목록

`proc.list`의 각 항목에는 Alcove 필드(`pid`, `ppid`, `name`, `path`, `cmd`, `mem_kb`)에 더해 `cpu_pct`(0.5초 간격 두 표본, 논리 코어 합 기준 0–100), `user?`, `started`(RFC 3339), `started_by_deskhand`(이 에이전트가 `exec.*`·`app.launch`·`pty`로 띄운 프로세스와 그 자손이면 true), `self`(에이전트 자신의 프로세스면 true)가 있다. `proc.kill`에 `pid`와 함께 `started`(그 프로세스의 `proc.list` 값)를 주면, 지금 그 PID의 시작 시각이 다를 때 종료하지 않고 `STALE_TARGET`으로 답한다. 에이전트 자신(`self`)은 종료하지 않는다(`PERMISSION_DENIED`).

Alcove에서 이식(필드·의미 동일, `rid` 추가): `ping`, `exec.run`, `exec.start`, `exec.read`, `exec.write`, `exec.kill`, `exec.list`, `proc.list`, `proc.kill`, `fs.stat`, `fs.list`, `fs.read`, `fs.tail`, `fs.write`, `fs.rename`, `fs.mkdir`, `fs.delete`, `app.launch`, `win.list`, `win.action`, `ui.tree`, `ui.find`, `ui.text`, `ui.rect`, `ui.act`, `ui.focused`, `ui.at`, `ui.ping`, `clipboard.get`, `clipboard.set`.

새 op:

| op | 파라미터 | 결과 |
|---|---|---|
| `req.status` | `rid` | `state`(`done`/`running`/`unknown`), 완료 시 원래 결과 |
| `time.sync` | — | `mono_us`(에이전트 단조 시계 µs) |
| `session.info` | — | `session_id`, `user`, `sid`, `connect_state`(`active`/`connected`/`disconnected`), `protocol`(`console`/`rdp`), `locked`, `input_desktop`(`Default`/`Winlogon`/`unknown`), `integrity` |
| `display.list` | — | `displays`: [{`index`, `name`, `adapter`, `x`, `y`, `width`, `height`, `dpi`, `scale`, `primary`, `refresh_hz`}] |
| `screen.capture` | `display`, `format`(`png`/`jpeg`), `max_width`, `quality`, `window`(선택: 창 영역만) | `capture_id`, `data`(base64), `width`, `height`, `scale`(원본 대비), `origin`({x,y} 가상 데스크톱 좌표), `dpi`, `mono_us`, `method`(`dd`/`gdi`) |
| `input.state` | — | `gen`, `pressed`(눌린 키·버튼) |
| `input.release_all` | — | `released` |
| `control.set_writer` | `gen` | `gen` — 이 값보다 작은 세대의 UI 계열 요청은 `CONTROL_PREEMPTED` |
| `fs.copy` | `from`, `to`, `on_conflict`, `recursive` | `path`, `copied` |
| `fs.trash` | `path` | `trashed` |
| `fs.hash` | `path`, `offset`, `length` | `sha256` |
| `xfer.prepare` | `transfer_id`, `path`, `size`, `mtime`, `sha256`(전체, push일 때), `on_conflict`(`fail`/`replace`/`skip`/`rename`) | `part_size`(이미 받은 바이트), `part_sha256`, `free_bytes`, `dest_exists` |
| `xfer.commit` | `transfer_id`, `path`, `sha256`, `on_conflict` | `path`(실제 반영 경로), `outcome`(`written`/`skipped`/`renamed`) |
| `xfer.abort` | `transfer_id`, `path` | `removed` — 임시 파일만 지운다 |
| `pty.list` | — | `ptys`: [{`job`, `pid`, `cols`, `rows`, `state`}] |
| `debug.ports` | `count` | `ports` — 루프백 빈 포트 |
| `diag.system` | — | `os`{`name`, `version`, `build`, `edition`}, `computer_name`, `cpu`{`name`, `cores`, `threads`, `usage_pct`}, `memory`{`total_bytes`, `used_bytes`}, `disks`: [{`root`, `label?`, `fs`, `total_bytes`, `free_bytes`}], `gpus`: [{`name`, `driver_version?`, `memory_bytes?`}], `boot_time`(RFC 3339), `uptime_s` |
| `diag.services` | `name?`(부분 일치, 대소문자 무시) | `services`: [{`name`, `display_name`, `state`(`running`/`stopped`/`paused`/`start_pending`/`stop_pending`/`unknown`), `start_type`(`auto`/`delayed_auto`/`manual`/`disabled`/`unknown`), `pid?`}] |
| `diag.events` | `log`(`Application`/`System`, 기본 `System`), `since?`(RFC 3339, 기본 24시간 전), `until?`, `levels?`([`critical`/`error`/`warning`/`information`/`verbose`], 기본 전부), `provider?`(정확히 일치), `max?`(기본 200, 최대 1000) | `events`: [{`record_id`, `time`, `level`, `provider`, `event_id`, `message`}](최신 먼저), `truncated`(`max`에서 잘렸으면 true) |
| `diag.crashes` | `since?`(기본 30일 전) | `dumps`: [{`path`, `app`, `time`, `size_bytes`, `kind`(`minidump`/`full`/`report`)}](최신 먼저). 파일은 `get` 스트림으로 받는다 |
| `agent.update` | `path`(이미 put한 새 실행 파일), `sha256` | 자기 교체 후 재시작 예약 |

이벤트(`ev`): Alcove의 `job.exited`, `process.exited`, 창 이벤트, `events.dropped`에 더해 `session.changed`, `display.changed`, `caps.changed`, `input.preempted`.

## 4. `video` 프레임

모든 바이너리 프레임: `u32 BE 길이 L` + `L바이트`. 본문 첫 바이트가 종류.

에이전트 → 데몬

| 종류 | 형식 |
|---|---|
| `0x01` AU | `u8 flags`(bit0 키프레임, bit1 SPS/PPS 포함), `u32 width`, `u32 height`, `u64 seq`, `u64 capture_mono_us`, `u64 encode_done_mono_us`, 이후 Annex-B 바이트 |
| `0x02` info | JSON: `codec`, `width`, `height`, `src_width`, `src_height`, `fps`, `bitrate`, `encoder`(MFT 이름), `hardware`(bool), `capture`(`dd`/`gdi`), `display` |
| `0x03` stats | JSON: `fps`, `bitrate`, `dropped`, `encode_ms_p50`, `encode_ms_p95`, `capture_ms_p50` (1초마다) |
| `0x04` paused | JSON: `reason` — `no_viewer` 또는 캡처 사유 고정 문자열 `session0`/`rdp_output_suppressed`/`secure_desktop`/`session_locked`/`session_disconnected`/`access_lost`/`no_output`/`no_encoder` |

데몬 → 에이전트(같은 스트림 반대 방향): `u32 길이` + JSON — `{"op":"keyframe"}`, `{"op":"bitrate","bps":…}`, `{"op":"config","max_fps":…,"max_width":…}`, `{"op":"pause"}`, `{"op":"resume"}`.

## 5. `cursor` 프레임

| 종류 | 형식 |
|---|---|
| `0x10` shape | `u32 shape_id`, `u16 w`, `u16 h`, `u16 hot_x`, `u16 hot_y`, 이후 BGRA(비곱셈 알파) w·h·4바이트 |
| `0x11` pos | `i32 x`, `i32 y`(캡처 디스플레이 원본 픽셀 좌표), `u8 visible`, `u32 shape_id` |

## 6. `input` 프레임

데몬 → 에이전트: `u32 길이` + JSON 이벤트. 공통 필드 `seq`(증가), `gen`.

| `k` | 필드 |
|---|---|
| `move` | `x`, `y`(디스플레이 원본 픽셀), `display` |
| `button` | `b`(`left`/`right`/`middle`/`x1`/`x2`), `down`(bool) |
| `wheel` | `dx`, `dy`(WHEEL_DELTA 단위 정수, 정밀 스크롤은 120 미만 값 허용) |
| `key` | `sc`(스캔코드), `ext`(확장키 bool) 또는 `vk`; `down`(bool) |
| `text` | `text`(유니코드), `method`(`unicode`/`paste`) |
| `release_all` | — |

에이전트 → 데몬: `u32 길이` + JSON `{"seq":n,"ok":true,"done_mono_us":…}` 또는 `{"seq":n,"ok":false,"code":"CONTROL_PREEMPTED"|"ELEVATION_REQUIRED"|"SESSION_LOCKED"|"DISPLAY_UNAVAILABLE","error":"…"}`. 입력 스트림이 닫히면 에이전트는 눌린 키·버튼을 모두 해제한다.

## 7. `pty` 프레임

| 종류 | 방향 | 형식 |
|---|---|---|
| `0x20` data | 양방향 | 원시 바이트(UTF-8) |
| `0x21` resize | 데몬→에이전트 | JSON `cols`, `rows` |
| `0x22` exit | 에이전트→데몬 | JSON `code` |
| `0x23` attached | 에이전트→데몬 | JSON `job`, `pid`, 이후 스크롤백(최대 1 MiB)이 data 프레임으로 이어진다 |

스트림이 끊겨도 ConPTY 프로세스는 남고 `job`으로 재부착한다. `exec.kill`로 끝낸다.

## 8. 오류 코드

`OFFLINE`, `AUTH_REQUIRED`, `SESSION_LOCKED`, `SESSION_CHANGED`, `CONTROL_HELD_BY_HUMAN`, `CONTROL_PREEMPTED`, `STALE_TARGET`, `DISPLAY_UNAVAILABLE`, `ELEVATION_REQUIRED`, `CAPABILITY_UNSUPPORTED`, `PORT_CONFLICT`, `TRANSFER_CONFLICT`, `HASH_MISMATCH`, `DISK_FULL`, `NOT_FOUND`, `PERMISSION_DENIED`, `INVALID_ARGUMENT`, `IN_PROGRESS`, `EXECUTION_OUTCOME_UNKNOWN`, `TIMEOUT`, `EXITED_EARLY`, `INTERNAL`.
