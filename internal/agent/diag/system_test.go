package diag

import (
	"encoding/json"
	"testing"
)

func TestTheProductNameOfAWindows11BuildSaysWindows11(t *testing.T) {
	cases := []struct {
		name  string
		build uint32
		want  string
	}{
		{"Windows 10 Pro", 26200, "Windows 11 Pro"},
		{"Windows 10 Home", 22000, "Windows 11 Home"},
		{"Windows 10 Pro", 19045, "Windows 10 Pro"},
		{"Windows Server 2022 Standard", 20348, "Windows Server 2022 Standard"},
		{"Windows Server 2025 Datacenter", 26100, "Windows Server 2025 Datacenter"},
		{"", 26100, "Windows"},
	}
	for _, c := range cases {
		if got := productName(c.name, c.build); got != c.want {
			t.Errorf("%q build %d: %q, expected %q", c.name, c.build, got, c.want)
		}
	}
}

func TestTheBuildCarriesTheUpdateRevisionWhenKnown(t *testing.T) {
	if got := osBuild(26200, 9278, true); got != "26200.9278" {
		t.Fatalf("build %q", got)
	}
	if got := osBuild(26200, 0, false); got != "26200" {
		t.Fatalf("build without a revision %q", got)
	}
}

func TestCPUUsageIsTheBusyShareOfTheTimeBetweenTwoSamples(t *testing.T) {
	first := cpuSample{idle: 1000, kernel: 1500, user: 500}
	second := cpuSample{idle: 1600, kernel: 2300, user: 900}
	if got := cpuUsage(first, second); got != 50 {
		t.Fatalf("usage %v", got)
	}
	if got := cpuUsage(first, first); got != 0 {
		t.Fatalf("usage without elapsed time %v", got)
	}
	if got := cpuUsage(second, cpuSample{idle: 1600, kernel: 2400, user: 900}); got != 100 {
		t.Fatalf("usage of a busy interval %v", got)
	}
}

func TestTheUserModeDriverVersionReadsAsFourParts(t *testing.T) {
	umd := int64(32)<<48 | int64(0)<<32 | int64(15)<<16 | 6636
	if got := driverVersion(umd); got != "32.0.15.6636" {
		t.Fatalf("driver version %q", got)
	}
}

func TestTheSystemSummaryHasTheProtocolFields(t *testing.T) {
	s := System{OS: OS{Name: "Windows 11 Pro", Version: "25H2", Build: "26200.9278", Edition: "Professional"}, ComputerName: "DEV-PC",
		CPU: CPU{Name: "CPU", Cores: 8, Threads: 16, UsagePct: 3.5}, Memory: Memory{TotalBytes: 32, UsedBytes: 16},
		Disks: []Disk{{Root: `C:\`, FS: "NTFS", TotalBytes: 10, FreeBytes: 5}}, GPUs: []GPU{{Name: "GPU"}},
		BootTime: "2026-10-20T00:00:00.000Z", UptimeS: 60}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"os":{"name":"Windows 11 Pro","version":"25H2","build":"26200.9278","edition":"Professional"},"computer_name":"DEV-PC",` +
		`"cpu":{"name":"CPU","cores":8,"threads":16,"usage_pct":3.5},"memory":{"total_bytes":32,"used_bytes":16},` +
		`"disks":[{"root":"C:\\","fs":"NTFS","total_bytes":10,"free_bytes":5}],"gpus":[{"name":"GPU"}],"boot_time":"2026-10-20T00:00:00.000Z","uptime_s":60}`
	if string(b) != want {
		t.Fatalf("json\n%s\nexpected\n%s", b, want)
	}
}
