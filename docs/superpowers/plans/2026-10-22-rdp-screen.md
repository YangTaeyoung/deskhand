# RDP 화면 전환 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 커스텀 디스플레이 드라이버 없이 Deskhand 내부 RDP 화면과 기존 에이전트를 통합한다.
**Architecture:** 고정 FreeRDP 라이브러리 C 엔진 → Swift 프레임 표시. 영상 없는 기존 제어권 보호 입력 스트림과 에이전트 RDP 메타데이터 조회를 추가한다.
**Tech Stack:** Go, Swift 6/SwiftUI/AppKit, FreeRDP 3.32.2, C/CMake.
**Spec:** docs/superpowers/specs/2026-10-22-rdp-screen-design.md

## Global Constraints

암호는 메모리만; 서버 인증서 SHA-256 고정; NLA 유지; 사람 입력 세대 검사 유지; 에이전트 세션 불일치 시 입력 거부; spikes/ 보존; 모니터 전원 요구 없음.

## Review Focus

인증서 교체/불일치; 연결 중 취소와 프레임 폭주 메모리; AI 제어 중 사람 입력; 다른 Windows 계정/세션; RDP만 연결되거나 에이전트만 끊긴 상태.

### Task 1: 기존 실험 정리·설계 전환
- [x] 준비된 정확한 제거 스크립트로 시험 드라이버·추가 인증서 제거.
- [x] 제거 후 장치/드라이버/인증서와 BCD 상태 확인, 설계 배제 조건 갱신.

### Task 2: 에이전트/데몬 RDP 계약과 입력 전용 스트림
Files: internal/daemon/screen, internal/daemon/localapi, internal/agent/server, internal/wire, docs/local-api.md.
- [x] stream.attach kind=input 계약 추가. 기존 Viewer 입력 중재·해제 재사용, video/cursor upstream을 열지 않음.
- [x] session.rdp로 페어링된 에이전트의 리스너 port/certificate_sha256/session_id/user 조회. 다른 OS/조회 실패는 명시 오류.
- [x] 무영상 입력, AI 거부, 종료 후 키 해제, 잘못된 endpoint/세션 테스트와 Windows 교차 빌드.

### Task 3: 내장 RDP 엔진
Files: native/rdp/{CMakeLists.txt,deskhand_rdp.c,deskhand_rdp.h}, scripts/build-rdp.sh.
- [x] 고정 FreeRDP를 빌드하고 앱에서 읽을 엔진 dylib 생성. 안전한 C ABI로 new/run/stop/free/copy_latest_frame/state 제공.
- [x] 고정 인증서 검증·NLA·GDI frame callback·GFX channel·크기 제한·bounded latest-frame queue 구현. 엔진은 데스크톱 입력/클립보드/드라이브 공유를 제공하지 않음.
- [x] 잘못된 인증서/인자, 연결 중 취소, 재생성 테스트. 빌드 산출물 의존 경로·라이선스 기록.

### Task 4: 앱 통합·실기기 검증
Files: app/DeskhandKit/Sources/DeskhandRDP, DeskhandUI/Screen/RDP*, Package.swift, app/scripts/build-app.sh.
- [x] Swift loader/session·메모리 한정 로그인·RDP 화면을 실제 ScreenView 기본 경로로 연결. 기존 preview/snapshot은 유지.
- [x] 영상 없는 입력 스트림 연결, 동일 세션 검사, 제어권/에이전트 연결에 따른 입력 차단.
- [x] 설정·프레임 크기/갱신·종료·입력 차단 테스트, 앱 빌드·번들 코드 서명 검증.
- [ ] 실제 로그인/프레임·입력·재연결·독립 UI 검수. 접근이 없으면 정확한 중단 지점 기록하고 통과로 판정하지 않음.

## 검증 상태 (2026-10-22)

구현·빌드·테스트와 로그인 단계 독립 시각 검수는 완료했다. 실제 Windows 인증, 화면 FPS·입력 지연·재연결·제어권 전환 검증은 사용자 로그인 대기이며 통과하지 않았다. 상세 근거는 [RDP 수용 검증](../../acceptance/rdp-screen.md)에 기록한다.
