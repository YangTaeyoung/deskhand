#!/usr/bin/env bash
# scripts/wintest.sh [--ssh] <go 패키지> [go test 인수…]
#
# Cross-builds the test binary of a Go package for windows/amd64 and runs it
# on the SSH host DESKHAND_WINTEST_HOST (default "windows") in a folder of
# its own, <base>\<name>-<run id>\, with the package's testdata/; <base> is
# DESKHAND_WINTEST_DIR (default C:\Users\apxkf\dev\deskhand-test, an
# absolute Windows path without spaces or quotes):
#   - default: in the signed-in user's session, through an interactive
#     scheduled task DeskhandTest-<run id> (highest run level), with
#     DESKHAND_WINTEST=1;
#   - --ssh: directly in the SSH session (Session 0).
# scripts/wintestlaunch starts the binary there without any console window
# and kills it after DESKHAND_WINTEST_WAIT seconds (default 480; at most 510
# with --ssh). go test flags (-run, -v, -count, -timeout, …) become -test.*
# flags after the defaults -test.v and -test.timeout (nine tenths of the
# wait). Prints the test output, deletes the run's task and folder, and
# exits with the binary's exit code: 0 only for 0, otherwise its low byte,
# or 1 when that byte is 0 (124: killed at the wait limit; 125: the task did
# not start).
set -euo pipefail
cd "$(dirname "$0")/.."

host="${DESKHAND_WINTEST_HOST:-windows}"
wait_secs="${DESKHAND_WINTEST_WAIT:-480}"
base_dir="${DESKHAND_WINTEST_DIR:-C:\Users\apxkf\dev\deskhand-test}"
ssh_opts=(-o BatchMode=yes -o ClearAllForwardings=yes -o ServerAliveInterval=15)
if ssh -G -o WarnWeakCrypto=no localhost >/dev/null 2>&1; then
  ssh_opts+=(-o WarnWeakCrypto=no)
fi

mode=task
if [ "${1:-}" = "--ssh" ]; then
  mode=ssh
  shift
fi
if [ $# -lt 1 ]; then
  echo "사용법: scripts/wintest.sh [--ssh] <go 패키지> [go test 인수…]" >&2
  exit 2
fi
pkg="$1"
shift
if ! [[ "$wait_secs" =~ ^[0-9]+$ ]] || [ "$wait_secs" -lt 1 ]; then
  echo "wintest: DESKHAND_WINTEST_WAIT는 1 이상의 초 단위 정수여야 합니다." >&2
  exit 2
fi
if ! [[ "$base_dir" =~ ^[A-Za-z]:(\\[A-Za-z0-9._-]+)+$ ]]; then
  echo "wintest: DESKHAND_WINTEST_DIR는 공백·따옴표 없는 Windows 절대 경로(예: C:\Users\me\dev\deskhand-test)여야 합니다." >&2
  exit 2
fi
if [ "$mode" = ssh ] && [ "$wait_secs" -gt 510 ]; then
  echo "wintest: --ssh에서는 DESKHAND_WINTEST_WAIT를 510초 이하로 지정하세요." >&2
  exit 2
fi

export CGO_ENABLED=0 GOOS=windows GOARCH=amd64
pkg_dir="$(go list -f '{{.Dir}}' "$pkg")"
name="$(basename "$pkg_dir")"
name="${name//[^A-Za-z0-9_-]/_}"
run_id="$$-${RANDOM}${RANDOM}"
task="DeskhandTest-${run_id}"
remote_dir="${base_dir}\\${name}-${run_id}"
remote_scp="${remote_dir//\\//}"
exe="${name}.test.exe"
launcher="wintestlaunch.exe"

work="$(mktemp -d "${TMPDIR:-/tmp}/wintest.XXXXXX")"
remote_ssh() { ssh "${ssh_opts[@]}" "$host" "$@"; }

# kill_run ends the processes whose image lies in the run's folder (the
# launcher and the test binary) with their process trees.
kill_run='$d='"'${remote_dir}\\'"'; Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($d, [StringComparison]::OrdinalIgnoreCase) } | ForEach-Object { taskkill.exe /F /T /PID $_.ProcessId | Out-Null }'

task_created=0
remote_created=0
finished=0
cleanup() {
  if [ "$task_created" = 1 ]; then
    remote_ssh "schtasks /End /TN \"${task}\" >nul 2>&1 & schtasks /Delete /TN \"${task}\" /F >nul 2>&1" || true
  fi
  if [ "$remote_created" = 1 ]; then
    if [ "$finished" != 1 ]; then
      remote_ssh "powershell -NoProfile -NonInteractive -Command \"${kill_run}\"" || true
    fi
    left="$(remote_ssh "if exist \"${remote_dir}\" rmdir /s /q \"${remote_dir}\" 2>nul & rmdir \"${base_dir}\" >nul 2>&1 & if exist \"${remote_dir}\" echo left" | tr -d '\r\n ')" || left=unknown
    if [ "$left" != "" ]; then
      echo "wintest: 경고: ${host}의 ${remote_dir}가 남아 있을 수 있습니다. 실행 중인 테스트가 없는지 확인한 뒤 지우세요." >&2
    fi
  fi
  rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

go test -c -o "$work/$exe" "$pkg"
if [ ! -f "$work/$exe" ]; then
  echo "wintest: ${pkg}에 테스트 파일이 없습니다. _test.go 파일이 있는 패키지를 지정하세요." >&2
  exit 2
fi
go build -ldflags=-H=windowsgui -o "$work/$launcher" ./scripts/wintestlaunch

args=(-test.v "-test.timeout=$(( wait_secs * 9 / 10 + 1 ))s")
for a in "$@"; do
  if [[ "$a" =~ ^--?(run|skip|v|count|timeout|short|failfast|parallel|bench|benchtime|benchmem|cpu|list|shuffle|fullpath)(=.*)?$ ]]; then
    a="-test.${BASH_REMATCH[1]}${BASH_REMATCH[2]}"
  fi
  if [[ "$a" == *$'\n'* || "$a" == *$'\r'* ]]; then
    echo "wintest: 줄바꿈이 들어간 인수는 넘길 수 없습니다." >&2
    exit 2
  fi
  args+=("$a")
done

{
  printf 'exe %s\r\n' "$exe"
  printf 'limit %s\r\n' "$wait_secs"
  printf 'env DESKHAND_HOME=%s\\home\r\n' "$remote_dir"
  if [ "$mode" = task ]; then
    printf 'env DESKHAND_WINTEST=1\r\n'
  fi
  for a in "${args[@]}"; do
    printf 'arg %s\r\n' "$a"
  done
} > "$work/run.txt"

cat > "$work/wait.ps1" <<'PS1'
param([int]$Seconds)
$dir = Split-Path -Parent $MyInvocation.MyCommand.Path
$end = (Get-Date).AddSeconds($Seconds)
while ($true) {
  if (Test-Path (Join-Path $dir 'exit.txt')) { 'done'; exit 0 }
  if ((Get-Date) -ge $end) { break }
  Start-Sleep -Milliseconds 500
}
if (Test-Path (Join-Path $dir 'pid.txt')) { 'running' } else { 'waiting' }
PS1

remote_created=1
remote_ssh "mkdir \"${remote_dir}\\home\"" >/dev/null
files=("$work/$exe" "$work/$launcher" "$work/run.txt" "$work/wait.ps1")
if [ -d "$pkg_dir/testdata" ]; then
  files+=("$pkg_dir/testdata")
fi
scp -q -r "${ssh_opts[@]}" "${files[@]}" "${host}:${remote_scp}/"

if [ "$mode" = task ]; then
  echo "wintest: ${pkg} → ${host} ${remote_dir} (예약 작업 ${task})" >&2
  user="$(remote_ssh "echo %USERNAME%" | tr -d '\r\n ')"
  task_created=1
  if ! out="$(remote_ssh "schtasks /Create /TN \"${task}\" /TR \"${remote_dir}\\${launcher}\" /SC ONCE /ST 00:00 /IT /RL HIGHEST /RU \"${user}\" /F 2>&1 && schtasks /Run /TN \"${task}\" 2>&1")"; then
    printf '%s\n' "$out" | iconv -f CP949 -t UTF-8 >&2 || true
    echo "wintest: 예약 작업 ${task}을 만들거나 실행하지 못했습니다. 위 schtasks 출력을 확인하세요." >&2
    exit 1
  fi
else
  echo "wintest: ${pkg} → ${host} ${remote_dir} (SSH 세션)" >&2
  remote_ssh "start \"\" /wait \"${remote_dir}\\${launcher}\"" || true
  state="$(remote_ssh "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File \"${remote_dir}\\wait.ps1\" -Seconds 5" | tr -d '\r\n ')" || state=error
  if [ "$state" != done ]; then
    echo "wintest: SSH 세션에서 ${launcher}가 결과를 남기지 않았습니다(${state}). ${host}의 ${remote_dir}를 확인하세요." >&2
    exit 1
  fi
fi

started_at="$(date +%s)"
failures=0
while [ "$mode" = task ]; do
  state="$(remote_ssh "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File \"${remote_dir}\\wait.ps1\" -Seconds 30" | tr -d '\r\n ')" || state=error
  if [ "$state" = done ]; then
    break
  fi
  if [ "$state" = error ]; then
    failures=$(( failures + 1 ))
    if [ "$failures" -ge 3 ]; then
      echo "wintest: ${host}에 연속 3번 접속하지 못했습니다. 네트워크와 SSH를 확인하세요." >&2
      exit 1
    fi
  else
    failures=0
  fi
  elapsed=$(( $(date +%s) - started_at ))
  if [ "$state" = waiting ] && [ "$elapsed" -ge 30 ]; then
    echo "wintest: 예약 작업 ${task}이 30초 안에 시작되지 않았습니다. PC에 ${host}의 사용자가 로그인해 있는지 확인하세요." >&2
    exit 125
  fi
  if [ "$elapsed" -ge $(( wait_secs + 60 )) ]; then
    echo "wintest: $(( wait_secs + 60 ))초 안에 결과가 없습니다. DESKHAND_WINTEST_WAIT를 늘리거나 -run으로 테스트를 줄이세요." >&2
    exit 124
  fi
done

finished=1
scp -q "${ssh_opts[@]}" "${host}:${remote_scp}/out.txt" "$work/out.txt" 2>/dev/null || true
if [ -f "$work/out.txt" ]; then
  cat "$work/out.txt"
fi
scp -q "${ssh_opts[@]}" "${host}:${remote_scp}/exit.txt" "$work/exit.txt"
code="$(tr -d '\r\n ' < "$work/exit.txt")"
if ! [[ "$code" =~ ^-?[0-9]+$ ]]; then
  echo "wintest: 종료 코드 '${code}'를 읽지 못했습니다." >&2
  exit 1
fi
echo "wintest: 종료 코드 ${code}" >&2
if [ "$code" -eq 0 ]; then
  exit 0
fi
low=$(( code & 255 ))
if [ "$low" -eq 0 ]; then
  low=1
fi
exit "$low"
