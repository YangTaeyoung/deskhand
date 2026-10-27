# 무인 AI와 사람용 RDP 검증 — 2026-10-27

기준: `f91d6e5` 위의 `feature/unattended-ai` 변경. 원본 checkout의 미커밋 UI
6개 파일은 사용자의 명시적 승인 후 별도 worktree에 복사해 포함했다. 원본 파일은
변경하지 않았다. 아래는 코드·격리 실기기·기존 설치본의 검증을 구분한 기록이다.

## 요구와 판정

| 요구 | 확인한 범위 | 아직 필요한 범위 |
|---|---|---|
| AI가 가능한 한 사람 개입 없이 작업 | 제한 시간 종료, 연결·데몬 재시작 후 추적, 에이전트 재시작의 unknown 처리, 중복 실행 방지, GUI QA 반복 | Mac 앱 완전 종료 뒤 연결 유지, 장시간 soak와 추가 장애 종류 |
| 사람이 일반 RDP처럼 접속·조작 | 앱 기동 후 저장된 로그인으로 실제 RDP 접속, 키보드·마우스로 시험 앱 조작, AI에게 제어권 반환 | 화면·입력 자동복구 변경의 최종 설치본 실측, 큰 창·긴 사용 시간의 품질 |

사용자는 전용 계정 전체 권한과 RDP 종료 뒤 잠금 없는 콘솔 자동 인계를 선택했다.
`Deskhand Console Handoff`를 해당 계정에만 설치했다. 활성 세션·다른 SID·Session 0은
거부하고, 종료 상태를 유예 전후 두 번 검사한다. 최종 조회와 tscon 사이의 OS 경합은
남는다. 비활성화 후 재활성화도 실기기에서 확인했다.

## 코드 회귀와 빌드

- `scripts/test.sh` 통과: 전체 Go vet·tests, Windows amd64 vet와 전체 테스트
  바이너리 교차 컴파일. 독립적인 race 테스트로 jobs·QA·agent input/server를 확인했다.
- Windows 실제 사용자 세션에서 `launch.started`, PID 생성 시각 확인 kill,
  오래된 writer 거부, 식별되지 않은 guarded kill 거부 테스트 통과.
- 최종 Swift 전체 655개 테스트 통과(UI 596, media 10, client 49).
- Mac 앱 빌드와 deep/strict 코드서명 검증 통과. 배포용 공증·GitHub Release는
  이번 검증에 포함하지 않는다.

주요 회귀의 RED는 수정 전 직접 확인했다. 일시적 종료 요청 실패 후 영구 running,
원격 작업 누락 후 영구 running, 손상된 meta.json 이후 작업 ID 재사용, 시간 초과
처리 시작 뒤 자연 종료가 success로 바뀜, 창 대기 timeout의 오류 코드 누락,
창 없는 launch의 식별 정보 누락, 입력 스트림 단절 후 자동복구 누락이다.

## 격리 Windows 에이전트 반복 검증

기존 `Deskhand Agent`와 별도로 `DeskhandE2E-1027164720` 작업에서 이번 빌드의
에이전트를 Windows 사용자 세션 1에 실행했다. QUIC은 loopback에만 열고 시험용
SSH UDP relay를 경유했다. 이 반복 실측을 직접 LAN 성능으로 해석하지 않는다.

`scripts/e2e/unattended.py --rounds 10`은 총 **310.284초** 동안 통과했다.

- 제한 시간 초과 프로세스의 실제 종료 10회.
- 사람이 제어권을 가진 동안 MCP 입력 거부 10회.
- 네트워크 단절 후 재연결·기존 작업의 부작용 1회 확인 5회.
- Mac 데몬 재시작 후 기존 작업 추적·부작용 1회 확인 5회.
- 각 회차와 에이전트 재시작 뒤 GUI QA 합계 11회, 각각 12/12단계 통과.
- 에이전트 강제 재시작 뒤 중단 작업 `unknown`, 원래 명령의 자동 재실행 없음.

별도 windowless 실패 시나리오는 `ping.exe` launch → 존재하지 않는 창 500ms 대기
→ teardown close였다. 대기는 의도대로 `TIMEOUT`으로 실패했고, close가 반환된
PID+생성 시각을 사용해 그 root 프로세스만 종료했다. PID 32552가 실제로 사라진
것도 SSH로 확인했다. 성공한 run으로 기록하지 않는다.

원시 결과는 worktree의 `build/validation/unattended-final.json`,
`windowless-cleanup.json`과 `/tmp/dh-e3-d5U1/artifacts/`에 있다. 생성된 보고서·화면은
검증 산출물이며 저장소에 커밋하지 않는다.

## 기존 설치 경로와 실제 Mac 앱

처음 standalone CLI가 시작한 기본 데몬은 UDP 송신에서 `EHOSTUNREACH`로
연결하지 못했다. 실행 중 작업 0개를 확인하고 그 데몬을 종료한 뒤, 앱 자체가
동봉 CLI로 데몬을 시작하자 같은 Windows PC의 UDP 47600에 직접 연결됐다.
이 실행 차이를 특정 VPN·Windows 방화벽 결함으로 단정하지 않는다.

실제 앱에서 저장된 로그인으로 RDP 화면이 열렸고, Windows 세션 1·SID·입력
desktop Default·잠금 해제·RDP 상태를 확인했다. 별도 시험 앱에 사람이 쓰는
RDP 화면을 통해 `deskhand human rdp check`를 입력하고 Tab/Return 및 마우스로
Send를 눌러 응답을 관찰했다. 시험 앱을 닫고 `AI에게 넘기기`로 owner가 none으로
돌아오는 것도 확인했다. 이 관찰만으로 프레임률·입력 지연을 정량 보장하지 않는다.

별도 Codex CLI 프로세스에 이번 앱의 `deskhand mcp`만 연결한 실제 AI 호출도
확인했다. 에이전트는 `hosts_list`와 `exec_run`만 사용해 시험 폴더의 파일 한 개를
기록·읽기·해시 계산했고, 원문 일치와 exit 0을 반환했다. 부모가 SSH로 계산한
SHA-256도 원문 해시와 일치했다. 기존 전역 MCP 설정은 변경하지 않았다. 이 검증은
명령·파일 경로이며 raw MCP 좌표 클릭·타이핑 성공 경로와는 별개다.

## 재부팅 최초 설정

SSH 서비스는 자동 시작이고, 기존 에이전트 작업은 사용자 로그인 시 대화형
세션에서 실행되도록 등록돼 있다. AC 절전 제한은 0초(절전 안 함)로 확인했다.
최초 자동 로그인은 꺼져 있었고 사용자가 Microsoft Autologon Enable을 직접 완료했다.
활성화 true, 횟수 제한 false, Winlogon 평문 password 없음만 확인했다. 암호/LSA는 읽지 않았다.

공식 Sysinternals Live에서 Autologon64.exe를 내려받아 Microsoft Authenticode
`Valid`를 확인했다. 실행 파일은 `C:\ProgramData\Deskhand\setup\Autologon\`에
두고 실제 RDP 화면에 라이선스 창을 열었다. 계정 암호를 읽거나 인자로 전달하지
않았다. 이후 실제 재부팅을 요청했고 LastBootUpTime이 2026-10-23에서
2026-10-27T18:05:20.500+09:00으로 바뀌었다. 별도 사람 로그인 없이 session 1의
에이전트 PID 12344가 시작되고 direct LAN 연결이 generation 2로 복구됐다.
콘솔 active, locked=false, input_desktop=Default와 화면/입력 ready를 확인했다.
그 상태에서 새 격리 에이전트의 UIA 12단계와 raw MCP 한글 입력·좌표 클릭도 통과했다.
재부팅 요청부터 raw MCP GUI 성공을 기록한 시각까지는 268초 상한이다. 중간에
설치·정책 테스트·격리 에이전트 준비를 병행했으므로 순수 부팅 지연 측정은 아니다.
최종 설치한 amd64 console/window binary SHA-256은 local build와 모두 일치했다.

[Microsoft Autologon](https://learn.microsoft.com/en-us/sysinternals/downloads/autologon),
[Microsoft 사용자 로그인 작업](https://learn.microsoft.com/en-us/windows/win32/taskschd/starting-an-executable-when-a-user-logs-on),
[Apple 로컬 네트워크 권한](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy)

## 독립 검수와 후속 수정

8개 관점 및 별도 Claude peer 검수에서 P0/P1은 없었다. 지적된 raw MCP 입력 검증
누락, Ctrl-C 시 잘못된 성공 receipt, 재시도 소진 뒤 오해하는 상태 문구를 수정했다.
Python -O에서도 검증을 건너뛰지 않으며 Ctrl-C receipt와 최적화 실행 회귀 테스트 2개가
RED→GREEN이다. 별도 인계 검수에서 최종 검사 뒤 로그 IO가 경합을 늘리는 문제를
수정했고 PowerShell 실행 순서 회귀 테스트로 확인했다.

실제 RDP 종료 후 protocol 값이 0으로 바뀌어 초기 handoff가 skipped된 문제를 발견했다.
Disconnected 상태와 같은 SID 검사를 유지하면서 protocol 0/2를 허용하도록 수정했다.
Windows PS 5.1 mocked 정책/계정/재접속/실행 순서 테스트가 통과했다.

## 재부팅 뒤 최종 반복 및 인계 실측

새 격리 작업 `DeskhandE2E-1027180617`의 강화 검증은 **349.971초, 10회차,
70개 확인 항목을 모두 통과**했다. 앞선 310초 시험과 달리 매 GUI QA 뒤
실제 MCP 이미지 블록, foreground 확인, capture ID를 이용한 좌표 클릭,
Unicode 한글 입력과 transcript `[done]`까지 확인했다. 총 11회 성공했다.
작업·시험 폴더·프로세스·relay는 `phase3.sh down`으로 제거했고 기존 설치 작업은
Running으로 확인했다. 원시 receipt는 `build/validation/unattended-enhanced.json`이다.

최종 앱에서도 RDP 클릭·`final rdp check` 타이핑·Send 응답을 확인했다.
`AI에게 넘기기` 후 명시적으로 RDP를 끊자 18:16:00.597 before부터
18:16:03.041 `handed_off`까지 약 2.44초가 걸렸다. 같은 SID/session 1의
console active·locked=false·Default·owner none을 확인했다. 그 뒤 기존 설치
에이전트에 직접 LAN MCP로 한글 입력·좌표 클릭을 실행해 성공했다
(`build/validation/installed-handoff-raw-mcp.json`).

## Mac 키체인과 설치본 범위

새 빌드에서 legacy `SecItemCopyMatching`이 main thread를 막는 실측을 발견했다.
background executor와 UI 2초 제한, inflight 누적 차단, legacy noUI shim으로 수정했다.
최종 조회가 OS 안에서 계속 대기하면 busy 해제와 원래 interaction flag 복구는
그 native 호출이 실제 반환할 때 일어난다. 2초는 OS 호출 강제 종료 보장이 아니다.
Focused 9개와 전체 655개 테스트, 독립 검수, 실제 새 앱 UI 표시를 확인했다.

최종 앱은 `~/Applications/Deskhand.app`에 Developer ID Application 인증서로
서명해 설치했고 deep/strict 검증을 통과했다. 공개 배포·공증 DMG 릴리스는 하지 않았다.
임시 서명 빌드의 기존 키체인 item은 다른 서명에서 읽지 못해 직접 재로그인이 필요했다.

Mac 앱의 **완전 종료(⌘Q)** 뒤에는 로컬 네트워크 송신이 EHOSTUNREACH로
거절되는 별도 문제가 남았다. 서명 변경만으로 해결되지 않았으며 launchd 사용자
agent 시험도 동일해 시험 등록은 제거했다. 설정에서 네트워크 토글은 모두 on이었다.
현재 직접 LAN 무인 운용은 Mac 앱을 실행 상태로 유지하는 조건이다. Windows RDP
연결을 종료하는 것과 Mac 앱을 완전히 종료하는 것을 구분한다.
[Apple TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy)
의 responsible code·서명·다중 설치 설명은 조사 방향이며 이 OS 증상의 확정 원인은 아니다.

마지막 Mac 종료 경로 독립 소스 조사에서도 Quit가 `host.disconnect`나 daemon stop을
호출하는 결함은 없었다. daemon은 Setsid로 분리되고 transport.Dial은 매 시도 새
UDP 소켓을 만든다. [Apple DTS의 유사 EHOSTUNREACH 사례](https://developer.apple.com/forums/thread/778457)
는 후속 조사 근거다. Network.framework·정식 SMAppService helper 등은 아직
시험하지 않았으므로 해결책으로 확정하지 않는다.
