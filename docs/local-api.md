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

## 3. 메서드

### 호스트·연결
| 메서드 | 파라미터 | 결과 |
|---|---|---|
| `host.list` | — | `hosts`: [{`host_id`, `name`, `os`, `arch`, `address`, `state`(연결 상태), `caps`, `session`, `owner`, `last_seen`}] |
| `host.add` | `name`, `ssh`(별칭), `address`(선택), `port` | 설치·페어링 job(진행 이벤트 `host.install.progress`) |
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
| `exec.run{host_id, argv|cmd, shell, cwd, env, stdin, timeout_ms}` | 봉투 + `code`, `stdout`, `stderr` (큰 출력은 artifact) |
| `exec.start{…}` | 봉투(job) |
| `jobs.list{host_id?, kind?, status?}` / `jobs.get{job_id}` / `jobs.cancel{job_id}` | job 목록·상세 |
| `jobs.output{job_id, cursor}` | `chunks`, `next_cursor`, `state` |
| `proc.list` / `proc.kill` / `app.launch` | 에이전트 op와 동일 + 봉투 |
| `pty.open{host_id, shell, cols, rows}` | 봉투(job, kind `pty`) + `pty_id`. `shell`은 `powershell`/`cmd`/`pwsh`(`caps.pty.shells`에 있는 것). 이후 `stream.attach{kind:"pty", pty_id}`. 셸 목록은 `jobs.list{host_id, kind:"pty", status:"running"}`, 끝내기는 `jobs.cancel{job_id}`(봉투 `cancelled`; 셸이 먼저 끝났으면 `succeeded`). pty job의 `result`는 `{shell, pid, client_name?}` |

### 파일
| 메서드 | 결과 |
|---|---|
| `files.list{host_id, path}` / `files.stat` / `files.read{path, offset, length}` / `files.write` | 원격 파일 작업 |
| `files.copy` / `files.move` / `files.rename` / `files.mkdir` / `files.trash` / `files.delete{permanent:true}` | 봉투 |
| `transfer.start{direction:"push"|"pull"|"move_push"|"move_pull", host_id, local, remote, on_conflict}` | `transfer_id` |
| `transfer.list` / `transfer.get{transfer_id}` | 파일별 상태, 진행률, `outcome` |
| `transfer.cancel` / `transfer.resume` | 상태 |

### 진단
`diag.system{host_id}`, `diag.services{host_id, name?}`, `diag.events{host_id, …}`, `diag.crashes{host_id, since?}` — 에이전트 op(protocol.md §3.1)와 같은 파라미터·결과 + 봉투, `save:true`면 결과 JSON을 artifact로 저장해 `artifact_id`를 붙인다. 크래시 덤프 받기는 `transfer.start{direction:"pull"}`. 프로세스 표는 `proc.list{host_id, name?}` → `procs`(protocol.md §3.1의 확장 필드 포함), 종료는 `proc.kill{host_id, pid, started?, tree?}` → 봉투 + `killed`.

### 디버깅
Chrome DevTools Protocol(CDP) 디버거. 세부 규칙은 [§9](#9-디버깅-세부-계약).

| 메서드 | 결과 |
|---|---|
| `tunnel.open{host_id, remote_port}` → `local_port` / `tunnel.close{local_port}` / `tunnel.list{host_id?}` | |
| `debug.list{host_id?}` | `{sessions:[DebugSession]}`(만든 순) |
| `debug.get{debug_id}` | DebugSession |
| `debug.electron.launch{host_id, path, args?, cwd?, pause_on_start?, breakpoints?, pause_on_exceptions?}` | 봉투(job, kind `debug`) + `session`(DebugSession). `breakpoints`는 [BreakpointSpec]이고 앱이 코드를 실행하기 전에 건다 |
| `debug.attach{host_id, port? \| pid?, role?, breakpoints?, pause_on_exceptions?}` | 봉투(job, kind `debug`) + `session`(DebugSession) |
| `debug.target.attach{debug_id, target_id}` | 봉투 + `target`(DebugTarget) |
| `debug.breakpoint.set{debug_id, match, pattern, line, column?, condition?}` | 봉투 + `breakpoint`(Breakpoint) |
| `debug.breakpoint.update{debug_id, breakpoint_id, enabled?, condition?}` | 봉투 + `breakpoint`(Breakpoint). `condition:""`는 조건을 없앤다 |
| `debug.breakpoint.remove{debug_id, breakpoint_id}` | 봉투 + `breakpoint`(지운 뒤의 `rev`를 실은 Breakpoint) |
| `debug.breakpoints.activate{debug_id, active}` | 봉투 + `session`(DebugSession) |
| `debug.exceptions{debug_id, mode}` | 봉투 + `session`(DebugSession) |
| `debug.pause{debug_id, target_id}` / `debug.resume{debug_id, target_id}` / `debug.step{debug_id, target_id, kind}` | 봉투 + `target`(응답 시점의 DebugTarget) |
| `debug.state{debug_id, target_id}` | PauseState |
| `debug.variables{debug_id, target_id, object_id, offset?, limit?}` | `{properties:[Property], total}` |
| `debug.evaluate{debug_id, target_id, expression, frame_id?}` | 봉투 + RemoteValue 필드(`type`, `subtype?`, `class_name?`, `value_preview`, `object_id?`, `has_children`) + `exception?`(ExceptionInfo) |
| `debug.scripts{debug_id, target_id?}` | `{scripts:[DebugScript]}`(URL이 있는 로드된 스크립트, 로드 순) |
| `debug.source{debug_id, target_id, script_id}` | `{script_id, url, text, truncated}` |
| `debug.console{debug_id, cursor? \| before?, limit?}` | `{entries:[ConsoleEntry], next_cursor, first_seq, has_more, has_earlier, gap, dropped}` |
| `debug.console.clear{debug_id}` | 봉투 + `cleared_through`(지운 마지막 `seq`) |
| `debug.close{debug_id, keep_process?}` | 봉투 + `session`(DebugSession, `state:"closed"`) + `inspector_left_open?` |

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

`kind:"pty"` (`pty_id` 또는 `job_id`): `0x20`–`0x23` (protocol.md §7과 동일). `pty_id`는 방금 연 셸, `job_id`는 실행 중인 pty job에 다시 붙는다(분리했거나 연결이 끊긴 셸). 붙으면 데몬이 `0x23`{`job`(데몬 job_id), `pid`}을 보내고 이어서 스크롤백을 `0x20`으로 다시 보낸다. 앱은 `0x23`을 받을 때마다 현재 크기를 `0x21`로 보낸다. 끝난 job에 붙으면 `NOT_FOUND`.

## 5. 모델

모든 JSON 키는 snake_case. 시각은 RFC 3339 UTC 밀리초(`2026-10-20T12:34:56.789Z`). 표시 안 한 필드는 항상 있다; `?`는 없을 수 있다.

| 모델 | 필드 |
|---|---|
| Host | `host_id`, `name`, `os`, `arch`, `address`, `port`, `ssh_alias`, `state`(연결 상태), `caps`(기능 이름 → Capability), `session?`(SessionInfo), `displays`([DisplayInfo]), `owner`(`none`/`human`/`ai`), `owner_client?`, `agent_version?`, `connection_generation`, `last_seen?` |
| Capability | `state`(`ready`/`unavailable`/`unsupported`/`needs_user`), `reason?`, `shells?`(`pty` 기능만: 띄울 수 있는 셸 `powershell`/`cmd`/`pwsh`) |
| SessionInfo | `session_id`(정수), `user`, `sid?`, `connect_state`(`active`/`connected`/`disconnected`/`other`/`unknown`), `protocol`(`console`/`rdp`/`unknown`), `locked?`(에이전트가 잠금 상태를 보고하지 않으면 없음), `input_desktop`(`Default`/`Winlogon`/`unknown`), `integrity?` |
| DisplayInfo | `index`, `name`, `adapter`, `x`, `y`, `width`, `height`, `dpi`(정수), `scale`(실수), `primary`, `refresh_hz`(정수) |
| ControlState | `owner`, `owner_client?`, `gen`(호스트별 제어권 상태가 바뀔 때마다 — owner 또는 ai_paused — 1씩 오르는 값. 클라이언트는 더 큰 gen만 반영한다), `ai_paused` |
| SessionInspect (`session.inspect` 결과) | SessionInfo의 모든 필드 + ControlState의 모든 필드 + `displays`([DisplayInfo]) + `caps` + `connection_generation` — 한 단계 평평한 객체 |
| Job | `job_id`, `host_id`, `kind`(`exec`/`transfer`/`qa`/`install`/`debug`/`pty`), `status`(봉투 status 값), `request_id?`, `summary`, `progress?`(0–1), `created_at`, `updated_at`, `finished_at?`, `result?`(객체) |
| FileEntry | `name`, `path`, `dir`, `size`, `mtime`, `link`, `hidden?`, `readonly?` |
| Transfer | `transfer_id`, `host_id`, `direction`(`push`/`pull`/`move_push`/`move_pull`), `local`, `remote`, `on_conflict`, `state`(`queued`/`running`/`paused`/`verifying`/`done`/`failed`/`cancelled`), `bytes_done`, `bytes_total`, `files_done`, `files_total`, `rate_bps`, `outcome?`(`written`/`skipped`/`renamed`/`copied_source_retained`/`partial`/`failed`), `error?`, `files`([TransferFile]), `rev`(전송마다 바뀔 때마다 1씩 오르는 정수 — 클라이언트는 더 큰 rev만 반영), `updated_at` |
| TransferFile | `rel`, `size`, `bytes_done`, `state`, `outcome?`, `sha256?`, `error?` |
| ProcessInfo | protocol.md §3.1 `proc.list` 항목과 같다(`pid`, `ppid`, `name`, `path`, `cmd`, `mem_kb`, `cpu_pct`, `user?`, `started`, `started_by_deskhand`, `self`) |
| DiagSystem | protocol.md §3.1 `diag.system` 결과와 같다 |
| EventLogEntry | protocol.md §3.1 `diag.events`의 `events` 항목과 같다(`record_id`, `time`, `level`, `provider`, `event_id`, `message`) |
| DebugSession | `debug_id`, `host_id`, `job_id?`, `kind`(`launch`/`attach`), `name`(앱 이름: launch는 실행 파일 이름에서 `.exe`를 뗀 것, attach는 프로세스 이름 또는 `/json/version`의 `Browser`), `path?`, `args`([문자열]), `cwd?`, `pause_on_start`, `pid?`, `ports`{`inspect?`, `remote_debugging?`}(원격 PC의 루프백 포트), `state`(`running`/`paused`/`closed`: 대상 하나라도 멈췄으면 `paused`), `targets`([DebugTarget], main 먼저), `main_unavailable_reason?`(`inspector_disabled`/`inspector_not_responding`: main 없이 renderer만 붙은 세션), `breakpoints`([Breakpoint], 만든 순), `breakpoints_active`, `pause_on_exceptions`(`none`/`uncaught`/`all`), `created_by`(Actor), `created_at`, `closed_at?`, `close_reason?`(`requested`/`process_exited`/`connection_lost`), `exit_code?`, `rev` |
| Actor | `actor`(`human`/`ai`/`system`), `client_name?` — 요청한 클라이언트(§7 행위자 규칙) |
| DebugTarget | `id`(CDP `targetId`), `role`(`main`/`renderer`), `type`(CDP 대상 종류: `node`/`page`/`iframe`/`webview`/`worker`/`service_worker`/`shared_worker`/`other`), `url`, `title`, `attached`, `paused`, `pause_pending`(`debug.pause`를 받았지만 아직 실행할 코드가 없어 멈추지 않음), `gen`(대상별로 일시정지·재개 때마다 1씩 오르는 정수) |
| BreakpointSpec | `match`(`file_name`/`url`/`url_regex`), `pattern`, `line`, `column?`, `condition?`, `enabled?`(기본 true) |
| Breakpoint | `breakpoint_id`, BreakpointSpec의 모든 필드(`enabled`는 항상 있다), `resolved`(`locations`가 비어 있지 않음), `locations`([BreakpointLocation]), `rev` |
| BreakpointLocation | `target_id`, `script_id`, `url`, `line`, `column` |
| PauseState | `target_id`, `gen`, `paused`, `reason?`(`breakpoint`/`exception`/`promise_rejection`/`pause_request`/`step`/`start`/`other`), `requested_by?`(Actor: `pause_request`·`step`일 때 요청한 클라이언트), `hit_breakpoints`([`breakpoint_id`]), `call_frames`([CallFrame], 가장 안쪽 먼저), `exception?`(RemoteValue: `reason`이 `exception`·`promise_rejection`일 때 던져진 값). 멈추지 않았으면 `paused:false`, `call_frames:[]` |
| CallFrame | `frame_id`(CDP `callFrameId`), `function`(익명 함수는 `""`), `script_id`, `url`, `line`, `column`, `scope_chain`([Scope], 안쪽 먼저), `this?`(RemoteValue) |
| Scope | `type`(`local`/`closure`/`block`/`catch`/`with`/`script`/`module`/`eval`/`global`/`wasm_expression_stack`), `object_id`, `name?`(클로저 함수 이름 등) |
| RemoteValue | `type`(`object`/`function`/`undefined`/`string`/`number`/`boolean`/`symbol`/`bigint`), `subtype?`(CDP `subtype`: `array`/`null`/`regexp`/`date`/`map`/`set`/`error`/`promise`/`typedarray`/`node`/`proxy`/…), `class_name?`, `value_preview`, `object_id?`(펼쳐 읽을 수 있는 개체만), `has_children` |
| Property | RemoteValue의 모든 필드 + `name`, `kind`(`property`/`accessor`/`internal`), `writable?` |
| ExceptionInfo | `text`(한 줄 설명, 예: `TypeError: Cannot read properties of undefined (reading 'total')`), `script_id?`, `url?`, `line?`, `column?`, `stack`([StackFrame]) |
| StackFrame | `function`(익명 함수는 `""`), `script_id?`, `url`, `line`, `column` |
| DebugScript | `script_id`, `target_id`, `url` |
| ConsoleEntry | `seq`(세션 안에서 1부터 오르는 정수), `time`, `target_id`, `source`(`console`/`exception`/`browser`), `level`(`debug`/`info`/`log`/`warning`/`error`), `text`, `script_id?`, `url?`, `line?`, `column?`, `stack?`([StackFrame]) |
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
| `debug.session_changed` | `debug_id`, `session`(DebugSession) — 세션이 생겼을 때, `state`·`breakpoints_active`·`pause_on_exceptions`·`main_unavailable_reason`이 바뀌었을 때 |
| `debug.target_changed` | `debug_id`, `target`(DebugTarget), `removed`(bool), `rev`(바뀐 뒤 세션 `rev`) — 대상이 생기거나 바뀌거나(`title`·`url`·`attached`·`pause_pending`) 사라졌을 때 |
| `debug.breakpoint_changed` | `debug_id`, `breakpoint`(Breakpoint), `removed`(bool), `error?`{`code`, `message`} — 만들기·바꾸기·위치 확정(`Debugger.breakpointResolved`)·지우기, 처음 붙는 대상이 거부해 지운 경우 `error` |
| `debug.paused` | `debug_id`, `state`(PauseState, `paused:true`) |
| `debug.resumed` | `debug_id`, `target_id`, `gen`, `by?`(Actor: `debug.resume`·`debug.step`을 부른 클라이언트) |
| `debug.console` | `debug_id`, `entry`(ConsoleEntry) |
| `debug.console_cleared` | `debug_id`, `through_seq` |
| `debug.closed` | `debug_id`, `reason`(`requested`/`process_exited`/`connection_lost`), `exit_code?`, `rev` |

- `timeline.list{host_id, since?, limit?}` 결과: `{entries: [TimelineEntry], next_cursor?}`(최신순).
- `host.doctor` 항목 `id`: `ssh_reachable`, `agent_version`, `quic_connect`, `fingerprint`, `session`, `firewall_rule`.

## 7. 오류와 결과 규칙

- **요청 문제는 JSON-RPC 오류**: 잘못되거나 빠진 파라미터, 모르는 호스트·job·메서드, `permanent:true` 없는 `files.delete`. 읽기 메서드(`files.list/stat/read`, `proc.list`, `jobs.*`, `session.inspect`, `host.*`, `debug.list/get/state/variables/scripts/source/console`)의 실패도 JSON-RPC 오류다.
- **원격에서 시도한 동작은 항상 봉투**(§2): 오프라인·차단된 호스트는 `status:"failed"`, `action:{executed:false, outcome:"not_started"}`, `error.code` `OFFLINE`/`AUTH_REQUIRED`. 에이전트가 거절하면 `failed`/`not_started`와 에이전트의 코드. 응답을 잃으면 `status:"unknown"`, `action:{executed:null, outcome:"unknown"}`, `EXECUTION_OUTCOME_UNKNOWN`(또는 `TIMEOUT`), `next_action:"check_state"`.
- `exec.run`의 0이 아닌 종료 코드는 `status:"failed"`, `error:null`, `code`에 종료 코드. 명령 시간 초과는 `failed` + `TIMEOUT`.
- `host.add`/`host.repair`는 동기 호출이다(진행은 `host.install.progress` 이벤트). 결과는 봉투 + `host`(Host) + `job`(Job, kind `install`). 이미 페어링된 PC는 봉투 `failed` + `INVALID_ARGUMENT` + `next_action:"host_repair"`. 같은 이름 중복은 JSON-RPC `INVALID_ARGUMENT`.
- `host.connect`/`host.disconnect` 결과는 Host. `host.remove` 결과 `{host_id, removed, uninstalled}`. `host.doctor` 결과 `{host_id, checks}`.
- `jobs.list` → `{jobs}`, `jobs.get` → Job, `jobs.cancel` → 봉투(`cancelled`) + `job`, `jobs.output{job_id, cursor?}` → `{chunks:[{stream:"stdout"|"stderr", data}], next_cursor, state, status, code?, dropped?}` (`next_cursor`는 `"<out>:<err>"` 바이트 위치, 끝났으면 `"end"`).
- `timeline.list`는 `cursor`(이전 결과의 `next_cursor`, seq)도 받는다. `since`는 RFC 3339. 행위자: `mcp` 클라이언트 → `ai`, `app`/`cli` → `human`, 데몬 자신 → `system`.
- Host에는 준비되지 않은 이유 `reason?`이 있다. `hello` 결과 `{client_id, daemon_version, api_version}`, `events.subscribe` 결과 `{subscribed, host_id, kinds}`.
- 봉투의 `session_id`는 알면 `s-<host_id>-<session_id>`, 모르면 null.

## 8. 파일·전송 세부 계약

- `files.list{host_id, path, cursor?, limit?}` → `{entries:[FileEntry], next_cursor?, total?, truncated?}`. `limit` 기본 500·최대 5000, `cursor`는 이전 결과의 `next_cursor`(불투명 문자열). 에이전트 한 번 목록이 5,000개를 넘으면 `truncated:true`.
- `files.stat{host_id, path}` → FileEntry, 없으면 JSON-RPC `NOT_FOUND`.
- `files.mkdir{host_id, path}`, `files.rename{host_id, from, to}`(전체 경로), `files.copy{host_id, from, to, on_conflict, recursive}`, `files.trash{host_id, path}`, `files.delete{host_id, path, permanent:true}`, `files.hash{host_id, path, offset?, length?}` → 모두 §2 봉투(`files.hash`는 봉투 + `sha256`).
- `transfer.start{direction, host_id, local, remote, on_conflict}`: `local`·`remote`는 옮길 항목의 전체 경로(대상 폴더가 아님). 결과는 봉투 + `transfer_id`. 폴더는 `on_conflict`를 폴더 안 파일마다 적용한다.
- `transfer.list{host_id?}` → `{transfers:[Transfer]}`(최신순). `transfer.get{transfer_id}` → Transfer.
- `transfer.pause{transfer_id}` → Transfer(`paused`). `transfer.resume{transfer_id}`는 `paused`·`failed`·`cancelled` 모두 다시 시작한다 → Transfer. `transfer.cancel{transfer_id}` → Transfer(`cancelled`, 끝난 파일은 남긴다).
- `Transfer.rate_bps`와 진행 값은 **초당 바이트**다(비트 아님).
- `app.launch{host_id, path, args?, cwd?, wait_window_ms?}` → 봉투 + `pid`, `window?`.
- `Transfer.error`·`TransferFile.error`는 `{code, message}` 객체다(§2 error 객체와 같은 형태, `retryable`·`next_action`은 선택).

## 9. 디버깅 세부 계약

- **세션**: 데몬이 세션(`debug_id`)을 소유한다. 앱을 닫아도 세션은 남고, 앱·CLI·MCP가 같은 세션을 쓴다. `debug.*` 이벤트는 그 호스트를 구독한 모든 클라이언트에 간다. 닫힌 세션은 닫힌 뒤 10분 동안 `debug.list`에 `state:"closed"`로 남는다. `created_by`는 세션을 만든 클라이언트다.
- **순서(`rev`·`gen`)**: DebugSession의 `rev`는 세션 필드(`state`, `breakpoints_active`, `pause_on_exceptions`, `main_unavailable_reason`, 대상 구성, 닫힘)가 바뀔 때마다, Breakpoint의 `rev`는 그 중단점이 바뀌거나 지워질 때마다 1씩 오른다(Transfer.`rev`와 같은 규칙). 클라이언트는 아는 값보다 큰 `rev`만 반영한다. 지운 중단점·사라진 대상은 지운 뒤의 `rev`를 기억해 그보다 오래된 스냅샷이 되살리지 않게 한다. 대상의 멈춤 필드(`paused`, `pause_pending`)와 PauseState는 대상별 `gen`으로만 순서를 정하고, 대상의 다른 필드(`title`, `url`, `attached`)는 받은 대로 반영한다. `pause/resume/step` 결과의 `target`은 데몬이 응답할 때 `paused`·`pause_pending`·`gen`을 한 번에 읽은 값이다.
- **줄·열**: 이 API의 `line`·`column`은 모두 **1부터** 센다(편집기·V8 스택 문자열 표기). 데몬이 CDP(`lineNumber`·`columnNumber`, 0부터)와 주고받을 때 1을 빼고 더한다. `column`은 UTF-16 단위다. 중단점에 `column`이 없으면 그 줄의 첫 실행 위치다. 줄 나눔은 ECMAScript 줄 끝(LF, CR, CRLF, U+2028, U+2029)을 따른다.
- **launch**: 에이전트가 `debug.ports{count:2}`로 루프백 빈 포트 P1·P2를 받아 `path args… --inspect-brk=127.0.0.1:P1 --remote-debugging-port=P2`로 사용자 세션에서 실행한다(`cwd` 기본은 `path`의 폴더). 에이전트는 띄운 프로세스와 그 자손 트리를 지켜본다. 띄운 프로세스가 끝나도 자손이 P1·P2를 열면 계속 기다리고, `pid`는 P2(없으면 P1)를 연 프로세스다. 데몬은 P1·P2에 터널을 열고 20초 동안 두 포트의 `/json/list`를 기다린다.
  - 둘 다 응답하면 main(`type:"node"`)과 renderer 대상에 붙는다. P2만 응답하면(퓨즈 `EnableNodeCliInspectArguments`가 꺼진 앱은 `--inspect`를 무시한다) renderer만 붙은 세션을 돌려주고 `main_unavailable_reason:"inspector_disabled"`(P1이 열리지 않음) 또는 `"inspector_not_responding"`(열렸지만 응답 없음)을 싣는다.
  - 실패: `path`가 없으면 `failed` + `NOT_FOUND`. 두 포트가 열리기 전에 트리의 모든 프로세스가 끝나면 `failed` + `EXITED_EARLY` + 결과의 `exit_code`(띄운 프로세스의 종료 코드, 흔한 원인은 이미 실행 중인 같은 앱의 단일 인스턴스 잠금). 20초 안에 어느 포트도 응답하지 않으면 `failed` + `TIMEOUT`. 실패하면 데몬이 띄운 프로세스 트리와 터널을 정리한다.
  - 시작 순서: 데몬은 main에 붙어 `Runtime.enable`·`Debugger.enable`, 받은 `breakpoints`·`pause_on_exceptions`를 건 뒤 `Runtime.runIfWaitingForDebugger`를 부른다. `pause_on_start`가 아니면 `--inspect-brk`의 첫 줄 멈춤에서 바로 `Debugger.resume`하고, `pause_on_start`면 `reason:"start"`로 멈춘 채 둔다. renderer는 브라우저 대상에 `Target.setAutoAttach{autoAttach:true, waitForDebuggerOnStart:true, flatten:true}`를 걸어 페이지가 스크립트를 실행하기 전에 멈추게 하고, 같은 중단점·예외 설정을 건 뒤 `Runtime.runIfWaitingForDebugger`로 놓는다.
- **attach**: `port`는 원격 PC의 루프백 포트다. 데몬은 `/json/version`의 `Browser`가 `node.js/…`면 main(Node 검사기), 아니면 renderer(DevTools) 포트로 본다. `role`(`main`/`renderer`)을 주면 그대로 쓴다. `pid`면 에이전트가 그 프로세스의 Node 검사기를 켜고(`process._debugProcess`와 같은 방식), 그 프로세스가 실제로 연 수신 포트를 찾아(프로세스가 소유한 루프백 TCP 수신 소켓) main으로 붙는다. 그 프로세스 명령줄에 `--remote-debugging-port=N`이 있으면 N에도 붙어 renderer를 연다. 검사기를 켤 수 없거나 5초 안에 수신 포트가 생기지 않으면 `failed` + `CAPABILITY_UNSUPPORTED`, 포트가 응답하지 않으면 `failed` + `NOT_FOUND`. `breakpoints`·`pause_on_exceptions`는 붙는 즉시 건다.
- **대상**: 데몬은 main과 `type:"page"` 대상에 자동으로 붙어 `Runtime.enable`·`Debugger.enable`을 켠다(`attached:true`). 다른 종류(`iframe`, `webview`, `worker`, `service_worker`, `shared_worker`, `other`)는 `attached:false`로 목록에만 두고 `debug.target.attach`로 붙는다. 나중에 생긴 page 대상도 자동으로 붙는다. 붙을 때 세션의 중단점·`pause_on_exceptions`·`breakpoints_active`를 그 대상에 다시 건다. 붙지 않은 대상에 `debug.state`·`debug.variables`·`debug.evaluate`·실행 제어를 부르면 JSON-RPC `INVALID_ARGUMENT` + `next_action:"attach_target"`(실행 제어는 봉투 `failed` + 같은 코드).
- **중단점**: 세션 단위다. `match`는 `pattern`을 읽는 방법이다. `file_name`은 URL 경로가 `pattern`으로 끝나는 스크립트(경로 구분자 경계에서, 쿼리 `?…`와 조각 `#…`은 무시)이고 데몬이 정규식(`(^|[/\\])` + 이스케이프한 `pattern` + `([?#].*)?$`)을 만들어 `urlRegex`로 건다. `url`은 정확히 일치(`url`), `url_regex`는 JavaScript 정규식(`urlRegex`)이다. 데몬은 붙은 대상마다 `Debugger.setBreakpointByUrl{…, lineNumber: line-1, columnNumber, condition}`을 부른다. `url_regex`는 데몬이 따로 검사하지 않고 처음 부른 `setBreakpointByUrl`이 거부하면 그 메시지로 JSON-RPC `INVALID_ARGUMENT`를 돌려준다. 붙은 대상이 없으면 정의만 보관하고, 처음 붙는 대상이 거부하면 그 중단점을 지우고 `debug.breakpoint_changed{removed:true, error}`를 보낸다. `pattern`이 비었거나 `line` < 1이면 JSON-RPC `INVALID_ARGUMENT`. `locations`는 모든 대상의 실제 위치를 합친 것이고 스크립트가 나중에 로드되면(`Debugger.breakpointResolved`) 늘어난다. `enabled:false`는 CDP 중단점을 지우되 정의는 남기고, `true`면 다시 건다. `debug.breakpoint.update`의 `condition`이 바뀌면 데몬이 CDP 중단점을 지우고 새 조건으로 다시 건다. `debug.breakpoints.activate{active:false}`는 `Debugger.setBreakpointsActive{active:false}`(정의와 `enabled`는 그대로 두고 모두 멈추지 않게 한다).
- **예외에서 멈춤**: `debug.exceptions{mode}`의 `mode` `none`/`uncaught`/`all` → `Debugger.setPauseOnExceptions{state}`, 붙은 모든 대상에. 새 세션의 기본값은 `uncaught`.
- **일시정지 순서**: DebugTarget·PauseState·`debug.paused`·`debug.resumed`·`pause/resume/step` 결과의 `target`은 같은 대상별 `gen`을 싣는다. 클라이언트는 대상마다 지금 아는 값보다 큰 `gen`만 반영한다. DebugTarget의 `gen`이 클라이언트가 가진 PauseState보다 크고 `paused:true`면 `debug.state`로 호출 스택을 읽는다. 클라이언트는 그 상태를 받기 전까지 그 `gen`을 반영한 것으로 치지 않는다.
- **`reason`**: CDP `Debugger.paused`의 `reason`을 바꿔 싣는다. `other` + `hitBreakpoints`가 있으면 `breakpoint`, `exception` → `exception`, `promiseRejection` → `promise_rejection`, 데몬이 `debug.pause`로 멈췄으면 `pause_request`, `debug.step` 다음에 멈췄으면 `step`, `--inspect-brk` 첫 줄 멈춤은 `start`, 나머지(`debugger;` 문, `assert`, `OOM` 등)는 `other`. `pause_request`·`step`에는 요청한 클라이언트를 `requested_by`로 싣고, 재개는 `debug.resumed{by}`에 싣는다. 실행 제어는 timeline `kind:"debug"`에 같은 행위자로 남는다.
- **실행 제어**: `debug.pause` → `Debugger.pause`(이미 멈춘 대상이면 아무것도 하지 않고 `succeeded`). 실행할 코드가 없어 바로 멈추지 않으면 대상의 `pause_pending:true`(같은 `gen`)를 알리고, 다음 JavaScript 실행에서 멈출 때 `false`가 된다. `debug.resume` → `Debugger.resume`(실행 중이면 `succeeded`, `pause_pending`도 취소한다), `debug.step{kind}`의 `kind` `over`/`into`/`out` → `Debugger.stepOver`/`stepInto`/`stepOut`. 실행 중인 대상에 `debug.step` 또는 `frame_id`가 있는 `debug.evaluate`를 부르면 `failed` + `STALE_TARGET` + `next_action:"check_state"`.
- **개체 ID**: `object_id`는 CDP `RemoteObjectId`다. 데몬은 일시정지 `gen`마다 내준 ID를 기록하고, 멈춘 동안 내준 ID(스코프, `debug.variables`의 속성, `exception`)를 그 대상이 재개된 뒤 받으면 CDP에 보내지 않고 JSON-RPC `STALE_TARGET`으로 거부한다(그 일시정지의 개체 그룹은 재개할 때 놓는다). `debug.evaluate`의 결과(`frame_id`가 있어도)와 그 속성은 세션의 `console` 개체 그룹(`objectGroup:"console"`)에 들어가 재개·한 단계 실행 뒤에도 유효하고, `debug.console.clear`나 세션 닫기에서 놓인다. 대상이 다른 페이지로 이동하거나 다시 불러오면(실행 컨텍스트가 사라지면) 그 대상의 모든 ID가 무효이고 `STALE_TARGET`이다.
- **변수**: `debug.variables` → `Runtime.getProperties{objectId, ownProperties:true, generatePreview:true}`. 내부 속성(`[[Prototype]]`, `[[Scopes]]`, `[[PromiseState]]` 등)은 `kind:"internal"`로 뒤에 붙고, getter·setter만 있는 속성은 `kind:"accessor"`(값은 읽지 않고 `value_preview`가 `(...)`). 순서는 CDP가 준 순서(배열은 인덱스 순). `offset`(기본 0)·`limit`(기본 200, 최대 1000)으로 나눠 받고 `total`은 전체 개수. 스코프 개체(`scope_chain[].object_id`)도 같은 방법으로 읽는다.
- **`value_preview`**: 사람이 읽는 한 줄, 최대 200자(넘으면 `…`로 자름). 문자열은 JSON 따옴표(`"서울"`), 숫자·불·`null`·`undefined`·bigint(`12n`)·symbol은 쓰인 그대로, 함수는 `ƒ 이름(인수)`, 개체는 CDP `description`(`Array(3)`, `Map(2)`, `Order`)에 미리보기가 있으면 ` {id: 1, total: 52000, …}`·` [1, 2, 3]`을 붙인다. `has_children`은 `object_id`가 있고 미리보기로 비어 있음(`[]`, `{}`)이 확인되지 않으면 true.
- **평가**: `frame_id`가 있으면 `Debugger.evaluateOnCallFrame{objectGroup:"console"}`, 없으면 `Runtime.evaluate{replMode:true, includeCommandLineAPI:true, objectGroup:"console"}`, 둘 다 `generatePreview:true`. 식이 예외를 던지면 봉투는 `succeeded`이고 RemoteValue 필드에 던져진 값, `exception`에 설명이 온다. 30초를 넘기면 데몬이 `Runtime.terminateExecution`을 부르고 `failed` + `TIMEOUT`.
- **스크립트**: 데몬이 붙은 대상마다 `Debugger.scriptParsed`로 받은 스크립트 중 URL이 있는 것(`node:internal/…` 포함)을 보관한다(페이지를 다시 불러오면 그 대상의 목록을 새로 시작한다). `target_id`를 주면 그 대상 것만 준다.
- **소스**: `debug.source` → `Debugger.getScriptSource`. UTF-8로 2 MiB를 넘으면 2 MiB를 넘지 않는 마지막 문자 경계까지만 보내고 `truncated:true`. 모르는 `script_id`는 JSON-RPC `NOT_FOUND`. 소스맵은 P1이다.
- **콘솔**: 데몬이 세션마다 마지막 5,000개 항목을 보관한다. `cursor`(이전 결과의 `next_cursor`)가 있으면 그보다 큰 `seq`를 오래된 순으로 최대 `limit`개, `before`(seq)가 있으면 그보다 작은 `seq` 중 마지막 `limit`개, 둘 다 없으면 마지막 `limit`개를 준다(`limit` 기본 200, 최대 1000, 항목은 언제나 오래된 순). `cursor`와 `before`를 함께 주면 JSON-RPC `INVALID_ARGUMENT`.
  - `next_cursor`: 돌려준 마지막 `seq`. 항목이 없으면 `cursor` 페이지는 받은 `cursor`, `before` 페이지는 `before - 1`, 최신 페이지는 버퍼의 마지막 `seq`(비었으면 0).
  - `first_seq`: 버퍼에 남은 가장 오래된 `seq`(비었으면 마지막 `seq + 1`). `gap`: `cursor`가 `first_seq - 1`보다 작아 그 사이 항목이 버퍼에서 밀려났으면 true. `has_more`: 돌려준 것 뒤에 항목이 더 있으면 true. `has_earlier`: 돌려준 첫 항목보다 오래된 항목이 버퍼에 있으면 true. `dropped`: 버퍼에서 밀려난 항목 수(세션 전체).
  - `debug.console.clear` → 그때까지의 항목을 버퍼에서 지우고(`first_seq`가 그 다음으로 옮겨 간다) `console` 개체 그룹을 놓은 뒤 `debug.console_cleared{through_seq}`를 보낸다. 모든 클라이언트가 같은 버퍼를 보므로 다른 클라이언트의 콘솔에서도 지워진다.
  - 이벤트를 놓쳤을 수 있으면(데몬 연결이 다시 이어진 뒤) 가진 가장 큰 `seq`를 `cursor`로 불러 채우고, `gap:true`면 그 자리에 놓친 항목이 있음을 표시한다.
- **콘솔 항목**: `source` `console` ← `Runtime.consoleAPICalled`, `exception` ← `Runtime.exceptionThrown`, `browser` ← `Log.entryAdded`. `level`은 CDP `type`에서 `log`/`info`/`debug`/`warning`(`warning`)/`error`로 줄인다(`trace` → `debug`, `assert` 실패 → `error`, `dir`·`dirxml`·`table`·`count`·`timeEnd` → `log`). `text`는 인수 미리보기를 공백으로 이은 것(`%s`·`%d`·`%i`·`%f`·`%o`·`%O` 치환 적용, `%c` 제거, 최대 10,000자). `script_id`·`url`·`line`·`column`은 호출 위치(스택 첫 프레임), `stack`은 최대 20프레임.
- **닫기**: `debug.close` → 데몬이 CDP 연결과 자기가 연 터널을 닫고, launch 세션이면(`keep_process:true`가 아니면) 자기가 띄운 프로세스 트리를 끝낸다. attach 세션은 프로세스를 두고 연결만 해제한다. PID로 연결하면서 데몬이 켠 Node 검사기는 가능하면 닫고(main 대상에서 `require('node:inspector').close()`), 닫지 못하면 결과에 `inspector_left_open:true`를 싣는다. 결과 뒤 `debug.closed{reason:"requested"}`. 프로세스가 스스로 끝나면 `debug.closed{reason:"process_exited", exit_code?}`. 원격 PC와 연결이 끊기면 세션은 `connection_lost`로 닫히고, 다시 연결되면 데몬이 그 launch 세션이 띄운 프로세스를 끝낸다. 닫힌 세션에 다른 `debug.*`를 부르면 JSON-RPC `NOT_FOUND`.
- **기록**: launch·attach·중단점·실행 제어·평가·콘솔 지우기·닫기는 timeline `kind:"debug"`로 요청한 클라이언트의 행위자와 함께 남는다. 평가 식은 기록하고 결과 값은 기록하지 않는다.
