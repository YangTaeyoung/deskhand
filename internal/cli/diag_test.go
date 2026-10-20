package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func diagSystemResult() map[string]any {
	return envelope("succeeded", map[string]any{"job_id": nil,
		"os":            map[string]any{"name": "Windows 11 Pro", "version": "25H2", "build": "26200.9278", "edition": "Professional"},
		"computer_name": "DEV-PC",
		"cpu":           map[string]any{"name": "AMD Ryzen 7 3800X 8-Core Processor", "cores": 8, "threads": 16, "usage_pct": 13.3},
		"memory":        map[string]any{"total_bytes": 34257387520, "used_bytes": 16575488000},
		"disks":         []any{map[string]any{"root": `C:\`, "fs": "NTFS", "total_bytes": 511097368576, "free_bytes": 104360009728}, map[string]any{"root": `D:\`, "label": "개발", "fs": "exFAT", "total_bytes": 1 << 40, "free_bytes": 1 << 39}},
		"gpus":          []any{map[string]any{"name": "NVIDIA GeForce RTX 2070", "driver_version": "32.0.16.1074", "memory_bytes": 8394899456}},
		"boot_time":     "2026-10-20T01:22:59.557Z", "uptime_s": 53703})
}

func TestDiagSystemPrintsTheSummary(t *testing.T) {
	f := startFake(t, map[string]handler{"diag.system": ok(diagSystemResult())})
	r := runCLI(t, false, "", "diag", "system", "win")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	for _, want := range []string{"DEV-PC", "Windows 11 Pro 25H2 (빌드 26200.9278, Professional)", "AMD Ryzen 7 3800X 8-Core Processor", "8코어 16스레드", "사용률 13.3%",
		"15.4 GiB / 31.9 GiB", "14시간 55분", `C:\`, "개발", "exFAT", "97.2 GiB", "NVIDIA GeForce RTX 2070", "32.0.16.1074", "7.8 GiB"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	if calls := f.called("diag.system"); len(calls) != 1 || calls[0]["host_id"] != "win" || calls[0]["save"] != nil {
		t.Fatalf("calls %v", calls)
	}
	r = runCLI(t, false, "", "--json", "diag", "system", "win", "--save")
	var out map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || r.code != 0 || out["computer_name"] != "DEV-PC" || out["schema_version"] != float64(1) {
		t.Fatalf("json %q %v", r.stdout, err)
	}
	if calls := f.called("diag.system"); calls[1]["save"] != true {
		t.Fatalf("save call %v", calls[1])
	}
}

func TestDiagSaveTellsWhereTheArtifactIs(t *testing.T) {
	res := diagSystemResult()
	res["job_id"], res["artifact_id"] = "j-9", "a-9"
	res["artifacts"] = []any{map[string]any{"artifact_id": "a-9", "path": "/tmp/dh/artifacts/j-9/diag-system.json", "name": "diag-system.json"}}
	startFake(t, map[string]handler{"diag.system": ok(res)})
	r := runCLI(t, false, "", "diag", "system", "win", "--save")
	if r.code != 0 || !strings.Contains(r.err, "/tmp/dh/artifacts/j-9/diag-system.json") || strings.Contains(r.stdout, "/tmp/dh") {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", r.code, r.stdout, r.err)
	}
}

func TestDiagServicesPrintsATable(t *testing.T) {
	f := startFake(t, map[string]handler{"diag.services": ok(envelope("succeeded", map[string]any{"services": []any{
		map[string]any{"name": "wuauserv", "display_name": "Windows Update", "state": "running", "start_type": "delayed_auto", "pid": 1200},
		map[string]any{"name": "BITS", "display_name": "Background Intelligent Transfer Service", "state": "stopped", "start_type": "manual"},
	}}))})
	r := runCLI(t, false, "", "diag", "services", "win", "--name", "update")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	for _, want := range []string{"이름", "표시 이름", "wuauserv", "Windows Update", "실행 중", "자동(지연된 시작)", "1200", "중지됨", "수동"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	if calls := f.called("diag.services"); calls[0]["name"] != "update" {
		t.Fatalf("calls %v", calls)
	}
}

func TestDiagEventsSendsTheFiltersAndSaysWhenItWasCut(t *testing.T) {
	f := startFake(t, map[string]handler{"diag.events": ok(envelope("succeeded", map[string]any{"truncated": true, "events": []any{
		map[string]any{"record_id": 26759, "time": "2026-10-20T15:29:11.077Z", "level": "error", "provider": "Service Control Manager", "event_id": 7031,
			"message": "Windows Update 서비스가 예기치 않게 종료되었습니다.\n이 작업은 1번 수행되었습니다."},
	}}))})
	before := time.Now()
	r := runCLI(t, false, "", "diag", "events", "win", "--log", "Application", "--since", "2h", "--level", "error,warning", "--provider", "Service Control Manager", "--max", "50")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	for _, want := range []string{"오류", "Service Control Manager", "7031", "Windows Update 서비스가 예기치 않게 종료되었습니다."} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "1번 수행") {
		t.Errorf("the table shows more than the first line:\n%s", r.stdout)
	}
	if !strings.Contains(r.err, "--max") {
		t.Errorf("no note on the cut list: %s", r.err)
	}
	call := f.called("diag.events")[0]
	since, err := time.Parse(time.RFC3339Nano, call["since"].(string))
	if err != nil || since.Before(before.Add(-2*time.Hour-time.Second)) || since.After(time.Now().Add(-2*time.Hour)) {
		t.Fatalf("since %v", call["since"])
	}
	if call["log"] != "Application" || call["provider"] != "Service Control Manager" || call["max"] != float64(50) || strings.Join(toStrings(call["levels"]), ",") != "error,warning" || call["until"] != nil {
		t.Fatalf("call %v", call)
	}
	r = runCLI(t, false, "", "diag", "events", "win", "--since", "2026-10-20T00:00:00Z", "--until", "3d")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	if call = f.called("diag.events")[1]; call["since"] != "2026-10-20T00:00:00Z" || call["until"] == nil {
		t.Fatalf("call %v", call)
	}
}

func toStrings(v any) []string {
	list, _ := v.([]any)
	out := []string{}
	for _, s := range list {
		out = append(out, s.(string))
	}
	return out
}

func TestDiagCrashesPrintsThePathsToPull(t *testing.T) {
	f := startFake(t, map[string]handler{"diag.crashes": ok(envelope("succeeded", map[string]any{"dumps": []any{
		map[string]any{"path": `C:\Users\me\AppData\Local\CrashDumps\Shop.exe.4013.dmp`, "app": "Shop.exe", "time": "2026-10-20T10:00:00.000Z", "size_bytes": 51100000, "kind": "minidump"},
		map[string]any{"path": `C:\ProgramData\Microsoft\Windows\WER\ReportArchive\AppCrash_Code.exe_1\Report.wer`, "app": "Code.exe", "time": "2026-10-18T10:00:00.000Z", "size_bytes": 41872, "kind": "report"},
	}}))})
	r := runCLI(t, false, "", "diag", "crashes", "win", "--since", "7d")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	for _, want := range []string{"Shop.exe", "미니 덤프", "48.7 MiB", `C:\Users\me\AppData\Local\CrashDumps\Shop.exe.4013.dmp`, "Code.exe", "오류 보고서"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	if !strings.Contains(r.err, "deskhand files pull win:") {
		t.Errorf("no pull hint: %s", r.err)
	}
	since, err := time.Parse(time.RFC3339Nano, f.called("diag.crashes")[0]["since"].(string))
	if err != nil || time.Since(since) < 7*24*time.Hour-time.Minute || time.Since(since) > 7*24*time.Hour+time.Minute {
		t.Fatalf("since %v", since)
	}
}

func TestDiagEmptyListsSaySo(t *testing.T) {
	startFake(t, map[string]handler{
		"diag.services": ok(envelope("succeeded", map[string]any{"services": []any{}})),
		"diag.events":   ok(envelope("succeeded", map[string]any{"events": []any{}, "truncated": false})),
		"diag.crashes":  ok(envelope("succeeded", map[string]any{"dumps": []any{}})),
	})
	for sub, want := range map[string]string{"services": "일치하는 서비스가 없습니다", "events": "조건에 맞는 이벤트가 없습니다", "crashes": "크래시 기록이 없습니다"} {
		if r := runCLI(t, false, "", "diag", sub, "win"); r.code != 0 || !strings.Contains(r.stdout, want) {
			t.Errorf("%s: exit %d %q %q", sub, r.code, r.stdout, r.err)
		}
	}
}

func TestDiagFailuresAndUsage(t *testing.T) {
	startFake(t, map[string]handler{"diag.system": ok(envelope("failed", map[string]any{"job_id": nil,
		"error": map[string]any{"code": "CAPABILITY_UNSUPPORTED", "message": "이 PC에서는 진단 정보를 읽을 수 없습니다", "retryable": false}}))})
	if r := runCLI(t, false, "", "diag", "system", "win"); r.code != 1 || !strings.Contains(r.err, "이 PC에서는 진단 정보를 읽을 수 없습니다") {
		t.Fatalf("failed envelope: exit %d %q", r.code, r.err)
	}
	for _, args := range [][]string{{"diag"}, {"diag", "logs", "win"}, {"diag", "system"}, {"diag", "events", "win", "--since", "soon"}, {"diag", "events", "win", "--max", "x"}, {"diag", "crashes", "win", "--since", "-1h"}} {
		if r := runCLI(t, false, "", args...); r.code != 2 {
			t.Errorf("%v: exit %d %q", args, r.code, r.err)
		}
	}
}

func TestProcListShowsTheCPUAndStartAndKillPassesStarted(t *testing.T) {
	f := startFake(t, map[string]handler{
		"proc.list": ok(map[string]any{"host_id": "win", "procs": []any{
			map[string]any{"pid": 4321, "ppid": 1, "name": "node.exe", "mem_kb": 2048, "cpu_pct": 12.5, "user": `DEV-PC\me`, "started": "2026-10-20T01:00:00.000Z", "started_by_deskhand": true, "self": false},
			map[string]any{"pid": 900, "ppid": 1, "name": "deskhand-agentw.exe", "mem_kb": 1024, "cpu_pct": 0.0, "started": "2026-10-20T00:00:00.000Z", "started_by_deskhand": false, "self": true},
		}}),
		"proc.kill": ok(envelope("succeeded", map[string]any{"killed": 1})),
	})
	r := runCLI(t, false, "", "proc", "ls", "win")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	for _, want := range []string{"CPU", "12.5%", "시작", "node.exe", "에이전트"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	if r := runCLI(t, false, "", "proc", "kill", "win", "4321", "--started", "2026-10-20T01:00:00.000Z"); r.code != 0 {
		t.Fatalf("kill exit %d: %s", r.code, r.err)
	}
	if call := f.called("proc.kill")[0]; call["started"] != "2026-10-20T01:00:00.000Z" || call["pid"] != float64(4321) {
		t.Fatalf("kill call %v", call)
	}
}
