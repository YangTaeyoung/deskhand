# 모니터를 켜지 않는 공용 Windows PC의 화면

2026-10-21 사용자 요구: 공용 검증 PC는 로그인해 두지만 실제 모니터 전원을 켜 두지 않는다. 따라서 모니터를 켜야만 화면이 부드럽게 전송되는 구현은 완료 조건을 충족하지 않는다.

## 현재 실기기 근거

- 자세한 재현과 측정은 [통합 검증](../acceptance/integration.md)에 있다. 로그인된 콘솔 세션에서 Desktop Duplication은 움직이는 시험 창의 업데이트를 주지 않았고, Deskhand는 화면 정지를 감지해 GDI로 전환한다(`internal/agent/capture/open_windows.go`, `stall.go`).
- GDI와 독립 WGC 프로브 모두 움직이는 화면에서 약 13 fps였고, 같은 실행의 `DwmFlush` p50은 76.53 ms였다. 드라이버·전원·디스플레이 중 어느 요소가 이 간격을 결정하는지는 아직 증명되지 않았다. PC가 보고한 60 Hz 모드만으로 실제 화면 합성 속도를 보장할 수 없다.
- 따라서 GDI 면적 축소나 호출 병렬화, 단순 WGC API 전환을 해결책으로 표시하지 않는다. 두 최적화와 WGC 모두 이 PC에서 목표 30 fps를 달성하지 못했다.

## 제품 방향과 검증 문턱

Microsoft의 [Indirect Display Driver 모델](https://learn.microsoft.com/en-us/windows-hardware/drivers/display/indirect-display-driver-model-overview)은 물리 GPU 출력에 연결되지 않은 모니터와 원격·가상 디스플레이를 지원하며, IddCx가 DirectX 표면으로 데스크톱 이미지를 제공한다. [공식 IddSample](https://learn.microsoft.com/en-us/samples/microsoft/windows-driver-samples/indirect-display-driver-sample/)은 단일 모니터 열거 예시이고, 생산용으로 채워야 할 TODO가 남아 있다. Deskhand 전용 가상 디스플레이를 개발한다면 이 API와 샘플을 출발점으로 조사한다. **가상 디스플레이가 현재 PC의 13 fps 제한을 해결한다는 측정 결과는 없다.**

다음 실기기 단계는 자체 IDD 실험 빌드, 드라이버 서명·설치·제거 가능성 확인, 모니터 꺼짐 상태에서 움직이는 창의 frame present·캡처·인코딩 속도 분리 측정이다. 시험 드라이버를 공용 PC에 설치하기 전에 되돌릴 수 있는 설치 절차와 기존 디스플레이 구성 보존을 검증한다. 제품 통과 조건은 모니터 전원에 의존하지 않고 20초 이상의 연속 화면, 정상 입력·재연결, 목표 30 fps를 재현하는 것이다.
