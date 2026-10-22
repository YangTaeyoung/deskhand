# Headless 디스플레이 실험 패키지 검증

2026-10-21. 제품 출시·실기기 fps 통과 판정이 아닌 시험 패키지 준비 기록이다.

## 산출물

- Windows: `C:\Users\apxkf\AppData\Local\Temp\DeskhandDisplayBuild\test-package-20261021-v2`
- Mac 검증 사본: `/tmp/deskhand-display-package-v2`
- DLL SHA-256: `BC4DBECF903DB2CB99A8ECDA61ECCD59B0FF1E29DEB8F7B81D638D74DDD19155`
- INF SHA-256: `B96A78071A6461EA55D5C245373BFEB0E6C31B6CA1F389E0565F3818EC2EC361`
- CAT SHA-256: `23259C556741085CDEF2151A722CBA7838EBF843B9947BC41779CBD440273566`
- 시험 인증서 thumbprint: `2492F67797379E374799E3F01E6F2CD845D6C76E` (7일짜리 시험 서명, 출시 서명 아님).

## 통과한 검증

EWDK 28000.2526의 `BuildEnv\SetupBuildEnv.cmd amd64` 환경에서 Release x64를 빌드했다. DLL 컴파일·링크 통과, INF signability 검사 오류·경고 없음, 카탈로그 생성 통과였다. MSBuild는 임시 폴더의 중간·출력 디렉터리에 대해 MSB8029 경고를 냈다(증분 빌드 위치 경고). 빌드 로그: Windows `DeskhandDisplayBuild\build.log`.

첫 빌드는 `IDDCX_VERSION_MAJOR` C 매크로 누락으로 실패했다. EWDK가 1.4 include 경로를 선택하는 MSBuild 속성과 C 매크로는 별도였으므로 프로젝트에 명시했다. `SignMode=Off`도 EWDK `WindowsDriver.Common.targets`의 실제 속성으로 확인했다.

`build-test-package.ps1`은 DLL·CAT를 서명하고 INF2CAT 검사와 manifest 생성을 완료했다. SSH에서 CurrentUser Root 등록을 시도했던 첫 버전은 UI 확인 창을 만들 수 없어 실패했다. 이후 패키징에서 신뢰 등록을 제거하고 별도 오프라인 검증으로 변경했다. 현재 사용자 My·Root에 이 시험 인증서가 남아 있지 않은 것을 확인했다.

`verify-package.py`와 osslsigncode 소스 커밋 `868d6e95c603a8057e89336f12fe12b5d2258e00`의 임시 로컬 빌드로 다음을 확인했다.

- 네 패키지 파일의 manifest SHA-256 일치, 인증서 thumbprint 일치.
- DLL 서명, CAT 서명, DLL 카탈로그 멤버십 검증 성공. 시험 인증서를 명령별 파일로 지정해 시스템 신뢰 저장소를 바꾸지 않았다.
- DLL 코드 바이트를 바꾼 뒤 manifest 해시까지 다시 계산한 부정 시험: 서명 검증 실패, 이전 성공 표시가 `pending`으로 바뀜.
- INF 카탈로그 멤버십은 해당 오프라인 도구가 입력 형식을 지원하지 않아 검증하지 않았다. INF의 manifest 해시와 WDK 카탈로그 생성 검사는 통과했다.

Go 장치 제어 도구의 Windows x64 `go vet`·빌드, PowerShell 설치·패키징 문법 검사도 통과했다. 드라이버 없는 상태의 장치 생성→정상 닫기와 강제 종료 뒤 present 장치 0 시험은 [설계 기록](../design/headless-display.md)에 있다.

## 독립 코드 검토 반영

Sonnet 검토 한 차례(`/tmp/deskhand-headless-code-review.txt`)를 판단해 다음을 수정했다.

- swapchain 할당 실패는 `STATUS_GRAPHICS_INDIRECT_DISPLAY_ABANDON_SWAPCHAIN`으로 반환하고, 성공으로 소유권을 받기 전 swapchain을 직접 삭제하지 않는다([Microsoft 규약](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/iddcx/nc-iddcx-evt_idd_cx_monitor_assign_swapchain)).
- D0 진입 재초기화를 막던 `requested` guard 제거, D3D 장치를 swapchain 삭제까지 보유.
- IddCx C 버전 매크로와 `SignMode` 수정. 실제 EWDK 빌드로 확인했다.
- 제거 시 살아 있는 자체 장치는 거부하고, 정확한 인스턴스·패키지 ID가 일치하는 inactive 장치 기록만 제거한 뒤 해당 패키지를 삭제한다. 전역 디스플레이 구성은 삭제하지 않는다.

Monitor cleanup의 join/자식 swapchain 삭제 순서에 관한 우려는 아직 실제 드라이버 실행으로 검증하지 않았다. 테스트 드라이버 적재 뒤 10회 생성·제거, 강제 종료, 모드 변경·D0 재진입 시험이 필요하다. 단순히 스레드를 detach해 수명 문제를 숨기지 않았다.

## 2026-10-21 당시 실행하지 않은 단계

컴퓨터 Root·TrustedPublisher 등록, Driver Store staging, `testsigning` 활성화, 재부팅, 실제 드라이버 적재와 모니터 생성은 실행하지 않았다. 현재 Windows는 `testsigning`이 활성화되어 있지 않다. [UMDF 시험 드라이버 절차](https://learn.microsoft.com/en-us/samples/microsoft/windows-driver-samples/toaster-sample-umdf-version-2/)를 근거로 공용 PC의 시험 신뢰·부팅 정책 변경과 재부팅은 별도 사용자 승인 뒤 수행한다.

승인 뒤에는 현재 부팅 설정을 기록하고, 시험 패키지 적재 및 한시적인 소프트웨어 장치 수명 안에서 화면 공급·입력·복구를 측정해야 한다. 종료 시 장치·패키지·추가한 신뢰만 제거하고, 변경 전의 부팅 정책으로 복원한다. 부팅 정책 반영·복원에는 재부팅이 수반되고, 진행 중인 공용 PC 세션이 끊기므로 실행 시간을 사용자와 맞춘다.

## 2026-10-22 실제 적재·장치 수명 검증

사용자의 진행 요청 후 동일한 검증 패키지의 파일 해시·인증서 유효기간을 다시 확인했다. Windows의 변경 전 부팅 설정과 디스플레이 상태를 기록하고 `test-driver.ps1`로 시험 인증서를 LocalMachine Root·TrustedPublisher에 등록했다. DLL·CAT의 Windows 서명 검증 후 `oem86.inf`로 Driver Store staging에 성공했다. 인증서는 2026-10-28 12:55:18 UTC까지 유효하며 출시 인증서가 아니다.

- `virtual-display-probe.exe`로 만든 `SWD\Deskhand\DeskhandVirtualDisplay`가 실제 Display 장치 `OK`, ProblemCode `0`, DriverInfPath `oem86.inf`로 나타났다. 자식 Generic Monitor도 `OK`였다.
- 6초 수명의 프로브를 10회 실행했다. 매회 장치 `OK`, ProblemCode `0`, 자식 모니터 존재, 프로브 종료 코드 `0`, 종료 뒤 활성 장치 `0`을 확인했다.
- 프로브를 강제 종료한 뒤 활성 장치와 모니터가 모두 `0`으로 돌아왔다.
- 자체 장치 인스턴스만 `pnputil /restart-device`로 재시작했다. 성공 응답 뒤 Display 장치 `OK`, ProblemCode `0`, 자식 모니터 1개를 확인했다. 전체 디스플레이 드라이버나 PC를 재시작하지 않았다.
- `TESTSIGNING`을 켜거나 Secure Boot·메모리 무결성 설정을 바꾸지 않았고 PC를 재부팅하지 않았다. 전후 BCD 문자열이 일치했다. Device Guard는 SecurityServicesRunning `[2]`, VirtualizationBasedSecurityStatus `2`를 유지했다.
- 첫 수명 시험은 PowerShell `Start-Process` 객체의 ExitCode가 null인 상태라 검증기에서 중단됐다. 프로세스 핸들을 종료 코드 확인까지 보유하도록 진단 스크립트를 고친 뒤 위 10회 시험을 완료했다. 드라이버 오류를 고친 것으로 해석하지 않는다.

원시 결과와 사용한 진단 스크립트는 Mac `build/validation/20261022-idd-runtime/`에 보존한다. Windows는 사용자 세션이 Winlogon 잠금 상태이므로 **화면 공급 fps, 모드 변경 중 캡처 복구, 입력 지연은 아직 측정하지 않았다.** 장치 생성·적재 통과와 Deskhand의 기존 약 13fps 개선 판정은 구분한다.

중단 시점 상태: 패키지 `oem86.inf`와 시험 인증서 Root·TrustedPublisher 등록은 다음 성능 측정을 위해 유지했다. 프로브는 모두 종료됐고 활성 가상 디스플레이·모니터는 0이다. 부팅 시 장치를 자동 생성하는 작업이나 서비스는 추가하지 않았다. 제거는 Windows `runtime-20261022\test-driver.ps1 -PackageDirectory <test-package-20261021-v2> -Remove`이며, 기록된 정확한 패키지와 이 실험에서 추가한 인증서만 대상으로 한다.

## 2026-10-22 RDP 전환 후 정리

사용자 승인으로 기본 화면 경로를 내장 RDP로 전환했다. 위 staging 유지 기록은 종료됐다. 정확한 제거 스크립트로 `oem86.inf`와 비활성 자체 SWD 장치 및 이 실험에서 추가한 LocalMachine Root·TrustedPublisher 인증서를 제거했고, 모두 부재함을 확인했다. BCD 변경·재부팅은 하지 않았다. 자체 드라이버는 기본 설치 경로가 아니다. 이후 검증 상태는 [RDP 화면 전환](rdp-screen.md)을 따른다.
