# 릴리즈 관리

현재 배포 대상은 macOS 27 이상 Apple Silicon이다. `app/scripts/package-release.sh VERSION IDENTITY NOTARY_PROFILE`가 개발용 앱과 별도의 Release 앱을 만들고 서명·공증·staple·DMG·SHA-256을 생성한다.

1. `scripts/test.sh`, `cd app/DeskhandKit && swift test`, `python3 -m unittest discover -s app/scripts/tests -v`를 실행한다. 네이티브 C 검사 3개는 앱 빌드 단계에도 실행된다.
2. 변경·릴리즈 노트를 커밋한다. 패키징은 추적 중인 수정과 staged 수정을 거부한다. 기존 `spikes/` 실험은 릴리즈 소스에 추가하지 않는다.
3. 유효한 Developer ID Application 인증서와 기존 notarytool 프로파일을 지정해 패키징한다. 인증 정보나 개인 키를 저장소·릴리즈 파일·명령 인자로 기록하지 않는다.
4. 결과 DMG를 읽기 전용으로 마운트해 앱의 버전·CPU 아키텍처·서명·Gatekeeper·공증 티켓·앱 내부 엔진 로드·CLI 실행을 확인한다. 검증 후 마운트를 해제한다.
5. GitHub 저장소·소스 업로드 범위가 승인된 뒤 릴리즈 태그와 소스를 push한다. DMG와 `SHA256SUMS`, 릴리즈 노트를 프리릴리즈로 게시한다. 업로드한 파일을 다시 다운로드해 SHA-256을 대조한다.

프리뷰의 남은 성능·AI 범위·개인정보·장기 복구 제한은 릴리즈 노트에서 삭제하지 않는다. 서명·공증·테스트 통과를 전체 상업용 완성도나 Mac 모니터 표시 FPS의 근거로 쓰지 않는다.

참고: [Apple 공증](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution), [공증 워크플로](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow), [GitHub release create](https://cli.github.com/manual/gh_release_create).

`check-minimum-os.py`는 앱·CLI·모든 내장 dylib의 모든 아키텍처 최소 OS를 읽어 앱의 선언보다 높은 항목이 있으면 패키징을 중단한다. 첫 내부 시도에서 발견한 macOS 26/27 불일치는 게시 전에 이 검사로 재현하고 수정했다.
