package server

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/diag"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// fakeDiag is a diag.Source that records the requests it gets.
type fakeDiag struct {
	mu       sync.Mutex
	filter   string
	query    wire.DiagEventsQuery
	since    time.Time
	failWith error
}

func (f *fakeDiag) System(context.Context) (diag.System, error) {
	if f.failWith != nil {
		return diag.System{}, f.failWith
	}
	return diag.System{OS: diag.OS{Name: "Windows 11 Pro", Version: "25H2", Build: "26200.9278", Edition: "Professional"}, ComputerName: "DEV-PC",
		CPU: diag.CPU{Name: "CPU", Cores: 8, Threads: 16, UsagePct: 12.5}, Memory: diag.Memory{TotalBytes: 32 << 30, UsedBytes: 8 << 30},
		Disks: []diag.Disk{{Root: `C:\`, Label: "Windows", FS: "NTFS", TotalBytes: 100, FreeBytes: 40}}, GPUs: []diag.GPU{{Name: "GPU", DriverVersion: "1.2.3.4", MemoryBytes: 8 << 30}},
		BootTime: "2026-10-20T01:00:00.000Z", UptimeS: 3600}, nil
}

func (f *fakeDiag) Services(_ context.Context, filter string) ([]diag.Service, error) {
	f.mu.Lock()
	f.filter = filter
	f.mu.Unlock()
	return []diag.Service{{Name: "wuauserv", DisplayName: "Windows Update", State: "running", StartType: "delayed_auto", PID: 1200}}, nil
}

func (f *fakeDiag) Events(_ context.Context, q wire.DiagEventsQuery) (diag.Events, error) {
	f.mu.Lock()
	f.query = q
	f.mu.Unlock()
	return diag.Events{Events: []diag.Event{{RecordID: 7, Time: "2026-10-20T15:29:11.077Z", Level: "warning", Provider: "p", EventID: 36, Message: "m"}}, Truncated: true}, nil
}

func (f *fakeDiag) Crashes(_ context.Context, since time.Time) ([]diag.Dump, error) {
	f.mu.Lock()
	f.since = since
	f.mu.Unlock()
	return []diag.Dump{{Path: `C:\d\Shop.exe.1.dmp`, App: "Shop.exe", Time: "2026-10-20T10:00:00.000Z", SizeBytes: 96, Kind: "minidump"}}, nil
}

// seen returns the filter, query and since the source got last.
func (f *fakeDiag) seen() (string, wire.DiagEventsQuery, time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.filter, f.query, f.since
}

// asJSON encodes v with the keys of its objects sorted, for a comparison.
func asJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return jsonText(t, string(raw))
}

// jsonText returns the JSON text s with the keys of its objects sorted.
func jsonText(t *testing.T, s string) string {
	t.Helper()
	raw := []byte(s)
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestDiagOpsAnswerTheFieldsOfTheSource(t *testing.T) {
	src := &fakeDiag{}
	c, _ := newRPCClient(t, Options{Diag: src})
	sys := c.ok("diag.system", nil)
	want, _ := src.System(context.Background())
	for k, v := range map[string]any{"os": want.OS, "computer_name": want.ComputerName, "cpu": want.CPU, "memory": want.Memory, "disks": want.Disks,
		"gpus": want.GPUs, "boot_time": want.BootTime, "uptime_s": want.UptimeS} {
		if asJSON(t, sys[k]) != asJSON(t, v) {
			t.Errorf("diag.system %s = %s, expected %s", k, asJSON(t, sys[k]), asJSON(t, v))
		}
	}

	svc := c.ok("diag.services", proto.Message{"name": "update"})
	if filter, _, _ := src.seen(); filter != "update" || asJSON(t, svc["services"]) != jsonText(t, `[{"name":"wuauserv","display_name":"Windows Update","state":"running","start_type":"delayed_auto","pid":1200}]`) {
		t.Fatalf("diag.services %v", svc)
	}

	before := time.Now()
	ev := c.ok("diag.events", nil)
	if ev["truncated"] != true || asJSON(t, ev["events"]) != jsonText(t, `[{"record_id":7,"time":"2026-10-20T15:29:11.077Z","level":"warning","provider":"p","event_id":36,"message":"m"}]`) {
		t.Fatalf("diag.events %v", ev)
	}
	_, q, _ := src.seen()
	if q.Log != "System" || q.Max != 200 || q.Levels != nil || q.Since.Before(before.Add(-24*time.Hour-time.Second)) || q.Since.After(time.Now().Add(-24*time.Hour)) {
		t.Fatalf("default query %+v", q)
	}
	c.ok("diag.events", proto.Message{"log": "application", "since": "2026-10-20T00:00:00Z", "until": "2026-10-21T00:00:00Z",
		"levels": []any{"error", "critical"}, "provider": "Service Control Manager", "max": 1000})
	_, q, _ = src.seen()
	if q.Log != "Application" || q.Max != 1000 || asJSON(t, q.Levels) != `["critical","error"]` || q.Provider != "Service Control Manager" ||
		!q.Since.Equal(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)) || !q.Until.Equal(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("query %+v", q)
	}

	cr := c.ok("diag.crashes", nil)
	if asJSON(t, cr["dumps"]) != jsonText(t, `[{"path":"C:\\d\\Shop.exe.1.dmp","app":"Shop.exe","time":"2026-10-20T10:00:00.000Z","size_bytes":96,"kind":"minidump"}]`) {
		t.Fatalf("diag.crashes %v", cr)
	}
	if _, _, since := src.seen(); time.Since(since) < 30*24*time.Hour || time.Since(since) > 30*24*time.Hour+time.Minute {
		t.Fatalf("default since %s", since)
	}
	c.ok("diag.crashes", proto.Message{"since": "2026-10-01T09:00:00+09:00"})
	if _, _, since := src.seen(); !since.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("since %s", since)
	}
}

func TestDiagParamsAreCheckedBeforeTheSourceReadsThem(t *testing.T) {
	src := &fakeDiag{}
	c, _ := newRPCClient(t, Options{Diag: src})
	for _, bad := range []struct {
		op     string
		fields proto.Message
	}{
		{"diag.events", proto.Message{"log": "Security"}},
		{"diag.events", proto.Message{"since": "yesterday"}},
		{"diag.events", proto.Message{"since": "2026-10-21T00:00:00Z", "until": "2026-10-20T00:00:00Z"}},
		{"diag.events", proto.Message{"levels": []any{"error", 3}}},
		{"diag.events", proto.Message{"levels": "error"}},
		{"diag.events", proto.Message{"levels": []any{"fatal"}}},
		{"diag.events", proto.Message{"max": 0}},
		{"diag.events", proto.Message{"max": 1001}},
		{"diag.events", proto.Message{"max": 2.5}},
		{"diag.events", proto.Message{"max": "10"}},
		{"diag.events", proto.Message{"provider": 3}},
		{"diag.services", proto.Message{"name": 3}},
		{"diag.crashes", proto.Message{"since": "last week"}},
	} {
		res := c.call(bad.op, bad.fields)
		if res["ok"] != false || res.String("code") != wire.CodeInvalidArgument || res.String("error") == "" {
			t.Errorf("%s %v: %v", bad.op, bad.fields, res)
		}
	}
	if filter, q, since := src.seen(); filter != "" || !q.Since.IsZero() || !since.IsZero() {
		t.Fatalf("the source was asked %q %+v %v", filter, q, since)
	}
}

func TestDiagSourceFailuresKeepTheirMeaning(t *testing.T) {
	c, _ := newRPCClient(t, Options{Diag: &fakeDiag{failWith: diag.ErrUnsupported}})
	if res := c.call("diag.system", nil); res.String("code") != wire.CodeCapabilityUnsupported || res["retryable"] != false {
		t.Fatalf("unsupported %v", res)
	}
	c, _ = newRPCClient(t, Options{Diag: &fakeDiag{failWith: &diag.InvalidError{Text: "bad"}}})
	if res := c.call("diag.system", nil); res.String("code") != wire.CodeInvalidArgument || res.String("error") != "bad" {
		t.Fatalf("invalid %v", res)
	}
}

func TestTheDiagCapabilityFollowsTheSource(t *testing.T) {
	_, r, _, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), AllowExec: true, Diag: &fakeDiag{}})
	if !capSet(expect(t, r, "hello"))["diag"] {
		t.Fatal("diag is not ready with a source")
	}
	_, r, _, _ = startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), AllowExec: true})
	caps, _ := expect(t, r, "hello")["caps"].(map[string]any)
	st, _ := caps["diag"].(map[string]any)
	if runtime.GOOS == "windows" {
		if st["state"] != "ready" {
			t.Fatalf("caps.diag %v", st)
		}
	} else if st["state"] != "unsupported" || st["reason"] != diag.ErrUnsupported.Error() {
		t.Fatalf("caps.diag %v", st)
	}
	_, r, _, _ = startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), Diag: &fakeDiag{}})
	caps, _ = expect(t, r, "hello")["caps"].(map[string]any)
	if st, _ := caps["diag"].(map[string]any); st["state"] != "unavailable" || st["reason"] != errExecDisabled {
		t.Fatalf("caps.diag without exec %v", st)
	}
}
