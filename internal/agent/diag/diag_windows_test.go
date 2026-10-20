//go:build windows

package diag

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || !strings.HasSuffix(s, "Z") || len(s) != len("2006-01-02T15:04:05.000Z") {
		t.Fatalf("time %q is not RFC 3339 UTC with milliseconds (%v)", s, err)
	}
	return at
}

func TestDiagSystemFields(t *testing.T) {
	began := time.Now()
	s, err := Local().System(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took < SampleGap {
		t.Errorf("the CPU usage was sampled over %s, less than %s", took, SampleGap)
	}
	t.Logf("system %+v", s)
	host, _ := os.Hostname()
	if !strings.HasPrefix(s.OS.Name, "Windows") || s.OS.Build == "" || s.OS.Edition == "" || s.OS.Version == "" || s.ComputerName != host {
		t.Errorf("os %+v computer %q", s.OS, s.ComputerName)
	}
	if !strings.Contains(s.OS.Build, ".") {
		t.Errorf("build %q lacks the update revision", s.OS.Build)
	}
	if s.CPU.Name == "" || s.CPU.Cores < 1 || s.CPU.Threads < s.CPU.Cores || s.CPU.UsagePct < 0 || s.CPU.UsagePct > 100 {
		t.Errorf("cpu %+v", s.CPU)
	}
	if s.Memory.TotalBytes < 1<<30 || s.Memory.UsedBytes == 0 || s.Memory.UsedBytes > s.Memory.TotalBytes {
		t.Errorf("memory %+v", s.Memory)
	}
	system := os.Getenv("SystemDrive") + `\`
	found := false
	for _, d := range s.Disks {
		if d.Root == "" || d.FS == "" || d.TotalBytes == 0 || d.FreeBytes > d.TotalBytes {
			t.Errorf("disk %+v", d)
		}
		found = found || strings.EqualFold(d.Root, system)
	}
	if !found {
		t.Errorf("the system drive %s is not among %+v", system, s.Disks)
	}
	for _, g := range s.GPUs {
		if g.Name == "" || strings.Contains(g.Name, "Basic Render") {
			t.Errorf("gpu %+v", g)
		}
	}
	boot := parseTime(t, s.BootTime)
	if s.UptimeS <= 0 || absDuration(time.Since(boot)-time.Duration(s.UptimeS)*time.Second) > 5*time.Second {
		t.Errorf("boot %s uptime %d s", s.BootTime, s.UptimeS)
	}
}

func absDuration(x time.Duration) time.Duration {
	if x < 0 {
		return -x
	}
	return x
}

func checkEvents(t *testing.T, res Events, q wire.DiagEventsQuery) {
	t.Helper()
	if len(res.Events) > q.Max {
		t.Fatalf("%d events above max %d", len(res.Events), q.Max)
	}
	var last time.Time
	for i, e := range res.Events {
		at := parseTime(t, e.Time)
		if at.Before(q.Since.Truncate(time.Millisecond)) || (!q.Until.IsZero() && at.After(q.Until)) {
			t.Errorf("event %d at %s is outside %s–%s", e.RecordID, e.Time, q.Since, q.Until)
		}
		if i > 0 && at.After(last.Add(time.Second)) {
			t.Errorf("event %d at %s is newer than the one before it at %s", e.RecordID, e.Time, last)
		}
		last = at
		if len(q.Levels) > 0 && !slices.Contains(q.Levels, e.Level) {
			t.Errorf("event %d has level %s outside %v", e.RecordID, e.Level, q.Levels)
		}
		if !slices.Contains(wire.DiagEventLevels, e.Level) || e.Provider == "" || e.RecordID == 0 {
			t.Errorf("event %+v", e)
		}
		if q.Provider != "" && e.Provider != q.Provider {
			t.Errorf("event %d of %s, expected %s", e.RecordID, e.Provider, q.Provider)
		}
	}
}

func TestEventsFilter(t *testing.T) {
	now := time.Now()
	errors := wire.DiagEventsQuery{Log: "System", Since: now.Add(-time.Hour).UTC(), Levels: []string{"error"}, Max: 200}
	res, err := Local().Events(context.Background(), errors)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d System errors in the last hour", len(res.Events))
	checkEvents(t, res, errors)

	recent := wire.DiagEventsQuery{Log: "System", Since: now.AddDate(0, 0, -7).UTC(), Max: 5}
	res, err = Local().Events(context.Background(), recent)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, res, recent)
	if len(res.Events) == 0 {
		t.Fatal("the System log has no record in the last 7 days")
	}
	wider := recent
	wider.Max = 6
	more, err := Local().Events(context.Background(), wider)
	if err != nil {
		t.Fatal(err)
	}
	if res.Truncated != (len(more.Events) > 5) || more.Events[0].RecordID != res.Events[0].RecordID {
		t.Errorf("%d events, truncated %v; with max 6 %d events", len(res.Events), res.Truncated, len(more.Events))
	}
	withMessage := 0
	for _, e := range res.Events {
		if e.Message != "" {
			withMessage++
		}
	}
	if withMessage == 0 {
		t.Errorf("none of %+v has a message", res.Events)
	}

	first := res.Events[0]
	one := wire.DiagEventsQuery{Log: "System", Since: parseTime(t, first.Time).Add(-time.Minute), Until: parseTime(t, first.Time).Add(time.Second),
		Provider: first.Provider, Levels: []string{first.Level}, Max: 1000}
	res, err = Local().Events(context.Background(), one)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, res, one)
	if !slices.ContainsFunc(res.Events, func(e Event) bool { return e.RecordID == first.RecordID }) {
		t.Errorf("record %d of %s is not found by its provider, level and time", first.RecordID, first.Provider)
	}

	app := wire.DiagEventsQuery{Log: "Application", Since: now.AddDate(0, 0, -1).UTC(), Max: 3}
	res, err = Local().Events(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, res, app)
}

func TestServicesShape(t *testing.T) {
	all, err := Local().Services(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 20 {
		t.Fatalf("only %d services", len(all))
	}
	states := []string{"running", "stopped", "paused", "start_pending", "stop_pending", "unknown"}
	starts := []string{"auto", "delayed_auto", "manual", "disabled", "unknown"}
	seen := map[string]int{}
	for i, s := range all {
		if s.Name == "" || !slices.Contains(states, s.State) || !slices.Contains(starts, s.StartType) || (s.State == "running") != (s.PID > 0) {
			t.Errorf("service %+v", s)
		}
		if i > 0 && strings.ToLower(all[i-1].Name) > strings.ToLower(s.Name) {
			t.Errorf("%s is listed after %s", s.Name, all[i-1].Name)
		}
		seen[s.StartType]++
	}
	t.Logf("%d services, start types %v", len(all), seen)
	if seen["delayed_auto"] == 0 || seen["auto"] == 0 || seen["manual"] == 0 {
		t.Errorf("start types %v lack delayed_auto, auto or manual", seen)
	}
	rpc, err := Local().Services(context.Background(), "RPCSS")
	if err != nil || !slices.ContainsFunc(rpc, func(s Service) bool { return s.Name == "RpcSs" && s.State == "running" && s.StartType == "auto" }) {
		t.Fatalf("RpcSs %+v %v", rpc, err)
	}
	for _, s := range rpc {
		if !strings.Contains(strings.ToLower(s.Name+s.DisplayName), "rpcss") {
			t.Errorf("%+v does not match rpcss", s)
		}
	}
}

func TestCrashesShape(t *testing.T) {
	since := time.Now().AddDate(-2, 0, 0)
	dumps, err := Local().Crashes(context.Background(), since)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d crashes since %s", len(dumps), since.Format(time.DateOnly))
	var last time.Time
	for i, c := range dumps {
		at := parseTime(t, c.Time)
		if at.Before(since) || c.App == "" || !slices.Contains([]string{KindMinidump, KindFull, KindReport}, c.Kind) {
			t.Errorf("crash %+v", c)
		}
		if i > 0 && at.After(last) {
			t.Errorf("%s at %s is newer than the one before it", c.Path, c.Time)
		}
		last = at
		fi, err := os.Stat(c.Path)
		if err != nil || fi.Size() != c.SizeBytes {
			t.Errorf("%s: %v, size %d", c.Path, err, c.SizeBytes)
		}
		if c.Kind == KindReport && !strings.EqualFold(fileName(c.Path), "Report.wer") {
			t.Errorf("report %s is not a Report.wer", c.Path)
		}
	}
	recent, err := Local().Crashes(context.Background(), time.Now().Add(time.Hour))
	if err != nil || len(recent) != 0 {
		t.Fatalf("crashes after now: %+v %v", recent, err)
	}
}

func fileName(p string) string { return p[strings.LastIndexByte(p, '\\')+1:] }
