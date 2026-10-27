#!/usr/bin/env python3
"""Fault-injection soak for the isolated Windows agent started by phase3.sh up.

Never points at the installed agent. The supplied PC folder and scheduled task
must be the same run's e2e3-*/DeskhandE2E-* pair. Results go to a JSON receipt.
"""

import argparse
import base64
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import time
import uuid


ROOT = Path(__file__).resolve().parents[2]


def require(condition, detail="verification failed"):
    if not condition:
        raise AssertionError(detail)


class Soak:
    def __init__(self, args):
        self.args = args
        self.env = dict(os.environ, DESKHAND_HOME=args.home)
        self.checks = []
        self.started = time.monotonic()
        self.run_id = uuid.uuid4().hex
        self.remote_file = args.pc_dir + "\\files\\unattended-" + self.run_id + ".txt"

    def command(self, argv, timeout=75):
        result = subprocess.run(argv, env=self.env, capture_output=True, text=True,
                                encoding="utf-8", errors="replace", timeout=timeout)
        if result.returncode:
            raise RuntimeError(f"{Path(argv[0]).name} exited {result.returncode}: "
                               f"{result.stdout[-3000:]} {result.stderr[-1000:]}")
        return result.stdout

    def api(self, method, **params):
        raw = self.command([str(ROOT / "build/apicall"), method, json.dumps(params)])
        return json.loads(raw)

    def ps(self, script):
        encoded = base64.b64encode(("[Console]::OutputEncoding=[Text.Encoding]::UTF8; "
                                   "$ErrorActionPreference='Stop'; " + script)
                                  .encode("utf-16le")).decode()
        return self.command(["ssh", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes",
                             self.args.ssh, "powershell", "-NoProfile", "-NonInteractive",
                             "-EncodedCommand", encoded])

    def record(self, name, **details):
        check = dict(name=name, elapsed_s=round(time.monotonic() - self.started, 3), **details)
        self.checks.append(check)
        print(json.dumps(check, ensure_ascii=False), flush=True)

    def ready(self, previous=None):
        end = time.monotonic() + 60
        while time.monotonic() < end:
            try:
                host = next(h for h in self.api("host.list")["hosts"]
                            if h["host_id"] == self.args.host)
                if host["state"] == "ready" and (previous is None or
                                                host["connection_generation"] > previous):
                    return host
            except (RuntimeError, StopIteration, KeyError):
                pass
            time.sleep(.3)
        raise AssertionError("test host did not become ready")

    def job(self, job_id, timeout=65):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            job = self.api("jobs.get", job_id=job_id)
            if job["status"] not in ("running", "waiting_for_user"):
                return job
            time.sleep(.25)
        raise AssertionError(f"job {job_id} remained running: {job}")

    def execute(self, cmd):
        result = self.api("exec.run", host_id=self.args.host, shell="powershell", cmd=cmd)
        require(result["status"] == "succeeded", result)
        return result.get("stdout", "").strip()

    def marker(self, value):
        return f"Add-Content -LiteralPath '{self.remote_file}' -Value '{value}' -Encoding UTF8"

    def marker_count(self, value):
        return int(self.execute(f"@(Get-Content -LiteralPath '{self.remote_file}' | "
                                f"Where-Object {{ $_ -eq '{value}' }}).Count"))

    def mcp(self, tool, **params):
        raw = self.command([str(ROOT / "build/mcpprobe"), "-deskhand",
                            str(ROOT / "build/deskhand"), tool + "=" + json.dumps(params)])
        lines = [json.loads(line) for line in raw.splitlines()]
        result = lines[-1]
        require(result.get("tool") == tool and not result.get("is_error"), result)
        return result

    def timeout(self):
        result = self.api("exec.start", host_id=self.args.host, shell="powershell",
                          cmd="Start-Sleep -Seconds 120", timeout_ms=1200)
        job = self.job(result["job_id"])
        require(job["status"] == "failed" and job["result"]["error"]["code"] == "TIMEOUT", job)
        pid = result["pid"]
        require(self.ps(f"if (Get-Process -Id {pid} -ErrorAction SilentlyContinue) "
                        "{ 'present' } else { 'gone' }").strip() == "gone", pid)
        self.record("timeout_process_terminated", job_id=job["job_id"])

    def takeover(self):
        self.api("control.acquire", host_id=self.args.host, **{"as": "human", "force": True})
        try:
            raw = subprocess.run([str(ROOT / "build/mcpprobe"), "-deskhand",
                                  str(ROOT / "build/deskhand"),
                                  "input_type=" + json.dumps(dict(host=self.args.host,
                                                                 text="MUST_NOT_BE_TYPED"))],
                                 env=self.env, capture_output=True, text=True, timeout=30)
            require(raw.returncode == 1 and "CONTROL_HELD_BY_HUMAN" in raw.stdout, raw.stdout)
        finally:
            self.api("control.release", host_id=self.args.host)
        self.record("human_takeover_blocks_mcp_input")

    def recover_job(self, kind, round_index):
        value = f"{self.run_id}-{round_index}-{kind}"
        result = self.api("exec.start", host_id=self.args.host, shell="powershell",
                          cmd=self.marker(value) + "; Start-Sleep -Seconds 18; 'finished'",
                          timeout_ms=55000)
        end = time.monotonic() + 10
        while time.monotonic() < end and self.marker_count(value) == 0:
            time.sleep(.1)
        require(self.marker_count(value) == 1)
        if kind == "network":
            before = self.ready()["connection_generation"]
            os.kill(self.args.relay_pid, signal.SIGUSR1)
            host = self.ready(before)
            self.record("network_reconnected", generation=host["connection_generation"])
        else:
            self.command([str(ROOT / "build/deskhand"), "daemon", "stop"])
            self.command([str(ROOT / "build/deskhand"), "daemon", "start"])
            self.ready()
        job = self.job(result["job_id"])
        require(job["status"] == "succeeded", job)
        require(self.marker_count(value) == 1, "side effect was duplicated")
        self.record(kind + "_job_survived_once", job_id=job["job_id"])

    def agent_restart(self):
        before = self.ready()["connection_generation"]
        value = f"{self.run_id}-agent-restart"
        result = self.api("exec.start", host_id=self.args.host, shell="powershell",
                          cmd=self.marker(value) + "; Start-Sleep -Seconds 120; 'unexpected'",
                          timeout_ms=150000)
        end = time.monotonic() + 10
        while time.monotonic() < end and self.marker_count(value) == 0:
            time.sleep(.1)
        require(self.marker_count(value) == 1)
        self.ps(f"Stop-ScheduledTask -TaskName '{self.args.agent_task}'; "
                f"Get-CimInstance Win32_Process | Where-Object {{ "
                f"$_.ExecutablePath -eq '{self.args.pc_dir}\\deskhand-agent.exe' }} | "
                "ForEach-Object { Stop-Process -Id $_.ProcessId -Force }; "
                f"Start-ScheduledTask -TaskName '{self.args.agent_task}'")
        self.ready(before)
        job = self.job(result["job_id"])
        require(job["status"] == "unknown", job)
        require(self.marker_count(value) == 1)
        self.record("agent_restart_outcome_unknown_no_replay", job_id=job["job_id"])

    def qa(self):
        raw = self.command([str(ROOT / "build/deskhand"), "--json", "qa", "run",
                            str(ROOT / "docs/examples/qa/fixture-smoke.yaml"),
                            "--host", self.args.host, "--var",
                            "qa_root=" + self.args.pc_dir + "\\qa"], timeout=150)
        run = json.loads(raw)
        require(run["status"] == "passed" and run["steps_passed"] == 12, run)
        self.record("gui_qa_12_steps", run_id=run["run_id"])
        self.mcp_input(run["run_id"])

    def mcp_input(self, qa_run_id):
        fixture = self.args.pc_dir + "\\qa\\" + qa_run_id + "\\deskhand-fixture.exe"
        launch = self.mcp("app_launch", host=self.args.host, path=fixture)["structured"]
        require(launch["status"] == "succeeded", launch)
        window = launch["window"]["id"]
        message = "무인 입력 검증 " + uuid.uuid4().hex[:12]
        try:
            self.mcp("win_action", host=self.args.host, window=window, action="focus")
            windows = self.mcp("win_list", host=self.args.host)["structured"]["windows"]
            require(any(w["id"] == window and w.get("foreground") for w in windows),
                    "fixture did not become the foreground window")
            for name, role in (("Message", "edit"), ("Send", "button")):
                found = self.mcp("ui_find", host=self.args.host, window=window,
                                 name=name, role=role)["structured"]["nodes"]
                require(len(found) == 1, found)
                node = found[0]
                require(node["w"] > 0 and node["h"] > 0, node)
                captured = self.mcp("screen_capture", host=self.args.host)
                require(captured.get("images", 0) > 0 and captured.get("image_bytes", 0) > 0,
                        "MCP screen_capture did not return an image content block")
                capture = captured["structured"]
                origin, scale = capture["origin"], capture["scale"]
                x = (node["x"] + node["w"] / 2 - origin["x"]) * scale
                y = (node["y"] + node["h"] / 2 - origin["y"]) * scale
                clicked = self.mcp("input_click", host=self.args.host,
                                   capture_id=capture["capture_id"], x=x, y=y)["structured"]
                require(clicked["status"] == "succeeded", clicked)
                if name == "Message":
                    typed = self.mcp("input_type", host=self.args.host, text=message,
                                     method="unicode")["structured"]
                    require(typed["status"] == "succeeded", typed)
            end = time.monotonic() + 15
            while time.monotonic() < end:
                text = self.mcp("ui_text", host=self.args.host, window=window,
                                name="Transcript", role="document")["structured"].get("text", "")
                if "You: " + message in text and "[done]" in text:
                    self.record("mcp_raw_unicode_and_click", window=window)
                    return
                time.sleep(.3)
            raise AssertionError("raw MCP input was not reflected in the fixture transcript")
        finally:
            self.mcp("win_action", host=self.args.host, window=window, action="close")

    def run(self):
        relay = self.command(["ps", "-p", str(self.args.relay_pid), "-o", "command="]).strip()
        require(relay.startswith(str(ROOT / "build/udprelay") + " mac "), relay)
        task_program = self.ps(f"(Get-ScheduledTask -TaskName '{self.args.agent_task}').Actions.Execute").strip().strip('"')
        require(task_program.lower() == (self.args.pc_dir + "\\e2e3launch.exe").lower(), task_program)
        self.ready()
        session = self.api("session.inspect", host_id=self.args.host)
        require(session["session_id"] > 0 and session["connect_state"] == "active", session)
        self.record("interactive_session", session_id=session["session_id"])
        self.mcp("exec_run", host=self.args.host, argv=["hostname.exe"])
        self.record("mcp_command")
        for i in range(self.args.rounds):
            self.timeout()
            self.takeover()
            self.recover_job("network" if i % 2 == 0 else "daemon", i)
            self.qa()
            self.record("round_passed", round=i + 1)
        self.agent_restart()
        self.qa()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--home", required=True)
    parser.add_argument("--host", default="windows-e2e3")
    parser.add_argument("--ssh", default="windows")
    parser.add_argument("--pc-dir", required=True)
    parser.add_argument("--agent-task", required=True)
    parser.add_argument("--relay-pid", type=int, required=True)
    parser.add_argument("--rounds", type=int, default=10)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    run = re.fullmatch(r"[A-Za-z]:\\[A-Za-z0-9._\\-]+\\e2e3-([0-9]+)", args.pc_dir)
    if not run or args.agent_task != "DeskhandE2E-" + run[1]:
        parser.error("pc-dir and agent-task must identify the same phase3 isolated run")
    if not 1 <= args.rounds <= 1000 or args.relay_pid <= 1:
        parser.error("rounds must be 1..1000 and relay-pid must be >1")
    soak = Soak(args)
    failure = "run did not finish"
    try:
        soak.run()
        failure = None
    except Exception as error:
        failure = repr(error)
    finally:
        # The caller owns phase3.sh down. Keep evidence even when a check fails.
        args.report.parent.mkdir(parents=True, exist_ok=True)
        receipt = dict(passed=failure is None, error=failure, run_id=soak.run_id,
                       elapsed_s=round(time.monotonic() - soak.started, 3),
                       rounds=args.rounds, checks=soak.checks)
        args.report.write_text(json.dumps(receipt, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps(receipt, ensure_ascii=False), flush=True)
    return 0 if failure is None else 1


if __name__ == "__main__":
    raise SystemExit(main())
