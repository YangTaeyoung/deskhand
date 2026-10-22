# RDP 화면 전환 검증 (2026-10-22)

## 구현과 범위

화면 기본 경로를 Windows 내장 RDP → 앱에 포함된 FreeRDP 3.32.2 → Deskhand 화면으로 변경했다. 로그인은 앱의 Windows 계정 암호 필드에서 직접 진행한다. Jump Desktop 실행이나 자체 가상 디스플레이 드라이버를 요구하지 않는다. 입력은 기존 에이전트의 제어권 검사를 거치는 별도 스트림을 사용한다. [설계](../superpowers/specs/2026-10-22-rdp-screen-design.md), [Microsoft RDP 세션 설명](https://learn.microsoft.com/en-us/windows/win32/termserv/consoles-vs-terminals), [FreeRDP 고정 버전](https://github.com/FreeRDP/FreeRDP/releases/tag/3.32.2).

## 완료한 확인

- 시험 드라이버 `oem86.inf`, 자체 SWD 장치, 이 실험에서 추가한 Root·TrustedPublisher 인증서를 제거했다. 부팅 정책을 바꾸거나 재부팅하지 않았다. 이전 문서의 staging 유지 기록은 과거 상태다.
- Go 전체 테스트는 계약 보강 전 통과했고, 후속 세션 바인딩 변경에 대한 관련 테스트·race·반복 동시성 검사는 별도로 통과했다. Swift 최종 선택 검사에서 UI 24개, endpoint 모델 1개, Swift Testing 엔진·프레임 19개가 통과했다. C 엔진 2개와 별도 ASan/UBSan 2개도 통과했다. 테스트 준비의 기존 SwiftTerm 경고와 일부 테스트 actor 경고는 남아 있다.
- 실제 데몬 입력 연결 검사 5개가 통과했다. 바인딩 누락, 다른 SID, 다른 연결 세대, AI 요청은 거부했고, 일치하는 바인딩만 수락했다. 이 검사는 실제 키·마우스를 보내지 않았다. 근거: `build/validation/20261022-rdp/input-binding-results.json`.
- 최종 앱의 엔진으로 실제 Windows 리스너에 접속해 잘못된 인증서 핀을 인증 전에 거부했다(`0xd4000004`). 가짜 시험 사용자 이름과 빈 암호를 사용했으며 실제 계정 암호를 시험하지 않았다. 근거: `build/validation/20261022-rdp/final-bundle.json`.
- 최종 앱에 포함된 dylib 10개의 의존 경로가 번들 내부 또는 시스템 라이브러리로만 연결됐고, LC_RPATH 제거·엔진 로드·`codesign --verify --deep --strict`가 통과했다. Homebrew GPL FFmpeg 대신 H.264 decoder/parser와 avcodec·avutil·swscale만 포함한 LGPL 구성으로 빌드했다. 소스 아카이브·빌드 설정·라이선스를 앱에 포함했다. 이는 전체 제품의 법률 검토 완료 판정은 아니다.
- 독립 UI 검수에서 수정 두 차례 후 밝은/어두운 로그인 스냅샷이 PASS였다. 실제 최종 앱에서도 사용자 이름·암호 라벨, 비활성 연결 버튼, 앱 내부 로그인 배너를 확인했다. 근거: `build/rdp/ui-review-final.md`, `build/validation/20261022-rdp/login-final-live.png`.

## 아직 통과하지 않은 확인

Windows 사용자가 아직 새 앱에서 인증하지 않았다. 실제 화면 프레임률, 입력 지연, 재연결, RDP 세션 전환 시 에이전트 결합, AI 제어권·긴급 정지의 전체 동작을 검증하지 않았다. 로그인 UI 검수는 연결 후 UI나 전체 제품의 상업용 품질 판정이 아니다. 기존 개인정보 설정·AI 범위 제한의 미검증 사항도 [통합 기록](integration.md)에 남아 있다.

앱은 `app/build/Deskhand.app`에 빌드해 실행해 두었다. 다음 단계는 앱의 ‘화면’ 탭에서 사용자가 Windows 계정 암호로 로그인한 뒤, 물리 모니터가 꺼진 조건 그대로 화면·입력·복구를 측정하는 것이다. 암호를 채팅이나 파일로 받지 않는다.

## 후속: 로그인과 창 영역 자동 해상도 확인

사용자가 Deskhand에서 Windows 계정으로 로그인했다. 실제 앱에 RDP 화면과 입력 가능 상태가 표시됐다. 연결 크기를 고정 1920×1080 대신 화면 영역의 포인트 크기·Mac 배율로 계산하고, Display Control 채널로 연결 중 해상도를 갱신한다. 변경은 250ms 안정화 뒤 최신 요청 하나만 전송한다. Retina 배율을 Windows desktop scale로 전달하며 최대 변 8192, 앱 요청 면적 16,777,216 픽셀로 제한한다. 입력 준비 안내는 화면 위에 겹쳐 표시해 표시 여부로 해상도가 반복 변경되는 문제를 막았다.

- 실제 로그인 직후 5496×2986을 확인했다. 사이드바를 숨기자 5812×2885, 복원하자 5496×2986으로 바뀌었다. 이 왕복 중 RDP 연결을 다시 맺지 않았다. 이는 화면 영역 변화 검사이며 전체 창 테두리 드래그·다른 모니터 이동은 아직 직접 검사하지 않았다.
- Native viewport/caps 검사와 Swift 크기·배율·잘못된 값·면적 제한 검사 통과. 후속 C 검사 3개와 ASan/UBSan 검사 3개가 통과했다. Swift 관련 선택 검사 4개(XCTest)·13개(Swift Testing)가 통과했다.
- 독립 코드 검수에서 DISP 종료와 전송 간 수명 경쟁, caps 면적 계산에서 모니터 수 누락을 발견했다. DISP의 OnOpen/OnClose/Send에 공통 수명 보호를 추가했고 실제 수정된 DISP 코드의 동시 write/close 시험이 통과했다. 서버 제한의 전체 곱을 상한 내에서 계산하고 초과 요청은 비율을 유지하며 줄인다. 고정 upstream 소스를 직접 수정하지 않고 `native/rdp/patch_disp.py`로 빌드 디렉터리에 변환본을 만들고 패치를 앱 라이선스 폴더에 포함한다.
- 최종 빌드·서명이 통과했다. 사용자의 다시 로그인한 연결을 유지하기 위해 마지막 수명 보호 수정 이후 앱을 다시 종료하지 않았다. **현재 프로세스는 자동 해상도 첫 빌드이고, 후속 수명 보호 수정은 다음 앱 실행부터 적용된다.**

근거: `build/validation/20261022-rdp/resize-runtime.json`, `resize-live-ax.txt`, `resize-live.png`, `build/rdp/resize-swift-final.log`, `resize-asan-tests.log`, `resize-final-app-build.log`. 전체 화면 성능·지연·복구·AI 제어권의 상업용 완료 판정은 여전히 별도 검증이 필요하다.

표준 근거: [Display Control 개요](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpedisp/bdc90b21-4b14-43bc-9c03-b7fecbfc6a1f), [크기·배율 제한](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpedisp/ea2de591-9203-42cd-9908-be7a55237d1c), [서버 최대 면적 계산](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpedisp/8989a211-984e-4ecc-80f3-60694fc4b476).

후속 독립 코드 재검수는 두 수정 범위에서 PASS였고 미해결 Critical/Important가 없었다(`build/rdp/resize-rereview.md`).

## 후속: 과도한 Retina 기본 해상도 수정

기본 배율 정책을 수정했다. 이전 5496×2986 / 5812×2885 결과는 당시 Retina 확대 정책의 검증값이고, 적절한 성능 기본값이라는 판정이 아니다. 새 기본값은 **창 화면 영역의 1포인트당 원격 1픽셀, Windows 배율 100%**다. Retina backing scale은 기본 원격 해상도에 곱하지 않는다. 매우 큰 창은 최대 변 3840, 총 8,294,400 픽셀 이내로 비율을 유지하며 제한한다. 해상도 숫자에 `px`와 도움말을 추가해 원격 픽셀 값임을 구분했다.

- Retina 1200×800에서 2400×1600이 생성되는 기존 동작과 모니터 배율 변화에 따른 불필요한 해상도 확대를 테스트로 재현했다. 수정 전 5개 테스트에서 7개 assertion이 실패했다.
- 수정 후 viewport 5개와 native 연결 4개(Swift Testing), 입력 gate/channel 4개(XCTest)가 통과했다. 앱 빌드·서명과 빌드 과정의 C 테스트 3개도 통과했다. 근거: `build/rdp/viewport-default-{red,green,app-build}.log`.
- 새 앱으로 재시작했다. 이전에 다음 시작으로 미뤘던 DISP 수명 보호 패치도 이번 프로세스에 포함됐다. 암호는 저장하지 않으므로 사용자의 앱 내 재로그인 후 최종 실제 해상도 확인이 필요하다. 이 절차에서 Windows 재부팅이나 로그오프를 실행하지 않았다.

구현: `app/DeskhandKit/Sources/DeskhandRDP/RDPViewport.swift`, 화면 관측·지연 갱신: `DeskhandUI/Screen/RDPWorkspaceView.swift`. FPS 개선량은 아직 측정하지 않았으므로 픽셀 감소를 실측 프레임률 향상으로 표시하지 않는다.

실기기 후속 확인: 사용자가 새 앱에서 다시 로그인했다. 동일 화면 영역에서 **5496×2986 → 2748×1493 px**로 변경돼 원격 픽셀 수가 75% 줄었다. 사이드바를 숨겼을 때 3008×1493, 이후 더 작은 창 상태에서 1280×601, 사이드바 복원 뒤 1020×601을 확인했다. 이 화면 영역 변경 중 재접속하지 않았고 실제 화면 연결과 입력 가능 상태가 유지됐다. 최종 새 앱은 로그인된 채 실행 중이다. 이는 해상도와 픽셀 수 확인이며 FPS/지연 개선량 검증은 아니다. 근거: `build/validation/20261022-rdp/viewport-default-runtime.json`, `viewport-default-{live,expanded,final}-ax.txt`, `viewport-default-final.png`.

## 2026-10-22 성능 실측

작은 창과 큰 창의 실제 연속 화면 갱신 시험에서 Windows 출력 FPS·인코딩·Mac CPU/RSS를 측정했다. 결과와 Mac 앱 내부 계측의 남은 단계는 [성능 실측 기록](rdp-performance.md)을 따른다. 서버 출력 FPS를 실제 Mac 표시 FPS로 간주하지 않는다.
