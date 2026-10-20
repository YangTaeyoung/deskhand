package xfer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// chunkSize is the unit in which files are read, sent and written.
	chunkSize = 4 << 20
	// parallelFiles is how many files of a transfer move at once.
	parallelFiles = 4
	// streamIdle bounds each read and write on a put or get stream.
	streamIdle = 30 * time.Second
	// resultWait bounds the wait for the result line of a put.
	resultWait = 2 * time.Minute
	// opWait bounds an op that does not read a whole file.
	opWait = 2 * time.Minute
	// strikesMax is how many attempts of a file may fail without progress
	// on a connection that stays before the file fails.
	strikesMax = 5
	// lineMax bounds a JSON line of a put or get stream.
	lineMax = 64 << 10
)

// hashWait bounds an op that reads a whole file of size bytes on the host.
func hashWait(size int64) time.Duration {
	return opWait + time.Duration(size/(16<<20))*time.Second
}

// result is how a step of a transfer ended.
type result int

const (
	// resDone: the step reached its end; a file is done or failed.
	resDone result = iota
	// resRetry: the step failed for a reason that can pass, such as the
	// connection; the transfer tries again.
	resRetry
	// resStopped: the run was stopped.
	resStopped
)

// streamError is a failure of a put or get stream itself; the transfer goes
// on from the bytes that arrived.
type streamError struct{ err error }

func (e *streamError) Error() string { return "the stream ended: " + e.err.Error() }
func (e *streamError) Unwrap() error { return e.err }

// transient reports whether a step that failed with err can succeed later:
// a stream that broke, a host that does not answer, a busy part file or an
// outcome that is not known yet.
func transient(err error) bool {
	var se *streamError
	if errors.As(err, &se) {
		return true
	}
	var e *apierr.Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Code {
	case wire.CodeOffline, wire.CodeInProgress, wire.CodeTimeout, wire.CodeExecutionOutcomeUnknown:
		return true
	case wire.CodeInternal:
		return e.Retryable
	}
	return false
}

// keepPart reports whether a file that failed with err keeps its part
// file for a resume: after a failure that can pass, one the agent marks
// retryable and a full volume.
func keepPart(err error) bool {
	return transient(err) || apierr.As(err).Retryable || apierr.CodeOf(err) == wire.CodeDiskFull
}

// asAPI returns err as the error object of a file.
func asAPI(err error) *apierr.Error {
	var se *streamError
	if errors.As(err, &se) {
		return apierr.New(wire.CodeInternal, fmt.Sprintf("PC와 주고받던 데이터 스트림이 끊겼습니다(%v). transfer.resume으로 이어서 옮기세요", se.err), true, apierr.NextRetry)
	}
	return apierr.As(err)
}

func hostReady(h *conn.Host) bool {
	s := h.State()
	return s == conn.StateReady || s == conn.StateDegraded
}

// attempt plans the transfer when it has no manifest, makes its folders,
// moves its unfinished files, then removes the source of a move and
// finishes the transfer.
func (t *transfer) attempt(ctx context.Context, h *conn.Host) result {
	t.mu.Lock()
	if t.row.State == StateQueued {
		t.row.State = StateRunning
		t.publishLocked()
	}
	planned := t.row.Planned
	t.mu.Unlock()
	if !planned {
		if err := t.plan(ctx, h); err != nil {
			switch {
			case ctx.Err() != nil:
				return resStopped
			case transient(err):
				return resRetry
			}
			t.mu.Lock()
			t.failPlanLocked(asAPI(err))
			t.mu.Unlock()
			return resDone
		}
	}
	actx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	if r := t.makeFolders(actx, h); r != resDone {
		if ctx.Err() != nil {
			return resStopped
		}
		return r
	}
	retry := t.runFiles(actx, stop, h, t.pending())
	switch {
	case ctx.Err() != nil:
		return resStopped
	case retry:
		return resRetry
	}
	return t.complete(ctx, h)
}

// failPlanLocked fails a transfer whose files could not be listed with e.
func (t *transfer) failPlanLocked(e *apierr.Error) {
	now := time.Now()
	t.row.State, t.row.Outcome, t.row.Error, t.row.FinishedAt = StateFailed, OutcomeFailed, encodeError(e), &now
	t.recordLocked(t.callerLocked(), fmt.Sprintf("%s 실패: %s (%s)", t.verbLocked(), t.routeLocked(), e.Message), model.TimelineFailed)
	t.publishLocked()
}

// pending returns the files of the transfer that are not finished.
func (t *transfer) pending() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []int
	for i, f := range t.files {
		if !f.Dir && f.State != StateDone && f.State != StateFailed {
			out = append(out, i)
		}
	}
	return out
}

// makeFolders creates the folders of a folder transfer at its destination:
// on the host the folders no other folder lies in (fs.mkdir creates the
// ones above), on this Mac every folder. A folder that cannot be created
// fails with the files below it.
func (t *transfer) makeFolders(ctx context.Context, h *conn.Host) result {
	if !t.row.Folder {
		return resDone
	}
	t.mu.Lock()
	dirs := []string{""}
	for _, f := range t.files {
		if f.Dir && f.State != StateFailed {
			dirs = append(dirs, f.Rel)
		}
	}
	t.mu.Unlock()
	if !pushing(t.row.Direction) {
		for _, rel := range dirs {
			p := filepath.Join(t.row.Local, filepath.FromSlash(rel))
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.failFolder(rel, localError(err, fmt.Sprintf("이 Mac에 폴더 %s를 만들지 못했습니다", p), "권한과 경로를 확인한 뒤 transfer.resume으로 다시 시도하세요"))
			}
		}
		return resDone
	}
	for _, rel := range leafFolders(dirs) {
		p := remoteJoin(t.row.Remote, rel, t.row.Sep)
		_, _, err := call(ctx, h, "fs.mkdir", proto.Message{"path": p}, opWait)
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return resStopped
		case transient(err):
			return resRetry
		default:
			t.failFolder(rel, asAPI(err))
		}
	}
	return resDone
}

// leafFolders returns the folders of dirs (relative paths, "" the root)
// that no other of them lies in.
func leafFolders(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		leaf := true
		for _, o := range dirs {
			if o != d && (d == "" || strings.HasPrefix(o, d+"/")) {
				leaf = false
				break
			}
		}
		if leaf {
			out = append(out, d)
		}
	}
	return out
}

// failFolder fails the folder rel ("" the root) and the unfinished files
// below it with e.
func (t *transfer) failFolder(rel string, e *apierr.Error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.files {
		f := &t.files[i]
		below := rel == "" || f.Rel == rel || strings.HasPrefix(f.Rel, rel+"/")
		if below && f.State != StateDone {
			f.State, f.Error = StateFailed, encodeError(e)
			t.dirty[i] = true
		}
	}
	t.changedLocked()
}

// runFiles moves the files seqs, parallelFiles at a time, and reports
// whether one of them has to be tried again. A file that has to be tried
// again while the host does not answer ends the attempt through stop.
func (t *transfer) runFiles(ctx context.Context, stop context.CancelCauseFunc, h *conn.Host, seqs []int) bool {
	var mu sync.Mutex
	retry := false
	work := make(chan int)
	var wg sync.WaitGroup
	for range min(parallelFiles, len(seqs)) {
		wg.Go(func() {
			for seq := range work {
				if t.moveFile(ctx, h, seq) == resRetry {
					mu.Lock()
					retry = true
					mu.Unlock()
					if !hostReady(h) {
						stop(errRetry)
					}
				}
			}
		})
	}
feed:
	for _, seq := range seqs {
		select {
		case work <- seq:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	return retry || errors.Is(context.Cause(ctx), errRetry)
}

// moveFile moves the file seq. A file whose attempts fail strikesMax times
// without progress while the connection stays fails with the last error.
func (t *transfer) moveFile(ctx context.Context, h *conn.Host, seq int) result {
	gen := h.Generation()
	before := t.file(seq).BytesDone
	t.update(seq, func(f *store.TransferFile) { f.State, f.Error = StateRunning, nil })
	var r result
	var err error
	if pushing(t.row.Direction) {
		r, err = t.pushFile(ctx, h, seq)
	} else {
		r, err = t.pullFile(ctx, h, seq)
	}
	if r == resDone {
		return resDone
	}
	requeue := func(f *store.TransferFile) {
		if f.State == StateRunning || f.State == StateVerifying {
			f.State = StateQueued
		}
	}
	if r == resStopped || ctx.Err() != nil {
		t.update(seq, requeue)
		return resStopped
	}
	if h.Generation() == gen && hostReady(h) && t.file(seq).BytesDone <= before {
		t.mu.Lock()
		t.strikes[seq]++
		n := t.strikes[seq]
		t.mu.Unlock()
		if n >= strikesMax {
			t.failFile(seq, asAPI(err))
			return resDone
		}
	}
	t.update(seq, requeue)
	return resRetry
}

// verdict returns how a step of the file seq that failed with err ends:
// stopped when the run ended, retried when err can pass, else done with
// the file failed.
func (t *transfer) verdict(ctx context.Context, seq int, err error) (result, error) {
	switch {
	case ctx.Err() != nil:
		return resStopped, err
	case transient(err):
		return resRetry, err
	}
	t.failFile(seq, asAPI(err))
	return resDone, nil
}

// failFile fails the file seq with e.
func (t *transfer) failFile(seq int, e *apierr.Error) {
	t.update(seq, func(f *store.TransferFile) {
		f.State, f.Outcome, f.Error = StateFailed, "", encodeError(e)
	})
}

// doneFile records that the file seq is at path with outcome.
func (t *transfer) doneFile(seq int, path, outcome string) {
	t.update(seq, func(f *store.TransferFile) {
		f.State, f.Outcome, f.Path, f.Error = StateDone, outcome, path, nil
		f.BytesDone, f.CommitRID, f.Part = f.Size, "", false
	})
	t.mu.Lock()
	delete(t.strikes, seq)
	t.mu.Unlock()
}

// hook helpers.
func (t *transfer) opened(rel string, offset int64) {
	if fn := t.m.opt.Hooks.Opened; fn != nil {
		fn(t.row.TransferID, rel, offset)
	}
}

func (t *transfer) wrote(rel string, done int64) {
	if fn := t.m.opt.Hooks.Wrote; fn != nil {
		fn(t.row.TransferID, rel, done)
	}
}

// readLine reads one JSON line of a put or get stream within wait.
func readLine(st conn.DataStream, br *bufio.Reader, wait time.Duration) (proto.Message, error) {
	_ = st.SetReadDeadline(time.Now().Add(wait))
	line, err := br.ReadSlice('\n')
	if err != nil {
		return nil, &streamError{err}
	}
	var m proto.Message
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, &streamError{fmt.Errorf("the agent wrote %q: %w", line, err)}
	}
	return m, nil
}

// lineError returns the error of a line with ok false.
func lineError(m proto.Message) *apierr.Error {
	code := m.String("code")
	if code == "" {
		code = wire.CodeInternal
	}
	msg := m.String("error")
	if msg == "" {
		msg = "에이전트가 이유를 밝히지 않고 전송 스트림을 거절했습니다. PC의 에이전트 로그를 확인하세요"
	}
	return apierr.New(code, msg, m.Bool("retryable"), "")
}
