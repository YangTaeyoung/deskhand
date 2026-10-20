#!/usr/bin/env bash
# scripts/wintest.sh [--ssh] <go 패키지> [go test 인수…]
#
# Cross-builds the test binary of a Go package for windows/amd64, copies it
# and the package's testdata/ to C:\deskhand-test\<name>\ on the SSH host
# DESKHAND_WINTEST_HOST (default "windows") and runs it there:
#   - default: in the signed-in user's session, through the interactive
#     scheduled task DeskhandTest (highest run level), with DESKHAND_WINTEST=1;
#   - --ssh: directly in the SSH session (Session 0).
# go test flags (-run, -v, -count, -timeout, …) become -test.* flags; the
# binary always gets -test.v and -test.timeout=10m before them. Prints the
# test output, deletes the task and the remote folder, and exits with the
# test binary's exit code (124: no result within DESKHAND_WINTEST_WAIT
# seconds, default 660; 125: the task did not start).
set -euo pipefail
cd "$(dirname "$0")/.."

host="${DESKHAND_WINTEST_HOST:-windows}"
wait_secs="${DESKHAND_WINTEST_WAIT:-660}"
task="DeskhandTest"
ssh_opts=(-o BatchMode=yes -o ClearAllForwardings=yes)
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

export CGO_ENABLED=0 GOOS=windows GOARCH=amd64
pkg_dir="$(go list -f '{{.Dir}}' "$pkg")"
name="$(basename "$pkg_dir")"
remote_dir="C:\\deskhand-test\\${name}"
remote_scp="C:/deskhand-test/${name}"
exe="${name}.test.exe"

work="$(mktemp -d "${TMPDIR:-/tmp}/wintest.XXXXXX")"
remote_ssh() { ssh "${ssh_opts[@]}" "$host" "$@"; }

task_created=0
remote_created=0
cleanup() {
  if [ "$task_created" = 1 ]; then
    remote_ssh "schtasks /End /TN ${task} >nul 2>&1 & schtasks /Delete /TN ${task} /F >nul 2>&1" || true
  fi
  if [ "$remote_created" = 1 ]; then
    remote_ssh "taskkill /F /IM ${exe} >nul 2>&1 & if exist ${remote_dir} rmdir /s /q ${remote_dir} & rmdir C:\\deskhand-test >nul 2>&1" || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

go test -c -o "$work/$exe" "$pkg"
if [ ! -f "$work/$exe" ]; then
  echo "wintest: ${pkg}에 테스트 파일이 없습니다. _test.go 파일이 있는 패키지를 지정하세요." >&2
  exit 2
fi

args=(-test.v -test.timeout=10m)
for a in "$@"; do
  if [[ "$a" =~ ^--?(run|skip|v|count|timeout|short|failfast|parallel|bench|benchtime|benchmem|cpu|list|shuffle|fullpath)(=.*)?$ ]]; then
    a="-test.${BASH_REMATCH[1]}${BASH_REMATCH[2]}"
  fi
  if [[ "$a" == *'"'* ]]; then
    echo "wintest: 큰따옴표가 들어간 인수는 넘길 수 없습니다: $a" >&2
    exit 2
  fi
  args+=("$a")
done

quoted=""
for a in "${args[@]}"; do
  quoted+=" \"${a//%/%%}\""
done
{
  printf '@echo off\r\n'
  printf 'cd /d "%s"\r\n' "$remote_dir"
  printf 'set DESKHAND_HOME=%s\\home\r\n' "$remote_dir"
  if [ "$mode" = task ]; then
    printf 'set DESKHAND_WINTEST=1\r\n'
  fi
  printf '"%s\\%s"%s > "%s\\out.txt" 2>&1\r\n' "$remote_dir" "$exe" "$quoted" "$remote_dir"
  printf '> "%s\\exit.tmp" echo %%ERRORLEVEL%%\r\n' "$remote_dir"
  printf 'move /y "%s\\exit.tmp" "%s\\exit.txt" > nul\r\n' "$remote_dir" "$remote_dir"
} > "$work/run.cmd"

cat > "$work/wait.ps1" <<'PS1'
param([int]$Seconds)
$dir = Split-Path -Parent $MyInvocation.MyCommand.Path
$end = (Get-Date).AddSeconds($Seconds)
$startBy = (Get-Date).AddSeconds(30)
while ((Get-Date) -lt $end) {
  if (Test-Path (Join-Path $dir 'exit.txt')) { 'done'; exit 0 }
  if (-not (Test-Path (Join-Path $dir 'out.txt')) -and (Get-Date) -gt $startBy) { 'notstarted'; exit 0 }
  Start-Sleep -Milliseconds 500
}
'timeout'
PS1

remote_created=1
remote_ssh "if exist ${remote_dir} rmdir /s /q ${remote_dir}" >/dev/null
remote_ssh "mkdir ${remote_dir}\\home" >/dev/null
files=("$work/$exe" "$work/run.cmd" "$work/wait.ps1")
if [ -d "$pkg_dir/testdata" ]; then
  files+=("$pkg_dir/testdata")
fi
scp -q -r "${ssh_opts[@]}" "${files[@]}" "${host}:${remote_scp}/"

echo "wintest: ${pkg} → ${host} ${remote_dir} (${mode})" >&2
if [ "$mode" = task ]; then
  user="$(remote_ssh "echo %USERNAME%" | tr -d '\r\n ')"
  task_created=1
  if ! out="$(remote_ssh "schtasks /Create /TN ${task} /TR \"${remote_dir}\\run.cmd\" /SC ONCE /ST 00:00 /IT /RL HIGHEST /RU ${user} /F 2>&1 && schtasks /Run /TN ${task} 2>&1")"; then
    printf '%s\n' "$out" | iconv -f CP949 -t UTF-8 >&2 || true
    echo "wintest: 예약 작업 ${task}을 만들거나 실행하지 못했습니다. 위 schtasks 출력을 확인하세요." >&2
    exit 1
  fi
else
  remote_ssh "cmd /c ${remote_dir}\\run.cmd"
fi

state="$(remote_ssh "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File ${remote_dir}\\wait.ps1 -Seconds ${wait_secs}" | tr -d '\r')"

scp -q "${ssh_opts[@]}" "${host}:${remote_scp}/out.txt" "$work/out.txt" 2>/dev/null || true
if [ -f "$work/out.txt" ]; then
  cat "$work/out.txt"
fi
case "$state" in
  done)
    scp -q "${ssh_opts[@]}" "${host}:${remote_scp}/exit.txt" "$work/exit.txt"
    code="$(tr -dc '0-9' < "$work/exit.txt")"
    echo "wintest: 종료 코드 ${code}" >&2
    exit "${code:-1}"
    ;;
  notstarted)
    echo "wintest: 예약 작업 ${task}이 30초 안에 시작되지 않았습니다. PC에 ${host}의 사용자가 로그인해 있는지 확인하세요." >&2
    exit 125
    ;;
  *)
    echo "wintest: ${wait_secs}초 안에 결과가 없습니다. DESKHAND_WINTEST_WAIT를 늘리거나 -run으로 테스트를 줄이세요." >&2
    exit 124
    ;;
esac
