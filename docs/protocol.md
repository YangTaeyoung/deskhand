# 에이전트 ↔ 데몬 프로토콜 v1

코드: `internal/wire`(타입·프레이밍), `internal/transport`(QUIC·TLS). 이 문서와 코드가 다르면 코드를 고치거나 이 문서를 같은 변경에서 고친다.

## 1. 연결

- QUIC(quic-go), UDP 47600(기본), ALPN `deskhand/1`, TLS 1.3.
- 양쪽 모두 자체 서명 Ed25519 인증서를 쓴다. 상대 인증서의 SHA-256 지문(DER 기준, 소문자 hex)이 페어링 때 저장한 값과 같을 때만 연결을 유지한다. 일반 CA 검증은 쓰지 않는다.
- 데몬이 연결하고(클라이언트), 에이전트가 듣는다(서버).
- keep-alive 2초, idle timeout 8초. 데몬은 연결이 끊기면 지수 backoff(0.5초에서 시작, 최대 10초, ±20% jitter)로 재연결하고 `connection_generation`을 1 올린다. 한 번 ready였던 호스트에 다시 걸 때 아무것도 답하지 않으면(핸드셰이크·다이얼 시간 초과, 연결 거부) backoff를 처음 값(0.5초)으로 되돌린다.

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
| `video` | 데몬 | `display`(인덱스, 기본 0), `max_fps`(기본 60), `bitrate`(bps), `max_width`(0=원본), `codec`(`h264`) | 프레임(§4) |
| `cursor` | 데몬 | `display` | 프레임(§5) |
| `input` | 데몬 | `gen`(writer generation) | 프레임(§6) |
| `pty` | 데몬 | `job`(재부착) 또는 `new`: {`shell`, `cwd`, `cols`, `rows`, `env`} | 프레임(§7) |
| `tunnel` | 데몬 | `port`(1–65535, 루프백만) | 원시 바이트 |
| `put` | 데몬 | `transfer_id`, `path`(최종 경로), `offset`(임시 파일 크기), `size`(전체), `mtime`(RFC 3339), `roots` | 수락 줄 `{"ok":true,"part_size":…}` 뒤 데몬이 원시 바이트를 쓰고 FIN, 에이전트가 결과 줄 `{"ok":true,"received":n,"part_size":…}`(실패면 `ok:false`·`code`·`error`·`retryable`에 같은 두 필드)을 쓰고 FIN |
| `get` | 데몬 | `path`, `offset`, `length`(0=끝까지), `roots` | 수락 줄 `{"ok":true,"size":…,"offset":…,"length":n,"mtime":…}` 뒤 에이전트가 원시 바이트 n개를 쓰고 FIN. 다 쓰기 전에 실패하면 FIN 대신 스트림을 리셋한다 |

에이전트가 헤더를 거절하면 `{"ok":false,"code":"…","error":"…"}` 한 줄(`put`·`get`은 `retryable`도)을 쓰고 스트림을 닫는다. 처리기가 없는 `s`는 `CAPABILITY_UNSUPPORTED`다. 수락하면 `{"ok":true, …}` 한 줄을 먼저 쓴다(`video`는 `info` 프레임으로 대신한다). `rpc` 스트림은 수락 줄 없이 바로 `hello`로 시작한다.

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
- `session`: Windows는 `session_id`, `user`(에이전트 계정), `connect_state`(`active`/`connected`/`disconnected`/`other`) 중 읽은 것. `displays`는 화면 기능 전까지 `[]`. `boot_id`는 에이전트 프로세스가 시작할 때마다 바뀐다.
- `rid`(요청 ID): 데몬은 비멱등 op에 항상 붙인다. 기록 대상 op는 `exec.run`, `exec.start`, `exec.write`, `exec.kill`, `proc.kill`, `fs.write`, `fs.rename`, `fs.mkdir`, `fs.delete`, `fs.copy`, `fs.trash`, `xfer.commit`, `xfer.abort`, `app.launch`, `clipboard.set`, `win.action`, `ui.act`이고(데몬과 에이전트가 함께 쓰는 목록 `internal/wire.JournaledOps`), `rid`가 없는 요청은 기록 없이 실행한다. 형식은 영문자·숫자·`-`·`_` 1–128자의 문자열(Windows 장치 이름 제외)이다. 요청에 `rid`가 있으면 op와 상관없이 형식을 검사하고, 어기면(문자열이 아님, 빈 문자열, 허용하지 않는 문자) 실행하지 않고 `INVALID_ARGUMENT`로 답한다.
- 에이전트는 실행 전에 `DataDir/journal/<rid>.json`에 실행 중 기록을 쓰고, 끝나면 응답(`t`, `id` 제외)으로 바꾼다. 끝난 기록은 24시간 보존한다. 같은 `rid`가 다시 오면 실행하지 않고 저장한 응답을 `"replayed":true`와 함께 돌려준다(저장한 응답이 실패면 그 실패 그대로). 실행 중이면 `{"ok":false,"code":"IN_PROGRESS","retryable":true}`. 다른 op에 쓴 `rid`면 `INVALID_ARGUMENT`.
- 에이전트가 실행 중에 멈췄던 `rid`는 다시 실행하지 않고 `{"ok":false,"code":"EXECUTION_OUTCOME_UNKNOWN"}`를 저장한 응답으로 삼는다.
- `req.status{rid}` 응답: 기록이 없으면 `{"ok":true,"state":"unknown"}`, 실행 중이면 `{"ok":true,"state":"running"}`, 끝났으면 저장한 응답의 필드(`ok`·`code`·`error` 또는 결과 필드)에 `"state":"done"`을 더한 것.
- 오류: `ok:false`, `code`(대문자 상수), `error`(다음 행동을 알려 주는 문장), `retryable`. Alcove에서 가져온 op의 오류에는 아직 `code`가 없는 것이 있다.
- `caps`: 기능별 `{"state":"ready|unavailable|unsupported|needs_user","reason":"…"}`(`ready`면 `reason` 없음). 키: `screen`, `input`, `uia`, `exec`, `files`, `processes`, `pty`, `tunnel`, `diag`, `clipboard`. 바뀌면 에이전트가 `{"t":"ev","name":"caps.changed","caps":{…전체…}}`를 보낸다(welcome 직후 hello와 다를 때 포함).
- `exec.run` 결과: `code`, `stdout`, `stderr`(각각 끝 200000바이트), `timed_out`, `truncated`, 그리고 요청이 온 연결이 닫혀 프로세스 트리를 끝냈을 때 `killed:"connection_closed"`(이때와 시간 초과 때 `code`는 -1).
- `exec.read{job, out_offset, err_offset, max}` 결과: `state`(`running`/`exited`/`lost`), `pid`, `out`, `err`, `out_next`, `err_next`, `dropped`, `code`(`exited`일 때), `eof`(프로세스가 끝났고 이번 읽기로 두 스트림 끝까지 읽었을 때만 true). 한 번에 스트림마다 최대 256 KiB이므로 `eof`가 true가 될 때까지 `*_next`로 이어 읽는다. 모르는 job은 `NOT_FOUND`.
- exec job: `exec.start` job은 메타와 stdout·stderr 끝 1 MiB를 `DataDir/jobs/<job>/`에 기록한다. 에이전트가 다시 시작하면 그 job이 `exec.list`·`exec.read`에 `state:"exited"`(종료 코드 기록 시 `code` 포함) 또는 `state:"lost"`(실행 중에 에이전트가 멈춤, `code` 없음)로 남는다.

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
| `fs.copy` | `from`, `to`(복사본의 전체 경로, 그 폴더는 있어야 한다), `on_conflict`(`fail` 기본/`replace`/`skip`/`rename`), `recursive`(폴더일 때), `roots` | `path`(복사본의 실제 경로), `copied`(복사한 파일 수), `outcome`(`written`/`skipped`/`renamed`) |
| `fs.trash` | `path`, `roots` | `trashed`(없는 경로면 false) |
| `fs.hash` | `path`, `offset`, `length`(0=끝까지), `roots` | `sha256`, `offset`, `length`(읽은 바이트 수), `size`(파일 크기) |
| `xfer.prepare` | `transfer_id`, `path`, `size`, `mtime`, `sha256`(전체, push일 때), `on_conflict`(`fail`/`replace`/`skip`/`rename`), `roots` | `part_size`(이미 받은 바이트), `part_sha256`, `free_bytes`, `dest_exists` |
| `xfer.commit` | `transfer_id`, `path`, `sha256`, `on_conflict`, `roots` | `path`(실제 반영 경로), `outcome`(`written`/`skipped`/`renamed`) |
| `xfer.abort` | `transfer_id`, `path`, `roots` | `removed` — 임시 파일만 지운다 |
| `pty.list` | — | `ptys`: [{`job`, `pid`, `cols`, `rows`, `state`}] |
| `debug.ports` | `count` | `ports` — 루프백 빈 포트 |
| `diag.system` | — | `os`{`name`, `version`, `build`, `edition`}, `computer_name`, `cpu`{`name`, `cores`, `threads`, `usage_pct`}, `memory`{`total_bytes`, `used_bytes`}, `disks`: [{`root`, `label?`, `fs`, `total_bytes`, `free_bytes`}], `gpus`: [{`name`, `driver_version?`, `memory_bytes?`}], `boot_time`(RFC 3339), `uptime_s` |
| `diag.services` | `name?`(부분 일치, 대소문자 무시) | `services`: [{`name`, `display_name`, `state`(`running`/`stopped`/`paused`/`start_pending`/`stop_pending`/`unknown`), `start_type`(`auto`/`delayed_auto`/`manual`/`disabled`/`unknown`), `pid?`}] |
| `diag.events` | `log`(`Application`/`System`, 기본 `System`), `since?`(RFC 3339, 기본 24시간 전), `until?`, `levels?`([`critical`/`error`/`warning`/`information`/`verbose`], 기본 전부), `provider?`(정확히 일치), `max?`(기본 200, 최대 1000) | `events`: [{`record_id`, `time`, `level`, `provider`, `event_id`, `message`}](최신 먼저), `truncated`(`max`에서 잘렸으면 true) |
| `diag.crashes` | `since?`(기본 30일 전) | `dumps`: [{`path`, `app`, `time`, `size_bytes`, `kind`(`minidump`/`full`/`report`)}](최신 먼저). 파일은 `get` 스트림으로 받는다 |
| `agent.update` | `path`(이미 put한 새 실행 파일), `sha256` | 자기 교체 후 재시작 예약 |

파일 op `fs.copy`, `fs.trash`, `fs.hash`의 규칙:

- 경로 검사: Windows에서는 경로를 정리(`.`·`..`·구분자)한 뒤 `\\?\` 접두를 뗀다. `\\?\C:\…`는 `C:\…`, `\\?\UNC\서버\공유\…`는 `\\서버\공유\…`, `\\?\Volume{…}\…`는 그 볼륨이 연결된 첫 드라이브·폴더 아래 경로가 되고(연결된 곳이 없으면 `INVALID_ARGUMENT`), 결과 경로에는 접두를 붙이지 않는다. `\\.\…`와 `\\?\` 뒤가 드라이브 문자·`UNC\`·`Volume{`가 아닌 경로(`\\?\GLOBALROOT…` 등)는 장치 경로로 보고 `INVALID_ARGUMENT`. 장치 예약 이름(`CON`, `PRN`, `AUX`, `NUL`, `CONIN$`, `CONOUT$`, `COM0`–`COM9`, `LPT0`–`LPT9`, `COM¹²³`·`LPT¹²³`, 대소문자 무관, 확장자가 붙어도), 점이나 공백으로 끝나는 이름, 제어 문자나 `< > : " | ? *`가 든 이름도 `INVALID_ARGUMENT`다. 260자를 넘는 경로도 다룬다(`fs.trash` 제외).
- `roots`(선택): 절대 폴더 경로 배열. 비어 있거나 없으면 제한이 없다. 있으면 경로의 폴더를 링크·junction·짧은 이름·대소문자까지 해석한 경로와 경로 전체를 해석한 경로가 모두 어느 root(같은 방식으로 해석) 안이어야 하고, 아니면 `PERMISSION_DENIED`다. 대상이 없는 링크를 지나는 경로도 `PERMISSION_DENIED`다.
- 오류에는 항상 `code`가 있다. 없는 경로·폴더 `NOT_FOUND`, 볼륨 가득 참 `DISK_FULL`, 접근 거부 `PERMISSION_DENIED`, 다른 프로그램이 연 항목 `PERMISSION_DENIED`(`retryable:true`). 파일 시스템 루트·홈 폴더와 그 상위를 지우려는 `fs.trash`·`fs.delete`도 `PERMISSION_DENIED`다.
- `fs.copy`: 복사본을 `to`의 폴더에 임시 이름(`.deskhand-copy-<임의>`)으로 끝까지 쓰고 디스크에 내린 뒤 `to`로 옮긴다(덮어쓰지 않는 이름 바꾸기, `replace`일 때만 파일 교체). 실패하면 임시 항목을 지운다. 내용·수정 시각·권한 비트(Windows는 읽기 전용 여부)를 복사하고, 숨김 같은 다른 속성·ACL·대체 데이터 스트림은 복사하지 않는다. 폴더는 `recursive`가 있어야 하고 안의 링크·junction·특수 파일은 `INVALID_ARGUMENT`다. 링크인 `from`도 거절한다. 폴더를 그 안으로(대소문자·링크로 달리 쓴 같은 폴더 포함, 파일 ID로 비교) 복사하면 `INVALID_ARGUMENT`이고, 대상 쪽 폴더를 읽지 못해 그걸 가릴 수 없을 때도 `INVALID_ARGUMENT`다. 배포 루트 안 대상은 `fs.write`처럼 루트를 잠근 채로 먼저 만든다. `to`가 있으면 `fail`은 `TRANSFER_CONFLICT`, `skip`은 복사하지 않고 `outcome:"skipped"`(`path`는 있는 항목), `replace`는 파일을 파일로만 바꾸고(폴더가 끼면 `TRANSFER_CONFLICT`), `rename`은 `이름 (1).확장자`부터 비어 있는 이름에 쓴다(`outcome:"renamed"`). 대소문자만 다른 이름이 있으면 `fail`·`replace`는 그 이름을 밝힌 `TRANSFER_CONFLICT`다. Windows 밖 에이전트가 데스크톱 세션 사용자의 홈 안으로 복사하는 것은 `CAPABILITY_UNSUPPORTED`다.
- `fs.trash`: 현재 사용자의 휴지통으로 옮긴다(`SHFileOperationW` `FO_DELETE`, `FOF_ALLOWUNDO|FOF_NOCONFIRMATION|FOF_SILENT|FOF_NOERRORUI|FOF_NO_CONNECTED_ELEMENTS`, 창을 띄우지 않는다). 검사와 셸 호출은 경로의 정규형으로 한다: 폴더 부분은 링크·junction·짧은 이름·대소문자를 해석하고 마지막 이름은 폴더에 있는 긴 이름 그대로여서 링크는 링크 자체를 옮긴다. 정규형이 드라이브 문자 경로(`X:\…`)가 아니면(공유로 이어지는 링크 등) `CAPABILITY_UNSUPPORTED`. 휴지통이 받지 않고 바로 지울 항목도 옮기지 않고 `CAPABILITY_UNSUPPORTED`다: 고정 로컬 드라이브가 아니거나(네트워크·이동식·subst 등) 휴지통이 없는 볼륨, HKLM·HKCU 어느 쪽이든 켜진 "삭제된 파일을 휴지통으로 이동하지 않음" 정책이나 볼륨의 즉시 삭제 설정, 정규형 경로나 폴더 안 경로가 250자(UTF-16) 이상, 볼륨 휴지통 최대 크기(`MaxCapacity`와 HKLM·HKCU `RecycleBinSize` 정책 중 가장 작은 값)보다 큰 항목, 그 크기를 읽지 못할 때, 이미 휴지통에 있는 항목. 셸은 260자 이상 경로를 성공으로 보고하며 영구 삭제한다(실측). 셸이 성공을 보고하면 그 볼륨의 사용자 휴지통에서 원래 정규형 경로와 호출 시각 이후 삭제 시각을 가진 `$I` 기록을 찾아 확인하고, 없으면 `trashed:true` 대신 `INTERNAL`("…was deleted permanently instead of being moved to the Recycle Bin…")로 답한다. 검사와 셸 호출 사이(그동안 폴더가 커지거나 긴 경로가 생기는 경우)는 막지 못하며 그 결과는 이 사후 확인으로만 드러난다. 영구 삭제로 대신하지 않는다(영구 삭제는 `fs.delete`). Windows 밖의 에이전트는 항상 `CAPABILITY_UNSUPPORTED`.
- `fs.hash`: 일반 파일의 `offset`부터 `length`바이트(0이면 끝까지)의 SHA-256. `offset`이 파일 크기보다 크면 `INVALID_ARGUMENT`.

전송(`put`·`get` 스트림, `xfer.*`)의 규칙:

- 임시 파일은 대상과 같은 폴더의 `<대상 이름>.deskhand-part-<transfer_id>`이고 Windows에서는 숨김 속성을 갖는다. `transfer_id`는 영문자·숫자·`-`·`_` 1–64자이고, 임시 파일 이름이 255자(Windows는 UTF-16, 그 밖은 바이트)를 넘게 되는 대상 이름은 `INVALID_ARGUMENT`다.
- 경로 검사와 `roots`는 파일 op와 같다. 그 전에, 받은 그대로의 경로에 `/`·`\` 사이의 `.`이나 `..` 원소가 있으면(`put`, `get`, `xfer.*`) `INVALID_ARGUMENT`다. 쓰는 쪽(`put`, `xfer.prepare`, `xfer.commit`)은 대상 폴더가 있어야 하고(`NOT_FOUND`), Windows 밖 에이전트는 데스크톱 세션 사용자의 홈 안에 쓰지 않는다(`CAPABILITY_UNSUPPORTED`).
- 한 임시 파일에는 한 번에 한 작업(`put`, `xfer.prepare`, `xfer.commit`, `xfer.abort`)만 돈다. 같은 파일을 가리키는 다른 표기(짧은 이름, 대소문자, 링크·junction을 지나는 폴더)도 같은 임시 파일로 본다. 다른 작업 중이면 `IN_PROGRESS`(`retryable:true`).
- `xfer.prepare`: `on_conflict`를 commit처럼 미리 적용해 `fail`과 대소문자만 다른 이름(`fail`·`replace`)은 여기서 `TRANSFER_CONFLICT`다. 남은 바이트(`size` − `part_size`)가 볼륨 여유(`free_bytes`)보다 크면 아무것도 만들지 않고 `DISK_FULL`(`skip`이고 대상이 있을 때 제외). 임시 파일이 없으면 빈 파일로 만들고, `mtime`이 있으면 임시 파일에 준다. `part_sha256`은 임시 파일 전체의 SHA-256이다. 데몬은 원본 앞 `part_size`바이트의 SHA-256과 다르면 `xfer.abort` 뒤 0부터 보낸다.
- `put`: 헤더 `offset`이 임시 파일 크기(없으면 0)와 다르면 쓰지 않고 두 수를 밝힌 `INVALID_ARGUMENT`다. 받은 바이트는 스트림이 끊겨도 남고(`INTERNAL`·`TIMEOUT` retryable, 결과 줄 `part_size`), 다음 put은 그 크기부터다. `size`를 넘는 바이트가 오면 그 put으로 쓴 바이트를 버리고 `INVALID_ARGUMENT`. 볼륨이 차면 실제로 쓴 바이트까지만 남겨 디스크에 내리고 `DISK_FULL`(결과 줄 `part_size`)이고, 공간을 비운 뒤 prepare가 그 크기와 SHA-256을 알려 준다. 어느 경우든 끝나면 임시 파일의 수정 시각을 헤더 `mtime`으로(없으면 그대로) 둔다.
- `xfer.commit`: 임시 파일 전체의 SHA-256이 `sha256`과 다르면 `HASH_MISMATCH`이고 대상과 임시 파일은 그대로다. 같으면 디스크에 내리고 `on_conflict`를 `fs.copy`와 같이 적용한다: `skip`은 임시 파일을 지우고 `outcome:"skipped"`(`path`는 있는 항목), `rename`은 `이름 (1).확장자`부터 비어 있는 이름, `replace`는 파일만 교체(Windows `MoveFileExW(MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH)`), 대상이 없으면 덮어쓰지 않는 이름 바꾸기. 바뀐 파일은 원래 대상의 ACL·소유자·권한 비트·대체 데이터 스트림을 잇지 않고 받은 파일의 것을 갖는다(`fs.copy`의 `replace`와 같다). 반영된 파일의 수정 시각은 prepare·put의 `mtime`이다. 다른 프로그램이 삭제 공유 없이 연 대상은 `PERMISSION_DENIED`(`retryable:true`), 읽기 전용이거나 ACL이 막는 대상은 `PERMISSION_DENIED`이고, 어느 쪽이든 대상은 바뀌지 않고 임시 파일이 남는다. 다시 보낼 때는 새 rid로 보낸다(같은 rid는 저장한 실패를 돌려준다).
- `xfer.commit`은 `transfer_id`와 `path`마다 멱등이다: 성공한 commit은 영수증(`transfer_id`, 실제 `path`, `outcome`, `sha256`)을 `DataDir/receipts/`에 rid 기록과 같은 방식(임시 파일·디스크에 내림·이름 바꾸기)으로 남겨 24시간 보존하고, 임시 파일이 없는 commit은 영수증이 있으면 `sha256`이 같을 때 그 `path`·`outcome`을, 다르면 두 값을 밝힌 `INVALID_ARGUMENT`를 돌려주며, 영수증도 없으면 `NOT_FOUND`다. commit으로 임시 파일이 옮겨진 뒤의 `xfer.abort`는 대상을 건드리지 않고 `removed:false`다.
- `get`: 일반 파일만 읽는다. 폴더와 FIFO 같은 특수 파일은 열지 않고 `INVALID_ARGUMENT`다.
- `xfer.abort`: 임시 파일만 지운다. 없으면 `removed:false`이고, 그 이름의 폴더·링크는 건드리지 않고 `INVALID_ARGUMENT`다.

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
