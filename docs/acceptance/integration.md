# Deskhand 2단계 통합 실기기 검증 (2026-10-21)

## 환경과 판정

- Mac: `feat/phase2-integration` 브랜치의 앱·데몬·MCP 빌드. Windows: `ssh windows`(192.168.88.165), Windows 11 x64, 로그인된 콘솔 세션 1, `deskhand-agentw` 실행.
- PC는 기존 에이전트의 다른 컨트롤러 인증서로 페어링되어 있었다. 새 Mac 데몬의 신원으로 `deskhand host add 'Windows PC' --ssh windows --force`를 실행해 재페어링했다. 결과는 `host_id:windows-pc`, `state:ready`, `caps.screen/input/uia:ready`, 연결 세대 1. 기존 컨트롤러 신원은 교체되었다.
- 통합 앱 `app/build/Deskhand.app`에서 `Windows PC`가 연결됨으로 표시되고 원격 1024×768 화면이 실제로 렌더링됐다. 뷰어 통계에서 `PC→Mac 11 ms`, 인코더 `NVIDIA H.264 Encoder MFT · 하드웨어`가 표시됐다. 정지 화면에서 순간 `0 fps`로 관찰됐으므로 지속 FPS 성능 판정은 하지 않는다.
- MCP go-sdk `CommandTransport` 프로브에서 `screen_capture`가 1024×768 PNG 이미지 콘텐츠와 `capture_id`, 연결 세대, 유효 시각을 반환했다. `win_list`에서 테스트용 `Deskhand Fixture` 창을 찾고 `ui_tree`에서 Message·Send·Transcript를 읽었다.
- MCP `input_type{method:auto,text:"Deskhand 검증 😀 첫 줄"}`는 `paste`로 실행됐다. 같은 캡처 ID의 좌표로 `input_click`을 보내자 `ui_text` Transcript에 `You: Deskhand 검증 😀 첫 줄`이 나타났다.
- 사람 제어권 선점 실기기: MCP가 `unicode` 80,000자 입력 중일 때 CLI 로컬 API가 `control.acquire{as:"human",force:true}`를 호출했다. 응답은 `gen:11`, `owner:human`. 진행 중 입력은 `typed 200 of 80000 characters` 뒤 `CONTROL_PREEMPTED`, `action.outcome:partial`, `next_action:check_state`로 끝났고, 이어진 `input_key`는 `CONTROL_HELD_BY_HUMAN`, `not_started`였다. 테스트 뒤 `control.release`로 `owner:none`을 확인했다. 눌린 키 상태를 독립적으로 측정한 결과는 없다.
- QA `fixture-smoke.yaml`: 첫 실행은 `Transcript` 라벨을 문서 대신 선택해 7단계에서 실패했다. 선택자에 `role:document`를 지정한 뒤 재실행 `q-01M5ENRSNFS49HYKTSAJJXBSSP`에서 **12/12 통과**, 실패·차단 0. 업로드, GUI 실행, UIA 한글 라벨·버튼, 응답, 화면 글, 스크린샷, 로그 파일, 정리까지 검증했다. 상세 결과는 Mac의 Deskhand 산출물 폴더에서 이 실행 ID의 `report.md`와 `manifest.json`을 확인한다.

## 남은 확인

- 한글·이모지 입력은 위 MCP 경로에서 확인했다. 줄바꿈을 포함한 텍스트를 fixture의 파일로 저장해 바이트 단위로 대조하는 시나리오는 아직 없다. 현재 fixture의 Message는 한 줄 입력이다(`cmd/deskhand-fixture/main_windows.go`). 이 항목은 미검증이다.
- UI 적대적 검수에서 지적된 사항의 수정과 재검수는 별도 진행 중이다. 현 실기기 화면 표시가 상업용 UX의 최종 승인 근거는 아니다.
- `host.update`, `stream.attach.max_fps`, AI 전달 범위의 데몬 적용, 산출물 보존 설정은 현재 통합 계획의 후속 계약 항목이며 이 검증에서 확인하지 않았다. 앱의 개인정보 설정은 AI 범위가 아직 적용되지 않는다고 명시한다.
