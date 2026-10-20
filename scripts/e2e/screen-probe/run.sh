#!/usr/bin/env bash
# scripts/e2e/screen-probe/run.sh <local output folder> [scenario…]
#
# Measures this tree's deskhand-agent on the SSH host DESKHAND_WINTEST_HOST
# (default "windows") with screen-probe, without touching an installed agent:
#   - builds deskhand-agent, deskhand-fixture, wintestlaunch and screen-probe
#     for windows/amd64 and copies them to <base>\screenprobe-<run id>\,
#     <base> being DESKHAND_WINTEST_DIR as in scripts/wintest.sh;
#   - pairs a throwaway agent identity that listens on 127.0.0.1 port
#     DESKHAND_PROBE_PORT (default 47601) with a throwaway probe identity;
#   - starts that agent in the signed-in user's session through the
#     interactive scheduled task DeskhandTest-<run id> and wintestlaunch;
#   - runs screen-probe on the PC over loopback for each scenario: static
#     (60 s), motion (60 s, fixture --animate), motion1920 (30 s, max_width
#     1920), input (20 clicks and 20 Space presses on the fixture's Same
#     button); the default is all four;
#   - copies the results and the agent log into the local folder, ends the
#     launcher's process tree, deletes the task and the folder and runs
#     screen-probe report on the local folder.
set -euo pipefail
cd "$(dirname "$0")/../../.."

if [ $# -lt 1 ]; then
  echo "사용법: scripts/e2e/screen-probe/run.sh <결과 폴더> [static|motion|motion1920|input …]" >&2
  exit 2
fi
local_out="$1"
shift
scenarios=("$@")
if [ ${#scenarios[@]} -eq 0 ]; then
  scenarios=(static motion motion1920 input)
fi
for s in "${scenarios[@]}"; do
  case "$s" in
    static|motion|motion1920|input) ;;
    *) echo "screen-probe: 알 수 없는 시나리오 ${s}입니다. static, motion, motion1920, input 중에서 고르세요." >&2; exit 2 ;;
  esac
done

host="${DESKHAND_WINTEST_HOST:-windows}"
base_dir="${DESKHAND_WINTEST_DIR:-C:\Users\apxkf\dev\deskhand-test}"
port="${DESKHAND_PROBE_PORT:-47601}"
if ! [[ "$base_dir" =~ ^[A-Za-z]:(\\[A-Za-z0-9._-]+)+$ ]]; then
  echo "screen-probe: DESKHAND_WINTEST_DIR는 공백·따옴표 없는 Windows 절대 경로여야 합니다." >&2
  exit 2
fi
if ! [[ "$port" =~ ^[0-9]+$ ]] || [ "$port" -lt 1024 ] || [ "$port" -gt 65535 ]; then
  echo "screen-probe: DESKHAND_PROBE_PORT는 1024–65535 사이의 포트여야 합니다." >&2
  exit 2
fi
ssh_opts=(-o BatchMode=yes -o ClearAllForwardings=yes -o ServerAliveInterval=15)
if ssh -G -o WarnWeakCrypto=no localhost >/dev/null 2>&1; then
  ssh_opts+=(-o WarnWeakCrypto=no)
fi
remote_ssh() { ssh "${ssh_opts[@]}" "$host" "$@"; }

run_id="$$-${RANDOM}${RANDOM}"
task="DeskhandTest-${run_id}"
remote_dir="${base_dir}\\screenprobe-${run_id}"
remote_scp="${remote_dir//\\//}"
work="$(mktemp -d "${TMPDIR:-/tmp}/screenprobe.XXXXXX")"
mkdir -p "$local_out"

task_created=0
remote_created=0
launcher_pid=""
cleanup() {
  if [ -n "$launcher_pid" ]; then
    remote_ssh "taskkill /F /T /PID ${launcher_pid} >nul 2>&1" || true
  elif [ "$remote_created" = 1 ]; then
    remote_ssh "powershell -NoProfile -NonInteractive -Command \"\$d='${remote_dir}\\'; Get-CimInstance Win32_Process | Where-Object { \$_.ExecutablePath -and \$_.ExecutablePath.StartsWith(\$d, [StringComparison]::OrdinalIgnoreCase) } | ForEach-Object { taskkill.exe /F /T /PID \$_.ProcessId | Out-Null }\"" || true
  fi
  if [ "$task_created" = 1 ]; then
    remote_ssh "schtasks /End /TN \"${task}\" >nul 2>&1 & schtasks /Delete /TN \"${task}\" /F >nul 2>&1" || true
  fi
  if [ "$remote_created" = 1 ]; then
    sleep 1
    left="$(remote_ssh "if exist \"${remote_dir}\" rmdir /s /q \"${remote_dir}\" 2>nul & rmdir \"${base_dir}\" >nul 2>&1 & if exist \"${remote_dir}\" echo left" | tr -d '\r\n ')" || left=unknown
    if [ "$left" != "" ]; then
      echo "screen-probe: 경고: ${host}의 ${remote_dir}가 남아 있을 수 있습니다. 실행 중인 프로세스가 없는지 확인한 뒤 지우세요." >&2
    fi
  fi
  rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

if [ -n "$(remote_ssh "netstat -ano -p UDP | findstr /C:\"127.0.0.1:${port} \" /C:\"0.0.0.0:${port} \"" | tr -d '\r' || true)" ]; then
  echo "screen-probe: ${host}의 UDP ${port}를 이미 다른 프로세스가 쓰고 있습니다. DESKHAND_PROBE_PORT로 다른 포트를 고르세요." >&2
  exit 1
fi

(
  export CGO_ENABLED=0 GOOS=windows GOARCH=amd64
  go build -o "$work/deskhand-agent.exe" ./cmd/deskhand-agent
  go build -o "$work/deskhand-fixture.exe" ./cmd/deskhand-fixture
  go build -ldflags=-H=windowsgui -o "$work/wintestlaunch.exe" ./scripts/wintestlaunch
  go build -o "$work/screen-probe.exe" ./scripts/e2e/screen-probe
)
probe_fp="$(go run ./scripts/e2e/screen-probe keygen -dir "$work/probe" | sed -n 's/.*"fingerprint":"\([0-9a-f]*\)".*/\1/p')"
if [ ${#probe_fp} -ne 64 ]; then
  echo "screen-probe: 프로브 인증서를 만들지 못했습니다." >&2
  exit 1
fi

remote_created=1
remote_ssh "mkdir \"${remote_dir}\\out\"" >/dev/null
scp -q -r "${ssh_opts[@]}" "$work/deskhand-agent.exe" "$work/deskhand-fixture.exe" "$work/wintestlaunch.exe" "$work/screen-probe.exe" "$work/probe" "${host}:${remote_scp}/"
init_out="$(remote_ssh "\"${remote_dir}\\deskhand-agent.exe\" init --name screen-probe --controller-fp ${probe_fp} --listen 127.0.0.1:${port} --config \"${remote_dir}\\home\\agent.json\"" | tr -d '\r')"
agent_fp="$(printf '%s\n' "$init_out" | sed -n 's/.*"fingerprint":"\([0-9a-f]*\)".*/\1/p')"
if [ ${#agent_fp} -ne 64 ]; then
  echo "screen-probe: 테스트 에이전트를 초기화하지 못했습니다: ${init_out}" >&2
  exit 1
fi

limit=$(( 300 + ${#scenarios[@]} * 240 ))
{
  printf 'exe deskhand-agent.exe\r\n'
  printf 'arg run\r\n'
  printf 'arg --config\r\n'
  printf 'arg %s\\home\\agent.json\r\n' "$remote_dir"
  printf 'limit %s\r\n' "$limit"
} > "$work/run.txt"
scp -q "${ssh_opts[@]}" "$work/run.txt" "${host}:${remote_scp}/run.txt"

user="$(remote_ssh "echo %USERNAME%" | tr -d '\r\n ')"
task_created=1
if ! out="$(remote_ssh "schtasks /Create /TN \"${task}\" /TR \"${remote_dir}\\wintestlaunch.exe\" /SC ONCE /ST 00:00 /IT /RL HIGHEST /RU \"${user}\" /F 2>&1 && schtasks /Run /TN \"${task}\" 2>&1")"; then
  printf '%s\n' "$out" | iconv -f CP949 -t UTF-8 >&2 || true
  echo "screen-probe: 예약 작업 ${task}을 만들거나 실행하지 못했습니다." >&2
  exit 1
fi
for _ in $(seq 1 30); do
  pids="$(remote_ssh "if exist \"${remote_dir}\\pid.txt\" type \"${remote_dir}\\pid.txt\"" | tr -d '\r' || true)"
  if [ -n "$pids" ]; then
    launcher_pid="${pids%% *}"
    break
  fi
  sleep 1
done
if [ -z "$launcher_pid" ]; then
  echo "screen-probe: 테스트 에이전트가 30초 안에 시작되지 않았습니다. PC에 ${host}의 사용자가 로그인해 있는지 확인하세요." >&2
  exit 125
fi
echo "screen-probe: ${host} ${remote_dir}, 에이전트 ${pids} (예약 작업 ${task}, udp 127.0.0.1:${port})" >&2
sleep 3

status=0
for s in "${scenarios[@]}"; do
  args=(-scenario static -seconds 60)
  case "$s" in
    motion) args=(-scenario motion -seconds 60) ;;
    motion1920) args=(-scenario motion -seconds 30 -max-width 1920) ;;
    input) args=(-scenario input -clicks 20 -keys 20) ;;
  esac
  echo "screen-probe: ${s}" >&2
  if ! remote_ssh "\"${remote_dir}\\screen-probe.exe\" run -addr 127.0.0.1:${port} -id \"${remote_dir}\\probe\" -agent-fp ${agent_fp} -out \"${remote_dir}\\out\" -name ${s} -fixture \"${remote_dir}\\deskhand-fixture.exe\" ${args[*]}"; then
    status=1
  fi
done

{ scp -q -r "${ssh_opts[@]}" "${host}:${remote_scp}/out" "$work/" && cp -R "$work/out/." "$local_out/"; } || status=1
scp -q "${ssh_opts[@]}" "${host}:${remote_scp}/home/logs/agent.log" "$local_out/agent.log" 2>/dev/null || true
scp -q "${ssh_opts[@]}" "${host}:${remote_scp}/out.txt" "$local_out/agent-out.txt" 2>/dev/null || true
go run ./scripts/e2e/screen-probe report -dir "$local_out" || status=1
exit "$status"
