# 1단계 실기기 인수 기록

Mac 컨트롤러에서 실제 Windows 11 PC로 에이전트를 설치·페어링하고 1단계 기능을 끝까지 돌린 기록이다. 재현은 `scripts/e2e/phase1.sh`(단계별 실행 가능, `CHECK <단계> PASS|FAIL` 줄로 판정, 종료 코드 = 실패한 확인 수).

- 일시: 2026-10-20 15:44–16:20 (KST). 시각은 Mac 시계 기준이고, PC 시계는 Mac보다 약 0.4 s 빠르다(에이전트 저널 시작 시각이 데몬의 job 생성 시각보다 앞선다). 두 기기 시각을 섞은 간격에는 ±0.5 s 오차가 있다.
- 최종 판정 실행: 커밋 `34d74d0`, `DESKHAND_HOME=/tmp/dh-e2e-V54r`, 16:18:19–16:19:38, 확인 24개 모두 PASS.
- 첫 실행: 커밋 `c0c6e40`(Task 8 끝), `DESKHAND_HOME=/tmp/dh-e2e-nQCl`. 여기서 찾은 결함은 아래 커밋으로 고쳤다.

| 커밋 | 고친 것 |
|---|---|
| `725778f` fix(agent): 콘솔 창 없이 실행하고 설치 스크립트는 Start-Process로 기다린다 | 에이전트 콘솔이 사용자 세션의 Windows Terminal 탭으로 뜨던 문제 |
| `f5992e9` fix(agent): 콘솔 코드 페이지로 나온 명령 출력을 UTF-8로 바꾼다 | `cmd`·네이티브 프로그램의 CP949 출력이 깨지던 문제 |
| `34d74d0` fix(cli): 시작 응답 전에 끝난 명령도 출력과 종료 코드를 따라가고 설치 상세는 한 번만 보인다 | 바로 끝나는 명령에서 `deskhand exec`가 "명령 시작: 실패했습니다"·종료 1로 끝나던 경쟁, 설치 진행 줄 중복 |

## 환경

| 항목 | 값 |
|---|---|
| Mac | 192.168.88.167, macOS(Darwin 27.0.0, arm64), OpenSSH_10.3p1, Go 1.26.0 교차 빌드 |
| PC | `ssh windows` → 192.168.88.165(Wi-Fi, 같은 /24, 네트워크 프로필 Private), Windows 11 Pro 10.0.26200 AMD64, Windows PowerShell 5.1.26100.9278, OpenSSH_9.5p2 for Windows |
| 계정·세션 | SSH 계정 `desktop-08rkhvp\apxkf`(관리자, SSH 세션은 상승된 세션 0). 사용자는 콘솔 세션 1에 로그인해 있음(Active) |
| 보안 | Windows Defender 실시간 보호 켜짐. 기본 터미널 = Windows Terminal(`HKCU\Console\%%Startup` DelegationTerminal 설정됨) |
| 빌드 | `scripts/build.sh`, `DESKHAND_AGENT_DIR=build`, 보조 도구 `scripts/e2e/apicall`(로컬 API 호출·이벤트 출력), `scripts/e2e/mcpprobe`(go-sdk MCP 클라이언트) |

## 요약

| 단계 | 첫 실행(`c0c6e40`) | 최종(`34d74d0`) |
|---|---|---|
| 1 설치·페어링 | PASS(브리프 기준) + 결함: 에이전트 창이 사용자 화면에 뜸 | PASS |
| 2 exec 종료 코드·한글 | FAIL(종료 코드 7은 맞음, stdout 깨짐) | PASS |
| 3 korean-path | PASS | PASS |
| 4 결과 불명 확정(A05 근사) | PASS | PASS |
| 5 에이전트 재시작(A04 일부) | PASS | PASS |
| 6 MCP | PASS | PASS |
| 8 정리 | — | PASS(에이전트는 남김) |
| Task 5 이월: scp 공백 경로, 종료 코드 4 전달, PS 5.1 공백 이름 argv | PASS | PASS |
| 재페어링 흐름(remove --uninstall 뒤 add) | FAIL(아래 F1) | FAIL(미수정, 결정 필요) |

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

**첫 실행 15:50:59 — PASS, 최종 16:19:14 — PASS.**

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

## 8. 정리

**최종 16:19:37 — PASS.** `C:\deskhand-e2e` 삭제(`e2e folder left=False`), 에이전트 유지(`agent running=True`), Mac 데몬 정지. 최종 PC 상태: 에이전트 pid 34132 SessionId 1, 작업 Running, `%TEMP%\deskhand-install`·`deskhand-agent-*.exe.old` 없음, `C:\deskhand-test` 없음, `windows-mcp-server` 작업 Running(건드리지 않음), 15:40 이후 Defender 탐지 0건.

에이전트는 `/tmp/dh-e2e-V54r/identity`의 컨트롤러 인증서와 페어링돼 있다. 다른 `DESKHAND_HOME`에서 `host add`하면 F1 때문에 실패한다. 이 홈을 쓰거나 `scripts/e2e/phase1.sh reset add`로 새로 페어링한다.

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
| 에이전트 강제 종료 → degraded / reconnecting / ready | 16.6 s / 21.8 s / 22.3 s |

## 발견 사항과 남은 문제

- **F1 (FAIL, 결정 필요) 이미 페어링된 PC를 다시 등록할 길이 없다.** `deskhand host remove windows-dev --uninstall`(16:05:04)은 `uninstall`을 `--purge` 없이 부르므로 `C:\ProgramData\Deskhand\agent.key`가 남는다. 이어서 `host add windows-dev`는 init 종료 4로 실패하며 `deskhand host repair <이름>`을 안내하지만, 등록에 실패한 이름이라 `host repair windows-dev`는 `'windows-dev' PC가 등록돼 있지 않습니다`로 끝난다(16:05:17). 다른 Mac·새 홈에서 페어링하는 경우도 같다. 안내 문구의 `<이름>`도 채워지지 않는다. 고치려면 `host add`의 강제 재페어링(예: `--force`) 또는 `remove --uninstall`의 purge 같은 계약 결정이 필요해 고치지 않았다. E2E는 PC에서 `uninstall --purge`와 데이터 폴더 삭제로 우회했다(`reset` 단계).
- **F2 (관찰) 죽은 에이전트를 알아차리고 다시 붙는 데 약 22 s.** 새 에이전트는 1 s 만에 떴지만 데몬은 QUIC 유휴 시간 초과(`no recent network activity`)까지 옛 연결을 붙잡았다. 에이전트가 키에서 유도한 고정 stateless reset 키를 쓰면 재시작 직후 끊김을 알 수 있다(확인 필요).
- **F3 (관찰) 에이전트가 강제 종료되면 자식 프로세스가 남는다.** `ping`이 에이전트 사망 뒤에도 돌았고 job은 `unknown`/`lost`가 됐다. Windows의 `prepareTree`는 아무것도 하지 않는다(job object 없음).
- **F4 (관찰) `deskhand exec`는 명령이 바로 끝나도 약 0.5 s 걸린다.** CLI가 `jobs.output`을 500 ms 간격으로 조회한다(`exec.run` 직접 호출은 51 ms).
- **F5 (관찰) 세션 정보가 일부 비어 있다.** `protocol unknown`, `input_desktop unknown`. 이 브랜치 에이전트에 `session.info`가 없다(미디어 브랜치에서 추가, 병합 때 해결 예정).
- **F6 (관찰) 실패한 `host add`가 `%TEMP%\deskhand-install` 복사본을 남긴다.** init 단계에서 실패하면 지우지 않는다(15:57 실행 뒤 `temp-left=True`, Task 5 이월 사소 항목). 설치된 exe로 `uninstall`하면 실행 중인 exe를 `%TEMP%\deskhand-agent-<pid>.exe.old`로 옮긴 채 둔다(`cmd/deskhand-agent/install_windows.go` `removeInstallDir`, 이번 실행에서 파일을 따로 보지는 않았다).
- **F7 (관찰) CLI에 `files write`가 없다.** 브리프 3단계의 쓰기는 로컬 API로 했다.
- **F8 (확인 필요) windowsgui 에이전트를 대화형 콘솔에서 직접 실행할 때.** 표준 핸들이 없으면 부모 콘솔에 붙어 출력하도록 했지만, 사용자 콘솔에 입력을 넣을 수 없어 실기기에서 확인하지 못했다. SSH에서는 `& $exe`가 windowsgui 프로그램의 종료 코드를 잃는 것을 확인했다(1단계 표). 대화형 콘솔에서도 같은지는 확인 필요. 수동 복구 안내(`run deskhand-agent install again from an elevated prompt` 등)는 `Start-Process -Wait -NoNewWindow -PassThru` 형식을 알려 주도록 고칠지 결정이 필요하다.
- **F9 (관찰) `req.status` 확정을 데몬 로그로 직접 볼 수 없다.** 4단계 증거는 간접이다.
