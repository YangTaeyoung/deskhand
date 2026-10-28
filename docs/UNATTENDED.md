# 전용 Windows PC 무인 운용

범위는 신뢰하는 Mac 제어기와 전용 Windows 계정 전체다. 앱별 보안 격리를
제공하는 모드가 아니다. AI는 Deskhand MCP의 화면·UI·명령·파일 도구를 사용하고,
사람은 언제든 UI 제어권을 가져올 수 있다.

## 이 테스트 PC에서 확인한 운용 조건 (2026-10-27)

RDP 연결을 끊은 뒤 콘솔 인계가 설정돼 있으면 Windows 세션에서 AI 작업을
이어갈 수 있다. Mac 화면 앱을 완전히 종료한 뒤 직접 UDP 송신이 거절되는
환경에서는 SSH 키 인증을 이용한 중계로 복구한다. 상세 조건과 실측 범위는
[검증 기록](acceptance/unattended.md)을 참고한다.

## 최초 설정

1. Windows의 SSH와 RDP를 준비하고 Deskhand 앱에서 PC를 등록한다.
   설치되는 `Deskhand Agent` 예약 작업은 해당 계정 로그인 시 대화형 세션에서
   실행하며 실패 시 다시 시작한다. SSH 성공만으로 GUI 준비가 끝난 것은 아니다.
2. 전용 PC의 관리자 PowerShell에서 `scripts/windows/prepare-unattended.ps1
   -OpenAutologon`을 실행한다. 스크립트는 Microsoft 공식 배포본을 내려받아
   Authenticode 서명을 확인하고 상태만 출력한다. 암호를 인자로 받지 않는다.
3. 열린 Microsoft Autologon 창에 사람이 직접 계정 암호를 입력하고 Enable을
   누른다. 암호를 채팅·셸 인자·테스트 로그에 넣지 않는다. 이 도구는 암호를
   LSA secret에 저장한다. 이 PC의 관리자는 복호화할 수 있으므로 전용 계정과
   전용 PC를 전제로 한다. [Microsoft Autologon](https://learn.microsoft.com/en-us/sysinternals/downloads/autologon)
4. 절전 정책과 자동 잠금 정책을 확인한다. 화면을 끄는 것과 시스템 절전은
   다르다. SSH·에이전트가 계속 실행돼야 하며 GUI 작업에는 잠금 해제된 사용자
   세션이 필요하다. 정책을 자동으로 변경하지는 않는다.
5. 실제 재부팅 후 SSH 복귀, 에이전트 자동 시작, `session_inspect`의 사용자 세션,
   화면 캡처, 한글 입력·클릭·파일 확인까지 다시 검증한다. 이 확인 전에는
   재부팅 복구가 완료됐다고 판정하지 않는다.

Autologon 비활성화는 같은 Microsoft 도구의 Disable을 사용한다.
Deskhand 에이전트를 Session 0 서비스로 바꾸는 것으로 GUI 로그인을 대신할 수
없다. [Microsoft Interactive Services](https://learn.microsoft.com/en-us/windows/win32/services/interactive-services)

## 화면을 함께 보면서 조작

RDP 화면을 연결한 상태에서도 AI가 조작하는 장면을 볼 수 있다. 사람이 화면을
조작하면 사람에게 우선권이 넘어가고, 키·버튼을 모두 놓은 뒤 5초 동안 입력이
없으면 AI가 다시 조작할 수 있다. 다른 사람의 입력 스트림도 함께 확인하며,
긴급 정지는 자동으로 해제하지 않는다. 원격 화면을 닫으면 같은 조건을 확인해
사람 제어권을 놓는다. 부분 실행됐거나 결과가 불명확한 AI 동작은 자동 재실행하지
않으므로 AI는 화면을 다시 관찰한 뒤 이어가야 한다.

## RDP 종료 후 AI에 자동 인계

전용 PC에서 사람이 RDP를 끊은 뒤에도 GUI 작업을 이어가려면, 같은 관리자
PowerShell에서 아래 옵션을 명시적으로 켠다. `prepare-unattended.ps1`과
`console-handoff.ps1`을 같은 폴더에 둔다. 기본값은 꺼짐이다.

```powershell
.\prepare-unattended.ps1 -EnableConsoleHandoff
```

이 모드는 사람이 RDP 연결을 종료하면 전용 계정의 세션을 잠금 없는 로컬
콘솔로 인계한다. PC에 물리적으로 접근하는 사람도 그 화면을 조작할 수 있으므로
전용 PC에서만 사용한다. 사람이 다시 RDP로 연결하면 같은 Windows 세션을
조작하며, AI 입력은 Deskhand의 사람 제어권 규칙에 따른다.

스크립트는 보호된 `%ProgramData%\Deskhand\setup`에 설치하고, 선택한 계정 SID의
`RemoteDisconnect`에만 반응하는 `Deskhand Console Handoff` 예약 작업을 만든다.
기존 로그인 토큰으로 실행하며, 암호를 저장하지 않는다.
[Microsoft SessionStateChangeTrigger](https://learn.microsoft.com/en-us/windows/win32/taskschd/sessionstatechangetrigger)

작업은 자신의 실제 세션 ID·계정 SID·프로토콜·Disconnected 상태를 조회하고
2초 뒤 다시 확인한 다음 `tscon <현재 세션 ID> /dest:console`을 실행한다.
연결 종료 후 프로토콜이 console(0)로 바뀌는 Windows 동작을 수용해 RDP(2)와
console(0)를 허용하며, 시작 조건은 해당 계정의 RemoteDisconnect로 제한한다.
Session 0, 이미 연결된 세션(활성 콘솔 포함), 다른 계정, 조회 실패는 인계하지 않는다.
상태 조회와 `tscon`은 하나의 원자적 동작이 아니므로 마지막 확인 직후의 RDP
재접속과는 경합이 남는다. 실제 PC에서 종료 후 AI 재개와 사람의 재접속을 각각
검증한다. 결과와 유예 전후의 세션 ID·상태·프로토콜·SID는 설치 폴더의
`console-handoff.log`에 남는다.
[Microsoft WTSQuerySessionInformation](https://learn.microsoft.com/en-us/windows/win32/api/wtsapi32/nf-wtsapi32-wtsquerysessioninformationw),
[Microsoft tscon](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/tscon)

해제는 같은 계정의 관리자 PowerShell에서 실행한다.

```powershell
.\prepare-unattended.ps1 -DisableConsoleHandoff
```

Deskhand 에이전트 제거도 이 예약 작업을 함께 제거한다. Microsoft Autologon의
사용자 설정은 별도로 유지되며, 필요하면 해당 도구에서 Disable을 누른다.

## Mac 앱 종료 후 연결 복구

Mac에서 화면 앱을 종료해도 데몬은 계속 실행된다. macOS가 직접 UDP 송신을
`EHOSTUNREACH`로 거절하면, 저장된 Windows SSH 별칭과 키 인증이 있는 경우에만
SSH 중계로 자동 재연결한다. SSH 호스트 키 확인과 기존 QUIC 양방향 인증서·
에이전트 지문 검증을 그대로 적용한다. 인증서 오류에는 중계로 우회하지 않는다.
중계 프로그램은 Windows 계정의 TEMP에 콘텐츠 해시 이름으로 두고 매번 해시를
검증한다. SSH 연결이 끊기면 중계 프로세스가 끝나고 데몬은 다시 연결한다.

별칭 또는 키 인증이 없거나 SSH에도 연결할 수 없다면 자동 복구할 수 없다.
기본 직접 UDP 경로가 가능한 동안에는 SSH 중계를 사용하지 않는다.

## AI 작업 규칙

- 긴 명령은 `exec_start`에 작업별 `timeout_ms`를 주고 `job_get`·`job_output`으로
  종료 상태와 출력을 확인한다. 시간 초과 후 종료 요청만 재시도하며 원래
  명령을 다시 보내지 않는다.
- `unknown`은 성공도 실패도 아니다. 작업 ID와 실제 파일·프로세스를 확인한 뒤
  다음 동작을 정한다. 작업 기록 손상·유실을 새 작업의 성공으로 해석하지 않는다.
- `CONTROL_HELD_BY_HUMAN`이면 입력을 멈춘다. 사람이 제어 중일 때 UI 작업을
  반복 강행하지 않는다. 이 제어권은 계정 전체의 명령 실행 권한을 취소하는
  보안 샌드박스와 다르다.
- 같은 에이전트 DataDir를 유지한다. 작업 디렉터리 전체 삭제나 다른 DataDir로
  재설치한 경우 기존 작업 기록을 복구 가능한 상태로 취급하지 않는다.
- QA 결과에는 각 단계의 assertion·오류 코드와 산출물 manifest가 있어야 한다.
  최종 상태만 보고 성공을 선언하지 않는다.

## 반복 장애 검증

설치된 에이전트 대신 `scripts/e2e/phase3.sh build up`이 만든 별도 사용자 세션
에이전트를 사용한다. `build/e2e3-home`이 가리키는 홈의 `e2e3.env`에서 실행별
폴더·예약 작업·relay PID를 확인한 뒤 아래 값을 전달한다.

```sh
CGO_ENABLED=0 go build -o build/mcpprobe ./scripts/e2e/mcpprobe
python3 scripts/e2e/unattended.py \
  --home /tmp/dh-e3-EXAMPLE \
  --pc-dir 'C:\Users\me\dev\deskhand-test\e2e3-1234567890' \
  --agent-task DeskhandE2E-1234567890 --relay-pid 12345 \
  --rounds 10 --report build/validation/unattended.json
scripts/e2e/phase3.sh down
```

검증기는 제한 시간 종료·사람 선점·네트워크 단절·Mac 데몬 재시작을 반복하고,
작업의 부작용이 한 번만 발생했는지 확인한다. 마지막에는 테스트 에이전트를
재시작해 중단 작업이 `unknown`으로 남고 재실행되지 않는지 검사한 뒤 GUI QA를
다시 수행한다. 각 회차는 한글 입력·클릭·스크린샷·파일 확인을 포함한다.
보고서는 실패한 경우에도 남긴다. 작업 폴더·프로세스·예약 작업 정리는 호출자가
`phase3.sh down`으로 수행하며, 원래 설치된 에이전트에는 장애를 주입하지 않는다.

이 반복 검증은 실제 Windows 재부팅 검증과 별개다. 최종 실측과 설치 상태는
`docs/acceptance/unattended.md`에 기록한다.
