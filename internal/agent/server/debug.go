package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// procCmd is a process and its command line.
type procCmd struct {
	pid int
	cmd string
}

// inspectorTimeout is the refusal of a pid attach whose inspector did not
// open a loopback port in time.
func inspectorTimeout() error {
	return coded(wire.CodeCapabilityUnsupported,
		"the Node inspector did not open a loopback port within 5s; start the app with --inspect and attach by its port, or launch it with debug.electron.launch", false)
}

// inspectorPortWait bounds the wait for the inspector port of a pid attach.
const inspectorPortWait = 5 * time.Second

// debugLaunch holds what the agent knows of an app it launched for a debug
// session: whether it is still running and its exit code once it ended.
type debugLaunch struct {
	exited bool
	code   int
}

// debugState holds the launches the agent started for debug sessions.
type debugState struct {
	mu       sync.Mutex
	launches map[int]*debugLaunch
}

func (d *debugState) track(pid int) {
	d.mu.Lock()
	if d.launches == nil {
		d.launches = map[int]*debugLaunch{}
	}
	d.launches[pid] = &debugLaunch{}
	d.mu.Unlock()
}

func (d *debugState) exited(pid, code int) {
	d.mu.Lock()
	if l := d.launches[pid]; l != nil {
		l.exited, l.code = true, code
	}
	d.mu.Unlock()
}

func (d *debugState) code(pid int) (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if l := d.launches[pid]; l != nil && l.exited {
		return l.code, true
	}
	return 0, false
}

func (s *Server) registerDebug() {
	s.register("debug.electron.launch", true, s.debugElectronLaunch)
	s.register("debug.tree", true, s.debugTree)
	s.register("debug.attach.pid", true, s.debugAttachPID)
}

// debugElectronLaunch picks two free loopback ports P1 and P2, starts the
// program path with its args and
// "--inspect[-brk]=127.0.0.1:P1 --remote-debugging-port=P2" in the user
// session, verifies the pid and returns it with the ports. The daemon opens
// its tunnels to P1 and P2 and connects to the inspector and the DevTools
// endpoint. An agent without exec refuses it.
func (s *Server) debugElectronLaunch(_ context.Context, req proto.Message) (proto.Message, error) {
	path := req.String("path")
	if path == "" {
		return nil, errors.New("missing path; pass the Electron program to start")
	}
	ports, err := tunnel.FreePorts(2)
	if err != nil {
		return nil, fmt.Errorf("cannot pick inspector ports (%v); check that this PC allows local sockets", err)
	}
	p1, p2 := ports[0], ports[1]
	inspect := fmt.Sprintf("--inspect=127.0.0.1:%d", p1)
	if req.Bool("pause_on_start") {
		inspect = fmt.Sprintf("--inspect-brk=127.0.0.1:%d", p1)
	}
	args := append([]string{}, req.Strings("args")...)
	args = append(args, inspect, fmt.Sprintf("--remote-debugging-port=%d", p2))
	c, started, err := s.buildChild(path, args, req.String("cwd"), req.StringMap("env"))
	if err != nil {
		return nil, err
	}
	pid, _, _, err := s.startChild(c, started, func(code int) { s.debug.exited(c.Process.Pid, code) })
	if err != nil {
		return nil, fmt.Errorf("cannot start '%s': %v; check the path with fs.stat", path, err)
	}
	s.debug.track(pid)
	out := proto.Message{"pid": pid, "ports": proto.Message{"inspect": p1, "remote_debugging": p2}}
	if at, ok := startedAt(pid); ok {
		out["started"] = wire.FormatDiagTime(at)
	}
	return out, nil
}

// debugTree reports whether the process pid or any of its descendants is
// still running, and the exit code of pid once the agent has seen it end.
// The daemon polls it while it waits for the inspector ports to detect that
// the launched tree exited early.
func (s *Server) debugTree(_ context.Context, req proto.Message) (proto.Message, error) {
	pid := req.Int("pid")
	if pid <= 0 {
		return nil, errors.New("pass the pid of a launched process")
	}
	out := proto.Message{"alive": treeAlive(pid)}
	if code, ok := s.debug.code(pid); ok {
		out["exit_code"] = code
	}
	return out, nil
}

// treeAlive reports whether pid or a descendant of it is a running process.
func treeAlive(pid int) bool {
	parents := processParents()
	if parents == nil {
		return false
	}
	if _, ok := parents[pid]; ok {
		return true
	}
	tree := map[int]bool{pid: true}
	growTree(tree, parents)
	for p := range parents {
		if tree[p] {
			return true
		}
	}
	return false
}

// debugAttachPID turns on the Node inspector of the process pid and returns
// the loopback port it listens on, with the remote debugging port of its
// command line when it has one. On a platform without a supported way to
// signal the inspector it is a CAPABILITY_UNSUPPORTED refusal.
func (s *Server) debugAttachPID(ctx context.Context, req proto.Message) (proto.Message, error) {
	pid := req.Int("pid")
	if pid <= 0 {
		return nil, errors.New("pass the pid of the process to attach to")
	}
	if !inspectableProcess(pid) {
		return nil, coded(wire.CodeCapabilityUnsupported,
			fmt.Sprintf("pid %d is not a Node or Electron process; attach by port after starting it with --inspect", pid), false)
	}
	before := listeningPorts(pid)
	opened, err := signalInspector(pid)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(inspectorPortWait)
	for {
		for p := range listeningPorts(pid) {
			if !before[p] {
				out := proto.Message{"inspect_port": p, "inspector_opened": opened}
				if rd := remoteDebuggingArg(pid); rd > 0 {
					out["remote_debugging_port"] = rd
				}
				return out, nil
			}
		}
		if !time.Now().Before(deadline) {
			return nil, inspectorTimeout()
		}
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// remoteDebuggingArg returns the --remote-debugging-port value of pid's
// command line, or 0.
func remoteDebuggingArg(pid int) int {
	for _, p := range processCmdlines() {
		if p.pid != pid {
			continue
		}
		for _, f := range strings.Fields(p.cmd) {
			if v, ok := strings.CutPrefix(f, "--remote-debugging-port="); ok {
				n := 0
				if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
					return n
				}
			}
		}
	}
	return 0
}
