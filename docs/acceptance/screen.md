# 화면·입력 실기기 실측

측정: 2026-10-20 20:09–20:14(KST), 브랜치 `feat/media-track`의 `0134697`(에이전트 `time.sync`, fixture `--animate` 포함) 위의 `scripts/e2e/screen-probe`. 같은 날 19:59–20:05에 같은 절차로 한 번 더 돌렸고, 그 값은 각 표 아래 "앞선 실행"에 적었다.

## 1. 환경

| 항목 | 값 |
|---|---|
| PC | Windows 11 Pro, Ryzen 7 3800X, RTX 2070(ADR 0001과 같은 PC), Wi-Fi(Private 프로필) |
| 디스플레이 | `\\.\DISPLAY1` 3840×2160 @60 Hz, 144 DPI(주, index 0) + `\\.\DISPLAY2` 3840×2160(x=3840). 측정은 fixture가 뜬 DISPLAY1 |
| 세션 | session 1, `console`, `active`, 잠김 아님, 입력 데스크톱 `Default`, 에이전트 무결성 `high` |
| 인코더 | NVIDIA H.264 Encoder MFT(하드웨어), 캡처 `dd` |
| 시스템 타이머 해상도 | 모든 실행의 시작·끝에서 1.0 ms(`NtQueryTimerResolution`) |
| 에이전트 | 설치된 `Deskhand Agent` 예약 작업·실행 파일은 건드리지 않았다. 이 브랜치의 에이전트를 별도 인스턴스로 띄웠다: 실행마다 `C:\Users\apxkf\dev\deskhand-test\screenprobe-<id>\`, 대화형 예약 작업 `DeskhandTest-<id>`(최고 권한)과 `wintestlaunch`, `127.0.0.1:47601`, 버리는 페어링 인증서 한 쌍. 끝나면 런처의 프로세스 트리(pid)만 끝내고 작업·폴더를 지웠다 |
| 측정 도구 | `screen-probe`(데몬 없이 QUIC로 붙는 Go 프로그램)를 **같은 PC에서** SSH 세션으로 실행해 루프백 QUIC로 붙었다(§6.1). 받은 스트림은 Mac으로 가져와 Mac ffmpeg 9.0.2로 디코딩했다 |

## 2. 방법

- 시나리오(각각 새 QUIC 연결, `app.launch`로 fixture를 띄우고 `win.action focus`, 끝나면 `proc.kill`):
  - `static`: fixture(정지) 창이 앞에 있는 DISPLAY1, 60초. 화면 변화는 fixture의 Message 입력란 캐럿 깜빡임(스트림 시작 3.4초 뒤 멈췄다)과 작업 표시줄 시계뿐이다.
  - `motion`: `deskhand-fixture --animate`(640×360 논리 픽셀 = 960×540 물리 픽셀 패널을 합성마다 다시 그림), 60초, 원본 해상도.
  - `motion1920`: 같은 fixture, `max_width` 1920, 30초.
  - `input`: fixture(정지)에 클릭 20회, Space 20회(§4).
  - 모두 `video` 헤더 기본값(60 fps, 원본 4K는 20 Mbps, 1920은 6 Mbps).
- 시계 보정: 연결마다 측정 앞뒤로 `time.sync` 50회(5 ms 간격). 오프셋(에이전트 `mono_us` − 프로브 시계)은 RTT가 가장 작은 왕복의 `mono_us − (보낸 시각 + 받은 시각)/2`. 프로브 시계는 QPC.
- 캡처→수신 = 프로브 수신 시각 − (`capture_mono_us` − 오프셋). 첫 AU(인코더 생성 직후)는 따로 적고 분포에서 뺐다. 캡처→인코딩 완료는 `encode_done_mono_us − capture_mono_us`(에이전트 시계만 씀).
- 비트레이트·fps 평균은 스트림을 연 때부터 닫을 때까지의 AU 바이트·개수. 에이전트 `stats`(1초마다)도 함께 적었다.
- 디코딩: `ffmpeg -v error -i <x>.h264 -f null -`의 오류 줄 수, `ffprobe -count_frames`의 프레임 수.
- 백분위는 최근접 순위.

### 2.1 시계 보정의 불확실성

| 실행 | 최소 RTT | RTT 중앙값 | 빠른 8회 오프셋 폭 | 측정 뒤 오프셋 변화 |
|---|---|---|---|---|
| static | 0.168 ms | 0.198 ms | 0.021 ms | +0.001 ms |
| motion | 0.170 ms | 0.229 ms | 0.023 ms | −0.001 ms |
| motion1920 | 0.172 ms | 0.206 ms | 0.070 ms | −0.001 ms |
| input | 0.169 ms | 0.214 ms | 0.021 ms | −0.002 ms |

- 왕복 오차 한계는 최소 RTT의 절반, 약 ±0.09 ms.
- 에이전트의 `mono_us`는 Go 단조 시계이고, Windows에서 Go 1.26.0의 단조 시계는 `KUSER_SHARED_DATA`의 인터럽트 시각을 읽는다(`runtime/sys_windows_amd64.s:200`, `nanotime1`). 이 값은 타이머 틱마다(측정 중 1 ms) 바뀌므로, 같은 PC에서 Go 단조 시계의 증가 폭은 p50 1 ms(최소 0.44, 최대 1.69 ms, 1초 표본)였다. 그래서 에이전트가 찍는 `capture_mono_us`·`done_mono_us`는 실제보다 0–1 ms 이를 수 있고, 이 값을 쓰는 지연(캡처→수신, 주입 완료→변화 캡처)은 0–1 ms 크게 나올 수 있다. `time.sync`는 프로브가 5 ms 잠든 뒤(틱 직후) 보내므로 그 오차가 작고 빠른 8회의 오프셋 폭이 0.02–0.07 ms로 모였다.
- 결론: 시계 보정한 값의 불확실성은 −1.1 ms ~ +0.1 ms 수준이다.

## 3. 영상 결과(콘솔)

| 시나리오 | 출력 | AU / 시간 | fps 평균(stats p50) | 비트레이트 평균(stats p50) | AU 크기 p50 / p95 | 캡처→수신 p50 / p95 (최대) | 캡처→인코딩 완료 p50 / p95 | 인코딩 완료→수신 p50 / p95 | dropped / encoder_busy | 디코딩 |
|---|---|---|---|---|---|---|---|---|---|---|
| static | 3840×2160 | 15 / 60.1 s | 0.2 | 0.02 Mbps | 4,599 / 51,473 B | 25.82 / 100.12 ms (100.12) | 24.09 / 92.43 ms | 0.55 / 7.69 ms | 0 / 0 | 오류 0, 15프레임 |
| motion | 3840×2160 | 3,585 / 60.0 s | 59.7 (60.0) | 20.75 (19.73) Mbps | 43,253 / 49,279 B | 15.72 / 16.13 ms (28.40) | 14.65 / 15.12 ms | 1.07 / 1.64 ms | 0 / 0 | 오류 0, 3,585프레임 |
| motion1920 | 1920×1080 | 1,785 / 30.0 s | 59.4 (60.0) | 6.52 (6.57) Mbps | 13,354 / 16,577 B | 7.71 / 13.82 ms (22.76) | 7.35 / 13.17 ms | 0.47 / 1.04 ms | 0 / 0 | 오류 0, 1,785프레임 |
| input | 3840×2160 | 105 / 47.3 s | 2.2 | 0.08 Mbps | 3,271 / 8,814 B | 30.65 / 46.58 ms (98.70) | 30.03 / 46.20 ms | 0.47 / 1.13 ms | 0 / 0 | 오류 0, 105프레임 |

- 모든 스트림이 Mac ffmpeg로 오류 없이 디코딩됐고 프레임 수 = AU 수다. 4K는 High L5.2, 1920은 High L4.2, 색 정보 `tv`/`bt709`.
- 에이전트 `stats`의 `encode_ms_p50`(초당 값의 p50): motion 14.54 ms, motion1920 7.25 ms, static 26.47 ms, input 30.26 ms. `capture_ms_p50`은 모두 0.2 ms 이하.
- 스트림을 연 뒤 첫 AU(IDR)까지 256–269 ms(두 실행의 8개 스트림).
- static의 AU 15개: IDR 1개, 그 6.6 ms 뒤 캡처한 내용 프레임 1개(§5.1), 캐럿 깜빡임 등 7개(약 0.53초 간격, 3.4초까지), 마지막 AU에서 10초마다 반복 AU 5개, 56.2초(20:11:00 무렵, 작업 표시줄 시계의 분이 바뀐 때)의 변화 1개. 화면이 바뀌지 않으면 보내지 않고 10초마다 반복한다는 규칙(protocol §4)대로다. 드문 프레임은 인코딩이 motion보다 오래 걸린다(§5.2).
- 앞선 실행: motion 캡처→수신 15.83 / 16.28 ms, 59.7 fps, 20.96 Mbps; motion1920 7.83 / 13.94 ms, 59.5 fps, 6.55 Mbps; static 24.11 / 100.39 ms; 디코딩 오류 모두 0.

## 4. 입력 결과(콘솔)

- 방법: `input` 스트림(gen 0)으로 fixture의 첫 번째 Same 버튼 가운데로 `move`, 측정하지 않는 준비 클릭 1회(포커스가 버튼으로 옮겨 가 캐럿이 사라짐) 뒤, 클릭 20회(`button` left down → 400 ms → up → 600 ms)와 Space 20회(`key` vk 0x20 down → 400 ms → up → 600 ms). 버튼에 포커스가 있으므로 Space는 그 버튼을 누른다.
- 보호: 이벤트마다 그 직전에 `win.list`로 앞 창이 fixture인지 확인했고, button down 직전에는 `ui.at`으로 대상 점의 요소가 fixture 창의 `Same`인지 확인했다. 어긋나면 입력 스트림을 닫고(에이전트가 그 스트림이 누른 것을 놓는다) 멈추게 했다. 두 실행 모두 83개(이동 1, 준비 2, 측정 80) 전부 보호를 통과했고 실패한 ACK는 없다.
- 화면 변화: 받은 스트림을 디코딩해 fixture 창 영역(1062×770)을 회색조로 자르고, 입력을 보내기 전에 받은 마지막 프레임과 밝기 차 24 이상인 픽셀이 40개 이상인 첫 프레임을 변화로 봤다(입력 뒤 1초 안). 80개 모두 변화를 찾았고, 변화 프레임은 854–2,777픽셀, 입력 뒤 그 앞까지 받은 프레임들은 모두 0픽셀이었다.

| 입력 | ACK 왕복 p50 / p95 | 송신→주입 완료(보정) p50 / p95 | 주입 완료→변화 캡처 p50 / p95 | 송신→변화 AU 수신 p50 / p95 (최대) |
|---|---|---|---|---|
| 클릭 down | 13.60 / 15.28 ms | 13.09 / 15.02 ms | 18.2 / 18.9 ms | 61.4 / 63.1 ms (63.3) |
| 클릭 up | 0.62 / 0.80 ms | 0.29 / 0.32 ms | 30.8 / 31.5 ms | 62.0 / 62.6 ms (62.7) |
| Space down | 2.46 / 2.70 ms | 2.35 / 2.41 ms | 10.8 / 19.0 ms | 42.9 / 50.6 ms (51.4) |
| Space up | 0.52 / 0.56 ms | 0.28 / 0.31 ms | 19.8 / 31.8 ms | 54.2 / 63.3 ms (64.5) |

(각 20개)

- "송신→변화 AU 수신"은 프로브 시계만 쓴 값(입력을 쓴 때부터 변화가 담긴 AU를 다 받은 때까지, 디코딩·표시 제외)이다. 클릭 down의 p50 61.4 ms는 대략 주입 13 + 앱·합성 18 + 캡처→인코딩 완료 30 + 전송 0.5 ms로 나뉜다.
- 앞선 실행: 클릭 down ACK 14.64 / 15.53 ms, 송신→변화 62.6 / 63.3 ms; 클릭 up 62.4 / 62.7 ms; Space down 44.3 / 50.9 ms; Space up 52.8 / 62.5 ms.

## 5. 발견

### 5.1 원본 해상도 첫 IDR이 검은 화면

8개 스트림 중 7개(4K 6개 전부, 1920 2개 중 1개)의 첫 AU(IDR)가 전부 검은 화면이다(4K 2,249 B, 1920 657 B; 디코딩한 첫 프레임의 밝기 최댓값 0). 다음 AU(P 프레임, 39–74 KB)가 4.9–76 ms 뒤 캡처한 화면 내용을 담고, 받는 쪽에서는 첫 AU 수신 뒤 5–18 ms 안에 도착했다. 화면이 계속 바뀌지 않는 PC에서 그 두 번째 프레임이 늘 오는지는 확인하지 않았다. 의심 지점: 새 복제의 첫 프레임을 이미지 유무와 상관없이 복사한다(`internal/agent/capture/dd_windows.go` `take`의 `s.fresh`) — 원인 확인 필요.

### 5.2 드문 프레임의 인코딩 지연

연속 움직임에서 캡처→인코딩 완료는 14.65 ms(4K)인데, 프레임이 드물 때(static, input)는 24–30 ms(p50)이다. 입력→화면 지연에서 가장 큰 몫이다. 원인(GPU 클록 상태, 하드웨어 MFT의 출력 시점 등) 확인 필요.

### 5.3 버튼 down 주입 시간

클릭 down의 ACK가 13.6 ms(p50)로 up(0.6 ms)이나 Space down(2.5 ms)보다 길다. 에이전트는 down과 up을 같은 경로로 처리하고 차이는 `SendInput` 호출 안에서 생긴다(송신→주입 완료 13.1 ms 대 0.3 ms). 원인(이 PC의 저수준 마우스 훅 등) 확인 필요.

## 6. 세션 상태별

| 상태 | 결과 | 근거 |
|---|---|---|
| 콘솔(session 1 console, active) | §3·§4 | 이 문서의 실측 |
| RDP 표시 | 미측정 — 사람이 RDP로 접속했을 때 재측정 | 에이전트는 RDP 상태를 만들지 않는다(tscon·로그오프·RDP 접속 없음) |
| RDP 숨김(클라이언트 창이 보이지 않음) | 캡처 불가. 출력은 RDP 가상 디스플레이(스파이크 당시 `\\.\DISPLAY49` 3008×1692)이고 `DuplicateOutput`이 `0x80070005`(`E_ACCESSDENIED`), GDI `GetPixel`이 `CLR_INVALID`였다(150초 223회 모두 실패, 545초 동안 회복 없음). 에이전트는 RDP 세션의 접근 거부를 `rdp_output_suppressed`로 분류하므로(`internal/agent/capture/classify.go`, 단위 테스트 `classify_test.go`) `video`는 `paused`(`rdp_output_suppressed`)를 보내고 caps `screen`은 `needs_user`가 된다 | ADR 0001 세션 제약, 스파이크 기록 `spikes/capture/artifacts/{probe,watch2,e2}.log`. 이 에이전트로 RDP 숨김 상태를 종단 측정하지는 않았다 |

### 6.1 측정하지 않은 것

- **Mac↔PC 네트워크 구간.** PC 방화벽은 모든 프로필에서 켜져 있고 기본 인바운드 동작은 `NotConfigured`(Windows 기본값 차단)이며, Deskhand 허용 규칙은 설치된 에이전트의 UDP 47600뿐이다(다른 포트로 실제로 닿는지는 시험하지 않았다). 테스트 에이전트를 다른 포트로 띄우라는 결정과 보안 설정을 바꾸지 말라는 결정을 함께 지키려고, 방화벽 규칙을 더하지 않고 프로브를 PC에서 루프백으로 돌렸다. 그래서 위 지연에는 네트워크가 빠져 있다(인코딩 완료→수신 0.5–1 ms는 루프백 QUIC과 에이전트 쓰기 큐). 참고로 Mac→PC TCP 연결 왕복(포트 22, 40회)은 p50 4.68 / p95 15.10 ms(최대 69.3 ms)였다. 재측정: P1 병합 때 설치된 에이전트(47600)에 Mac의 `screen-probe run -addr <PC>:47600`으로 붙거나, 사람이 임시 방화벽 규칙을 허락하면 `run.sh`의 프로브를 Mac에서 돌린다.
- RDP 표시 상태(위 표).
- CPU 사용률(M3에서 4K60 움직임 논리 CPU 1개의 약 20.9%로 측정).

## 7. 재현

```
scripts/e2e/screen-probe/run.sh <Mac 결과 폴더> [static|motion|motion1920|input …]
```

- 필요한 것: `ssh windows`(BatchMode), PC에 그 사용자가 로그인한 콘솔 세션, Mac에 ffmpeg/ffprobe.
- 환경 변수: `DESKHAND_WINTEST_HOST`(기본 `windows`), `DESKHAND_WINTEST_DIR`(기본 `C:\Users\apxkf\dev\deskhand-test`), `DESKHAND_PROBE_PORT`(기본 47601).
- 결과 폴더에 시나리오마다 `<이름>.json`(AU·입력·time.sync 기록)과 `<이름>.h264`, 에이전트 로그, `report.json`이 남고, 같은 내용을 `screen-probe report`가 한국어로 출력한다. `.h264`는 저장소에 넣지 않는다.
