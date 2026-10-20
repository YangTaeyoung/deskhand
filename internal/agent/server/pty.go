package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/YangTaeyoung/deskhand/internal/agent/pty"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// ptyScrollbackBytes bounds the output a pty keeps for the streams
	// that attach later (docs/protocol.md §7).
	ptyScrollbackBytes = 1 << 20
	// maxPTYs bounds the ptys that run at once; ptyExitedKeep bounds the
	// ended ones pty.list keeps.
	maxPTYs       = 32
	ptyExitedKeep = 32
	// ptyChunk is the most output one data frame carries.
	ptyChunk = 32 << 10
	// ptyQueueFrames and ptyQueueBytes bound the frames and the bytes
	// waiting for one attached stream; a stream that falls further behind
	// is detached.
	ptyQueueFrames = 16384
	ptyQueueBytes  = 4 << 20
	// ptyMaxInput bounds the frames a pty stream reads.
	ptyMaxInput = 1 << 20
	// ptyWriteTimeout bounds one frame write to an attached stream.
	ptyWriteTimeout = 30 * time.Second
	// ptyDrainWait bounds the wait for the rest of the output after the
	// shell exited, before and after the terminal is closed.
	ptyDrainWait = 3 * time.Second
	// ptyKillWait bounds the wait of exec.kill for a pty to end.
	ptyKillWait = 10 * time.Second
	// ptyDefaultCols and ptyDefaultRows size a pty whose header has none.
	ptyDefaultCols, ptyDefaultRows = 80, 24
	// ptyReplayAlign is how far into a scrollback that lost its start the
	// replay looks for the end of a line to start after.
	ptyReplayAlign = 4 << 10
)

// PTYStream is a pty stream (docs/protocol.md §2, §7).
type PTYStream interface {
	io.Reader
	io.Writer
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	// Close ends the agent's direction of the stream.
	Close() error
	// CancelRead asks the daemon to stop writing to the stream.
	CancelRead()
	// CancelWrite resets the agent's direction of the stream.
	CancelWrite()
}

// ptyFrame is a frame queued for an attached stream.
type ptyFrame struct {
	kind    byte
	payload []byte
}

// ptyAttachment is one stream attached to a pty. Frames are queued without
// waiting; a full queue detaches the stream.
type ptyAttachment struct {
	frames chan ptyFrame
	// queued counts the payload bytes in frames.
	queued atomic.Int64
	gone   chan struct{}
	once   sync.Once
}

func newAttachment() *ptyAttachment {
	return &ptyAttachment{frames: make(chan ptyFrame, ptyQueueFrames), gone: make(chan struct{})}
}

// push queues f and reports whether it fit within ptyQueueFrames and,
// for a data frame, ptyQueueBytes; a full queue ends the attachment.
func (a *ptyAttachment) push(f ptyFrame) bool {
	if a.queued.Add(int64(len(f.payload))) > ptyQueueBytes && f.kind == wire.KindPTYData {
		a.end()
		return false
	}
	select {
	case a.frames <- f:
		return true
	default:
		a.end()
		return false
	}
}

// took records that the writer took f from the queue.
func (a *ptyAttachment) took(f ptyFrame) { a.queued.Add(-int64(len(f.payload))) }

// end ends the attachment; its writer stops after the frames it holds.
func (a *ptyAttachment) end() { a.once.Do(func() { close(a.gone) }) }

// ptySession is one shell on a pseudo-terminal. It outlives the streams
// attached to it and the connections they came on. It is listed from the
// moment it is added to the table; pid and con are set once its shell
// started.
type ptySession struct {
	id, rid, shell string

	inputMu sync.Mutex

	mu       sync.Mutex
	pid      int
	started  time.Time
	con      *pty.Console
	cols     int
	rows     int
	scroll   *ring
	attached map[*ptyAttachment]struct{}
	exited   bool
	code     int
	done     chan struct{}
}

// attach registers a new attachment and returns it with the scrollback it
// replays first; output after the scrollback goes to the attachment. An
// ended pty has no attachments.
func (p *ptySession) attach() (*ptyAttachment, []byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exited {
		return nil, nil, coded(wire.CodeNotFound, fmt.Sprintf("pty '%s' has exited with code %d; open a new one", p.id, p.code), false)
	}
	if p.con == nil {
		return nil, nil, coded(wire.CodeInProgress, fmt.Sprintf("pty '%s' is still starting; attach again in a moment", p.id), true)
	}
	a := newAttachment()
	p.attached[a] = struct{}{}
	return a, replayable(p.scroll.snapshot()), nil
}

// detach removes a and ends it.
func (p *ptySession) detach(a *ptyAttachment) {
	p.mu.Lock()
	delete(p.attached, a)
	p.mu.Unlock()
	a.end()
}

// replayable returns the scrollback data as it is replayed: when older
// output was dropped, without a leading partial character and from the
// start of the first line that begins within ptyReplayAlign bytes.
func replayable(data []byte, dropped bool) []byte {
	if !dropped {
		return data
	}
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:]
	}
	if i := bytes.IndexByte(data[:min(len(data), ptyReplayAlign)], '\n'); i >= 0 {
		data = data[i+1:]
	}
	return data
}

// output records b in the scrollback and queues it for every attachment.
func (p *ptySession) output(b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scroll.Write(b)
	for a := range p.attached {
		if !a.push(ptyFrame{wire.KindPTYData, b}) {
			delete(p.attached, a)
		}
	}
}

// finish records the exit code, queues the exit frame for every attachment
// and drops the scrollback.
func (p *ptySession) finish(code int) {
	b, _ := json.Marshal(wire.PTYExit{Code: code})
	p.mu.Lock()
	p.exited, p.code = true, code
	for a := range p.attached {
		a.push(ptyFrame{wire.KindPTYExit, b})
	}
	p.attached = map[*ptyAttachment]struct{}{}
	p.scroll = newRing(1)
	p.mu.Unlock()
	close(p.done)
}

// input writes keyboard input to the shell; writes run one after another.
func (p *ptySession) input(b []byte) error {
	p.inputMu.Lock()
	defer p.inputMu.Unlock()
	_, err := p.con.Write(b)
	return err
}

// resize sets the size of the terminal.
func (p *ptySession) resize(cols, rows int) error {
	if err := p.con.Resize(cols, rows); err != nil {
		return err
	}
	p.mu.Lock()
	p.cols, p.rows = cols, rows
	p.mu.Unlock()
	return nil
}

// info is the pty.list entry of p.
func (p *ptySession) info() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := map[string]any{"job": p.id, "pid": p.pid, "cols": p.cols, "rows": p.rows, "shell": p.shell, "state": "running"}
	if p.rid != "" {
		m["rid"] = p.rid
	}
	switch {
	case p.exited:
		m["state"], m["code"] = "exited", p.code
	case p.con == nil:
		m["state"] = "starting"
	}
	if !p.started.IsZero() {
		m["started"] = p.started.UTC().Format(time.RFC3339)
	}
	return m
}

func (p *ptySession) hasExited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited
}

// ptyTable holds the agent's ptys: the running ones and the ptyExitedKeep
// that ended last.
type ptyTable struct {
	mu    sync.Mutex
	byID  map[string]*ptySession
	order []string
	next  int
}

func newPTYTable() *ptyTable { return &ptyTable{byID: map[string]*ptySession{}} }

// get returns the pty with id, or nil.
func (t *ptyTable) get(id string) *ptySession {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.byID[id]
}

// add assigns p an id and keeps it, forgetting the oldest ended ptys past
// ptyExitedKeep. It fails while maxPTYs ptys run.
func (t *ptyTable) add(p *ptySession) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	running := 0
	var ended []string
	for _, id := range t.order {
		if t.byID[id].hasExited() {
			ended = append(ended, id)
		} else {
			running++
		}
	}
	if running >= maxPTYs {
		return fmt.Errorf("%d shells are running; end one with exec.kill before opening another", running)
	}
	for len(ended) >= ptyExitedKeep {
		t.forget(ended[0])
		ended = ended[1:]
	}
	t.next++
	p.id = fmt.Sprintf("p%s-%d", bootID[:8], t.next)
	t.byID[p.id] = p
	t.order = append(t.order, p.id)
	return nil
}

// forget removes the pty with id; the caller holds t.mu.
func (t *ptyTable) forget(id string) {
	delete(t.byID, id)
	if i := slices.Index(t.order, id); i >= 0 {
		t.order = slices.Delete(t.order, i, i+1)
	}
}

// list returns the ptys in the order they started.
func (t *ptyTable) list() []*ptySession {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*ptySession, 0, len(t.order))
	for _, id := range t.order {
		out = append(out, t.byID[id])
	}
	return out
}

func (s *Server) registerPTY() {
	s.register("pty.list", true, s.ptyList)
}

// ptyList lists the ptys with their job, pid, size, shell, start time,
// state and, once exited, code.
func (s *Server) ptyList(context.Context, proto.Message) (proto.Message, error) {
	items := []any{}
	for _, p := range s.ptys.list() {
		items = append(items, p.info())
	}
	return proto.Message{"ptys": items}, nil
}

// processID returns the process id of p's shell, 0 while it starts.
func (p *ptySession) processID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

// killPTY ends the process tree of p's shell and waits for p to end.
func (s *Server) killPTY(p *ptySession) (proto.Message, error) {
	if !p.hasExited() {
		pid := p.processID()
		if pid == 0 {
			return nil, coded(wire.CodeInProgress, fmt.Sprintf("pty '%s' is still starting; kill it again in a moment", p.id), true)
		}
		_ = killTree(pid)
		select {
		case <-p.done:
		case <-time.After(ptyKillWait):
			return nil, errors.New("the shell did not end after the kill; call pty.list to check its state")
		}
	}
	return proto.Message{"state": "exited"}, nil
}

// ServePTY serves the pty stream st with header h (docs/protocol.md §2,
// §7): h.New starts a shell, h.Job attaches to a running one. The
// acceptance line carries the pty's job and pid; the attached frame and the
// scrollback follow, then the shell's output as it comes and its exit.
// Data frames from the daemon are keyboard input and resize frames set the
// terminal size. The shell keeps running when the stream ends. A header it
// cannot serve gets the refusal line. It closes st.
func (s *Server) ServePTY(ctx context.Context, st PTYStream, h wire.StreamHeader) {
	defer st.Close()
	stop := context.AfterFunc(ctx, func() {
		st.CancelRead()
		st.CancelWrite()
	})
	defer stop()
	p, err := s.ptyOf(h)
	if err != nil {
		refuseTransfer(st, err)
		return
	}
	a, replay, err := p.attach()
	if err != nil {
		refuseTransfer(st, err)
		return
	}
	defer p.detach(a)
	if err := writeLine(st, transferLine(nil, proto.Message{"job": p.id, "pid": p.processID()})); err != nil {
		st.CancelRead()
		return
	}
	written := make(chan struct{})
	go func() {
		defer close(written)
		s.writePTY(st, p, a, replay)
		st.CancelRead()
	}()
	s.readPTY(st, p)
	p.detach(a)
	<-written
}

// writePTY writes the attached frame and replay to st, then the frames
// queued for a until a ends, a write fails or the exit frame is written.
func (s *Server) writePTY(st PTYStream, p *ptySession, a *ptyAttachment, replay []byte) {
	write := func(kind byte, payload []byte) bool {
		_ = st.SetWriteDeadline(time.Now().Add(ptyWriteTimeout))
		return wire.WriteFrame(st, kind, payload) == nil
	}
	attached, _ := json.Marshal(wire.PTYAttached{Job: p.id, PID: p.processID()})
	if !write(wire.KindPTYAttached, attached) {
		return
	}
	for len(replay) > 0 {
		n := min(len(replay), ptyChunk)
		if n < len(replay) {
			if k := completeUTF8Len(replay[:n]); k > 0 {
				n = k
			}
		}
		if !write(wire.KindPTYData, replay[:n]) {
			return
		}
		replay = replay[n:]
	}
	for {
		select {
		case f := <-a.frames:
			a.took(f)
			if !write(f.kind, f.payload) || f.kind == wire.KindPTYExit {
				return
			}
		case <-a.gone:
			for {
				select {
				case f := <-a.frames:
					if f.kind == wire.KindPTYExit {
						write(f.kind, f.payload)
						return
					}
				default:
					return
				}
			}
		}
	}
}

// readPTY applies the frames the daemon writes to st until the stream
// ends: data frames are input, resize frames set the size, other kinds are
// ignored.
func (s *Server) readPTY(st PTYStream, p *ptySession) {
	r := bufio.NewReader(st)
	for {
		kind, payload, err := wire.ReadFrame(r, ptyMaxInput)
		if err != nil {
			return
		}
		switch kind {
		case wire.KindPTYData:
			if err := p.input(payload); err != nil {
				return
			}
		case wire.KindPTYResize:
			var size wire.PTYResize
			if err := json.Unmarshal(payload, &size); err != nil {
				s.opt.Logger.Printf("pty %s: resize frame %q: %v", p.id, cutChars(string(payload), 64), err)
				continue
			}
			if err := p.resize(size.Cols, size.Rows); err != nil {
				s.opt.Logger.Printf("pty %s: resize to %dx%d: %v", p.id, size.Cols, size.Rows, err)
			}
		}
	}
}

// ptyOf returns the pty a stream header names: the one h.Job names, which
// has to be running, or a new one started from h.New.
func (s *Server) ptyOf(h wire.StreamHeader) (*ptySession, error) {
	if !s.opt.AllowExec {
		return nil, coded(wire.CodeCapabilityUnsupported, errExecDisabled, false)
	}
	switch {
	case h.Job != "" && h.New != nil:
		return nil, invalidArgument("a pty header carries job to attach or new to start a shell, not both")
	case h.Job != "":
		p := s.ptys.get(h.Job)
		if p == nil {
			return nil, coded(wire.CodeNotFound, fmt.Sprintf("no pty '%s'; call pty.list", cutChars(h.Job, 64)), false)
		}
		return p, nil
	case h.New != nil:
		return s.startPTY(*h.New)
	}
	return nil, invalidArgument(`a pty header needs job to attach or new to start a shell; send {"s":"pty","new":{"shell":…}}`)
}

// startPTY starts the shell n names in the home folder, or in n.Cwd
// resolved against it, with the child environment and n.Env added, on a
// terminal of n.Cols by n.Rows (80 by 24 when both are 0).
func (s *Server) startPTY(n wire.PTYNew) (*ptySession, error) {
	if n.RID != "" && !validRID(n.RID) {
		return nil, ridError(n.RID)
	}
	cols, rows := n.Cols, n.Rows
	if cols == 0 && rows == 0 {
		cols, rows = ptyDefaultCols, ptyDefaultRows
	}
	if err := pty.CheckSize(cols, rows); err != nil {
		return nil, invalidArgument(err.Error() + "; send the size of the terminal")
	}
	shell := n.Shell
	shells := ptyShells()
	if shell == "" && len(shells) > 0 {
		shell = shells[0]
	}
	if !slices.Contains(shells, shell) {
		if knownPTYShell(shell) {
			return nil, coded(wire.CodeCapabilityUnsupported, fmt.Sprintf("the shell '%s' is not installed on this PC; use one of %v", shell, shells), false)
		}
		return nil, invalidArgument(fmt.Sprintf("unknown shell '%s'; use one of %v", cutChars(shell, 64), shells))
	}
	env, err := childEnvironment()
	if err != nil {
		return nil, err
	}
	if len(n.Env) > 0 {
		keys := make([]string, 0, len(n.Env))
		for k := range n.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env = append(env, k+"="+n.Env[k])
		}
	}
	spec, err := ptyShellSpec(shell, env)
	if err != nil {
		return nil, err
	}
	home := s.homeDir()
	spec.Dir = home
	if n.Cwd != "" {
		spec.Dir = resolveHomePath(home, n.Cwd)
	}
	spec.Cols, spec.Rows = cols, rows
	p := &ptySession{rid: n.RID, shell: shell, cols: cols, rows: rows, scroll: newRing(ptyScrollbackBytes),
		attached: map[*ptyAttachment]struct{}{}, done: make(chan struct{})}
	if err := s.ptys.add(p); err != nil {
		return nil, err
	}
	con, err := pty.Start(spec)
	if err != nil {
		s.ptys.mu.Lock()
		s.ptys.forget(p.id)
		s.ptys.mu.Unlock()
		return nil, fmt.Errorf("cannot start the shell '%s': %v; check cwd and shell", shell, err)
	}
	p.mu.Lock()
	p.con, p.pid, p.started = con, con.Pid(), time.Now()
	p.mu.Unlock()
	go s.runPTY(p)
	return p, nil
}

// runPTY reads p's output until the terminal ends and finishes p when its
// shell exited: the rest of the output is read, the terminal closed, the
// exit frame queued and pty.exited sent to every connection.
func (s *Server) runPTY(p *ptySession) {
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, ptyChunk-utf8.UTFMax)
		var carry []byte
		for {
			n, err := p.con.Read(buf)
			if n > 0 {
				data := append(carry, buf[:n]...)
				k := completeUTF8Len(data)
				if k > 0 {
					p.output(slices.Clone(data[:k]))
				}
				carry = slices.Clone(data[k:])
			}
			if err != nil {
				if len(carry) > 0 {
					p.output(carry)
				}
				return
			}
		}
	}()
	code, err := p.con.Wait()
	if err != nil {
		s.opt.Logger.Printf("pty %s: wait for the shell: %v", p.id, err)
	}
	go p.con.Shutdown()
	select {
	case <-read:
	case <-time.After(ptyDrainWait):
	}
	go p.con.Close()
	select {
	case <-read:
	case <-time.After(ptyDrainWait):
		s.opt.Logger.Printf("pty %s: the terminal did not end after the shell exited", p.id)
	}
	p.finish(code)
	s.emit("pty.exited", proto.Message{"job": p.id, "code": code})
}
