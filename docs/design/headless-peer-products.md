# 모니터 없는 Windows: 유사 제품 조사

2026-10-21. 사용자 질문: 다른 제품에서도 비슷한 문제가 있는가, Deskhand와 유사한 제품이 이미 존재하는가?

## 결론과 범위

유사 제품은 이미 있고, 모니터가 없는 Windows에서 화면 캡처가 실패하는 문제도 다른 제품의 공식 문서에 있다. Parsec은 더미 디스플레이 또는 가상 디스플레이 드라이버를 안내한다. Apollo와 RustDesk는 가상 디스플레이 생성·관리 경로를 제품에 포함한다. 따라서 모니터 부재는 이미 알려진 제품 설계 조건이다.

**다른 제품도 이 PC에서 Deskhand와 같은 약 13fps가 나온다는 측정은 없다.** 현재 확인한 것은 문제 유형과 해결 구조다. 다른 제품의 실기기 fps·지연·안정성 통과나 우리 IDD의 효과로 확대 해석하지 않는다. 모니터 전원 꺼짐과 실제 연결 해제도 검증에서 각각 기록해야 한다.

## 비교

| 제품 | 공식 문서·코드에서 확인한 것 | Deskhand에 참고할 부분 |
|---|---|---|
| Parsec | Windows headless에서 캡처 실패와 커서 비표시를 공식 문제로 안내. 더미 플러그 또는 Parsec VDD를 해결 방법으로 제시 | 화면 공급과 커서 장치를 별도로 검증. 제품 자체의 headless 지원 절차 제공 |
| Apollo | Sunshine 기반 스트리밍 호스트. SudoVDA 통합, 클라이언트별 디스플레이 ID, 해상도·프레임률 맞춤, 종료 시 제거 | 세션 시작 때 생성→모드 적용→캡처 대상으로 선택→종료 시 제거하는 수명 관리 |
| RustDesk | 설치·OS·드라이버 조건을 검사하고 화면이 없으면 headless 가상 디스플레이 추가 후 다시 열거 | 단순 캡처 재시도 전에 화면 존재 여부를 검사. 드라이버 백엔드별 제어·제거 제약 구분 |
| Sunshine + Moonlight | Windows GPU 텍스처 기반 인코딩 경로 공개. 공식 시작 문서는 DD와 디스플레이 GPU의 관계를 설명 | 정상 디스플레이가 주어진 상태의 캡처→GPU 변환→인코딩 성능 비교군 |

Parsec 공식 자료: [headless PC 안내](https://support.parsec.app/hc/en-us/articles/32381854731156-Running-Parsec-On-A-Headless-Gaming-PC-Or-A-Server), [Windows 앱과 virtual displays](https://support.parsec.app/hc/en-us/articles/32381199341716-Parsec-App-for-Windows).

Apollo 공식 자료: [README](https://github.com/ClassicOldSong/Apollo), [FAQ](https://github.com/ClassicOldSong/Apollo/wiki/FAQ).

Sunshine 공식 자료: [시작 안내의 Windows 고려사항](https://docs.lizardbyte.dev/projects/sunshine/master/md_docs_2getting__started.html).

## 확인한 소스 좌표

최신 기본 브랜치의 commit을 GitHub API로 조회한 뒤 해당 SHA의 소스를 로컬 `/tmp/deskhand-peer-review`에 내려받아 읽었다. 앱 설치나 실행은 하지 않았다.

- Apollo `adc5c5a0bd80831ce495434bb16aee2cd4175fb8`:
  - `README.md:34`: SudoVDA와 스트림 수명, `:36`: 클라이언트별 ID, `:42`: headless 설정 설명.
  - [`src/process.cpp:236`](https://github.com/ClassicOldSong/Apollo/blob/adc5c5a0bd80831ce495434bb16aee2cd4175fb8/src/process.cpp#L236): headless/요청/활성 화면 부재에서 가상 디스플레이 경로 진입.
  - [`src/process.cpp:291`](https://github.com/ClassicOldSong/Apollo/blob/adc5c5a0bd80831ce495434bb16aee2cd4175fb8/src/process.cpp#L291): 생성, 모드 설정, 새 디스플레이를 캡처 output으로 선택. 이름 취득 실패도 수명 추적에 반영.
  - [`src/process.cpp:759`](https://github.com/ClassicOldSong/Apollo/blob/adc5c5a0bd80831ce495434bb16aee2cd4175fb8/src/process.cpp#L759): 사용한 디스플레이 GUID로 제거, 설정 복구 처리.
- RustDesk `bd9262397208c509ea97af61f0d44621ef25611c`:
  - [`src/server/display_service.rs:982`](https://github.com/rustdesk/rustdesk/blob/bd9262397208c509ea97af61f0d44621ef25611c/src/server/display_service.rs#L982): 설치·지원 검사 및 Amyuni 생성 조건.
  - [`src/server/display_service.rs:1016`](https://github.com/rustdesk/rustdesk/blob/bd9262397208c509ea97af61f0d44621ef25611c/src/server/display_service.rs#L1016): 화면 부재이면 `plug_in_headless`, 성공 뒤 재열거.
  - [`src/virtual_display_manager.rs:9`](https://github.com/rustdesk/rustdesk/blob/bd9262397208c509ea97af61f0d44621ef25611c/src/virtual_display_manager.rs#L9): 확인한 기본 브랜치의 선택값은 Amyuni. RustDesk 자체 IDD 구현도 파일에 있지만, 존재만으로 현재 기본 사용이라 단정하지 않는다.
- Sunshine `3494b23e20b13d27b88fa08b97d07cb6d917f919`:
  - [`src/platform/windows/display_vram.cpp:484`](https://github.com/LizardByte/Sunshine/blob/3494b23e20b13d27b88fa08b97d07cb6d917f919/src/platform/windows/display_vram.cpp#L484): D3D 캡처 이미지·공유 텍스처를 AVCodec/NVENC 입력으로 변환하는 경로.

우리 코드는 로컬 `feat/phase2-integration`의 `b907c1d`와 미커밋 headless 실험을 기준으로 확인했다(등록된 원격 없음). `internal/agent/capture/open_windows.go:30`의 DD→GDI 경로, `internal/agent/encode/vp_windows.go:157`의 CPU 프레임 GPU 업로드와 비교했다. 현재 자체 IDD는 빌드·서명 검증까지이며 세션 시작·캡처 대상으로의 자동 연결은 아직 제품 경로에 반영되지 않았다.

## 우리 구현에 반영할 판단

현재 핵심 차이는 headless 디스플레이를 세션에 공급하고 활성 캡처 대상으로 관리하는 제품 경로가 완성되지 않았다는 점이다. GDI fallback으로 연결이 유지된다는 사실과 부드럽게 사용할 수 있다는 판정은 분리한다.

참고 우선순위는 Apollo의 디스플레이 수명·모드·대상 선택, RustDesk의 지원 조건과 재열거·세션 전환 처리, Sunshine의 GPU 캡처·인코딩 경로다. 자체 IDD를 구현한 뒤에도 생성 콜백만 확인하지 말고 활성화·실제 화면 갱신·입력·종료 복구까지 통과시켜야 한다.

동일 PC의 제품 간 성능 비교는 해상도·프레임률 목표·움직이는 창을 맞추고 다음을 분리해 측정한다: 디스플레이 없음/전원 꺼짐, 생성·캡처·수신·실제 표시 fps와 간격 p95, 입력 반응, CPU/GPU, 연결 종료 후 디스플레이 복구. 아직 이 비교 실행 결과는 없다. 기존 시험 서명·재부팅 승인 질문도 이 조사로 승인된 것으로 취급하지 않는다.
