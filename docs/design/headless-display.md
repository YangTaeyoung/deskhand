# 모니터를 켜지 않는 공용 Windows PC의 화면

2026-10-21 사용자 요구: 공용 검증 PC는 로그인해 두지만 실제 모니터 전원을 켜 두지 않는다. 따라서 모니터를 켜야만 화면이 부드럽게 전송되는 구현은 완료 조건을 충족하지 않는다.

## 현재 실기기 근거

- 자세한 재현과 측정은 [통합 검증](../acceptance/integration.md)에 있다. 로그인된 콘솔 세션에서 Desktop Duplication은 움직이는 시험 창의 업데이트를 주지 않았고, Deskhand는 화면 정지를 감지해 GDI로 전환한다(`internal/agent/capture/open_windows.go`, `stall.go`).
- GDI와 독립 WGC 프로브 모두 움직이는 화면에서 약 13 fps였고, 같은 실행의 `DwmFlush` p50은 76.53 ms였다. 드라이버·전원·디스플레이 중 어느 요소가 이 간격을 결정하는지는 아직 증명되지 않았다. PC가 보고한 60 Hz 모드만으로 실제 화면 합성 속도를 보장할 수 없다.
- 따라서 GDI 면적 축소나 호출 병렬화, 단순 WGC API 전환을 해결책으로 표시하지 않는다. 두 최적화와 WGC 모두 이 PC에서 목표 30 fps를 달성하지 못했다.

## 제품 방향과 검증 문턱

Microsoft의 [Indirect Display Driver 모델](https://learn.microsoft.com/en-us/windows-hardware/drivers/display/indirect-display-driver-model-overview)은 물리 GPU 출력에 연결되지 않은 모니터와 원격·가상 디스플레이를 지원하며, IddCx가 DirectX 표면으로 데스크톱 이미지를 제공한다. [공식 IddSample](https://learn.microsoft.com/en-us/samples/microsoft/windows-driver-samples/indirect-display-driver-sample/)은 단일 모니터 열거 예시이고, 생산용으로 채워야 할 TODO가 남아 있다. Deskhand 전용 가상 디스플레이를 개발한다면 이 API와 샘플을 출발점으로 조사한다. **가상 디스플레이가 현재 PC의 13 fps 제한을 해결한다는 측정 결과는 없다.**

다음 실기기 단계는 자체 IDD 실험 빌드, 드라이버 서명·설치·제거 가능성 확인, 모니터 꺼짐 상태에서 움직이는 창의 frame present·캡처·인코딩 속도 분리 측정이다. 시험 드라이버를 공용 PC에 설치하기 전에 되돌릴 수 있는 설치 절차와 기존 디스플레이 구성 보존을 검증한다. 제품 통과 조건은 모니터 전원에 의존하지 않고 20초 이상의 연속 화면, 정상 입력·재연결, 목표 30 fps를 재현하는 것이다.

## 성능 수정 재현 (2026-10-21)

`scripts/e2e/screen-probe/run.sh /tmp/deskhand-perf-baseline-20261021 motion1920`으로 별도 사용자 세션 에이전트·움직이는 픽스처·로컬 QUIC을 실행했다. 30.1초에 AU 354개(평균 11.8fps), 초당 통계 중앙값 13.0fps, H.264 디코드 오류 0이었다. 캡처된 프레임의 인코딩 완료까지 p50 6.22ms, 인코딩 완료→수신 p50 0.48ms였다. 에이전트 로그는 Desktop Duplication에서 GDI로 전환했음을 기록했다. 보고서 첫 `VideoInfo`의 `dd`만으로 전 구간을 DD로 해석하면 안 된다.

이 6.22ms는 GDI가 화면을 얻기 위해 기다린 시간을 포함하지 않는다. GDI 프레임의 시각은 `grab` 완료 뒤 기록하고(`internal/agent/capture/gdi_windows.go`), `capture_ms_p50`은 그 시각부터 인코더 제출 시각까지를 센다(`internal/agent/screen/stats.go`). 별도 `DwmFlush`·GDI 호출 시간과 함께 봐야 한다.

현재 세션 1은 콘솔 활성 상태이고 NVIDIA RTX 2070을 사용하지만 `Get-PnpDevice -PresentOnly -Class Monitor`는 연결된 모니터를 반환하지 않았다. 별도 움직이는 WinForms 창에서 다음 A/B를 측정했다. [Microsoft SetThreadExecutionState](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-setthreadexecutionstate)의 디스플레이·시스템 유지 요청을 같은 측정 스레드에서 설정하고 끝나면 해제했다.

| 단계 | GDI p50 | DwmFlush p50 | 처리량 |
|---|---:|---:|---:|
| 요청 전 | 76.61ms | 76.67ms | 12.84 / 13.06fps |
| `ES_CONTINUOUS | ES_DISPLAY_REQUIRED | ES_SYSTEM_REQUIRED` | 76.62ms | 76.65ms | 13.08 / 13.01fps |
| 요청 해제 후 | 76.62ms | 76.74ms | 13.02 / 13.08fps |

따라서 이 PC에서는 전원 유지 요청만 추가하는 수정은 효과가 없었다. 임시 창·예약 작업·원격 측정 파일은 제거했다.

`drivers/virtual-display/`에 단일 EDID 없는 60Hz 모니터를 제공하는 UMDF/IddCx 실험 소스와 검증·제거 절차를 작성했다. `scripts/diagnostics/virtual-display` 제어 도구는 1초 동안 소프트웨어 장치를 열고 닫는 실기기 시험에서 콜백을 받았고, 닫은 뒤 present 장치 수 0을 확인했다. 별도 강제 종료 시험도 실행 중 present 1 → `Stop-Process` 뒤 present 0이었다. 두 시험의 남은 PnP 기록은 정확한 인스턴스 ID로 제거했다. **이 시험은 아직 드라이버 로딩이나 화면 성능을 검증한 것이 아니다.** 드라이버 빌드·시험 서명·적재와 전후 비교가 다음 단계다.

이어 EWDK Release x64 DLL 빌드, INF 검사·카탈로그 생성, 오프라인 DLL/CAT 서명·DLL 카탈로그 멤버십 검증을 통과했다. 변조 DLL을 재해시한 부정 시험도 거부했다. [패키지 검증 기록](../acceptance/headless-display-build.md)에 산출물·해시·독립 코드 검토 반영과 남은 실기기 시험을 정리했다. 드라이버 적재·시험 모드·재부팅은 아직 실행하지 않았다. [Jump 비교 조사](jump-performance-comparison.md)에서는 현재 저장된 Jump 연결이 RDP임을 확인해, 콘솔 캡처와 원격 세션의 가상 디스플레이 경로를 구분했다.
