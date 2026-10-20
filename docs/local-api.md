# 데몬 로컬 API v1

앱·CLI·MCP가 공통으로 쓰는 데몬 API. 코드: `internal/daemon/localapi`(서버), `internal/client`(Go 클라이언트), `app/DeskhandKit/Sources/DeskhandClient`(Swift 클라이언트).

## 1. 연결

- unix socket `~/Library/Application Support/Deskhand/run/d.sock`(권한 0600, 디렉터리 0700). 경로는 `DESKHAND_HOME`으로 바꿀 수 있다(테스트용).
- NDJSON JSON-RPC 2.0. 요청 `{"jsonrpc":"2.0","id":1,"method":"host.list","params":{}}`, 응답 `result` 또는 `error`.
- 오류: `{"code":-32000,"message":"<다음 행동을 알려 주는 문장>","data":{"code":"CONTROL_HELD_BY_HUMAN","retryable":false,"next_action":"human_release"}}`. `data.code`는 [protocol.md §8](protocol.md#8-오류-코드)의 상수.
- 첫 요청은 `hello{client:"app"|"cli"|"mcp", name, version}`. 데몬은 `client_id`를 돌려준다. 제어권과 활동 기록은 이 `client_id`로 남는다.
- 알림: `events.subscribe{host_id?, kinds?}` 후 `{"jsonrpc":"2.0","method":"event","params":{seq, ts, host_id, kind, …}}`.

## 2. 공통 결과 봉투

상태를 바꾸거나 원격에서 실행하는 메서드의 `result`는 다음 필드를 포함한다.

```json
{
  "schema_version": 1,
  "request_id": "r-01JB…",
  "job_id": "j-01JB…",
  "host_id": "windows-dev",
  "session_id": "s-01JB…",
  "connection_generation": 4,
  "status": "succeeded|failed|running|waiting_for_user|unknown|cancelled",
  "action": {"executed": true, "outcome": "done|not_started|unknown"},
  "error": null,
  "next_action": null,
  "artifacts": []
}
```

`error`는 실패했거나 결과를 모를 때 `{code, message, retryable, next_action?}` 객체이고 그 밖에는 `null`이다. `code`는 [protocol.md §8](protocol.md#8-오류-코드)의 상수, `message`는 다음 행동을 알려 주는 한 문장, `retryable`은 같은 요청을 그대로 다시 보내도 되는지, `next_action`은 `check_state`/`retry`/`fix_params`/`host_repair`/`host_connect`/`host_doctor`/`host_add_force`/`human_release` 같은 다음 단계 이름이다. 봉투의 `next_action`은 따로 정하지 않으면 `error.next_action`과 같다.

## 3. 메서드

### 호스트·연결
| 메서드 | 파라미터 | 결과 |
|---|---|---|
| `host.list` | — | `hosts`: [{`host_id`, `name`, `os`, `arch`, `address`, `state`(연결 상태), `caps`, `session`, `owner`, `last_seen`}] |
| `host.add` | `name`, `ssh`(별칭), `address`(선택), `port`(선택, 기본 47600: 에이전트가 듣는 UDP 포트이자 방화벽 규칙이 여는 포트), `force`(선택) | 설치·페어링 job(진행 이벤트 `host.install.progress`) |
| `host.repair` | `host_id` | 저장된 `port`로 다시 설치·페어링하는 job |
| `host.remove` | `host_id`, `uninstall`(bool) | — |
| `host.doctor` | `host_id` | 항목별 `checks`: [{`id`, `status`(`pass`/`fail`/`warn`/`skip`), `detail`, `fix`}] |
| `host.connect` / `host.disconnect` | `host_id` | 연결 상태 |
| `session.inspect` | `host_id` | session.info + displays + caps + owner + `connection_generation` |

### 제어권
| 메서드 | 파라미터 | 결과 |
|---|---|---|
| `control.get` | `host_id` | `owner`(`none`/`human`/`ai`), `owner_client`, `gen`, `ai_paused` |
| `control.acquire` | `host_id`, `as`(`human`/`ai`), `force`(사람만) | 새 owner |
| `control.release` | `host_id` | — |
| `control.stop` | `host_id` | 긴급 정지: owner=human, `ai_paused=true`. 연결이 끊긴 호스트에도 적용된다(재연결 후 AI 입력 차단 유지) |
| `control.resume_ai` | `host_id` | `ai_paused=false`, owner=none |

### 관찰·입력 (AI·CLI)
| 메서드 | 파라미터 | 결과 |
|---|---|---|
| `screen.capture` | `host_id`, `display`, `max_width`(기본 1568), `format` | `capture_id`, `width`, `height`, `scale`, `origin`, `dpi`, `ts`, artifact(이미지 파일) |
| `input.click` | `host_id`, `capture_id`, `x`, `y`(캡처 좌표), `button`, `count` | 봉투 + `observation` |
| `input.move` / `input.drag` | `capture_id`, 좌표들 | 봉투 |
| `input.scroll` | `capture_id`, `x`, `y`, `dx`, `dy` | 봉투 |
| `input.key` | `keys`(예: `ctrl+shift+esc`, `enter`) | 봉투 |
| `input.type` | `text`, `method`(`auto`/`unicode`/`paste`) | 봉투 |
| `observe` | `host_id`, `level`(`none`/`summary`/`image`/`ui`), `settle_ms` | `foreground`, `focused`, `changed`, `capture`(선택), `ui`(선택) |

좌표는 `capture_id`의 이미지 좌표다. 데몬이 원본 픽셀로 환산한다. `capture_id`가 다른 `connection_generation`이나 바뀐 디스플레이 구성에서 나왔으면 `STALE_TARGET`.

### UI Automation
`ui.tree`, `ui.find`, `ui.text`, `ui.rect`, `ui.act`, `ui.focused`, `ui.at`, `win.list`, `win.action` — 에이전트 op와 같은 파라미터에 `host_id`. `ui.act`·`win.action`은 제어권 검사를 거친다.

### 명령·터미널·프로세스
| 메서드 | 결과 |
|---|---|
| `exec.run{host_id, argv|cmd, shell, cwd, env, stdin, timeout_ms}` | 봉투 + `code`, `stdout`, `stderr`, `timed_out`(시간 초과로 끝냈으면 true), `truncated`(스트림마다 끝 200000바이트만 남겼으면 true), `killed?`(§7) (큰 출력은 artifact) |
| `exec.start{…, timeout_ms?}` | 봉투(job). `timeout_ms`는 데몬이 job을 만든 시각부터 잰다. 넘으면 `jobs.cancel`과 같은 길로 프로세스를 끝내고 job을 `failed` + `TIMEOUT`(`result.timed_out:true`)으로 끝낸다. 그때까지의 출력은 `jobs.output`으로 읽을 수 있다. 데몬이 다시 시작해도 남은 시간을 이어 재고, 연결이 끊긴 동안 시간이 다 된 job은 다시 연결될 때 끝낸다 |
| `jobs.list{host_id?, kind?, status?}` / `jobs.get{job_id}` / `jobs.cancel{job_id}` | job 목록·상세 |
| `jobs.output{job_id, cursor}` | `chunks`, `next_cursor`, `state` |
| `proc.list` / `proc.kill` / `app.launch` | 에이전트 op와 동일 + 봉투 |
| `pty.open{host_id, shell?, cols?, rows?}` | 봉투(job, kind `pty`) + `pty_id` + `job`(Job). `shell`은 `powershell`/`cmd`/`pwsh`(`caps.pty.shells`에 있는 것, Windows 밖 PC는 `sh`), 생략하면 `caps.pty.shells`의 첫 셸. `cols`·`rows`는 함께 1–1000(둘 다 생략하면 80×24). 이후 `stream.attach{kind:"pty", pty_id}`. 셸 목록은 `jobs.list{host_id, kind:"pty", status:"running"}`, 끝내기는 `jobs.cancel{job_id}`(봉투 `cancelled`; 셸이 먼저 끝났으면 `succeeded`). pty job의 `result`는 `{shell, pid, client_name?}`(§5) |

### 파일
| 메서드 | 결과 |
|---|---|
| `files.list{host_id, path, recursive?, cursor?, limit?}` | `{host_id, path(실제 경로), entries:[FileEntry], total, next_cursor?, truncated, skipped?}` — 페이지 규칙은 §8 |
| `files.stat{host_id, path, sha256?}` | `{host_id, exists, path, name, dir, size, link, mtime?, owner?, sha256?, locked?}` — 없는 경로는 `exists:false` |
| `files.read{host_id, path, offset, length}` | `{host_id, path, data(base64), size(파일 전체), eof, encoding?}` |
| `files.write` | 봉투 + `size` |
| `files.copy{host_id, from, to, on_conflict?, recursive?}` | 봉투 + `path`(복사본의 실제 경로), `copied`(파일 수), `outcome`(`written`/`skipped`/`renamed`). `on_conflict`는 `fail`(기본)/`replace`/`skip`/`rename`, 규칙은 [protocol.md §3.1](protocol.md#31-op-목록) `fs.copy` |
| `files.trash{host_id, path}` | 봉투 + `trashed`(없는 경로면 false). 휴지통이 받지 않는 항목은 옮기지 않고 `failed` + `CAPABILITY_UNSUPPORTED`(영구 삭제로 대신하지 않는다. 영구 삭제는 `files.delete{permanent:true}`). 셸이 성공을 보고했는데 휴지통에 항목이 없으면 `failed` + `INTERNAL` |
| `files.hash{host_id, path, offset?, length?}` | 봉투 + `sha256`, `offset`, `length`(읽은 바이트 수), `size`(파일 크기). `length` 0이면 끝까지 |
| `files.rename` / `files.mkdir` / `files.delete{permanent:true}` | 봉투. `files.delete` 결과는 `deleted`. Mac과 PC 사이 이동은 `transfer.start{direction:"move_push"/"move_pull"}`(§8)다 |
| `transfer.start{direction:"push"|"pull"|"move_push"|"move_pull", host_id, local, remote, on_conflict}` | `transfer_id` |
| `transfer.list` / `transfer.get{transfer_id}` | 전송 목록(파일 수·바이트 합계) / TransferDetail(파일별 상태) — §8 |
| `transfer.pause` / `transfer.resume` / `transfer.cancel` | TransferDetail(§8) |

### 진단
`diag.system{host_id}`, `diag.services{host_id, name?}`, `diag.events{host_id, …}`, `diag.crashes{host_id, since?}` — 에이전트 op(protocol.md §3.1)와 같은 파라미터·결과 + 봉투, `save:true`면 결과 JSON을 artifact로 저장해 `artifact_id`를 붙인다. 크래시 덤프 받기는 `transfer.start{direction:"pull"}`. 프로세스 표는 `proc.list{host_id, name?}` → `procs`(protocol.md §3.1의 확장 필드 포함), 종료는 `proc.kill{host_id, pid, started?, tree?}` → 봉투 + `killed`.

### 디버깅
| 메서드 | 결과 |
|---|---|
| `tunnel.open{host_id, remote_port}` → `local_port` / `tunnel.close` / `tunnel.list` | |
| `debug.electron.launch{host_id, path, args, cwd}` | `debug_id`, `pid`, `targets`: [{`id`, `role`(`main`/`renderer`), `url`, `title`}] |
| `debug.attach{host_id, port|pid, role}` | `debug_id`, `targets` |
| `debug.breakpoint.set{debug_id, target, url_regex, line, condition}` / `.remove` | `breakpoint_id`, `locations` |
| `debug.pause` / `debug.resume` / `debug.step{kind:"over"|"into"|"out"}` | 상태 |
| `debug.state{debug_id}` | `paused`, `reason`, `call_frames`(함수·URL·줄·열), `scopes` |
| `debug.variables{debug_id, object_id}` | `properties` |
| `debug.evaluate{debug_id, expression, frame}` | `value` |
| `debug.console{debug_id, cursor}` | 콘솔·예외 기록 |
| `debug.close{debug_id}` | 자기 터널·프로세스 정리 |

### QA·산출물·기록
| 메서드 | 결과 |
|---|---|
| `qa.run{host_id, scenario_path | scenario}` | 봉투(job) |
| `qa.get{run_id}` / `qa.list` | 단계별 결과 |
| `artifacts.list{job_id}` / `artifacts.export{job_id, dir}` | 파일과 manifest |
| `timeline.list{host_id, since, limit}` | 사람·AI 동작 기록 |

## 4. 스트림 연결 (`stream.attach`)

새 소켓 연결에서 `hello` 다음 `stream.attach`를 부르면, 성공 응답 줄 이후 그 연결은 바이너리 프레임(`u32 BE 길이` + 본문, 첫 바이트 종류)으로 바뀐다.

`kind:"screen"` (`host_id`, `display`, `preset`(`sharp`/`balanced`/`saver`))
- 데몬 → 앱: 비디오 `0x01`–`0x04`(protocol.md §4와 동일), 커서 `0x10`·`0x11`, `0x30` 알림 JSON(`control_denied`, `owner_changed`, `connection_state`, `latency`{`capture_to_receive_ms`}).
- 앱 → 데몬: `0x40` 제어 JSON(`keyframe`, `preset`), `0x41` 사람 입력 JSON(protocol.md §6 이벤트, 좌표는 원본 픽셀). 소유권이 AI면 입력을 버리고 `control_denied`를 보낸다.
- `0x30` 알림 JSON은 `{"type": …}`로 구분한다: `latency`{`capture_to_receive_ms`, `clock_offset_ms`}, `control_denied`{`owner`, `owner_client?`}, `owner_changed`{ControlState 필드}, `connection_state`{`state`, `reason?`}.
- 사람 입력(`0x41`)의 `gen`은 데몬이 현재 writer generation으로 덮어쓴다. 앱은 0을 보내도 된다.
- 단축키: 화면 입력을 잡고 있어도 ⌘Q·⌘H·⌘Tab·⌘⇧.·⌘1–⌘7·⌘⌥0·⌘K·⌘,는 Mac에 남는다(설정 "시스템 단축키도 원격으로 보내기"로 ⌘Q·⌘H·⌘Tab만 원격 전달 가능, 기본 끔). 그 밖의 ⌘ 조합은 Cmd↔Ctrl 설정에 따라 원격으로 간다.

`kind:"pty"` (`pty_id` 또는 `job_id`): `0x20`–`0x23` (protocol.md §7과 동일). `pty_id`는 방금 연 셸, `job_id`는 실행 중인 pty job에 다시 붙는다(분리했거나 연결이 끊긴 셸). 붙으면 데몬이 `0x23`{`job`(데몬 job_id), `pid`}을 보내고 이어서 스크롤백(셸의 마지막 출력 최대 1 MiB, protocol.md §7)을 `0x20`으로 다시 보낸다. 앱은 `0x23`을 받을 때마다 현재 크기를 `0x21`로 보낸다. 끝난 job에 붙으면 `NOT_FOUND`.
- 성공 응답의 `result`는 `{job_id, pid, host_id}`. 실패는 JSON-RPC 오류다: 없는 job·pty나 끝난 셸 `NOT_FOUND`, PC에 연결돼 있지 않음 `OFFLINE`(`retryable:true` — 다시 연결되면 붙는다), 셸이 아직 열리는 중 `IN_PROGRESS`, `pty_id`와 `job_id`를 함께 주거나 둘 다 없으면 `INVALID_ARGUMENT`.
- 앱 → 데몬은 `0x20`(입력, 한 프레임 최대 1 MiB)과 `0x21`(`cols`·`rows` 각 1–1000, 범위 밖은 버린다)만 전달되고 다른 종류는 버린다. 데몬 → 앱은 `0x23`, `0x20`, `0x22`다.
- 셸이 끝나면 `0x22`{`code`} 뒤 데몬이 연결을 닫는다. `0x22` 없이 닫히면(PC 연결 끊김) 셸은 PC에서 계속 돌고 있으니 `job_id`로 다시 붙는다. 앱이 연결을 닫으면(분리) 셸은 계속 돈다. 한 셸에 여러 연결이 함께 붙을 수 있고 출력은 모두에게 간다.

스트림 연결 규칙: `stream.attach`는 `hello` 다음 아무 때나 부를 수 있지만 `events.subscribe`를 부른 연결에서는 `INVALID_ARGUMENT`다. 데몬은 그 연결에서 먼저 받은 요청의 응답을 모두 쓴 뒤 attach를 처리하고, 성공 응답 줄 바로 다음 바이트부터 프레임이다(응답 줄 뒤에 앱이 미리 보낸 바이트도 프레임으로 읽는다). 거절된 attach는 오류 응답만 쓰고 연결은 JSON-RPC 그대로 남는다. 모르는 `kind`는 `INVALID_ARGUMENT`, `kind:"screen"`은 화면 스트림 구현 전까지 `CAPABILITY_UNSUPPORTED`다.

## 5. 모델

모든 JSON 키는 snake_case. 시각은 RFC 3339 UTC 밀리초(`2026-10-20T12:34:56.789Z`). 표시 안 한 필드는 항상 있다; `?`는 없을 수 있다.

| 모델 | 필드 |
|---|---|
| Host | `host_id`, `name`, `os`, `arch`, `address`, `port`, `ssh_alias`, `state`(연결 상태), `caps`(기능 이름 → Capability), `session?`(SessionInfo), `displays`([DisplayInfo]), `owner`(`none`/`human`/`ai`), `owner_client?`, `agent_version?`, `connection_generation`, `last_seen?` |
| Capability | `state`(`ready`/`unavailable`/`unsupported`/`needs_user`), `reason?`, `shells?`(`pty` 기능만: 띄울 수 있는 셸, 첫 항목이 기본. Windows는 `powershell`, `pwsh`(PowerShell 7이 설치돼 있을 때), `cmd` 순서, Windows 밖 PC는 `sh`) |
| SessionInfo | `session_id`(정수), `user`, `sid?`, `connect_state`(`active`/`connected`/`disconnected`/`other`/`unknown`), `protocol`(`console`/`rdp`/`unknown`), `locked?`(에이전트가 잠금 상태를 보고하지 않으면 없음), `input_desktop`(`Default`/`Winlogon`/`unknown`), `integrity?` |
| DisplayInfo | `index`, `name`, `adapter`, `x`, `y`, `width`, `height`, `dpi`(정수), `scale`(실수), `primary`, `refresh_hz`(정수) |
| ControlState | `owner`, `owner_client?`, `gen`(호스트별 제어권 상태가 바뀔 때마다 — owner 또는 ai_paused — 1씩 오르는 값. 클라이언트는 더 큰 gen만 반영한다), `ai_paused` |
| SessionInspect (`session.inspect` 결과) | SessionInfo의 모든 필드 + ControlState의 모든 필드 + `displays`([DisplayInfo]) + `caps` + `connection_generation` — 한 단계 평평한 객체 |
| Job | `job_id`, `host_id`, `kind`(`exec`/`transfer`/`qa`/`install`/`debug`/`pty`), `status`(봉투 status 값), `request_id?`, `summary`, `progress?`(0–1), `created_at`, `updated_at`, `finished_at?`, `result?`(객체, 명령 출력 `stdout`·`stderr`는 넣지 않는다), `output_bytes?`(데몬이 기록한 stdout·stderr 바이트 수. 출력은 `jobs.output`과 artifact로만 읽는다). `kind:"transfer"` job은 전송 하나를 따른다: `status`는 전송이 `queued`·`running`·`verifying`·`paused`면 `running`(`paused`면 `result.paused:true`), `done`이면 `succeeded`, `failed`·`cancelled`면 그대로이고, `result`는 `{transfer_id, state, outcome?, paused?, error?}`다. `failed`·`cancelled` 전송을 `transfer.resume`하면 job도 다시 `running`(`finished_at` 없음)이 된다. `kind:"pty"` job은 셸 하나를 따른다: 셸이 도는 동안 `running`, 끝나면 종료 코드 0은 `succeeded`·그 밖은 `failed`(`result.code`), `jobs.cancel`로 끝내면 `cancelled`, 열지 못했으면 `failed`, PC 에이전트가 다시 시작돼 셸이 사라졌으면 `unknown`(`result.state:"lost"`, `error.code` `EXECUTION_OUTCOME_UNKNOWN`)이다. `result`는 `{shell, pid, client_name?}`이고 끝나면 `state`·`code`가 더해진다. `summary`는 `PowerShell 터미널`처럼 셸 이름이다 |
| FileEntry | `name`, `path`, `dir`, `size`, `mtime`, `link`, `hidden?`, `readonly?` |
| Transfer | `transfer_id`, `host_id`, `direction`(`push`/`pull`/`move_push`/`move_pull`), `local`, `remote`, `on_conflict`, `state`(`queued`/`running`/`paused`/`verifying`/`done`/`failed`/`cancelled`), `bytes_done`, `bytes_total`, `files_done`, `files_failed`, `files_total`, `rate_bps`, `outcome?`(`written`/`skipped`/`renamed`/`copied_source_retained`/`partial`/`failed`), `error?`, `rev`(전송마다 바뀔 때마다 1씩 오르는 정수 — 클라이언트는 더 큰 rev만 반영), `updated_at` |
| TransferDetail | Transfer의 모든 필드 + `files`([TransferFile]) — 한 단계 평평한 객체 |
| TransferFile | `rel`(폴더 전송은 폴더 안 상대 경로, `/` 구분. 파일 하나는 그 이름), `size`, `bytes_done`, `state`, `outcome?`, `sha256?`, `path?`(반영된 실제 경로 — `rename`이면 바뀐 이름), `error?` |
| ProcessInfo | protocol.md §3.1 `proc.list` 항목과 같다(`pid`, `ppid`, `name`, `path`, `cmd`, `mem_kb`, `cpu_pct`, `user?`, `started`, `started_by_deskhand`, `self`) |
| DiagSystem | protocol.md §3.1 `diag.system` 결과와 같다 |
| EventLogEntry | protocol.md §3.1 `diag.events`의 `events` 항목과 같다(`record_id`, `time`, `level`, `provider`, `event_id`, `message`) |
| DebugTarget | `id`, `role`(`main`/`renderer`), `type`, `url`, `title` |
| CallFrame | `id`, `function`, `url`, `line`, `column`, `scopes`[{`type`, `name?`, `object_id`}] |
| QARun | `run_id`, `host_id`, `name`, `status`(`running`/`passed`/`failed`/`blocked`/`error`), `started_at`, `finished_at?`, `steps_total`, `steps_passed`, `steps_failed`, `steps_blocked`, `report_dir?`, `steps`([QAStep], `qa.get`에서만) |
| QAStep | `index`, `name`, `action`, `status`(`pass`/`fail`/`blocked`/`not_run`), `started_at?`, `duration_ms?`, `message?`, `artifacts`([Artifact]) |
| TimelineEntry | `seq`, `ts`, `host_id`, `actor`(`human`/`ai`/`system`), `client_id?`, `client_name?`, `kind`(`input`/`exec`/`file`/`control`/`qa`/`debug`/`connection`/`install`), `summary`, `outcome`(`ok`/`failed`/`unknown`/`denied`), `job_id?`, `artifacts`([Artifact]) |
| Artifact | `artifact_id`, `job_id?`, `kind`(`screenshot`/`ui_tree`/`log`/`file`/`report`/`trace`), `name`, `path`, `size`, `mime`, `sha256`, `created_at` |

- 데몬 로그: `<home>/logs/daemon.log`.
- `events.subscribe`는 연결당 하나다. 다시 부르면 이전 필터를 대체한다.

## 6. 이벤트

`event` 알림의 `params`는 `{seq, ts, kind, host_id?, …}`이고 아래 필드가 같은 객체에 들어간다.

| `kind` | 필드 |
|---|---|
| `connection.state` | `state`, `connection_generation`, `reason?` |
| `host.added` / `host.updated` | `host`(Host) |
| `host.removed` | — |
| `host.install.progress` | `name`, `step`(`probe`/`copy`/`init`/`install`/`verify`), `status`(`active`/`done`/`failed`), `detail?`, `error?`{`code`, `message`, `next_action?`} |
| `caps.changed` | `caps` |
| `session.changed` | `session`(SessionInfo) |
| `display.changed` | `displays` |
| `control.changed` | ControlState 필드 |
| `timeline.appended` | `entry`(TimelineEntry) |
| `job.updated` | `job`(Job) |
| `transfer.updated` | `transfer`(Transfer — 파일 목록 없음), `changed_files?`([TransferFile] — 앞 이벤트 뒤 `state`·`outcome`·`path`·`error`가 바뀐 파일. 진행 바이트만 바뀐 파일은 넣지 않는다) |
| `events.dropped` | `count`(이 연결이 읽지 못해 버린 이벤트 수). `seq`는 0이다. 받으면 필요한 상태를 `host.list`·`jobs.list` 등으로 다시 읽는다 |

- `timeline.list{host_id, since?, limit?}` 결과: `{entries: [TimelineEntry], next_cursor?}`(최신순).
- `host.doctor` 항목 `id`: `ssh_reachable`, `agent_version`, `quic_connect`, `fingerprint`, `session`, `firewall_rule`(규칙이 꺼져 있거나 호스트 `port`가 아닌 포트를 열면 `warn`).

## 7. 오류와 결과 규칙

- **요청 문제는 JSON-RPC 오류**: 잘못되거나 빠진 파라미터, 모르는 호스트·job·메서드, `permanent:true` 없는 `files.delete`. 읽기 메서드(`files.list/stat/read`, `proc.list`, `jobs.*`, `session.inspect`, `host.*`)의 실패도 JSON-RPC 오류다.
- **원격에서 시도한 동작은 항상 봉투**(§2): 오프라인·차단된 호스트는 `status:"failed"`, `action:{executed:false, outcome:"not_started"}`, `error.code` `OFFLINE`/`AUTH_REQUIRED`. 에이전트가 거절하면 `failed`/`not_started`와 에이전트의 코드. `CAPABILITY_UNSUPPORTED`의 `next_action`은 에이전트가 op를 모를 때만 `host_repair`다. 응답을 잃으면 `status:"unknown"`, `action:{executed:null, outcome:"unknown"}`, `EXECUTION_OUTCOME_UNKNOWN`(또는 `TIMEOUT`), `next_action:"check_state"`.
- `exec.run`의 0이 아닌 종료 코드는 `status:"failed"`, `error:null`, `code`에 종료 코드. 명령 시간 초과는 `failed` + `TIMEOUT`. 실행 중에 PC와의 연결이 끊겨 에이전트가 명령을 끝냈으면(재연결 뒤 `req.status`로 확인) `failed` + `OFFLINE` + `next_action:"check_state"`, `killed:"connection_closed"`, `code:-1`이다. 명령이 일부만 실행됐을 수 있다.
- `host.add`/`host.repair`는 동기 호출이다(진행은 `host.install.progress` 이벤트). 결과는 봉투 + `host`(Host) + `job`(Job, kind `install`). 이미 페어링된 PC에 `force` 없는 `host.add`는 봉투 `failed` + `INVALID_ARGUMENT` + `next_action:"host_add_force"`(오류 문장에 `deskhand host add <이름> --ssh <별칭> --force`)이고, `host.add{force:true}`는 그 PC를 새 에이전트 키로 다시 페어링하며, `host.remove{uninstall:true}`는 PC의 에이전트 키·설정·데이터까지 지운다(`uninstall --purge`). 같은 이름 중복은 JSON-RPC `INVALID_ARGUMENT`.
- `host.connect`/`host.disconnect` 결과는 Host. `host.remove` 결과 `{host_id, removed, uninstalled}`. `host.doctor` 결과 `{host_id, checks}`.
- `pty.open`이 셸을 열지 못하면 봉투 `failed`/`not_started` + `pty_id:null`이다: PC가 `pty`를 지원하지 않거나 그 셸이 없음 `CAPABILITY_UNSUPPORTED`, 오프라인 `OFFLINE`, 에이전트 거절은 그 코드. 셸을 여는 사이 연결이 끊기면 데몬이 다시 연결해 `pty.list`의 `rid`로 확인한다: 열렸으면 `running`, 같은 에이전트 실행에서 없으면 `failed` + `OFFLINE`(`retryable:true`), 확인하지 못하면 `unknown` + `EXECUTION_OUTCOME_UNKNOWN`(`next_action:"check_state"`). 데몬은 셸 시작을 다시 보내지 않는다. PC가 ready가 될 때마다 데몬은 `pty.list`를 한 번 불러 그 PC의 실행 중 pty job 가운데 끝난 셸은 종료 코드로, 목록에 없는 셸은 `unknown`(`result.state:"lost"`)으로 끝낸다.
- `jobs.list` → `{jobs}`, `jobs.get` → Job, `jobs.cancel` → 봉투(`cancelled`) + `job`(pty job은 셸이 이미 끝났으면 봉투 `succeeded`), `jobs.output{job_id, cursor?}` → `{chunks:[{stream:"stdout"|"stderr", data}], next_cursor, state, status, code?, dropped?, timed_out?, error?}`(`timed_out`·`error`는 끝난 job의 결과에 있을 때 `"end"` 쪽에 붙는다) (`next_cursor`는 `"<out>:<err>"` 바이트 위치. 프로세스가 끝나고 출력을 모두 돌려준 뒤에만 `"end"`다. 프로세스가 끝난 뒤에도 출력이 남아 있으면 `state:"exited"`인 채로 바이트 위치를 계속 주므로, 클라이언트는 `"end"`가 나올 때까지 읽는다. exec job이 아닌 job은 끝나면 출력 없이 `"end"`).
- 데몬이 시작할 때 끝나지 않았고 원격 job id가 없는 job(진행 중이던 `exec.run`, 응답 전의 `exec.start`, `install`)은 `status:"unknown"`(`result.state:"interrupted"`, `error.code` `EXECUTION_OUTCOME_UNKNOWN`, `next_action:"check_state"`)으로 끝내고 `job.updated`와 행위자 `system`의 타임라인 항목을 남긴다. 호스트가 ready가 될 때마다 데몬은 `exec.list`를 한 번 불러 그 호스트의 실행 중 exec job 가운데 에이전트가 `exited`로 보고한 것은 종료 코드로, `lost`이거나 목록에 없는 것은 `unknown`(`result.state:"lost"`)으로 끝낸다.
- `timeline.list`는 `cursor`(이전 결과의 `next_cursor`, seq)도 받는다. `since`는 RFC 3339. 행위자: `mcp` 클라이언트 → `ai`, `app`/`cli` → `human`, 데몬 자신 → `system`.
- Host에는 준비되지 않은 이유 `reason?`이 있다. `hello` 결과 `{client_id, daemon_version, api_version}`, `events.subscribe` 결과 `{subscribed, host_id, kinds}`.
- 봉투의 `session_id`는 알면 `s-<host_id>-<session_id>`, 모르면 null.

## 8. 파일·전송 세부 계약

- `files.list{host_id, path, cursor?, limit?}` → `{entries:[FileEntry], next_cursor?, total?, truncated?}`. `limit` 기본 500·최대 5000, `cursor`는 이전 결과의 `next_cursor`(불투명 문자열). 에이전트 한 번 목록이 5,000개를 넘으면 `truncated:true`. 항목은 폴더가 먼저, 그다음 이름(대소문자 무시) 순이고, `total`은 에이전트가 돌려준 전체 항목 수(항상 있다), `next_cursor`는 다음 페이지가 있을 때만 있다. `limit`은 0(또는 생략)이면 기본값 500, 5000 초과·음수나 읽을 수 없는 `cursor`는 JSON-RPC `INVALID_ARGUMENT`다. 데몬은 페이지마다 에이전트에 목록을 다시 요청해 `cursor` 뒤의 항목을 고른다: 폴더가 바뀌어도 `cursor` 뒤에 남은 항목은 중복도 누락도 없이 나오고, `cursor` 앞에 새로 생긴 항목은 보이지 않으며, `total`은 호출마다 달라질 수 있다. `truncated:true`이면 에이전트가 이름순(바이트 순서)으로 앞의 5,000개만 돌려준 것이라 페이지는 그 부분집합 안에서만 이어진다.
- `files.stat{host_id, path}` → FileEntry, 없으면 JSON-RPC `NOT_FOUND`.
- `files.mkdir{host_id, path}`, `files.rename{host_id, from, to}`(전체 경로), `files.copy{host_id, from, to, on_conflict, recursive}`, `files.trash{host_id, path}`, `files.delete{host_id, path, permanent:true}`, `files.hash{host_id, path, offset?, length?}` → 모두 §2 봉투(`files.hash`는 봉투 + `sha256`·`offset`·`length`·`size`).
- `transfer.start{direction, host_id, local, remote, on_conflict}`: `local`·`remote`는 옮길 항목의 전체 경로(대상 폴더가 아님). 결과는 봉투 + `transfer_id`이고 봉투 `job_id`는 그 전송을 따르는 job(§5 Job `kind:"transfer"`)이다. 그 job의 `jobs.cancel`은 `transfer.cancel`과 같다. 폴더는 `on_conflict`를 폴더 안 파일마다 적용한다. `remote`에 `.`이나 `..` 원소가 있으면 JSON-RPC `INVALID_ARGUMENT`다.
- `transfer.list{host_id?}` → `{transfers:[Transfer]}`(최신순, 파일 목록 없이 `files_total`·`files_done`·`files_failed`·`bytes_total`·`bytes_done`). `transfer.get{transfer_id}` → TransferDetail(파일 목록 포함). 앱은 목록과 이벤트로 합계를 보이고, 파일 목록은 `transfer.get`으로 읽은 뒤 `transfer.updated`의 `changed_files`를 `rel`로 맞춰 반영한다.
- `transfer.pause{transfer_id}` → TransferDetail(`paused`). `transfer.resume{transfer_id}`는 `paused`·`failed`·`cancelled` 모두 다시 시작한다 → TransferDetail. `transfer.cancel{transfer_id}` → TransferDetail(`cancelled`, 끝난 파일은 남긴다).
- `Transfer.rate_bps`와 진행 값은 **초당 바이트**다(비트 아님).
- `app.launch{host_id, path, args?, cwd?, wait_window_ms?}` → 봉투 + `pid`, `window?`.
- `Transfer.error`·`TransferFile.error`는 `{code, message}` 객체다(§2 error 객체와 같은 형태, `retryable`·`next_action`은 선택).
- `transfer.start`는 원본이 없거나, 대상 폴더(push는 `remote`의 폴더, pull은 `local`의 폴더)가 없거나, PC가 응답하지 않으면 전송을 만들지 않고 봉투 `failed`/`not_started` + `transfer_id:null`이다. 시작하면 봉투 `status:"running"`이고 전송은 `queued`에서 시작한다. 링크인 원본은 `INVALID_ARGUMENT`다.
- `queued`는 PC 연결을 기다리는 상태다(전송 중에 연결이 끊겨도 `queued`로 돌아가 다시 연결되면 받은 바이트부터 잇는다). 데몬이 다시 시작하면 `queued`·`running`·`verifying` 전송은 PC가 연결될 때 이어지고, `paused` 전송은 `transfer.resume`까지 그대로다. 전송의 `verifying`은 이동의 원본 정리 중, 파일의 `verifying`은 검증·반영 중이다.
- `transfer.updated`는 파일 진행·파일 상태가 바뀌면 전송마다 250 ms에 많아야 한 번, 전송 상태가 바뀌면 바로 나간다.
- 원본 지문은 계획 때 잡은 크기·수정 시각(push는 sha256도)이다. 전송 중에 원본이 바뀌면 그 파일만 `TRANSFER_CONFLICT`로 실패하고 대상은 그대로다. `transfer.resume`은 실패한 파일의 지문을 다시 잡는다.
- 전송 `outcome`: 파일이 모두 같은 결과면 그 값, 섞이면 `written`. 일부 파일이 실패하면 `state:"failed"` + `partial`, 모두 실패하면 `failed`. `transfer.resume`은 끝난 파일을 다시 보내지 않는다.
- 이동(`move_*`)은 모든 파일이 반영·검증된 뒤에만 원본을 지운다: PC의 원본은 휴지통(`fs.trash`)으로, 이 Mac의 원본은 지운다. 폴더는 원본 폴더의 항목이 계획 때와 같을 때만 통째로 지운다. 원본을 남기면(삭제 실패, 바뀌거나 새로 생긴 항목, 건너뛴 파일) `state:"done"` + `outcome:"copied_source_retained"` + `error`(남긴 이유)다.
- 이동의 원본 행방은 전송 상태에서 정해진다: `done`이고 `outcome`이 `copied_source_retained`가 아니면 원본이 정리됐고(`move_pull` 휴지통, `move_push` 삭제), 그 밖의 끝난 상태(`failed`·`cancelled`·`copied_source_retained`)는 원본이 남아 있다. CLI `--json`과 MCP 구조화 결과는 이를 `source`(`trashed`/`deleted`/`retained`, 진행 중은 `pending`, 이동이 아니면 없음)로 덧붙인다. 데몬 응답에는 이 필드가 없다.
- 취소하면 끝난 파일은 남고 나머지 파일은 `cancelled`이며 임시 파일은 지운다(PC가 연결돼 있지 않으면 다음 연결 때). `transfer.pause`는 끝난 전송에, `transfer.resume`·`transfer.cancel`은 `done` 전송에 JSON-RPC `INVALID_ARGUMENT`이고(멈추는 사이에 그렇게 끝난 전송도 같다), 이미 그 상태인 전송은 그대로 돌려준다. 이동이 받은 쪽 검증을 마치고 원본을 정리하기 시작하면 `transfer.pause`·`transfer.cancel`은 `INVALID_ARGUMENT`("원본 정리 중")이고 그 이동은 끝까지 간다.
- 끝난(`done`·`failed`·`cancelled`) 지 30일이 지났고 PC에 임시 파일이 남지 않은 전송은 데몬이 시작할 때와 24시간마다 파일 기록·job과 함께 지운다.
- pull은 이 Mac의 `<대상>.deskhand-part-<transfer_id>`(숨김 플래그)에 받아 PC가 계산한 sha256과 맞춘 뒤 이름을 바꾼다. 충돌 정책과 `이름 (1).확장자` 규칙은 PC 쪽(protocol.md §3.1)과 같다. 옮기는 메타데이터는 내용과 수정 시각뿐이다. 폴더 안의 링크·특수 파일은 옮기지 않고 그 항목만 `INVALID_ARGUMENT`로 실패한다. 폴더 안 이름에 빈 원소·`.`·`..`·`\`가 있거나, Windows로 보낼 때 Windows가 받지 못하는 이름(`< > : " | ? *`·제어 문자, 점이나 공백으로 끝남, `CON`·`NUL`·`COM1` 같은 장치 이름 — 확장자가 붙어도)이면 그 항목만 `INVALID_ARGUMENT`이고, Windows로 보낼 때와 대소문자를 가리지 않는 Mac 볼륨으로 받을 때 대소문자만 다른 두 이름은 뒤의 것이 `TRANSFER_CONFLICT`다. PC가 읽지 못한 항목이 있는 폴더의 pull은 `PERMISSION_DENIED`로 실패한다.
