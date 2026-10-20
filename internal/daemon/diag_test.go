package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/diag"
	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// fakeDiag is the diagnostics of the test agent; it records the requests.
type fakeDiag struct {
	mu     sync.Mutex
	filter string
	query  wire.DiagEventsQuery
	since  time.Time
}

func (f *fakeDiag) System(context.Context) (diag.System, error) {
	return diag.System{OS: diag.OS{Name: "Windows 11 Pro", Version: "25H2", Build: "26200.9278", Edition: "Professional"}, ComputerName: "DEV-PC",
		CPU: diag.CPU{Name: "CPU", Cores: 8, Threads: 16, UsagePct: 12.5}, Memory: diag.Memory{TotalBytes: 34257387520, UsedBytes: 16575488000},
		Disks: []diag.Disk{{Root: `C:\`, FS: "NTFS", TotalBytes: 511097368576, FreeBytes: 104360009728}}, GPUs: []diag.GPU{{Name: "NVIDIA GeForce RTX 2070", DriverVersion: "32.0.16.1074", MemoryBytes: 8394899456}},
		BootTime: "2026-10-20T01:22:59.557Z", UptimeS: 53703}, nil
}

func (f *fakeDiag) Services(_ context.Context, filter string) ([]diag.Service, error) {
	f.mu.Lock()
	f.filter = filter
	f.mu.Unlock()
	return []diag.Service{{Name: "EventLog", DisplayName: "Windows Event Log", State: "running", StartType: "auto", PID: 2000}}, nil
}

func (f *fakeDiag) Events(_ context.Context, q wire.DiagEventsQuery) (diag.Events, error) {
	f.mu.Lock()
	f.query = q
	f.mu.Unlock()
	return diag.Events{Events: []diag.Event{{RecordID: 26759, Time: "2026-10-20T15:29:11.077Z", Level: "warning", Provider: "Microsoft-Windows-Time-Service", EventID: 36, Message: "시간 서비스"}}}, nil
}

func (f *fakeDiag) Crashes(_ context.Context, since time.Time) ([]diag.Dump, error) {
	f.mu.Lock()
	f.since = since
	f.mu.Unlock()
	return []diag.Dump{{Path: `C:\Users\me\AppData\Local\CrashDumps\Shop.exe.4013.dmp`, App: "Shop.exe", Time: "2026-10-20T10:00:00.000Z", SizeBytes: 51100000, Kind: "minidump"}}, nil
}

func (f *fakeDiag) seen() (string, wire.DiagEventsQuery, time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.filter, f.query, f.since
}

func TestDiagMethodsCarryTheAgentResultInTheEnvelope(t *testing.T) {
	src := &fakeDiag{}
	e := setup(t, setupOpts{trustDaemon: true, agentDiag: src})
	e.waitState(t, "ready")
	sys := e.call(t, "diag.system", map[string]any{"host_id": "pc"})
	checkEnvelope(t, sys)
	if sys["status"] != "succeeded" || sys["job_id"] != nil || sys["computer_name"] != "DEV-PC" || sys["uptime_s"] != float64(53703) ||
		sys["boot_time"] != "2026-10-20T01:22:59.557Z" || sys["artifact_id"] != nil {
		t.Fatalf("diag.system %v", sys)
	}
	if cpu := sys["cpu"].(map[string]any); cpu["usage_pct"] != 12.5 || cpu["threads"] != float64(16) {
		t.Fatalf("cpu %v", cpu)
	}
	if gpus := sys["gpus"].([]any); len(gpus) != 1 || gpus[0].(map[string]any)["driver_version"] != "32.0.16.1074" {
		t.Fatalf("gpus %v", gpus)
	}

	svc := e.call(t, "diag.services", map[string]any{"host_id": "pc", "name": "event"})
	if filter, _, _ := src.seen(); filter != "event" || svc["status"] != "succeeded" || len(svc["services"].([]any)) != 1 {
		t.Fatalf("diag.services %v (filter %q)", svc, filter)
	}

	ev := e.call(t, "diag.events", map[string]any{"host_id": "pc", "log": "Application", "since": "2026-10-20T00:00:00Z", "until": "2026-10-21T00:00:00.000Z",
		"levels": []string{"error", "warning"}, "provider": "Microsoft-Windows-Time-Service", "max": 50})
	_, q, _ := src.seen()
	if q.Log != "Application" || q.Max != 50 || strings.Join(q.Levels, ",") != "error,warning" || q.Provider != "Microsoft-Windows-Time-Service" ||
		!q.Since.Equal(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)) || !q.Until.Equal(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("agent query %+v", q)
	}
	events := ev["events"].([]any)
	if ev["status"] != "succeeded" || ev["truncated"] != false || len(events) != 1 || events[0].(map[string]any)["record_id"] != float64(26759) {
		t.Fatalf("diag.events %v", ev)
	}
	e.call(t, "diag.events", map[string]any{"host_id": "pc"})
	if _, q, _ = src.seen(); q.Log != "System" || q.Max != 200 || time.Since(q.Since) < 24*time.Hour-time.Minute {
		t.Fatalf("default agent query %+v", q)
	}

	cr := e.call(t, "diag.crashes", map[string]any{"host_id": "pc", "since": "2026-10-01T00:00:00Z"})
	if _, _, since := src.seen(); !since.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || cr["status"] != "succeeded" || len(cr["dumps"].([]any)) != 1 {
		t.Fatalf("diag.crashes %v (since %s)", cr, since)
	}
}

func TestDiagSaveKeepsTheResultAsAJobArtifact(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true, agentDiag: &fakeDiag{}})
	e.waitState(t, "ready")
	for _, c := range []struct {
		method, name, key string
	}{
		{"diag.system", "diag-system.json", "computer_name"},
		{"diag.services", "diag-services.json", "services"},
		{"diag.events", "diag-events.json", "events"},
		{"diag.crashes", "diag-crashes.json", "dumps"},
	} {
		res := e.call(t, c.method, map[string]any{"host_id": "pc", "save": true})
		checkEnvelope(t, res)
		arts := res["artifacts"].([]any)
		jobID, _ := res["job_id"].(string)
		if res["status"] != "succeeded" || len(arts) != 1 || !strings.HasPrefix(jobID, "j-") {
			t.Fatalf("%s save %v", c.method, res)
		}
		art := arts[0].(map[string]any)
		if art["artifact_id"] != res["artifact_id"] || art["job_id"] != jobID || art["kind"] != "report" || art["mime"] != "application/json" || art["name"] != c.name {
			t.Fatalf("%s artifact %v (envelope artifact_id %v)", c.method, art, res["artifact_id"])
		}
		b, err := os.ReadFile(art["path"].(string))
		if err != nil {
			t.Fatal(err)
		}
		var saved map[string]any
		if err := json.Unmarshal(b, &saved); err != nil || saved["host_id"] != "pc" || saved["method"] != c.method || saved["saved_at"] == nil {
			t.Fatalf("%s saved %s (%v)", c.method, b, err)
		}
		if got, _ := json.Marshal(saved["result"].(map[string]any)[c.key]); string(got) != mustJSON(t, res[c.key]) {
			t.Fatalf("%s saved %s, answered %s", c.method, got, mustJSON(t, res[c.key]))
		}
		job := e.call(t, "jobs.get", map[string]any{"job_id": jobID})
		if job["kind"] != "diag" || job["status"] != "succeeded" || job["finished_at"] == nil {
			t.Fatalf("%s job %v", c.method, job)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDiagParamsAreRefusedBeforeTheCall(t *testing.T) {
	src := &fakeDiag{}
	e := setup(t, setupOpts{trustDaemon: true, agentDiag: src})
	e.waitState(t, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, c := range []struct {
		method string
		params map[string]any
	}{
		{"diag.system", map[string]any{}},
		{"diag.events", map[string]any{"host_id": "pc", "log": "Security"}},
		{"diag.events", map[string]any{"host_id": "pc", "since": "yesterday"}},
		{"diag.events", map[string]any{"host_id": "pc", "since": "2026-10-21T00:00:00Z", "until": "2026-10-20T00:00:00Z"}},
		{"diag.events", map[string]any{"host_id": "pc", "levels": []string{"fatal"}}},
		{"diag.events", map[string]any{"host_id": "pc", "max": 0}},
		{"diag.events", map[string]any{"host_id": "pc", "max": 1001}},
		{"diag.events", map[string]any{"host_id": "pc", "max": 2.5}},
		{"diag.crashes", map[string]any{"host_id": "pc", "since": "last week"}},
		{"proc.kill", map[string]any{"host_id": "pc", "pid": 5, "started": "yesterday"}},
		{"proc.kill", map[string]any{"host_id": "pc", "name": "x.exe", "started": "2026-10-20T00:00:00.000Z"}},
	} {
		var cerr *client.Error
		if err := e.c.Call(ctx, c.method, c.params, nil); !errors.As(err, &cerr) || cerr.Code != "INVALID_ARGUMENT" || cerr.Message == "" {
			t.Errorf("%s %v: %v", c.method, c.params, err)
		}
	}
	if filter, q, since := src.seen(); filter != "" || !q.Since.IsZero() || !since.IsZero() {
		t.Fatalf("the agent was asked %q %+v %v", filter, q, since)
	}
}

func TestDiagOfAnAgentWithoutDiagnosticsFailsInTheEnvelope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test agent reads Windows diagnostics")
	}
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	res := e.call(t, "diag.system", map[string]any{"host_id": "pc"})
	checkEnvelope(t, res)
	er, _ := res["error"].(map[string]any)
	if res["status"] != "failed" || er["code"] != "CAPABILITY_UNSUPPORTED" || res["next_action"] == "host_repair" || res["computer_name"] != nil {
		t.Fatalf("envelope %v", res)
	}
}

func TestProcListCarriesTheAgentFields(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	start := e.call(t, "exec.start", map[string]any{"host_id": "pc", "argv": []string{"sleep", "30"}})
	jobID := start["job_id"].(string)
	pid := int(start["pid"].(float64))
	t.Cleanup(func() { _ = e.c.Call(context.Background(), "jobs.cancel", map[string]any{"job_id": jobID}, nil) })
	procs := e.call(t, "proc.list", map[string]any{"host_id": "pc"})["procs"].([]any)
	var self, job map[string]any
	for _, p := range procs {
		m := p.(map[string]any)
		if _, ok := m["cpu_pct"].(float64); !ok {
			t.Fatalf("no cpu_pct in %v", m)
		}
		switch int(m["pid"].(float64)) {
		case os.Getpid():
			self = m
		case pid:
			job = m
		}
	}
	if self == nil || self["self"] != true || self["started_by_deskhand"] != false || self["started"] == nil || self["user"] == nil {
		t.Fatalf("agent %v", self)
	}
	if job == nil || job["started_by_deskhand"] != true || job["self"] != false {
		t.Fatalf("job process %v", job)
	}

	kill := e.call(t, "proc.kill", map[string]any{"host_id": "pc", "pid": pid, "started": "2001-01-01T00:00:00.000Z"})
	checkEnvelope(t, kill)
	if er, _ := kill["error"].(map[string]any); kill["status"] != "failed" || er["code"] != "STALE_TARGET" {
		t.Fatalf("stale kill %v", kill)
	}
	kill = e.call(t, "proc.kill", map[string]any{"host_id": "pc", "pid": pid, "started": job["started"]})
	if kill["status"] != "succeeded" || kill["killed"] != float64(1) {
		t.Fatalf("kill %v", kill)
	}
	self2 := e.call(t, "proc.kill", map[string]any{"host_id": "pc", "pid": os.Getpid()})
	if er, _ := self2["error"].(map[string]any); self2["status"] != "failed" || er["code"] != "PERMISSION_DENIED" {
		t.Fatalf("kill of the agent %v", self2)
	}
}
