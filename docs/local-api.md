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
  "action": {"executed": true, "outcome": "done|not_started|unknown|partial"},
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
| `control.get` | `host_id` | ControlState(§5): `owner`(`none`/`human`/`ai`), `owner_client?`, `owner_client_id?`, `gen`, `ai_paused` |
| `control.acquire` | `host_id`, `as`(`human`/`ai`), `force`(사람만) | 바뀐 뒤의 ControlState |
| `control.release` | `host_id` | 바뀐 뒤의 ControlState(owner=none) |
| `control.stop` | `host_id` | 긴급 정지: owner=human, `ai_paused=true`. 연결이 끊긴 호스트에도 적용된다(재연결 후 AI 입력 차단 유지). AI 클라이언트가 부르면 `ai_paused=true`만 켜고 owner는 사람이면 그대로, 아니면 none(타임라인 행위자는 그 AI) |
| `control.resume_ai` | `host_id` | `ai_paused=false`, owner=none |

- `gen`은 owner(AI면 그 클라이언트까지)나 `ai_paused`가 바뀔 때마다 1씩 오르고 데몬을 다시 시작해도 줄지 않는다. 바뀔 때마다 `control.changed`와 타임라인 항목(`kind:"control"`)이 나가고, 연결된 에이전트에 `control.set_writer{gen}`을 보낸다(실패하면 0.1초에서 두 배씩 최대 5초 간격으로 에이전트가 받을 때까지 다시 보낸다). 사람이 부른 변경은 에이전트가 새 `gen`을 받을 때까지(최대 3초) 기다렸다가 답한다. PC와 새로 연결될 때마다(재연결 포함) 데몬은 그 연결을 ready로 표시하기 전에 그 연결로 현재 `gen`의 `control.set_writer`를 보내고(실패하면 그 연결을 끊고 다시 연결한다), 그 사이 도착한 에이전트 이벤트는 ready 뒤에 전한다. 에이전트가 더 큰 세대를 답하거나 AI 입력 스트림·`app.launch`를 `CONTROL_PREEMPTED`로 거절하면 데몬이 에이전트의 세대를 물어 더 큰 값을 호스트의 `gen`으로 삼는다(입력 스트림은 그 세대로 한 번 다시 연다).
- AI 제어권은 lease다: AI 입력과 UI 계열 동작(아래 writer 동작)마다 60초로 갱신되고, 60초 동안 없거나 그 클라이언트의 연결이 끊기거나 데몬이 다시 시작하면 owner=none(`gen`+1)이 된다. 사람 제어권은 놓거나(`control.release`·`control.resume_ai`) AI에게 넘길 때까지 남는다.
- 거절은 JSON-RPC 오류다. AI 클라이언트(`mcp`)의 `as:"ai"`: 사람이 쥐고 있으면 `CONTROL_HELD_BY_HUMAN`(`human_release`), `ai_paused`면 `CONTROL_HELD_BY_HUMAN`(`human_resume_ai`), 다른 AI 클라이언트의 lease가 살아 있으면 `CONTROL_PREEMPTED`(`retryable:true`, `retry`). 사람의 `as:"human"`은 AI lease가 살아 있으면 `force:true`일 때만 가져오고 아니면 `INVALID_ARGUMENT`. AI 클라이언트의 `as:"human"`·`control.resume_ai`는 `PERMISSION_DENIED`, `as:"ai"`의 `force`는 `INVALID_ARGUMENT`. AI 클라이언트는 사람 제어권(`CONTROL_HELD_BY_HUMAN`)이나 다른 AI의 lease(`CONTROL_PREEMPTED`)를 `control.release`로 놓지 못한다. 거절된 `control.acquire`는 `outcome:"denied"` 타임라인 항목을 남긴다.
- writer 동작: 데몬은 에이전트의 writer op(`app.launch`, `debug.electron.launch`, `ui.act`, `win.action`, `clipboard.set` — protocol.md §3.1)와 입력 스트림에 `gen`을 붙여 보낸다. AI 클라이언트의 요청은 보내기 전에 제어권을 확인하고(위 lease 규칙, 없으면 lease를 얻는다), 거절되면 PC에 아무것도 보내지 않고 봉투 `failed`/`not_started` + 위 오류다(`debug.electron.launch`는 세션·job을 만들지 않고 `session:null`). 사람·데몬의 요청은 현재 `gen`을 붙인다. 연결을 기다린 요청을 포함해 writer op는 연결로 내보내기 직전에 다시 확인한다: AI 요청은 같은 클라이언트가 같은 `gen`으로 쥐고 있을 때만, 사람·데몬의 요청은 `gen`이 그대로일 때만 나가고, 아니면 보내지 않고 같은 봉투(AI는 위 오류, 사람은 `CONTROL_PREEMPTED`·`retryable:true`)다.

### 관찰·입력 (AI·CLI)
코드: `internal/daemon/observe`.

| 메서드 | 파라미터 | 결과 |
|---|---|---|
| `screen.capture` | `host_id`, `display`(기본 0), `max_width`(기본 1568, 0은 디스플레이 폭), `format`(`png` 기본/`jpeg`), `quality`(`jpeg`) | `capture_id`, `host_id`, `display`, `width`, `height`, `scale`, `origin`{`x`,`y`}, `dpi`, `ts`(데몬이 이미지를 받은 시각), `mono_us`, `method`(`dd`/`gdi`), `format`, `connection_generation`, `expires_at`(좌표를 쓸 수 있는 끝), `artifact`(Artifact, `kind:"screenshot"`) |
| `input.click` | `host_id`, `capture_id`, `x`, `y`(캡처 좌표), `button`(`left` 기본/`right`/`middle`/`x1`/`x2`), `count`(1–3, 기본 1), `observe?` | 봉투 + `target` + `events_total`·`events_done` + `observation` |
| `input.move` | `host_id`, `capture_id`, `x`, `y`, `observe?` | 봉투 + `target` + … |
| `input.drag` | `host_id`, `capture_id`, `x`, `y`(시작), `to_x`, `to_y`(끝, 같은 캡처), `button`, `steps`(1–100, 기본 10), `duration_ms`(0–10000, 0은 300), `observe?` | 봉투 + `target`·`to` + … |
| `input.scroll` | `host_id`, `capture_id`, `x`, `y`, `dx`, `dy`(휠 칸 수, 한 칸 = 120, `dy` 양수 위·`dx` 양수 오른쪽, 각 ±100 안), `observe?` | 봉투 + `target` + … |
| `input.key` | `host_id`, `keys`(예: `ctrl+shift+esc`, `enter`, `ctrl+a delete`), `observe?` | 봉투 + `keys`(정규화한 이름) + … |
| `input.type` | `host_id`, `text`, `method`(`auto` 기본/`unicode`/`paste`), `observe?` | 봉투 + `method`(쓴 방법) + `chars` + … |
| `observe` | `host_id`, `level`(`none`/`summary` 기본/`image`/`ui`), `settle_ms`(0–30000), `display`(기본 0), `max_width`·`format`(`image`) | `host_id`, `level`, `foreground`, `focused`, `changed`, `settled`, `waited_ms`, `capture?`, `ui?`, `errors`, `connection_generation` |

- **캡처와 좌표**: `screen.capture`는 에이전트 `screen.capture`(protocol.md §3.1)의 정지 이미지를 `<home>/artifacts/captures/<host_id>/<capture_id>.png|.jpg`에 저장하고(24시간 뒤 지운다) 좌표 환산 표를 `capture_id`별로 10분 동안 둔다. 입력의 `x`, `y`는 그 이미지의 좌표다(0 ≤ `x` < `width`, 0 ≤ `y` < `height`, 밖이면 JSON-RPC `INVALID_ARGUMENT`). 데몬은 점이 든 이미지 픽셀 `floor(x)`의 가운데를 원본으로 옮긴 픽셀 `floor((floor(x)+0.5)/scale)`(디스플레이 크기 `round(width/scale)` 안으로 맞춘다)을 그 디스플레이의 픽셀로 보낸다. `target`은 {`capture_id`, `display`, `x`, `y`(디스플레이 왼쪽 위 기준 원본 픽셀), `screen_x`, `screen_y`(가상 데스크톱 좌표 = `origin` + 원본 픽셀, 음수일 수 있다)}다. 같은 이미지의 좌표는 디스플레이가 어디 있든(왼쪽·위의 음수 원점 포함) 그 디스플레이 안으로만 간다.
- **오래된 capture_id**: 모르는 `capture_id`(10분이 지났거나 데몬이 다시 시작함), 다른 `connection_generation`에서 찍은 것, 찍은 뒤 그 PC의 `display.changed`를 받은 것은 PC에 아무것도 보내지 않고 봉투 `failed`/`not_started` + `STALE_TARGET`(`next_action:"screen_capture"`)다. 연결 세대는 입력 스트림이 열린 연결로 첫 이벤트를 쓰기 직전에 다시 확인한다. 다른 PC의 `capture_id`는 JSON-RPC `INVALID_ARGUMENT`다.
- **제어권**: 입력은 §3 제어권의 writer 동작과 같다. AI 클라이언트의 입력은 `CheckAI`(lease를 얻거나 60초로 갱신)를 거쳐 그 `gen`의 입력 스트림으로 가고, 사람의 입력(`app`/`cli`)은 아무도 쥐지 않았으면 사람 제어권을 얻고 AI lease가 살아 있으면 `CONTROL_PREEMPTED`다. 거절은 PC에 보내지 않고 봉투 `failed`/`not_started` + 그 오류이고 타임라인 `outcome:"denied"`다. 이벤트마다 쓰기 직전에 다시 확인해 제어권이 바뀌면 남은 이벤트를 보내지 않는다.
- **결과**: `action.executed`는 모든 이벤트에 에이전트의 확인(`0x42` ok)이 왔을 때만 true(`succeeded`/`done`)다. 확인 전에 입력 스트림이나 연결이 끊기면 `unknown`(`executed:null`, `EXECUTION_OUTCOME_UNKNOWN`, `next_action:"check_state"`)이고 데몬은 다시 보내지 않는다. 일부 이벤트만 주입됐거나 실패한 `text`가 앞부분을 쳤을 수 있으면 `failed` + `action:{executed:false, outcome:"partial"}`(`next_action:"check_state"`), 아무 이벤트도 주입되지 않았으면 `failed`/`not_started`이고 오류는 에이전트 확인의 코드다(protocol.md §6). `events_total`은 보낸(보내려던) 이벤트 수, `events_done`은 앞에서부터 확인 ok를 받은 수다.
- **키**: `keys`는 공백으로 나눈 조합(최대 50개)이고 조합은 `+`로 잇는다: 수정 키(`ctrl`·`shift`·`alt`·`win`, 각 `l`/`r` 형태, `altgr`=`ralt`) 뒤에 키 하나(수정 키만도 된다). 대소문자는 가리지 않고 두 글자 이상 이름의 `_`는 무시한다(`page_down`=`pagedown`). 이름은 Windows 키 이름이다: `a`–`z`, `0`–`9`, `-` `=` `[` `]` `\` `;` `'` `,` `.` `/` `` ` ``(별칭 `minus`·`equal`·`comma`·`slash` 등), `esc`, `enter`, `tab`, `space`, `backspace`, `delete`, `insert`, `home`, `end`, `pageup`, `pagedown`, `up`/`down`/`left`/`right`, `f1`–`f24`, `capslock`, `numlock`, `scrolllock`, `printscreen`, `pause`, `menu`, `numpad0`–`numpad9`, `numpad_add`/`subtract`/`multiply`/`divide`/`decimal`/`enter`, `hangul`(`한영`, `한/영`), `hanja`(`한자`), `volumeup`/`volumedown`/`volumemute`, `mediaplaypause`/`medianext`/`mediaprev`/`mediastop`. 조합은 키를 차례로 누르고 거꾸로 뗀다. 위치가 정해진 키는 set 1 스캔코드(E0 키는 `ext`)로, `f13`–`f24`·`printscreen`·`pause`·`numlock`·`hangul`(VK_HANGUL)·`hanja`(VK_HANJA)·음량·미디어 키는 가상 키로 보낸다. 모르는 이름(`cmd` 등)·빈 이름(`ctrl+`)·같은 키 두 번·키 뒤의 키(`a+b`, `enter+ctrl`)는 아는 이름 목록을 담은 JSON-RPC `INVALID_ARGUMENT`다.
- **글자**: `auto`는 글이 2000자(에이전트 기준: 코드 포인트, `\r\n`은 2자) 이하이고 모든 글자를 `unicode`가 한 글자씩 칠 수 있으면(기본 다국어 평면의 글자, 제어 문자는 `\t`·`\r`·`\n`만) `unicode`, 아니면(더 길거나 이모지 같은 평면 밖 글자, 다른 제어 문자) `paste`다. 실패한 `unicode` 입력을 `paste`로 다시 보내지 않는다(앞부분이 이미 쳐졌을 수 있다). 100000자를 넘는 글은 `\r\n`을 가르지 않고 100000자 이하의 `text` 이벤트 여럿으로 나눠 한 번에 보낸다. 타임라인에는 글 내용 없이 방법과 글자 수만 남긴다.
- **관찰**: `observe`는 `settle_ms`가 있으면 그 디스플레이를 폭 256 PNG로 0.1초마다 찍어(산출물로 남기지 않는다) 화면이 두 간격(0.2초) 동안 바뀌지 않고 에이전트 창 이벤트(`window.opened`·`window.closed`·`window.foreground`·`window.title`·`dialog.opened`)도 없을 때까지, 최대 `settle_ms` 기다린다(`settled`, `waited_ms`). 화면을 찍지 못하면(잠김 등) 창 이벤트가 0.2초 동안 없을 때까지 기다리고 `errors`에 `screen` 항목을 남긴다. 화면 비교는 디스플레이를 64×36칸으로 나눈 칸별 평균 밝기(0–255)이고, 4를 넘게 달라진 칸이 있으면 바뀐 것이다(커서·깜빡이는 캐럿 정도는 바뀐 것으로 치지 않는다). `changed`는 관찰 끝의 화면을 그 PC에서 마지막으로 본 화면(앞의 `screen.capture`·`observe`, 입력의 `observe`는 그 `capture_id`의 화면)과 비교한 값이고, 둘 중 하나라도 볼 수 없으면 `null`이다. `settled`는 `settle_ms`가 0이면 `null`이다. `level`: `none`은 위만, `summary`는 `foreground`(`win.list`의 앞 창)와 `focused`(`ui.focused`의 `node`), `image`는 `summary`에 `capture`(`screen.capture`와 같은 결과, `display`·`max_width`·`format`), `ui`는 `summary`에 `ui`(앞 창의 `ui.tree{filter:"interactive", depth:12, max:300}`)를 더한다. 읽지 못한 부분은 `null`이고 `errors`([{`part`(`screen`/`capture`/`foreground`/`focused`/`ui`), `code`, `message`}])에 남는다. PC와 연결돼 있지 않으면 JSON-RPC `OFFLINE`이다.
- **입력 뒤 관찰**: 입력 메서드의 `observe`{`level`(기본 `summary`), `settle_ms`, `display?`(기본: 좌표 입력은 그 디스플레이, 아니면 0)}는 입력이 (일부라도) 실행됐을 때 그 뒤에 `observe`를 하고 결과를 `observation`에 담는다. 관찰이 실패하면 `observation:null`과 `observation_error`이고 입력 결과(봉투)는 그대로다 — 관찰 실패로 입력을 다시 보내지 않는다. `observe`가 없거나 입력이 실행되지 않았으면 `observation:null`.
- **기록**: 입력은 타임라인 `kind:"input"`, `screen.capture`·`observe`와 아래 UI 읽기는 `kind:"observe"`로 그 클라이언트를 행위자로 남긴다(`screen.capture`는 그 산출물과 함께).

### UI Automation
`ui.tree`, `ui.find`, `ui.text`, `ui.rect`, `ui.focused`, `ui.at`, `win.list`(읽기)와 `ui.act`, `win.action`(writer 동작) — 에이전트 op와 같은 파라미터에 `host_id`.
- 읽기는 제어권과 상관없이(사람이 쥐고 있어도) 에이전트 결과에 `host_id`·`connection_generation`을 더해 돌려주고, 실패는 JSON-RPC 오류(에이전트의 코드)다. `ui.at`은 `x`, `y`와 함께 `capture_id`를 주면 그 이미지 좌표를 위 규칙으로 가상 데스크톱 좌표로 바꿔 보내고 `target`을 더한다(오래된 `capture_id`는 JSON-RPC `STALE_TARGET`).
- `ui.act`·`win.action`은 §3 제어권의 writer 동작이다: AI 클라이언트의 요청은 lease를 얻거나 갱신하고, 사람이 쥐었거나 긴급 정지 중이면 PC에 보내지 않고 봉투 `failed`/`not_started` + `CONTROL_HELD_BY_HUMAN`이다. 연결을 기다린 요청도 쓰기 직전에 다시 확인한다. 결과는 봉투 + 에이전트 결과 필드이고 타임라인 `kind:"input"`이다.

### 명령·터미널·프로세스
| 메서드 | 결과 |
|---|---|
| `exec.run{host_id, argv|cmd, shell, cwd, env, stdin, timeout_ms}` | 봉투 + `code`, `stdout`, `stderr`, `timed_out`(시간 초과로 끝냈으면 true), `truncated`(스트림마다 끝 200000바이트만 남겼으면 true), `killed?`(§7) (큰 출력은 artifact) |
| `exec.start{…, timeout_ms?}` | 봉투(job). job의 표준 입력은 `stdin` 글(없으면 빈 글) 뒤 EOF다. `timeout_ms`는 데몬이 job을 만든 시각부터 잰다. 넘으면 `jobs.cancel`과 같은 길로 프로세스를 끝내고 job을 `failed` + `TIMEOUT`(`result.timed_out:true`)으로 끝낸다. 그때까지의 출력은 `jobs.output`으로 읽을 수 있다. 데몬이 다시 시작해도 남은 시간을 이어 재고, 연결이 끊긴 동안 시간이 다 된 job은 다시 연결될 때 끝낸다 |
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

- 결과 필드는 봉투와 같은 객체에 평평하게 들어간다(`diag.system`은 `os`·`computer_name`·`cpu`·`memory`·`disks`·`gpus`·`boot_time`·`uptime_s`, `diag.services`는 `services`, `diag.events`는 `events`·`truncated`, `diag.crashes`는 `dumps`). 읽기라서 `request_id`는 없고, PC가 읽지 못하면 `failed`/`not_started`와 에이전트의 코드다(Windows 밖 PC는 `CAPABILITY_UNSUPPORTED`).
- 파라미터 문제는 PC에 보내지 않고 JSON-RPC `INVALID_ARGUMENT`다: `host_id` 없음, `diag.events`의 모르는 `log`·`levels`, RFC 3339가 아닌 `since`·`until`, `since`보다 이른 `until`, 1–1000 밖이거나 정수가 아닌 `max`, `diag.crashes`의 RFC 3339가 아닌 `since`, `proc.kill`의 RFC 3339가 아닌 `started`와 `pid` 없이 준 `started`.
- `save:true`: 성공한 결과를 `kind:"diag"` job(요약 `시스템 정보 저장`·`서비스 목록 저장`·`이벤트 로그 저장 (<로그>)`·`크래시 목록 저장`)의 artifact로 저장한다. artifact는 `kind:"report"`, `mime:"application/json"`, 이름 `diag-system.json`·`diag-services.json`·`diag-events.json`·`diag-crashes.json`이고 내용은 `{schema_version, host_id, method, saved_at, result}`(`result`는 위 결과 필드)다. 봉투의 `job_id`가 그 job, `artifacts`가 그 artifact, `artifact_id`가 그 ID다. job은 바로 `succeeded`로 끝나고 `result`는 `{artifact_id, artifacts}`다. 저장하지 못하면 결과 필드는 그대로 두고 `failed` + `INTERNAL`이다.
- `proc.list`의 `started_by_deskhand`는 에이전트가 정한다. 그 필드를 보내지 않는 이전 에이전트에서는 데몬이 실행 중인 exec·pty job의 PID와 그 자손으로 정한다.

### 디버깅
Chrome DevTools Protocol(CDP) 디버거. 세부 규칙은 [§9](#9-디버깅-세부-계약).

터널(`Tunnel`): `{tunnel_id("tn-…"), host_id, remote_port, local_port, local_address("127.0.0.1:<local_port>"), owner, created_at, connections(지금 열린 연결), total_connections, failed_connections, bytes_to_remote, bytes_from_remote, last_error?}`.

- 이 Mac 쪽은 `127.0.0.1`에만 묶고(`0.0.0.0`·다른 인터페이스 없음) PC 쪽은 에이전트가 `127.0.0.1`·`::1`에만 연결한다. 받은 TCP 연결 하나가 `tunnel` 스트림 하나(protocol.md §2)이고 양쪽 반쪽 닫기를 그대로 전한다. 호스트가 연결돼 있고 에이전트 `caps.tunnel`이 `ready`일 때만 연다: 연결이 없으면 `OFFLINE`, 터널을 모르는 에이전트는 `CAPABILITY_UNSUPPORTED`, `local_port`가 이미 쓰이면 `PORT_CONFLICT`, 포트가 1–65535 밖(`local_port`는 0 허용)이거나 `host_id`가 없으면 `INVALID_ARGUMENT`.
- `owner`는 `"user"`(이 API로 연 터널) 또는 `"debug:<debug_id>"`(디버그 세션이 연 터널). 디버그 세션의 터널은 그 세션이 `debug.close`·`connection_lost`·실패 정리에서 닫는다. `tunnel.close`는 `owner`가 `user`가 아닌 터널을 `INVALID_ARGUMENT`로 거절한다. 디버거는 `debug.ports`로 받은 PC 포트마다 자기 `owner`로 터널을 열고 자기 터널만 닫는다.
- 호스트 연결이 끊겨도 리스너는 열려 있다. 그동안 들어온 연결은 바로 닫히고 `failed_connections`와 `last_error`에 이유가 남으며, 연결이 돌아오면 같은 포트로 다시 쓸 수 있다. 이미 열려 있던 연결은 호스트 연결과 함께 끊긴다. 호스트를 제거하거나(`host.remove`) 데몬이 끝나면 그 터널이 닫힌다. 데몬을 다시 시작해도 터널은 이어지지 않는다.
- 한도: 호스트마다 터널 32개(넘으면 `tunnel.open`이 `INVALID_ARGUMENT`), 터널마다 동시 연결 128개, 호스트 전체 터널 스트림 256개. 넘어서 들어온 연결은 바로 닫히고 `failed_connections`와 `last_error`에 남는다. 한쪽 방향이 끝난 연결은 반대 방향이 2분 동안 바이트를 나르지 않으면 닫힌다(양쪽이 열려 있으면 오래 놀아도 유지된다).
- PC의 포트에 받는 프로그램이 없으면 터널은 열리지만 그 포트로 들어온 연결이 바로 닫히고 `last_error`에 이유가 남는다.
- 사람이 쓰는 CLI는 `deskhand tunnel <호스트> <포트> [--local P]`(실행하는 동안 열려 있고 Ctrl-C로 닫음), `deskhand tunnel list [호스트]`, `deskhand tunnel close <로컬포트|터널ID>`다. MCP는 `tunnel_open`·`tunnel_close`·`tunnel_list`.

| 메서드 | 결과 |
|---|---|
| `tunnel.open{host_id, remote_port, local_port?}` | Tunnel(아래) — 이 Mac의 `127.0.0.1:local_port`(생략하면 빈 포트)에서 받아 PC의 `127.0.0.1:remote_port`로 잇는다 |
| `tunnel.close{tunnel_id \| local_port}` | `{closed:true, tunnel_id, local_port, remote_port, host_id}` — 리스너와 모든 연결을 닫는다. 없는 터널은 `NOT_FOUND` |
| `tunnel.list{host_id?}` | `{tunnels:[Tunnel]}`(연 순) |
| `debug.list{host_id?}` | `{sessions:[DebugSession]}`(만든 순) |
| `debug.get{debug_id}` | DebugSession |
| `debug.electron.launch{host_id, path, args?, cwd?, pause_on_start?, breakpoints?, pause_on_exceptions?}` | 봉투(job, kind `debug`) + `session`(DebugSession). `breakpoints`는 [BreakpointSpec]이다(거는 시점은 §9 launch) |
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
QA 시나리오 실행기. 세부 규칙은 [§10](#10-qa-세부-계약).

| 메서드 | 결과 |
|---|---|
| `qa.run{host_id?, scenario_path \| scenario, base_dir?, vars?, timeout_ms?}` | 봉투(job, kind `qa`, `status:"running"`) + `run_id` + `run`(QARun, 단계 포함) |
| `qa.get{run_id}` | QARun(단계 포함) |
| `qa.list{host_id?, limit?, cursor?}` | `{runs:[QARun(단계 없음)], next_cursor?}`(최신순) |
| `qa.cancel{run_id}` | 봉투 + `run`(QARun, 단계 포함) |
| `artifacts.list{job_id}` | `{job_id, dir, artifacts:[Artifact]}` |
| `artifacts.export{job_id, dir}` | `{job_id, dir(만든 폴더), files, bytes, manifest?}` |
| `timeline.list{host_id, since, limit}` | 사람·AI 동작 기록 |

## 4. 스트림 연결 (`stream.attach`)

새 소켓 연결에서 `hello` 다음 `stream.attach`를 부르면, 성공 응답 줄 이후 그 연결은 바이너리 프레임(`u32 BE 길이` + 본문, 첫 바이트 종류)으로 바뀐다.

`kind:"screen"` (`host_id`, `display`(기본 0), `preset`(`sharp`/`balanced`/`saver`, 기본 `balanced`)) — 코드: `internal/daemon/screen`
- 성공 응답의 `result`는 `{host_id, display, preset}`. 실패는 JSON-RPC 오류다: `host_id`가 없거나 `display`가 음수이거나 모르는 `preset`은 `INVALID_ARGUMENT`, 모르는 PC는 `NOT_FOUND`, PC에 그 디스플레이가 없으면(에이전트의 `video` 거절 `NOT_FOUND`) `DISPLAY_UNAVAILABLE`, 화면을 보낼 수 없는 에이전트(Windows 밖·화면 스트림이 없는 에이전트)는 `CAPABILITY_UNSUPPORTED`, PC와 연결돼 있지 않으면 `OFFLINE`(`retryable:true`). 에이전트가 거절하면 코드는 그대로 두고 문장은 코드별로 다음 행동을 알려 주는 한국어 문장이다(에이전트의 원문은 데몬 로그에 남긴다).
- 데몬 → 앱: 비디오 `0x01`–`0x04`(protocol.md §4와 동일), 커서 `0x10`·`0x11`(§5와 동일), `0x30` 알림 JSON.
- 앱 → 데몬: `0x40` 제어 JSON(`{"op":"keyframe"}`, `{"op":"preset","preset":…}`), `0x41` 사람 입력 JSON(protocol.md §6 이벤트, 좌표는 원본 픽셀). 다른 종류와 모르는 `op`는 버린다.
- 공유: 한 PC의 한 디스플레이에 붙은 연결(앱 창)은 모두 에이전트 `video`·`cursor` 스트림 하나씩을 함께 쓴다. 첫 연결이 열고, 마지막 연결이 떠난 뒤 5초 동안 아무도 붙지 않으면 데몬이 닫는다(그 사이 붙으면 그대로 쓴다). 데몬은 영상을 디코딩하지 않고 프레임을 그대로 나른다. 에이전트 스트림이 끝나면(연결 끊김 포함) 붙은 연결은 그대로 두고 0.2초부터 두 배씩 최대 5초 간격으로 다시 열며, 다시 열 수 없는 거절(`DISPLAY_UNAVAILABLE`·`CAPABILITY_UNSUPPORTED` 등)이면 붙은 연결을 닫는다(앱이 다시 붙으면 그 오류를 받는다).
- 프리셋: `sharp`는 원본 폭·20 Mbps, `balanced`는 최대 2560 폭·12 Mbps, `saver`는 최대 1920 폭·4 Mbps다. 에이전트 스트림은 붙은 연결의 프리셋 가운데 가장 높은 것으로 열고, 그 최고 프리셋이 바뀌면(더 높은 연결이 붙거나 떠나거나 `preset` 제어) `bitrate`와 `config`(`max_width`, `max_fps`는 기본)로 바꾼다.
- 키프레임: 새 연결은 붙자마자 지금 상태(마지막 `info`, 멈춰 있으면 `paused`, 마지막 커서 `shape`·`pos`)를 받고 다음 키프레임부터 AU를 받는다. 데몬은 새 연결이 붙을 때, 앱이 `keyframe`을 보낼 때, 세션이 바뀔 때(`session.changed`), 그리고 30초마다 에이전트에 `keyframe`을 요청한다(0.1초 안의 요청은 하나로 합친다). `paused` 뒤에는 모든 연결이 다음 키프레임부터 AU를 받는다.
- 느린 연결: 연결마다 보내지 못한 프레임이 1024개 또는 16 MiB를 넘으면 그 연결만 프레임을 버리기 시작한다. 쌓인 프레임을 다 보낸 뒤 지금 상태를 다시 보내고 키프레임을 요청해 그 키프레임부터 AU를 이어 보낸다. 다른 연결과 에이전트 스트림은 기다리지 않는다. 10초 동안 쓰기가 끝나지 않는 연결은 닫는다.
- 디스플레이·연결 변화: `display.changed`의 목록에 그 디스플레이가 없으면 데몬이 `0x04`{`reason`:`no_output`}을 보내고(에이전트가 이미 그 사유로 멈췄으면 보내지 않는다), 있으면 키프레임을 요청한다. 에이전트의 `paused`·`info`는 오는 대로 전한다.
- `0x30` 알림 JSON은 `{"type": …}`로 구분한다: `latency`{`capture_to_receive_ms`, `clock_offset_ms`}, `control_denied`{`owner`, `owner_client?`}, `owner_changed`{ControlState 필드}, `connection_state`{`state`, `reason?`}.
  - `latency`: 데몬은 PC와 연결될 때마다 `time.sync`를 5번 불러 왕복이 가장 짧은 표본으로 시계 차(`clock_offset_ms` = 에이전트 시계 − 데몬 시계, 그 왕복의 가운데 기준)를 정하고, 1초마다 그 1초에 받은 AU의 캡처→데몬 수신 시간 평균(`capture_to_receive_ms`)을 보낸다. 그 1초에 AU가 없거나 시계 차를 모르면 보내지 않는다.
  - `owner_changed`: 그 PC의 제어권이 바뀔 때마다(`control.changed`와 같은 필드) 그 PC에 붙은 모든 연결에 보낸다.
  - `connection_state`: PC와의 연결 상태가 바뀔 때마다(`connection.state`와 같은 `state`·`reason`) 보낸다.
  - `control_denied`: 사람 입력을 버렸을 때 연결마다 1초에 한 번까지 보낸다.
- 사람 입력(`0x41`): `seq`와 `gen`은 데몬이 덮어쓴다(앱은 0을 보내도 된다). 이벤트는 PC마다 에이전트 `input` 스트림 하나로 받은 순서대로 간다(처음 입력에 열고, 입력 없이 30초가 지나고 누른 키·버튼이 없으면, 또는 그 PC의 마지막 연결이 떠나면 닫는다). PC와의 연결이 `ready`가 아니면 입력은 바로 버린다(제어권도 얻지 않고, 연결된 뒤에 보내지도 않는다). 이벤트마다 제어권을 확인한다(`control.acquire`와 같은 규칙): 아무도 쥐지 않았으면 그 연결의 클라이언트가 사람 제어권을 얻고(`ai_paused`여도), 사람이 쥐었으면(다른 창·클라이언트 포함) 현재 `gen`으로 보내며, AI 클라이언트의 lease가 살아 있으면 버리고 `control_denied`를 보낸다. 입력 스트림을 여는 데는 3초까지 기다리고, 에이전트가 더 큰 세대로 거절하면 세대를 맞춘 뒤 한 번 다시 확인한다. 쓰기 직전에 사람이 그 `gen`으로 제어권을 쥐고 있는지 다시 확인하고, 아니거나 다시 확인이 거절되면 버리고 `control_denied`를 보낸다. AI 클라이언트(`mcp`)의 연결이 보낸 입력도 버리고 `control_denied`를 보낸다. `release_all`은 사람이 제어권을 쥐었을 때만 그 제어권 상태의 `gen`으로 보내고(연결이 `degraded`여도 보낸다) 제어권을 얻지 않는다. 에이전트의 확인(`0x42`)은 앱에 전하지 않는다(결과는 화면으로 본다). 연결이 떠날 때 그 연결이 누른 채인 키·버튼이 있고 사람이 제어권을 쥐었으면 `release_all`을 보낸다.
- 단축키: 화면 입력을 잡고 있어도 ⌘Q·⌘H·⌘Tab·⌘⇧.·⌘1–⌘7·⌘⌥0·⌘K·⌘,는 Mac에 남는다(설정 "시스템 단축키도 원격으로 보내기"로 ⌘Q·⌘H·⌘Tab만 원격 전달 가능, 기본 끔). 그 밖의 ⌘ 조합은 Cmd↔Ctrl 설정에 따라 원격으로 간다.

`kind:"pty"` (`pty_id` 또는 `job_id`): `0x20`–`0x23` (protocol.md §7과 동일). `pty_id`는 방금 연 셸, `job_id`는 실행 중인 pty job에 다시 붙는다(분리했거나 연결이 끊긴 셸). 붙으면 데몬이 `0x23`{`job`(데몬 job_id), `pid`}을 보내고 이어서 스크롤백(셸의 마지막 출력 최대 1 MiB, protocol.md §7)을 `0x20`으로 다시 보낸다. 앱은 `0x23`을 받을 때마다 현재 크기를 `0x21`로 보낸다. 끝난 job에 붙으면 `NOT_FOUND`.
- 성공 응답의 `result`는 `{job_id, pid, host_id}`. 실패는 JSON-RPC 오류다: 없는 job·pty나 끝난 셸 `NOT_FOUND`, PC에 연결돼 있지 않음 `OFFLINE`(`retryable:true` — 다시 연결되면 붙는다), 셸이 아직 열리는 중 `IN_PROGRESS`, `pty_id`와 `job_id`를 함께 주거나 둘 다 없으면 `INVALID_ARGUMENT`.
- 앱 → 데몬은 `0x20`(입력, 한 프레임 최대 1 MiB)과 `0x21`(`cols`·`rows` 각 1–1000, 범위 밖은 버린다)만 전달되고 다른 종류는 버린다. 데몬 → 앱은 `0x23`, `0x20`, `0x22`다.
- 셸이 끝나면 `0x22`{`code`} 뒤 데몬이 연결을 닫는다. `0x22` 없이 닫히면(PC 연결 끊김) 셸은 PC에서 계속 돌고 있으니 `job_id`로 다시 붙는다. 앱이 연결을 닫으면(분리) 셸은 계속 돈다. 한 셸에 여러 연결이 함께 붙을 수 있고 출력은 모두에게 간다.

스트림 연결 규칙: `stream.attach`는 `hello` 다음 아무 때나 부를 수 있지만 `events.subscribe`를 부른 연결에서는 `INVALID_ARGUMENT`다. 데몬은 그 연결에서 먼저 받은 요청의 응답을 모두 쓴 뒤 attach를 처리하고, 성공 응답 줄 바로 다음 바이트부터 프레임이다(응답 줄 뒤에 앱이 미리 보낸 바이트도 프레임으로 읽는다). 거절된 attach는 오류 응답만 쓰고 연결은 JSON-RPC 그대로 남는다. 모르는 `kind`는 `INVALID_ARGUMENT`다.

## 5. 모델

모든 JSON 키는 snake_case. 시각은 RFC 3339 UTC 밀리초(`2026-10-20T12:34:56.789Z`). 표시 안 한 필드는 항상 있다; `?`는 없을 수 있다.

| 모델 | 필드 |
|---|---|
| Host | `host_id`, `name`, `os`, `arch`, `address`, `port`, `ssh_alias`, `state`(연결 상태), `caps`(기능 이름 → Capability), `session?`(SessionInfo), `displays`([DisplayInfo]), `owner`(`none`/`human`/`ai`), `owner_client?`, `agent_version?`, `connection_generation`, `last_seen?` |
| Capability | `state`(`ready`/`unavailable`/`unsupported`/`needs_user`), `reason?`, `shells?`(`pty` 기능만: 띄울 수 있는 셸, 첫 항목이 기본. Windows는 `powershell`, `pwsh`(PowerShell 7이 설치돼 있을 때), `cmd` 순서, Windows 밖 PC는 `sh`) |
| SessionInfo | `session_id`(정수), `user`, `sid?`, `connect_state`(`active`/`connected`/`disconnected`/`other`/`unknown`), `protocol`(`console`/`rdp`/`unknown`), `locked?`(에이전트가 잠금 상태를 보고하지 않으면 없음), `input_desktop`(`Default`/`Winlogon`/`unknown`), `integrity?` |
| DisplayInfo | `index`, `name`, `adapter`, `x`, `y`, `width`, `height`, `dpi`(정수), `scale`(실수), `primary`, `refresh_hz`(정수) |
| ControlState | `owner`, `owner_client?`(제어권을 쥔 클라이언트의 hello `name`), `owner_client_id?`(그 클라이언트의 `client_id`), `gen`(호스트별 제어권 상태가 바뀔 때마다 — owner(AI면 그 클라이언트까지) 또는 ai_paused — 1씩 오르는 값, 에이전트 writer generation과 같다. 클라이언트는 더 큰 gen만 반영한다), `ai_paused` |
| SessionInspect (`session.inspect` 결과) | SessionInfo의 모든 필드 + ControlState의 모든 필드 + `displays`([DisplayInfo]) + `caps` + `connection_generation` — 한 단계 평평한 객체 |
| Job | `job_id`, `host_id`, `kind`(`exec`/`transfer`/`qa`/`install`/`debug`/`pty`/`diag`), `status`(봉투 status 값), `request_id?`, `summary`, `progress?`(0–1), `created_at`, `updated_at`, `finished_at?`, `result?`(객체, 명령 출력 `stdout`·`stderr`는 넣지 않는다), `output_bytes?`(데몬이 기록한 stdout·stderr 바이트 수. 출력은 `jobs.output`과 artifact로만 읽는다). `kind:"transfer"` job은 전송 하나를 따른다: `status`는 전송이 `queued`·`running`·`verifying`·`paused`면 `running`(`paused`면 `result.paused:true`), `done`이면 `succeeded`, `failed`·`cancelled`면 그대로이고, `result`는 `{transfer_id, state, outcome?, paused?, error?}`다. `failed`·`cancelled` 전송을 `transfer.resume`하면 job도 다시 `running`(`finished_at` 없음)이 된다. `kind:"pty"` job은 셸 하나를 따른다: 셸이 도는 동안 `running`, 끝나면 종료 코드 0은 `succeeded`·그 밖은 `failed`(`result.code`), `jobs.cancel`로 끝내면 `cancelled`, 열지 못했으면 `failed`, PC 에이전트가 다시 시작돼 셸이 사라졌으면 `unknown`(`result.state:"lost"`, `error.code` `EXECUTION_OUTCOME_UNKNOWN`)이다. `result`는 `{shell, pid, client_name?}`이고 끝나면 `state`·`code`가 더해진다. `summary`는 `PowerShell 터미널`처럼 셸 이름이다. `kind:"qa"` job은 QA 실행 하나를 따른다(§10): 실행 중 `running`, 끝나면 `passed` → `succeeded`, `failed`·`blocked` → `failed`, `cancelled` → `cancelled`, `error` → `unknown`이고, `result`는 `{run_id, status(QARun status), steps_passed, steps_failed, steps_blocked, steps_not_run, report_dir?, error?}`, `summary`는 `QA: <시나리오 이름>`이다. 그 job의 `jobs.cancel`은 `qa.cancel`과 같다 |
| FileEntry | `name`, `path`, `dir`, `size`, `mtime`, `link`, `hidden?`, `readonly?` |
| Transfer | `transfer_id`, `host_id`, `direction`(`push`/`pull`/`move_push`/`move_pull`), `local`, `remote`, `on_conflict`, `state`(`queued`/`running`/`paused`/`verifying`/`done`/`failed`/`cancelled`), `bytes_done`, `bytes_total`, `files_done`, `files_failed`, `files_total`, `rate_bps`, `outcome?`(`written`/`skipped`/`renamed`/`copied_source_retained`/`partial`/`failed`), `error?`, `rev`(전송마다 바뀔 때마다 1씩 오르는 정수 — 클라이언트는 더 큰 rev만 반영), `updated_at` |
| TransferDetail | Transfer의 모든 필드 + `files`([TransferFile]) — 한 단계 평평한 객체 |
| TransferFile | `rel`(폴더 전송은 폴더 안 상대 경로, `/` 구분. 파일 하나는 그 이름), `size`, `bytes_done`, `state`, `outcome?`, `sha256?`, `path?`(반영된 실제 경로 — `rename`이면 바뀐 이름), `error?` |
| ProcessInfo | protocol.md §3.1 `proc.list` 항목과 같다(`pid`, `ppid`, `name`, `path`, `cmd`, `mem_kb`, `cpu_pct`, `user?`, `started`, `started_by_deskhand`, `self`) |
| DiagSystem | protocol.md §3.1 `diag.system` 결과와 같다 |
| EventLogEntry | protocol.md §3.1 `diag.events`의 `events` 항목과 같다(`record_id`, `time`, `level`, `provider`, `event_id`, `message`) |
| DebugSession | `debug_id`, `host_id`, `job_id?`, `kind`(`launch`/`attach`), `name`(앱 이름: launch는 실행 파일 이름에서 `.exe`를 뗀 것, attach는 프로세스 이름 또는 `/json/version`의 `Browser`), `path?`, `args`([문자열]), `cwd?`, `pause_on_start`, `pid?`, `ports`{`inspect?`, `remote_debugging?`}(원격 PC의 루프백 포트), `state`(`running`/`paused`/`closed`: 대상 하나라도 멈췄으면 `paused`), `targets`([DebugTarget], main 먼저), `main_unavailable_reason?`(`inspector_disabled`/`inspector_not_responding`: main 없이 renderer만 붙은 세션), `renderer_unavailable_reason?`(`waiting`: `pause_on_start` launch가 main에 붙은 뒤 P2를 기다리는 중(최대 20초), `remote_debugging_disabled`/`remote_debugging_not_responding`: renderer 없이 main만 붙은 세션), `breakpoints`([Breakpoint], 만든 순), `breakpoints_active`, `pause_on_exceptions`(`none`/`uncaught`/`all`), `created_by`(Actor), `created_at`, `closed_at?`, `close_reason?`(`requested`/`process_exited`/`connection_lost`), `exit_code?`, `rev` |
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
| QARun | `run_id`(`q-…`), `job_id`, `host_id`, `name`(시나리오 `name`), `scenario_path?`(이 Mac의 시나리오 파일 절대 경로, 글로 준 시나리오는 없음), `status`(`running`/`passed`/`failed`/`blocked`/`cancelled`/`error`), `rev`(실행마다 바뀔 때마다 1씩 오르는 정수, 데몬을 다시 시작해도 줄지 않는다 — 클라이언트는 더 큰 rev만 반영), `started_at`, `updated_at`, `finished_at?`, `steps_total`, `steps_passed`, `steps_failed`, `steps_blocked`, `steps_not_run`(진행 중에는 아직 끝나지 않은 단계가 이 넷 어디에도 들지 않는다), `current_step?`(실행 중인 단계의 `index`), `cancel_requested`(bool), `error?`({`code`, `message`} — 실행 전체를 멈춘 이유: 시간 초과 `TIMEOUT`, 데몬 재시작 `EXECUTION_OUTCOME_UNKNOWN`), `unsupported`([{`action`, `code`, `reason`}] — 이 PC·데몬이 할 수 없어 `blocked`가 된 단계 종류, 종류마다 한 번), `report_dir?`(끝난 뒤 결과 묶음 폴더), `report?`{`json`, `md`, `html`, `manifest`}(결과 묶음 파일의 절대 경로), `steps`([QAStep] — `qa.run`·`qa.get`·`qa.cancel`에만 있다) |
| QAStep | `index`(1부터, setup·steps·teardown을 이은 실행 순서), `phase`(`setup`/`steps`/`teardown`), `name`, `action`(단계 종류, §10), `line`(시나리오 파일의 줄), `params`(치환 뒤 단계 값, `secret_` 변수는 `***`), `optional`, `continue_on_fail`, `status`(`pending`/`running`/`pass`/`fail`/`blocked`/`not_run` — 실행이 끝나면 `pending`·`running`은 남지 않는다), `started_at?`, `duration_ms?`, `message?`(사람이 읽는 한 줄), `error?`({`code`, `message`}), `assertions`([QAAssertion]), `result?`(종류별 결과 객체, §10), `evidence`([Artifact]) |
| QAAssertion | `kind`(§10), `expected`, `actual`(문자열은 최대 2000자, 넘으면 `…`), `passed` |
| TimelineEntry | `seq`, `ts`, `host_id`, `actor`(`human`/`ai`/`system`), `client_id?`, `client_name?`, `kind`(`input`/`observe`/`exec`/`file`/`control`/`qa`/`debug`/`connection`/`install`), `summary`, `outcome`(`ok`/`failed`/`unknown`/`denied`), `job_id?`, `artifacts`([Artifact]) |
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
| `debug.session_changed` | `debug_id`, `session`(DebugSession) — 세션이 생겼을 때, `state`·`breakpoints_active`·`pause_on_exceptions`·`main_unavailable_reason`·`renderer_unavailable_reason`이 바뀌었을 때 |
| `debug.target_changed` | `debug_id`, `target`(DebugTarget), `removed`(bool), `rev`(바뀐 뒤 세션 `rev`) — 대상이 생기거나 바뀌거나(`title`·`url`·`attached`·`pause_pending`) 사라졌을 때 |
| `debug.breakpoint_changed` | `debug_id`, `breakpoint`(Breakpoint), `removed`(bool), `error?`{`code`, `message`} — 만들기·바꾸기·위치 확정(`Debugger.breakpointResolved`)·지우기, 처음 붙는 대상이 거부해 지운 경우 `error` |
| `debug.paused` | `debug_id`, `state`(PauseState, `paused:true`) |
| `debug.resumed` | `debug_id`, `target_id`, `gen`, `by?`(Actor: `debug.resume`·`debug.step`을 부른 클라이언트) |
| `debug.console` | `debug_id`, `entry`(ConsoleEntry) |
| `debug.console_cleared` | `debug_id`, `through_seq` |
| `debug.closed` | `debug_id`, `reason`(`requested`/`process_exited`/`connection_lost`), `exit_code?`, `rev` |
| `qa.updated` | `run`(QARun — `steps` 없음), `step?`(이번에 바뀐 QAStep 하나). 실행이 시작할 때, 단계가 시작하고 끝날 때, 취소를 받았을 때, 실행이 끝나 결과 묶음을 쓴 뒤에 나간다. `run.rev`가 이벤트마다 1씩 오른다 |

- 데몬은 지난 이벤트를 다시 보내지 않는다. `events.subscribe`는 구독한 뒤에 생긴 이벤트만 주고, 다시 연결했거나 `events.dropped`를 받은 클라이언트는 필요한 상태를 메서드로 다시 읽는다(`host.list`·`jobs.list`·`transfer.get`·`qa.get`·`debug.get`, 콘솔은 §9 `debug.console{cursor}`).
- 데몬은 이벤트마다 `seq`와 요약을 기록해 30일 동안 두고 시작할 때와 24시간마다 오래된 기록을 지운다. 이 기록은 클라이언트에 나가지 않는다. `debug.*`의 기록은 `debug_id`와 순서를 정하는 필드(`debug.console`은 항목 `seq`, `debug.paused`·`debug.resumed`는 `target_id`·`gen`, 그 밖은 `rev` 등)뿐이고 콘솔 글·호출 스택·세션 스냅샷은 남기지 않는다.
- `timeline.list{host_id, since?, limit?}` 결과: `{entries: [TimelineEntry], next_cursor?}`(최신순).
- `host.doctor` 항목 `id`: `ssh_reachable`, `agent_version`, `quic_connect`, `fingerprint`, `session`, `firewall_rule`(규칙이 꺼져 있거나 호스트 `port`가 아닌 포트를 열면 `warn`).

## 7. 오류와 결과 규칙

- **요청 문제는 JSON-RPC 오류**: 잘못되거나 빠진 파라미터, 모르는 호스트·job·메서드, `permanent:true` 없는 `files.delete`. 읽기 메서드(`files.list/stat/read`, `proc.list`, `jobs.*`, `session.inspect`, `host.*`, `debug.list/get/state/variables/scripts/source/console`, `qa.get/list`, `artifacts.list`)의 실패도 JSON-RPC 오류다.
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

- `files.list{host_id, path, recursive?, cursor?, limit?}` → `{host_id, path(실제 경로), entries:[FileEntry], total, next_cursor?, truncated, skipped?}`(§3). `limit` 기본 500·최대 5000, `cursor`는 이전 결과의 `next_cursor`(불투명 문자열). 에이전트 한 번 목록이 5,000개를 넘으면 `truncated:true`. 항목은 폴더가 먼저, 그다음 이름(대소문자 무시) 순이다. `host_id`·`path`·`entries`·`total`(에이전트가 돌려준 전체 항목 수)·`truncated`는 항상 있고, `next_cursor`는 다음 페이지가 있을 때만, `skipped`(에이전트가 정보를 읽지 못해 건너뛴 항목 수)는 그런 항목이 있을 때만 있다. `limit`은 0(또는 생략)이면 기본값 500, 5000 초과·음수나 읽을 수 없는 `cursor`는 JSON-RPC `INVALID_ARGUMENT`다. 데몬은 페이지마다 에이전트에 목록을 다시 요청해 `cursor` 뒤의 항목을 고른다: 폴더가 바뀌어도 `cursor` 뒤에 남은 항목은 중복도 누락도 없이 나오고, `cursor` 앞에 새로 생긴 항목은 보이지 않으며, `total`은 호출마다 달라질 수 있다. `truncated:true`이면 에이전트가 이름순(바이트 순서)으로 앞의 5,000개만 돌려준 것이라 페이지는 그 부분집합 안에서만 이어진다.
- `files.stat{host_id, path, sha256?}` → `{host_id, exists, path, name, dir, size, link, mtime?, owner?, sha256?, locked?}`(§3). 없는 경로는 오류가 아니라 `exists:false`이고 `mtime`·`sha256`이 없다.
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

## 9. 디버깅 세부 계약

- **세션**: 데몬이 세션(`debug_id`)을 소유한다. 앱을 닫아도 세션은 남고, 앱·CLI·MCP가 같은 세션을 쓴다. `debug.*` 이벤트는 그 호스트를 구독한 모든 클라이언트에 간다. 닫힌 세션은 닫힌 뒤 10분 동안 `debug.list`에 `state:"closed"`로 남는다. `created_by`는 세션을 만든 클라이언트다.
- **순서(`rev`·`gen`)**: DebugSession의 `rev`는 세션 필드(`state`, `breakpoints_active`, `pause_on_exceptions`, `main_unavailable_reason`, `renderer_unavailable_reason`, 대상 구성, 닫힘)가 바뀔 때마다, Breakpoint의 `rev`는 그 중단점이 바뀌거나 지워질 때마다 1씩 오른다(Transfer.`rev`와 같은 규칙). 클라이언트는 아는 값보다 큰 `rev`만 반영한다. 지운 중단점·사라진 대상은 지운 뒤의 `rev`를 기억해 그보다 오래된 스냅샷이 되살리지 않게 한다. 대상의 멈춤 필드(`paused`, `pause_pending`)와 PauseState는 대상별 `gen`으로만 순서를 정하고, 대상의 다른 필드(`title`, `url`, `attached`)는 받은 대로 반영한다. `pause/resume/step` 결과의 `target`은 데몬이 응답할 때 `paused`·`pause_pending`·`gen`을 한 번에 읽은 값이다.
- **줄·열**: 이 API의 `line`·`column`은 모두 **1부터** 센다(편집기·V8 스택 문자열 표기). 데몬이 CDP(`lineNumber`·`columnNumber`, 0부터)와 주고받을 때 1을 빼고 더한다. `column`은 UTF-16 단위다. 중단점에 `column`이 없으면 그 줄의 첫 실행 위치다. 줄 나눔은 ECMAScript 줄 끝(LF, CR, CRLF, U+2028, U+2029)을 따른다.
- **launch**: 에이전트가 `debug.ports{count:2}`로 루프백 빈 포트 P1·P2를 받아 사용자 세션에서 `path args… --inspect=127.0.0.1:P1 --remote-debugging-port=P2`로 실행한다. `pause_on_start`면 `--inspect` 대신 `--inspect-brk=127.0.0.1:P1`이다. `cwd` 기본은 `path`의 폴더. 에이전트는 띄운 프로세스와 그 자손 트리를 지켜본다. 띄운 프로세스가 끝나도 자손이 P1·P2를 열면 계속 기다리고, `pid`는 P2(없으면 P1)를 연 프로세스다. 데몬은 P1·P2에 터널을 연다.
  - `pause_on_start`(`--inspect-brk`): main은 main.js를 실행하기 전에 멈춰 기다린다. 데몬은 먼저 P1만 최대 20초 기다리고, 응답하면 main(`type:"node"`)에 붙어 `Runtime.enable`·`Debugger.enable`, 받은 `breakpoints`·`pause_on_exceptions`를 건 뒤 `Runtime.runIfWaitingForDebugger`를 부르고 바로 답한다. main은 첫 줄에서 `reason:"start"`로 멈춘 채 남는다. 답한 세션의 `renderer_unavailable_reason`은 `waiting`이고, 데몬은 이어서 P2의 `/json/version`을 최대 20초 폴링한다: 응답하면 renderer에 붙어 사유를 지우고, 응답하지 않으면 사유를 `remote_debugging_disabled`/`remote_debugging_not_responding`(아래 결과)으로 바꾼다. 어느 쪽이든 `rev`가 오르고 `debug.session_changed`가 나간다. 사유가 바뀐 뒤에도 아래 결과의 규칙대로 P2가 나중에 응답하면 붙는다.
  - `pause_on_start`가 아니면(`--inspect`) main은 바로 실행된다. 데몬은 20초 동안 P1·P2를 함께 기다리고 응답하는 대로 붙으며, 붙는 즉시 `breakpoints`·`pause_on_exceptions`를 건다. 붙기 전에 실행된 코드에서는 멈추지 않는다(시작 코드에서 멈추려면 `pause_on_start`).
  - renderer: 데몬은 P2의 브라우저 대상에 `Target.setAutoAttach{autoAttach:true, waitForDebuggerOnStart:true, flatten:true}`를 걸어, 이후 생기는 페이지가 스크립트를 실행하기 전에 멈추게 하고 같은 중단점·예외 설정을 건 뒤 `Runtime.runIfWaitingForDebugger`로 놓는다.
  - 결과: 둘 다 응답하면 main과 renderer가 붙은 세션. P1만 응답하면 main만 붙은 세션과 `renderer_unavailable_reason`(`remote_debugging_disabled`: P2가 열리지 않음, `remote_debugging_not_responding`: 열렸지만 응답 없음). 세션이 열려 있는 동안 P2가 나중에 응답하면 데몬이 renderer에 붙고 `renderer_unavailable_reason`을 지운다(`debug.target_changed`, `debug.session_changed`). P2만 응답하면(퓨즈 `EnableNodeCliInspectArguments`가 꺼진 앱은 `--inspect`·`--inspect-brk`를 무시한다) renderer만 붙은 세션과 `main_unavailable_reason`(`inspector_disabled`: P1이 열리지 않음, `inspector_not_responding`: 열렸지만 응답 없음). `pause_on_start`에서 main에 붙지 못하면(P1이 20초 안에 응답하지 않거나 응답했지만 붙지 못함) 그때 P2를 최대 20초 확인해 같은 규칙을 따르고, P2도 응답하지 않으면 아래 실패다.
  - 실패: `path`가 없으면 `failed` + `NOT_FOUND`. 어느 쪽에도 붙기 전에 트리의 모든 프로세스가 끝나면(`pause_on_start`도 같다) `failed` + `EXITED_EARLY` + 결과의 `exit_code`(띄운 프로세스의 종료 코드, 흔한 원인은 이미 실행 중인 같은 앱의 단일 인스턴스 잠금). 기다리는 동안 어느 쪽에도 붙지 못하면 `failed` + `TIMEOUT`. 실패하면 데몬이 그 세션이 띄운 프로세스 트리와 그 세션의 터널(`owner` `debug:<debug_id>`)만 정리하고, 세션은 `closed`(`close_reason` 종료 코드가 있으면 `process_exited`, 없으면 `requested`, `debug.closed`), job은 `failed`로 끝난다.
- **attach**: `port`는 원격 PC의 루프백 포트다. 데몬은 `/json/version`의 `Browser`가 `node.js/…`면 main(Node 검사기), 아니면 renderer(DevTools) 포트로 본다. `role`(`main`/`renderer`)을 주면 그대로 쓴다. `pid`면 에이전트가 그 프로세스의 Node 검사기를 지원되는 디버그 신호 방식으로 켜고(Node 문서의 신호: Windows 밖은 `SIGUSR1`; Windows는 공식 신호 경로가 없고 원격 스레드 생성은 쓰지 않으므로 pid 부착이 `failed` + `CAPABILITY_UNSUPPORTED`다 — 그 PC에서는 `--inspect`로 실행한 뒤 `port`를 쓰거나 `debug.electron.launch`로 실행한다), 그 프로세스가 실제로 연 수신 포트를 찾아(프로세스가 소유한 루프백 TCP 수신 소켓) main으로 붙는다. 그 프로세스 명령줄에 `--remote-debugging-port=N`이 있으면 N에도 붙어 renderer를 연다. 검사기를 켤 수 없거나 5초 안에 수신 포트가 생기지 않으면 `failed` + `CAPABILITY_UNSUPPORTED`, 포트가 응답하지 않으면 `failed` + `NOT_FOUND`. `breakpoints`·`pause_on_exceptions`는 붙는 즉시 건다.
- **대상**: 데몬은 main과 `type:"page"` 대상에 자동으로 붙어 `Runtime.enable`·`Debugger.enable`을 켠다(`attached:true`). 다른 종류(`iframe`, `webview`, `worker`, `service_worker`, `shared_worker`, `other`)는 `attached:false`로 목록에만 두고 `debug.target.attach`로 붙는다. 나중에 생긴 page 대상도 자동으로 붙는다. 붙을 때 세션의 중단점·`pause_on_exceptions`·`breakpoints_active`를 그 대상에 다시 건다. 붙지 않은 대상에 `debug.state`·`debug.variables`·`debug.evaluate`·실행 제어를 부르면 JSON-RPC `INVALID_ARGUMENT` + `next_action:"attach_target"`(실행 제어는 봉투 `failed` + 같은 코드).
- **중단점**: 세션 단위다. `match`는 `pattern`을 읽는 방법이다. `file_name`은 URL 경로가 `pattern`으로 끝나는 스크립트(경로 구분자 경계에서, 쿼리 `?…`와 조각 `#…`은 무시)이고 데몬이 정규식(`(^|[/\\])` + 이스케이프한 `pattern` + `([?#].*)?$`)을 만들어 `urlRegex`로 건다. `url`은 정확히 일치(`url`), `url_regex`는 JavaScript 정규식(`urlRegex`)이다. 데몬은 붙은 대상마다 `Debugger.setBreakpointByUrl{…, lineNumber: line-1, columnNumber, condition}`을 부른다. `setBreakpointByUrl`은 잘못된 `urlRegex`도 오류 없이 받아들여 아무 데도 걸지 않으므로, `url_regex`는 데몬이 처음 붙는 대상의 자기 JavaScript 엔진에서 `Runtime.evaluate`로 `new RegExp(<JSON 인코딩한 pattern>)`을 실행해 검사한다: 엔진이 예외를 던지면 그 메시지로 JSON-RPC `INVALID_ARGUMENT`이고, 전송 오류는 `INVALID_ARGUMENT`로 보지 않는다. 붙은 대상이 없으면 정의만 보관하고, 처음 붙는 대상이 거부하면 그 중단점을 지우고 `debug.breakpoint_changed{removed:true, error}`를 보낸다. `pattern`이 비었거나 `line` < 1이면 JSON-RPC `INVALID_ARGUMENT`. `locations`는 모든 대상의 실제 위치를 합친 것이고 스크립트가 나중에 로드되면(`Debugger.breakpointResolved`) 늘어난다. `enabled:false`는 CDP 중단점을 지우되 정의는 남기고, `true`면 다시 건다. `debug.breakpoint.update`의 `condition`이 바뀌면 데몬이 CDP 중단점을 지우고 새 조건으로 다시 건다. `debug.breakpoints.activate{active:false}`는 `Debugger.setBreakpointsActive{active:false}`(정의와 `enabled`는 그대로 두고 모두 멈추지 않게 한다).
- **예외에서 멈춤**: `debug.exceptions{mode}`의 `mode` `none`/`uncaught`/`all` → `Debugger.setPauseOnExceptions{state}`, 붙은 모든 대상에. 새 세션의 기본값은 `uncaught`.
- **일시정지 순서**: DebugTarget·PauseState·`debug.paused`·`debug.resumed`·`pause/resume/step` 결과의 `target`은 같은 대상별 `gen`을 싣는다. 클라이언트는 대상마다 지금 아는 값보다 큰 `gen`만 반영한다. DebugTarget의 `gen`이 클라이언트가 가진 PauseState보다 크고 `paused:true`면 `debug.state`로 호출 스택을 읽는다. 클라이언트는 그 상태를 받기 전까지 그 `gen`을 반영한 것으로 치지 않는다.
- **`reason`**: CDP `Debugger.paused`의 `reason`을 바꿔 싣는다. `other` + `hitBreakpoints`가 있으면 `breakpoint`, `exception` → `exception`, `promiseRejection` → `promise_rejection`, 데몬이 `debug.pause`로 멈췄으면 `pause_request`, `debug.step` 다음에 멈췄으면 `step`, `--inspect-brk` 첫 줄 멈춤은 `start`, 나머지(`debugger;` 문, `assert`, `OOM` 등)는 `other`. `pause_request`·`step`에는 요청한 클라이언트를 `requested_by`로 싣고, 재개는 `debug.resumed{by}`에 싣는다. 실행 제어는 timeline `kind:"debug"`에 같은 행위자로 남는다.
- **실행 제어**: `debug.pause` → `Debugger.pause`(이미 멈춘 대상이면 아무것도 하지 않고 `succeeded`). 실행할 코드가 없어 바로 멈추지 않으면 대상의 `pause_pending:true`를 알린다(`pause_pending`이 바뀌면 그 대상의 `gen`이 1 오르고 `debug.target_changed`가 나가며, 결과의 `target`은 새 `gen`을 싣는다). 다음 JavaScript 실행에서 멈출 때 `pause_pending`이 `false`가 된다. `debug.resume` → `Debugger.resume`(실행 중이면 `succeeded`). 아직 멈추지 않은 `pause_pending`을 `debug.resume`하면 그 보류를 취소하고(엔진은 코드를 실행할 때 그 일시정지를 그래도 한 번 보낸다 — 데몬은 `reason`이 다른 값이 아니고 중단점·단계가 아닌 그 일시정지를 자동으로 재개해 드러내지 않는다), `debug.step{kind}`의 `kind` `over`/`into`/`out` → `Debugger.stepOver`/`stepInto`/`stepOut`. 실행 중인 대상에 `debug.step` 또는 `frame_id`가 있는 `debug.evaluate`를 부르면 `failed` + `STALE_TARGET` + `next_action:"check_state"`. CDP가 실행 제어를 거부하면(이미 멈춘 상태 등) `debug.resume`은 `succeeded`로 두고 그 밖(`debug.pause`·`debug.step`)은 `failed` + 그 코드다.
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

## 10. QA 세부 계약

데몬이 실행(`run_id`)을 소유한다. 앱을 닫거나 CLI·MCP 연결이 끊겨도 실행은 끝까지 가고, 앱·CLI·MCP가 같은 실행을 본다. 시나리오 예: [docs/examples/qa/fixture-smoke.yaml](examples/qa/fixture-smoke.yaml).

### 시나리오 파일

YAML 1.2의 부분집합이다: 블록 매핑·블록 시퀀스, 흐름 시퀀스 `[a, b]`·흐름 매핑 `{k: v}`(여러 줄 가능), 따옴표 없는 값·작은따옴표(`''`로 `'`)·큰따옴표(`\n` `\t` `\\` `\"` `\uXXXX` 등 이스케이프), 블록 값 `|`·`|-`·`>`·`>-`, `#` 주석, 맨 앞 `---` 하나. 앵커·별칭(`&`·`*`)·태그(`!`)·`?` 키·여러 문서·들여쓰기 탭·같은 키 두 번·모르는 키·모르는 단계 종류는 `<파일>:<줄>: <다음 행동을 알려 주는 문장>`의 `INVALID_ARGUMENT`로 거절한다(글로 준 시나리오의 파일 이름은 `scenario`). 오류는 처음 하나만 알린다. 값은 100단계까지 중첩할 수 있고, 닫히지 않은 `[`·`{`는 그것을 연 줄로 알린다. 단계는 setup·steps·teardown을 합해 1000개까지다. Windows 경로는 따옴표 없이(`C:\qa\app.exe`)나 작은따옴표로 쓴다(큰따옴표 안의 `\`는 이스케이프).

| 최상위 키 | 뜻 |
|---|---|
| `name` | 필수. 실행 이름 |
| `description` | 선택 |
| `host` | 선택. host_id 또는 이름. `qa.run`의 `host_id`가 우선하고 둘 다 없으면 `INVALID_ARGUMENT` |
| `vars` | 선택. 이름 → 문자열(숫자·불도 글자 그대로). `qa.run`의 `vars`가 같은 이름을 덮어쓴다 |
| `timeout_ms` | 선택. setup·steps 전체 제한(teardown 제외). `qa.run`의 `timeout_ms`가 우선 |
| `setup` / `steps` / `teardown` | 단계 목록. `steps`는 필수이고 하나 이상 |

- **변수**: 모든 문자열 값의 `${이름}`을 실행 전에 바꾼다(키는 바꾸지 않는다). 이름은 `[A-Za-z_][A-Za-z0-9_]*`, `$${`는 글자 `${`. 정의되지 않은 이름은 그 줄의 `INVALID_ARGUMENT`. 미리 정의된 `run_id`(이 실행의 ID)가 있다. 이름이 `secret_`로 시작하는 변수의 값은 `params`·`message`·`assertions`·`result`·텍스트 증거·결과 묶음과 시나리오 오류 문장에서 `***`로 가린다(따옴표·이스케이프·자르기 전에 가린다. 스크린샷 속 화면은 가리지 못한다). 비밀값은 CLI `--var` 대신 `--var-file`로 넘긴다. `exec`에 변수를 넣을 때는 셸이 해석하는 `cmd` 글 대신 `argv` 항목으로 넣는다(값이 셸 명령으로 실행되지 않는다).
- **경로**: `push`의 `from`이 상대 경로면 시나리오 파일의 폴더(글로 준 시나리오는 `base_dir`, 없으면 `INVALID_ARGUMENT`) 기준이다. `from`이 없으면 실행 전에 `INVALID_ARGUMENT`.
- **단계**: 매핑 하나에 종류 키 하나와 공통 키 `name?`(없으면 종류와 주요 값으로 만든다), `timeout_ms?`(종류별 기본값, 실행 제한이 더 이르면 그때까지), `continue_on_fail?`(기본 false), `optional?`(기본 false). 종류 키가 없거나 둘 이상이면 `INVALID_ARGUMENT`. 글로 줄여 쓴 `launch`·`wait_window`·`key`·`exec`·`assert_screen_text`의 빈 글, `contains`·`not_contains`·`stdout_contains`·`stderr_contains`의 빈 항목도 `INVALID_ARGUMENT`다.
- **선택자**(`<선택자>`): `window?`(창 ID `0x…`, 제목 일부, `foreground`. 없으면 현재 창 — 앞의 `launch`·`wait_window`가 찾은 창 —, 그것도 없으면 전경 창), `role?`(UIA 역할 이름, 예 `button`·`edit`·`text`), `name?`(정확히 일치), `name_contains?`(대소문자 무시), `automation_id?`, `index?`(맞는 것 중 0부터). `window` 말고 하나 이상 필요하다. 에이전트 `ui.*` op에는 `window`·`role`·`name`·`nameContains`·`automationId`·`index`로 간다.

| 종류 | 값 | PC에서 하는 일 | 기본 `timeout_ms` | 제어권 확인 | 필요한 caps |
|---|---|---|---|---|---|
| `push` | `{from, to, on_conflict?(기본 replace), mkdir?(기본 true)}` | `to`의 폴더를 만들고(`fs.mkdir`) 파일·폴더를 보내(transfer push, job과 전송 기록이 따로 남는다) 끝날 때까지 기다린다 | 600000 | — | `files` |
| `launch` | 경로, 또는 `{path, args?, cwd?, wait_window_ms?(기본 15000)}` | `app.launch`. 응답의 창이 현재 창이 된다 | 60000 | 예 | `exec` |
| `wait_window` | 제목 일부, 또는 `{title?, title_contains?, process?, pid?, gone?(기본 false)}`(하나 이상) | `win.list`를 300 ms마다 읽어 맞는 창이 생길 때까지(`gone:true`면 없어질 때까지). 찾은 창이 현재 창이 된다 | 15000 | — | `uia` |
| `click` | `{element: <선택자>}` 또는 `{x, y, window?}`(창 왼쪽 위 기준 화면 픽셀), `button?`(`left`/`right`/`middle`), `count?`(1·2) | 마우스 입력 | 30000 | 예 | `input` |
| `type` | 글, 또는 `{text, method?(auto/unicode/paste)}` | 키보드 글자 입력 | 30000 | 예 | `input` |
| `key` | `ctrl+s` 같은 글, 또는 `{keys}` | 키 입력 | 30000 | 예 | `input` |
| `ui_act` | `{action, value?, <선택자>}` — `action`은 `invoke`/`toggle`/`expand`/`collapse`/`select`/`set_value`(`value` 필요)/`focus`/`scroll_into_view` | `ui.act` | 30000 | 예 | `uia` |
| `assert_ui` | `{<선택자>, exists?(count가 없으면 기본 true), text?, text_contains?, text_matches?, enabled?, count?}` | `ui.find`(최대 200개)와, 글 기준이 있으면 `ui.text`. 모두 맞을 때까지 300 ms마다 다시 본다 | 5000 | — | `uia` |
| `assert_screen_text` | 포함할 글, 또는 `{contains?, not_contains?, matches?, window?}`(`contains`·`not_contains`는 글 또는 글 목록, 하나 이상) | 창의 `ui.tree{filter:"all", max:2000}` 항목(정적 글 포함)의 이름·값(값은 항목마다 앞 200자)을 줄로 이은 글. 맞을 때까지 300 ms마다 | 5000 | — | `uia` |
| `assert_file` | `{path, exists?(기본 true), contains?, not_contains?, matches?, sha256?, size?}` | `fs.stat`(`sha256`이 있으면 해시 포함)과, 내용 기준이 있으면 `fs.read`(앞 8 MiB, UTF-16LE 파일은 UTF-8로 바꿔 비교). 맞을 때까지 300 ms마다 | 5000 | — | `files` |
| `exec` | 명령 글(`cmd`, 셸 기본값), 또는 `{argv? \| cmd?, shell?, cwd?, expect_code?(기본 0), stdout_contains?, stderr_contains?}` | `exec.run{timeout_ms}` | 120000 | — | `exec` |
| `screenshot` | 비움·이름, 또는 `{name?, display?(기본 0), window?, max_width?(기본 1920)}` | `screen.capture{format:"png"}`. `window`(선택자의 `window`와 같은 값)가 있으면 `win.list`로 그 창을 찾고, 창 가운데가 있는 디스플레이(`display.list`로 정하고, 정하지 못하면 `display`)를 `max_width`로 찍어 창 영역만 잘라 저장한다. 창이 그 화면에 보이지 않으면(최소화 등) `NOT_FOUND` | 30000 | — | `screen`(`window`가 있으면 `uia`도) |
| `collect` | `{file, max_bytes?(기본 8 MiB, 최대 64 MiB), name?}`, `{events: {log?, since?(기본 실행 시작 시각), until?, levels?, provider?, max?}, name?}`, `{crashes: {since?(기본 실행 시작 시각)}, name?}` 중 하나 | 파일 끝 `max_bytes`(`fs.read`) / `diag.events` / `diag.crashes`를 증거로 저장 | 120000 | — | `files` / `diag` / `diag` |
| `sleep` | 밀리초 정수, 또는 `{ms}`(최대 600000) | 기다린다 | — | — | — |
| `close` | 비움·창, 또는 `{window?}` | `win.action{action:"close"}` | 15000 | 예 | `uia` |

- `matches`·`text_matches`는 Go 정규식(RE2) 문법이고 잘못된 식은 실행 전 `INVALID_ARGUMENT`다.
- 단계 `result`: `push` `{transfer_id, state, files_total, bytes_total, files:[{rel, size, sha256?, path?}](최대 50개)}`, `launch` `{pid, window?}`, `wait_window` `{window?}`, `ui_act` 에이전트 응답(`node`, `gone?`, `pending?`, `unresponsive?`), `exec` `{code, timed_out, truncated}`, `screenshot` `{width, height, method?}`, `collect` `{path?, size?, collected_bytes?, truncated?, count?}`, `close` `{window?}`. 에이전트에 보낸 요청의 `rid`가 있으면 `request_id`가 붙는다.
- `assertions[].kind`: `wait_window` `window`, `assert_ui` `exists`·`count`·`enabled`·`text`·`text_contains`·`text_matches`, `assert_screen_text` `contains`·`not_contains`·`matches`, `assert_file` `exists`·`size`·`sha256`·`contains`·`not_contains`·`matches`, `exec` `exit_code`·`stdout_contains`·`stderr_contains`. 다시 볼 때마다 마지막으로 본 값으로 바꿔 둔다.

### 실행과 결과

- `qa.run`은 시나리오를 읽고 검사한 뒤 job(kind `qa`)과 실행을 만들고 바로 봉투 `running`으로 답한다. 시나리오 문제·없는 호스트·`scenario_path`와 `scenario`를 함께 주거나 둘 다 없음은 JSON-RPC `INVALID_ARGUMENT`/`NOT_FOUND`다. `scenario_path`는 이 Mac의 절대 경로, `scenario`는 YAML 글(최대 1 MiB), `vars` 값은 문자열·숫자·불, `timeout_ms`는 0 이상. 같은 PC에서 끝나지 않은 실행이 있으면 JSON-RPC `IN_PROGRESS`(`retryable:true`, `next_action:"check_state"`)다. PC에 연결돼 있지 않아도 실행은 시작하고 PC가 필요한 단계가 `blocked`(`OFFLINE`)가 된다.
- 단계는 setup → steps → teardown 순서로 하나씩 실행한다. 단계 `status`:
  - `pass`: 기준이 모두 맞았다.
  - `fail`: 기준이 맞지 않음, PC가 요청을 거절함(`NOT_FOUND`·`INVALID_ARGUMENT`·`PERMISSION_DENIED` 등), 단계 시간 초과(`TIMEOUT`), 명령 종료 코드가 다름.
  - `blocked`: 할 수 없었다 — 사람이 제어권을 쥠(`CONTROL_HELD_BY_HUMAN`·`CONTROL_PREEMPTED`), 이 PC·데몬이 그 기능을 못 함(`CAPABILITY_UNSUPPORTED`), PC 연결 없음(`OFFLINE`·`AUTH_REQUIRED`), 잠금·보안 데스크톱·권한 상승 필요(`SESSION_LOCKED`·`DISPLAY_UNAVAILABLE`·`ELEVATION_REQUIRED`), 결과를 확인하지 못함(`EXECUTION_OUTCOME_UNKNOWN`), 취소.
  - `not_run`: 앞 단계 때문에 건너뜀(이유는 `message`).
- 흐름: `optional`도 `continue_on_fail`도 아닌 단계가 `fail`·`blocked`면 그 뒤의 setup·steps 단계는 `not_run`이다. teardown 단계는 앞의 결과와 상관없이 모두 차례로 실행한다. `optional` 단계는 결과를 그대로 남기지만 흐름과 실행 상태 계산에서 빠진다.
- 실행 `status`: 취소가 단계를 멈추거나 건너뛰게 했으면 `cancelled`, 데몬이 멈춰 끊겼거나 결과 묶음을 쓰지 못했으면 `error`(`error`에 이유, `report` 없음), 그 밖에는 `optional`이 아닌 단계(teardown 포함) 중 `fail`이 있으면 `failed`, 아니면 `blocked`가 있으면 `blocked`, 아니면 `passed`. 실행 제한(`timeout_ms`)은 setup·steps에만 걸리고 그것들이 끝나면 멈춘다: 넘으면 그때 실행 중인 단계가 `fail`(`TIMEOUT`), 남은 setup·steps는 `not_run`, teardown을 실행하고 실행은 `failed` + `error`(`TIMEOUT`)다. 제한이 지났어도 멈추거나 건너뛴 단계가 없으면 결과는 단계로 정한다.
- **제어권**: `launch`·`click`·`type`·`key`·`ui_act`·`close`는 단계마다 실행 직전에 제어권(§3 제어권, design §5.1)을 확인한다. 사람이 쥐고 있으면 PC에 아무것도 보내지 않고 `blocked` + `CONTROL_HELD_BY_HUMAN`("사람이 조작 중입니다 …")이다. 실행기는 제어권을 가져오거나 기다리지 않는다. 확인은 실행을 시작한 클라이언트의 AI 입력으로 한다(누가 시작했든, §3 제어권의 lease를 얻고 갱신한다): 다른 AI 클라이언트의 lease가 살아 있으면 `blocked` + `CONTROL_PREEMPTED`이고, 그 단계의 writer op는 확인에서 받은 `gen`을 싣는다. 다른 단계(관찰·확인·명령·파일)는 제어권과 상관없이 실행한다.
- **기능**: 단계에 필요한 caps(호스트 `caps`, 에이전트가 알린 기능만)가 `ready`가 아니면 PC에 보내지 않고 `blocked` + `CAPABILITY_UNSUPPORTED`(caps의 `reason`)이고 `run.unsupported`에 남는다. `click`·`type`·`key`는 실행을 시작한 클라이언트의 AI 입력(§3 관찰·입력)으로 보낸다: `click`의 화면 픽셀은 그때 읽은 `display.list`로 디스플레이와 그 안의 픽셀로 바꾸고(그 사이 PC와 다시 연결됐으면 `STALE_TARGET`으로 보내지 않는다, 어느 디스플레이에도 없으면 `INVALID_ARGUMENT`), `type`의 `method`와 `key`의 이름은 `input.type`·`input.key`와 같다. 입력이 일부만 실행됐거나 결과를 모르면 그 오류 코드의 단계 결과다. `screenshot`과 실패 스크린샷은 에이전트의 `screen`이 `ready`일 때만 찍는다.
- **취소**: `qa.cancel`은 바로 답한다(`run.cancel_requested:true`, 봉투 `running`). 실행 중인 setup·steps 단계의 요청을 멈춰 그 단계는 `blocked`("취소로 멈췄습니다"), 남은 setup·steps는 `not_run`, 그다음 teardown을 실행하고 실행은 `cancelled`, job은 `cancelled`로 끝난다. 첫 `qa.cancel`은 teardown을 멈추지 않는다(teardown 중에 받아도 teardown은 끝까지 가고, 멈춘 단계가 없으면 결과는 단계로 정한다). 첫 취소 뒤 teardown 중에 다시 받은 `qa.cancel`은 teardown도 멈춘다(실행 중인 teardown 단계 `blocked`, 남은 teardown `not_run`, 실행 `cancelled`). 이미 `cancelled`인 실행은 그대로 돌려주고(봉투 `cancelled`), 다른 상태로 끝난 실행은 JSON-RPC `INVALID_ARGUMENT`다. 진행 중 `push`의 전송은 함께 취소한다.
- **데몬 재시작**: 끝나지 않은 실행은 데몬이 시작할 때 `error` + `error`(`EXECUTION_OUTCOME_UNKNOWN`)로 끝낸다. 실행 중이던 단계는 `blocked`(`EXECUTION_OUTCOME_UNKNOWN`), 남은 단계(teardown 포함)는 `not_run`이고 결과 묶음을 그때 쓴다. 데몬은 단계를 다시 보내지 않는다.
- `qa.list`: `limit` 기본 50·최대 500(0이면 기본값, 그 밖은 `INVALID_ARGUMENT`), `cursor`는 이전 결과의 `next_cursor`(불투명 문자열), 다음 페이지가 있을 때만 `next_cursor`.
- `qa.updated`(§6)는 `run`과 바뀐 `step` 하나를 싣는다. 클라이언트는 `run.rev`가 더 클 때만 반영하고 `step`은 `index`로 맞춘다. `events.dropped`를 받았으면 `qa.get`으로 다시 읽는다.
- 기록: 실행 시작·끝은 timeline `kind:"qa"`(끝 항목의 `artifacts`는 report 파일들), 결과 `outcome`은 `passed` `ok`, `failed`·`cancelled` `failed`, `blocked` `denied`, `error` `unknown`.

### 증거와 결과 묶음

결과 묶음은 `<home>/artifacts/<job_id>/`(`report_dir`)이고, 실행이 끝나면 다음을 쓴다.

```
report.json    기계 판독 결과
report.md      Markdown 보고서(한국어)
report.html    HTML 보고서 한 파일(한국어, 스타일 내장, 스크립트·외부 자원 없음, 이미지는 evidence/… 상대 경로)
manifest.json  묶음 안 모든 파일(자신 제외)의 sha256
evidence/      단계 증거
```

- 증거(`evidence/<NN>-<종류>[-<무엇>].<확장자>`, `NN`은 단계 `index`): `fail`인 단계마다 화면 전체 스크린샷(`-screenshot.png`, `screenshot` kind — `screen` caps가 `ready`일 때), `assert_ui`·`assert_screen_text`가 `fail`이면 그 창의 UI 스냅샷(`-ui-tree.json`, `ui_tree` kind — `ui.tree{filter:"interactive", depth:12, max:300}` 응답), `assert_file` 내용 기준이 `fail`이면 읽은 내용의 끝 64 KiB(`-content.txt`, `log`), `exec`의 비지 않은 stdout·stderr(`-stdout.txt`·`-stderr.txt`, `exec.run`이 돌려준 각 끝 200000바이트, `log`), `screenshot` 단계의 화면(`screenshot`), `collect`가 모은 것(파일은 원래 이름, 로그·텍스트는 `log`, 그 밖은 `file`; 이벤트·크래시 목록은 `report` JSON). 증거는 QAStep `evidence`의 Artifact이고 `artifact_id`는 manifest와 같다.
- `report.json`: `{schema_version:1, generated_at, run(QARun, steps 포함), scenario:{path?, sha256, source}, vars(secret_ 가림), host:{host_id, name, os, arch, address, agent_version?}, environment?(실행 시작 때 읽은 diag.system 결과 필드, 읽지 못하면 없음), daemon_version}`.
- `manifest.json`: `{schema_version:1, run_id, job_id, generated_at, files:[{path(묶음 안 상대 경로, / 구분), artifact_id, kind, mime, size, sha256}]}`(경로 순).
- `artifacts.list{job_id}`: job 폴더에 `manifest.json`이 있으면 그 파일들(`path`는 절대 경로), 없으면 job `result.artifacts`, 그것도 없으면 폴더의 파일(그때 `sha256`을 계산). 모르는 job은 `NOT_FOUND`, 산출물이 없는 job은 빈 목록.
- `artifacts.export{job_id, dir}`: `dir`은 이 Mac의 있는 폴더 절대 경로(아니면 `INVALID_ARGUMENT`). job 폴더 전체를 `<dir>/<job_id>`(있으면 `<job_id> (1)`…)로 복사하고, manifest가 있는 묶음은 폴더의 파일(manifest 제외)이 manifest 목록과 같아야 하고(빠지거나 더 있으면 복사하지 않고 `HASH_MISMATCH`), 복사본의 sha256이 manifest·Artifact와 다르면 복사본을 지우고 `HASH_MISMATCH`, 산출물이 없는 job은 `NOT_FOUND`다. 결과 `{job_id, dir(만든 폴더), files(파일 수), bytes, manifest?(복사한 manifest.json 경로)}`. 상대 경로 이미지 덕분에 복사한 `report.html`도 그대로 열린다.

### CLI·MCP

- CLI: `deskhand qa run <시나리오.yaml> [--host H] [--var 이름=값]… [--var-file 파일] [--timeout 10m] [--detach] [--json]`은 단계가 끝날 때마다 한 줄씩 보이고 결과와 보고서 경로를 출력한다. 종료 코드 0 `passed`, 1 `failed`·`cancelled`, 2 사용법, 3 `blocked`, 4 `error`·결과 불명. Ctrl-C는 실행을 취소하고(teardown까지 최대 1분 기다림), 한 번 더 누르면 `qa.cancel`을 다시 보내 teardown도 멈춘다. `--detach`는 `run_id`만 출력한다. `deskhand qa list [--host H] [--limit N]`, `deskhand qa get <run_id>`, `deskhand qa report <run_id> [--open]`(`--open`은 report.html을 연다), `deskhand qa cancel <run_id>`. `--var-file`은 한 줄에 `이름=값`, `#` 주석.
- MCP: `qa_run`(바로 `run_id`·`job_id`와 `next_action:"qa_get"`), `qa_get`, `qa_list`, `qa_cancel`. 짧은 시나리오를 `scenario` 글로 바로 보내 자유 탐색 중의 확인에도 쓴다.
