# 1단계 실기기 인수 기록

Mac 컨트롤러에서 실제 Windows 11 PC로 에이전트를 설치·페어링하고 1단계 기능을 끝까지 돌린 기록이다. 재현은 `scripts/e2e/phase1.sh`(단계별 실행 가능, `CHECK <단계> PASS|FAIL` 줄로 판정, 종료 코드 = 실패한 확인 수).

- 일시: 2026-10-20 15:44–16:40 (KST). 시각은 Mac 시계 기준이다. PC 시계는 Mac보다 약 0.44 s 늦다(16:37:30 측정 −446 ms: 한 SSH 세션에서 PC가 찍은 시각과 Mac 도착 시각 차의 최댓값). 5단계 시간은 이 차이를 보정했고, 보정하지 않은 두 기기 시각을 섞은 간격에는 ±0.5 s 오차가 있다.
- 최종 판정 실행(수정 1회차 뒤): 커밋 `e949ba1`, 16:36:32–16:39:21, 확인 38개 모두 PASS. 에이전트는 이 실행이 끝난 뒤 `DESKHAND_HOME=/tmp/dh-e2e-H4bO`의 컨트롤러 인증서(SHA-256 `0e3482f1…74fb4b`)와 페어링돼 있다.
- 이전 판정 실행: 커밋 `34d74d0`, `DESKHAND_HOME=/tmp/dh-e2e-V54r`, 16:18:19–16:19:38, 확인 24개 모두 PASS.
- 첫 실행: 커밋 `c0c6e40`(Task 8 끝), `DESKHAND_HOME=/tmp/dh-e2e-nQCl`. 여기서 찾은 결함은 아래 커밋으로 고쳤다.
- 아래 1–4·6단계에서 "최종"이라 적은 증거는 `34d74d0` 실행의 것이다. `e949ba1` 실행에서도 같은 확인이 모두 PASS였고, 5단계와 1b단계는 `e949ba1` 실행의 증거를 따로 적었다.

| 커밋 | 고친 것 |
|---|---|
| `725778f` fix(agent): 콘솔 창 없이 실행하고 설치 스크립트는 Start-Process로 기다린다 | 에이전트 콘솔이 사용자 세션의 Windows Terminal 탭으로 뜨던 문제 |
| `f5992e9` fix(agent): 콘솔 코드 페이지로 나온 명령 출력을 UTF-8로 바꾼다 | `cmd`·네이티브 프로그램의 CP949 출력이 깨지던 문제 |
| `34d74d0` fix(cli): 시작 응답 전에 끝난 명령도 출력과 종료 코드를 따라가고 설치 상세는 한 번만 보인다 | 바로 끝나는 명령에서 `deskhand exec`가 "명령 시작: 실패했습니다"·종료 1로 끝나던 경쟁, 설치 진행 줄 중복 |
| `7d1a522` fix(host): 이미 페어링된 PC는 host add --force로 다시 페어링하고 remove --uninstall은 키까지 지운다 | F1 재등록 막다른 길(수정 1회차 판정) |
| `e8ba981` fix(transport): keep-alive 2초·idle timeout 8초, ready였던 호스트가 응답하지 않으면 재연결 대기를 처음 값으로 | F2 에이전트 재시작 뒤 재연결 22 s(목표 ≤ 15 s, 수정 1회차 판정) |
| `e949ba1` test: E2E에 재등록 흐름과 에이전트 재시작 5회 시간 측정 추가 | `reregister` 단계, 5단계 5회 측정 |

## 환경

| 항목 | 값 |
|---|---|
| Mac | 192.168.88.167, macOS(Darwin 27.0.0, arm64), OpenSSH_10.3p1, Go 1.26.0 교차 빌드 |
| PC | `ssh windows` → 192.168.88.165(Wi-Fi, 같은 /24, 네트워크 프로필 Private), Windows 11 Pro 10.0.26200 AMD64, Windows PowerShell 5.1.26100.9278, OpenSSH_9.5p2 for Windows |
| 계정·세션 | SSH 계정 `desktop-08rkhvp\apxkf`(관리자, SSH 세션은 상승된 세션 0). 사용자는 콘솔 세션 1에 로그인해 있음(Active) |
| 보안 | Windows Defender 실시간 보호 켜짐. 기본 터미널 = Windows Terminal(`HKCU\Console\%%Startup` DelegationTerminal 설정됨) |
| 빌드 | `scripts/build.sh`, `DESKHAND_AGENT_DIR=build`, 보조 도구 `scripts/e2e/apicall`(로컬 API 호출·이벤트 출력), `scripts/e2e/mcpprobe`(go-sdk MCP 클라이언트) |

## 요약

| 단계 | 첫 실행(`c0c6e40`) | `34d74d0` | 최종(`e949ba1`) |
|---|---|---|---|
| 1 설치·페어링 | PASS(브리프 기준) + 결함: 에이전트 창이 사용자 화면에 뜸 | PASS | PASS |
| 2 exec 종료 코드·한글 | FAIL(종료 코드 7은 맞음, stdout 깨짐) | PASS | PASS |
| 3 korean-path | PASS | PASS | PASS |
| 4 결과 불명 확정(A05 근사) | PASS | PASS | PASS |
| 5 에이전트 재시작(A04 일부) | PASS(ready까지 22.3 s) | PASS(22.3 s) | PASS(5회 p50 9.4 s ≤ 15 s) |
| 6 MCP | PASS | PASS | PASS |
| 1b 재등록(remove --uninstall 뒤 add, 새 홈에서 add --force) | FAIL(아래 F1) | FAIL | PASS |
| 8 정리 | — | PASS(에이전트는 남김) | PASS(에이전트는 남김) |
| Task 5 이월: scp 공백 경로, 종료 코드 4 전달, PS 5.1 공백 이름 argv | PASS | PASS | — (다시 돌리지 않음) |

## 1. 설치·페어링 (`host add`)

- 명령: `deskhand host add windows-dev --ssh windows` (새 `DESKHAND_HOME`, 데몬은 `deskhand daemon start`)
- 기대: 단계 `probe→copy→init→install→verify` 출력, 사용자 세션에서 에이전트 실행(`Get-Process deskhand-agent`의 SessionId 1).

**첫 실행 15:44:49, 8.9 s, 종료 0 — PASS(브리프 기준), 결함 발견.**

```
[진행] PC 확인 ...
       PC 확인
[완료] PC 확인
       PC 확인
... (각 단계마다 상세 줄이 두 번)
PC 등록 완료: windows-dev (상태 ready)
Id 41548  SessionId 1  Path C:\Program Files\Deskhand\deskhand-agent.exe
TaskName Deskhand Agent  State Running  Principal apxkf/Interactive/Highest  Action "C:\Program Files\Deskhand\deskhand-agent.exe" run
Deskhand Agent  Inbound Allow  Profile Any  RemoteAddress LocalSubnet  UDP 47600
```

결함: 에이전트(콘솔 서브시스템)가 예약 작업으로 세션 1에서 시작되며 콘솔을 받았고, 기본 터미널 위임으로 사용자의 Windows Terminal에 탭이 생겼다. 세션 1에서 본 창 제목(에이전트로 실행한 `Get-Process | Where-Object MainWindowTitle`):

```
 3356 WindowsTerminal      C:\Program Files\Deskhand\deskhand-agent.exe
```

에이전트 자식으로 `conhost.exe 0x4`가 있었고, 5단계에서 에이전트를 다시 시작한 시각(15:51:15)에 `OpenConsole`이 새로 떴다. 사용자 Windows Terminal 창의 제목이 에이전트 경로였으므로 에이전트 탭이 그 창의 활성 탭이 됐다. 탭을 닫으면 콘솔에 붙은 에이전트에 `CTRL_CLOSE_EVENT`가 가서 에이전트가 끝난다(https://learn.microsoft.com/windows/console/handlerroutine, 실기기에서는 확인 필요). 상세 줄 중복은 CLI가 `active`와 `done` 모두에서 라벨과 같은 상세를 찍었기 때문.

수정(`725778f`): 에이전트를 `-H windowsgui`로 빌드(콘솔 없음). windowsgui 프로그램의 종료 코드를 PowerShell 호출 연산자(`& $exe`)가 받지 못하고, 변수로 받으면 출력도 잃는 것을 실기기에서 확인했다(아래 표). 그래서 설치·검증·제거·doctor 스크립트를 `Start-Process -NoNewWindow -Wait -PassThru` + `exit $p.ExitCode`로 바꿨다. 대화형 콘솔에서 직접 실행할 때를 위해 표준 핸들이 없으면 부모 콘솔에 붙는다(`AttachConsole`). 설치 중 `schtasks`·`netsh`·`taskkill`은 `CREATE_NO_WINDOW`.

| 실행 형식(windowsgui 시험 빌드, SSH `powershell -Command -`) | `version` | `bogus`(기대 2) |
|---|---|---|
| `& $exe …; exit $LASTEXITCODE` | 출력 있음, 종료 0 | 종료 **0** |
| `$o = & $exe …` | 출력 **없음**, 종료 0 | 종료 **0** |
| `Start-Process -NoNewWindow -Wait -PassThru` | 출력 있음, 종료 0 | 종료 **2** |
| `[Diagnostics.Process]` 리디렉션 | 출력 있음, 종료 0 | 종료 2 |

**최종 16:18:26, 11 s, 종료 0 — PASS.**

```
[진행] PC 확인 ...
[완료] PC 확인
[진행] 에이전트 복사 ...
[완료] 에이전트 복사
[진행] 에이전트 초기화 ...
       에이전트 키 만들기
[완료] 에이전트 초기화
[진행] 서비스 설치 ...
       에이전트 설치
[완료] 서비스 설치
[진행] 연결 확인 ...
       설치 확인
[완료] 연결 확인
PC 등록 완료: windows-dev (상태 ready)
agent pid=11632 session=1 path=C:\Program Files\Deskhand\deskhand-agent.exe
console children=0
task state=Running run level=Highest logon=Interactive
rule enabled=True remote=LocalSubnet port=47600/UDP
defender detections (10 min)=0
CHECK add PASS no window in the session is titled deskhand-agent (count 0)
```

`host doctor windows-dev`: `ssh_reachable`·`agent_version 34d74d0`·`quic_connect(세대 1)`·`fingerprint`·`session(세션 1, active)`·`firewall_rule` 모두 통과. Windows Terminal 창 제목은 사용자 자신의 탭(`관리자: …powershell.exe`)으로 돌아왔다. 15:40 이후 Defender 탐지 0건(`Get-MpThreatDetection`).

## 2. exec 종료 코드와 한글 출력

- 명령: `deskhand exec windows-dev -- cmd /c "echo 안녕 & exit 7"`
- 기대: stdout `안녕`, 종료 코드 7.

**첫 실행 15:45:41, 0.56 s — FAIL.** 종료 코드 7은 맞았다. stdout 바이트 `ef bf bd c8 b3 ef bf bd 20 0d 0a`: cmd가 콘솔 코드 페이지 949로 낸 `BE C8 B3 E7`(안녕)을 UTF-8로 읽어 U+FFFD·`ȳ`·U+FFFD가 됐다. 같은 이유로 `ping` 출력도 깨졌다(`Ping 127.0.0.1 32�Ʈ …`). PowerShell 셸은 접두 스크립트가 UTF-8로 바꿔서 정상이었다(`[Console]::Out.Write("한글 출력 😀")` → `한글 출력 😀`).

수정(`f5992e9`): 에이전트가 명령 출력 쓰기마다 UTF-8이면 그대로 두고, 아니면 OEM 코드 페이지(새 콘솔의 코드 페이지, `GetOEMCP`)로 `MultiByteToWideChar` 변환한다. 쓰기 경계에서 잘린 DBCS 문자는 다음 쓰기와 합친다.

**최종 16:18:51 — PASS.** 같은 실행을 16:10에 5회 반복해도 모두 PASS.

```
stdout bytes ec9588eb8595200d, text "안녕 "
CHECK exec PASS exit code 7, want 7
CHECK exec PASS stdout is 안녕
```

중간 실행(16:08:21, 수정 전 CLI)에서 `명령 시작: 실패했습니다`·종료 1이 한 번 나왔다. job은 실제로 실행돼 `failed`/`code 7`로 끝나 있었다. 명령이 `exec.start` 응답 전에 끝나면 시작 봉투가 이미 `failed`(error 없음)인데 CLI가 이를 시작 실패로 읽었다. 수정(`34d74d0`): 시작 봉투가 `succeeded` 또는 error 없는 `failed`이면 명령이 끝난 것으로 보고 출력과 종료 코드를 따라간다(`--detach`는 job id 출력).

## 3. korean-path

- 명령: `deskhand files mkdir 'windows-dev:C:\deskhand-e2e\한글 폴더 😀'` → `files.write`(로컬 API) → `deskhand files cat` → `deskhand files ls`
- 기대: 내용 일치, 이름 일치.
- CLI에는 `files write`가 없다(Task 7 명령 목록에도 없음). 쓰기는 `scripts/e2e/apicall files.write`로 했다.

**첫 실행 15:48:53 — PASS, 최종 16:18:51 — PASS.**

```
폴더를 만들었습니다: C:\deskhand-e2e\한글 폴더 😀
files.write … "size":27,"status":"succeeded"
CHECK files PASS files cat returns the written bytes
이름         크기  수정 시각
파일 😀.txt  27 B  2026-10-20T07:18:50.000Z
remote name 한글 폴더 😀 = D55C AE00 0020 D3F4 B354 0020 D83D DE00
remote name 파일 😀.txt = D30C C77C 0020 D83D DE00 002E 0074 0078 0074
```

이름은 SSH로 PC에서 따로 UTF-16 코드 단위를 읽어 확인했다(😀 = 서로게이트 쌍 D83D DE00). 첫 실행에서 파일 바이트도 SSH로 읽어 `EC-95-88-EB-85-95-20-F0-9F-98-80-0A-…`(보낸 UTF-8 그대로)임을 확인했다.

## 4. 결과 불명 확정 (A05 근사)

- 명령: 데몬을 `DESKHAND_TEST_DROP_AFTER_SEND=exec.run`으로 시작(`deskhand daemon start`가 띄우는 데몬이 환경을 물려받음, 로그 `test hook … is set`) → `apicall exec.run {"cmd":"echo line>>C:\deskhand-e2e\lines.txt","shell":"cmd"}`
- 기대: 데몬이 `req.status`로 결과 확정, 원격 파일 줄 수 1.

**첫 실행 15:49:31 — PASS, 최종 16:18:57 — PASS.**

```
exec.run envelope: {"action":{"executed":true,"outcome":"done"},"code":0,"connection_generation":1,"status":"succeeded",…}
CHECK drop PASS generation 1 -> 2
16:18:56.586223 host windows-dev: connection ended: the connection was dropped by DESKHAND_TEST_DROP_AFTER_SEND
CHECK drop PASS the remote file has 1 line(s), want 1
journal {"rid":"r-01M5C2TX8RJ9B3E7GQ7G7B3Q1A","op":"exec.run","state":"done",…,"res":{"code":0,"ok":true,…}}
connection.state reconnecting 07:18:56.586Z → ready(세대 2) 07:18:57.050Z
job.updated succeeded 07:18:57.055Z
```

응답을 버린 뒤 재연결(464 ms)하고 job이 `succeeded`·`code 0`으로 확정됐다. 에이전트 저널의 같은 rid 기록이 `done` 하나이고 원격 파일이 한 줄이므로 다시 실행되지 않았다. 데몬 로그에는 `req.status` 확정 자체를 적는 줄이 없어서, 확정 경로는 "끊김 로그 + 세대 증가 + 버려진 응답의 결과가 봉투에 있음 + 저널 기록"으로 간접 확인했다.

## 5. 에이전트 재시작 (A04 일부)

- 명령: `deskhand exec windows-dev --detach -- cmd /c "echo before-restart & exit 3"`(끝날 때까지 2 s) → SSH로 `Stop-Process -Id <agent> -Force` → 1 s 뒤 `schtasks /Run /TN "Deskhand Agent"`
- 기대: 데몬 상태 `reconnecting→ready`, 세대 +1, 앞의 `exec.start` job이 `jobs.get`에서 결과 유지.

**첫 실행 15:50:59 — PASS, `34d74d0` 실행 16:19:14 — PASS.**

최종 실행 시각표:

| 시각 | 사건 |
|---|---|
| 16:19:14.506 (PC) | `Stop-Process 11632` |
| 16:19:15.571 (PC) | `schtasks /Run`, 새 에이전트 pid 34132, SessionId 1 |
| 16:19:31.102 | `connection.state` degraded(세대 1) — 죽인 뒤 16.6 s |
| 16:19:36.332 | reconnecting(세대 1) — 죽인 뒤 21.8 s |
| 16:19:36.847 | ready(세대 2) — 새 에이전트 시작 뒤 21.3 s |

```
job before: {"status":"failed",…,"result":{"code":3,"job":"j3","pid":38256,"state":"exited"}}
job after:  {"status":"failed",…,"result":{"code":3,"job":"j3","pid":38256,"state":"exited"}}
CHECK restart PASS connection.state went reconnecting -> ready
CHECK restart PASS generation 1 -> 2
CHECK restart PASS jobs.get keeps the earlier exec.start result
```

**수정 1회차 뒤(`e8ba981`, 실행 `e949ba1`) 16:37:30 — PASS.** keep-alive 2 s·idle timeout 8 s, ready였던 호스트에 응답 없는 다이얼은 backoff를 0.5 s로 되돌린다. 같은 절차(`Stop-Process` → 1 s 뒤 `schtasks /Run`)를 5회 돌렸다. 시간은 PC가 찍은 kill 시각(시계 차 −446 ms 보정)부터 데몬의 `ready` 이벤트까지.

| 회 | 상태 이벤트(UTC) | 세대 | kill → ready |
|---|---|---|---|
| 1 | reconnecting 07:37:43.063 → ready 07:37:43.627 | 1 → 2 | 8.6 s |
| 2 | reconnecting 07:37:55.646 → ready 07:37:56.098 | 2 → 3 | 9.4 s |
| 3 | reconnecting 07:38:08.175 → ready 07:38:08.772 | 3 → 4 | 9.9 s |
| 4 | reconnecting 07:38:20.956 → ready 07:38:21.542 | 4 → 5 | 9.3 s |
| 5 | reconnecting 07:38:33.894 → ready 07:38:34.457 | 5 → 6 | 10.4 s |

p50 9.4 s(목표 ≤ 15 s). 5회 모두 `degraded` 없이 `reconnecting → ready`. 수정 전 22.3 s의 대부분은 20 s 유휴 시간 초과를 기다린 시간이었다. 처음(5회 전)에 만든 `exec.start` job은 5번 재시작 뒤에도 `jobs.get` 결과(`failed`, `code 3`, `state exited`)가 같다. 직전 실행(16:34, 빌드 `e8ba981-dirty`: 같은 코드에 E2E 스크립트만 커밋 전)에서도 8.6 / 9.9 / 10.1 / 9.8 / 10.4 s, p50 9.9 s였다.

첫 실행에서는 죽일 때 돌고 있던 job(`ping -n 60 127.0.0.1`)도 봤다: 재연결 뒤 `status:"unknown"`, `result.state:"lost"`, 출력 앞부분 132바이트는 남았다(`jobs.output`). 에이전트가 죽은 뒤에도 `PING` 프로세스는 계속 돌았다(F3). 첫 실행 데몬 로그: `connection ended: timeout: no recent network activity`.

## 6. MCP

- 명령: `mcpprobe -deskhand build/deskhand 'hosts_list={}' 'exec_run={"host":"windows-dev","argv":["C:\\Windows\\System32\\hostname.exe"]}'`(go-sdk `CommandTransport`로 `deskhand mcp` 실행)
- 기대: `hosts_list`, `exec_run` 호출 성공.

**첫 실행 15:52:32 — PASS, 최종 16:19:37 — PASS.**

```
{"server":{"name":"deskhand","version":"34d74d0"},"tools":["app_launch","exec_run","exec_start","files_delete","files_list","files_mkdir","files_move","files_read","files_stat","files_write","host_doctor","hosts_list","job_cancel","job_get","job_output","proc_kill","proc_list","session_inspect"]}
hosts_list: is_error false, windows-dev ready, connection_generation 2
exec_run:   is_error false, elapsed_ms 50, status succeeded, code 0, stdout "DESKTOP-08RKHVP\r\n"
```

첫 실행에서 `exec_run {"cmd":"[Console]::Out.Write(\"한글 출력 😀\"); exit 5","shell":"powershell"}` → `is_error false`, `status failed`, `code 5`, stdout `한글 출력 😀`(754 ms).

## 1b. 재등록 (수정 1회차)

- 명령: ① `deskhand host remove windows-dev --uninstall` → `deskhand host add windows-dev --ssh windows`. ② 새 `DESKHAND_HOME`(`/tmp/dh-e2e-H4bO`)에서 `deskhand host add windows-dev --ssh windows` → `deskhand --json host add …` → `deskhand host add windows-dev --ssh windows --force`
- 기대: ① `remove --uninstall`이 PC의 에이전트 키까지 지워서 이어진 일반 `host add`가 된다. ② 새 홈의 일반 `host add`는 실패하고 다음 단계로 정확히 `deskhand host add windows-dev --ssh windows --force`를 알려 준다(`next_action:"host_add_force"`), `--force`는 페어링한다.

**첫 실행(16:05) — FAIL**(F1). **최종 16:38:36–16:39:18 — PASS.**

```
PC windows-dev를 제거하고 에이전트도 삭제했습니다
left task=False rule=False program=False data=False
CHECK reregister PASS plain host add after remove --uninstall
new Mac home /tmp/dh-e2e-H4bO
[실패] 에이전트 초기화: 이 PC에는 이미 Deskhand 에이전트가 페어링돼 있습니다. 이 Mac과 다시 페어링하려면 deskhand host add windows-dev --ssh windows --force 를 실행하세요
다음 단계: deskhand host add windows-dev --ssh windows --force 를 실행하세요
CHECK reregister PASS host add from the new home exits 1 (exit 1)
json: {"status":"failed","error":{"code":"INVALID_ARGUMENT","message":"…deskhand host add windows-dev --ssh windows --force 를 실행하세요","next_action":"host_add_force","retryable":false},"next_action":"host_add_force"}
PC 등록 완료: windows-dev (상태 ready)
CHECK reregister PASS deskhand host add windows-dev --ssh windows --force exits 0
CHECK reregister PASS exec after add --force (DESKTOP-08RKHVP)
```

PC의 `agent.json` `controller_fingerprints`는 `0e3482f1f3a4a5884d8781410b7c3b9fad853438ccc96c587c5f13e71574fb4b`로 `/tmp/dh-e2e-H4bO/identity/controller.crt`의 SHA-256과 같다. `host repair`는 등록된 호스트에만 쓴다(변경 없음).

## 8. 정리

**최종 16:39:18 — PASS.** `C:\deskhand-e2e`와 `%TEMP%\deskhand-agent-*.exe.old` 삭제, 에이전트 유지(`agent running=True`), Mac 데몬 정지. 최종 PC 상태: 에이전트 pid 34512 SessionId 1(16:39:15 시작), 작업 Running, `%TEMP%\deskhand-install` 없음, `C:\deskhand-test` 없음, `windows-mcp-server` 작업 Running(건드리지 않음), 15:40 이후 Defender 탐지 0건.

에이전트는 `/tmp/dh-e2e-H4bO/identity`의 컨트롤러 인증서와 페어링돼 있다(`/tmp`는 Mac 재부팅 때 지워질 수 있다). 다른 `DESKHAND_HOME`에서는 `deskhand host add windows-dev --ssh windows --force`로 다시 페어링한다. 이전 실행의 홈들은 지웠다.

## Task 5 이월 확인

| 항목 | 명령 | 결과 | 판정 |
|---|---|---|---|
| ssh가 종료 코드 4를 전달 | 키가 있는 PC에 15:57:43 `deskhand host add 'windows dev 둘' --ssh windows`(콘솔 빌드, `& $exe`), 16:05:17 `deskhand host add windows-dev --ssh windows`(windowsgui 빌드, `Start-Process`) | 두 경우 모두 init 종료 4 → `이 PC에는 이미 Deskhand 에이전트가 페어링돼 있습니다…`, CLI 종료 1 | PASS |
| PS 5.1 네이티브 argv에 공백·한글 이름 | windowsgui 빌드로 `init --name "PC 이름 테스트" … --config C:\deskhand-e2e\gui\cfg\agent.json`을 제품과 같은 base64 + `Start-Process` 형식으로 두 번 | 1회: 종료 0, `{"name":"PC 이름 테스트"}`, agent.json `"name": "PC 이름 테스트"`. 2회: 종료 4 | PASS |
| scp 공백 경로 | `scp -o BatchMode=yes -o ClearAllForwardings=yes -- "…/scp test.txt" "windows:C:/deskhand-e2e/a b/scp test.txt"`(`SSH.Copy`와 같은 argv) | 종료 0, 원격 내용 일치 | PASS |

제품의 복사 경로는 `%TEMP%` 8.3 짧은 경로라 이 PC(`C:\Users\apxkf\AppData\Local\Temp`)에서는 공백이 없다. 공백 경로는 위 직접 시험으로 확인했다.

## 측정치

최종 빌드, Mac→PC Wi-Fi, 각 10회, 프로세스 시작·소켓 연결 포함:

| 동작 | 최소 / 중앙 / 최대 |
|---|---|
| `exec.run` hostname.exe(argv) | 47 / 51 / 118 ms |
| `exec.run` `cmd` echo | 46 / 49 / 110 ms |
| `exec.run` PowerShell `Get-Date` | 943 / 967 / 1040 ms |
| `files.stat` | 8 / 10 / 77 ms |
| `deskhand exec` hostname(CLI: exec.start + jobs.output 500 ms 간격 조회) | 106 / 557 / 573 ms |
| MCP `exec_run` hostname | 50 ms(1회) |
| `host add` 전체 | 11 s(최종), 8.9 s(첫 실행) |
| 연결 끊김(테스트 훅) → ready | 464 ms |
| 에이전트 강제 종료 → degraded / reconnecting / ready (`34d74d0`) | 16.6 s / 21.8 s / 22.3 s |
| 에이전트 강제 종료 → ready (`e949ba1`, 5회) | 8.6 / 9.4 / 9.9 / 9.3 / 10.4 s, p50 9.4 s |

## 발견 사항과 남은 문제

- **F1 (해결: `7d1a522`, 1b 단계 PASS) 이미 페어링된 PC를 다시 등록할 길이 없었다.** `deskhand host remove windows-dev --uninstall`(16:05:04)은 `uninstall`을 `--purge` 없이 부르므로 `C:\ProgramData\Deskhand\agent.key`가 남는다. 이어서 `host add windows-dev`는 init 종료 4로 실패하며 `deskhand host repair <이름>`을 안내하지만, 등록에 실패한 이름이라 `host repair windows-dev`는 `'windows-dev' PC가 등록돼 있지 않습니다`로 끝난다(16:05:17). 다른 Mac·새 홈에서 페어링하는 경우도 같다. 안내 문구의 `<이름>`도 채워지지 않는다. 판정에 따라 `host add --force`(`host.add{force:true}`)가 이 Mac에 기록이 없어도 다시 페어링하고, 등록되지 않은 호스트의 이미 페어링됨 오류는 정확히 그 명령을 알려 주며(`next_action:"host_add_force"`), `host remove --uninstall`은 `uninstall --purge`로 키까지 지운다. `reset` 단계는 그대로 둔다(설치 흔적을 처음부터 지우는 용도).
- **F2 (해결: `e8ba981`, 5회 p50 9.4 s) 죽은 에이전트를 알아차리고 다시 붙는 데 약 22 s 걸렸다.** 새 에이전트는 1 s 만에 떴지만 데몬은 QUIC 유휴 시간 초과(20 s, `no recent network activity`)까지 옛 연결을 붙잡았다. 판정대로 keep-alive 2 s·idle timeout 8 s로 줄이고, ready였던 호스트에 아무것도 답하지 않는 다이얼(핸드셰이크·다이얼 시간 초과, 연결 거부)은 backoff를 0.5 s로 되돌린다. 남은 시간의 대부분은 8 s 유휴 시간 초과다. 30 s 네트워크 단절 모의는 이번 범위가 아니다.
- **F3 (후속 과제) 에이전트가 강제 종료되면 자식 프로세스가 남는다.** `ping`이 에이전트 사망 뒤에도 돌았고 job은 `unknown`/`lost`가 됐다. Windows의 `prepareTree`는 아무것도 하지 않는다(job object 없음).
- **F4 (관찰) `deskhand exec`는 명령이 바로 끝나도 약 0.5 s 걸린다.** CLI가 `jobs.output`을 500 ms 간격으로 조회한다(`exec.run` 직접 호출은 51 ms).
- **F5 (관찰) 세션 정보가 일부 비어 있다.** `protocol unknown`, `input_desktop unknown`. 이 브랜치 에이전트에 `session.info`가 없다(미디어 브랜치에서 추가, 병합 때 해결 예정).
- **F6 (관찰) 실패한 `host add`가 `%TEMP%\deskhand-install` 복사본을 남긴다.** init 단계에서 실패하면 지우지 않는다(15:57 실행 뒤 `temp-left=True`, Task 5 이월 사소 항목). 설치된 exe로 `uninstall`하면 실행 중인 exe를 `%TEMP%\deskhand-agent-<pid>.exe.old`로 옮긴 채 둔다(`cmd/deskhand-agent/install_windows.go` `removeInstallDir`, 이번 실행에서 파일을 따로 보지는 않았다).
- **F7 (후속 과제) CLI에 `files write`가 없다.** 브리프 3단계의 쓰기는 로컬 API로 했다.
- **F8 (확인 필요) windowsgui 에이전트를 대화형 콘솔에서 직접 실행할 때.** 표준 핸들이 없으면 부모 콘솔에 붙어 출력하도록 했지만, 사용자 콘솔에 입력을 넣을 수 없어 실기기에서 확인하지 못했다. SSH에서는 `& $exe`가 windowsgui 프로그램의 종료 코드를 잃는 것을 확인했다(1단계 표). 대화형 콘솔에서도 같은지는 확인 필요. 수동 복구 안내(`run deskhand-agent install again from an elevated prompt` 등)는 `Start-Process -Wait -NoNewWindow -PassThru` 형식을 알려 주도록 고칠지 결정이 필요하다.
- **F9 (후속 과제) `req.status` 확정을 데몬 로그로 직접 볼 수 없다.** 4단계 증거는 간접이다. 확정 때 rid와 결과를 적는 데몬 로그 줄이 필요하다.

수정 1회차 판정에 따라 F3·F7·F9는 이번에 고치지 않고 후속 과제로 남긴다.
