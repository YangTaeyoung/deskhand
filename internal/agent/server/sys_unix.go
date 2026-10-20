//go:build !windows

package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// childEnvironment returns the agent's own environment.
func childEnvironment() ([]string, error) { return os.Environ(), nil }

// agentHome returns the home directory of the agent's user: $HOME, else the
// one in the user database.
func agentHome() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return ""
}

// prepareProgram leaves c as it is and returns "".
func prepareProgram(*exec.Cmd) (string, error) { return "", nil }

func hideWindow(*exec.Cmd) {}

// windowsFolder answers that this system has no Windows folder.
func windowsFolder() (string, error) {
	return "", errors.New("this system has no Windows folder")
}

// setRawCommandLine has no effect outside Windows.
func setRawCommandLine(*exec.Cmd, string) {}

// prepareTree puts the child in its own process group.
func prepareTree(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// killTree sends SIGKILL to the process group led by pid, or to pid alone
// when it leads no group.
func killTree(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to kill pid %d", pid)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

// psColumns are the ps columns listProcesses reads, args last.
const psColumns = "pid=,ppid=,rss=,time=,user=,lstart=,args="

// psOutput runs ps with args in the C locale, which prints lstart in
// English.
func psOutput(args ...string) (string, error) {
	c := exec.Command("ps", args...)
	c.Env = append(os.Environ(), "LC_ALL=C")
	out, err := c.Output()
	return string(out), err
}

func listProcesses() ([]procInfo, error) {
	out, err := psOutput("-axo", psColumns)
	if err != nil {
		return nil, err
	}
	return parseProcesses(out, kernelName, time.Local), nil
}

// sampleProcesses lists the processes with the processor share each used
// between a ps reading of the processor times and the listing gap later.
func sampleProcesses(ctx context.Context, gap time.Duration) ([]procInfo, error) {
	out, err := psOutput("-axo", "pid=,time=")
	if err != nil {
		return nil, err
	}
	first := map[int]time.Duration{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		used, err2 := parseCPUTime(f[1])
		if err1 == nil && err2 == nil {
			first[pid] = used
		}
	}
	began := time.Now()
	t := time.NewTimer(gap)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	procs, err := listProcesses()
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(began)
	for i, p := range procs {
		procs[i].CPUPct = cpuPercent(p.CPUTime-first[p.PID], elapsed, runtime.NumCPU())
	}
	return procs, nil
}

// parseCPUTime reads a ps time: [[days-]hours:]minutes:seconds with an
// optional fraction of a second.
func parseCPUTime(s string) (time.Duration, error) {
	var days int
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, err
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("time %q", s)
	}
	sec, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, err
	}
	total := time.Duration(days) * 24 * time.Hour
	units := []time.Duration{time.Minute, time.Hour}
	for i := len(parts) - 2; i >= 0; i-- {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return 0, err
		}
		total += time.Duration(n) * units[len(parts)-2-i]
	}
	return total + time.Duration(sec*float64(time.Second)), nil
}

// lstartLayout is the layout of ps lstart in the C locale.
const lstartLayout = "Mon Jan 2 15:04:05 2006"

// kernelName returns the name the kernel keeps for pid (/proc/<pid>/comm,
// at most 15 bytes), or false where there is none.
func kernelName(pid int) (string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return "", false
	}
	return strings.TrimSuffix(string(b), "\n"), true
}

// parseProcesses reads ps with psColumns, lstart in loc. A process is
// named by comm, its kernel name; when that name is the base name of its
// first argument, or the first 15 bytes of it, by that base name; without a
// kernel name by that base name.
func parseProcesses(out string, comm func(int) (string, bool), loc *time.Location) []procInfo {
	var procs []procInfo
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 11 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rss, err3 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		used, _ := parseCPUTime(f[3])
		started, _ := time.ParseInLocation(lstartLayout, strings.Join(f[5:10], " "), loc)
		args := f[10:]
		base := filepath.Base(args[0])
		name := base
		if c, ok := comm(pid); ok && c != "" && c != base && !(len(c) == 15 && strings.HasPrefix(base, c)) {
			name = c
		}
		p := procInfo{PID: pid, PPID: ppid, MemKB: rss, Name: name, Cmd: strings.Join(args, " "), User: f[4], Started: started, CPUTime: used}
		if filepath.IsAbs(args[0]) {
			p.Path = args[0]
		}
		procs = append(procs, p)
	}
	return procs
}

// processEntries lists the running processes with their creation times.
func processEntries() ([]procEntry, error) {
	procs, err := listProcesses()
	if err != nil {
		return nil, err
	}
	out := make([]procEntry, 0, len(procs))
	for _, p := range procs {
		out = append(out, procEntry{PID: p.PID, PPID: p.PPID, Name: p.Name, Created: p.Started})
	}
	return out, nil
}

// processStarted returns the creation time ps lists for pid, and false
// when no process has it.
func processStarted(pid int) (time.Time, bool) {
	out, err := psOutput("-o", "lstart=", "-p", strconv.Itoa(pid))
	if err != nil {
		return time.Time{}, false
	}
	at, err := time.ParseInLocation(lstartLayout, strings.Join(strings.Fields(out), " "), time.Local)
	return at, err == nil
}

// killStarted ends pid, with tree its process group, when the process that
// runs with pid was created at want to the millisecond as ps lists it.
func killStarted(pid int, want time.Time, tree bool) error {
	created, ok := processStarted(pid)
	if !ok {
		return noProcess(pid)
	}
	if !sameMillisecond(created, want) {
		return otherProcess(pid, created, want)
	}
	kill := killPID
	if tree {
		kill = killTree
	}
	if err := kill(pid); err != nil {
		return fmt.Errorf("cannot kill pid %d: %v; call proc.list to check it", pid, err)
	}
	return nil
}

// pidCreation reports false: outside Windows the creation time of a
// started process is not read.
func pidCreation(int) (time.Time, bool) { return time.Time{}, false }

// killByName kills every process whose name equals name (case-insensitive)
// and returns how many were signalled.
func killByName(name string, tree bool) (int, error) {
	procs, err := listProcesses()
	if err != nil {
		return 0, err
	}
	self := os.Getpid()
	killed := 0
	for _, p := range procs {
		if p.PID == self || !strings.EqualFold(p.Name, name) {
			continue
		}
		var err error
		if gid, gerr := syscall.Getpgid(p.PID); tree && gerr == nil && gid == p.PID {
			err = killTree(p.PID)
		} else {
			err = syscall.Kill(p.PID, syscall.SIGKILL)
		}
		if err == nil {
			killed++
		}
	}
	return killed, nil
}

// resolveProgram returns the program to start for an app.launch path: path
// itself.
func resolveProgram(path string) (string, error) { return path, nil }

// processParents maps each running process to its parent.
func processParents() map[int]int {
	procs, err := listProcesses()
	if err != nil {
		return nil
	}
	parents := make(map[int]int, len(procs))
	for _, p := range procs {
		parents[p.PID] = p.PPID
	}
	return parents
}

// signalInspector asks the Node process pid to open its inspector by
// sending it SIGUSR1, the documented activation signal (node(1)).
func signalInspector(pid int) (bool, error) {
	if err := syscall.Kill(pid, syscall.SIGUSR1); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return false, coded(wire.CodeNotFound, fmt.Sprintf("no process has pid %d; call proc.list to check it", pid), false)
		}
		return false, coded(wire.CodeCapabilityUnsupported, fmt.Sprintf("cannot signal pid %d to open its inspector (%v); start the app with --inspect and attach by port", pid, err), false)
	}
	return true, nil
}

// listeningPorts returns the loopback TCP ports that pid listens on, read
// from lsof.
func listeningPorts(pid int) map[int]bool {
	out := map[int]bool{}
	c := exec.Command("lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN")
	c.Env = append(os.Environ(), "LC_ALL=C")
	data, err := c.Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		for _, f := range strings.Fields(line) {
			host, port, ok := strings.Cut(f, ":")
			if !ok || (host != "127.0.0.1" && host != "[::1]" && host != "localhost" && host != "*") {
				continue
			}
			n, err := strconv.Atoi(port)
			if err == nil && n > 0 {
				out[n] = true
			}
		}
	}
	return out
}

// processCmdlines returns the running processes with their command lines.
func processCmdlines() []procCmd {
	procs, err := listProcesses()
	if err != nil {
		return nil
	}
	out := make([]procCmd, 0, len(procs))
	for _, p := range procs {
		out = append(out, procCmd{pid: p.PID, cmd: p.Cmd})
	}
	return out
}

// startedAt returns the creation time ps lists for pid.
func startedAt(pid int) (time.Time, bool) { return processStarted(pid) }

// inspectableProcess reports whether pid is a Node or Electron process, so
// SIGUSR1 would open an inspector.
func inspectableProcess(pid int) bool {
	name := strings.ToLower(processName(pid))
	return strings.Contains(name, "node") || strings.Contains(name, "electron")
}

// processName returns the executable name ps lists for pid.
func processName(pid int) string {
	out, err := psOutput("-o", "comm=", "-p", strconv.Itoa(pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
