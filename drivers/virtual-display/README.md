# Deskhand Virtual Display (실험 단계)

물리 모니터가 없는 Windows 11 x64(빌드 22000 이상) 콘솔의 화면 공급 속도를 검증하기 위한 자체 UMDF/IddCx 드라이버다. 하나의 EDID 없는 가상 모니터에 1920×1080, 1600×900, 1280×720의 60Hz 모드를 제공한다. 프레임을 네트워크로 보내지 않으며, 화면 전송은 기존 Deskhand 에이전트가 담당한다.

아직 제품 기본 설치에 포함하지 않는다. 빌드 성공과 모니터의 60Hz 모드 표시는 화면 전송 30fps 통과의 증거가 아니다. 드라이버 설치·제거, 모니터가 꺼진 실기기의 캡처·인코딩·입력·재연결을 별도로 검증해야 한다.

## 출처와 라이선스

`Driver.cpp`는 Microsoft [IddSample](https://github.com/microsoft/Windows-driver-samples/tree/bb68ed5e770bce04dc5395ec9258dafab034184d/video/IndirectDisplay)의 API 초기화·모드·swapchain 처리 패턴을 바탕으로 작성했다. 원본 커밋은 `bb68ed5e770bce04dc5395ec9258dafab034184d`이며 파생 소스에는 동봉한 MS-PL을 적용한다. 다른 제조사의 EDID, 샘플 장치 ID, 전역 전원 설정은 사용하지 않는다.

## 빌드

Microsoft [EWDK](https://learn.microsoft.com/en-us/windows-hardware/drivers/download-the-wdk#enterprise-wdk-ewdk)의 `LaunchBuildEnv.cmd` 환경에서:

```bat
msbuild DeskhandVirtualDisplay.vcxproj /p:Configuration=Release /p:Platform=x64 /p:SignMode=Off
```

INF 검증·카탈로그 생성은 WDK가 수행한다. 테스트 서명·신뢰 등록·설치는 별도 절차이며 이 빌드 명령은 실행하지 않는다.

`build-test-package.ps1`은 빌드된 DLL·스탬프된 INF와 WDK의 Inf2Cat·SignTool 경로를 받아 새 출력 폴더에 7일짜리 실험 패키지를 만든다. DLL·카탈로그를 서명하고 SHA-256 manifest를 남기며, 서명 개인 키는 `finally`에서 제거한다. 시스템·사용자 신뢰 저장소나 부팅 정책은 바꾸지 않는다.

이후 패키지를 Mac으로 복사해 OpenSSL과 [osslsigncode](https://github.com/mtrojnar/osslsigncode)로 검증한다. 인증서를 해당 명령에만 파일로 지정하므로 SSH의 인증서 신뢰 확인 창이 필요하지 않다.

```sh
python3 verify-package.py /path/to/package --osslsigncode /path/to/osslsigncode
```

검증 범위는 DLL·CAT 서명, DLL의 카탈로그 포함 여부, 네 파일의 manifest 해시다. INF의 카탈로그 포함 여부나 Windows 드라이버 로딩 통과를 뜻하지 않는다. 성공하면 manifest의 `verification`이 갱신되고, 설치 스크립트는 이 검증 표시가 없는 패키지를 거부한다.

## 공용 PC의 시험 설치와 제거

관리자 PowerShell에서 `test-driver.ps1 -PackageDirectory <패키지>`는 파일 해시·서명자를 대조하고 시험 인증서를 로컬 컴퓨터 Root·TrustedPublisher에 등록한 뒤 패키지만 Driver Store에 추가한다. 장치는 만들지 않는다. 사전에 같은 Deskhand 디스플레이 패키지가 있으면 교체하지 않고 실패한다.

제거는 `test-driver.ps1 -PackageDirectory <패키지> -Remove`다. 설치 기록의 INF 해시·게시 이름을 확인하고, 이 실험에서 새로 추가한 신뢰만 지운다. 사용 중인 드라이버를 강제로 제거하지 않는다. 프로세스 수명이 제한된 실험 도구를 먼저 종료해야 한다. 설치 도중 실패하면 같은 제거 절차로 되돌린다.

스크립트는 테스트 서명 모드·Secure Boot·재부팅을 자동으로 바꾸지 않는다. Microsoft의 [UMDF 샘플 수동 배포 절차](https://learn.microsoft.com/en-us/samples/microsoft/windows-driver-samples/toaster-sample-umdf-version-2/)에는 시험 서명 활성화와 인증서 등록이 포함되어 있다. 다만 2026-10-22 실제 검증 PC에서는 시험 인증서를 등록한 뒤 **TESTSIGNING 변경·재부팅 없이** 이 패키지가 적재됐고, 가상 모니터 생성·제거 10회와 장치 재시작을 통과했다. 이 관측을 모든 Windows 환경의 설치 조건으로 일반화하지 않는다. 부팅 정책 변경이 필요하면 현재 상태를 확인하고 별도로 취급한다. 시험 인증서는 출시 서명이 아니며, 만료 후 반복 사용하지 않는다. [실기기 검증 기록](../../docs/acceptance/headless-display-build.md)

## 수명이 제한된 장치 실험

```sh
GOOS=windows GOARCH=amd64 go build -o build/virtual-display-probe.exe ./scripts/diagnostics/virtual-display
```

드라이버 패키지가 준비된 PC에서 관리자 권한으로 `virtual-display-probe.exe -seconds 120`을 실행한다. `SwDeviceCreate`는 `DeskhandVirtualDisplay` 하드웨어 ID만 요청하고, 프로세스가 보유한 핸들을 닫으면 PnP가 장치를 제거한다([생성](https://learn.microsoft.com/en-us/windows/win32/api/swdevice/nf-swdevice-swdevicecreate), [종료](https://learn.microsoft.com/en-us/windows/win32/api/swdevice/nf-swdevice-swdeviceclose)). 이 도구는 드라이버 설치나 인증서 신뢰 등록을 하지 않는다. 출력의 `enumerated`는 드라이버 시작·화면 준비 완료 판정이 아니다.
