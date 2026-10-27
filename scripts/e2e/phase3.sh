#!/usr/bin/env bash
# Phase 3 end-to-end run against a real Windows PC (docs/acceptance/phase3.md).
#
#   scripts/e2e/phase3.sh [step ...]
#
# The run never touches the installed "Deskhand Agent" task or the default
# Deskhand home. It builds this checkout's agent, starts it as a test agent
# from a folder of its own on the PC, <base>\e2e3-<run id>, listening on
# 127.0.0.1 only, and pairs it with a throwaway daemon home on the Mac.
# Datagrams between the two travel through scripts/e2e/udprelay over the
# standard input and output of one ssh session: the daemon dials 127.0.0.1
# on the Mac, and every throughput and latency the run prints includes that
# ssh hop.
#
# Steps, in the order "all" runs them:
#   build    scripts/build.sh and the e2e helpers (apicall, e2ehost,
#            ptyprobe, udprelay for both sides, wintestlaunch)
#   up       new run folder on the PC, agent init, agent start (an
#            interactive task in the signed-in user's session, or the ssh
#            session when nobody is signed in), relay, host row, daemon
#   exec     A11: stdout/stderr, exit 7, timeout, stdin EOF, a long job
#            across a connection drop
#   pty      A11 PTY and terminal reattach after a connection drop
#   xfer     A12: text, binary, 1 GiB, 1000 files both ways, progress,
#            pause/resume, cancel
#   conflict A13: rename, move, the four conflict policies both ways
#   movefail A14: move with a copy failure, a hash mismatch and a source
#            that cannot be removed
#   resume   A15: source changed while paused, locked files
#   names    A16: Korean, spaces, emoji, long paths, case collisions,
#            junctions, dot segments
#   diag     diag system/services/events/crashes and --save
#   tunnel   tunnel to a loopback echo server on the PC
#   debug    CDP debugger on a pinned portable Electron with the
#            cmd/deskhand-fixture/electron app
#   qa       docs/examples/qa/fixture-smoke.yaml and its result bundle
#   measure  10 exec.run and files.stat calls and 3 connection drops
#   down     stop the daemon, relay and agent, delete the run folder and
#            the run's task and the run's Recycle Bin entries, check that
#            nothing of the run is left
# Not in "all": redeploy copies this checkout's agent into the running run
# and restarts the agent and the daemon.
#
# Environment: DESKHAND_E2E_SSH (ssh alias, default windows),
# DESKHAND_E2E_BASE (PC base folder, default
# C:\Users\apxkf\dev\deskhand-test), DESKHAND_E2E_AGENT_PORT (agent UDP
# port on the PC's loopback, default 47650), DESKHAND_E2E_RELAY_PORT (Mac
# relay port, default 47651), DESKHAND_E2E_AGENT_MODE (auto, task or ssh),
# DESKHAND_E2E_BIG (bytes of the big file, default 1073741824),
# DESKHAND_E2E_MANY (files of the many-file folder, default 1000),
# DESKHAND_E2E_ELECTRON (Electron version, default 44.6.0),
# DESKHAND_E2E_CACHE (Mac folder for the Electron download, default
# build/e2e-cache), DESKHAND_HOME (default: a new /tmp/dh-e3-XXXX,
# remembered in build/e2e3-home until down).
# Needs jq and python3 on the Mac.
# Every check prints "CHECK <step> PASS|FAIL|BLOCKED|NOT_RUN <what>"; the
# exit code is the number of failed checks.
set -uo pipefail
cd "$(dirname "$0")/../.." || exit 2
repo=$PWD

ssh_alias=${DESKHAND_E2E_SSH:-windows}
base_dir=${DESKHAND_E2E_BASE:-'C:\Users\apxkf\dev\deskhand-test'}
agent_port=${DESKHAND_E2E_AGENT_PORT:-47650}
relay_port=${DESKHAND_E2E_RELAY_PORT:-47651}
host=windows-e2e3
fails=0
ssh_opts=(-o BatchMode=yes -o ClearAllForwardings=yes -o ServerAliveInterval=15)
if ssh -G -o WarnWeakCrypto=no localhost >/dev/null 2>&1; then
  ssh_opts+=(-o WarnWeakCrypto=no)
fi

steps=("$@")
[ ${#steps[@]} -eq 0 ] && steps=(all)
mkdir -p build
if [ -z "${DESKHAND_HOME:-}" ]; then
  if [ -f build/e2e3-home ] && [ -d "$(cat build/e2e3-home)" ]; then
    DESKHAND_HOME=$(cat build/e2e3-home)
  else
    DESKHAND_HOME=$(mktemp -d /tmp/dh-e3-XXXX)
  fi
fi
export DESKHAND_HOME
echo "$DESKHAND_HOME" >build/e2e3-home
state="$DESKHAND_HOME/e2e3.env"
work="$DESKHAND_HOME/work"
mkdir -p "$work"
# shellcheck disable=SC1090
[ -f "$state" ] && . "$state"

dh() { "$repo/build/deskhand" "$@"; }
dhj() { "$repo/build/deskhand" --json "$@"; }
api() { "$repo/build/apicall" "$@"; }
now() { date '+%Y-%m-%dT%H:%M:%S%z'; }
log() { printf '[%s] %s\n' "$(now)" "$*"; }
check() { # check <step> <ok 0|1> <what>
  if [ "$2" = 0 ]; then log "CHECK $1 PASS $3"; else log "CHECK $1 FAIL $3"; fails=$((fails + 1)); fi
}
blocked() { log "CHECK $1 BLOCKED $2"; }
notrun() { log "CHECK $1 NOT_RUN $2"; }
ok() { "$@" >/dev/null 2>&1 && echo 0 || echo 1; }
setstate() { # setstate NAME VALUE
  touch "$state"
  grep -v "^$1=" "$state" >"$state.tmp" 2>/dev/null
  printf "%s=%q\n" "$1" "$2" >>"$state.tmp"
  mv "$state.tmp" "$state"
  printf -v "$1" '%s' "$2"
}
ms() { python3 -I -c 'import time; print(int(time.time()*1000))'; }
mbps() { python3 -I -c 'import sys; b,ms=int(sys.argv[1]),int(sys.argv[2]); print("%.1f" % (b/1e6/(ms/1000.0)) if ms>0 else "NaN")' "$1" "$2"; }
sha() { shasum -a 256 "$1" | cut -d' ' -f1; }

# rps runs the PowerShell script on stdin on the PC with UTF-8 output.
rps() {
  local script enc
  script='[Console]::OutputEncoding=[Text.Encoding]::UTF8; $ProgressPreference="SilentlyContinue"
'"$(cat)"
  enc=$(printf '%s' "$script" | iconv -f UTF-8 -t UTF-16LE | base64 | tr -d '\n')
  ssh "${ssh_opts[@]}" "$ssh_alias" powershell -NoProfile -NonInteractive -EncodedCommand "$enc" 2> >(grep -v -e '^\*\* ' -e '^#< CLIXML' >&2)
}

# pcq prints a PowerShell single-quoted literal of $1.
pcq() { printf "'%s'" "${1//\'/\'\'}"; }

# hostfield prints the field of the test host from host.list.
hostfield() { api host.list | jq -r --arg h "$host" ".hosts[] | select(.name == \$h) | .$1"; }

# wait_ready waits up to $1 seconds for the host to be ready.
wait_ready() {
  for _ in $(seq 1 $(($1 * 2))); do
    [ "$(hostfield state 2>/dev/null)" = ready ] && return 0
    sleep 0.5
  done
  return 1
}

# drop makes the relay drop every datagram for its -drop time (12 s), which
# outlasts the 8 s QUIC idle timeout, and waits until the host is ready
# again on a new connection generation; it prints the generation before,
# the generation after and the milliseconds from the drop to ready.
drop() {
  local gen0 gen1 t0
  gen0=$(hostfield connection_generation)
  t0=$(ms)
  kill -USR1 "$relay_pid"
  for _ in $(seq 1 120); do
    gen1=$(hostfield connection_generation)
    [ "$gen1" != "$gen0" ] && [ "$(hostfield state)" = ready ] && break
    sleep 0.5
  done
  echo "$gen0 $gen1 $(($(ms) - t0))"
}

# job_wait waits up to $2 seconds for job $1 to end and prints it.
job_wait() {
  local j
  for _ in $(seq 1 $(($2 * 2))); do
    j=$(api jobs.get "{\"job_id\":\"$1\"}")
    case "$(jq -r .status <<<"$j")" in
    running | waiting_for_user) sleep 0.5 ;;
    *) printf '%s\n' "$j"; return 0 ;;
    esac
  done
  printf '%s\n' "$j"
  return 1
}

# xfer_wait waits up to $2 seconds for transfer $1 to leave queued/running/
# verifying and prints transfer.get.
xfer_wait() {
  local t
  for _ in $(seq 1 $(($2 * 2))); do
    t=$(api transfer.get "{\"transfer_id\":\"$1\"}")
    case "$(jq -r .state <<<"$t")" in
    queued | running | verifying) sleep 0.5 ;;
    *) printf '%s\n' "$t"; return 0 ;;
    esac
  done
  printf '%s\n' "$t"
  return 1
}

# tstart starts a transfer: tstart DIRECTION LOCAL REMOTE [ON_CONFLICT]; it
# prints the envelope.
tstart() {
  api transfer.start "$(jq -cn --arg d "$1" --arg h "$host" --arg l "$2" --arg r "$3" --arg c "${4:-fail}" \
    '{direction:$d, host_id:$h, local:$l, remote:$r, on_conflict:$c}')"
}

# rhash prints the SHA-256 of the PC file $1 through files.hash.
rhash() { api -timeout 300s files.hash "$(jq -cn --arg h "$host" --arg p "$1" '{host_id:$h, path:$p}')" | jq -r '.sha256 // empty'; }

# rexists prints true or false for the PC path $1.
rexists() { api files.stat "$(jq -cn --arg h "$host" --arg p "$1" '{host_id:$h, path:$p}')" | jq -r '.exists'; }

kill_run_ps() { # PowerShell that ends the processes whose image is in the run folder
  printf '%s' '$d = '"$(pcq "$pcdir\\")"'; Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($d, [StringComparison]::OrdinalIgnoreCase) } | ForEach-Object { taskkill.exe /F /T /PID $_.ProcessId 2>&1 | Out-Null; "killed " + $_.ProcessId + " " + $_.Name }'
}

start_daemon() { dh daemon start >/dev/null && log "daemon: $(dh daemon status | head -1)"; }
stop_daemon() { dh daemon stop >/dev/null 2>&1 || true; }

step_build() {
  log "== build"
  scripts/build.sh &&
    CGO_ENABLED=0 go build -o build/apicall ./scripts/e2e/apicall &&
    CGO_ENABLED=0 go build -o build/mcpprobe ./scripts/e2e/mcpprobe &&
    CGO_ENABLED=0 go build -o build/e2ehost ./scripts/e2e/e2ehost &&
    CGO_ENABLED=0 go build -o build/ptyprobe ./scripts/e2e/ptyprobe &&
    CGO_ENABLED=0 go build -o build/udprelay ./scripts/e2e/udprelay &&
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o build/udprelay-windows-amd64.exe ./scripts/e2e/udprelay &&
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags=-H=windowsgui -o build/wintestlaunch-windows-amd64.exe ./scripts/wintestlaunch
  check build $? "scripts/build.sh and helpers ($(git describe --always --dirty))"
}

# console_user reports whether someone is signed in to the PC's console.
console_user() {
  rps <<'EOF' | tr -d '\r'
$e = @(Get-CimInstance Win32_Process -Filter "Name='explorer.exe'" | Where-Object { $_.SessionId -gt 0 })
if ($e.Count -gt 0) { 'user session=' + $e[0].SessionId } else { 'no user' }
EOF
}

# start_agent starts the run folder's agent in mode task (an interactive
# task in the signed-in user's session) or ssh (the ssh session) and waits
# until it listens on 127.0.0.1:$agent_port.
start_agent() {
  local mode=$1 out scp_dir=${pcdir//\\//}
  if [ "$mode" = task ]; then
    local user
    user=$(ssh "${ssh_opts[@]}" "$ssh_alias" "echo %USERNAME%" | tr -d '\r\n ')
    printf 'exe deskhand-agent.exe\r\nlimit 21600\r\narg run\r\narg --config\r\narg %s\\agent\\agent.json\r\n' "$pcdir" >"$work/run.txt"
    scp -q "${ssh_opts[@]}" "$work/run.txt" "$ssh_alias:$scp_dir/run.txt"
    setstate agent_task "DeskhandE2E-$run_id"
    ssh "${ssh_opts[@]}" "$ssh_alias" "schtasks /Create /TN \"$agent_task\" /TR \"$pcdir\\e2e3launch.exe\" /SC ONCE /ST 00:00 /IT /RL HIGHEST /RU \"$user\" /F >nul && schtasks /Run /TN \"$agent_task\" >nul"
    check up $? "test agent started through the interactive task $agent_task"
  else
    nohup perl -MPOSIX -e 'POSIX::setsid(); exec @ARGV' ssh "${ssh_opts[@]}" "$ssh_alias" \
      "\"$pcdir\\deskhand-agent.exe\" run --config \"$pcdir\\agent\\agent.json\"" >>"$DESKHAND_HOME/agent-ssh.log" 2>&1 &
    setstate agent_ssh_pid $!
    log "the test agent runs in the ssh session (Session 0), ssh pid $agent_ssh_pid"
  fi
  out=""
  for _ in $(seq 1 40); do
    out=$(rps <<EOF
\$p = Get-CimInstance Win32_Process -Filter "Name='deskhand-agent.exe'" | Where-Object { \$_.ExecutablePath -like $(pcq "$pcdir\\*") }
if (\$p) { 'agent pid=' + \$p.ProcessId + ' session=' + \$p.SessionId + ' udp=' + ((Get-NetUDPEndpoint -OwningProcess \$p.ProcessId -ErrorAction SilentlyContinue | ForEach-Object { \$_.LocalAddress + ':' + \$_.LocalPort }) -join ',') }
EOF
)
    grep -q "udp=127.0.0.1:$agent_port" <<<"$out" && break
    sleep 1
  done
  printf '%s\n' "$out"
  grep -q "udp=127.0.0.1:$agent_port" <<<"$out"
  check up $? "the test agent listens on udp 127.0.0.1:$agent_port only"
}

# stop_agent ends the run folder's agent.
stop_agent() {
  if [ -n "${agent_task:-}" ]; then
    ssh "${ssh_opts[@]}" "$ssh_alias" "schtasks /End /TN \"$agent_task\" >nul 2>&1 & schtasks /Delete /TN \"$agent_task\" /F >nul 2>&1"
    setstate agent_task ""
  fi
  rps <<EOF
Get-CimInstance Win32_Process -Filter "Name='deskhand-agent.exe'" | Where-Object { \$_.ExecutablePath -like $(pcq "$pcdir\\*") } | ForEach-Object { taskkill.exe /F /T /PID \$_.ProcessId 2>&1 | Out-Null; 'stopped agent ' + \$_.ProcessId }
EOF
  if [ -n "${agent_ssh_pid:-}" ]; then
    kill "$agent_ssh_pid" 2>/dev/null
    setstate agent_ssh_pid ""
  fi
}

step_redeploy() {
  log "== redeploy (this checkout's agent and daemon into run $run_id)"
  stop_daemon
  stop_agent
  sleep 1
  scp -q "${ssh_opts[@]}" build/deskhand-agent-windows-amd64.exe "$ssh_alias:${pcdir//\\//}/deskhand-agent.exe"
  check redeploy $? "agent copied"
  start_agent "$agent_mode"
  start_daemon
  wait_ready 40
  check redeploy $? "host $host ready (agent $(hostfield agent_version))"
}

step_up() {
  log "== up (DESKHAND_HOME=$DESKHAND_HOME)"
  if [ -n "${run_id:-}" ]; then
    log "a run is already up ($run_id); run the down step first"
    return
  fi
  stop_daemon
  setstate run_id "$(date +%m%d%H%M%S)"
  setstate pcdir "$base_dir\\e2e3-$run_id"
  local mode=${DESKHAND_E2E_AGENT_MODE:-auto} who fp out agent_fp
  who=$(console_user)
  log "PC console: $who"
  if [ "$mode" = auto ]; then
    case "$who" in user*) mode=task ;; *) mode=ssh ;; esac
  fi
  setstate agent_mode "$mode"
  setstate agent_session "$who"
  rps <<EOF
New-Item -ItemType Directory -Force -Path $(pcq "$pcdir\\agent"), $(pcq "$pcdir\\files"), $(pcq "$pcdir\\qa") | Out-Null
'run folder ' + (Test-Path $(pcq "$pcdir"))
EOF
  local scp_dir=${pcdir//\\//}
  scp -q "${ssh_opts[@]}" build/deskhand-agent-windows-amd64.exe "$ssh_alias:$scp_dir/deskhand-agent.exe" &&
    scp -q "${ssh_opts[@]}" build/udprelay-windows-amd64.exe "$ssh_alias:$scp_dir/udprelay.exe" &&
    scp -q "${ssh_opts[@]}" build/wintestlaunch-windows-amd64.exe "$ssh_alias:$scp_dir/e2e3launch.exe"
  check up $? "agent, relay and launcher copied to $pcdir"
  fp=$("$repo/build/e2ehost" fingerprint)
  log "controller fingerprint $fp"
  out=$(rps <<EOF
& $(pcq "$pcdir\\deskhand-agent.exe") init --name '$host' --controller-fp '$fp' --listen '127.0.0.1:$agent_port' --config $(pcq "$pcdir\\agent\\agent.json")
'exit=' + \$LASTEXITCODE
EOF
)
  printf '%s\n' "$out"
  agent_fp=$(sed -n 's/.*"fingerprint":"\([0-9a-f]*\)".*/\1/p' <<<"$out" | head -1)
  setstate agent_fp "$agent_fp"
  check up "$([ ${#agent_fp} = 64 ] && echo 0 || echo 1)" "agent init on 127.0.0.1:$agent_port (fingerprint $agent_fp)"
  start_agent "$mode"
  nohup perl -MPOSIX -e 'POSIX::setsid(); exec @ARGV' "$repo/build/udprelay" mac -listen "127.0.0.1:$relay_port" -log "$DESKHAND_HOME/relay.log" -- \
    ssh "${ssh_opts[@]}" "$ssh_alias" "\"$pcdir\\udprelay.exe\" pc -agent 127.0.0.1:$agent_port" >/dev/null 2>&1 &
  setstate relay_pid $!
  "$repo/build/e2ehost" put -id "$host" -name "$host" -address 127.0.0.1 -port "$relay_port" -fp "$agent_fp"
  check up $? "host row $host -> 127.0.0.1:$relay_port"
  start_daemon
  wait_ready 40
  check up $? "host $host ready through the relay (state $(hostfield state), generation $(hostfield connection_generation))"
  api session.inspect "{\"host_id\":\"$host\"}" | jq -c '{session_id, user, connect_state, protocol, caps: (.caps | map_values(.state))}'
}

step_down() {
  log "== down"
  stop_daemon
  [ -n "${relay_pid:-}" ] && kill "$relay_pid" 2>/dev/null
  if [ -n "${pcdir:-}" ]; then
    rps <<EOF
$(kill_run_ps)
EOF
    if [ -n "${agent_task:-}" ]; then
      ssh "${ssh_opts[@]}" "$ssh_alias" "schtasks /End /TN \"$agent_task\" >nul 2>&1 & schtasks /Delete /TN \"$agent_task\" /F >nul 2>&1"
    fi
    [ -n "${agent_ssh_pid:-}" ] && kill "$agent_ssh_pid" 2>/dev/null
    sleep 1
    local out
    out=$(rps <<EOF
$trash_ps
\$t = @(Get-RunTrash $(pcq "$pcdir"))
foreach (\$e in \$t) { Remove-Item -LiteralPath \$e.R -Recurse -Force -ErrorAction SilentlyContinue; Remove-Item -LiteralPath \$e.I -Force -ErrorAction SilentlyContinue }
'Recycle Bin entries of the run removed=' + \$t.Count + ' left=' + @(Get-RunTrash $(pcq "$pcdir")).Count
Remove-Item -LiteralPath $(pcq "$pcdir") -Recurse -Force -ErrorAction SilentlyContinue
if (Test-Path -LiteralPath $(pcq "$pcdir")) { Start-Sleep 2; cmd /c rmdir /s /q $(pcq "$pcdir") 2>&1 | Out-Null }
\$base = $(pcq "$base_dir")
if ((Test-Path -LiteralPath \$base) -and -not (Get-ChildItem -LiteralPath \$base -Force)) { Remove-Item -LiteralPath \$base -Force }
'left folder=' + (Test-Path -LiteralPath $(pcq "$pcdir")) + ' processes=' + @(Get-CimInstance Win32_Process | Where-Object { \$_.ExecutablePath -like $(pcq "$pcdir\\*") }).Count + ' task=' + [bool](Get-ScheduledTask -TaskName 'DeskhandE2E-*' -ErrorAction SilentlyContinue) + ' installed task=' + (Get-ScheduledTask -TaskName 'Deskhand Agent').State
EOF
)
    printf '%s\n' "$out"
    grep -q 'left=0' <<<"$out" && grep -q 'left folder=False processes=0 task=False' <<<"$out"
    check down $? "nothing of run $run_id is left on the PC"
  fi
  rm -rf "$work"
  rm -f "$state"
  rm -f build/e2e3-home
  log "Mac home $DESKHAND_HOME kept for the logs; delete it when done"
}

# pcproc prints "running" or "gone" for the PC process with id $1.
pcproc() { rps <<<"if (Get-Process -Id $1 -ErrorAction SilentlyContinue) { 'running' } else { 'gone' }" | tr -d '\r\n '; }

step_exec() {
  log "== exec (A11)"
  wait_ready 30
  local res t0 t1 out err code j pid g0 g1 dms
  res=$(api exec.run "$(jq -cn --arg h "$host" '{host_id:$h, cmd:"echo out-line & echo err-line 1>&2 & echo 안녕 & exit 7", shell:"cmd"}')")
  log "exec.run: $(jq -c '{status, code, stdout, stderr, error}' <<<"$res")"
  check exec "$(ok jq -e '.status == "failed" and .code == 7 and .error == null and (.stdout | gsub("\r";"") | split("\n") | map(sub(" +$";"")) | . == ["out-line","안녕",""]) and (.stderr | gsub("\r";"") | sub(" +\n";"\n")) == "err-line\n"' <<<"$res")" \
    "exec.run: stdout out-line/안녕, stderr err-line, exit 7 as failed without error"
  out=$(dh exec "$host" -- cmd /c "echo out-line & echo err-line 1>&2 & exit 7" 2>"$work/err.txt")
  code=$?
  err=$(cat "$work/err.txt")
  log "deskhand exec: exit $code, stdout $(printf '%q' "$out"), stderr $(printf '%q' "$err")"
  check exec "$([ "$code" = 7 ] && [ "$(tr -d '\r ' <<<"$out")" = out-line ] && [ "$(tr -d '\r ' <<<"$err")" = err-line ] && echo 0 || echo 1)" \
    "deskhand exec keeps stdout and stderr apart and exits 7"

  t0=$(ms)
  res=$(api exec.run "$(jq -cn --arg h "$host" '{host_id:$h, cmd:"ping -n 31 127.0.0.1", shell:"cmd", timeout_ms:2000}')")
  t1=$(ms)
  log "exec.run timeout_ms 2000: $(jq -c '{status, code, timed_out, error: .error.code}' <<<"$res"), $((t1 - t0)) ms"
  out=$(rps <<<"@(Get-CimInstance Win32_Process -Filter \"Name='PING.EXE'\" | Where-Object { \$_.CommandLine -like '*-n 31 127.0.0.1*' }).Count" | tr -d '\r\n ')
  check exec "$(jq -e '.status == "failed" and .timed_out == true and .error.code == "TIMEOUT"' <<<"$res" >/dev/null && [ $((t1 - t0)) -lt 6000 ] && [ "$out" = 0 ] && echo 0 || echo 1)" \
    "exec.run timeout: failed + TIMEOUT after $((t1 - t0)) ms, no ping left on the PC ($out)"
  t0=$(ms)
  dh exec "$host" --timeout 3s -- ping -n 32 127.0.0.1 >/dev/null 2>&1
  code=$?
  t1=$(ms)
  out=$(rps <<<"@(Get-CimInstance Win32_Process -Filter \"Name='PING.EXE'\" | Where-Object { \$_.CommandLine -like '*-n 32 127.0.0.1*' }).Count" | tr -d '\r\n ')
  check exec "$([ "$code" = 124 ] && [ $((t1 - t0)) -lt 8000 ] && [ "$out" = 0 ] && echo 0 || echo 1)" \
    "deskhand exec --timeout 3s exits 124 after $((t1 - t0)) ms, no ping left ($out)"

  res=$(api exec.run "$(jq -cn --arg h "$host" '{host_id:$h, cmd:"findstr /n \"^\"", shell:"cmd", stdin:"첫 줄\nsecond\n", timeout_ms:10000}')")
  log "exec.run stdin: $(jq -c '{status, code, stdout}' <<<"$res")"
  check exec "$(ok jq -e '.status == "succeeded" and (.stdout | gsub("\r";"")) == "1:첫 줄\n2:second\n"' <<<"$res")" "exec.run passes stdin and then EOF"
  t0=$(ms)
  out=$(dh exec "$host" --timeout 20s -- cmd /c "set /p X=& echo got-[%X%]" 2>&1)
  code=$?
  t1=$(ms)
  log "deskhand exec reading stdin: exit $code after $((t1 - t0)) ms: $(printf '%q' "$out")"
  check exec "$([ "$code" = 0 ] && [ $((t1 - t0)) -lt 5000 ] && grep -q 'got-\[' <<<"$out" && echo 0 || echo 1)" \
    "a command reading stdin under deskhand exec (exec.start) sees EOF at once"
  res=$(api exec.start "$(jq -cn --arg h "$host" '{host_id:$h, cmd:"findstr /n \"^\"", shell:"cmd", stdin:"a\nb\n"}')")
  j=$(job_wait "$(jq -r .job_id <<<"$res")" 15)
  out=$(api jobs.output "$(jq -cn --arg j "$(jq -r .job_id <<<"$res")" '{job_id:$j}')" | jq -r '[.chunks[].data] | join("")' | tr -d '\r')
  check exec "$([ "$(jq -r .status <<<"$j")" = succeeded ] && [ "$out" = $'1:a\n2:b' ] && echo 0 || echo 1)" "exec.start passes its stdin and then EOF ($(printf '%q' "$out"))"

  res=$(api exec.start "$(jq -cn --arg h "$host" '{host_id:$h, cmd:"ping -n 33 127.0.0.1", shell:"cmd"}')")
  pid=$(jq -r .pid <<<"$res")
  sleep 1
  res=$(api jobs.cancel "$(jq -cn --arg j "$(jq -r .job_id <<<"$res")" '{job_id:$j}')")
  sleep 1
  check exec "$([ "$(jq -r .status <<<"$res")" = cancelled ] && [ "$(pcproc "$pid")" = gone ] && echo 0 || echo 1)" "jobs.cancel ends a running job (pid $pid gone)"

  res=$(api exec.start "$(jq -cn --arg h "$host" '{host_id:$h, cmd:"echo start-long & ping -n 26 127.0.0.1 >nul & echo done-after-drop & exit 5", shell:"cmd"}')")
  j=$(jq -r .job_id <<<"$res")
  pid=$(jq -r .pid <<<"$res")
  sleep 2
  read -r g0 g1 dms <<<"$(drop)"
  log "connection drop for the long job: generation $g0 -> $g1, ready again after $dms ms, job $(api jobs.get "{\"job_id\":\"$j\"}" | jq -c '{status}')"
  res=$(job_wait "$j" 60)
  out=$(api jobs.output "{\"job_id\":\"$j\"}" | jq -r '[.chunks[].data] | join("")' | tr -d '\r')
  log "long job after the drop: $(jq -c '{status, result}' <<<"$res"), output $(printf '%q' "$out")"
  check exec "$(jq -e '.status == "failed" and .result.code == 5' <<<"$res" >/dev/null && grep -q start-long <<<"$out" && grep -q done-after-drop <<<"$out" && [ "$g1" -gt "$g0" ] && echo 0 || echo 1)" \
    "a job started before a connection drop (generation $g0 -> $g1) keeps running and ends with its output and exit code 5"
}

step_pty() {
  log "== pty (A11 PTY, terminal reattach)"
  wait_ready 30
  local res pty job out pid g0 g1 dms hold
  res=$(api pty.open "$(jq -cn --arg h "$host" '{host_id:$h, shell:"cmd", cols:120, rows:40}')")
  pty=$(jq -r .pty_id <<<"$res")
  job=$(jq -r .job_id <<<"$res")
  log "pty.open cmd: $(jq -c '{status, pty_id, job_id, shell: .job.result.shell, pid: .job.result.pid}' <<<"$res")"
  out=$("$repo/build/ptyprobe" -pty "$pty" -send $'echo pty-%COMPUTERNAME%-한글 출력 😀\r\n' -expect 'pty-DESKTOP-\w+-한글 출력 😀' -timeout 20s)
  log "ptyprobe: $(jq -c '{job, pid, matched, closed}' <<<"$out")"
  check pty "$(ok jq -e '.matched and .job != ""' <<<"$out")" "cmd PTY runs a command whose output has the computer name and Korean/emoji text"
  out=$("$repo/build/ptyprobe" -job "$job" -send $'exit 7\r\n' -hold 10s)
  log "exit 7: $(jq -c '{exited, exit_code, closed}' <<<"$out")"
  res=$(job_wait "$job" 15)
  check pty "$(jq -e '.exited and .exit_code == 7' <<<"$out" >/dev/null && jq -e '.status == "failed" and .result.code == 7' <<<"$res" >/dev/null && echo 0 || echo 1)" "exit 7 in the shell: exit frame 7 and job failed with code 7"

  res=$(api pty.open "$(jq -cn --arg h "$host" '{host_id:$h, shell:"powershell"}')")
  pty=$(jq -r .pty_id <<<"$res")
  job=$(jq -r .job_id <<<"$res")
  out=$("$repo/build/ptyprobe" -pty "$pty" -send $'$m = "before-drop"; "ready-$m"\r' -expect 'ready-before-drop' -timeout 30s)
  pid=$(jq -r .pid <<<"$out")
  log "powershell PTY pid $pid: $(jq -c '{matched}' <<<"$out")"
  check pty "$(ok jq -e '.matched' <<<"$out")" "powershell PTY runs a command"
  "$repo/build/ptyprobe" -job "$job" -send $'1..10 | % { "tick$_"; Start-Sleep 1 }\r' -hold 60s >"$work/pty-hold.json" &
  hold=$!
  sleep 3
  read -r g0 g1 dms <<<"$(drop)"
  wait "$hold"
  out=$(cat "$work/pty-hold.json")
  log "attached stream across the drop (generation $g0 -> $g1, ready after $dms ms): $(jq -c '{closed, exited}' <<<"$out")"
  check pty "$(ok jq -e '.closed == true and .exited == false' <<<"$out")" "the attached stream ends without an exit frame when the connection drops"
  check pty "$([ "$(pcproc "$pid")" = running ] && [ "$(api jobs.get "{\"job_id\":\"$job\"}" | jq -r .status)" = running ] && echo 0 || echo 1)" "the shell (pid $pid) and its job keep running after the drop"
  sleep 5
  out=$("$repo/build/ptyprobe" -job "$job" -send $'"after-$m"\r' -expect 'after-before-drop' -timeout 30s)
  log "reattach by job_id: $(jq -c '{pid, matched, attached}' <<<"$out")"
  check pty "$(jq -e --argjson p "$pid" '.matched and .pid == $p' <<<"$out" >/dev/null && echo 0 || echo 1)" "reattach by job_id reaches the same shell (pid $pid, variable kept)"
  check pty "$(ok jq -e '.output | test("ready-before-drop") and test("tick10")' <<<"$out")" "the scrollback replays output from before and during the drop (ready-before-drop … tick10)"
  res=$(api jobs.cancel "{\"job_id\":\"$job\"}")
  sleep 1
  check pty "$([ "$(jq -r .status <<<"$res")" = cancelled ] && [ "$(pcproc "$pid")" = gone ] && echo 0 || echo 1)" "jobs.cancel ends the shell (pid $pid gone)"

  out=$(printf 'echo cli-shell-%%USERNAME%%\r\nexit 3\r\n' | dh shell "$host" --shell cmd 2>&1)
  local code=$?
  check pty "$([ "$code" = 3 ] && grep -q 'cli-shell-apxkf' <<<"$out" && echo 0 || echo 1)" "deskhand shell with piped input runs the lines and exits 3 (exit $code)"
}

# xfer_when waits up to $3 seconds until transfer $1 matches the jq filter
# $2 (or ended) and prints transfer.get.
xfer_when() {
  local t
  for _ in $(seq 1 $(($3 * 5))); do
    t=$(api transfer.get "{\"transfer_id\":\"$1\"}")
    if jq -e "($2) or (.state | IN(\"queued\",\"running\",\"verifying\") | not)" <<<"$t" >/dev/null; then
      printf '%s\n' "$t"
      return 0
    fi
    sleep 0.2
  done
  printf '%s\n' "$t"
  return 1
}

# tctl calls transfer.pause, transfer.resume or transfer.cancel on $2.
tctl() { api "transfer.$1" "{\"transfer_id\":\"$2\"}"; }

# treemanifest prints "<sha256>  <relative path>" for every file under $1,
# sorted.
treemanifest() { (cd "$1" && find . -type f ! -name '.DS_Store' -print0 | sort -z | xargs -0 shasum -a 256) | sed 's#  \./#  #'; }

# pccount prints the number of files under the PC folder $1 and the number
# of part files among them.
pccount() {
  rps <<EOF | tr -d '\r'
\$f = @(Get-ChildItem -LiteralPath $(pcq "$1") -Recurse -File -Force -ErrorAction SilentlyContinue)
'' + \$f.Count + ' ' + @(\$f | Where-Object { \$_.Name -like '*.deskhand-part-*' }).Count
EOF
}

# events_start records transfer.updated events of the host into $1 until
# events_stop.
events_start() {
  "$repo/build/apicall" -events transfer.updated -host "$host" -for 3600s >"$1" &
  ev_pid=$!
  sleep 0.3
}
events_stop() { kill "$ev_pid" 2>/dev/null; wait "$ev_pid" 2>/dev/null; }

step_xfer() {
  log "== xfer (A12)"
  wait_ready 30
  local d="$work/xfer" up="$pcdir\\files\\up" big=${DESKHAND_E2E_BIG:-1073741824} many=${DESKHAND_E2E_MANY:-1000}
  local out t0 t1 id t b1 b2 secs pause_ms res
  rm -rf "$d"
  mkdir -p "$d/src" "$d/down"
  printf '안녕 😀 첫 줄\r\n두 번째 줄\n%s\n' "$(seq 1 200 | tr '\n' ' ')" >"$d/src/text.txt"
  head -c 8388608 /dev/urandom >"$d/src/bin.dat"
  t0=$(ms)
  head -c "$big" /dev/urandom >"$d/src/big.bin"
  log "made $big random bytes in $(($(ms) - t0)) ms"
  python3 -I - "$d/src/many" "$many" <<'PY'
import os, sys
root, n = sys.argv[1], int(sys.argv[2])
for i in range(n):
    sub = os.path.join(root, "dir%02d" % (i % 10))
    os.makedirs(sub, exist_ok=True)
    with open(os.path.join(sub, "file%04d.bin" % i), "wb") as f:
        f.write(os.urandom(1024 + (i * 37) % 3072))
PY
  pcfresh "$up"

  local name
  for name in text.txt bin.dat; do
    t0=$(ms)
    out=$(dhj files push "$d/src/$name" "$host:$up\\$name")
    t1=$(ms)
    check xfer "$(jq -e '.state == "done" and .outcome == "written"' <<<"$out" >/dev/null && [ "$(rhash "$up\\$name")" = "$(sha "$d/src/$name")" ] && echo 0 || echo 1)" \
      "push $name ($(stat -f %z "$d/src/$name") B, $((t1 - t0)) ms): PC SHA-256 matches"
    t0=$(ms)
    out=$(dhj files pull "$host:$up\\$name" "$d/down/$name")
    t1=$(ms)
    check xfer "$(jq -e '.state == "done"' <<<"$out" >/dev/null && cmp -s "$d/src/$name" "$d/down/$name" && echo 0 || echo 1)" \
      "pull $name back ($((t1 - t0)) ms): bytes identical"
  done

  log "-- big push ($big B)"
  events_start "$d/push-events.jsonl"
  t0=$(ms)
  id=$(tstart push "$d/src/big.bin" "$up\\big.bin" | jq -r .transfer_id)
  t=$(xfer_wait "$id" 1800)
  t1=$(ms)
  events_stop
  secs=$((t1 - t0))
  log "big push: $(jq -c '{state, outcome, bytes_done, rate_bps}' <<<"$t"), $secs ms, $(mbps "$big" "$secs") MB/s (SSH 중계 경유 — 대표값 아님)"
  setstate m_big_push "$(mbps "$big" "$secs") MB/s, $secs ms"
  check xfer "$(jq -e '.state == "done"' <<<"$t" >/dev/null && [ "$(rhash "$up\\big.bin")" = "$(sha "$d/src/big.bin")" ] && echo 0 || echo 1)" "big push done, PC SHA-256 matches"
  out=$(jq -cs --arg id "$id" '[.[] | select(.transfer.transfer_id == $id) | .transfer.bytes_done] | {n: length, monotonic: (. == (. | sort)), distinct: (unique | length), last: .[-1]}' "$d/push-events.jsonl")
  log "push progress events: $out"
  check xfer "$(jq -e --argjson b "$big" '.distinct >= 5 and .monotonic and .last == $b' <<<"$out" >/dev/null && echo 0 || echo 1)" "transfer.updated shows rising progress up to $big bytes"

  log "-- big pull with pause and resume"
  events_start "$d/pull-events.jsonl"
  t0=$(ms)
  id=$(tstart pull "$d/down/big.bin" "$up\\big.bin" | jq -r .transfer_id)
  t=$(xfer_when "$id" ".bytes_done >= $((big / 4))" 900)
  res=$(tctl pause "$id")
  local tp=$(ms)
  sleep 3
  t=$(api transfer.get "{\"transfer_id\":\"$id\"}")
  b1=$(jq -r .bytes_done <<<"$t")
  local part="$d/down/big.bin.deskhand-part-$id"
  sleep 2
  b2=$(jq -r .bytes_done <<<"$(api transfer.get "{\"transfer_id\":\"$id\"}")")
  log "paused: $(jq -c '{state: .state, bytes_done}' <<<"$res") -> $b1 / $b2, part $(stat -f %z "$part" 2>/dev/null) B"
  check xfer "$([ "$(jq -r .state <<<"$t")" = paused ] && [ "$b1" = "$b2" ] && [ -f "$part" ] && [ ! -e "$d/down/big.bin" ] && echo 0 || echo 1)" \
    "transfer.pause stops the pull at $b1 bytes and keeps the part file"
  tctl resume "$id" >/dev/null
  pause_ms=$(($(ms) - tp))
  t=$(xfer_wait "$id" 1800)
  t1=$(ms)
  events_stop
  secs=$((t1 - t0 - pause_ms))
  out=$(jq -cs --arg id "$id" '[.[] | select(.transfer.transfer_id == $id) | .transfer] as $t | ($t | map(.state == "paused") | rindex(true)) as $p | {n: ($t | length), paused_at: $p, after_resume_min: ($t[($p + 1):] | map(.bytes_done) | min)}' "$d/pull-events.jsonl")
  log "big pull: $(jq -c '{state, outcome, bytes_done}' <<<"$t"), $secs ms without the pause, $(mbps "$big" "$secs") MB/s (SSH 중계 경유 — 대표값 아님); events $out"
  setstate m_big_pull "$(mbps "$big" "$secs") MB/s, $secs ms"
  check xfer "$(jq -e '.state == "done"' <<<"$t" >/dev/null && [ "$(sha "$d/down/big.bin")" = "$(sha "$d/src/big.bin")" ] && [ ! -e "$part" ] && echo 0 || echo 1)" \
    "transfer.resume finishes the pull, bytes identical, part file gone"
  check xfer "$(jq -e --argjson b1 "$b1" '.after_resume_min >= $b1' <<<"$out" >/dev/null && echo 0 || echo 1)" "the resumed pull continues from $b1 bytes, not from 0"

  log "-- pull cancel"
  id=$(tstart pull "$d/down/big-cancel.bin" "$up\\big.bin" | jq -r .transfer_id)
  t=$(xfer_when "$id" ".bytes_done >= $((big / 10))" 600)
  res=$(tctl cancel "$id")
  sleep 1
  check xfer "$([ "$(jq -r .state <<<"$res")" = cancelled ] && [ -z "$(ls -A "$d/down" | grep big-cancel)" ] && echo 0 || echo 1)" \
    "transfer.cancel ends the pull after $(jq -r .bytes_done <<<"$t") bytes and leaves no part or target file (bytes_done now $(jq -r .bytes_done <<<"$res"))"

  log "-- $many files push with cancel and resume"
  t0=$(ms)
  id=$(tstart push "$d/src/many" "$up\\many" | jq -r .transfer_id)
  t=$(xfer_when "$id" ".files_done >= $((many * 3 / 10))" 900)
  res=$(tctl cancel "$id")
  local kept=$(jq -r .files_done <<<"$res")
  sleep 2
  out=$(pccount "$up\\many")
  log "cancelled at $kept files: $(jq -c '{state, files_done, files_total, outcome}' <<<"$res"); PC files and parts: $out"
  check xfer "$([ "$(jq -r .state <<<"$res")" = cancelled ] && [ "$out" = "$kept 0" ] && echo 0 || echo 1)" \
    "transfer.cancel keeps the $kept finished files on the PC and no part file"
  tctl resume "$id" >/dev/null
  t=$(xfer_wait "$id" 1800)
  t1=$(ms)
  out=$(pccount "$up\\many")
  log "resumed: $(jq -c '{state, outcome, files_done, files_total, files_failed}' <<<"$t"), PC $out, $((t1 - t0)) ms with the cancel"
  check xfer "$(jq -e --argjson n "$many" '.state == "done" and .files_done == $n and .files_failed == 0' <<<"$t" >/dev/null && [ "$out" = "$many 0" ] && echo 0 || echo 1)" \
    "transfer.resume after cancel finishes all $many files"
  t0=$(ms)
  out=$(dhj files pull "$host:$up\\many" "$d/down/many")
  t1=$(ms)
  log "$many files pull: $(jq -c '{state, files_done, files_failed}' <<<"$out"), $((t1 - t0)) ms, $(python3 -I -c "print('%.1f' % ($many/(($t1-$t0)/1000)))") files/s (SSH 중계 경유 — 대표값 아님)"
  setstate m_many_pull "$((t1 - t0)) ms for $many files"
  treemanifest "$d/src/many" >"$d/m-src.txt"
  treemanifest "$d/down/many" >"$d/m-down.txt"
  check xfer "$(jq -e '.state == "done"' <<<"$out" >/dev/null && [ "$(wc -l <"$d/m-src.txt" | tr -d ' ')" = "$many" ] && cmp -s "$d/m-src.txt" "$d/m-down.txt" && echo 0 || echo 1)" \
    "$many files pulled back: names and SHA-256 identical to the source"

  log "-- scp baseline over the same ssh path (256 MiB)"
  head -c 268435456 "$d/src/big.bin" >"$d/src/scp.bin"
  t0=$(ms)
  scp -q "${ssh_opts[@]}" "$d/src/scp.bin" "$ssh_alias:${up//\\//}/scp.bin"
  t1=$(ms)
  local scp_up=$(mbps 268435456 $((t1 - t0)))
  t0=$(ms)
  scp -q "${ssh_opts[@]}" "$ssh_alias:${up//\\//}/scp.bin" "$d/down/scp.bin"
  t1=$(ms)
  local scp_down=$(mbps 268435456 $((t1 - t0)))
  log "scp 256 MiB: up $scp_up MB/s, down $scp_down MB/s"
  setstate m_scp "up $scp_up MB/s, down $scp_down MB/s"
  rm -f "$d/src/big.bin" "$d/down/big.bin" "$d/src/scp.bin" "$d/down/scp.bin"
  rps <<<"Remove-Item -LiteralPath $(pcq "$up\\big.bin"), $(pcq "$up\\scp.bin") -Force -ErrorAction SilentlyContinue"
}

# pcfresh deletes the PC folder $1 of an earlier run of a step and makes it
# again.
pcfresh() {
  rps <<<"Remove-Item -LiteralPath $(pcq "$1") -Recurse -Force -ErrorAction SilentlyContinue; New-Item -ItemType Directory -Force -Path $(pcq "$1") | Out-Null" >/dev/null
}

# rwrite writes the text $2 to the PC file $1 (folders made, file replaced).
rwrite() {
  api files.write "$(jq -cn --arg h "$host" --arg p "$1" --arg d "$(printf '%s' "$2" | base64)" '{host_id:$h, path:$p, data:$d, truncate:true, mkdirs:true}')" >/dev/null
}

# rcat prints the first MiB of the PC file $1.
rcat() {
  api files.read "$(jq -cn --arg h "$host" --arg p "$1" '{host_id:$h, path:$p, offset:0, length:1048576}')" | jq -r '.data // empty' | base64 -D
}

# trash_ps is a PowerShell function Get-RunTrash that lists the Recycle Bin
# entries ($I file, $R file, original path) of the SSH user whose original
# path lies under the given prefix.
trash_ps='function Get-RunTrash([string]$prefix) {
  $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
  $dir = Join-Path $prefix.Substring(0, 3) ("`$Recycle.Bin\" + $sid)
  Get-ChildItem -LiteralPath $dir -Force -Filter "`$I*" -ErrorAction SilentlyContinue | ForEach-Object {
    $b = [IO.File]::ReadAllBytes($_.FullName)
    if ([BitConverter]::ToInt64($b, 0) -eq 2) { $n = [BitConverter]::ToInt32($b, 24); $p = [Text.Encoding]::Unicode.GetString($b, 28, ($n - 1) * 2) }
    else { $p = [Text.Encoding]::Unicode.GetString($b, 24, 520).TrimEnd([char]0) }
    if ($p.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
      [pscustomobject]@{ I = $_.FullName; R = (Join-Path $_.DirectoryName ("`$R" + $_.Name.Substring(2))); Path = $p }
    }
  }
}
'

# intrash prints the number of Recycle Bin entries whose original path is
# the PC path $1.
intrash() {
  rps <<EOF | tr -d '\r\n '
$trash_ps
@(Get-RunTrash $(pcq "$pcdir") | Where-Object { \$_.Path -eq $(pcq "$1") }).Count
EOF
}

# outcome_of prints "state outcome source error.code" of a CLI --json
# transfer result.
outcome_of() { jq -r '"\(.state) \(.outcome) \(.source // "-") \(.error.code // "-")"' <<<"$1"; }

step_conflict() {
  log "== conflict (A13)"
  wait_ready 30
  local c="$pcdir\\files\\c" m="$work/c" out res
  pcfresh "$c"
  rm -rf "$m"
  mkdir -p "$m"
  rwrite "$c\\a.txt" "alpha"
  rwrite "$c\\keep.txt" "keep"
  dh files mv "$host:$c\\a.txt" "$host:$c\\b.txt" >/dev/null
  check conflict "$([ "$(rexists "$c\\a.txt")" = false ] && [ "$(rcat "$c\\b.txt")" = alpha ] && echo 0 || echo 1)" "files mv renames a.txt to b.txt on the PC"
  out=$(dh files mv "$host:$c\\b.txt" "$host:$c\\keep.txt" 2>&1)
  check conflict "$([ $? != 0 ] && [ "$(rcat "$c\\b.txt")" = alpha ] && [ "$(rcat "$c\\keep.txt")" = keep ] && echo 0 || echo 1)" \
    "files mv onto an existing file is refused and both stay ($(head -1 <<<"$out"))"
  dh files mv --overwrite "$host:$c\\b.txt" "$host:$c\\keep.txt" >/dev/null
  check conflict "$([ "$(rexists "$c\\b.txt")" = false ] && [ "$(rcat "$c\\keep.txt")" = alpha ] && echo 0 || echo 1)" "files mv --overwrite replaces keep.txt"
  api files.mkdir "$(jq -cn --arg h "$host" --arg p "$c\\sub" '{host_id:$h, path:$p}')" >/dev/null
  dh files mv "$host:$c\\keep.txt" "$host:$c\\sub\\keep.txt" >/dev/null
  check conflict "$([ "$(rexists "$c\\keep.txt")" = false ] && [ "$(rcat "$c\\sub\\keep.txt")" = alpha ] && echo 0 || echo 1)" "files mv moves a file into a subfolder"

  local pol want_pc want_new
  printf 'new' >"$m/p.txt"
  for pol in fail skip rename replace; do
    rwrite "$c\\p-$pol.txt" "old"
    out=$(dhj files push --on-conflict "$pol" "$m/p.txt" "$host:$c\\p-$pol.txt")
    log "push --on-conflict $pol: $(outcome_of "$out") path $(jq -r '.files[0].path // "-"' <<<"$out")"
    case $pol in
    fail) want_pc=old; [ "$(outcome_of "$out")" = "failed failed - TRANSFER_CONFLICT" ] ;;
    skip) want_pc=old; [ "$(outcome_of "$out")" = "done skipped - -" ] ;;
    rename) want_pc=old; [ "$(outcome_of "$out")" = "done renamed - -" ] && [ "$(rcat "$c\\p-$pol (1).txt")" = new ] && [ "$(jq -r '.files[0].path' <<<"$out")" = "$c\\p-$pol (1).txt" ] ;;
    replace) want_pc=new; [ "$(outcome_of "$out")" = "done written - -" ] ;;
    esac
    local okk=$?
    check conflict "$([ "$okk" = 0 ] && [ "$(rcat "$c\\p-$pol.txt")" = "$want_pc" ] && echo 0 || echo 1)" "push onto an existing PC file with $pol: the PC file holds '$want_pc'"
  done
  rwrite "$c\\l.txt" "new"
  for pol in fail skip rename replace; do
    printf 'old' >"$m/l-$pol.txt"
    out=$(dhj files pull --on-conflict "$pol" "$host:$c\\l.txt" "$m/l-$pol.txt")
    log "pull --on-conflict $pol: $(outcome_of "$out") path $(jq -r '.files[0].path // "-"' <<<"$out")"
    case $pol in
    fail) want_pc=old; [ "$(outcome_of "$out")" = "failed failed - TRANSFER_CONFLICT" ] ;;
    skip) want_pc=old; [ "$(outcome_of "$out")" = "done skipped - -" ] ;;
    rename) want_pc=old; [ "$(outcome_of "$out")" = "done renamed - -" ] && [ "$(cat "$m/l-$pol (1).txt")" = new ] ;;
    replace) want_pc=new; [ "$(outcome_of "$out")" = "done written - -" ] ;;
    esac
    local okk=$?
    check conflict "$([ "$okk" = 0 ] && [ "$(cat "$m/l-$pol.txt")" = "$want_pc" ] && echo 0 || echo 1)" "pull onto an existing Mac file with $pol: the Mac file holds '$want_pc'"
  done

  printf 'move me' >"$m/mv.txt"
  out=$(dhj files move "$m/mv.txt" "$host:$c\\mv.txt")
  log "move_push: $(outcome_of "$out")"
  check conflict "$([ "$(outcome_of "$out")" = "done written deleted -" ] && [ ! -e "$m/mv.txt" ] && [ "$(rcat "$c\\mv.txt")" = "move me" ] && echo 0 || echo 1)" \
    "files move to the PC: the PC has the file, the Mac source is deleted"
  rwrite "$c\\mvp.txt" "move back"
  local n0=$(intrash "$c\\mvp.txt")
  out=$(dhj files move "$host:$c\\mvp.txt" "$m/mvp.txt")
  log "move_pull: $(outcome_of "$out")"
  check conflict "$([ "$(outcome_of "$out")" = "done written trashed -" ] && [ "$(cat "$m/mvp.txt")" = "move back" ] && [ "$(rexists "$c\\mvp.txt")" = false ] && [ "$(intrash "$c\\mvp.txt")" = $((n0 + 1)) ] && echo 0 || echo 1)" \
    "files move from the PC: the Mac has the file, the PC source is in the Recycle Bin"
  rwrite "$c\\taken.txt" "taken"
  printf 'mine' >"$m/taken.txt"
  out=$(dhj files move "$m/taken.txt" "$host:$c\\taken.txt")
  log "move_push onto an existing file with fail: $(outcome_of "$out")"
  check conflict "$([ "$(jq -r .state <<<"$out")" = failed ] && [ "$(jq -r .source <<<"$out")" = retained ] && [ "$(cat "$m/taken.txt")" = mine ] && [ "$(rcat "$c\\taken.txt")" = taken ] && echo 0 || echo 1)" \
    "a move that hits a conflict (fail) keeps the source and the target"
}

# lock_start holds the PC file $1 open without sharing in a job of its own
# for $2 seconds and prints the job id.
lock_start() {
  local j cmd
  cmd="\$f = [IO.File]::Open($(pcq "$1"), 'OpenOrCreate', 'ReadWrite', 'None'); Write-Output locked; Start-Sleep $2; \$f.Close()"
  j=$(api exec.start "$(jq -cn --arg h "$host" --arg c "$cmd" '{host_id:$h, shell:"powershell", cmd:$c}')" | jq -r .job_id)
  for _ in $(seq 1 40); do
    api jobs.output "{\"job_id\":\"$j\"}" | jq -e '[(.chunks // [])[].data] | join("") | test("locked")' >/dev/null && break
    sleep 0.25
  done
  echo "$j"
}
lock_stop() { api jobs.cancel "{\"job_id\":\"$1\"}" >/dev/null; sleep 1; }

# corrupt_pc_part overwrites the first 4096 bytes of the PC part file of
# transfer $2 next to the PC file $1.
corrupt_pc_part() {
  rps <<EOF
\$dir = Split-Path -Parent $(pcq "$1")
\$p = Get-ChildItem -LiteralPath \$dir -Force -Filter ('*.deskhand-part-' + '$2') | Select-Object -First 1
\$f = [IO.File]::Open(\$p.FullName, 'Open', 'ReadWrite', 'ReadWrite'); \$f.Write((New-Object byte[] 4096), 0, 4096); \$f.Close()
'corrupted ' + \$p.Name + ' ' + \$p.Length
EOF
}

step_movefail() {
  log "== movefail (A14)"
  wait_ready 30
  local c="$pcdir\\files\\mf" m="$work/mf" out t id j sum b1 events
  pcfresh "$c"
  rm -rf "$m"
  mkdir -p "$m/folder" "$m/ro"

  printf 'ok' >"$m/folder/ok.txt"
  printf 'bad' >"$m/folder/bad:name.txt"
  out=$(dhj files move "$m/folder" "$host:$c\\folder")
  log "move_push with a name Windows refuses: $(outcome_of "$out"), files $(jq -c '[.files[] | {rel, state, code: .error.code}]' <<<"$out")"
  check movefail "$([ "$(jq -r '.state + " " + .outcome + " " + .source' <<<"$out")" = "failed partial retained" ] && [ -f "$m/folder/ok.txt" ] && [ -f "$m/folder/bad:name.txt" ] && [ "$(rcat "$c\\folder\\ok.txt")" = ok ] && echo 0 || echo 1)" \
    "move_push with one file that cannot be copied: partial, the PC has ok.txt, the Mac source folder keeps both files"
  rwrite "$c\\pf\\a.txt" "a"
  rwrite "$c\\pf\\locked.txt" "locked"
  j=$(lock_start "$c\\pf\\locked.txt" 120)
  local n0=$(intrash "$c\\pf")
  out=$(dhj files move "$host:$c\\pf" "$m/pf")
  log "move_pull with a locked file: $(outcome_of "$out"), files $(jq -c '[.files[] | {rel, state, code: .error.code}]' <<<"$out")"
  lock_stop "$j"
  check movefail "$([ "$(jq -r '.state + " " + .outcome + " " + .source' <<<"$out")" = "failed partial retained" ] && [ "$(rexists "$c\\pf\\a.txt")" = true ] && [ "$(rexists "$c\\pf\\locked.txt")" = true ] && [ "$(intrash "$c\\pf")" = "$n0" ] && echo 0 || echo 1)" \
    "move_pull of a folder with a locked file: partial, the PC folder stays complete and nothing is trashed"

  head -c 67108864 /dev/urandom >"$m/hm.bin"
  scp -q "${ssh_opts[@]}" "$m/hm.bin" "$ssh_alias:${c//\\//}/hm.bin"
  sum=$(sha "$m/hm.bin")
  events_start "$m/hm-events.jsonl"
  id=$(tstart move_pull "$m/hm-pulled.bin" "$c\\hm.bin" | jq -r .transfer_id)
  xfer_when "$id" '.bytes_done >= 20000000' 600 >/dev/null
  tctl pause "$id" >/dev/null
  sleep 2
  dd if=/dev/zero of="$m/hm-pulled.bin.deskhand-part-$id" bs=4096 count=1 conv=notrunc 2>/dev/null
  log "move_pull paused at $(api transfer.get "{\"transfer_id\":\"$id\"}" | jq -r .bytes_done) bytes; zeroed the first 4096 bytes of the Mac part file"
  tctl resume "$id" >/dev/null
  t=$(xfer_wait "$id" 600)
  events_stop
  events=$(jq -cs --arg id "$id" '[.[] | select(.transfer.transfer_id == $id) | .transfer] as $t | ($t | map(.state == "paused") | rindex(true)) as $p | ($t[($p + 1):] | map(.bytes_done)) as $a | {after_resume_max_before_drop: $a[0], drops: ([range(1; $a | length) | select($a[.] < $a[. - 1])] | length), min_after: ($a | min)}' "$m/hm-events.jsonl")
  log "move_pull after the corrupted part: $(jq -c '{state, outcome, files: [.files[] | {state, outcome}]}' <<<"$t"); progress after resume $events"
  check movefail "$(jq -e '.state == "done" and .outcome == "written"' <<<"$t" >/dev/null && [ "$(sha "$m/hm-pulled.bin")" = "$sum" ] && [ "$(rexists "$c\\hm.bin")" = false ] && echo 0 || echo 1)" \
    "a pulled file whose part does not match the PC's SHA-256 is received again: the Mac file is identical, then the PC source is trashed"
  check movefail "$(jq -e '.drops >= 1' <<<"$events" >/dev/null && echo 0 || echo 1)" "the progress went back for the second receive (hash mismatch seen before the source was touched)"

  head -c 67108864 /dev/urandom >"$m/hp.bin"
  sum=$(sha "$m/hp.bin")
  id=$(tstart move_push "$m/hp.bin" "$c\\hp.bin" | jq -r .transfer_id)
  xfer_when "$id" '.bytes_done >= 20000000' 600 >/dev/null
  tctl pause "$id" >/dev/null
  sleep 2
  corrupt_pc_part "$c\\hp.bin" "$id"
  check movefail "$([ -f "$m/hp.bin" ] && echo 0 || echo 1)" "the Mac source of the paused move_push is still there"
  tctl resume "$id" >/dev/null
  t=$(xfer_wait "$id" 600)
  log "move_push after the corrupted PC part: $(jq -c '{state, outcome}' <<<"$t")"
  check movefail "$(jq -e '.state == "done" and .outcome == "written"' <<<"$t" >/dev/null && [ "$(rhash "$c\\hp.bin")" = "$sum" ] && [ ! -e "$m/hp.bin" ] && echo 0 || echo 1)" \
    "a PC part that no longer matches the Mac file is sent again: the PC file is identical, then the Mac source is deleted"

  printf 'read only parent' >"$m/ro/x.txt"
  chmod 555 "$m/ro"
  out=$(dhj files move "$m/ro/x.txt" "$host:$c\\x.txt")
  chmod 755 "$m/ro"
  log "move_push with an undeletable source: $(outcome_of "$out") $(jq -r '.error.message // ""' <<<"$out")"
  check movefail "$([ "$(jq -r '.state + " " + .outcome + " " + .source' <<<"$out")" = "done copied_source_retained retained" ] && [ -f "$m/ro/x.txt" ] && [ "$(rcat "$c\\x.txt")" = "read only parent" ] && echo 0 || echo 1)" \
    "move_push whose Mac source cannot be deleted: copied_source_retained, both copies exist"
  local long="$c\\$(printf 'L%.0s' $(seq 1 60))\\$(printf 'M%.0s' $(seq 1 60))\\$(printf 'N%.0s' $(seq 1 60))"
  rwrite "$long\\deep-source-file.txt" "deep"
  log "long source path: $((${#long} + 22)) characters"
  out=$(dhj files move "$host:$long\\deep-source-file.txt" "$m/deep.txt")
  log "move_pull with a source the Recycle Bin refuses: $(outcome_of "$out") $(jq -r '.error.message // ""' <<<"$out")"
  check movefail "$([ "$(jq -r '.state + " " + .outcome + " " + .source' <<<"$out")" = "done copied_source_retained retained" ] && [ "$(cat "$m/deep.txt")" = deep ] && [ "$(rexists "$long\\deep-source-file.txt")" = true ] && echo 0 || echo 1)" \
    "move_pull whose PC source cannot go to the Recycle Bin (path of $((${#long} + 22)) characters): copied_source_retained, the source stays"
}

step_resume() {
  log "== resume (A15)"
  wait_ready 30
  local c="$pcdir\\files\\rs" m="$work/rs" out t id j sum
  pcfresh "$c"
  rm -rf "$m"
  mkdir -p "$m"

  head -c 67108864 /dev/urandom >"$m/ch.bin"
  id=$(tstart push "$m/ch.bin" "$c\\ch.bin" | jq -r .transfer_id)
  xfer_when "$id" '.bytes_done >= 20000000' 600 >/dev/null
  tctl pause "$id" >/dev/null
  sleep 1
  printf 'changed' | dd of="$m/ch.bin" bs=1 seek=100 conv=notrunc 2>/dev/null
  touch "$m/ch.bin"
  tctl resume "$id" >/dev/null
  t=$(xfer_wait "$id" 600)
  log "push resumed after the Mac source changed: $(jq -c '{state, outcome, files: [.files[] | {state, code: .error.code}]}' <<<"$t")"
  check resume "$(jq -e '.state == "failed" and .files[0].error.code == "TRANSFER_CONFLICT"' <<<"$t" >/dev/null && [ "$(rexists "$c\\ch.bin")" = false ] && [ "$(pccount "$c")" = "0 0" ] && echo 0 || echo 1)" \
    "a push whose source changed while paused fails with TRANSFER_CONFLICT; no target and no part file on the PC"
  tctl resume "$id" >/dev/null
  t=$(xfer_wait "$id" 600)
  check resume "$(jq -e '.state == "done"' <<<"$t" >/dev/null && [ "$(rhash "$c\\ch.bin")" = "$(sha "$m/ch.bin")" ] && echo 0 || echo 1)" \
    "transfer.resume takes the new fingerprint and sends the changed file"

  head -c 67108864 /dev/urandom >"$m/pc.bin"
  scp -q "${ssh_opts[@]}" "$m/pc.bin" "$ssh_alias:${c//\\//}/pc.bin"
  id=$(tstart pull "$m/pc-pulled.bin" "$c\\pc.bin" | jq -r .transfer_id)
  xfer_when "$id" '.bytes_done >= 20000000' 600 >/dev/null
  tctl pause "$id" >/dev/null
  sleep 1
  rps <<<"\$f = [IO.File]::Open($(pcq "$c\\pc.bin"), 'Open', 'ReadWrite', 'None'); \$f.Seek(100, 'Begin') | Out-Null; \$f.Write([byte[]](1,2,3,4), 0, 4); \$f.Close()" >/dev/null
  tctl resume "$id" >/dev/null
  t=$(xfer_wait "$id" 600)
  log "pull resumed after the PC source changed: $(jq -c '{state, files: [.files[] | {state, code: .error.code}]}' <<<"$t")"
  check resume "$(jq -e '.state == "failed" and .files[0].error.code == "TRANSFER_CONFLICT"' <<<"$t" >/dev/null && [ ! -e "$m/pc-pulled.bin" ] && [ -z "$(ls -A "$m" | grep deskhand-part)" ] && echo 0 || echo 1)" \
    "a pull whose PC source changed while paused fails with TRANSFER_CONFLICT; no target and no part file on the Mac"
  tctl resume "$id" >/dev/null
  t=$(xfer_wait "$id" 600)
  check resume "$(jq -e '.state == "done"' <<<"$t" >/dev/null && [ "$(sha "$m/pc-pulled.bin")" = "$(rhash "$c\\pc.bin")" ] && echo 0 || echo 1)" \
    "transfer.resume takes the PC's new fingerprint and receives the changed file"

  notrun resume "disk full: filling a disk of the Mac or the PC (or attaching a small volume) is outside this run; the agent's DISK_FULL path is covered by TestCommitFailureLeavesNoCorruptTarget/disk_full_while_receiving"

  rwrite "$c\\lk.txt" "locked source"
  j=$(lock_start "$c\\lk.txt" 120)
  out=$(dhj files pull "$host:$c\\lk.txt" "$m/lk.txt")
  log "pull of a locked PC file: $(outcome_of "$out") $(jq -r '.files[0].error.message // .error.message // ""' <<<"$out")"
  check resume "$([ "$(jq -r .state <<<"$out")" = failed ] && [ ! -e "$m/lk.txt" ] && echo 0 || echo 1)" "pull of a file another process holds without sharing fails with $(jq -r '.files[0].error.code // .error.code' <<<"$out") and writes nothing"
  lock_stop "$j"
  t=$(tctl resume "$(jq -r .transfer_id <<<"$out")")
  t=$(xfer_wait "$(jq -r .transfer_id <<<"$out")" 120)
  check resume "$(jq -e '.state == "done"' <<<"$t" >/dev/null && [ "$(cat "$m/lk.txt")" = "locked source" ] && echo 0 || echo 1)" "after the lock is gone transfer.resume finishes the pull"
  rwrite "$c\\lkd.txt" "old target"
  printf 'new content' >"$m/lkd.txt"
  j=$(lock_start "$c\\lkd.txt" 120)
  out=$(dhj files push --on-conflict replace "$m/lkd.txt" "$host:$c\\lkd.txt")
  log "push replace onto a locked PC file: $(outcome_of "$out") $(jq -r '.files[0].error.message // .error.message // ""' <<<"$out")"
  lock_stop "$j"
  check resume "$([ "$(jq -r .state <<<"$out")" = failed ] && [ "$(rcat "$c\\lkd.txt")" = "old target" ] && echo 0 || echo 1)" \
    "push replace onto a locked PC file fails with $(jq -r '.files[0].error.code // .error.code' <<<"$out"); the target is unchanged (part files: $(pccount "$c" | cut -d' ' -f2), kept for transfer.resume)"
  t=$(tctl resume "$(jq -r .transfer_id <<<"$out")")
  t=$(xfer_wait "$(jq -r .transfer_id <<<"$out")" 120)
  check resume "$(jq -e '.state == "done"' <<<"$t" >/dev/null && [ "$(rcat "$c\\lkd.txt")" = "new content" ] && [ "$(pccount "$c" | cut -d' ' -f2)" = 0 ] && echo 0 || echo 1)" "after the lock is gone transfer.resume replaces the target and no part file is left"
}

# pcnames prints the names under the PC folder $1, relative and sorted,
# one per line.
pcnames() {
  rps <<EOF | tr -d '\r'
\$root = (Get-Item -LiteralPath $(pcq "$1")).FullName.TrimEnd('\\') + '\\'
Get-ChildItem -LiteralPath $(pcq "$1") -Recurse -Force | ForEach-Object { \$_.FullName.Substring(\$root.Length) } | Sort-Object
EOF
}

step_names() {
  log "== names (A16)"
  wait_ready 30
  local c="$pcdir\\files\\nm" m="$work/nm" out t res kor="한글 폴더 😀" long seg
  pcfresh "$c"
  rm -rf "$m"
  mkdir -p "$m/src/$kor/하위 공백 폴더" "$m/back"
  printf '내용 1' >"$m/src/$kor/파일 😀 1.txt"
  printf '내용 2' >"$m/src/$kor/하위 공백 폴더/공백 이름 2.txt"
  printf 'cafe' >"$m/src/$kor/café ü.txt"
  out=$(dhj files push "$m/src/$kor" "$host:$c\\$kor")
  log "push $kor: $(jq -c '{state, files_done, files_total}' <<<"$out")"
  res=$(pcnames "$c\\$kor")
  printf '%s\n' "$res"
  check names "$(jq -e '.state == "done" and .files_done == 3' <<<"$out" >/dev/null && grep -qx '파일 😀 1.txt' <<<"$res" && grep -qx '하위 공백 폴더\\공백 이름 2.txt' <<<"$res" && grep -qx 'café ü.txt' <<<"$res" && echo 0 || echo 1)" \
    "a folder with Korean, spaces and emoji keeps its names on the PC"
  out=$(dhj files pull "$host:$c\\$kor" "$m/back/$kor")
  check names "$(jq -e '.state == "done"' <<<"$out" >/dev/null && [ "$(treemanifest "$m/src/$kor")" = "$(treemanifest "$m/back/$kor")" ] && echo 0 || echo 1)" \
    "pulled back, names and contents are identical"

  seg=$(printf 'd%.0s' $(seq 1 60))
  long="$c\\long\\${seg}1\\${seg}2\\${seg}3\\${seg}4"
  api files.mkdir "$(jq -cn --arg h "$host" --arg p "$long" '{host_id:$h, path:$p}')" >/dev/null
  printf 'long path content' >"$m/long.txt"
  out=$(dhj files push "$m/long.txt" "$host:$long\\long-file-name-to-pass-260.txt")
  log "long path: $((${#long} + 31)) characters, push $(jq -c '{state, outcome}' <<<"$out")"
  check names "$(jq -e '.state == "done"' <<<"$out" >/dev/null && [ "$(rhash "$long\\long-file-name-to-pass-260.txt")" = "$(sha "$m/long.txt")" ] && echo 0 || echo 1)" \
    "push to a PC path of $((${#long} + 31)) characters (over 260) and the PC SHA-256 matches"
  out=$(dhj files pull "$host:$c\\long" "$m/back/long")
  check names "$(jq -e '.state == "done" and .files_done == 1' <<<"$out" >/dev/null && cmp -s "$m/long.txt" "$m/back/long/${seg}1/${seg}2/${seg}3/${seg}4/long-file-name-to-pass-260.txt" && echo 0 || echo 1)" \
    "pull of the folder holding it brings the file back intact"

  rwrite "$c\\case\\README.txt" "upper"
  printf 'mixed' >"$m/Readme.TXT"
  out=$(dhj files push "$m/Readme.TXT" "$host:$c\\case\\Readme.TXT")
  log "push Readme.TXT next to README.txt with fail: $(outcome_of "$out")"
  check names "$([ "$(outcome_of "$out")" = "failed failed - TRANSFER_CONFLICT" ] && [ "$(rcat "$c\\case\\README.txt")" = upper ] && echo 0 || echo 1)" \
    "a name that differs only in case is a conflict on the PC (fail keeps README.txt)"
  out=$(dhj files push --on-conflict rename "$m/Readme.TXT" "$host:$c\\case\\Readme.TXT")
  log "with rename: $(outcome_of "$out") -> $(jq -r '.files[0].path' <<<"$out"); PC names $(pcnames "$c\\case" | tr '\n' ' ')"
  check names "$([ "$(jq -r .outcome <<<"$out")" = renamed ] && [ "$(rcat "$(jq -r '.files[0].path' <<<"$out")")" = mixed ] && [ "$(rcat "$c\\case\\README.txt")" = upper ] && echo 0 || echo 1)" \
    "with rename the new file gets a numbered name and README.txt stays"
  api files.mkdir "$(jq -cn --arg h "$host" --arg p "$c\\cs" '{host_id:$h, path:$p}')" >/dev/null
  out=$(rps <<EOF
fsutil.exe file setCaseSensitiveInfo $(pcq "$c\\cs") enable 2>&1
'exit=' + \$LASTEXITCODE
EOF
)
  log "fsutil setCaseSensitiveInfo: $(tr -d '\r' <<<"$out" | tr '\n' ' ')"
  if grep -q 'exit=0' <<<"$out"; then
    rwrite "$c\\cs\\A.txt" "upper a"
    rwrite "$c\\cs\\a.txt" "lower a"
    log "PC names in the case-sensitive folder: $(pcnames "$c\\cs" | tr '\n' ' ')"
    out=$(dhj files pull "$host:$c\\cs" "$m/back/cs")
    log "pull to the Mac's case-insensitive volume: $(jq -c '{state, outcome, files: [.files[] | {rel, state, code: .error.code}]}' <<<"$out")"
    check names "$(jq -e '.state == "failed" and .outcome == "partial" and ([.files[] | select(.state == "done")] | length) == 1 and ([.files[] | select(.error.code == "TRANSFER_CONFLICT")] | length) == 1' <<<"$out" >/dev/null && [ "$(ls "$m/back/cs" | wc -l | tr -d ' ')" = 1 ] && echo 0 || echo 1)" \
      "pull of A.txt and a.txt to a case-insensitive Mac volume: one arrives, the other is TRANSFER_CONFLICT"
  else
    notrun names "case-sensitive PC folder: fsutil setCaseSensitiveInfo is not available here ($(tr -d '\r' <<<"$out" | head -1))"
  fi
  local img="$work/cs.dmg" mnt="$work/csmnt"
  if hdiutil create -quiet -size 16m -fs 'Case-sensitive APFS' -volname dhcs -layout NONE "$img" && mkdir -p "$mnt" && hdiutil attach -quiet -nobrowse -mountpoint "$mnt" "$img"; then
    mkdir -p "$mnt/pair"
    printf 'upper b' >"$mnt/pair/B.txt"
    printf 'lower b' >"$mnt/pair/b.txt"
    out=$(dhj files push "$mnt/pair" "$host:$c\\pair")
    log "push B.txt and b.txt from a case-sensitive Mac volume: $(jq -c '{state, outcome, files: [.files[] | {rel, state, code: .error.code}]}' <<<"$out"); PC $(pcnames "$c\\pair" | tr '\n' ' ')"
    check names "$(jq -e '.state == "failed" and .outcome == "partial" and ([.files[] | select(.error.code == "TRANSFER_CONFLICT")] | length) == 1' <<<"$out" >/dev/null && [ "$(pcnames "$c\\pair" | wc -l | tr -d ' ')" = 1 ] && echo 0 || echo 1)" \
      "push of B.txt and b.txt to Windows: one arrives, the other is TRANSFER_CONFLICT"
    hdiutil detach -quiet "$mnt"
  else
    notrun names "case-sensitive Mac volume: hdiutil could not make or attach a disk image"
  fi
  rm -rf "$img" "$mnt"

  rwrite "$c\\j\\real.txt" "real"
  rwrite "$c\\target\\t.txt" "inside target"
  out=$(rps <<EOF
cmd /c mklink /J $(pcq "$c\\j\\in") $(pcq "$c\\target") 2>&1
cmd /c mklink /J $(pcq "$c\\j\\out") 'C:\Windows\System32\drivers\etc' 2>&1
EOF
)
  log "junctions: $(tr -d '\r' <<<"$out" | tr '\n' ' ')"
  res=$(api files.list "$(jq -cn --arg h "$host" --arg p "$c\\j" '{host_id:$h, path:$p}')")
  log "files.list j: $(jq -c '[.entries[] | {name, dir, link}]' <<<"$res")"
  check names "$(ok jq -e '[.entries[] | select(.link)] | map(.name) | sort == ["in","out"]' <<<"$res")" "files.list marks both junctions as links"
  out=$(dhj files pull "$host:$c\\j" "$m/back/j")
  log "pull of a folder with junctions: $(jq -c '{state, outcome, files: [.files[] | {rel, state, code: .error.code}]}' <<<"$out"); Mac $(cd "$m/back/j" 2>/dev/null && find . | sort | tr '\n' ' ')"
  check names "$(jq -e '.state == "failed" and .outcome == "partial" and ([.files[] | select(.state == "done") | .rel] == ["real.txt"]) and ([.files[] | select(.error.code == "INVALID_ARGUMENT")] | length) == 2' <<<"$out" >/dev/null && [ ! -e "$m/back/j/out" ] && [ ! -e "$m/back/j/in" ] && echo 0 || echo 1)" \
    "pull of a folder with junctions copies real.txt only; each junction is INVALID_ARGUMENT and nothing behind them reaches the Mac"
  res=$(tstart pull "$m/back/out-direct" "$c\\j\\out")
  log "pull of a junction itself: $(jq -c '{status, transfer_id, error: .error.code}' <<<"$res")"
  check names "$(jq -e '.status == "failed" and .transfer_id == null and .error.code == "INVALID_ARGUMENT"' <<<"$res" >/dev/null && [ ! -e "$m/back/out-direct" ] && echo 0 || echo 1)" \
    "a junction as the source is refused before any transfer"

  printf 'escape' >"$m/escape.txt"
  res=$(tstart push "$m/escape.txt" "$c\\sub\\..\\..\\escape.txt")
  log "push to a path with ..: $(jq -c '{error: (.error // .), transfer_id}' <<<"$res" | cut -c1-300)"
  check names "$(jq -e '(.error.code == "INVALID_ARGUMENT") and (.transfer_id // null) == null' <<<"$res" >/dev/null && [ "$(rexists "$pcdir\\files\\escape.txt")" = false ] && echo 0 || echo 1)" \
    "a remote path with .. is refused and nothing is written outside"
  res=$(tstart pull "$m/back/escape.txt" "$c\\..\\c\\l.txt")
  check names "$(jq -e '.error.code == "INVALID_ARGUMENT"' <<<"$res" >/dev/null && [ ! -e "$m/back/escape.txt" ] && echo 0 || echo 1)" "a pull from a path with .. is refused"

  mkdir -p "$m/win"
  for n in ok.txt CON.txt 'trail.' 'q?.txt' 'pipe|.txt'; do printf 'x' >"$m/win/$n"; done
  out=$(dhj files push "$m/win" "$host:$c\\win")
  log "push of names Windows refuses: $(jq -c '{state, outcome, files: [.files[] | {rel, state, code: .error.code}]}' <<<"$out"); PC $(pcnames "$c\\win" | tr '\n' ' ')"
  check names "$(jq -e '.state == "failed" and .outcome == "partial" and ([.files[] | select(.state == "done") | .rel] == ["ok.txt"]) and ([.files[] | select(.error.code == "INVALID_ARGUMENT")] | length) == 4' <<<"$out" >/dev/null && [ "$(pcnames "$c\\win")" = ok.txt ] && echo 0 || echo 1)" \
    "CON.txt, a trailing dot, ? and | are refused one by one; ok.txt arrives"
}

step_diag() {
  log "== diag"
  wait_ready 30
  local out res j pid started
  out=$(dhj diag system "$host")
  log "diag system: $(jq -c '{status, os: .os, computer_name, cpu: .cpu.name, cores: .cpu.cores, memory_total: .memory.total_bytes, disks: (.disks | length), gpus: (.gpus | length), uptime_s}' <<<"$out")"
  check diag "$(ok jq -e '.status == "succeeded" and (.computer_name | length > 0) and (.cpu.name | length > 0) and (.disks | length > 0) and .uptime_s > 0' <<<"$out")" "diag system: computer name, CPU, disks and uptime"
  out=$(dhj diag services "$host")
  log "diag services: $(jq -c '{status, n: (.services | length), sample: [.services[] | select(.name == "EventLog" or .name == "sshd") | {name, state}]}' <<<"$out")"
  check diag "$(ok jq -e '.status == "succeeded" and (.services | length > 50) and ([.services[] | select(.name == "EventLog" and .state == "running")] | length == 1)' <<<"$out")" "diag services lists the services with EventLog running"
  out=$(dhj diag services --name sshd "$host")
  check diag "$(ok jq -e '[.services[].name] | index("sshd") != null' <<<"$out")" "diag services --name sshd finds sshd"
  out=$(dhj diag events --log System --since 7d --max 20 "$host")
  log "diag events System: $(jq -c '{status, n: (.events | length), truncated, first: (.events[0] | {time, level, provider, event_id})}' <<<"$out")"
  check diag "$(ok jq -e '.status == "succeeded" and (.events | length > 0) and (.events[0] | has("record_id") and has("level") and has("provider") and has("message"))' <<<"$out")" "diag events reads the System log"
  out=$(dhj diag events --log System --level critical,error --since 7d --max 5 "$host")
  check diag "$(ok jq -e '.status == "succeeded" and ([.events[].level] - ["critical","error"] | length == 0)' <<<"$out")" "diag events --level critical,error returns only those levels ($(jq -r '.events | length' <<<"$out") entries)"
  out=$(dhj diag crashes --since 30d "$host")
  log "diag crashes: $(jq -c '{status, n: (.dumps | length)}' <<<"$out")"
  check diag "$(ok jq -e '.status == "succeeded" and (.dumps | type == "array")' <<<"$out")" "diag crashes answers with a dump list ($(jq -r '.dumps | length' <<<"$out") dumps)"
  out=$(dhj diag system --save "$host")
  res=$(api artifacts.list "$(jq -cn --arg j "$(jq -r .job_id <<<"$out")" '{job_id:$j}')")
  log "diag system --save: artifact $(jq -c '.artifacts[0] | {name, size, sha256}' <<<"$res")"
  check diag "$(jq -e '.artifacts[0].name == "diag-system.json"' <<<"$res" >/dev/null && [ "$(sha "$(jq -r '.artifacts[0].path' <<<"$res")")" = "$(jq -r '.artifacts[0].sha256' <<<"$res")" ] && jq -e '.method == "diag.system" and .result.computer_name != null' "$(jq -r '.artifacts[0].path' <<<"$res")" >/dev/null && echo 0 || echo 1)" \
    "diag system --save stores diag-system.json as an artifact with a matching SHA-256"
  res=$(api exec.start "$(jq -cn --arg h "$host" '{host_id:$h, cmd:"ping -n 41 127.0.0.1", shell:"cmd"}')")
  j=$(jq -r .job_id <<<"$res")
  sleep 1
  out=$(dhj proc ls --name PING.EXE "$host")
  pid=$(jq -r '[.procs[] | select(.started_by_deskhand)][0].pid' <<<"$out")
  started=$(jq -r --argjson p "$pid" '.procs[] | select(.pid == $p) | .started' <<<"$out")
  log "proc ls: $(jq -c '[.procs[] | {pid, name, started_by_deskhand, started}]' <<<"$out")"
  check diag "$([ "$pid" != null ] && [ -n "$started" ] && echo 0 || echo 1)" "proc ls marks the ping the run started as started_by_deskhand (pid $pid)"
  out=$(dhj proc kill --started 2000-01-01T00:00:00Z "$host" "$pid" 2>/dev/null)
  check diag "$([ "$(jq -r '.error.code' <<<"$out")" = STALE_TARGET ] && [ "$(pcproc "$pid")" = running ] && echo 0 || echo 1)" "proc kill with another start time is STALE_TARGET and the process keeps running"
  out=$(dhj proc kill --started "$started" "$host" "$pid")
  sleep 1
  check diag "$(jq -e '.killed >= 1' <<<"$out" >/dev/null && [ "$(pcproc "$pid")" = gone ] && echo 0 || echo 1)" "proc kill with its start time ends it"
  job_wait "$j" 10 >/dev/null
}

step_tunnel() {
  log "== tunnel"
  wait_ready 30
  local res j port tn lp out
  local script='$l=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,0);$l.Start();[Console]::Out.WriteLine("port=" + $l.LocalEndpoint.Port);[Console]::Out.Flush();while($true){$c=$l.AcceptTcpClient();$s=$c.GetStream();$b=New-Object byte[] 65536;while(($n=$s.Read($b,0,$b.Length)) -gt 0){$s.Write($b,0,$n);$s.Flush()};$c.Client.Shutdown([Net.Sockets.SocketShutdown]::Send);$c.Close()}'
  res=$(api exec.start "$(jq -cn --arg h "$host" --arg c "$script" '{host_id:$h, cmd:$c, shell:"powershell"}')")
  j=$(jq -r .job_id <<<"$res")
  for _ in $(seq 1 40); do
    port=$(api jobs.output "{\"job_id\":\"$j\"}" | jq -r '[(.chunks // [])[].data] | join("")' | sed -n 's/.*port=\([0-9]*\).*/\1/p' | head -1)
    [ -n "$port" ] && break
    sleep 0.25
  done
  log "PC echo server on 127.0.0.1:$port (job $j)"
  res=$(api tunnel.open "$(jq -cn --arg h "$host" --argjson p "$port" '{host_id:$h, remote_port:$p}')")
  tn=$(jq -r .tunnel_id <<<"$res")
  lp=$(jq -r .local_port <<<"$res")
  log "tunnel.open: $(jq -c '{tunnel_id, local_address, remote_port, owner}' <<<"$res")"
  check tunnel "$(ok jq -e '.local_address == ("127.0.0.1:" + (.local_port | tostring)) and .owner == "user"' <<<"$res")" "tunnel.open binds 127.0.0.1:$lp for PC port $port"
  out=$(python3 -I - "$lp" <<'PY'
import socket, sys, threading, time, json
port = int(sys.argv[1])
s = socket.create_connection(("127.0.0.1", port), timeout=30)
s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
msg = "안녕 터널 😀\n".encode()
rtts = []
for _ in range(30):
    t0 = time.time()
    s.sendall(msg)
    got = b""
    while len(got) < len(msg):
        got += s.recv(65536)
    rtts.append((time.time() - t0) * 1000)
    assert got == msg, got
rtts.sort()
size = 32 << 20
data = bytes(range(256)) * (size // 256)
recv = bytearray()
def send():
    for i in range(0, size, 1 << 16):
        s.sendall(data[i:i + (1 << 16)])
    s.shutdown(socket.SHUT_WR)
t0 = time.time()
th = threading.Thread(target=send)
th.start()
while True:
    b = s.recv(1 << 20)
    if not b:
        break
    recv += b
secs = time.time() - t0
th.join()
print(json.dumps({"echo_ok": True, "rtt_p50_ms": round(rtts[len(rtts) // 2], 1), "rtt_max_ms": round(rtts[-1], 1),
                  "bulk_bytes": len(recv), "bulk_ok": bytes(recv) == msg * 0 + data, "eof_after_half_close": True,
                  "mbps": round(2 * size / 1e6 / secs, 1)}))
PY
)
  log "echo through the tunnel: $out (SSH 중계 경유 — 대표값 아님)"
  setstate m_tunnel "$out"
  check tunnel "$(ok jq -e '.echo_ok and .bulk_ok and .bulk_bytes == 33554432' <<<"$out")" "30 Korean/emoji echoes and 32 MiB each way come back intact, and the half close reaches the PC and back"
  res=$(api tunnel.list "{\"host_id\":\"$host\"}")
  log "tunnel.list: $(jq -c '.tunnels[] | {tunnel_id, connections, total_connections, failed_connections, bytes_to_remote, bytes_from_remote}' <<<"$res")"
  check tunnel "$(ok jq -e --arg t "$tn" '.tunnels[] | select(.tunnel_id == $t) | .total_connections >= 1 and .bytes_to_remote >= 33554432 and .bytes_from_remote >= 33554432' <<<"$res")" "tunnel.list counts the connection and the bytes both ways"
  res=$(api tunnel.open "$(jq -cn --arg h "$host" --argjson p "$port" --argjson l "$lp" '{host_id:$h, remote_port:$p, local_port:$l}')")
  check tunnel "$(ok jq -e '.error.code == "PORT_CONFLICT"' <<<"$res")" "a second tunnel on local port $lp is PORT_CONFLICT"
  dh tunnel list "$host"
  res=$(api tunnel.close "{\"tunnel_id\":\"$tn\"}")
  out=$(python3 -I -c 'import socket,sys
try:
    socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=3); print("open")
except OSError as e: print("refused")' "$lp")
  check tunnel "$(jq -e '.closed' <<<"$res" >/dev/null && [ "$out" = refused ] && echo 0 || echo 1)" "tunnel.close closes the listener (connect: $out)"
  res=$(api tunnel.open "$(jq -cn --arg h "$host" '{host_id:$h, remote_port:1}')")
  tn=$(jq -r .tunnel_id <<<"$res")
  lp=$(jq -r .local_port <<<"$res")
  out=$(python3 -I -c 'import socket,sys
s=socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=10)
s.settimeout(10)
print("eof" if s.recv(10) == b"" else "data")' "$lp" 2>&1 | tail -1)
  sleep 1
  res=$(api tunnel.list "{\"host_id\":\"$host\"}" | jq -c --arg t "$tn" '.tunnels[] | select(.tunnel_id == $t) | {failed_connections, last_error}')
  log "tunnel to a PC port with no listener: client $out, $res"
  check tunnel "$(jq -e '.failed_connections >= 1 and (.last_error | length > 0)' <<<"$res" >/dev/null && [ "$out" = eof ] && echo 0 || echo 1)" "a connection to a PC port without a listener is closed and counted with last_error"
  api tunnel.close "{\"tunnel_id\":\"$tn\"}" >/dev/null
  api jobs.cancel "{\"job_id\":\"$j\"}" >/dev/null
}

# dbg calls a debug.* method with debug_id $d and the JSON fields $2.
dbg() {
  local x=${2:-}
  [ -n "$x" ] || x='{}'
  api "debug.$1" "$(jq -cn --arg d "$d" --argjson x "$x" '{debug_id:$d} + $x')"
}

# dbg_paused waits up to $3 seconds until target $1 is paused with reason $2
# and prints its PauseState.
dbg_paused() {
  local st
  for _ in $(seq 1 $(($3 * 4))); do
    st=$(dbg state "$(jq -cn --arg t "$1" '{target_id:$t}')")
    jq -e --arg r "$2" '.paused and .reason == $r' <<<"$st" >/dev/null && { printf '%s\n' "$st"; return 0; }
    sleep 0.25
  done
  printf '%s\n' "$st"
  return 1
}

# dbg_console_has waits up to $2 seconds for a console entry whose text
# contains $1 and prints it.
dbg_console_has() {
  local e
  for _ in $(seq 1 $(($2 * 4))); do
    e=$(dbg console '{"limit":1000}' | jq -c --arg s "$1" '[.entries[] | select(.text | contains($s))][0] // empty')
    [ -n "$e" ] && { printf '%s\n' "$e"; return 0; }
    sleep 0.25
  done
  return 1
}

# local_vars prints the names of the local scope of the top frame of the
# PauseState $1 on target $2.
local_vars() {
  local obj
  obj=$(jq -r '.call_frames[0].scope_chain[] | select(.type == "local") | .object_id' <<<"$1" | head -1)
  dbg variables "$(jq -cn --arg t "$2" --arg o "$obj" '{target_id:$t, object_id:$o}')" | jq -c '[.properties[] | select(.kind != "internal") | {(.name): .value_preview}] | add'
}

# pc_run_procs prints the number of processes whose image lies in the run
# folder and whose name is $1.
pc_run_procs() {
  rps <<<"@(Get-CimInstance Win32_Process -Filter \"Name='$1'\" | Where-Object { \$_.ExecutablePath -like $(pcq "$pcdir\\*") }).Count" | tr -d '\r\n '
}

step_debug() {
  log "== debug (A17/A18, Electron)"
  wait_ready 30
  local ver=${DESKHAND_E2E_ELECTRON:-44.6.0} cache=${DESKHAND_E2E_CACHE:-$repo/build/e2e-cache}
  local dir zip want got out code d mt rt st rurl t0 vars ev
  dir="$cache/electron-v$ver"
  zip="$dir/electron-v$ver-win32-x64.zip"
  if [ ! -f "$zip" ] || [ ! -f "$dir/SHASUMS256.txt" ]; then
    mkdir -p "$dir"
    curl -fsSL -o "$dir/SHASUMS256.txt" "https://github.com/electron/electron/releases/download/v$ver/SHASUMS256.txt" &&
      curl -fsSL -o "$zip.part" "https://github.com/electron/electron/releases/download/v$ver/electron-v$ver-win32-x64.zip" &&
      mv "$zip.part" "$zip"
  fi
  want=$(awk -v n="electron-v$ver-win32-x64.zip" '{f = $2; sub(/^\*/, "", f); if (f == n) print $1}' "$dir/SHASUMS256.txt")
  got=$(sha "$zip")
  check debug "$([ -n "$want" ] && [ "$want" = "$got" ] && echo 0 || echo 1)" "Electron v$ver win32-x64 zip matches the release's SHASUMS256.txt ($got)"
  [ "$want" = "$got" ] || return
  local edir="$pcdir\\electron" app="$pcdir\\fixture-app" data="$pcdir\\electron-data"
  if [ "$(rcat "$edir\\version" | tr -d '\r\n ')" != "$ver" ]; then
    t0=$(ms)
    out=$(dhj files push --on-conflict replace "$zip" "$host:$pcdir\\electron.zip")
    log "Electron zip pushed: $(jq -c '{state, bytes_done}' <<<"$out"), $(($(ms) - t0)) ms"
    api files.mkdir "$(jq -cn --arg h "$host" --arg p "$edir" '{host_id:$h, path:$p}')" >/dev/null
    out=$(api -timeout 600s exec.run "$(jq -cn --arg h "$host" --arg z "$pcdir\\electron.zip" --arg e "$edir" '{host_id:$h, argv:["C:\\Windows\\System32\\tar.exe", "-xf", $z, "-C", $e], timeout_ms:600000}')")
    rps <<<"Remove-Item -LiteralPath $(pcq "$pcdir\\electron.zip") -Force -ErrorAction SilentlyContinue"
  fi
  check debug "$([ "$(rcat "$edir\\version" | tr -d '\r\n ')" = "$ver" ] && echo 0 || echo 1)" "portable Electron $ver is unpacked in the run folder only"
  out=$(dhj files push --on-conflict replace "$repo/cmd/deskhand-fixture/electron" "$host:$app")
  check debug "$(ok jq -e '.state == "done" and .files_total == 4' <<<"$out")" "the cmd/deskhand-fixture/electron app is in the run folder"

  log "-- launch with pause_on_start"
  t0=$(ms)
  out=$(dhj debug launch "$host" "$edir\\electron.exe" --arg "$app" --arg "--user-data-dir=$data" --break --bp "file_name:fixture-app/main.js:9" 2>"$work/dbg-err.txt")
  code=$?
  log "debug launch --break: exit $code after $(($(ms) - t0)) ms, $(jq -c '{status, session: (.session | {debug_id, state, pid, ports, targets: [.targets[] | {role, type, attached, paused}], renderer_unavailable_reason, breakpoints: [.breakpoints[] | {breakpoint_id, pattern, line, resolved}]})}' <<<"$out") $(cat "$work/dbg-err.txt")"
  d=$(jq -r .session.debug_id <<<"$out")
  mt=$(jq -r '.session.targets[] | select(.role == "main") | .id' <<<"$out")
  check debug "$([ "$code" = 0 ] && jq -e '.session.state == "paused" and .session.pid > 0 and .session.ports.inspect > 0 and .session.ports.remote_debugging > 0 and ([.session.targets[] | select(.role == "main" and .paused)] | length == 1)' <<<"$out" >/dev/null && echo 0 || echo 1)" \
    "deskhand debug launch --break exits 0 with main paused, its pid and both loopback ports"
  st=$(dbg_paused "$mt" start 10)
  check debug "$?" "main is paused with reason start before main.js runs"
  dbg resume "$(jq -cn --arg t "$mt" '{target_id:$t}')" >/dev/null
  st=$(dbg_paused "$mt" breakpoint 20)
  log "main paused: $(jq -c '{reason, hit_breakpoints, frames: [.call_frames[] | {function, url, line, column}][0:3]}' <<<"$st")"
  check debug "$(jq -e '(.hit_breakpoints | length == 1) and .call_frames[0].function == "calcTotal" and (.call_frames[0].url | endswith("/fixture-app/main.js")) and .call_frames[0].line == 9' <<<"$st" >/dev/null && echo 0 || echo 1)" \
    "the file_name breakpoint stops main in calcTotal at fixture-app/main.js:9 with the frame's URL"
  vars=$(local_vars "$st" "$mt")
  log "main locals: $vars"
  check debug "$(ok jq -e 'has("items") and has("total") and has("count")' <<<"$vars")" "the local scope shows items, total and count"
  ev=$(dbg evaluate "$(jq -cn --arg t "$mt" --arg f "$(jq -r '.call_frames[0].frame_id' <<<"$st")" '{target_id:$t, frame_id:$f, expression:"items.map(i => i.price).join(\"+\") + \"=\" + items.reduce((a, i) => a + i.price, 0)"}')")
  check debug "$(ok jq -e '.status == "succeeded" and .value_preview == "\"10+20+30=60\""' <<<"$ev")" "evaluate in the paused frame reads its variables ($(jq -r .value_preview <<<"$ev"))"
  dbg step "$(jq -cn --arg t "$mt" '{target_id:$t, kind:"over"}')" >/dev/null
  st=$(dbg_paused "$mt" step 10)
  check debug "$(jq -e '.call_frames[0].function == "calcTotal" and .call_frames[0].line == 10' <<<"$st" >/dev/null && echo 0 || echo 1)" "step over goes to line 10 ($(jq -c '.call_frames[0] | {line, column}' <<<"$st"))"
  dbg breakpoint.update "$(jq -cn '{breakpoint_id:"bp-1", enabled:false}')" >/dev/null
  dbg step "$(jq -cn --arg t "$mt" '{target_id:$t, kind:"out"}')" >/dev/null
  st=$(dbg_paused "$mt" step 10)
  log "after step out: $(jq -c '.call_frames[0] | {function, url, line, column}' <<<"$st")"
  check debug "$(jq -e '.call_frames[0].function != "calcTotal" and (.call_frames[0].url | endswith("/fixture-app/main.js")) and .call_frames[0].line >= 28 and .call_frames[0].line <= 29' <<<"$st" >/dev/null && echo 0 || echo 1)" "step out returns to the caller after main.js:28 ($(jq -c '.call_frames[0] | {line, column}' <<<"$st"))"
  dbg resume "$(jq -cn --arg t "$mt" '{target_id:$t}')" >/dev/null
  out=$(dbg_console_has "main 합계" 15)
  check debug "$(ok jq -e '.text == "main 합계 60 3개 합계 서울" and .source == "console"' <<<"$out")" "after resume main logs 'main 합계 60 3개 합계 서울'"
  for _ in $(seq 1 120); do
    rt=$(dbg get | jq -r '[.targets[] | select(.role == "renderer" and .type == "page" and .attached)][0].id // empty')
    [ -n "$rt" ] && break
    sleep 0.25
  done
  out=$(dbg get)
  log "session after main ran: $(jq -c '{state, renderer_unavailable_reason, targets: [.targets[] | {role, type, url, attached, paused}]}' <<<"$out")"
  check debug "$([ -n "$rt" ] && jq -e '.renderer_unavailable_reason == null and .targets[0].role == "main"' <<<"$out" >/dev/null && echo 0 || echo 1)" "the renderer page is attached once the window opens (main/renderer told apart, main listed first)"
  out=$(dbg_console_has "renderer 준비 완료" 10)
  check debug "$(ok jq -e --arg t "$rt" '.target_id == $t and .text == "renderer 준비 완료 — 서울"' <<<"$out")" "the renderer's first console line is recorded under the renderer target"

  rurl=$(dbg scripts "$(jq -cn --arg t "$rt" '{target_id:$t}')" | jq -r '[.scripts[] | select(.url | endswith("/fixture-app/renderer.js"))][0].url')
  setstate renderer_url "$rurl"
  out=$(dbg breakpoint.set "$(jq -cn --arg u "$rurl" '{match:"url", pattern:$u, line:11}')")
  local rbp=$(jq -r .breakpoint.breakpoint_id <<<"$out")
  check debug "$(ok jq -e '.breakpoint.resolved and (.breakpoint.locations | length >= 1)' <<<"$out")" "a url breakpoint on $rurl:11 resolves in the renderer"
  dbg evaluate "$(jq -cn --arg t "$rt" '{target_id:$t, expression:"setTimeout(() => document.getElementById(\"compute\").click(), 200); \"scheduled\""}')" >/dev/null
  st=$(dbg_paused "$rt" breakpoint 15)
  log "renderer paused: $(jq -c '{reason, hit_breakpoints, frames: [.call_frames[] | {function, url, line}]}' <<<"$st")"
  check debug "$(jq -e --arg b "$rbp" '.hit_breakpoints == [$b] and .call_frames[0].function == "tallyPrices" and .call_frames[1].function == "onCompute" and (.call_frames[0].url | endswith("/renderer.js"))' <<<"$st" >/dev/null && echo 0 || echo 1)" \
    "a click in the page (DOM click from evaluate) stops the renderer in tallyPrices called by onCompute"
  vars=$(local_vars "$st" "$rt")
  ev=$(dbg evaluate "$(jq -cn --arg t "$rt" --arg f "$(jq -r '.call_frames[0].frame_id' <<<"$st")" '{target_id:$t, frame_id:$f, expression:"items.length * 1000 + total"}')")
  log "renderer locals: $vars, evaluate: $(jq -r .value_preview <<<"$ev")"
  check debug "$(jq -e '.items == "Array(3) [Object, Object, Object]" and .total == "1500" and .highest == "0"' <<<"$vars" >/dev/null && jq -e '.value_preview == "4500"' <<<"$ev" >/dev/null && echo 0 || echo 1)" \
    "renderer locals items Array(3), total 1500, highest 0 and evaluate in the frame gives 4500"
  dbg step "$(jq -cn --arg t "$rt" '{target_id:$t, kind:"over"}')" >/dev/null
  st=$(dbg_paused "$rt" step 10)
  check debug "$?" "step over in the renderer ($(jq -c '.call_frames[0] | {line, column}' <<<"$st"))"
  dbg breakpoint.update "$(jq -cn --arg b "$rbp" '{breakpoint_id:$b, enabled:false}')" >/dev/null
  dbg step "$(jq -cn --arg t "$rt" '{target_id:$t, kind:"out"}')" >/dev/null
  st=$(dbg_paused "$rt" step 10)
  check debug "$(jq -e '.call_frames[0].function == "onCompute"' <<<"$st" >/dev/null && echo 0 || echo 1)" "step out returns to onCompute ($(jq -c '.call_frames[0] | {function, line}' <<<"$st"))"
  dbg resume "$(jq -cn --arg t "$rt" '{target_id:$t}')" >/dev/null
  sleep 0.5
  ev=$(dbg evaluate "$(jq -cn --arg t "$rt" '{target_id:$t, expression:"document.getElementById(\"result\").textContent"}')")
  check debug "$(ok jq -e '.value_preview == "\"결과: 7000원 (3개 품목)\""' <<<"$ev")" "after resume the page shows $(jq -r .value_preview <<<"$ev")"

  dbg evaluate "$(jq -cn --arg t "$rt" '{target_id:$t, expression:"setTimeout(() => document.getElementById(\"boom\").click(), 200); 1"}')" >/dev/null
  st=$(dbg_paused "$rt" exception 15)
  log "exception pause: $(jq -c '{reason, exception: .exception.value_preview, top: (.call_frames[0] | {function, line, column})}' <<<"$st")"
  check debug "$(jq -e '.exception.class_name == "TypeError" and (.exception.value_preview | contains("Cannot read properties of null (reading '"'"'total'"'"')")) and .call_frames[0].function == "onBoom" and .call_frames[0].line == 32' <<<"$st" >/dev/null && echo 0 || echo 1)" \
    "the uncaught TypeError pauses the renderer in onBoom at renderer.js:32 with the thrown value"
  dbg resume "$(jq -cn --arg t "$rt" '{target_id:$t}')" >/dev/null
  out=$(dbg_console_has "Cannot read properties of null" 10)
  ev=$(dbg_console_has "의도된 예외를 던집니다" 5)
  log "console: $(jq -c '{source, level, text, line}' <<<"$ev") / $(jq -c '{source, level, text, line, stack: (.stack | length)}' <<<"$out")"
  check debug "$(jq -e '.source == "exception" and .level == "error" and .line == 32' <<<"$out" >/dev/null && jq -e '.level == "warning" and .source == "console"' <<<"$ev" >/dev/null && echo 0 || echo 1)" \
    "the console keeps the warning and the uncaught exception with its location"

  out=$(dbg pause "$(jq -cn --arg t "$mt" '{target_id:$t}')")
  log "pause on an idle main: $(jq -c '{status, target: (.target | {paused, pause_pending, gen})}' <<<"$out")"
  ev=$(dbg resume "$(jq -cn --arg t "$mt" '{target_id:$t}')")
  sleep 2
  st=$(dbg state "$(jq -cn --arg t "$mt" '{target_id:$t}')")
  check debug "$(jq -e '.target.pause_pending or .target.paused' <<<"$out" >/dev/null && jq -e '.target.pause_pending == false' <<<"$ev" >/dev/null && jq -e '.paused == false' <<<"$st" >/dev/null && echo 0 || echo 1)" \
    "debug pause on idle main is pending, and resume cancels it without a later pause"

  local epid
  epid=$(dbg get | jq -r .pid)
  out=$(dhj debug attach "$host" --pid "$epid" 2>/dev/null)
  check debug "$(ok jq -e '.status == "failed" and .error.code == "CAPABILITY_UNSUPPORTED"' <<<"$out")" "debug attach --pid on Windows is CAPABILITY_UNSUPPORTED"

  local d1=$d ports1
  ports1=$(dbg get | jq -c '{pid, ports}')

  log "-- second launch while the first session is open: without pause_on_start, breakpoints given at launch"
  out=$(dhj debug launch "$host" "$edir\\electron.exe" --arg "$app" --arg "--user-data-dir=$data-2" --bp "url:$rurl:11" 2>"$work/dbg-err.txt")
  code=$?
  d=$(jq -r .session.debug_id <<<"$out")
  mt=$(jq -r '.session.targets[] | select(.role == "main") | .id' <<<"$out")
  log "debug launch: exit $code, $(jq -c '{status, session: (.session | {state, pid, ports, targets: [.targets[] | {role, type, attached, paused}], renderer_unavailable_reason})}' <<<"$out") $(cat "$work/dbg-err.txt"); first session $ports1"
  check debug "$([ "$code" = 0 ] && jq -e '.session.state == "running" and ([.session.targets[] | select(.role == "main" and (.paused | not))] | length == 1) and .session.targets[0].role == "main"' <<<"$out" >/dev/null && echo 0 || echo 1)" "debug launch without --break exits 0 with main running and listed first"
  check debug "$(jq -e --argjson a "$ports1" '.session.pid != $a.pid and .session.ports.inspect != $a.ports.inspect and .session.ports.remote_debugging != $a.ports.remote_debugging and .session.ports.inspect != $a.ports.remote_debugging' <<<"$out" >/dev/null && echo 0 || echo 1)" \
    "the second Electron gets its own process and loopback ports next to the first"
  for _ in $(seq 1 120); do
    rt=$(dbg get | jq -r '[.targets[] | select(.role == "renderer" and .type == "page" and .attached)][0].id // empty')
    [ -n "$rt" ] && break
    sleep 0.25
  done
  out=$(dbg_console_has "renderer 준비 완료" 10)
  check debug "$([ -n "$rt" ] && jq -e '.text == "renderer 준비 완료 — 서울"' <<<"$out" >/dev/null && echo 0 || echo 1)" "the renderer is attached before its scripts ran (its first console line is recorded)"
  dbg evaluate "$(jq -cn --arg t "$rt" '{target_id:$t, expression:"setTimeout(() => document.getElementById(\"compute\").click(), 200); 1"}')" >/dev/null
  st=$(dbg_paused "$rt" breakpoint 15)
  check debug "$(jq -e '.call_frames[0].function == "tallyPrices"' <<<"$st" >/dev/null && echo 0 || echo 1)" "the breakpoint given at launch stops the renderer in tallyPrices"
  dbg breakpoints.activate '{"active":false}' >/dev/null
  dbg resume "$(jq -cn --arg t "$rt" '{target_id:$t}')" >/dev/null
  sleep 0.5
  ev=$(dbg evaluate "$(jq -cn --arg t "$rt" '{target_id:$t, expression:"document.getElementById(\"result\").textContent"}')")
  check debug "$(ok jq -e '.value_preview == "\"결과: 7000원 (3개 품목)\""' <<<"$ev")" "with breakpoints deactivated the page finishes ($(jq -r .value_preview <<<"$ev"))"
  out=$(dhj debug close "$d")
  sleep 3
  local first
  first=$(api debug.get "{\"debug_id\":\"$d1\"}" | jq -c '{state}')
  log "second session closed: $(jq -c '{session: (.session | {state, close_reason})}' <<<"$out"); first session $first, its tunnels $(api tunnel.list '{}' | jq -c --arg o "debug:$d1" '[.tunnels[] | select(.owner == $o)] | length'), Electron processes $(pc_run_procs electron.exe)"
  check debug "$(jq -e '.session.state == "closed"' <<<"$out" >/dev/null && [ "$(api tunnel.list '{}' | jq -c --arg o "debug:$d" '[.tunnels[] | select(.owner == $o)] | length')" = 0 ] && ! jq -e '.state == "closed"' <<<"$first" >/dev/null && [ "$(api tunnel.list '{}' | jq -c --arg o "debug:$d1" '[.tunnels[] | select(.owner == $o)] | length')" = 2 ] && [ "$(pc_run_procs electron.exe)" -gt 0 ] && echo 0 || echo 1)" \
    "closing the second session ends only its own Electron and tunnels; the first session keeps its two tunnels and its app"
  d=$d1
  out=$(dhj debug close "$d")
  sleep 3
  log "close: $(jq -c '{status, session: (.session | {state, close_reason}), inspector_left_open}' <<<"$out"); tunnels $(api tunnel.list '{}' | jq -c --arg o "debug:$d" '[.tunnels[] | select(.owner == $o)] | length'); electron processes $(pc_run_procs electron.exe)"
  check debug "$(jq -e '.session.state == "closed" and .session.close_reason == "requested"' <<<"$out" >/dev/null && [ "$(api tunnel.list '{}' | jq -c --arg o "debug:$d" '[.tunnels[] | select(.owner == $o)] | length')" = 0 ] && [ "$(pc_run_procs electron.exe)" = 0 ] && echo 0 || echo 1)" \
    "debug close of the first session ends its Electron tree and its tunnels; no Electron of the run is left"
  out=$(rps <<'EOF' | tr -d '\r'
'appdata=' + (Test-Path (Join-Path $env:APPDATA 'deskhand-debug-fixture')) + ' ' + (Test-Path (Join-Path $env:APPDATA 'Electron')) + ' ' + (Test-Path (Join-Path $env:LOCALAPPDATA 'deskhand-debug-fixture'))
EOF
)
  check debug "$([ "$out" = 'appdata=False False False' ] && echo 0 || echo 1)" "Electron kept its profile in the run folder (--user-data-dir), nothing in %APPDATA% ($out)"
}

# kill_run_image ends the processes named $1 whose image lies in the run
# folder and prints how many it ended.
kill_run_image() {
  rps <<EOF | tr -d '\r'
\$p = @(Get-CimInstance Win32_Process -Filter "Name='$1'" | Where-Object { \$_.ExecutablePath -like $(pcq "$pcdir\\*") })
\$p | ForEach-Object { taskkill.exe /F /T /PID \$_.ProcessId 2>&1 | Out-Null }
'' + \$p.Count
EOF
}

step_qa() {
  log "== qa (fixture-smoke.yaml)"
  wait_ready 30
  local out code run job exp dir left
  out=$(dhj qa run "$repo/docs/examples/qa/fixture-smoke.yaml" --host "$host" --var "qa_root=$pcdir\\qa" 2>"$work/qa-err.txt")
  code=$?
  run=$(jq -r .run_id <<<"$out")
  job=$(jq -r .job_id <<<"$out")
  log "qa run: exit $code, $(jq -c '{run_id, status, steps_passed, steps_failed, steps_blocked, steps_not_run, unsupported}' <<<"$out")"
  jq -r '.steps[] | "  \(.index) \(.phase) \(.action) \(.status) \(.error.code // "-") \(.duration_ms // 0)ms \(.message // "")"' <<<"$out"
  left=$(kill_run_image deskhand-fixture.exe)
  log "fixture processes of the run left after the run (ended now): $left"
  if [ "${agent_session:-}" = "no user" ]; then
    if jq -e '.steps[0].status == "pass" and .steps[1].status == "pass" and .steps[2].action == "wait_window" and .steps[2].status == "fail"' <<<"$out" >/dev/null; then
      blocked qa "fixture-smoke GUI steps: nobody is signed in to the PC, the test agent runs in Session 0 where deskhand-fixture.exe starts without a window (MainWindowHandle 0), so wait_window times out; push and launch passed"
    else
      check qa 1 "fixture-smoke in Session 0 ended differently than push/launch pass + wait_window fail"
    fi
  else
    check qa "$([ "$code" = 0 ] && jq -e '.status == "passed" and ([.steps[] | select(.optional | not) | .status] | all(. == "pass"))' <<<"$out" >/dev/null && echo 0 || echo 1)" \
      "fixture-smoke passes every non-optional step in the signed-in session (screenshot: $(jq -r '.steps[] | select(.action == "screenshot") | .status' <<<"$out"))"
    check qa "$([ "$left" = 0 ] && echo 0 || echo 1)" "the teardown closed the fixture window (no fixture process left)"
  fi
  out=$(dhj qa get "$run")
  check qa "$(jq -e --arg j "$job" '.job_id == $j and (.report.json | length > 0) and (.steps | length == 12)' <<<"$out" >/dev/null && echo 0 || echo 1)" "qa get returns the run with its 12 steps and the report paths"
  dir="$work/qa-export"
  rm -rf "$dir"
  mkdir -p "$dir"
  exp=$(api artifacts.export "$(jq -cn --arg j "$job" --arg d "$dir" '{job_id:$j, dir:$d}')")
  log "artifacts.export: $(jq -c '{dir, files, bytes, manifest}' <<<"$exp")"
  out=$(python3 -I - "$(jq -r .dir <<<"$exp")" <<'PY'
import hashlib, json, os, sys
root = sys.argv[1]
m = json.load(open(os.path.join(root, "manifest.json")))
listed = {f["path"]: f for f in m["files"]}
present = set()
for d, _, files in os.walk(root):
    for f in files:
        rel = os.path.relpath(os.path.join(d, f), root).replace(os.sep, "/")
        if rel != "manifest.json":
            present.add(rel)
bad = [p for p, f in listed.items() if p not in present or hashlib.sha256(open(os.path.join(root, p), "rb").read()).hexdigest() != f["sha256"] or os.path.getsize(os.path.join(root, p)) != f["size"]]
rep = json.load(open(os.path.join(root, "report.json")))
html = open(os.path.join(root, "report.html"), encoding="utf-8").read()
print(json.dumps({"listed": len(listed), "present": len(present), "same_set": set(listed) == present, "bad": bad,
                  "run_id_matches": rep["run"]["run_id"] == m["run_id"], "has_md": "report.md" in present,
                  "html_has_script": "<script" in html.lower(), "korean_title": "fixture-smoke" in html}))
PY
)
  log "exported bundle: $out"
  check qa "$(ok jq -e '.same_set and (.bad | length == 0) and .run_id_matches and .has_md and (.html_has_script | not)' <<<"$out")" "the exported bundle's files match manifest.json (SHA-256 and size), report.json names the run, report.html has no script"
}

# stats prints "min / median / max" of the numbers on stdin.
stats() { sort -n | awk '{a[NR] = $1} END {printf "%s / %s / %s", a[1], a[int((NR + 1) / 2)], a[NR]}'; }

step_measure() {
  log "== measure (SSH 중계 경유 — 대표값 아님)"
  wait_ready 30
  local i t0 a b c e
  a=$(for i in $(seq 1 10); do t0=$(ms); api exec.run "{\"host_id\":\"$host\",\"argv\":[\"C:\\\\Windows\\\\System32\\\\hostname.exe\"]}" >/dev/null; echo $(($(ms) - t0)); done | stats)
  b=$(for i in $(seq 1 10); do t0=$(ms); api exec.run "{\"host_id\":\"$host\",\"cmd\":\"echo hi\",\"shell\":\"cmd\"}" >/dev/null; echo $(($(ms) - t0)); done | stats)
  c=$(for i in $(seq 1 10); do t0=$(ms); api files.stat "$(jq -cn --arg h "$host" --arg p "$pcdir" '{host_id:$h, path:$p}')" >/dev/null; echo $(($(ms) - t0)); done | stats)
  e=$(for i in $(seq 1 3); do read -r _ _ dms <<<"$(drop)"; echo "$dms"; done | stats)
  log "exec.run hostname.exe (ms): $a"
  log "exec.run cmd echo (ms): $b"
  log "files.stat (ms): $c"
  log "relay drop 12 s -> ready (ms, 3 runs): $e"
  setstate m_exec_hostname "$a"
  setstate m_exec_cmd "$b"
  setstate m_stat "$c"
  setstate m_drop "$e"
  check measure 0 "measured 10 exec.run, 10 files.stat and 3 drops"
}

all_steps=(build up exec pty xfer conflict movefail resume names diag tunnel debug qa measure down)
for s in "${steps[@]}"; do
  case "$s" in
  all) for a in "${all_steps[@]}"; do "step_$a"; done ;;
  *)
    if declare -F "step_$s" >/dev/null; then "step_$s"; else
      echo "unknown step $s" >&2
      exit 2
    fi
    ;;
  esac
done
log "failed checks: $fails"
exit "$fails"
