//go:build !windows

package server

import (
	"reflect"
	"testing"
	"time"
)

// psSample is `ps -axo pid=,ppid=,rss=,time=,user=,lstart=,args=` of an
// Ubuntu 26.04 desktop.
const psSample = `      1       0 13312 00:00:04 root     Tue Oct 20 09:12:01 2026 /sbin/init splash
      2       0     0 00:00:00 root     Tue Oct 20 09:12:01 2026 [kthreadd]
   2420       1  8704 00:00:00 root     Tue Oct 20 09:12:05 2026 sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups
   3216    1990 61440 01:02:03 me       Wed Oct 21 10:00:00 2026 /usr/bin/gnome-text-editor
   3300    1990  4096 1-00:00:05 me     Thu Oct  1 08:30:00 2026 python3 /opt/tool/server.py --port 8080
`

func TestProcessNamesComeFromTheKernelName(t *testing.T) {
	comms := map[int]string{1: "systemd", 2: "kthreadd", 2420: "sshd", 3216: "gnome-text-edit", 3300: "python3"}
	got := parseProcesses(psSample, func(pid int) (string, bool) {
		c, ok := comms[pid]
		return c, ok
	}, time.UTC)
	boot := time.Date(2026, 10, 20, 9, 12, 1, 0, time.UTC)
	want := []procInfo{
		{PID: 1, PPID: 0, MemKB: 13312, Name: "systemd", Path: "/sbin/init", Cmd: "/sbin/init splash", User: "root", Started: boot, CPUTime: 4 * time.Second},
		{PID: 2, PPID: 0, MemKB: 0, Name: "kthreadd", Cmd: "[kthreadd]", User: "root", Started: boot},
		{PID: 2420, PPID: 1, MemKB: 8704, Name: "sshd", Cmd: "sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups", User: "root", Started: boot.Add(4 * time.Second)},
		{PID: 3216, PPID: 1990, MemKB: 61440, Name: "gnome-text-editor", Path: "/usr/bin/gnome-text-editor", Cmd: "/usr/bin/gnome-text-editor", User: "me",
			Started: time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC), CPUTime: time.Hour + 2*time.Minute + 3*time.Second},
		{PID: 3300, PPID: 1990, MemKB: 4096, Name: "python3", Cmd: "python3 /opt/tool/server.py --port 8080", User: "me",
			Started: time.Date(2026, 10, 1, 8, 30, 0, 0, time.UTC), CPUTime: 24*time.Hour + 5*time.Second},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("processes\n%+v\nwant\n%+v", got, want)
	}
}

func TestProcessNamesWithoutAKernelNameComeFromTheFirstArgument(t *testing.T) {
	got := parseProcesses(psSample, func(int) (string, bool) { return "", false }, time.UTC)
	if got[2].Name != "sshd:" || got[3].Name != "gnome-text-editor" {
		t.Fatalf("processes %+v", got)
	}
}

func TestPSTimesReadToTheHundredth(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"0:00.03":    30 * time.Millisecond,
		"12:34.50":   12*time.Minute + 34500*time.Millisecond,
		"1:02:03":    time.Hour + 2*time.Minute + 3*time.Second,
		"2-03:04:05": 51*time.Hour + 4*time.Minute + 5*time.Second,
		"00:00:00":   0,
		"1234:56.78": 1234*time.Minute + 56780*time.Millisecond,
	} {
		got, err := parseCPUTime(in)
		if err != nil || got.Round(time.Millisecond) != want {
			t.Errorf("%s: %v %v, expected %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "12", "a:b", "1:2:3:4"} {
		if _, err := parseCPUTime(bad); err == nil {
			t.Errorf("%q was read", bad)
		}
	}
}
