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
| `control.stop` | `host_id` | 긴급 정지: owner=human, `ai_paused=true` |
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
| `pty.open{host_id, shell, cols, rows}` → `pty_id` | 이후 `stream.attach{kind:"pty"}` |

### 파일
| 메서드 | 결과 |
|---|---|
| `files.list{host_id, path}` / `files.stat` / `files.read{path, offset, length}` / `files.write` | 원격 파일 작업 |
| `files.copy` / `files.move` / `files.rename` / `files.mkdir` / `files.trash` / `files.delete{permanent:true}` | 봉투 |
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

`kind:"pty"` (`pty_id`): `0x20`–`0x23` (protocol.md §7과 동일).

## 5. 모델

모든 JSON 키는 snake_case. 시각은 RFC 3339 UTC 밀리초(`2026-10-20T12:34:56.789Z`). 표시 안 한 필드는 항상 있다; `?`는 없을 수 있다.

| 모델 | 필드 |
|---|---|
| Host | `host_id`, `name`, `os`, `arch`, `address`, `port`, `ssh_alias`, `state`(연결 상태), `caps`(기능 이름 → Capability), `session?`(SessionInfo), `displays`([DisplayInfo]), `owner`(`none`/`human`/`ai`), `owner_client?`, `agent_version?`, `connection_generation`, `last_seen?` |
| Capability | `state`(`ready`/`unavailable`/`unsupported`/`needs_user`), `reason?` |
| SessionInfo | `session_id`(정수), `user`, `sid?`, `connect_state`(`active`/`connected`/`disconnected`), `protocol`(`console`/`rdp`), `locked`, `input_desktop`(`Default`/`Winlogon`/`unknown`), `integrity?` |
| DisplayInfo | `index`, `name`, `adapter`, `x`, `y`, `width`, `height`, `dpi`(정수), `scale`(실수), `primary`, `refresh_hz`(정수) |
| ControlState | `owner`, `owner_client?`, `gen`, `ai_paused` |
| Job | `job_id`, `host_id`, `kind`(`exec`/`transfer`/`qa`/`install`/`debug`/`pty`), `status`(봉투 status 값), `request_id?`, `summary`, `progress?`(0–1), `created_at`, `updated_at`, `finished_at?`, `result?`(객체) |
| FileEntry | `name`, `path`, `dir`, `size`, `mtime`, `link`, `hidden?`, `readonly?` |
| Transfer | `transfer_id`, `host_id`, `direction`(`push`/`pull`/`move_push`/`move_pull`), `local`, `remote`, `on_conflict`, `state`(`queued`/`running`/`paused`/`verifying`/`done`/`failed`/`cancelled`), `bytes_done`, `bytes_total`, `files_done`, `files_total`, `rate_bps`, `outcome?`(`written`/`skipped`/`renamed`/`copied_source_retained`/`partial`/`failed`), `error?`, `files`([TransferFile]) |
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
