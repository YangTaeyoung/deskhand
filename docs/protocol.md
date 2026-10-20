# 에이전트 ↔ 데몬 프로토콜 v1

코드: `internal/wire`(타입·프레이밍), `internal/transport`(QUIC·TLS). 이 문서와 코드가 다르면 코드를 고치거나 이 문서를 같은 변경에서 고친다.

## 1. 연결

- QUIC(quic-go), UDP 47600(기본), ALPN `deskhand/1`, TLS 1.3.
- 양쪽 모두 자체 서명 Ed25519 인증서를 쓴다. 상대 인증서의 SHA-256 지문(DER 기준, 소문자 hex)이 페어링 때 저장한 값과 같을 때만 연결을 유지한다. 일반 CA 검증은 쓰지 않는다.
- 데몬이 연결하고(클라이언트), 에이전트가 듣는다(서버).
- keep-alive 5초, idle timeout 20초. 데몬은 연결이 끊기면 지수 backoff(0.5초에서 시작, 최대 10초, ±20% jitter)로 재연결하고 `connection_generation`을 1 올린다.

## 2. 스트림

모든 스트림은 `rpc`를 포함해 연 쪽이 첫 줄에 JSON 헤더 한 줄(`\n` 종료, 최대 64 KiB)을 쓴다. QUIC은 데이터가 오기 전에는 상대에게 새 스트림을 알리지 않으므로, 헤더가 스트림의 첫 데이터다.

```
{"s":"rpc"}\n          이후 NDJSON(§3)
{"s":"video", ...}\n   이후 바이너리 프레임
```

데몬이 연결 직후 여는 **첫 양방향 스트림이 `rpc` 스트림**이고 헤더는 `{"s":"rpc"}`다. 에이전트는 첫 스트림의 `s`가 `rpc`가 아니면 거절 줄(`INVALID_ARGUMENT`)을 쓰고 연결을 닫는다. 두 번째 `rpc` 스트림도 `INVALID_ARGUMENT`로 거절한다. `rpc` 스트림이 끝나면 에이전트는 연결을 닫는다.

| `s` | 연 쪽 | 헤더 필드 | 본문 |
|---|---|---|---|
| `rpc` | 데몬 | — | NDJSON(§3) |
| `video` | 데몬 | `display`(인덱스, 기본 0), `max_fps`(1–240, 0·생략은 60), `bitrate`(bps, 100000–200000000, 0·생략은 출력 폭 1920 이하 6 Mbps·2560 이하 12 Mbps·그 위 20 Mbps), `max_width`(64–16384, 0·생략은 원본), `codec`(`h264`, 생략 가능) | 프레임(§4) |
| `cursor` | 데몬 | `display` | 프레임(§5) |
| `input` | 데몬 | `gen`(writer generation) | 프레임(§6) |
| `pty` | 데몬 | `job`(재부착) 또는 `new`: {`shell`, `cwd`, `cols`, `rows`, `env`} | 프레임(§7) |
| `tunnel` | 데몬 | `port`(1–65535, 루프백만) | 원시 바이트 |
| `put` | 데몬 | `transfer_id`, `path`(최종 경로), `offset`, `size`(전체), `mtime` | 원시 바이트 후 FIN, 에이전트가 결과 JSON 한 줄 응답 |
| `get` | 데몬 | `path`, `offset`, `length`(0=끝까지) | 에이전트가 원시 바이트를 쓰고 FIN |

에이전트가 헤더를 거절하면 `{"ok":false,"code":"…","error":"…"}` 한 줄을 쓰고 스트림을 닫는다. 처리기가 없는 `s`는 `CAPABILITY_UNSUPPORTED`다. 수락하면 `{"ok":true, …}` 한 줄을 먼저 쓴다(`video`는 `info` 프레임으로 대신한다). `rpc` 스트림은 수락 줄 없이 바로 `hello`로 시작한다.

## 3. `rpc` 스트림

Alcove 에이전트 프로토콜 2층과 같은 NDJSON(`t`로 구분). 한 줄 최대 길이는 데몬→에이전트 1 MiB, 에이전트→데몬 8 MiB.

```
데몬 → 에이전트  {"t":"req","id":7,"op":"exec.run","rid":"r-01J…","argv":["hostname"]}
에이전트 → 데몬  {"t":"res","id":7,"ok":true,"code":0,"stdout":"…"}
에이전트 → 데몬  {"t":"res","id":8,"ok":false,"code":"STALE_TARGET","error":"element e12 no longer exists; call ui.tree again","retryable":false}
에이전트 → 데몬  {"t":"ev","name":"session.changed","locked":true}
```

- 데몬은 헤더 `{"s":"rpc"}` 말고는 아무것도 쓰지 않고 기다린다. 헤더를 받으면 에이전트가 `{"t":"hello","v":1,"agent":"<버전>","os":"windows","arch":"amd64","host":"<컴퓨터 이름>","caps":{…},"session":{…},"displays":[…],"boot_id":"…"}`를 보낸다. 데몬은 `{"t":"welcome","v":1,"controller":"<이름>"}`으로 답하고 그 뒤에 요청을 보낸다.
- 에이전트는 welcome 전에는 요청을 처리하지 않고 이벤트도 보내지 않는다. 첫 메시지가 welcome이 아니거나 10초 안에 오지 않으면 스트림을 닫는다.
- hello·welcome 뒤에는 `req`(데몬→에이전트)와 `res`·`ev`(에이전트→데몬)만 흐른다. 에이전트는 그 밖의 `t`를 받으면 실행하지 않고 `{"t":"res","id":<그 메시지의 id 또는 null>,"ok":false,"code":"INVALID_ARGUMENT","error":"…","retryable":false}` 한 줄로 답한다. 두 번째 welcome은 무시한다.
- `session`: Windows는 `session.info` 결과와 같은 필드(§3.1), 그 밖의 OS는 데스크톱 세션의 `session_id`·`user` 중 읽은 것. `displays`는 `display.list`의 `displays`이고, 읽을 수 없으면(Session 0 등) `[]`. `boot_id`는 에이전트 프로세스가 시작할 때마다 바뀐다.
- `rid`(요청 ID): 데몬은 비멱등 op에 항상 붙인다. 기록 대상 op는 `exec.run`, `exec.start`, `exec.write`, `exec.kill`, `proc.kill`, `fs.write`, `fs.rename`, `fs.mkdir`, `fs.delete`, `app.launch`, `clipboard.set`, `win.action`, `ui.act`이고, `rid`가 없는 요청은 기록 없이 실행한다. 형식은 영문자·숫자·`-`·`_` 1–128자의 문자열(Windows 장치 이름 제외)이다. 요청에 `rid`가 있으면 op와 상관없이 형식을 검사하고, 어기면(문자열이 아님, 빈 문자열, 허용하지 않는 문자) 실행하지 않고 `INVALID_ARGUMENT`로 답한다.
- 에이전트는 실행 전에 `DataDir/journal/<rid>.json`에 실행 중 기록을 쓰고, 끝나면 응답(`t`, `id` 제외)으로 바꾼다. 끝난 기록은 24시간 보존한다. 같은 `rid`가 다시 오면 실행하지 않고 저장한 응답을 `"replayed":true`와 함께 돌려준다(저장한 응답이 실패면 그 실패 그대로). 실행 중이면 `{"ok":false,"code":"IN_PROGRESS","retryable":true}`. 다른 op에 쓴 `rid`면 `INVALID_ARGUMENT`.
- 에이전트가 실행 중에 멈췄던 `rid`는 다시 실행하지 않고 `{"ok":false,"code":"EXECUTION_OUTCOME_UNKNOWN"}`를 저장한 응답으로 삼는다.
- `req.status{rid}` 응답: 기록이 없으면 `{"ok":true,"state":"unknown"}`, 실행 중이면 `{"ok":true,"state":"running"}`, 끝났으면 저장한 응답의 필드(`ok`·`code`·`error` 또는 결과 필드)에 `"state":"done"`을 더한 것.
- 오류: `ok:false`, `code`(대문자 상수), `error`(다음 행동을 알려 주는 문장), `retryable`. Alcove에서 가져온 op의 오류에는 아직 `code`가 없는 것이 있다.
- `caps`: 기능별 `{"state":"ready|unavailable|unsupported|needs_user","reason":"…"}`(`ready`면 `reason` 없음). 키: `screen`, `input`, `uia`, `exec`, `files`, `processes`, `pty`, `tunnel`, `diag`, `clipboard`. 바뀌면 에이전트가 `{"t":"ev","name":"caps.changed","caps":{…전체…}}`를 보낸다(welcome 직후 hello와 다를 때 포함).
- exec job: `exec.start` job은 메타와 stdout·stderr 끝 1 MiB를 `DataDir/jobs/<job>/`에 기록한다. 에이전트가 다시 시작하면 그 job이 `exec.list`·`exec.read`에 `state:"exited"`(종료 코드 기록 시 `code` 포함) 또는 `state:"lost"`(실행 중에 에이전트가 멈춤, `code` 없음)로 남는다.

### 3.1 op 목록

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
| `diag.system` | — | OS, 빌드, CPU, 메모리, 디스크, GPU, 가동 시간 |
| `diag.services` | `name` | `services` |
| `diag.events` | `log`(`Application`/`System`), `since`, `level`, `provider`, `max` | `events` |
| `diag.crashes` | `since` | `dumps`(WER 덤프 목록) |
| `agent.update` | `path`(이미 put한 새 실행 파일), `sha256` | 자기 교체 후 재시작 예약 |

- `session.info`: `user`(`DOMAIN\이름`)·`sid`·`integrity`(`untrusted`/`low`/`medium`/`high`/`system`/`protected`)는 에이전트 프로세스 토큰 기준이다. `connect_state`는 WTS 상태를 접는다(Active·Shadow → `active`, Connected·ConnectQuery·Init → `connected`, 나머지 → `disconnected`). `protocol`은 세션의 클라이언트 프로토콜이 콘솔이 아니면 `rdp`. `input_desktop`은 에이전트가 입력 데스크톱을 열 권한이 없으면 `Winlogon`, 입력 데스크톱이 없는 세션(Session 0)이면 `unknown`. Linux 에이전트는 Alcove의 데스크톱 세션 필드(`session`, `user`, `uid`, `home`, `type`, `locked`, exec 허용 시)를 돌려주고, 그 밖의 OS는 `CAPABILITY_UNSUPPORTED`.
- `display.list`: 크기·위치는 물리 픽셀(에이전트는 Per-Monitor DPI aware v2), `scale`은 `dpi`/96, `index`는 주 디스플레이가 0이고 나머지는 왼쪽→오른쪽, 위→아래 순. Session 0에서는 `DISPLAY_UNAVAILABLE`, Windows가 아닌 에이전트는 `CAPABILITY_UNSUPPORTED`.
- `screen.capture`: 기본값은 `display` 0, `format` `png`, `max_width` 1568(0이면 디스플레이 폭 그대로, 더 좁을 때만 면적 평균으로 축소, 높이는 종횡비 유지·반올림), `quality` 80(`jpeg`에만 적용, 1–100). `capture_id`는 `c-` + 16진수 12자리, `scale`은 결과 폭 ÷ 디스플레이 폭, `origin`은 디스플레이의 가상 데스크톱 좌표, `mono_us`는 이미지를 얻은 시각(`time.sync`와 같은 시계). Desktop Duplication이 500 ms 안에 이미지를 주지 못하거나 거부되면 GDI로 찍고 `method`가 `gdi`. `window`는 아직 지원하지 않는다(`CAPABILITY_UNSUPPORTED`, 디스플레이를 찍고 `win.list`의 창 영역으로 자른다). 오류: 캡처할 수 없으면 `DISPLAY_UNAVAILABLE`(PC가 잠겼으면 `SESSION_LOCKED`)이고 `error`에 고정 사유(`session0`, `rdp_output_suppressed`, `secure_desktop`, `session_locked`, `session_disconnected`, `access_lost`, `no_output`)를 괄호로 넣는다(`access_lost`만 `retryable`). 없는 디스플레이는 `NOT_FOUND`, 이미지가 응답 한 줄(8 MiB)에 넘치면 `INVALID_ARGUMENT`(더 작은 `max_width`나 `jpeg`로 다시 요청).
- caps `screen`: 주 디스플레이의 1픽셀을 GDI로 복사할 수 있고 H.264 인코더를 만들 수 있으면 `ready`(캡처 소스가 열려 있으면 그 소스의 상태), 사유가 `secure_desktop`·`session_locked`·`rdp_output_suppressed`면 `needs_user`, 그 밖의 사유면 `unavailable`. `reason`은 위 고정 사유이고, 인코더를 만들 수 없으면(하드웨어·소프트웨어 H.264 인코더 MFT 모두 실패) `unavailable`·`no_encoder`. 캡처 판정은 5초 동안 재사용하고, 인코더 판정은 에이전트가 시작할 때 첫 디스플레이의 장치에 640×360 인코더를 한 번 만들어 보고(디스플레이가 없는 세션에서는 H.264 인코더 MFT가 등록돼 있는지만 본다) 결과를 재사용한다(성공은 프로세스 끝까지, 실패는 1분).

이벤트(`ev`): Alcove의 `job.exited`, `process.exited`, 창 이벤트, `events.dropped`에 더해 `clipboard.changed`(`text`: 에이전트가 쓰지 않은 PC 클립보드 변경, 700000바이트까지), `session.changed`, `display.changed`, `caps.changed`, `input.preempted`.

## 4. `video` 프레임

모든 바이너리 스트림(`video`, `cursor`, `input`, `pty`)의 모든 프레임은 양방향 모두 `u32 BE 길이 L` + `L바이트`이고, 본문 첫 바이트가 종류다(`internal/wire.WriteFrame`).

에이전트 → 데몬

| 종류 | 형식 |
|---|---|
| `0x01` AU | `u8 flags`(bit0 키프레임, bit1 SPS/PPS 포함), `u32 width`, `u32 height`, `u64 seq`, `u64 capture_mono_us`, `u64 encode_done_mono_us`, 이후 Annex-B 바이트 |
| `0x02` info | JSON: `codec`, `width`, `height`, `src_width`, `src_height`, `fps`, `bitrate`, `encoder`(MFT 이름), `hardware`(bool), `capture`(`dd`/`gdi`), `display` |
| `0x03` stats | JSON: `fps`, `bitrate`, `dropped`, `encode_ms_p50`, `encode_ms_p95`, `capture_ms_p50` (1초마다) |
| `0x04` paused | JSON: `reason` — `no_viewer` 또는 캡처 사유 고정 문자열 `session0`/`rdp_output_suppressed`/`secure_desktop`/`session_locked`/`session_disconnected`/`access_lost`/`no_output`/`no_encoder` |

데몬 → 에이전트(같은 스트림 반대 방향): 종류 `0x40` 프레임 + JSON — `{"op":"keyframe"}`, `{"op":"bitrate","bps":…}`, `{"op":"config","max_fps":…,"max_width":…}`, `{"op":"pause"}`, `{"op":"resume"}`.

- 수락: 헤더를 받아들이면 첫 프레임이 `info`다. 디스플레이가 지금 캡처되지 않으면 첫 프레임이 `paused`이고, 캡처가 되면 `info`가 이어진다. 거절(줄 하나, §2): `codec`이 `h264`가 아니거나 이 OS가 캡처·인코딩을 못 하면 `CAPABILITY_UNSUPPORTED`, 없는 `display`는 `NOT_FOUND`, 범위 밖 값은 `INVALID_ARGUMENT`.
- `info`: 이후 AU의 인코딩. 인코더를 새로 만들 때(출력 크기·`max_fps` 변경, 원본 크기 변경, 인코더 장애) 그리고 `paused` 뒤에 영상이 다시 흐를 때 보내고, 그 다음 AU는 SPS/PPS를 가진 IDR이다. `width`·`height`는 짝수이고 원본의 종횡비를 따른다(`max_width`보다 좁으면 원본 크기). 커서는 영상에 그리지 않는다(§5).
- AU: `seq`는 스트림 안에서 1부터 1씩 는다. flags는 Annex-B의 NAL 종류로 정한다(5가 있으면 키프레임, 7과 8이 모두 있으면 SPS/PPS). 모든 IDR에 SPS/PPS가 들어 있다. 주기 IDR은 없다(GOP 0, 키프레임은 첫 프레임·`keyframe` 요청·`info` 뒤에만). `capture_mono_us`는 이미지를 얻은 시각(`time.sync`와 같은 시계), 같은 이미지를 다시 인코딩한 AU는 그 인코딩을 시작한 시각이다.
- 속도: `max_fps`가 상한이다(어느 1초에도 `max_fps`+1개 이하). 화면이 바뀌지 않으면 AU를 보내지 않고, 10초 동안 AU가 없으면 마지막 이미지를 키프레임이 아닌 AU로 한 번 다시 보낸다. 데몬의 주기 키프레임 요청은 데몬 몫이다.
- `stats`: 영상이 흐르는 동안(`paused` 아님) 1초마다. `fps`는 그 1초에 쓴 AU 수, `bitrate`는 AU Annex-B 바이트의 bps, `dropped`는 인코더가 받을 자리가 없어 버린 프레임 수, `encode_ms`는 인코더에 넘기기 시작한 때부터 AU가 나온 때까지(NV12 변환 포함), `capture_ms`는 이미지를 얻은 때부터 인코더에 넘기기 시작한 때까지(속도 조절 대기 포함). 값이 없으면 0.
- `paused`: 캡처 장애의 고정 사유(§3.1)가 `secure_desktop`·`session_locked`면 같은 이름, 그 밖의 사유와 인코더를 만들지 못한 경우는 `display_unavailable`, `pause` 요청이면 `no_viewer`. 사유가 바뀔 때만 보내고, 그 전에 이미 인코더에 넣은 프레임의 AU를 먼저 쓴다. 다음 `info` 전에는 AU가 없다. 에이전트는 회복을 스스로 재시도한다(디스플레이 다시 열기는 0.5초부터 두 배씩 최대 5초 간격).
- 역방향 제어: `keyframe`은 다음 AU를 IDR로 만든다(화면이 그대로여도 곧바로 마지막 이미지를 IDR로 보낸다). `bitrate`(`bps` 100000–200000000)는 인코더에 바로 적용하고, 적용하지 못하면 인코더를 새로 만들어 `info`와 IDR을 보낸다. `config`는 `max_fps`·`max_width`를 헤더와 같은 뜻(0은 기본값)으로 둘 다 바꾸고 `info`와 IDR을 보낸다. `pause`는 캡처·인코더를 닫고 `paused`(`no_viewer`)를 보내며, `resume`은 다시 열어 `info`와 IDR부터 보낸다. 범위 밖 값, 알 수 없는 `op`, `0x40`이 아닌 종류의 프레임은 무시한다. 데몬이 자기 쪽 방향을 닫으면(FIN) 에이전트는 파이프라인을 멈추고 스트림을 닫는다.

## 5. `cursor` 프레임

| 종류 | 형식 |
|---|---|
| `0x10` shape | `u32 shape_id`, `u16 w`, `u16 h`, `u16 hot_x`, `u16 hot_y`, 이후 BGRA(비곱셈 알파) w·h·4바이트 |
| `0x11` pos | `i32 x`, `i32 y`(캡처 디스플레이 원본 픽셀 좌표), `u8 visible`, `u32 shape_id` |

## 6. `input` 프레임

데몬 → 에이전트: 종류 `0x41` 프레임 + JSON 이벤트. 공통 필드 `seq`(증가), `gen`.

| `k` | 필드 |
|---|---|
| `move` | `x`, `y`(디스플레이 원본 픽셀), `display` |
| `button` | `b`(`left`/`right`/`middle`/`x1`/`x2`), `down`(bool) |
| `wheel` | `dx`, `dy`(WHEEL_DELTA 단위 정수, 정밀 스크롤은 120 미만 값 허용) |
| `key` | `sc`(스캔코드), `ext`(확장키 bool) 또는 `vk`; `down`(bool) |
| `text` | `text`(유니코드), `method`(`unicode`/`paste`) |
| `release_all` | — |

에이전트 → 데몬: 종류 `0x42` 프레임 + JSON `{"seq":n,"ok":true,"done_mono_us":…}` 또는 `{"seq":n,"ok":false,"code":"CONTROL_PREEMPTED"|"ELEVATION_REQUIRED"|"SESSION_LOCKED"|"DISPLAY_UNAVAILABLE","error":"…"}`. 입력 스트림이 닫히면 에이전트는 눌린 키·버튼을 모두 해제한다.

## 7. `pty` 프레임

| 종류 | 방향 | 형식 |
|---|---|---|
| `0x20` data | 양방향 | 원시 바이트(UTF-8) |
| `0x21` resize | 데몬→에이전트 | JSON `cols`, `rows` |
| `0x22` exit | 에이전트→데몬 | JSON `code` |
| `0x23` attached | 에이전트→데몬 | JSON `job`, `pid`, 이후 스크롤백(최대 1 MiB)이 data 프레임으로 이어진다 |

스트림이 끊겨도 ConPTY 프로세스는 남고 `job`으로 재부착한다. `exec.kill`로 끝낸다.

## 8. 오류 코드

`OFFLINE`, `AUTH_REQUIRED`, `SESSION_LOCKED`, `SESSION_CHANGED`, `CONTROL_HELD_BY_HUMAN`, `CONTROL_PREEMPTED`, `STALE_TARGET`, `DISPLAY_UNAVAILABLE`, `ELEVATION_REQUIRED`, `CAPABILITY_UNSUPPORTED`, `PORT_CONFLICT`, `TRANSFER_CONFLICT`, `HASH_MISMATCH`, `DISK_FULL`, `NOT_FOUND`, `PERMISSION_DENIED`, `INVALID_ARGUMENT`, `IN_PROGRESS`, `EXECUTION_OUTCOME_UNKNOWN`, `TIMEOUT`, `INTERNAL`.
