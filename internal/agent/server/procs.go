package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/diag"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// procInfo is a process as proc.list reports it. CPUPct is its share of
// all logical processors between two samples, Started its creation time
// (zero when unknown) and CPUTime the processor time it used up to the
// listing where the listing reads it.
type procInfo struct {
	PID, PPID       int
	Name, Path, Cmd string
	MemKB           int
	CPUPct          float64
	User            string
	Started         time.Time
	CPUTime         time.Duration
}

func (s *Server) registerProcs() {
	s.register("proc.list", true, s.procList)
	s.register("proc.kill", true, s.procKill)
}

// procList lists the processes sampled over diag.SampleGap, marking the
// agent itself and the processes it started.
func (s *Server) procList(ctx context.Context, req proto.Message) (proto.Message, error) {
	procs, err := sampleProcesses(ctx, diag.SampleGap)
	if err != nil {
		return nil, fmt.Errorf("cannot list processes: %v", err)
	}
	marked := startedByDeskhand(procs, s.started.list())
	return proto.Message{"procs": procListEntries(procs, req.String("name"), marked, os.Getpid())}, nil
}

// procListEntries builds the proc.list reply entries for the processes whose
// name contains filter, each command line cut to procListCmdChars
// characters; marked holds the processes the agent started and self is the
// agent's pid.
func procListEntries(procs []procInfo, filter string, marked map[int]bool, self int) []any {
	filter = strings.ToLower(filter)
	out := []any{}
	for _, p := range procs {
		if filter != "" && !strings.Contains(strings.ToLower(p.Name), filter) {
			continue
		}
		e := map[string]any{
			"pid": p.PID, "ppid": p.PPID, "name": p.Name, "path": p.Path, "cmd": cutChars(p.Cmd, procListCmdChars), "mem_kb": p.MemKB,
			"cpu_pct": p.CPUPct, "started_by_deskhand": marked[p.PID], "self": p.PID == self,
		}
		if p.User != "" {
			e["user"] = p.User
		}
		if !p.Started.IsZero() {
			e["started"] = wire.FormatDiagTime(p.Started)
		}
		out = append(out, e)
	}
	return out
}

// cpuPercent returns used processor time over elapsed as a share of cpus
// logical processors, 0–100 to one decimal.
func cpuPercent(used, elapsed time.Duration, cpus int) float64 {
	if used <= 0 || elapsed <= 0 || cpus < 1 {
		return 0
	}
	return min(100, math.Round(1000*float64(used)/(float64(elapsed)*float64(cpus)))/10)
}

// maxStartedRoots bounds the processes a startedTable keeps.
const maxStartedRoots = 1024

// startSlack is how much earlier than its notBefore a root without a
// creation time may be listed as created.
const startSlack = time.Second

// startedRoot is a process the agent started: its pid, its creation time
// (zero when it could not be read), the time just before it was started and
// the time its end was seen (zero while it runs).
type startedRoot struct {
	pid                        int
	created, notBefore, exited time.Time
}

// start returns the creation time of r, or notBefore without one.
func (r startedRoot) start() time.Time {
	if !r.created.IsZero() {
		return r.created
	}
	return r.notBefore
}

// is reports whether a process created at started is r: its creation time
// to the millisecond, or without one a creation from notBefore less
// startSlack up to its end.
func (r startedRoot) is(started time.Time) bool {
	if started.IsZero() {
		return false
	}
	if !r.created.IsZero() {
		return sameMillisecond(r.created, started)
	}
	return !started.Before(r.notBefore.Add(-startSlack)) && (r.exited.IsZero() || !started.After(r.exited))
}

// sameMillisecond reports whether a and b are the same time to the
// millisecond.
func sameMillisecond(a, b time.Time) bool {
	return a.Truncate(time.Millisecond).Equal(b.Truncate(time.Millisecond))
}

// startedTable holds the processes exec, app.launch and pty started, the
// running ones and the newest ended ones up to maxStartedRoots.
type startedTable struct {
	mu    sync.Mutex
	roots []startedRoot
}

// add records pid, created at created (zero when unknown) and started after
// notBefore. Over maxStartedRoots the oldest ended process is dropped, or
// the oldest one when none ended.
func (t *startedTable) add(pid int, created, notBefore time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roots = append(t.roots, startedRoot{pid: pid, created: created, notBefore: notBefore})
	if len(t.roots) <= maxStartedRoots {
		return
	}
	drop := 0
	for i, r := range t.roots {
		if !r.exited.IsZero() {
			drop = i
			break
		}
	}
	t.roots = append(t.roots[:drop], t.roots[drop+1:]...)
}

// exited records that the newest running process pid ended at at.
func (t *startedTable) exited(pid int, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.roots) - 1; i >= 0; i-- {
		if t.roots[i].pid == pid && t.roots[i].exited.IsZero() {
			t.roots[i].exited = at
			return
		}
	}
}

// list returns the recorded processes.
func (t *startedTable) list() []startedRoot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]startedRoot(nil), t.roots...)
}

// trackStarted records pid, started by the agent after notBefore, as a
// process of started_by_deskhand.
func (s *Server) trackStarted(pid int, notBefore time.Time) {
	created, _ := pidCreation(pid)
	s.started.add(pid, created, notBefore)
}

// startedByDeskhand returns the pids of procs that are a root or descend
// from one. A listed parent counts only when it was not created after the
// child; a child whose parent pid is not listed, or is listed with a later
// creation, descends from a root of that pid that is not listed when the
// child was created from the root's start up to its end. A process without
// a creation time is not marked.
func startedByDeskhand(procs []procInfo, roots []startedRoot) map[int]bool {
	byPID := make(map[int]procInfo, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	live := func(r startedRoot) bool {
		p, ok := byPID[r.pid]
		return ok && r.is(p.Started)
	}
	isRoot := func(p procInfo) bool {
		for _, r := range roots {
			if r.pid == p.PID && r.is(p.Started) {
				return true
			}
		}
		return false
	}
	deadParent := func(p procInfo) bool {
		for _, r := range roots {
			if r.pid == p.PPID && !live(r) && !p.Started.Before(r.start().Add(-startSlack)) && (r.exited.IsZero() || !p.Started.After(r.exited)) {
				return true
			}
		}
		return false
	}
	const visiting = 2
	state := map[int]int{}
	var marked func(p procInfo) bool
	marked = func(p procInfo) bool {
		switch state[p.PID] {
		case 1:
			return true
		case -1, visiting:
			return false
		}
		state[p.PID] = visiting
		res := false
		switch parent, ok := byPID[p.PPID]; {
		case p.Started.IsZero():
		case isRoot(p):
			res = true
		case ok && p.PPID != p.PID && !parent.Started.IsZero() && !parent.Started.After(p.Started):
			res = marked(parent)
		default:
			res = deadParent(p)
		}
		state[p.PID] = -1
		if res {
			state[p.PID] = 1
		}
		return res
	}
	out := map[int]bool{}
	for _, p := range procs {
		if marked(p) {
			out[p.PID] = true
		}
	}
	return out
}

// treeHolds reports whether the process tree of root holds the process
// self: whether root is self or one of its ancestors, following each parent
// only when it was created before or with its child.
func treeHolds(procs []procEntry, root, self int) bool {
	byPID := make(map[int]procEntry, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	seen := map[int]bool{}
	for cur := self; !seen[cur]; {
		if cur == root {
			return true
		}
		seen[cur] = true
		p, ok := byPID[cur]
		if !ok {
			return false
		}
		parent, ok := byPID[p.PPID]
		if !ok || p.Created.IsZero() || parent.Created.IsZero() || parent.Created.After(p.Created) {
			return false
		}
		cur = p.PPID
	}
	return false
}

func (s *Server) procKill(_ context.Context, req proto.Message) (proto.Message, error) {
	tree := req.Bool("tree")
	started, err := optionalString(req, "started")
	if err != nil {
		return nil, err
	}
	if pid := req.Int("pid"); pid > 0 {
		if err := checkKillTarget(pid, tree); err != nil {
			return nil, err
		}
		if started != "" {
			want, err := wire.ParseDiagTime(started)
			if err != nil {
				return nil, invalidArgument(fmt.Sprintf("started '%s' is not an RFC 3339 time; pass the started value of proc.list", cutChars(started, 64)))
			}
			if err := killStarted(pid, want, tree); err != nil {
				return nil, err
			}
			return proto.Message{"killed": 1}, nil
		}
		if tree {
			err = killTree(pid)
		} else {
			err = killPID(pid)
		}
		if err != nil {
			return nil, fmt.Errorf("cannot kill pid %d: %v; call proc.list to check it", pid, err)
		}
		return proto.Message{"killed": 1}, nil
	}
	name := req.String("name")
	if name == "" {
		return nil, fmt.Errorf("missing pid or name")
	}
	if started != "" {
		return nil, invalidArgument("started applies to a kill by pid; pass pid with the started value of proc.list")
	}
	n, err := killNamed(name, tree)
	var refused *killRefusedError
	if errors.As(err, &refused) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("cannot kill '%s': %v; call proc.list to check it", name, err)
	}
	return proto.Message{"killed": n}, nil
}

// checkKillTarget refuses a kill by pid that would end the agent, itself or
// with tree through its tree.
func checkKillTarget(pid int, tree bool) error {
	self := os.Getpid()
	if pid == self {
		return coded(wire.CodePermissionDenied, fmt.Sprintf("pid %d is deskhand-agent itself, which proc.kill does not end; restart the agent through its scheduled task instead", pid), false)
	}
	if tree {
		procs, err := processEntries()
		if err != nil {
			return fmt.Errorf("the processes could not be listed to check the tree of pid %d (%v); retry", pid, err)
		}
		if treeHolds(procs, pid, self) {
			return coded(wire.CodePermissionDenied, fmt.Sprintf("the process tree of pid %d holds deskhand-agent itself, which proc.kill does not end; end the processes of the tree one by one without tree", pid), false)
		}
	}
	return nil
}

// noProcess is the refusal of a kill by pid and started when no process
// runs with pid.
func noProcess(pid int) error {
	return coded(wire.CodeStaleTarget, fmt.Sprintf("no process with pid %d runs now; call proc.list again", pid), false)
}

// otherProcess is the refusal of a kill by pid and started when the process
// that runs with pid was created at created, not at want.
func otherProcess(pid int, created, want time.Time) error {
	return coded(wire.CodeStaleTarget, fmt.Sprintf("pid %d now belongs to a process created at %s, not at %s; call proc.list again", pid,
		wire.FormatDiagTime(created), wire.FormatDiagTime(want)), false)
}

// childMap returns procs by pid and the children of each pid: a process is
// a child of its parent only when both creation times are known and the
// child was not created before the parent.
func childMap(procs []procEntry) (map[int]procEntry, map[int][]int) {
	byPID := make(map[int]procEntry, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	children := map[int][]int{}
	for _, p := range procs {
		parent, ok := byPID[p.PPID]
		if !ok || p.PPID == p.PID {
			continue
		}
		if p.Created.IsZero() || parent.Created.IsZero() || p.Created.Before(parent.Created) {
			continue
		}
		children[p.PPID] = append(children[p.PPID], p.PID)
	}
	return byPID, children
}

// treeTargets returns the descendants of root, created at created, each
// child before its parent, without following the tree into self.
func treeTargets(procs []procEntry, root int, created time.Time, self int) []procEntry {
	list := make([]procEntry, 0, len(procs))
	for _, p := range procs {
		if p.PID == root {
			p.Created = created
		}
		list = append(list, p)
	}
	byPID, children := childMap(list)
	seen := map[int]bool{self: true, root: true}
	out := []procEntry{}
	var visit func(pid int)
	visit = func(pid int) {
		for _, c := range children[pid] {
			if !seen[c] {
				seen[c] = true
				visit(c)
				out = append(out, byPID[c])
			}
		}
	}
	visit(root)
	return out
}

// killNamed kills the processes of a name for proc.kill.
var killNamed = killByName

// killRefusedError is the answer of a kill by name that some processes
// refused.
type killRefusedError struct{ text string }

func (e *killRefusedError) Error() string { return e.text }

// killRefusal is the error of a kill by name of which denied processes
// refused access and killed processes ended; elevated tells whether the
// agent's token is elevated.
func killRefusal(name string, killed, denied int, elevated bool) error {
	verb := "were"
	if killed == 1 {
		verb = "was"
	}
	if elevated {
		return &killRefusedError{fmt.Sprintf("could not kill %d of the %d processes named '%s' because Windows denied access to the elevated agent (%d %s killed); such a process is protected or needs more than administrator rights, so stop it through its service (sc stop <service>) or leave it running",
			denied, killed+denied, name, killed, verb)}
	}
	return &killRefusedError{fmt.Sprintf("could not kill %d of the %d processes named '%s' because access was denied (%d %s killed); restart deskhand-agent elevated through the scheduled task that deskhand-agent --install creates, then retry",
		denied, killed+denied, name, killed, verb)}
}

// errKillDenied is the error of a killHandle opener or terminate that the
// process refused access.
var errKillDenied = errors.New("access is denied")

// killHandle is a process opened for a kill by name.
type killHandle interface {
	// creation returns the creation time, and false when it cannot be read.
	creation() (time.Time, bool)
	// imageName returns the base name of the program file, and false when
	// it cannot be read.
	imageName() (string, bool)
	terminate() error
	// wait waits up to d for the process to end.
	wait(d time.Duration)
	close()
}

// listedCreation returns created, or the zero time when created is after
// began.
func listedCreation(created, began time.Time) time.Time {
	if created.After(began) {
		return time.Time{}
	}
	return created
}

// killListed opens each target with open and terminates it when its creation
// time equals the listed one and its image name equals the listed name
// without regard to case, then waits up to wait in all for the terminated
// processes to end. It returns how many it terminated and how many refused
// access with errKillDenied. A target whose creation time or image name is
// unknown or differs, or that fails for another reason, is skipped.
func killListed(targets []procEntry, open func(pid int) (killHandle, error), wait time.Duration) (killed, denied int) {
	var ending []killHandle
	for _, t := range targets {
		h, err := open(t.PID)
		if err != nil {
			if errors.Is(err, errKillDenied) {
				denied++
			}
			continue
		}
		created, known := h.creation()
		image, named := h.imageName()
		if !known || t.Created.IsZero() || !created.Equal(t.Created) || !named || !strings.EqualFold(image, t.Name) {
			h.close()
			continue
		}
		if err := h.terminate(); err != nil {
			if errors.Is(err, errKillDenied) {
				denied++
			}
			h.close()
			continue
		}
		killed++
		ending = append(ending, h)
	}
	deadline := time.Now().Add(wait)
	for _, h := range ending {
		if left := time.Until(deadline); left > 0 {
			h.wait(left)
		}
		h.close()
	}
	return killed, denied
}

// procEntry is a running process as a kill by name on Windows sees it.
type procEntry struct {
	PID, PPID int
	Name      string
	// Created is the creation time, zero when it is unknown.
	Created time.Time
}

// nameKillTargets returns the processes a kill by name on Windows ends: those
// whose name equals name without regard to case, with ".exe" added to a name
// without an extension, and with tree their descendants, each child before
// its parent. A process counts as a child of its parent only when both
// creation times are known and the child was not created before the parent.
// self is never a target, and a tree is not followed into self.
func nameKillTargets(procs []procEntry, name string, tree bool, self int) []procEntry {
	if filepath.Ext(name) == "" {
		name += ".exe"
	}
	byPID, children := childMap(procs)
	seen := map[int]bool{self: true}
	out := []procEntry{}
	var visit func(pid int)
	visit = func(pid int) {
		if seen[pid] {
			return
		}
		seen[pid] = true
		if tree {
			for _, c := range children[pid] {
				visit(c)
			}
		}
		out = append(out, byPID[pid])
	}
	for _, p := range procs {
		if strings.EqualFold(p.Name, name) {
			visit(p.PID)
		}
	}
	return out
}

func killPID(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
