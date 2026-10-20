//go:build windows

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// copySystemProgram copies a program of the system folder to name in a
// temporary folder and returns its path.
func copySystemProgram(t *testing.T, program, name string) string {
	t.Helper()
	data, err := os.ReadFile(systemProgram(program))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// openForWait opens pid for waiting on its end.
func openForWait(t *testing.T, pid int) syscall.Handle {
	t.Helper()
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatalf("open process %d: %v", pid, err)
	}
	t.Cleanup(func() { syscall.CloseHandle(h) })
	return h
}

// expectEnded fails the test when the process of h does not end within 10 s.
func expectEnded(t *testing.T, h syscall.Handle, pid int) {
	t.Helper()
	if ev, _ := syscall.WaitForSingleObject(h, 10000); ev != syscall.WAIT_OBJECT_0 {
		t.Fatalf("process %d still runs after proc.kill", pid)
	}
}

// startHidden starts path with args without a window and kills it at the
// end of the test.
func startHidden(t *testing.T, path string, args ...string) *exec.Cmd {
	t.Helper()
	c := exec.Command(path, args...)
	hideWindow(c)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { c.Wait(); close(exited) }()
	t.Cleanup(func() {
		c.Process.Kill()
		<-exited
	})
	return c
}

func TestProcKillByNameCountsTheKilledProcesses(t *testing.T) {
	path := copySystemProgram(t, "PING.EXE", "deskhand-victim.exe")
	var waits []syscall.Handle
	var pids []int
	for i := 0; i < 2; i++ {
		c := startHidden(t, path, "-n", "60", "127.0.0.1")
		pids = append(pids, c.Process.Pid)
		waits = append(waits, openForWait(t, c.Process.Pid))
	}
	c, _ := newRPCClient(t, Options{})
	if res := c.ok("proc.kill", proto.Message{"name": "deskhand-victim"}); res.Int("killed") != 2 {
		t.Fatalf("res %v", res)
	}
	for i, h := range waits {
		expectEnded(t, h, pids[i])
	}
	if res := c.ok("proc.kill", proto.Message{"name": "deskhand-victim"}); res.Int("killed") != 0 {
		t.Fatalf("a second kill answered %v", res)
	}
}

func TestProcKillByNameWithTreeEndsTheChildren(t *testing.T) {
	parent := startHidden(t, copySystemProgram(t, "cmd.exe", "deskhand-parent.exe"), "/c", systemProgram("PING.EXE"), "-n", "60", "127.0.0.1")
	child := 0
	deadline := time.Now().Add(10 * time.Second)
	for child == 0 {
		running, err := processEntries()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range running {
			if p.PPID == parent.Process.Pid && strings.EqualFold(p.Name, "PING.EXE") {
				child = p.PID
			}
		}
		if child == 0 && time.Now().After(deadline) {
			t.Fatal("the child did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	childWait := openForWait(t, child)
	parentWait := openForWait(t, parent.Process.Pid)
	c, _ := newRPCClient(t, Options{})
	if res := c.ok("proc.kill", proto.Message{"name": "deskhand-parent", "tree": true}); res.Int("killed") < 2 {
		t.Fatalf("res %v", res)
	}
	expectEnded(t, childWait, child)
	expectEnded(t, parentWait, parent.Process.Pid)
}

var (
	advapi32                      = syscall.NewLazyDLL("advapi32.dll")
	procConvertStringSDToSD       = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procGetSecurityDescriptorDacl = advapi32.NewProc("GetSecurityDescriptorDacl")
	procSetSecurityInfo           = advapi32.NewProc("SetSecurityInfo")
)

// setProcessDACL gives the process pid the access list of the SDDL string sddl.
func setProcessDACL(t *testing.T, pid int, sddl string) {
	t.Helper()
	const writeDAC = 0x00040000
	h, err := syscall.OpenProcess(writeDAC, false, uint32(pid))
	if err != nil {
		t.Fatalf("open process %d for its access list: %v", pid, err)
	}
	defer syscall.CloseHandle(h)
	text, err := syscall.UTF16PtrFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	var sd uintptr
	if r, _, err := procConvertStringSDToSD.Call(uintptr(unsafe.Pointer(text)), 1, uintptr(unsafe.Pointer(&sd)), 0); r == 0 {
		t.Fatalf("convert %s: %v", sddl, err)
	}
	defer syscall.LocalFree(syscall.Handle(sd))
	var present, defaulted int32
	var dacl uintptr
	if r, _, err := procGetSecurityDescriptorDacl.Call(sd, uintptr(unsafe.Pointer(&present)), uintptr(unsafe.Pointer(&dacl)), uintptr(unsafe.Pointer(&defaulted))); r == 0 {
		t.Fatalf("read the access list of %s: %v", sddl, err)
	}
	const seKernelObject, daclSecurityInformation = 6, 4
	if r, _, _ := procSetSecurityInfo.Call(uintptr(h), seKernelObject, daclSecurityInformation, 0, 0, dacl, 0); r != 0 {
		t.Fatalf("set the access list of process %d: error %d", pid, r)
	}
}

func TestProcKillByNameAnswersTheRefusalWhenAProcessDeniesTermination(t *testing.T) {
	victim := startHidden(t, copySystemProgram(t, "PING.EXE", "deskhand-guarded.exe"), "-n", "60", "127.0.0.1")
	setProcessDACL(t, victim.Process.Pid, "D:(D;;0x1;;;WD)(A;;0x1fffff;;;WD)")
	c, _ := newRPCClient(t, Options{})
	res := c.call("proc.kill", proto.Message{"name": "deskhand-guarded"})
	if want := killRefusal("deskhand-guarded", 0, 1, tokenElevated()).Error(); res["ok"] != false || res.String("error") != want {
		t.Fatalf("res %v, expected the refusal %q", res, want)
	}
	setProcessDACL(t, victim.Process.Pid, "D:(A;;0x1fffff;;;WD)")
	wait := openForWait(t, victim.Process.Pid)
	if res := c.ok("proc.kill", proto.Message{"name": "deskhand-guarded"}); res.Int("killed") != 1 {
		t.Fatalf("after the access list allowed termination: %v", res)
	}
	expectEnded(t, wait, victim.Process.Pid)
}

// shellWithChild is an exec.start request whose shell runs a child process
// for 30 s.
func shellWithChild() proto.Message {
	return proto.Message{"cmd": "ping -n 30 127.0.0.1 >nul & echo done", "shell": "cmd"}
}

// longCommand is an exec.start request of a process that runs for 30 s.
func longCommand() proto.Message {
	return proto.Message{"argv": []any{systemProgram("PING.EXE"), "-n", "30", "127.0.0.1"}}
}

// busyCommand is an exec.start request whose process keeps a processor busy.
func busyCommand() proto.Message {
	return proto.Message{"cmd": "for /L %i in (1,0,2) do @rem", "shell": "cmd"}
}

// unrelatedPID is a process the agent did not start.
func unrelatedPID() int { return 4 }

func TestAStartedTreeKillEndsTheTreeThroughItsHandles(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	start := c.ok("exec.start", shellWithChild())
	job, pid := start.String("job"), start.Int("pid")
	t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })
	child := childOf(t, c, pid)
	childPID := int(child["pid"].(float64))
	root := listed(c, pid)
	waitRoot, waitChild := openForWait(t, pid), openForWait(t, childPID)
	res := c.call("proc.kill", proto.Message{"pid": pid, "started": "2001-01-01T00:00:00.000Z", "tree": true})
	if res.String("code") != wire.CodeStaleTarget {
		t.Fatalf("stale tree kill %v", res)
	}
	if listed(c, childPID) == nil || listed(c, pid) == nil {
		t.Fatal("a stale tree kill ended a process")
	}
	if res := c.ok("proc.kill", proto.Message{"pid": pid, "started": root["started"], "tree": true}); res.Int("killed") != 1 {
		t.Fatalf("tree kill %v", res)
	}
	expectEnded(t, waitChild, childPID)
	expectEnded(t, waitRoot, pid)
}

func TestAStartedKillOfAProcessThatRefusesAccessAnswersTheRefusal(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	res := c.call("proc.kill", proto.Message{"pid": 4, "started": "2001-01-01T00:00:00.000Z"})
	if res["ok"] != false || res.String("code") != wire.CodePermissionDenied {
		t.Fatalf("kill of System %v", res)
	}
}
