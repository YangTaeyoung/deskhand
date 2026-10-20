package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/YangTaeyoung/deskhand/internal/proto"
)

var (
	jobBufferBytes = 1 << 20
	maxJobs        = 64

	stdinWriteTimeout = 10 * time.Second
)

const maxReadBytes = 262144

// ring keeps the newest max bytes and counts every byte written. Once full,
// buf holds max bytes and the oldest one is at head.
type ring struct {
	mu    sync.Mutex
	buf   []byte
	head  int
	max   int
	total int
}

func newRing(max int) *ring { return &ring{max: max} }

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	r.total += n
	if n >= r.max {
		r.buf = append(r.buf[:0], p[n-r.max:]...)
		r.head = 0
		return n, nil
	}
	if room := r.max - len(r.buf); room > 0 {
		k := min(room, len(p))
		r.buf = append(r.buf, p[:k]...)
		p = p[k:]
	}
	for len(p) > 0 {
		k := copy(r.buf[r.head:], p)
		p = p[k:]
		r.head = (r.head + k) % r.max
	}
	return n, nil
}

// slice returns a copy of the retained bytes from position a to b, counted
// from the oldest retained byte.
func (r *ring) slice(a, b int) []byte {
	out := make([]byte, 0, b-a)
	for a < b {
		i := (r.head + a) % len(r.buf)
		end := min(len(r.buf), i+b-a)
		out = append(out, r.buf[i:end]...)
		a += end - i
	}
	return out
}

// ReadFrom returns up to max bytes starting at the absolute offset. When the
// offset lies before the retained window, reading starts at the oldest
// retained byte and dropped is true.
func (r *ring) ReadFrom(offset, max int) (data []byte, next int, dropped bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	start := r.total - len(r.buf)
	if offset < start {
		offset, dropped = start, true
	}
	if offset > r.total {
		offset = r.total
	}
	end := r.total
	if max >= 0 && offset+max < end {
		end = offset + max
	}
	return r.slice(offset-start, end-start), end, dropped
}

// snapshot returns the retained bytes and whether older bytes were dropped.
func (r *ring) snapshot() ([]byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.slice(0, len(r.buf)), r.total > len(r.buf)
}

// tail returns the retained bytes and the count of every byte written.
func (r *ring) tail() ([]byte, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.slice(0, len(r.buf)), r.total
}

// restoredRing returns a ring of size bytes holding the last size bytes of
// data as the newest of total bytes written.
func restoredRing(size int, data []byte, total int) *ring {
	r := newRing(size)
	if len(data) > size {
		data = data[len(data)-size:]
	}
	r.buf = append(r.buf, data...)
	r.total = max(total, len(data))
	return r
}

func (r *ring) written() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}

type job struct {
	id      string
	pid     int
	cmdline string
	started time.Time
	out     *ring
	err     *ring
	done    chan struct{}

	inputMu  sync.Mutex
	mu       sync.Mutex
	stdin    io.WriteCloser
	stdinOff bool
	running  bool // the process was started
	exited   bool
	code     int
	// lost is set for a job that was running when the agent stopped; it
	// counts as exited without a code.
	lost bool

	// saveMu orders the writes of the job's files; exitSaved is set once
	// the files of its exit are written.
	saveMu    sync.Mutex
	exitSaved bool
}

// markStarted records the pid of the started process; from then on the job
// is listed and found by its id.
func (j *job) markStarted(pid int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pid, j.running = pid, true
}

func (j *job) isStarted() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.running
}

func (j *job) processID() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.pid
}

func (j *job) state() (exited bool, code int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.exited, j.code
}

// stateName returns "running", "exited" or "lost".
func (j *job) stateName() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case j.lost:
		return "lost"
	case j.exited:
		return "exited"
	}
	return "running"
}

func (j *job) closeStdin() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.stdinOff {
		j.stdinOff = true
		if j.stdin != nil {
			_ = j.stdin.Close()
		}
	}
}

type jobTable struct {
	mu    sync.Mutex
	jobs  map[string]*job
	order []string
	next  int
	// store keeps the jobs on disk; nil keeps them in memory only.
	store *jobStore
}

func newJobTable() *jobTable { return &jobTable{jobs: map[string]*job{}} }

func (t *jobTable) get(id string) (*job, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	j := t.jobs[id]
	if j == nil || !j.isStarted() {
		return nil, fmt.Errorf("no job '%s'; call exec.list", id)
	}
	return j, nil
}

// reserve assigns the job an id and a table slot, forgetting the oldest
// exited jobs to stay within maxJobs. The job is found and listed once
// markStarted ran.
func (t *jobTable) reserve(j *job) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(t.order) >= maxJobs {
		evicted := false
		for i, id := range t.order {
			if exited, _ := t.jobs[id].state(); exited {
				delete(t.jobs, id)
				t.order = append(t.order[:i], t.order[i+1:]...)
				t.store.remove(id)
				evicted = true
				break
			}
		}
		if !evicted {
			return fmt.Errorf("%d jobs are running; stop one with exec.kill", len(t.order))
		}
	}
	t.next++
	j.id = fmt.Sprintf("j%d", t.next)
	t.jobs[j.id] = j
	t.order = append(t.order, j.id)
	return nil
}

// release gives a reserved slot back.
func (t *jobTable) release(j *job) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.jobs, j.id)
	for i, id := range t.order {
		if id == j.id {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
	if j.id == fmt.Sprintf("j%d", t.next) {
		t.next--
	}
}

func (t *jobTable) list() []*job {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*job, 0, len(t.order))
	for _, id := range t.order {
		if j := t.jobs[id]; j.isStarted() {
			out = append(out, j)
		}
	}
	return out
}

func (s *Server) registerJobs() {
	s.register("exec.start", true, s.execStart)
	s.register("exec.read", true, s.execRead)
	s.register("exec.write", true, s.execWrite)
	s.register("exec.kill", true, s.execKill)
	s.register("exec.list", true, s.execList)
}

func (s *Server) execStart(_ context.Context, req proto.Message) (proto.Message, error) {
	c, err := s.newCommand(req)
	if err != nil {
		return nil, err
	}
	j := &job{
		cmdline: commandLine(req),
		started: time.Now(),
		out:     newRing(jobBufferBytes),
		err:     newRing(jobBufferBytes),
		done:    make(chan struct{}),
	}
	var flushOutput func()
	c.Stdout, c.Stderr, flushOutput = consoleOutput(consoleDecoder(), j.out, j.err)
	if err := s.jobs.reserve(j); err != nil {
		return nil, err
	}
	store := s.jobs.store
	if err := store.save(j, nil); err != nil {
		s.jobs.release(j)
		store.remove(j.id)
		return nil, fmt.Errorf("the job was not started because it could not be recorded (%v); check that the agent's data folder is writable", err)
	}
	if j.stdin, err = c.StdinPipe(); err != nil {
		s.jobs.release(j)
		store.remove(j.id)
		return nil, startError(err)
	}
	if err := c.Start(); err != nil {
		s.jobs.release(j)
		store.remove(j.id)
		return nil, startError(err)
	}
	j.markStarted(c.Process.Pid)
	s.saveJob(j, nil)
	go s.flushJob(j)
	go func() {
		_ = c.Wait()
		flushOutput()
		code := -1
		if c.ProcessState != nil {
			code = c.ProcessState.ExitCode()
		}
		s.saveJob(j, &code)
		j.mu.Lock()
		j.exited, j.code = true, code
		j.mu.Unlock()
		close(j.done)
		s.emit("job.exited", proto.Message{"job": j.id, "code": code})
	}()
	return proto.Message{"job": j.id, "pid": j.processID()}, nil
}

func (s *Server) execRead(_ context.Context, req proto.Message) (proto.Message, error) {
	j, err := s.jobs.get(req.String("job"))
	if err != nil {
		return nil, err
	}
	limit := req.Int("max")
	if limit <= 0 || limit > maxReadBytes {
		limit = maxReadBytes
	}
	if limit < utf8.UTFMax {
		limit = utf8.UTFMax
	}
	exited, code := j.state()
	state := j.stateName()
	out, outNext, outDropped := j.out.ReadFrom(req.Int("out_offset"), limit)
	errOut, errNext, errDropped := j.err.ReadFrom(req.Int("err_offset"), limit)
	finished := exited && outNext == j.out.written() && errNext == j.err.written()
	if !finished {
		outNext -= len(out) - completeUTF8Len(out)
		out = out[:completeUTF8Len(out)]
		errNext -= len(errOut) - completeUTF8Len(errOut)
		errOut = errOut[:completeUTF8Len(errOut)]
	}
	res := proto.Message{
		"state":    state,
		"pid":      j.processID(),
		"out":      text(out),
		"err":      text(errOut),
		"out_next": outNext,
		"err_next": errNext,
		"dropped":  outDropped || errDropped,
	}
	if state == "exited" {
		res["code"] = code
	}
	return res, nil
}

// writeInput writes data to the job's stdin and then closes it when eof is
// set. Calls for one job run one after another.
func (j *job) writeInput(data string, eof bool) error {
	j.inputMu.Lock()
	defer j.inputMu.Unlock()
	if data != "" {
		j.mu.Lock()
		closed := j.stdinOff
		j.mu.Unlock()
		if closed {
			return fmt.Errorf("stdin of job '%s' is closed; start a new job with exec.start", j.id)
		}
		if _, err := io.WriteString(j.stdin, data); err != nil {
			return fmt.Errorf("cannot write to job '%s': %v; call exec.list to check its state", j.id, err)
		}
	}
	if eof {
		j.closeStdin()
	}
	return nil
}

func (s *Server) execWrite(_ context.Context, req proto.Message) (proto.Message, error) {
	j, err := s.jobs.get(req.String("job"))
	if err != nil {
		return nil, err
	}
	switch j.stateName() {
	case "lost":
		return nil, fmt.Errorf("job '%s' was running when deskhand-agent stopped and has no input any more; start a new job with exec.start", j.id)
	case "exited":
		return nil, fmt.Errorf("job '%s' has exited; start a new job with exec.start", j.id)
	}
	data, eof := req.String("data"), req.Bool("eof")
	result := make(chan error, 1)
	go func() { result <- j.writeInput(data, eof) }()
	select {
	case err := <-result:
		if err != nil {
			return nil, err
		}
	case <-time.After(stdinWriteTimeout):
		return nil, fmt.Errorf("job '%s' is not reading its input; the data may still reach it later, so exec.kill it or wait before writing again", j.id)
	}
	return proto.Message{}, nil
}

func (s *Server) execKill(_ context.Context, req proto.Message) (proto.Message, error) {
	j, err := s.jobs.get(req.String("job"))
	if err != nil {
		return nil, err
	}
	if exited, _ := j.state(); !exited {
		_ = killTree(j.processID())
		select {
		case <-j.done:
		case <-time.After(5 * time.Second):
			return nil, errors.New("job did not exit after the kill; call exec.list to check its state")
		}
	}
	return proto.Message{"state": j.stateName()}, nil
}

func (s *Server) execList(context.Context, proto.Message) (proto.Message, error) {
	jobs := []any{}
	for _, j := range s.jobs.list() {
		_, code := j.state()
		state := j.stateName()
		m := map[string]any{
			"job":     j.id,
			"pid":     j.processID(),
			"state":   state,
			"cmd":     cutChars(j.cmdline, execListCmdChars),
			"started": j.started.Format(time.RFC3339),
		}
		if state == "exited" {
			m["code"] = code
		}
		jobs = append(jobs, m)
	}
	return proto.Message{"jobs": jobs}, nil
}
