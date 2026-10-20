# ADR 0001: 화면 파이프라인 — 순수 Go DXGI + Media Foundation

상태: 채택 (2026-10-20, 스파이크 `spikes/capture/` 실측 기반)

## 결정

- 캡처: DXGI Desktop Duplication(사용자 세션에서만 동작). 데스크톱 텍스처를 자체 BGRA 텍스처로 `CopyResource`한 뒤 처리한다.
- 색 변환·축소: D3D11 VideoProcessorBlt(BGRA 전체 범위 → NV12 BT.709 제한 범위). CPU 변환은 대체 경로로만 둔다.
- 인코딩: Media Foundation H.264. 하드웨어 MFT(비동기, D3D11 인식) 우선, 소프트웨어 "H264 Encoder MFT"(동기, 호출자 출력 버퍼 할당) 대체. 저지연 모드, CBR, GOP 0(주기 IDR 없음) + 요청 키프레임(`CODECAPI_AVEncVideoForceKeyFrame`) + 데몬 측 주기 요청.
- 스레드: 캡처·변환 생산자와 비동기 MFT 이벤트 루프를 분리한다(직렬 처리 시 4K 30fps로 떨어짐).
- cgo, 외부 실행 파일(ffmpeg 등) 없이 `syscall.SyscallN`으로 COM vtable을 호출한다.

## 실측 (Windows 11 Pro, Ryzen 7 3800X, RTX 2070, 합성 BGRA 소스)

| 경로 | 결과 |
|---|---|
| NVIDIA H.264 Encoder MFT, 3840×2160, 60fps 페이싱 | 60.1 fps, ProcessInput→출력 p50 13.5 ms / p95 14.8 ms, 20 Mbps |
| 같은 MFT, 4K→1920×1080 | 60.2 fps, p50 4.0 ms / p95 6.6 ms |
| 소프트웨어 MFT, 4K (GPU 변환 + readback) | 22.2 fps |
| 소프트웨어 MFT, 1080 | 51.2 fps |
| GPU NV12 변환 4K | p50 2.65 ms |
| GDI BitBlt 3008×1692 | 17.8 fps |

두 인코더 모두 Annex-B로 내보내고 IDR마다 SPS/PPS를 넣는다. 4K 출력은 High@5.2이며 Mac ffmpeg로 오류 없이 디코딩됐다.

## 세션 제약

- SSH(Session 0)에서는 출력 열거가 `DXGI_ERROR_NOT_CURRENTLY_AVAILABLE`. 에이전트는 사용자 세션(예약 작업)에서 실행한다.
- 세션이 RDP에 연결돼 있으면 출력은 RDP 클라이언트 해상도의 가상 디스플레이이고, 클라이언트 창이 보이지 않는 동안 DD와 GDI가 `E_ACCESSDENIED`/실패를 반환한다(클라이언트의 출력 억제로 추정, 확인 필요). 에이전트는 이를 `DISPLAY_UNAVAILABLE`(사유 `rdp_output_suppressed`)로 보고하고, 사람이 승인하면 세션을 콘솔로 옮기는 동작을 제공한다.
- 같은 프로세스에서 같은 출력을 두 번 `DuplicateOutput`하면 `E_INVALIDARG` — 이전 복제를 먼저 해제한다.

## vtable 인덱스

`spikes/capture/` 보고 기준. DXGI: Factory1.EnumAdapters1 12, Adapter1.EnumOutputs 7/GetDesc1 10, Output.GetDesc 7, Output1.DuplicateOutput 22, OutputDuplication.GetDesc 7/AcquireNextFrame 8/GetFrameDirtyRects 9/GetFrameMoveRects 10/GetFramePointerShape 11/ReleaseFrame 14. D3D11: Device.CreateTexture2D 5/CreateQuery 24, Context.Map 14/Unmap 15/End 28/GetData 29/CopyResource 47/UpdateSubresource 48/Flush 111, VideoDevice.CreateVideoProcessor 4/InputView 8/OutputView 9/Enumerator 10, VideoContext.Blt 53. MF: Transform.ProcessMessage 23/ProcessInput 24/ProcessOutput 25, EventGenerator.GetEvent 3, ICodecAPI.SetValue 9.
