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
| `pty.open{host_id, shell, cols, rows}` → `pty_id` | 이후 `stream.attach{kind:"pty"}` |

### 파일
| 메서드 | 결과 |
|---|---|
| `files.list{host_id, path, recursive?}` | `{host_id, path(실제 경로), entries:[FileEntry], truncated, skipped?}` |
| `files.stat{host_id, path, sha256?}` | `{host_id, exists, path, name, dir, size, link, mtime?, owner?, sha256?, locked?}` — 없는 경로는 `exists:false` |
| `files.read{host_id, path, offset, length}` | `{host_id, path, data(base64), size(파일 전체), eof, encoding?}` |
| `files.write` | 봉투 + `size` |
| `files.copy{host_id, from, to, on_conflict?, recursive?}` | 봉투 + `path`(복사본의 실제 경로), `copied`(파일 수), `outcome`(`written`/`skipped`/`renamed`). `on_conflict`는 `fail`(기본)/`replace`/`skip`/`rename`, 규칙은 [protocol.md §3.1](protocol.md#31-op-목록) `fs.copy` |
| `files.trash{host_id, path}` | 봉투 + `trashed`(없는 경로면 false). 휴지통이 받지 않는 항목은 옮기지 않고 `failed` + `CAPABILITY_UNSUPPORTED`(영구 삭제로 대신하지 않는다. 영구 삭제는 `files.delete{permanent:true}`). 셸이 성공을 보고했는데 휴지통에 항목이 없으면 `failed` + `INTERNAL` |
| `files.hash{host_id, path, offset?, length?}` | 봉투 + `sha256`, `offset`, `length`(읽은 바이트 수), `size`(파일 크기). `length` 0이면 끝까지 |
| `files.move` / `files.rename` / `files.mkdir` / `files.delete{permanent:true}` | 봉투 |
| `transfer.start{direction:"push"|"pull"|"move_push"|"move_pull", host_id, local, remote, on_conflict}` | `transfer_id` |
| `transfer.list` / `transfer.get{transfer_id}` | 파일별 상태, 진행률, `outcome` |
| `transfer.cancel` / `transfer.resume` | 상태 |

### 진단
`diag.system`, `diag.services`, `diag.events`, `diag.crashes`, `diag.processes` — 에이전트 op 래핑 + artifact 저장.

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

`kind:"pty"` (`pty_id`): `0x20`–`0x23` (protocol.md §7과 동일).

## 5. 모델

모든 JSON 키는 snake_case. 시각은 RFC 3339 UTC 밀리초(`2026-10-20T12:34:56.789Z`). 표시 안 한 필드는 항상 있다; `?`는 없을 수 있다.

| 모델 | 필드 |
|---|---|
| Host | `host_id`, `name`, `os`, `arch`, `address`, `port`, `ssh_alias`, `state`(연결 상태), `caps`(기능 이름 → Capability), `session?`(SessionInfo), `displays`([DisplayInfo]), `owner`(`none`/`human`/`ai`), `owner_client?`, `agent_version?`, `connection_generation`, `last_seen?` |
| Capability | `state`(`ready`/`unavailable`/`unsupported`/`needs_user`), `reason?` |
| SessionInfo | `session_id`(정수), `user`, `sid?`, `connect_state`(`active`/`connected`/`disconnected`/`other`/`unknown`), `protocol`(`console`/`rdp`/`unknown`), `locked?`(에이전트가 잠금 상태를 보고하지 않으면 없음), `input_desktop`(`Default`/`Winlogon`/`unknown`), `integrity?` |
| DisplayInfo | `index`, `name`, `adapter`, `x`, `y`, `width`, `height`, `dpi`(정수), `scale`(실수), `primary`, `refresh_hz`(정수) |
| ControlState | `owner`, `owner_client?`, `gen`(호스트별 제어권 상태가 바뀔 때마다 — owner 또는 ai_paused — 1씩 오르는 값. 클라이언트는 더 큰 gen만 반영한다), `ai_paused` |
| SessionInspect (`session.inspect` 결과) | SessionInfo의 모든 필드 + ControlState의 모든 필드 + `displays`([DisplayInfo]) + `caps` + `connection_generation` — 한 단계 평평한 객체 |
| Job | `job_id`, `host_id`, `kind`(`exec`/`transfer`/`qa`/`install`/`debug`/`pty`), `status`(봉투 status 값), `request_id?`, `summary`, `progress?`(0–1), `created_at`, `updated_at`, `finished_at?`, `result?`(객체, 명령 출력 `stdout`·`stderr`는 넣지 않는다), `output_bytes?`(데몬이 기록한 stdout·stderr 바이트 수. 출력은 `jobs.output`과 artifact로만 읽는다) |
| FileEntry | `name`, `path`, `dir`, `size`, `mtime`, `link`, `hidden?`, `readonly?` |
| Transfer | `transfer_id`, `host_id`, `direction`(`push`/`pull`/`move_push`/`move_pull`), `local`, `remote`, `on_conflict`, `state`(`queued`/`running`/`paused`/`verifying`/`done`/`failed`/`cancelled`), `bytes_done`, `bytes_total`, `files_done`, `files_total`, `rate_bps`, `outcome?`(`written`/`skipped`/`renamed`/`copied_source_retained`/`partial`/`failed`), `error?`, `files`([TransferFile]), `rev`(전송마다 바뀔 때마다 1씩 오르는 정수 — 클라이언트는 더 큰 rev만 반영), `updated_at` |
| TransferFile | `rel`, `size`, `bytes_done`, `state`, `outcome?`, `sha256?`, `error?` |
| ProcessInfo | `pid`, `ppid`, `name`, `path?`, `cmd?`, `mem_kb`, `cpu_pct?`, `user?`, `started_by_deskhand` |
| DiagSystem | `os`, `version`, `build`, `arch`, `hostname`, `cpu`{`name`, `cores`, `threads`, `load_pct`}, `memory`{`total_bytes`, `available_bytes`}, `disks`[{`name`, `total_bytes`, `free_bytes`}], `gpus`[{`name`, `driver`}], `uptime_s`, `boot_time` |
| EventLogEntry | `time`, `log`, `level`(`critical`/`error`/`warning`/`info`/`verbose`), `provider`, `event_id`, `message` |
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
| `transfer.updated` | `transfer`(Transfer) |
| `events.dropped` | `count`(이 연결이 읽지 못해 버린 이벤트 수). `seq`는 0이다. 받으면 필요한 상태를 `host.list`·`jobs.list` 등으로 다시 읽는다 |

- `timeline.list{host_id, since?, limit?}` 결과: `{entries: [TimelineEntry], next_cursor?}`(최신순).
- `host.doctor` 항목 `id`: `ssh_reachable`, `agent_version`, `quic_connect`, `fingerprint`, `session`, `firewall_rule`(규칙이 꺼져 있거나 호스트 `port`가 아닌 포트를 열면 `warn`).

## 7. 오류와 결과 규칙

- **요청 문제는 JSON-RPC 오류**: 잘못되거나 빠진 파라미터, 모르는 호스트·job·메서드, `permanent:true` 없는 `files.delete`. 읽기 메서드(`files.list/stat/read`, `proc.list`, `jobs.*`, `session.inspect`, `host.*`)의 실패도 JSON-RPC 오류다.
- **원격에서 시도한 동작은 항상 봉투**(§2): 오프라인·차단된 호스트는 `status:"failed"`, `action:{executed:false, outcome:"not_started"}`, `error.code` `OFFLINE`/`AUTH_REQUIRED`. 에이전트가 거절하면 `failed`/`not_started`와 에이전트의 코드. `CAPABILITY_UNSUPPORTED`의 `next_action`은 에이전트가 op를 모를 때만 `host_repair`다. 응답을 잃으면 `status:"unknown"`, `action:{executed:null, outcome:"unknown"}`, `EXECUTION_OUTCOME_UNKNOWN`(또는 `TIMEOUT`), `next_action:"check_state"`.
- `exec.run`의 0이 아닌 종료 코드는 `status:"failed"`, `error:null`, `code`에 종료 코드. 명령 시간 초과는 `failed` + `TIMEOUT`. 실행 중에 PC와의 연결이 끊겨 에이전트가 명령을 끝냈으면(재연결 뒤 `req.status`로 확인) `failed` + `OFFLINE` + `next_action:"check_state"`, `killed:"connection_closed"`, `code:-1`이다. 명령이 일부만 실행됐을 수 있다.
- `host.add`/`host.repair`는 동기 호출이다(진행은 `host.install.progress` 이벤트). 결과는 봉투 + `host`(Host) + `job`(Job, kind `install`). 이미 페어링된 PC에 `force` 없는 `host.add`는 봉투 `failed` + `INVALID_ARGUMENT` + `next_action:"host_add_force"`(오류 문장에 `deskhand host add <이름> --ssh <별칭> --force`)이고, `host.add{force:true}`는 그 PC를 새 에이전트 키로 다시 페어링하며, `host.remove{uninstall:true}`는 PC의 에이전트 키·설정·데이터까지 지운다(`uninstall --purge`). 같은 이름 중복은 JSON-RPC `INVALID_ARGUMENT`.
- `host.connect`/`host.disconnect` 결과는 Host. `host.remove` 결과 `{host_id, removed, uninstalled}`. `host.doctor` 결과 `{host_id, checks}`.
- `jobs.list` → `{jobs}`, `jobs.get` → Job, `jobs.cancel` → 봉투(`cancelled`) + `job`, `jobs.output{job_id, cursor?}` → `{chunks:[{stream:"stdout"|"stderr", data}], next_cursor, state, status, code?, dropped?, timed_out?, error?}`(`timed_out`·`error`는 끝난 job의 결과에 있을 때 `"end"` 쪽에 붙는다) (`next_cursor`는 `"<out>:<err>"` 바이트 위치. 프로세스가 끝나고 출력을 모두 돌려준 뒤에만 `"end"`다. 프로세스가 끝난 뒤에도 출력이 남아 있으면 `state:"exited"`인 채로 바이트 위치를 계속 주므로, 클라이언트는 `"end"`가 나올 때까지 읽는다. exec job이 아닌 job은 끝나면 출력 없이 `"end"`).
- 데몬이 시작할 때 끝나지 않았고 원격 job id가 없는 job(진행 중이던 `exec.run`, 응답 전의 `exec.start`, `install`)은 `status:"unknown"`(`result.state:"interrupted"`, `error.code` `EXECUTION_OUTCOME_UNKNOWN`, `next_action:"check_state"`)으로 끝내고 `job.updated`와 행위자 `system`의 타임라인 항목을 남긴다. 호스트가 ready가 될 때마다 데몬은 `exec.list`를 한 번 불러 그 호스트의 실행 중 exec job 가운데 에이전트가 `exited`로 보고한 것은 종료 코드로, `lost`이거나 목록에 없는 것은 `unknown`(`result.state:"lost"`)으로 끝낸다.
- `timeline.list`는 `cursor`(이전 결과의 `next_cursor`, seq)도 받는다. `since`는 RFC 3339. 행위자: `mcp` 클라이언트 → `ai`, `app`/`cli` → `human`, 데몬 자신 → `system`.
- Host에는 준비되지 않은 이유 `reason?`이 있다. `hello` 결과 `{client_id, daemon_version, api_version}`, `events.subscribe` 결과 `{subscribed, host_id, kinds}`.
- 봉투의 `session_id`는 알면 `s-<host_id>-<session_id>`, 모르면 null.

## 8. 파일·전송 세부 계약

- `files.list{host_id, path, cursor?, limit?}` → `{entries:[FileEntry], next_cursor?, total?, truncated?}`. `limit` 기본 500·최대 5000, `cursor`는 이전 결과의 `next_cursor`(불투명 문자열). 에이전트 한 번 목록이 5,000개를 넘으면 `truncated:true`.
- `files.stat{host_id, path}` → FileEntry, 없으면 JSON-RPC `NOT_FOUND`.
- `files.mkdir{host_id, path}`, `files.rename{host_id, from, to}`(전체 경로), `files.copy{host_id, from, to, on_conflict, recursive}`, `files.trash{host_id, path}`, `files.delete{host_id, path, permanent:true}`, `files.hash{host_id, path, offset?, length?}` → 모두 §2 봉투(`files.hash`는 봉투 + `sha256`·`offset`·`length`·`size`).
- `transfer.start{direction, host_id, local, remote, on_conflict}`: `local`·`remote`는 옮길 항목의 전체 경로(대상 폴더가 아님). 결과는 봉투 + `transfer_id`. 폴더는 `on_conflict`를 폴더 안 파일마다 적용한다.
- `transfer.list{host_id?}` → `{transfers:[Transfer]}`(최신순). `transfer.get{transfer_id}` → Transfer.
- `transfer.pause{transfer_id}` → Transfer(`paused`). `transfer.resume{transfer_id}`는 `paused`·`failed`·`cancelled` 모두 다시 시작한다 → Transfer. `transfer.cancel{transfer_id}` → Transfer(`cancelled`, 끝난 파일은 남긴다).
- `Transfer.rate_bps`와 진행 값은 **초당 바이트**다(비트 아님).
- `app.launch{host_id, path, args?, cwd?, wait_window_ms?}` → 봉투 + `pid`, `window?`.
- `Transfer.error`·`TransferFile.error`는 `{code, message}` 객체다(§2 error 객체와 같은 형태, `retryable`·`next_action`은 선택).
