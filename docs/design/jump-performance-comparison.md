# Jump Desktop과 Deskhand의 화면 경로 비교

2026-10-21. 사용자가 모니터를 끈 같은 PC에서 Jump가 더 부드러웠다고 보고해 조사했다. 이 문서는 실제 관측, 공개 문서, 정적 분석을 구분한다. Jump의 과거 fps를 재측정한 결과는 아니다.

## 먼저 확인한 연결 방식

설치된 Mac 앱은 Jump Desktop 8.8.28(`com.p5sys.jump.mac.viewer`)이다. 앱 UI에서 저장된 `Windows` 항목의 주소는 `192.168.88.165:3389`이고, 설정에는 `RD Gateway`, `Use Network Level Authentication`, `Console session`이 있다. 따라서 **현재 저장된 비교 대상은 RDP 연결**이다. 사용자가 기억한 과거 실행도 이 항목이었는지는 별도 확인이 필요하다. 연결 설정은 변경하지 않았고, RDP 접속으로 현재 콘솔 세션을 전환하지 않았다.

Windows에서 Jump/Fluid/Phase 이름의 서비스와 Program Files 최상위 설치 폴더는 검색되지 않았다. 반면 PnP에는 `Microsoft Remote Display Adapter`, `SWD\REMOTEDISPLAYENUM\RDPIDD_INDIRECTDISPLAY&SESSIONID_0001` 기록이 있었다(Status `Unknown`, 현재 활성이라는 뜻이 아님). 이것만으로 Jump Connect가 모든 위치에 없다고 단정하지 않는다.

[Microsoft의 콘솔·원격 터미널 설명](https://learn.microsoft.com/en-us/windows/win32/termserv/consoles-vs-terminals)에 따르면 RDP 원격 터미널은 세션에 원격 WDDM 간접 디스플레이 드라이버와 가상 모니터를 노출한다. 따라서 물리 모니터가 꺼진 콘솔 출력을 직접 캡처하는 Deskhand와 화면 생성 경로부터 다르다. 이 차이는 부드러움의 원인 후보를 좁히지만, 과거 Jump의 체감 차이 전체를 입증하지는 않는다.

## 현재 구현과의 차이

| 항목 | Jump에서 확인한 것 | Deskhand에서 확인한 것 | 조치 |
|---|---|---|---|
| 화면 원천 | 저장된 RDP 연결. Windows 원격 디스플레이 장치 기록 존재 | 콘솔 DD가 멈추면 GDI로 전환 (`internal/agent/capture/open_windows.go:29`, `:88`) | 물리 출력이 없는 상태에서도 정상 갱신하는 자체 디스플레이 경로 검증 |
| 현재 병목 | 이 조사에서 Jump의 활성 스트림 fps는 측정하지 않음 | 움직이는 창의 GDI/DWM 약 76.6ms, 정상 구간 약 13fps | 캡처 이전 화면 합성·디스플레이 공급부터 해결 |
| 인코딩 | 최신 Jump 문서는 Fluid/Fluid 2.0 소프트웨어와 Hardware GPU 인코더를 구분 | 실기기 NVIDIA MFT 사용, 캡처 타임스탬프→인코딩 완료 p50 6.22ms | 하드웨어 인코더 부재를 원인으로 오진하지 않음 |
| 메모리 경로 | 설치된 Mac 클라이언트만으로 Windows 호스트의 복사 경로를 확정할 수 없음 | GDI CPU 픽셀을 GPU 텍스처에 다시 업로드 (`internal/agent/encode/vp_windows.go:157`) | IDD/GPU 텍스처 경로 검증 뒤 불필요한 복사 제거 여부 측정 |
| 프레임 표시 | Metal 함수 본문에서 dirty 범위 관리와 command buffer의 present/commit을 확인. 실제 세션의 선택 경로는 미확정 | AVSampleBufferDisplayLayer 사용, DisplayImmediately 지정 (`app/DeskhandKit/Sources/DeskhandMedia/SampleBufferFactory.swift:105`) | fps 회복 후 프레임 간격 p50/p95 및 실제 표시 지연 측정 |
| 전송 | 저장된 RDP 연결의 UDP/TCP 협상 상태는 아직 관측하지 않음 | 이 PC와 Mac의 직접 UDP가 실패했던 별도 문제 존재 | Fluid의 UDP 설명을 현재 RDP 연결에 그대로 적용하지 않음 |

Deskhand 측정 상세와 재현 명령은 [headless-display.md](headless-display.md)에 있다. 6.22ms에는 GDI `grab` 대기가 빠져 있다(`gdi_windows.go:242` 이후에 프레임 시각을 기록). 인코더만 빨라져도 현재 76ms의 화면 공급 간격은 없어지지 않는다.

## Fluid와 RDP를 구분해 참고할 것

최신 Jump 문서는 [가상 디스플레이 사용](https://docs.jumpdesktop.com/viewer/performance/high-performance/)을 권장하고, [인코더 문서](https://docs.jumpdesktop.com/viewer/performance/codecs/)는 CPU Fluid/Fluid 2.0과 GPU Hardware를 명확히 구분한다. 이것은 최신 제품의 설계 참고이며, 설치된 구버전 8.8.28의 현재 RDP 연결이 같은 기능을 사용한다는 증거는 아니다.

[Jump의 연결 통계](https://support.jumpdesktop.com/hc/en-us/articles/4423458049165-Live-Fluid-Connection-Statistics)는 수신 Frames, 인코딩 시간, Direct/Relay, UDP/TCP, Displays의 Virt 표시, 실제 Encoder/Decoder를 분리한다. Deskhand도 수신/enqueue를 실제 화면 표시 완료와 구분해야 한다. 현재 `FrameClock.latencyMS`는 수신→enqueue까지만 더하므로 실제 디코드·스캔아웃 완료 지연으로 해석하면 안 된다.

## REA 조사 범위

사용자가 지정한 [morluto/rea](https://github.com/morluto/rea)를 `/tmp/deskhand-rea-analysis`에 읽기 전용 조사 목적으로 체크아웃했다. 확인한 main은 `25e1a24a3ed3671a0148757555f18d16c29d936a`, npm CLI는 `rea-agents@6.7.0`으로 고정했다. [공식 native 조사 절차](https://github.com/morluto/rea/blob/main/docs/native-investigation.md)와 `src/ghidra/GhidraProviderClient.ts`를 읽었다. 전역 에이전트 설정을 바꾸는 `setup`은 실행하지 않았다.

- 원본 앱 실행파일 SHA-256: `fd84d34f3369af97e20fdbce483691579daf90fec0f538423b6d8037fa087d20`
- `lipo -thin arm64`로 만든 분석 사본 SHA-256: `81eb6dce531825dd42f18fe1ae62ef70427a4dfb3e742bc98272fa1b5bba2e19`
- 원본 실행파일과 앱 번들은 변경하지 않았다. `strings`, `otool -L`에서 RDP/가상 디스플레이 관련 이름과 Metal 링크를 확인했다. 이름이나 링크가 존재한다는 사실만으로 실제 실행 경로나 성능을 증명하지 않는다.
- Ghidra 12.1.4를 NSA 공식 릴리스에서 받아 임시 폴더에 풀고, 공식 소스의 네이티브 디컴파일러를 arm64로 빌드했다.
- 기본 전체 분석은 330초 제한으로 실패했다. Objective-C Message Analyzer를 끈 두 번째 시도도 느린 switch 분석의 오류를 반복해 취소했다. Ghidra 클래스에서 정확한 analyzer 이름을 확인한 뒤 `Objective-C Message Analyzer,Decompiler Switch Analysis`를 끈 프로파일로 조사했다. 이 생략은 Objective-C 동적 디스패치·switch 호출 관계의 완전성을 제한한다.
- 이 프로파일의 REA 함수 검색은 성공해 조건에 맞는 61개 항목을 반환했다. 증거 파일은 `/tmp/deskhand-jump-rea-bounded-procedures.json`, 관측 snapshot은 `/tmp/deskhand-jump-analysis-bounded.json`, evidence ID는 `ev_3506ffaebb45020b2acbd378c062746ec59643fa872f1c74adae09e5c65cdcb4`다. 실패한 첫 시도의 파일은 `/tmp/deskhand-jump-rea-procedures.json`이며 성공 결과로 해석하지 않는다.

| 정적으로 확인한 함수 | 주소 | 확인 범위 |
|---|---|---|
| `RDSession::getDirectRDPTransport` | `0x100798cf8` | RDP transport 진입 함수 존재 |
| `RDSession::addVirtualDisplays:` | `0x1007a0c1c` | 가상 디스플레이 제어 함수 존재. 현재 RDP 세션의 사용 여부는 별도 |
| `RDController::setFrameRate:` | `0x1007b8414` | 프레임률 제어 함수 존재 |
| `MetalBitmapBuffer::flushDirty` | `0x10087ae7c` | 변경 영역 flush 함수 존재 |
| `MetalBitmapBuffer::updateTextureWithRects:` | `0x10087b0fc` | 영역 기반 텍스처 업데이트 함수 존재 |
| `MetalBitmapView::drawRect:` | `0x10087b87c` | Metal 뷰 표시 함수 존재 |

함수 이름 검색은 실행 경로나 부드러움을 증명하지 않는다. 후속 함수 본문 조사는 동일한 프로파일의 지속 MCP 세션에서 수행하고 `/tmp/deskhand-jump-mcp-*.json`에 보존한다. CLI snapshot은 관측 캐시이며 새 함수 질의마다 원본을 다시 분석할 수 있으므로, 여러 함수를 한 세션에서 조사한다.

### 함수 본문까지 확인한 결과

지속 MCP 세션에서 다섯 함수의 Ghidra dossier·pseudocode·assembly를 받았고, `close_binary`로 세션과 임시 프로젝트를 정리했다. 최종 관측 snapshot은 `/tmp/deskhand-jump-mcp-final-snapshot.json`(5개 관측, 5,281,593 bytes)이다. 아래는 역컴파일 결과에서 직접 관측한 동작이며, 해당 코드가 과거 연결에서 실행됐다는 증거는 아니다.

- `getDirectRDPTransport` (`0x100798cf8`): `directTcpHostName`, `directTcpPort`, `tsGateway`를 조회하고 게이트웨이 유무에 따라 전송 객체를 구성하는 분기가 있다. RDP 경로와 Fluid 경로를 구분해야 한다는 UI 관찰을 보강한다. 이 함수만으로 RDP 전체가 TCP만 사용한다고 판단하지 않는다. 증거 `ev_c0cd9435bf8267dbab588a17200090c7ac61a59d568d7adfd3c157a07ff3cd07`.
- `updateTextureWithRects:` (`0x10087b0fc`): 전달받은 변경 사각형들을 min/max로 합쳐 경계 영역을 누적한다. 함수 이름과 달리 이 본문에서 직접 GPU 복사를 수행한다고 판정하지 않는다. 증거 `ev_295ec5a1c642fb7795e98b2cbc85caf0664067a77515053a4900d2f93fea6b06`.
- `flushDirty` (`0x10087ae7c`): 변경 영역이 비어 있으면 반환하고, 버퍼 구성에 따라 `didModifyRange:`에 범위 또는 버퍼 전체 길이를 전달한 뒤 dirty 범위를 비운다. **항상 작은 영역만 복사한다는 뜻은 아니다.** 증거 `ev_be0445043f2923bcf88f99b31cda3b6ad8d752f9aafb2cef23251d1d71151987`. API 참고: [Apple MTLBuffer.didModifyRange](https://developer.apple.com/documentation/metal/mtlbuffer/didmodifyrange(_:)).
- `drawRect:` (`0x10087b87c`): 프레임 공급 콜백을 호출하고, command buffer를 만든다. 프레임과 render pass가 있으면 `flushDirty`→render encoder→`encodeRender`→`endEncoding`→`presentDrawable:` 순으로 호출하고 command buffer를 `commit`한다. 이 본문에서 명시적인 GPU 완료 대기는 관측되지 않았다. 호출한 하위 함수·런타임 전체에 대기가 없다는 뜻은 아니다. 증거 `ev_17050e8b2bea83fdca33eb907dd9d77843993caefd95fa8cb1741e36caa27522`. API 참고: [Apple MTLCommandBuffer.present](https://developer.apple.com/documentation/metal/mtlcommandbuffer/present(_:)).
- `setFrameRate:` (`0x1007b8414`): 메뉴 객체에서 값을 가져와 `setMaxFPS:`, `setMaxFrameRate:`, `writeServerToFile`을 호출한다. 이것만으로 실제 생성·수신·표시 fps가 그 값에 도달한다고 판정하지 않는다. 증거 `ev_841c58ffd6bee71588c37fe4a388aba467bbab7888cbaabf5457196a36d9717c`.

Deskhand와 비교하면 Jump에는 변경 범위를 누적하는 bitmap/Metal 표시 구현이 있고, 현재 Deskhand는 H.264 AU를 AVSampleBufferDisplayLayer에 넣는 경로다. 그러나 현재 Deskhand의 모니터 꺼짐 상태에서는 표시 계층에 도달하기 전 GDI의 76ms 대기가 이미 측정됐다. **Metal로 바꾸기만 하면 현재 13fps가 해결된다는 근거는 없다.** 먼저 화면 공급을 회복한 뒤 두 표시 방식의 비용을 비교한다.

## 수정 우선순위

1. 자체 headless 디스플레이에서 실제 프레임 공급이 회복되는지 먼저 측정한다. 이미 작성한 `drivers/virtual-display`는 아직 실험 소스이며 제품 기본 경로가 아니다. 드라이버 빌드·적재·복구와 fps 전후 비교가 필요하다.
2. 디스플레이→GPU 캡처→기존 하드웨어 인코더 경로를 검증한다. IDD가 60Hz 모드를 열거했다는 사실과 실제 60fps 생성·전송은 별도로 확인한다.
3. 직접 LAN 전송 문제를 해결한 뒤 같은 해상도·움직임에서 프레임 간격과 입력→화면 반응을 측정한다. 평균 fps만으로 부드러움을 통과 처리하지 않는다.
4. 그 뒤 클라이언트 디코딩·표시와 적체 시 복구를 조정한다. 현재의 13fps 원인이 확인되기 전에 코덱이나 UI 렌더러를 전면 교체하지 않는다.

검증한 구조적 차이와 측정 방법을 참고해 Deskhand 자체 디스플레이 공급 계층과 전송·표시 계층을 개선한다.

## 현재 Jump 연결 실측 (2026-10-22)

사용자가 물리 모니터가 분리된 상태에서도 현재 Jump 사용감이 괜찮다고 알려 실제 연결을 관찰했다. 물리 케이블 상태는 사용자 설명이며, 아래는 소프트웨어에서 직접 확인한 내용이다.

- Jump Desktop에 `Windows` 원격 창이 열려 실제 Windows 설정 화면이 보였다. 설정 화면에는 원격 세션에서 디스플레이 설정을 바꿀 수 없다는 안내가 있었다.
- Windows `quser`: 사용자 `apxkf`, 세션 `rdp-tcp#0`, ID 1, `Active`. 이전 콘솔 캡처 시험과 달리 지금은 원격 터미널에 연결돼 있다.
- Present PnP: NVIDIA RTX 2070과 `Microsoft Remote Display Adapter` 모두 `OK`. 원격 어댑터 ID는 `SWD\REMOTEDISPLAYENUM\RDPIDD_INDIRECTDISPLAY&SESSIONID_0001`. 모니터는 `DISPLAY\DEFAULT_MONITOR\1&C528B8A&0&UID256`, Generic Monitor가 `OK`였다. `WmiMonitorID`는 항목을 반환하지 않았다.
- `Win32_VideoController`에서 원격 어댑터는 3840×2160, 보고 주사율 59Hz였다. NVIDIA 항목은 별도로 1024×768@60을 보고했다. 이 모드 값으로 실제 전송 fps를 판정하지 않았다.

정지 화면에서는 RemoteFX Graphics의 입력·출력 fps가 0이었다. 따라서 세션이나 해상도 설정을 바꾸지 않고 640×360 WinForms 창에서 16ms 타이머로 이동 도형을 그리는 한시 시험을 했다. 창은 20초 후 자동 종료했다. 시작 4초 후부터 1초 간격으로 12개 RDP-Tcp 0 샘플을 수집했다.

| 항목 | 관측 |
|---|---|
| OutputFramesPerSecond | 35, 31, 31, 31, 31, 30, 30, 31, 31, 31, 31, 27 |
| 중앙값 | 31fps |
| AverageEncodingTime | 11개 샘플 0ms, 1개 샘플 1ms(카운터의 정수값) |
| 서버·네트워크·클라이언트 자원 부족 skipped 카운터 | 수집한 모든 샘플에서 0 |
| 시험 예약 작업 종료 코드 | 0 |

[Microsoft 카운터 설명](https://learn.microsoft.com/en-us/azure/virtual-desktop/remotefx-graphics-performance-counters)에 따라 Output Frames/Second는 클라이언트에 제공되는 프레임 수다. **Mac의 실제 표시 완료 fps·입력 지연을 측정한 값이 아니다.** 4K 원격 화면 안의 작은 변경 영역 시험이므로 4K 전체 화면 동영상 성능으로 해석하지 않는다. 앞선 Deskhand 시험과 워크로드·해상도도 달라 직접적인 배수 비교는 하지 않는다.

이 관측은 물리 모니터가 없는 조건에서 지금의 RDP 화면 경로가 약 30fps의 출력을 제공한다는 근거다. 따라서 앞서 측정한 Deskhand 콘솔 캡처의 약 13fps를 이 PC나 headless Windows 전체의 불가피한 상한으로 설명해서는 안 된다. 현재 Jump는 Windows 기본 원격 디스플레이 경로를 사용하며, Deskhand 별도 시험 드라이버의 적재 효과는 아직 증명되지 않았다.

원시 결과: `/tmp/deskhand-jump-live-20261022.txt`, `/tmp/deskhand-jump-motion-result-20261022.txt`. 시험 창·자체 예약 작업·원격 시험 스크립트는 종료·제거했다. Jump 연결, 세션 소속, 기존 디스플레이 설정은 유지했다. 이번 요청을 시험 드라이버 설치·시험 모드·재부팅의 승인으로 해석하지 않았다.
