# 3단계 실기기 인수 기록

Mac 데몬·CLI와 실제 Windows PC의 Deskhand 에이전트 사이에서 3단계 기능(명령·터미널, 파일 전송·이동·충돌·재개·무결성, 진단, 터널, CDP 디버거, QA 시나리오)을 끝까지 돌린 기록이다. 재현은 `scripts/e2e/phase3.sh`(단계별로 실행 가능, 줄마다 `CHECK <단계> PASS|FAIL|BLOCKED|NOT_RUN <무엇>`, 종료 코드 = FAIL 수). 설치된 `Deskhand Agent`와 사용자의 기본 Deskhand 홈은 쓰지 않는다: 이 브랜치의 에이전트를 PC의 실행 폴더에서 따로 띄우고 Mac의 임시 홈과 페어링한다.

**판정 실행: 커밋 `ef46504`, 07:45:38–07:54:23, 확인 135개 — PASS 133 · FAIL 0 · BLOCKED 1 · NOT_RUN 1.** 앞선 전체 실행 두 번(커밋 `900df2e`, 07:26:11–07:34:09 PASS 126, 07:34:45–07:43:29 PASS 133)도 FAIL 0 · BLOCKED 1 · NOT_RUN 1이었다. 두 번째 실행의 로그에서 디버그 세션의 대상 순서가 계약과 다른 것을 보고 `ef46504`로 고친 뒤 하네스에 그 확인을 더해 판정 실행을 했다.

| 판정 | 항목 |
|---|---|
| BLOCKED | QA `fixture-smoke.yaml`의 GUI 단계 — PC에 로그인한 사용자가 없어(Windows Update 재부팅 뒤 로그온 화면) 에이전트가 Session 0에서 돌았고, 그 세션의 `deskhand-fixture.exe`는 창 없이 뜬다(`MainWindowHandle 0`). push·launch는 PASS, `wait_window`가 시간 초과. 결과 묶음과 export는 PASS |
| NOT_RUN | A15 디스크 부족 — Mac·PC 디스크를 채우거나 작은 볼륨을 붙이는 것은 이번 범위 밖. 에이전트의 DISK_FULL 경로는 `TestCommitFailureLeavesNoCorruptTarget/disk_full_while_receiving`(모의 `ERROR_DISK_FULL`) |
| 사용자 세션 미검증 | 테스트 에이전트를 사용자 세션(대화형 예약 작업)에서 돌리는 경로, 그 세션의 Electron 창·QA GUI 단계, `wintest.sh` 기본(작업) 모드 — 모두 로그인한 사용자가 필요하다. 하네스는 사용자가 있으면 자동으로 그 경로를 쓴다(`DESKHAND_E2E_AGENT_MODE=auto`) |

이번에 고친 결함 6건(아래 "고친 결함"): `exec.start` 표준 입력이 EOF를 받지 못함, 실제 V8의 빈 호출 프레임 URL, `deskhand debug launch/attach`가 성공을 종료 4로 처리, 디버그 세션 대상 순서(main 먼저가 아님), 1단계부터 PC에서만 실패하던 클립보드 테스트(감시 시작 경쟁, 제품 수정)와 자식 환경 테스트(테스트의 잘못된 가정).

## 실행 환경과 경로

| 항목 | 값 |
|---|---|
| Mac | macOS Darwin 27.0.0 arm64, Go 1.25.0, 이 세션은 Kiln.app 아래에서 실행 |
| PC | `ssh windows` → 192.168.88.165, Windows 11 Pro 25H2 (26200.9711), AMD Ryzen 7 3800X 8코어, 메모리 32 GiB, PowerShell 5.1과 7 |
| PC 상태 | 05:34–05:40 Windows Update가 두 번 재부팅(이벤트 1074 `TrustedInstaller.exe` "운영 체제: 업그레이드(계획됨)"). 실행하는 동안 콘솔에 로그인한 사용자가 없었다(`qwinsta`의 console 세션 1에 사용자 없음, `LogonUI` 실행 중, `explorer.exe` 없음). 설치된 `Deskhand Agent` 작업은 `Ready`(로그온 때 시작, 실행 중 아님)로 그대로 두었다 |
| 네트워크 | 이 세션의 비플랫폼 실행 파일은 192.168.88.165에 닿지 못한다(Go로 만든 확인 프로그램의 TCP 22 연결: `connect: no route to host`, 06:19). `/usr/bin/ssh`는 된다 → macOS 로컬 네트워크 권한(판정대로 SSH 중계로 진행) |

### SSH 중계 (`scripts/e2e/udprelay`)

판정은 `ssh -L`로 잇는 UDP↔TCP 중계였다. 그런데 이 저장소의 SSH 규칙 `-o ClearAllForwardings=yes`는 명령줄의 `-L`까지 지운다(`ssh -G -o ClearAllForwardings=yes -L 47999:127.0.0.1:47999 windows`에 `localforward` 줄이 없고, 옵션을 빼면 생긴다). 이 Mac의 `~/.ssh/config`에는 `windows`에 `LocalForward 9222`·`9229`가 있어서 옵션을 빼면 사용자의 포워딩까지 함께 열린다. 그래서 같은 판정의 범위 안에서 포워딩 없이 **ssh 세션 하나의 표준 입출력**으로 데이터그램을 나른다.

```
데몬 ──UDP──▶ 127.0.0.1:47651 udprelay mac ══ ssh -o BatchMode=yes -o ClearAllForwardings=yes windows "<실행 폴더>\udprelay.exe pc -agent 127.0.0.1:47650" ══▶ udprelay pc ──UDP──▶ 127.0.0.1:47650 테스트 에이전트
```

- 프레임은 흐름 번호(4바이트) + 길이(2바이트) + 데이터그램. 데몬 쪽 UDP 출발지 하나가 흐름 하나이고, PC 쪽은 흐름마다 자기 UDP 소켓으로 에이전트에 보낸다(단위 테스트 `TestServePCRelaysEachFlowFromItsOwnSocket`).
- `SIGUSR1`을 받으면 12 s 동안 양방향 데이터그램을 모두 버린다. QUIC 유휴 시간 초과(8 s)보다 길어서 연결이 실제로 끊기고 데몬이 새 세대로 다시 붙는다. 하네스의 `drop`이 이것으로 "연결 끊김"을 만든다.
- ssh가 끝나면 1 s 뒤 다시 시작한다. 방화벽 규칙·시스템 설정은 바꾸지 않았고 `udprelay.exe`는 실행 폴더에만 있었다.
- 이 경로의 처리량·지연은 **SSH 중계 경유 — 대표값 아님**. 같은 ssh 경로의 `scp`(256 MiB) 값을 함께 적었다.

### 테스트 에이전트

- 설치된 `Deskhand Agent` 작업과 기본 홈(`~/Library/Application Support/Deskhand`)은 건드리지 않았다. 이 브랜치의 콘솔 빌드 `deskhand-agent.exe`를 실행 폴더 `C:\Users\apxkf\dev\deskhand-test\e2e3-<실행 ID>`(사용자의 Defender 제외 경로 아래)에 복사하고 `init --listen 127.0.0.1:47650 --config <실행 폴더>\agent\agent.json`로 키를 만들었다. 에이전트는 루프백(127.0.0.1)에서만 듣고 LAN 인터페이스에는 묶지 않는다.
- 페어링: Mac의 임시 홈(`/tmp/dh-e3-XXXX`, 유닉스 소켓 경로가 103바이트를 넘지 않도록 짧게)에서 `scripts/e2e/e2ehost fingerprint`가 컨트롤러 인증서를 데몬과 같은 방식(`daemon.ControllerIdentity`)으로 만들고 지문을 에이전트 `init --controller-fp`에 준다. 에이전트가 돌려준 지문으로 `e2ehost put`이 호스트 행(`127.0.0.1:47651`)을 넣고 데몬을 시작한다. `host add`는 설치·예약 작업을 바꾸므로 쓰지 않았다.
- 시작: 콘솔에 사용자가 있으면 `scripts/wintest.sh`처럼 대화형 예약 작업 `DeskhandE2E-<실행 ID>`(`/IT /RL HIGHEST`)로 사용자 세션에서 `wintestlaunch`가 띄운다. 이번에는 로그인한 사용자가 없어서 대화형 작업을 돌릴 수 없었고(`schtasks /Create /IT`는 작업 계정 사용자가 로그온해 있을 때만 작업을 실행한다: https://learn.microsoft.com/windows-server/administration/windows-commands/schtasks-create), 하네스가 자동으로 **ssh 세션(Session 0)** 에서 에이전트를 띄웠다(`agent pid=… session=0 udp=127.0.0.1:47650`). GUI가 필요한 부분은 그래서 아래처럼 BLOCKED다.

## 요약

| 항목 | 판정 | 핵심 증거 |
|---|---|---|
| A11 stdout/stderr·exit 7 | PASS | `exec.run`: stdout `out-line`/`안녕`, stderr `err-line`, `failed`·`code 7`·`error null`. `deskhand exec`도 두 스트림을 나눠 종료 7 |
| A11 timeout | PASS | `exec.run{timeout_ms:2000}` 2156 ms에 `failed`+`TIMEOUT`, PC에 ping 없음. `deskhand exec --timeout 3s` 종료 124(3635 ms) |
| A11 stdin EOF | PASS(수정 `cd99df3` 뒤) | `exec.run{stdin}`과 `exec.start{stdin}`이 입력 뒤 EOF, 표준 입력을 읽는 명령이 `deskhand exec`에서 589 ms에 끝남. 수정 전: 8 s 시간 초과(종료 124) |
| A11 취소·장기 실행 재부착 | PASS | `jobs.cancel` 뒤 PC 프로세스 없음. 연결 끊김(중계 12 s 차단) 전 시작한 job이 세대 +1 뒤 출력·종료 코드 5로 끝남 |
| A11 PTY | PASS | cmd PTY의 한글·이모지 출력, `exit 7` → 종료 프레임 7·job `failed` code 7, `deskhand shell` 파이프 입력 종료 3 |
| 터미널 재부착(연결 끊김) | PASS | 붙어 있던 스트림이 종료 프레임 없이 끝남 → 셸·job 유지 → `job_id`로 다시 붙으면 같은 pid, 셸 변수 유지, 끊긴 동안 나온 `tick10`까지 스크롤백으로 재생 |
| A12 텍스트·바이너리 | PASS | 729 B·8 MiB 양방향, PC SHA-256·Mac 바이트 일치 |
| A12 1 GiB | PASS | push 14.6 MB/s(73.6 s), pull 17.5 MB/s(61.4 s)(SSH 중계 경유 — 대표값 아님), 양쪽 SHA-256 일치, `transfer.updated` 진행 이벤트 263개, 서로 다른 진행 값 257개, 단조 증가, 마지막 1073741824 |
| A12 일시정지·재개 | PASS | 1 GiB pull을 269570146 B에서 멈춤(바이트 고정, 부분 파일 유지) → 재개 뒤 첫 진행 값이 그 위치 이상, 끝난 뒤 부분 파일 없음 |
| A12 취소 | PASS | pull 취소: 부분·대상 파일 없음. 1000개 push를 307개에서 취소: 끝난 파일은 PC에 남고 부분 파일 0 → `transfer.resume`이 1000개를 마침 |
| A12 1000개 파일 양방향 | PASS | push(취소·재개 포함) 16.8 s, pull 6.6 s(152.0 파일/s), 이름·SHA-256 목록 일치 |
| A13 이름 변경·이동 | PASS | `files mv` 이름 변경·하위 폴더로 이동, 있는 대상은 거절(둘 다 유지), `--overwrite`는 바꿈 |
| A13 충돌 4정책 | PASS | push·pull 각각 `fail`(TRANSFER_CONFLICT, 대상 유지)·`skip`·`rename`(`p-rename (1).txt`)·`replace` |
| A13 이동 | PASS | `files move` Mac→PC: 원본 삭제, PC→Mac: 원본이 사용자 휴지통(`$I` 기록으로 확인), 충돌(`fail`)로 실패한 이동은 원본·대상 모두 유지 |
| A14 복사 실패 | PASS | Windows가 받지 못하는 이름이 섞인 폴더 move_push → `partial`·원본 폴더 그대로. 잠긴 파일이 든 PC 폴더 move_pull → `partial`·PC 폴더 그대로, 휴지통에 아무것도 없음 |
| A14 hash 불일치 | PASS | move_pull 중 멈춰 Mac 부분 파일 앞 4 KiB를 0으로 → 재개 뒤 검증 불일치로 처음부터 다시 받음(진행 값이 0으로 돌아감) → 받은 파일 일치 뒤에야 PC 원본을 휴지통으로. move_push 중 PC 부분 파일을 망가뜨림 → 다시 보냄 → 검증 뒤에야 Mac 원본 삭제 |
| A14 원본 삭제 실패 | PASS | 읽기 전용 폴더의 Mac 원본: `done`+`copied_source_retained`+PERMISSION_DENIED, 양쪽에 파일. 휴지통이 받지 않는 262자 PC 원본: `copied_source_retained`+CAPABILITY_UNSUPPORTED, 원본 유지 |
| A15 재개 중 원본 변경 | PASS | push·pull 각각 멈춘 사이 원본을 바꿈 → 그 파일 `TRANSFER_CONFLICT`, 대상·부분 파일 없음 → 다시 재개하면 새 지문으로 바뀐 내용을 전송 |
| A15 디스크 부족 | NOT_RUN | 위 |
| A15 파일 잠금 | PASS | 공유 없이 열린 PC 파일 pull → PERMISSION_DENIED, Mac에 대상 없음 → 잠금 해제 뒤 재개 성공. 잠긴 PC 대상에 replace push → PERMISSION_DENIED, 대상 그대로(부분 파일은 재개용으로 남음) → 재개 성공 뒤 부분 파일 없음 |
| A16 한글·공백·이모지 | PASS | `한글 폴더 😀/하위 공백 폴더/공백 이름 2.txt`·`파일 😀 1.txt`·`café ü.txt`가 PC에서 그대로, 되받으면 동일 |
| A16 긴 경로 | PASS | 341자 PC 경로로 push·pull, SHA-256 일치 |
| A16 case 충돌 | PASS | PC `README.txt` 옆 `Readme.TXT`: fail은 충돌, rename은 `Readme (1).TXT`. 대소문자 구분 PC 폴더(`fsutil … setCaseSensitiveInfo`, 테스트 폴더만)의 `A.txt`·`a.txt` pull → 하나만, 다른 하나 TRANSFER_CONFLICT. 대소문자 구분 Mac 디스크 이미지의 `B.txt`·`b.txt` push → 하나만, 다른 하나 TRANSFER_CONFLICT |
| A16 junction·경로 탈출 | PASS | junction(`in`→실행 폴더, `out`→`C:\Windows\System32\drivers\etc`)이 든 폴더 pull: `real.txt`만, junction 둘은 INVALID_ARGUMENT, Mac에 그 뒤의 파일 없음. junction 자체를 원본으로: 전송 전에 거절. `..`가 든 원격 경로: push·pull 모두 INVALID_ARGUMENT, 바깥에 쓰지 않음. `CON.txt`·끝 점·`?`·`|` 이름은 하나씩 INVALID_ARGUMENT |
| 진단 | PASS | system(Windows 11 Pro 25H2, Ryzen 7 3800X, 32 GiB), services 302개(EventLog·sshd running), `--name sshd`, events System·`--level critical,error`, crashes 5개, `--save` artifact SHA-256 일치, `proc ls`의 `started_by_deskhand`, `proc kill --started` 다른 시각은 STALE_TARGET·맞는 시각은 종료 |
| 터널 | PASS | PC 루프백 에코 서버: 한글·이모지 30회 왕복(p50 5.0 ms), 32 MiB 양방향 일치·반쪽 닫기 전달(20.6 MB/s), `tunnel.list` 바이트 집계, 같은 로컬 포트 PORT_CONFLICT, `tunnel.close` 뒤 연결 거부, 듣는 프로그램 없는 포트는 연결이 닫히고 `last_error` |
| A17 Electron main/renderer | PASS(Session 0) | Electron 44.6.0(SHASUMS256 검증, 실행 폴더에만): main(`node`)·renderer(`page`) 구분, main 멈춤 동안 renderer 없음 → main이 창을 연 뒤 붙음, 두 번째 Electron은 다른 pid·포트, 두 번째 세션을 닫아도 첫 세션의 터널·앱은 유지, 닫으면 자기 프로세스 트리·터널만 정리, `--user-data-dir`로 `%APPDATA%`에 아무것도 남지 않음, Windows pid 부착은 CAPABILITY_UNSUPPORTED |
| A18 중단점·스택·변수·평가·재개 | PASS(Session 0, 수정 `255845d`·`5aabb70` 뒤) | `--break` 시작 멈춤(`start`) → `file_name:fixture-app/main.js:9`에서 `calcTotal`(프레임 URL 포함), 지역 `items/total/count`, 프레임 평가 `"10+20+30=60"`, step over·out. renderer `url` 중단점 → DOM click(평가)으로 `tallyPrices`←`onCompute`, 지역값·평가 4500, step, 재개 뒤 페이지 `결과: 7000원 (3개 품목)`. 잡히지 않은 TypeError에서 멈춤(`onBoom` 32행)과 콘솔의 경고·예외. idle main의 `debug pause`는 pending → resume이 취소 |
| QA 시나리오 | BLOCKED(GUI) / PASS(묶음) | 위. `qa get` 12단계, `artifacts.export` 묶음의 파일이 manifest와 같고(SHA-256·크기), report.json의 run_id 일치, report.html에 스크립트 없음 |
| 정리 | PASS | 실행 폴더·실행의 예약 작업·실행의 휴지통 항목 0, 실행 폴더에서 뜬 프로세스 0, 설치된 `Deskhand Agent` 작업 `Ready` 그대로 |
| 1단계 기준선 실패 두 테스트 | 원인 찾음·수정 | 아래 "1단계 기준선의 PC 전용 실패" |

## 단계별 기록

시각은 Mac 기준(KST), 로그는 판정 실행의 `scripts/e2e/phase3.sh all` 출력에서 줄였다.

### A11 명령 (`exec`)

```
exec.run: {"status":"failed","code":7,"stdout":"out-line \r\n안녕 \r\n","stderr":"err-line  \r\n","error":null}
deskhand exec: exit 7, stdout $'out-line \r', stderr $'err-line  \r'
exec.run timeout_ms 2000: {"status":"failed","code":-1,"timed_out":true,"error":"TIMEOUT"}, 2156 ms   (PC에 'ping -n 31' 0개)
deskhand exec --timeout 3s: 종료 124, 3635 ms                                  (PC에 'ping -n 32' 0개)
exec.run stdin: {"status":"succeeded","code":0,"stdout":"1:첫 줄\n2:second\n"}
deskhand exec reading stdin: exit 0 after 589 ms: 'got-[%X%]'
exec.start {stdin:"a\nb\n"} findstr → '1:a\n2:b', succeeded
jobs.cancel → cancelled, pid gone
connection drop for the long job: generation 1 -> 2, ready again after 12505 ms
long job after the drop: {"status":"failed","result":{"code":5,…,"state":"exited"}}, output 'start-long \ndone-after-drop '
```

- `cmd /c "set /p X=…"`가 `%X%`를 그대로 내는 것은 빈 입력에서의 cmd 동작이다(입력 없이 EOF를 받고 바로 끝났다는 증거).
- 연결 끊김은 중계가 12 s 동안 데이터그램을 모두 버린 것이다(QUIC 유휴 시간 초과 8 s를 넘김). job은 PC에서 계속 돌았고, 다시 연결된 뒤 그 job에 종료 코드 5와 끊기기 전·후의 출력이 모두 남았다.

### A11 PTY와 터미널 재부착 (`pty`)

```
pty.open cmd: {"status":"running","shell":"cmd",…}
ptyprobe: echo pty-%COMPUTERNAME%-한글 출력 😀 → 'pty-DESKTOP-08RKHVP-한글 출력 😀' matched
exit 7: {"exited":true,"exit_code":7,"closed":false} → job failed, result.code 7
powershell PTY: $m = "before-drop"; "ready-$m" matched
attached stream across the drop (generation 2 -> 3, ready after 13099 ms): {"closed":true,"exited":false}
the shell (pid …) and its job keep running after the drop
reattach by job_id: {"pid":<같은 pid>,"matched":true}  — "after-$m" → 'after-before-drop'
scrollback: 'ready-before-drop' … 'tick10' (끊긴 동안 나온 1..10 출력 포함)
jobs.cancel → cancelled, pid gone
printf 'echo cli-shell-%USERNAME%\r\nexit 3\r\n' | deskhand shell windows-e2e3 --shell cmd → 'cli-shell-apxkf', 종료 3
```

`scripts/e2e/ptyprobe`는 `stream.attach{kind:"pty"}`로 붙어 입력을 보내고, 터미널 이스케이프를 지운 출력에서 정규식을 찾고, 스트림이 종료 프레임 없이 끝났는지(`closed`)를 알려 준다.

### A12 전송 (`xfer`)

| 확인 | 결과 |
|---|---|
| text.txt 729 B | push 66 ms, pull 60 ms, PC SHA-256·Mac 바이트 일치 |
| bin.dat 8 MiB 난수 | push 731 ms, pull 1085 ms, 일치 |
| big.bin 1 GiB 난수 push | 14.6 MB/s(73.6 s), PC `files.hash` 일치, `transfer.updated` 이벤트 263개, 서로 다른 진행 값 257개, 단조 증가, 마지막 1073741824 |
| 1 GiB pull 일시정지 | 269570146 B에서 `paused`, 5 s 동안 bytes_done 고정, Mac에 `big.bin.deskhand-part-<ID>`만 있고 대상 없음 |
| 1 GiB pull 재개 | 17.5 MB/s(61.4 s)(멈춘 시간 제외), 재개 뒤 가장 작은 진행 값 = 멈춘 위치, SHA-256 일치, 부분 파일 없음 |
| pull 취소 | 109051904 B 받은 뒤 `cancelled`, Mac에 부분·대상 파일 없음 |
| 1000개(10개 폴더, 1–4 KiB) push 취소 | 307개 끝났을 때 `cancelled` → PC 파일 307개, 부분 파일 0 |
| 같은 전송 `transfer.resume` | `done`, 1000/1000, PC 파일 1000·부분 0, 취소 포함 16.8 s |
| 1000개 pull | 6.6 s(152.0 파일/s), 원본과 이름·SHA-256 목록 동일 |
| 같은 ssh 경로 `scp` 256 MiB | 19.9 / 23.9 MB/s |

### A13 이름 변경·이동·충돌 (`conflict`)

```
files mv a.txt → b.txt; mv onto keep.txt 거절('…keep.txt' exists; pass overwrite to replace it), --overwrite는 바꿈; 하위 폴더로 이동
push --on-conflict fail:    failed failed - TRANSFER_CONFLICT     (PC 'old')
push --on-conflict skip:    done skipped                          (PC 'old')
push --on-conflict rename:  done renamed → …\p-rename (1).txt      (PC 원래 'old', 새 이름 'new')
push --on-conflict replace: done written                          (PC 'new')
pull 네 정책: 같은 결과, rename은 'l-rename (1).txt'
move_push: done written deleted   (Mac 원본 없음)
move_pull: done written trashed   (PC 경로 없음, 휴지통 $I 항목 +1)
move_push onto an existing file with fail: failed failed retained TRANSFER_CONFLICT (양쪽 그대로)
```

### A14 이동 실패 (`movefail`)

```
move_push with a name Windows refuses: failed partial retained — bad:name.txt INVALID_ARGUMENT, ok.txt done (Mac 폴더에 두 파일 그대로)
move_pull with a locked file: failed partial retained — a.txt done, locked.txt PERMISSION_DENIED (PC 폴더 그대로, 휴지통 변화 없음)
move_pull paused at 22323443 bytes; zeroed the first 4096 bytes of the Mac part file
move_pull after the corrupted part: done written; progress after resume {"drops":1,"min_after":0} → Mac 파일 SHA-256 일치, PC 원본 휴지통
corrupted hp.bin.deskhand-part-<ID> (PC) → move_push: done written, PC SHA-256 일치, 그 뒤 Mac 원본 삭제
move_push with an undeletable source: done copied_source_retained retained PERMISSION_DENIED
  "받은 쪽 파일은 검증을 마쳤지만 이 Mac의 원본을 지우지 않고 남겼습니다. 폴더 …/mf/ro에서 항목을 지울 권한이 없습니다(permission denied). 원본을 직접 지우세요"
move_pull with a source the Recycle Bin refuses (262자): done copied_source_retained retained CAPABILITY_UNSUPPORTED
  "이 Mac에 받은 파일은 검증을 마쳤지만 PC의 원본을 휴지통으로 옮기지 않고 남겼습니다. the Recycle Bin cannot keep '…' because its path is 261 characters long …"
```

검증 전 원본 삭제가 없다는 근거: hash 불일치 두 경우 모두 재전송이 끝나 일치를 확인한 뒤에야 원본이 정리됐고(이벤트 순서: 진행 0으로 되돌아감 → 다시 끝까지 → `verifying` → `done`), 멈춘 동안 Mac 원본이 있음을 확인했다. 실패·부분 성공 세 경우는 원본이 그대로이고 결과의 `source`가 `retained`다.

### A15 재개 조건 (`resume`)

```
push resumed after the Mac source changed: failed — TRANSFER_CONFLICT, PC에 대상·부분 파일 없음 → resume → done, PC SHA-256 = 바뀐 Mac 파일
pull resumed after the PC source changed: failed — TRANSFER_CONFLICT, Mac에 대상·부분 파일 없음 → resume → done, Mac SHA-256 = 바뀐 PC 파일
disk full: NOT_RUN
pull of a locked PC file: failed PERMISSION_DENIED "… being used by another process; another program has it open, so close that program and retry" → 잠금 해제 뒤 resume → done
push replace onto a locked PC file: failed PERMISSION_DENIED "cannot put the received file at '…\lkd.txt', which is unchanged, as is the part file …" → resume → done, 부분 파일 0
```

잠금은 실행이 띄운 PowerShell job이 `[IO.File]::Open(…, 'ReadWrite', 'None')`으로 잡았다(사용자 프로세스는 건드리지 않음).

### A16 이름과 경로 (`names`)

```
push 한글 폴더 😀: done 3/3; PC 이름: café ü.txt · 파일 😀 1.txt · 하위 공백 폴더\공백 이름 2.txt → pull back: 이름·내용 동일
long path: 341 characters, push done, PC SHA-256 일치; 그 폴더 pull: 1개, 바이트 일치
push Readme.TXT next to README.txt: fail → TRANSFER_CONFLICT; rename → Readme (1).TXT (README.txt 그대로)
fsutil setCaseSensitiveInfo …\nm\cs enable → A.txt·a.txt → pull: A.txt done, a.txt TRANSFER_CONFLICT
case-sensitive APFS 이미지(16 MiB, 실행의 작업 폴더에 만들고 분리 후 삭제)의 B.txt·b.txt → push: B.txt done, b.txt TRANSFER_CONFLICT
junction in → …\nm\target, out → C:\Windows\System32\drivers\etc; files.list: in·out link:true
pull j: real.txt done, in·out INVALID_ARGUMENT; Mac에는 real.txt만
pull …\j\out (junction 자체): failed, transfer_id null, INVALID_ARGUMENT
push …\sub\..\..\escape.txt: INVALID_ARGUMENT "…에 . 이나 .. 원소가 있습니다. 그 원소 없이 항목의 전체 경로를 주세요"; pull …\..\c\l.txt: INVALID_ARGUMENT
push win/{ok.txt, CON.txt, trail., q?.txt, pipe|.txt}: ok.txt done, 나머지 4개 INVALID_ARGUMENT, PC에는 ok.txt만
```

### 진단 (`diag`)

```
diag system: {"os":{"name":"Windows 11 Pro","version":"25H2","build":"26200.9711"},"computer_name":"DESKTOP-08RKHVP","cpu":"AMD Ryzen 7 3800X 8-Core Processor","cores":8,"memory_total":34257387520,"disks":1,"gpus":1}
diag services: 302개, EventLog running, sshd running; --name sshd
diag events --log System --since 7d --max 20: 20개(truncated), record_id·level·provider·message 있음; --level critical,error는 그 수준만
diag crashes --since 30d: 5개
diag system --save: artifacts/<job>/diag-system.json, SHA-256 = Artifact.sha256, 내용 {method:"diag.system", result:{…}}
proc ls --name PING.EXE: 실행이 띄운 ping started_by_deskhand:true → proc kill --started 2000-01-01T00:00:00Z: STALE_TARGET(계속 실행) → 맞는 started: killed 1
```

### 터널 (`tunnel`)

PC에서 실행이 띄운 PowerShell 에코 서버(`TcpListener(Loopback, 0)`) 하나로 확인했다.

```
tunnel.open: local_address 127.0.0.1:<빈 포트>, owner user
echo through the tunnel: 왕복 p50 5.0 ms·최대 214.7 ms, 32 MiB 양방향 20.6 MB/s (SSH 중계 경유 — 대표값 아님)
tunnel.list: total_connections 1, bytes_to_remote·bytes_from_remote ≥ 32 MiB
같은 local_port로 다시 open: PORT_CONFLICT; tunnel.close 뒤 그 포트 연결 거부
remote_port 1(듣는 프로그램 없음): 클라이언트는 바로 EOF, failed_connections 1, last_error "cannot connect to port 1 of this PC's loopback (… actively refused it.); start the program there and retry"
```

### 디버거 (`debug`)

- Electron v44.6.0 win32-x64(2026-10-06 공개, 15일 경과, `electron-v44.6.0-win32-x64.zip` SHA-256 `04d6071f8deca081044e1398f96f65e4c1550ffdb3f882504cd78528d821d422` = 릴리스의 SHASUMS256.txt)을 Mac에 받아 `deskhand files push`로 실행 폴더에 올리고 `tar.exe -xf`로 풀었다. 앱은 `cmd/deskhand-fixture/electron`, 프로필은 `--user-data-dir=<실행 폴더>\electron-data`.
- PC에 로그인한 사용자가 없어 Electron도 Session 0에서 돌았다. 창 제목은 비어 있었지만(`MainWindowTitle`) 렌더러는 페이지를 불러 스크립트를 실행했고(DOM 결과 확인) CDP는 정상이었다.
- 중단점 패턴: `file_name:main.js`는 Electron의 `default_app.asar/main.js`(앱을 불러 주는 스크립트)에도 맞는다(첫 탐색 실행에서 그 9행에 멈춤). 판정 실행은 `file_name:fixture-app/main.js`를 쓴다 — 경로 구분자 경계 규칙대로다.

```
debug launch --break: exit 0 after 21084 ms, state paused, main paused, ports inspect/remote_debugging, renderer_unavailable_reason remote_debugging_disabled
main is paused with reason start
main paused: breakpoint bp-1, calcTotal file:///C:/Users/apxkf/dev/deskhand-test/e2e3-…/fixture-app/main.js:9:17, caller main.js:28
main locals: {"items":"Array(3) [Object, Object, Object]","total":"0","count":"0"}; evaluate in frame → "10+20+30=60"
step over → main.js:10:5; bp-1 끔 → step out → main.js:29:3(호출한 쪽)
resume → console 'main 합계 60 3개 합계 서울'
session after main ran: renderer page attached, renderer_unavailable_reason null, targets[0] main
renderer console 'renderer 준비 완료 — 서울' (renderer target)
url breakpoint renderer.js:11 resolved → DOM click(evaluate setTimeout(click)) → tallyPrices ← onCompute
renderer locals items Array(3), total 1500, highest 0; evaluate in frame 'items.length * 1000 + total' → 4500
step over → 11:42; bp 끔 → step out → onCompute:24; resume → page '결과: 7000원 (3개 품목)'
boom click → paused exception: TypeError "Cannot read properties of null (reading 'total')", onBoom:32:17
console: warning '의도된 예외를 던집니다'(29행) / exception 'TypeError: …'(32행, stack 2)
debug pause on idle main → pause_pending true; resume → pause_pending false, 2 s 뒤에도 멈추지 않음
debug attach --pid <Electron pid> → failed CAPABILITY_UNSUPPORTED
second launch (first open): 2 s 안에 exit 0(로그 시각 07:52:43 → 07:52:44), state running, main running and first, 다른 pid·포트
renderer attached before its scripts ran; launch 때 준 url 중단점에서 tallyPrices; breakpoints.activate false → resume → '결과: 7000원 (3개 품목)'
second session closed: 첫 세션 running, 그 터널 2개, Electron 유지 → 첫 세션 close: 터널 0, 실행 폴더의 electron.exe 0
%APPDATA%\deskhand-debug-fixture·Electron, %LOCALAPPDATA%\deskhand-debug-fixture 없음
```

### QA (`qa`)

```
deskhand --json qa run docs/examples/qa/fixture-smoke.yaml --host windows-e2e3 --var qa_root=<실행 폴더>\qa → 종료 1, status failed
  1 setup push pass (8640000바이트)   2 setup launch pass (15 s: 창을 기다린 시간)
  3 steps wait_window fail (15 s) "제목 "Deskhand Fixture"인 창이 제한 시간 안에 나타나지 않았습니다…"
  4–11 not_run "3단계(창 기다리기)의 결과(실패) 때문에 실행하지 않았습니다"
  12 teardown close fail INVALID_ARGUMENT "닫을 창이 없습니다. window를 주거나 앞에 launch나 wait_window를 두세요"
fixture processes of the run left after the run (ended now): 1
qa get: 12단계, report 경로
artifacts.export: files 4 (report.html·report.json·report.md·manifest.json)
exported bundle: {"listed":3,"present":3,"same_set":true,"bad":[],"run_id_matches":true,"has_md":true,"html_has_script":false}
```

이 브랜치의 입력 단계(`click`·`type`·`key`)는 항상 BLOCKED(데몬에 입력 경로 없음)이지만 `fixture-smoke.yaml`에는 입력 단계가 없고 `ui_act`(UIA)로 누른다. `screenshot`(선택)은 이 에이전트의 `screen`이 `unsupported`라 사용자 세션에서도 `blocked`가 된다. Session 0에서는 그 전 단계에서 멈췄다.

### 정리 (`down`)

```
killed <pid> deskhand-agent.exe
Recycle Bin entries of the run removed=2 left=0
left folder=False processes=0 task=False installed task=Ready
```

- PC: 실행 폴더 삭제, 실행 폴더 이미지의 프로세스 0, `DeskhandE2E-*` 작업 없음, 실행이 만든 휴지통 항목(원래 경로가 실행 폴더인 `$I`/`$R`)만 지움. `C:\Users\apxkf\dev\deskhand-test`가 비면 지운다. 설치된 `Deskhand Agent` 작업(`Ready`)·`windows-mcp-server`는 건드리지 않았다.
- Mac: 데몬·중계 종료, 큰 파일이 있던 작업 폴더 삭제, 임시 홈(`/tmp/dh-e3-…`)은 로그를 본 뒤 지웠다. Electron zip은 `build/e2e-cache/`(gitignore)에 남겨 다음 실행이 다시 받지 않는다.
- 방화벽·Defender·시스템 설정 변경 없음, 로그오프·재부팅·잠금 없음, GUI 입력 없음(디버거의 DOM `click()`은 실행이 띄운 픽스처 페이지 안의 JavaScript다).

## 측정치 (SSH 중계 경유 — 대표값 아님)

판정 실행, Mac 데몬 → `udprelay` → ssh(Wi-Fi) → PC. `apicall` 프로세스 시작 포함.

| 동작 | 값 |
|---|---|
| `exec.run` hostname.exe, 10회 | 56 / 57 / 67 ms (최소/중앙/최대) |
| `exec.run` cmd echo, 10회 | 53 / 54 / 63 ms |
| `files.stat`, 10회 | 25 / 27 / 32 ms |
| 중계 12 s 차단 → ready, 3회 | 13509 / 13616 / 14105 ms |
| 1 GiB push / pull | 14.6 MB/s(73.6 s) / 17.5 MB/s(61.4 s) |
| 8 MiB push / pull | 731 / 1085 ms |
| 1000개 파일 push(취소·재개 포함) / pull | 16.8 s / 6.6 s(152.0 파일/s) |
| 터널 에코 30회, 32 MiB 양방향 | 왕복 p50 5.0 ms·최대 214.7 ms, 32 MiB 양방향 20.6 MB/s |
| `debug launch --break` / `--break` 없음 | 21084 ms / 2 s 안(로그 시각 07:52:43 → 07:52:44) |
| 같은 ssh 경로 `scp` 256 MiB 올림/내림 | 19.9 / 23.9 MB/s (06:40 앞선 측정은 39.3 / 44.2 MB/s) |
| Electron zip(158 MB) `files push` | 10803 ms |

위 값은 모두 2단계 P6에서 같은 PC·LAN 직접 경로로 다시 잰다(이 세션의 Mac 프로그램은 PC 주소에 닿지 못했다).

## 고친 결함

| 커밋 | 실기기에서 본 것 | 고친 것 | TDD |
|---|---|---|---|
| `cd99df3` fix(exec): exec.start의 표준 입력은 stdin 글 뒤 EOF | `deskhand exec windows-e2e3 --timeout 8s -- cmd /c "set /p X=& echo got-[%X%]"`가 8 s 뒤 종료 124(TIMEOUT). `exec.start{stdin:"a\nb\n"}`의 `findstr`도 시간 초과 — 에이전트 `exec.start`가 표준 입력을 파이프로 열어 둔 채 `stdin`을 버렸고, 로컬 API에는 그 파이프에 쓸 방법이 없다(`exec.write` 없음) | 에이전트 `exec.start`는 `stdin`이 있으면 그 글을 읽힌 뒤 EOF(그 job의 `exec.write`는 거절), 없으면 지금처럼 열어 둔다. 데몬은 `exec.start`에 항상 `stdin`을 보낸다(없으면 빈 글). protocol.md·local-api.md에 적었다 | RED: `TestJobStartWithStdinReadsItThenEOF`·`TestJobStartWithEmptyStdinSeesEOFAtOnce`(`job j1 still running`), `TestExecStartWithoutStdinGivesTheCommandEOF`(`still running with output ""`) → GREEN. 실기기: 581 ms에 `got-[%X%]`, 종료 0 |
| `255845d` fix(debug): URL 없는 호출 프레임은 파싱된 스크립트의 URL을 쓴다 | Electron 44.6.0의 V8은 `Debugger.paused`의 `callFrames[].url`을 빈 글로 보낸다(CDP `Debugger.CallFrame.url`은 deprecated, `location.scriptId`로 `Debugger.scriptParsed`의 URL을 찾으라고 한다: https://github.com/ChromeDevTools/devtools-protocol/blob/master/json/js_protocol.json). main·renderer 모두 `CallFrame.url`이 `""`였다 | 대상마다 `Debugger.scriptParsed`의 `scriptId → url`을 기억하고 프레임 url이 비면 그 값을 쓴다 | RED: `TestCallFrameWithoutURLTakesTheURLOfItsParsedScript` → GREEN. 실기기: `file:///C:/Users/apxkf/dev/deskhand-test/e2e3-…/fixture-app/main.js` |
| `5aabb70` fix(cli): 열린 디버그 세션(running)의 launch·attach는 종료 코드 0 | 데몬은 열린 세션의 `debug.electron.launch`·`debug.attach`를 봉투 `running`(job이 세션을 따른다)으로 답한다. CLI는 이것을 "예상하지 못한 상태(running)입니다" + 종료 4로 처리했다(CLI 테스트의 가짜 데몬은 `succeeded`를 돌려줘서 드러나지 않았다) | launch·attach는 `running`을 성공으로 받는다(`--json` 출력 포함) | RED: `TestDebugLaunchAndAttachOfAnOpenSessionExitZero`(`exit 4 stderr "디버그 실행: 예상하지 못한 상태(running)…"`) → GREEN |
| `85e6866` fix(agent): 클립보드 감시의 첫 순번을 hello를 보내기 전에 읽는다 | 1단계 기준선에서 PC에서만 실패하던 `TestClipboardRepliesCutLongText`(`waiting for ev: … i/o timeout`). 클립보드 감시는 첫 연결의 hello를 보낸 **뒤에** 고루틴 안에서 기준 순번을 읽었다. hello 직후의 변경이 기준에 섞이면 그 변경은 보고되지 않는다(경쟁) | 기준 순번을 hello를 보내기 전에 동기로 읽는다 | RED(Mac에서 결정적으로 재현): 첫 `Sequence`가 300 ms 걸리는 가짜 클립보드의 `TestAClipboardChangeRightAfterHelloIsReported`(`i/o timeout`) → GREEN. PC(`wintest --ssh`): 두 테스트 PASS |
| `900df2e` test(agent): 자식 Path 검사는 system32 폴더가 들어 있는지 본다 | 1단계 기준선에서 PC에서만 실패하던 `TestTheChildEnvironmentTakesNothingFromTheAgentsEnvironment`(`Path is "C:\doitoracle\product\21c\dbhomeXE\bin;…"`). 이 PC의 기계 Path는 `C:\doitoracle\product\21c\dbhomeXE\bin`으로 시작하고 `C:\WINDOWS\system32`는 그 뒤에 있다. 제품은 기계 Path를 순서 그대로 쓴다(childenv 규칙) — 테스트의 가정이 틀렸다 | 테스트는 Path에 `<Windows>\system32` 폴더가 들어 있는지 본다 | PC(`wintest --ssh`): PASS |
| `ef46504` fix(debug): 세션의 대상 목록은 main을 먼저 싣는다 | `--break` 없는 launch에서 P2가 P1보다 먼저 응답하면 DebugSession.`targets`가 renderer, main 순이었다(두 번째 전체 실행 07:41:50, `"targets":[{"role":"renderer",…},{"role":"main",…}]`). 계약은 "main 먼저"(local-api §5) | 세션 스냅숏은 main 대상을 먼저, 나머지는 붙은 순서로 싣는다 | RED: `TestSessionListsMainBeforeTheRendererWhateverAttachedFirst`(`targets = [page-1 page-2 node-1]`) → GREEN. 하네스가 두 launch 모두 `targets[0].role == "main"`을 확인한다 |

## 1단계 기준선의 PC 전용 실패

두 테스트는 이 PC에서만 기준선부터 실패했다(진행 기록 F8 note). `scripts/wintest.sh --ssh ./internal/agent/server -run '…'`(07:18, 커밋 `5aabb70`)에서 재현했다.

| 테스트 | 실패 | 원인 | 조치 |
|---|---|---|---|
| `TestClipboardRepliesCutLongText` | `bounds_test.go:73: waiting for ev: … i/o timeout` | 제품 경쟁. 클립보드 감시는 첫 연결의 hello를 보낸 **뒤에** 고루틴 안에서 기준 순번을 읽는다. 테스트가 hello를 받자마자 클립보드를 바꾸면, 감시가 아직 기준을 읽지 않았을 때 그 변경이 기준에 섞여 보고되지 않는다. PC에서는 매번 그 순서였다. 실제로도 에이전트의 첫 연결 직후 PC에서 복사한 내용이 보고되지 않을 수 있다 | `85e6866`: 기준 순번을 hello 전에 동기로 읽는다. Mac에서 결정적으로 재현하는 `TestAClipboardChangeRightAfterHelloIsReported` RED → GREEN, PC에서 두 테스트 PASS |
| `TestTheChildEnvironmentTakesNothingFromTheAgentsEnvironment` | `Path is "C:\doitoracle\product\21c\dbhomeXE\bin;…"` | 테스트의 가정. 이 PC의 기계 Path는 `C:\doitoracle\product\21c\dbhomeXE\bin`으로 시작하고 `C:\WINDOWS\system32`는 그 뒤에 있다. 제품은 기계 Path를 순서 그대로 쓰고(childenv 규칙: 사용자 환경을 바꾸지 않는다) 에이전트 환경의 PATH를 가져오지 않는다 — 같은 테스트의 그 부분은 통과했다 | `900df2e`: Path에 `<Windows>\system32` 폴더가 들어 있는지 본다. PC에서 PASS |

같은 패키지 전체를 `--ssh`(Session 0)로 돌리면(07:23) 239 PASS · 16 SKIP · 2 FAIL이다. 남은 두 FAIL은 `TestProcKillByNameAnswersTheRefusalWhenAProcessDeniesTermination`(거부 ACE를 단 프로세스가 그대로 종료됨, `killed:1`)과 `TestCommitFailureLeavesNoCorruptTarget/access_control_entries_that_deny_the_deletion`(삭제 거부 ACE가 있는 대상이 그대로 바뀜, `outcome:written`)이다. 두 테스트는 거부 ACE로 거절을 만든다. SSH 세션 토큰에는 `SeDebugPrivilege`·`SeBackupPrivilege`·`SeRestorePrivilege`·`SeTakeOwnershipPrivilege`가 켜져 있다(`whoami /priv`: 사용). 이 권한들이 그 거부를 넘어서는 원인인지는 확인 필요다. 이 테스트들은 `wintest.sh` 기본 모드(사용자 세션의 대화형 작업)를 전제로 하고, 그 모드는 로그인한 사용자가 없어서 이번에 돌리지 못했다.

## 발견 사항과 남은 문제 (고치지 않음)

- **D1 `debug launch --break`는 약 21 s 걸리고(실측 네 번 20.7–21.1 s) 그동안 renderer 사유가 사실과 다르다.** Electron 44.6.0은 `--inspect-brk`로 main이 첫 줄에 멈춰 있는 동안 `--remote-debugging-port`(P2)를 열지 않는다. 계약(local-api §9 launch)대로 데몬은 P1에 붙은 뒤 P2를 20 s 기다리고 `renderer_unavailable_reason:"remote_debugging_disabled"`로 답한다(21084 ms). main을 재개하면 P2가 열리고 데몬이 붙어 사유를 지운다 — 동작은 계약대로지만 사람·AI는 20 s를 기다리고 "꺼져 있다"는 사유를 먼저 본다. 제안: `pause_on_start`에서는 P1에 붙자마자 답하고 renderer에는 "main 재개를 기다림" 같은 별도 사유를 둔다. 앱 A9가 정한 계약이라 결정이 필요하다.
- **Q1 QA teardown은 창이 안 뜬 앱을 남긴다.** `launch`는 됐는데 창이 없으면 `close`가 "닫을 창이 없습니다"(INVALID_ARGUMENT)로 실패하고, 실행기는 자기가 띄운 프로세스를 끝내지 않는다. Session 0 실행마다 `deskhand-fixture.exe`가 남았고 하네스가 실행 폴더의 그 이미지만 끝냈다. 제안: `launch`가 띄운 pid를 기억해 teardown 끝에 아직 살아 있으면 끝낸다(계약 변경).
- **Q2 `wait_window` 시간 초과 단계의 `error`가 비어 있다.** 단계는 `fail`이고 `message`는 있지만 `error.code`가 없다. local-api §10은 단계 시간 초과를 `TIMEOUT`이라고 적는다. 사소함.
- **X1 에이전트의 영어 오류 문장이 CLI 사람용 출력에 그대로 나온다.** 예: `'…\taken.txt' exists; pass on_conflict replace, skip or rename to transfer anyway`, `the Recycle Bin cannot keep '…' because its path is 261 characters long …`. 데몬이 만든 문장은 한국어라 한 화면에 두 언어가 섞인다. 제안: 데몬이 에이전트 오류 코드·필드로 한국어 문장을 만든다.
- **X2 실패한 전송은 부분 파일을 남긴다(계약대로).** 잠긴 PC 파일 pull이 PERMISSION_DENIED로 끝나면 Mac에 빈 숨김 `<대상>.deskhand-part-<전송 ID>`가, 잠긴 대상 replace push는 PC에 부분 파일이 남는다. 재개·취소·30일 정리에서 지운다(재개 성공 뒤 0개 확인).
- **E1 Session 0의 응용 프로그램 오류 창 1건.** 07:07:14 System 로그 `Application Popup` 26 "Windows - 응용 프로그램 오류 : 0x00007FF8D47403B4에 있는 명령이 0x0000000000010000의 메모리를 참조했습니다…". 그 시각은 수동으로 Electron 디버그 세션을 돌리던 때다. WER(Application 1000/1001) 기록이 없어 어느 프로세스인지는 확인 필요. 전체 실행 세 번에서는 다시 생기지 않았다(판정 실행 뒤 07:54에 조회한 지난 3시간의 `Application Popup`은 07:07:14 한 건). 창은 SSH 세션의 Session 0에 떴고 사용자 화면이 아니다.
- **N1 처리량.** 중계 경유 1 GiB push 14.6 MB/s(73.6 s)·pull 17.5 MB/s(61.4 s)는 같은 ssh 경로의 `scp`(19.9 / 23.9 MB/s)보다 낮다. 같은 날 앞선 `scp`는 39.3/44.2 MB/s였다(06:40). Wi-Fi 상태와 TCP 위 QUIC(재전송·흐름 제어가 겹침)이 섞인 값이라 비교 근거로 쓰지 않는다.
- **S1 사용자 세션 경로 미검증.** 테스트 에이전트의 대화형 작업 시작, 그 세션의 Electron 창, QA GUI 단계, `wintest.sh` 기본 모드. 하네스는 사용자가 로그인해 있으면 그 경로로 같은 확인을 한다(`DESKHAND_E2E_AGENT_MODE=auto`).
