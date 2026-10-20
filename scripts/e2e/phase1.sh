#!/usr/bin/env bash
# Phase 1 end-to-end run against a real Windows PC (docs/acceptance/phase1.md).
#
#   scripts/e2e/phase1.sh [step ...]
#
# Steps, in the order "all" runs them:
#   build    scripts/build.sh and the apicall and mcpprobe helpers
#   reset    remove an installed agent with uninstall --purge, its leftover
#            data and the E2E folder, so that add pairs from scratch
#   add      1: deskhand host add; the agent runs in the console session
#   exec     2: cmd /c "echo 안녕 & exit 7" gives 안녕 and exit code 7
#   files    3: Korean/emoji folder: mkdir, files.write, cat, ls
#   drop     4: daemon with DESKHAND_TEST_DROP_AFTER_SEND=exec.run settles an
#               appended line through req.status; the file has one line
#   restart  5: Stop-Process the agent and start its task DESKHAND_E2E_RESTARTS
#               times (default 5): reconnecting -> ready, generation +1 each
#               time, p50 agent kill -> ready <= 15 s, an earlier exec.start
#               job keeps its result
#   mcp      6: deskhand mcp through an MCP client: hosts_list, exec_run
#   reregister  remove --uninstall then a plain add; then from a new Mac home
#               add fails with the add --force next step and add --force pairs
#               (the new home becomes DESKHAND_HOME)
#   cleanup  8: remove the PC's E2E folder, stop the daemon (the agent stays)
#
# Environment: DESKHAND_E2E_SSH (ssh alias, default windows), DESKHAND_E2E_HOST
# (host name, default windows-dev), DESKHAND_HOME (default: a new
# /tmp/dh-e2e-XXXX, remembered in build/e2e-home for later steps).
# Needs jq and python3 on the Mac.
# Every check prints "CHECK <step> PASS|FAIL <what>"; the exit code is the
# number of failed checks.
set -uo pipefail
cd "$(dirname "$0")/../.." || exit 2
repo=$PWD

ssh_alias=${DESKHAND_E2E_SSH:-windows}
host=${DESKHAND_E2E_HOST:-windows-dev}
remote_dir='C:\deskhand-e2e'
fails=0

steps=("$@")
[ ${#steps[@]} -eq 0 ] && steps=(all)
if [ -z "${DESKHAND_HOME:-}" ]; then
  case "${steps[0]}" in
  all | build | reset | add)
    if [ -f build/e2e-home ] && [ -x build/deskhand ]; then
      DESKHAND_HOME=$(cat build/e2e-home) build/deskhand daemon stop >/dev/null 2>&1
    fi
    DESKHAND_HOME=$(mktemp -d /tmp/dh-e2e-XXXX)
    ;;
  *) DESKHAND_HOME=$(cat build/e2e-home 2>/dev/null || mktemp -d /tmp/dh-e2e-XXXX) ;;
  esac
fi
export DESKHAND_HOME DESKHAND_AGENT_DIR="$repo/build"
mkdir -p build && echo "$DESKHAND_HOME" > build/e2e-home

dh() { "$repo/build/deskhand" "$@"; }
api() { "$repo/build/apicall" "$@"; }
now() { date '+%Y-%m-%dT%H:%M:%S%z'; }
log() { printf '[%s] %s\n' "$(now)" "$*"; }
check() { # check <step> <ok 0|1> <what>
  if [ "$2" = 0 ]; then log "CHECK $1 PASS $3"; else log "CHECK $1 FAIL $3"; fails=$((fails + 1)); fi
}

# rps runs the PowerShell script on stdin on the PC with UTF-8 output.
rps() {
  local script enc
  script='[Console]::OutputEncoding=[Text.Encoding]::UTF8; $ProgressPreference="SilentlyContinue"
'"$(cat)"
  enc=$(printf '%s' "$script" | iconv -f UTF-8 -t UTF-16LE | base64 | tr -d '\n')
  ssh -o BatchMode=yes -o ClearAllForwardings=yes "$ssh_alias" powershell -NoProfile -NonInteractive -EncodedCommand "$enc" 2> >(grep -v '^\*\* ' >&2)
}

# hostfield prints the field of the host from host.list.
hostfield() { api host.list | jq -r --arg h "$host" ".hosts[] | select(.name == \$h) | .$1"; }

# wait_ready waits up to $1 seconds for the host to be ready.
wait_ready() {
  for _ in $(seq 1 $(($1 * 2))); do
    [ "$(hostfield state 2>/dev/null)" = ready ] && return 0
    sleep 0.5
  done
  return 1
}

start_daemon() { dh daemon start >/dev/null && log "daemon: $(dh daemon status | head -1)"; }
stop_daemon() { dh daemon stop >/dev/null 2>&1 || true; }

step_build() {
  log "== build"
  scripts/build.sh &&
    CGO_ENABLED=0 go build -o build/apicall ./scripts/e2e/apicall &&
    CGO_ENABLED=0 go build -o build/mcpprobe ./scripts/e2e/mcpprobe
  check build $? "scripts/build.sh and helpers ($(git describe --always --dirty))"
}

step_reset() {
  log "== 0 reset"
  stop_daemon
  local out
  out=$(rps <<'EOF'
$exe = 'C:\Program Files\Deskhand\deskhand-agent.exe'
if (Test-Path -LiteralPath $exe) {
  $p = Start-Process -FilePath $exe -ArgumentList 'uninstall --purge' -NoNewWindow -Wait -PassThru
  'uninstall --purge exit=' + $p.ExitCode
}
Remove-Item -LiteralPath 'C:\ProgramData\Deskhand' -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath (Join-Path $env:TEMP 'deskhand-install') -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path (Join-Path $env:TEMP 'deskhand-agent-*.exe.old') -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath 'C:\deskhand-e2e' -Recurse -Force -ErrorAction SilentlyContinue
'left task=' + [bool](Get-ScheduledTask -TaskName 'Deskhand Agent' -ErrorAction SilentlyContinue) + ' rule=' + [bool](Get-NetFirewallRule -DisplayName 'Deskhand Agent' -ErrorAction SilentlyContinue) + ' program=' + (Test-Path 'C:\Program Files\Deskhand') + ' data=' + (Test-Path 'C:\ProgramData\Deskhand') + ' process=' + [bool](Get-Process deskhand-agent -ErrorAction SilentlyContinue)
EOF
)
  printf '%s\n' "$out"
  grep -q 'left task=False rule=False program=False data=False process=False' <<<"$out"
  check reset $? "no agent, task, rule or data left on the PC"
}

step_add() {
  log "== 1 host add (DESKHAND_HOME=$DESKHAND_HOME)"
  start_daemon
  local t0 t1 out
  t0=$(date +%s)
  dh host add "$host" --ssh "$ssh_alias"
  check add $? "deskhand host add $host --ssh $ssh_alias exits 0"
  t1=$(date +%s)
  log "host add took $((t1 - t0)) s; state $(hostfield state), generation $(hostfield connection_generation)"
  out=$(rps <<'EOF'
$p = Get-Process deskhand-agent -ErrorAction SilentlyContinue
"agent pid=" + $p.Id + " session=" + $p.SessionId + " path=" + $p.Path
"console children=" + @(Get-CimInstance Win32_Process -Filter ("ParentProcessId=" + $p.Id + " AND Name='conhost.exe'")).Count
$t = Get-ScheduledTask -TaskName 'Deskhand Agent'
"task state=" + $t.State + " run level=" + $t.Principal.RunLevel + " logon=" + $t.Principal.LogonType
$r = Get-NetFirewallRule -DisplayName 'Deskhand Agent'
"rule enabled=" + $r.Enabled + " remote=" + ($r | Get-NetFirewallAddressFilter).RemoteAddress + " port=" + ($r | Get-NetFirewallPortFilter).LocalPort + "/" + ($r | Get-NetFirewallPortFilter).Protocol
"defender detections (10 min)=" + @(Get-MpThreatDetection | Where-Object { $_.InitialDetectionTime -gt (Get-Date).AddMinutes(-10) }).Count
EOF
)
  printf '%s\n' "$out"
  grep -q 'session=1 ' <<<"$out"
  check add $? "Get-Process deskhand-agent SessionId is 1"
  grep -q 'console children=0' <<<"$out"
  check add $? "the agent has no console host"
  out=$(dh exec "$host" --shell powershell -- "@(Get-Process | Where-Object { \$_.MainWindowTitle -like '*deskhand-agent*' }).Count")
  check add "$([ "$(tr -d '\r\n ' <<<"$out")" = 0 ] && echo 0 || echo 1)" "no window in the session is titled deskhand-agent (count $(tr -d '\r\n ' <<<"$out"))"
  dh host doctor "$host"
  check add $? "deskhand host doctor $host passes"
}

step_exec() {
  log "== 2 exec exit code and Korean output"
  start_daemon
  wait_ready 30
  local out code
  out=$(dh exec "$host" -- cmd /c "echo 안녕 & exit 7")
  code=$?
  log "stdout bytes $(printf '%s' "$out" | xxd -p | tr -d '\n'), text \"$(printf '%s' "$out" | tr -d '\r')\""
  check exec "$([ "$code" = 7 ] && echo 0 || echo 1)" "exit code $code, want 7"
  check exec "$([ "$(printf '%s' "$out" | tr -d '\r' | sed 's/ *$//')" = "안녕" ] && echo 0 || echo 1)" "stdout is 안녕"
}

step_files() {
  log "== 3 korean-path"
  start_daemon
  wait_ready 30
  local dir="$remote_dir\\한글 폴더 😀" file content got
  file="$dir\\파일 😀.txt"
  content=$'안녕 😀\n두 번째 줄\n'
  dh files mkdir "$host:$dir"
  check files $? "files mkdir $dir"
  api files.write "$(jq -cn --arg h "$host" --arg p "$file" --arg d "$(printf '%s' "$content" | base64)" '{host_id:$h, path:$p, data:$d}')"
  check files $? "files.write $file (the CLI has no files write)"
  got=$(dh files cat "$host:$file"; echo x)
  check files "$([ "${got%x}" = "$content" ] && echo 0 || echo 1)" "files cat returns the written bytes"
  dh files ls "$host:$dir"
  dh --json files ls "$host:$remote_dir" | jq -e --arg n "한글 폴더 😀" '.entries | map(.name) | index($n) != null' >/dev/null
  check files $? "files ls $remote_dir lists 한글 폴더 😀"
  dh --json files ls "$host:$dir" | jq -e '.entries | map(.name) == ["파일 😀.txt"]' >/dev/null
  check files $? "files ls lists 파일 😀.txt"
  rps <<EOF
Get-ChildItem -LiteralPath '$remote_dir' -Recurse | ForEach-Object { 'remote name ' + \$_.Name + ' = ' + ((\$_.Name.ToCharArray() | ForEach-Object { '{0:X4}' -f [int]\$_ }) -join ' ') }
EOF
}

step_drop() {
  log "== 4 outcome unknown settled through req.status"
  stop_daemon
  DESKHAND_TEST_DROP_AFTER_SEND=exec.run start_daemon
  wait_ready 30
  check drop $? "host ready under the hook"
  rps <<<"Remove-Item -LiteralPath '$remote_dir\\lines.txt' -ErrorAction SilentlyContinue; New-Item -ItemType Directory -Force -Path '$remote_dir' | Out-Null"
  local gen0 res gen1 lines
  gen0=$(hostfield connection_generation)
  api -events connection.state,job.updated -host "$host" -for 15s >"$DESKHAND_HOME/drop.events" &
  local ev=$!
  sleep 0.5
  res=$(api -timeout 120s exec.run "$(jq -cn --arg h "$host" --arg c "echo line>>$remote_dir\\lines.txt" '{host_id:$h, cmd:$c, shell:"cmd"}')")
  log "exec.run envelope: $res"
  check drop "$(jq -e '.status == "succeeded" and .action.executed == true and .code == 0' <<<"$res" >/dev/null && echo 0 || echo 1)" "the envelope is succeeded with code 0"
  wait_ready 30
  gen1=$(hostfield connection_generation)
  check drop "$([ "$gen1" = $((gen0 + 1)) ] && echo 0 || echo 1)" "generation $gen0 -> $gen1"
  grep -h "DESKHAND_TEST_DROP_AFTER_SEND" "$DESKHAND_HOME/logs/daemon.log" | tail -2
  lines=$(rps <<<"@(Get-Content -LiteralPath '$remote_dir\\lines.txt').Count" | tr -d '\r\n ')
  check drop "$([ "$lines" = 1 ] && echo 0 || echo 1)" "the remote file has $lines line(s), want 1"
  rps <<EOF
Get-ChildItem 'C:\\ProgramData\\Deskhand\\journal' -File | Sort-Object LastWriteTime | Select-Object -Last 1 | ForEach-Object { 'journal ' + (Get-Content -Raw \$_.FullName) }
EOF
  wait "$ev"
  cat "$DESKHAND_HOME/drop.events"
  stop_daemon
  start_daemon
  wait_ready 30
}

# pc_offset_ms prints the PC clock minus the Mac clock in milliseconds: the
# largest difference between a PC time stamp and the Mac time it arrives at.
pc_offset_ms() {
  python3 -I - "$ssh_alias" <<'PY'
import base64, subprocess, sys, time
script = "1..8 | ForEach-Object { [Console]::Out.WriteLine([DateTimeOffset]::Now.ToUnixTimeMilliseconds()); [Console]::Out.Flush(); Start-Sleep -Milliseconds 150 }"
enc = base64.b64encode(script.encode("utf-16-le")).decode()
p = subprocess.Popen(["ssh", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", sys.argv[1], "powershell",
                      "-NoProfile", "-NonInteractive", "-EncodedCommand", enc],
                     stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
best = None
for line in p.stdout:
    now = time.time() * 1000
    line = line.strip()
    if line.isdigit():
        d = int(line) - now
        best = d if best is None else max(best, d)
p.wait()
print(round(best))
PY
}

# iso_ms prints the Unix time in milliseconds of an RFC 3339 UTC time.
iso_ms() {
  python3 -I -c 'import sys, datetime; print(int(datetime.datetime.strptime(sys.argv[1], "%Y-%m-%dT%H:%M:%S.%fZ").replace(tzinfo=datetime.timezone.utc).timestamp() * 1000))' "$1"
}

step_restart() {
  log "== 5 agent restart"
  start_daemon
  wait_ready 30
  local runs=${DESKHAND_E2E_RESTARTS:-5} job before after offset i gen0 gen1 out ev kill_ms ready states secs p50
  local all=()
  job=$(dh exec "$host" --detach -- cmd /c "echo before-restart & exit 3" | head -1)
  sleep 2
  before=$(api jobs.get "{\"job_id\":\"$job\"}")
  log "job before: $before"
  offset=$(pc_offset_ms)
  log "PC clock minus Mac clock: $offset ms"
  for i in $(seq 1 "$runs"); do
    gen0=$(hostfield connection_generation)
    api -events connection.state -host "$host" -for 60s >"$DESKHAND_HOME/restart-$i.events" &
    ev=$!
    sleep 0.5
    out=$(rps <<'EOF'
$p = Get-Process deskhand-agent
Stop-Process -Id $p.Id -Force
'kill=' + [DateTimeOffset]::Now.ToUnixTimeMilliseconds() + ' pid=' + $p.Id
Start-Sleep -Seconds 1
schtasks /Run /TN "Deskhand Agent" | Out-Null
'run=' + [DateTimeOffset]::Now.ToUnixTimeMilliseconds()
EOF
)
    for _ in $(seq 1 120); do
      [ "$(hostfield connection_generation)" = $((gen0 + 1)) ] && [ "$(hostfield state)" = ready ] && break
      sleep 0.5
    done
    gen1=$(hostfield connection_generation)
    kill "$ev" 2>/dev/null
    wait "$ev" 2>/dev/null
    kill_ms=$(sed -n 's/^kill=\([0-9]*\).*/\1/p' <<<"$out")
    ready=$(jq -rs --argjson g $((gen0 + 1)) 'map(select(.state == "ready" and .connection_generation == $g)) | .[0].ts // empty' "$DESKHAND_HOME/restart-$i.events")
    states=$(jq -rs 'map(.state + "@" + .ts[11:23]) | join(" > ")' "$DESKHAND_HOME/restart-$i.events")
    secs=NaN
    if [ -n "$ready" ] && [ -n "$kill_ms" ]; then
      secs=$(python3 -I -c 'import sys; print("%.1f" % ((int(sys.argv[1]) - (int(sys.argv[2]) - int(sys.argv[3]))) / 1000))' "$(iso_ms "$ready")" "$kill_ms" "$offset")
    fi
    log "run $i: kill $(sed -n 's/^kill=[0-9]* //p' <<<"$out"), $states, generation $gen0 -> $gen1, agent kill -> ready $secs s"
    jq -s -e 'map(.state) | (index("reconnecting") != null) and (.[-1] == "ready")' "$DESKHAND_HOME/restart-$i.events" >/dev/null &&
      [ "$gen1" = $((gen0 + 1)) ]
    check restart $? "run $i: reconnecting -> ready, generation $gen0 -> $gen1"
    all+=("$secs")
  done
  p50=$(printf '%s\n' "${all[@]}" | sort -n | sed -n "$(((runs + 1) / 2))p")
  log "agent kill -> ready over $runs runs: ${all[*]} s, p50 $p50 s"
  check restart "$(python3 -I -c 'import sys; print(0 if float(sys.argv[1]) <= 15 else 1)' "$p50")" "p50 agent kill -> ready $p50 s <= 15 s"
  after=$(api jobs.get "{\"job_id\":\"$job\"}")
  log "job after: $after"
  check restart "$([ "$(jq -c '{status, result}' <<<"$before")" = "$(jq -c '{status, result}' <<<"$after")" ] && jq -e '.result.code == 3' <<<"$after" >/dev/null && echo 0 || echo 1)" "jobs.get keeps the earlier exec.start result"
}

step_mcp() {
  log "== 6 MCP"
  start_daemon
  wait_ready 30
  local out
  out=$("$repo/build/mcpprobe" -deskhand "$repo/build/deskhand" 'hosts_list={}' \
    "exec_run=$(jq -cn --arg h "$host" '{host:$h, argv:["C:\\Windows\\System32\\hostname.exe"]}')")
  check mcp $? "mcpprobe calls succeed"
  printf '%s\n' "$out" | cut -c1-400
  jq -s -e --arg h "$host" '.[1].structured.hosts | map(.name) | index($h) != null' <<<"$out" >/dev/null
  check mcp $? "hosts_list lists $host"
  jq -s -e '.[2].structured.status == "succeeded" and .[2].structured.code == 0 and (.[2].structured.stdout | length > 0)' <<<"$out" >/dev/null
  check mcp $? "exec_run hostname succeeds with output"
}

step_reregister() {
  log "== 1b re-register"
  start_daemon
  wait_ready 30
  local out err code
  dh host remove "$host" --uninstall
  check reregister $? "deskhand host remove $host --uninstall"
  out=$(rps <<'EOF'
'left task=' + [bool](Get-ScheduledTask -TaskName 'Deskhand Agent' -ErrorAction SilentlyContinue) + ' rule=' + [bool](Get-NetFirewallRule -DisplayName 'Deskhand Agent' -ErrorAction SilentlyContinue) + ' program=' + (Test-Path 'C:\Program Files\Deskhand') + ' data=' + (Test-Path 'C:\ProgramData\Deskhand')
EOF
)
  printf '%s\n' "$out"
  grep -q 'left task=False rule=False program=False data=False' <<<"$out"
  check reregister $? "remove --uninstall left no task, rule, program or data (key) on the PC"
  dh host add "$host" --ssh "$ssh_alias"
  check reregister $? "plain host add after remove --uninstall"
  wait_ready 30
  check reregister $? "host ready after the plain add"
  stop_daemon
  DESKHAND_HOME=$(mktemp -d /tmp/dh-e2e-XXXX)
  echo "$DESKHAND_HOME" >build/e2e-home
  log "new Mac home $DESKHAND_HOME"
  start_daemon
  err=$(dh host add "$host" --ssh "$ssh_alias" 2>&1 >/dev/null)
  code=$?
  printf '%s\n' "$err"
  check reregister "$([ "$code" = 1 ] && echo 0 || echo 1)" "host add from the new home exits 1 (exit $code)"
  grep -qF "다음 단계: deskhand host add $host --ssh $ssh_alias --force 를 실행하세요" <<<"$err"
  check reregister $? "the next step is deskhand host add $host --ssh $ssh_alias --force"
  out=$(dh --json host add "$host" --ssh "$ssh_alias" 2>/dev/null)
  log "json: $(jq -c '{status, error, next_action}' <<<"$out")"
  jq -e '(.error.next_action // .next_action) == "host_add_force"' <<<"$out" >/dev/null
  check reregister $? "--json carries next_action host_add_force"
  dh host add "$host" --ssh "$ssh_alias" --force
  check reregister $? "deskhand host add $host --ssh $ssh_alias --force exits 0"
  wait_ready 30
  check reregister $? "host ready after add --force"
  out=$(dh exec "$host" -- 'C:\Windows\System32\hostname.exe')
  check reregister $? "exec after add --force ($(tr -d '\r\n' <<<"$out"))"
}

step_cleanup() {
  log "== 8 cleanup"
  rps <<<"Remove-Item -LiteralPath '$remote_dir' -Recurse -Force -ErrorAction SilentlyContinue; Remove-Item -Path (Join-Path \$env:TEMP 'deskhand-agent-*.exe.old') -Force -ErrorAction SilentlyContinue; 'e2e folder left=' + (Test-Path '$remote_dir'); 'agent running=' + [bool](Get-Process deskhand-agent -ErrorAction SilentlyContinue)"
  stop_daemon
  log "daemon stopped; the agent stays paired with DESKHAND_HOME $DESKHAND_HOME"
}

for s in "${steps[@]}"; do
  case "$s" in
  all) for a in build reset add exec files drop restart mcp reregister cleanup; do "step_$a"; done ;;
  build | reset | add | exec | files | drop | restart | mcp | reregister | cleanup) "step_$s" ;;
  *) echo "unknown step $s" >&2; exit 2 ;;
  esac
done
log "failed checks: $fails"
exit "$fails"
