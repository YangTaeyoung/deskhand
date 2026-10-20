package xfer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

// pushFile sends the file seq to the host: it settles a commit whose answer
// was lost, checks the fingerprint of the source, readies the part file
// with xfer.prepare (resuming from the bytes it holds when they are the
// source's), sends the rest on a put stream while it hashes the file, and
// commits it once the hash is the planned SHA-256.
func (t *transfer) pushFile(ctx context.Context, h *conn.Host, seq int) (result, error) {
	f := t.file(seq)
	if f.CommitRID != "" {
		if r, err, settled := t.settleCommit(ctx, h, seq); settled {
			return r, err
		}
	}
	src := t.localPath(f)
	if f.Refresh {
		size, mtime, sum, e := fingerprint(ctx, src)
		if ctx.Err() != nil {
			return resStopped, ctx.Err()
		}
		if e != nil {
			return t.verdict(ctx, seq, e)
		}
		if err := t.updateNow(seq, func(f *store.TransferFile) {
			f.Size, f.MTime, f.SHA256, f.Refresh, f.BytesDone = size, mtime, sum, false, 0
		}); err != nil {
			return t.verdict(ctx, seq, err)
		}
	}
	f = t.file(seq)
	if e := unchanged(src, f); e != nil {
		_ = t.abortPart(ctx, h, seq)
		return t.verdict(ctx, seq, e)
	}
	for round := 0; ; round++ {
		prep, _, err := callBusy(ctx, h, "xfer.prepare", proto.Message{"transfer_id": t.row.TransferID, "path": t.remotePath(f), "size": f.Size,
			"mtime": f.MTime, "sha256": f.SHA256, "on_conflict": t.row.OnConflict}, hashWait(f.Size))
		if err != nil {
			return t.verdict(ctx, seq, err)
		}
		if err := t.updateNow(seq, func(f *store.TransferFile) { f.Part = true }); err != nil {
			return t.verdict(ctx, seq, err)
		}
		if t.row.OnConflict == "skip" && prep.Bool("dest_exists") {
			if err := t.abortPart(ctx, h, seq); err != nil {
				return t.verdict(ctx, seq, err)
			}
			t.doneFile(seq, t.remotePath(f), OutcomeSkipped)
			return resDone, nil
		}
		have := int64(prep.Int("part_size"))
		hasher := sha256.New()
		if have > 0 {
			same := have <= f.Size
			if same {
				sum, err := hashPrefix(ctx, src, have, hasher)
				if err != nil {
					return t.verdict(ctx, seq, err)
				}
				same = sum == prep.String("part_sha256")
			}
			if !same {
				if round > 0 {
					return resRetry, apierr.New(wire.CodeInternal, "PC의 임시 파일을 비웠는데도 내용이 남아 있습니다. transfer.resume으로 다시 시도하세요", true, apierr.NextRetry)
				}
				if err := t.abortPart(ctx, h, seq); err != nil {
					return t.verdict(ctx, seq, err)
				}
				continue
			}
		}
		t.progress(seq, have, 0)
		if have < f.Size {
			part, err := t.sendPut(ctx, h, seq, src, f, have, hasher)
			if err != nil {
				if ctx.Err() == nil && !keepPart(err) {
					_ = t.abortPart(ctx, h, seq)
				}
				return t.verdict(ctx, seq, err)
			}
			if part != f.Size {
				return resRetry, &streamError{fmt.Errorf("the part file holds %d of %d bytes", part, f.Size)}
			}
		}
		if hex.EncodeToString(hasher.Sum(nil)) != f.SHA256 || unchanged(src, f) != nil {
			_ = t.abortPart(ctx, h, seq)
			return t.verdict(ctx, seq, sourceChanged(src))
		}
		return t.commitPush(ctx, h, seq)
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

// sendPut sends the bytes of the file seq from offset from on a put stream
// (docs/protocol.md §2), feeding them to hasher, and returns the size of
// the part file the agent reports.
func (t *transfer) sendPut(ctx context.Context, h *conn.Host, seq int, src string, f store.TransferFile, from int64, hasher hash.Hash) (int64, error) {
	st, _, err := h.OpenStream(ctx, wire.StreamHeader{S: "put", TransferID: t.row.TransferID, Path: t.remotePath(f), Offset: from, Size: f.Size,
		MTime: f.MTime})
	if err != nil {
		return 0, err
	}
	stop := context.AfterFunc(ctx, func() {
		st.CancelWrite()
		st.CancelRead()
	})
	defer stop()
	defer st.Close()
	br := bufio.NewReaderSize(st, lineMax)
	accept, err := readLine(st, br, streamIdle)
	if err != nil {
		return 0, err
	}
	if accept["ok"] != true {
		return 0, lineError(accept)
	}
	t.opened(f.Rel, from)
	in, err := os.Open(src)
	if err != nil {
		st.CancelWrite()
		return 0, localError(err, fmt.Sprintf("이 Mac의 %s를 열지 못했습니다", src), "권한을 확인한 뒤 transfer.resume으로 다시 시도하세요")
	}
	defer in.Close()
	buf := make([]byte, chunkSize)
	sent := from
	r := io.NewSectionReader(in, from, f.Size-from)
	for sent < f.Size {
		n, rerr := io.ReadFull(r, buf[:min(int64(chunkSize), f.Size-sent)])
		if rerr != nil {
			st.CancelWrite()
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
				return 0, sourceChanged(src)
			}
			return 0, localError(rerr, fmt.Sprintf("이 Mac의 %s를 읽지 못했습니다", src), "디스크 상태를 확인한 뒤 transfer.resume으로 다시 시도하세요")
		}
		hasher.Write(buf[:n])
		_ = st.SetWriteDeadline(time.Now().Add(streamIdle))
		if _, err := st.Write(buf[:n]); err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, &streamError{err}
		}
		sent += int64(n)
		t.progress(seq, sent, int64(n))
		t.wrote(f.Rel, sent)
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
	}
	if err := st.CloseWrite(); err != nil {
		return 0, &streamError{err}
	}
	res, err := readLine(st, br, resultWait)
	if err != nil {
		return 0, err
	}
	part := int64(res.Int("part_size"))
	if res["ok"] != true {
		t.progress(seq, part, 0)
		return part, lineError(res)
	}
	return part, nil
}

// abortPart removes the part file of the file seq on the host with
// xfer.abort.
func (t *transfer) abortPart(ctx context.Context, h *conn.Host, seq int) error {
	f := t.file(seq)
	if !f.Part {
		return nil
	}
	_, _, err := callBusy(ctx, h, "xfer.abort", proto.Message{"transfer_id": t.row.TransferID, "path": t.remotePath(f)}, opWait)
	if err != nil {
		return err
	}
	t.update(seq, func(f *store.TransferFile) { f.Part, f.BytesDone = false, 0 })
	return nil
}

// commitPush commits the part file of the file seq on the host
// (docs/protocol.md §3.1 xfer.commit), each time with a new rid that is
// stored before the request goes out. A commit whose answer is lost keeps
// its rid for settleCommit; one the agent cannot find is settled by the
// SHA-256 of the destination.
func (t *transfer) commitPush(ctx context.Context, h *conn.Host, seq int) (result, error) {
	f := t.file(seq)
	t.update(seq, func(f *store.TransferFile) { f.State = StateVerifying })
	for range 3 {
		rid := conn.NewRID()
		if err := t.updateNow(seq, func(f *store.TransferFile) { f.CommitRID = rid }); err != nil {
			return t.verdict(ctx, seq, err)
		}
		cctx, cancel := context.WithTimeout(ctx, hashWait(f.Size))
		info := conn.CallInfo{}
		res, err := h.Call(cctx, "xfer.commit", proto.Message{"transfer_id": t.row.TransferID, "path": t.remotePath(f), "sha256": f.SHA256,
			"on_conflict": t.row.OnConflict}, conn.CallOpts{RID: rid, Info: &info})
		cancel()
		if err == nil {
			t.doneFile(seq, res.String("path"), res.String("outcome"))
			return resDone, nil
		}
		if ctx.Err() != nil {
			return resStopped, err
		}
		switch apierr.CodeOf(err) {
		case wire.CodeNotFound:
			return t.settleByHash(ctx, h, seq)
		case wire.CodeExecutionOutcomeUnknown:
			if info.Answered {
				continue
			}
			return resRetry, err
		case wire.CodeTimeout:
			return resRetry, err
		case wire.CodeHashMismatch:
			t.update(seq, func(f *store.TransferFile) { f.CommitRID = "" })
			return t.restart(ctx, h, seq, err)
		}
		if transient(err) {
			return resRetry, err
		}
		t.update(seq, func(f *store.TransferFile) { f.CommitRID = "" })
		if !keepPart(err) {
			_ = t.abortPart(ctx, h, seq)
		}
		t.failFile(seq, apierr.As(err))
		return resDone, nil
	}
	t.failFile(seq, apierr.New(wire.CodeExecutionOutcomeUnknown, fmt.Sprintf("PC가 %s를 반영하는 중에 거듭 멈춰 반영됐는지 확인하지 못했습니다. PC에서 그 파일을 확인한 뒤 transfer.resume으로 다시 시도하세요",
		t.remotePath(f)), false, apierr.NextCheckState))
	return resDone, nil
}

// settleCommit settles a commit of the file seq whose answer was lost: by
// its rid with req.status (conn.Host.Resolve), and when the agent has no
// record of it, with a commit under a new rid, which the agent answers from
// its receipt. settled is false when the agent refused that commit, which
// therefore did not happen; the file then goes on from its part file.
func (t *transfer) settleCommit(ctx context.Context, h *conn.Host, seq int) (result, error, bool) {
	f := t.file(seq)
	rctx, cancel := context.WithTimeout(ctx, hashWait(f.Size))
	res, err := h.Resolve(rctx, f.CommitRID)
	cancel()
	switch {
	case err == nil:
		t.doneFile(seq, res.String("path"), res.String("outcome"))
		return resDone, nil, true
	case ctx.Err() != nil:
		return resStopped, err, true
	case apierr.CodeOf(err) == wire.CodeExecutionOutcomeUnknown:
		r, err := t.commitPush(ctx, h, seq)
		return r, err, true
	}
	t.update(seq, func(f *store.TransferFile) { f.CommitRID = "" })
	return 0, nil, false
}

// settleByHash settles a commit of the file seq that the agent cannot find
// (no part file and no receipt): the commit happened when the destination,
// or under rename one of its numbered names, has the file's SHA-256 (the
// last such name), and the file fails otherwise.
func (t *transfer) settleByHash(ctx context.Context, h *conn.Host, seq int) (result, error) {
	f := t.file(seq)
	dst := t.remotePath(f)
	match := ""
	for k := 0; k <= wire.RenameMax; k++ {
		name := dst
		if k > 0 {
			name = remoteNumbered(dst, t.row.Sep, k)
		}
		res, _, err := call(ctx, h, "fs.hash", proto.Message{"path": name}, hashWait(f.Size))
		if err != nil {
			if c := apierr.CodeOf(err); c == wire.CodeNotFound || c == wire.CodeInvalidArgument {
				break
			}
			return t.verdict(ctx, seq, err)
		}
		if res.String("sha256") == f.SHA256 {
			match = name
		}
		if t.row.OnConflict != "rename" {
			break
		}
	}
	switch {
	case match == dst:
		t.doneFile(seq, match, OutcomeWritten)
		return resDone, nil
	case match != "":
		t.doneFile(seq, match, OutcomeRenamed)
		return resDone, nil
	}
	t.update(seq, func(f *store.TransferFile) { f.CommitRID, f.Part, f.BytesDone = "", false, 0 })
	t.failFile(seq, apierr.New(wire.CodeExecutionOutcomeUnknown, fmt.Sprintf("PC에서 %s의 임시 파일이 사라졌고 대상의 내용이 원본과 달라 반영됐는지 확인하지 못했습니다. PC에서 %s를 확인한 뒤 transfer.resume으로 다시 보내세요",
		f.Rel, dst), false, apierr.NextCheckState))
	return resDone, nil
}

// restart sends the file seq again from its start after its check failed
// with err once; a second failure fails the file with err.
func (t *transfer) restart(ctx context.Context, h *conn.Host, seq int, err error) (result, error) {
	t.mu.Lock()
	t.restarts[seq]++
	n := t.restarts[seq]
	t.mu.Unlock()
	if pushing(t.row.Direction) {
		if aerr := t.abortPart(ctx, h, seq); aerr != nil {
			return t.verdict(ctx, seq, aerr)
		}
	} else {
		t.dropLocalPart(seq)
	}
	if n > 1 {
		t.failFile(seq, asAPI(err))
		return resDone, nil
	}
	if pushing(t.row.Direction) {
		return t.pushFile(ctx, h, seq)
	}
	return t.pullFile(ctx, h, seq)
}

// dropLocalPart removes the part file of the file seq of a pull.
func (t *transfer) dropLocalPart(seq int) {
	f := t.file(seq)
	_ = os.Remove(partPath(t.localPath(f), t.row.TransferID))
	t.update(seq, func(f *store.TransferFile) { f.Part, f.BytesDone, f.SHA256 = false, 0, "" })
}

// changedRemote reports whether the answer st of fs.stat shows that the
// source of the file f on the host is no longer the one of its fingerprint.
func changedRemote(st proto.Message, f store.TransferFile) bool {
	return !st.Bool("exists") || st.Bool("dir") || int64(st.Int("size")) != f.Size || !sameSecond(st.String("mtime"), f.MTime)
}

// pullFile receives the file seq from the host: it settles a commit the
// daemon may have made before it stopped, applies on_conflict before any
// byte moves, appends the rest of the file to its part file on a get
// stream, checks the part against the SHA-256 the agent computes of the
// source and puts it at its destination.
func (t *transfer) pullFile(ctx context.Context, h *conn.Host, seq int) (result, error) {
	f := t.file(seq)
	src, target := t.remotePath(f), t.localPath(f)
	part := partPath(target, t.row.TransferID)
	policy := t.row.OnConflict
	if f.Refresh {
		st, _, err := call(ctx, h, "fs.stat", proto.Message{"path": src}, statWait)
		if err != nil {
			return t.verdict(ctx, seq, err)
		}
		switch {
		case !st.Bool("exists"):
			return t.verdict(ctx, seq, apierr.NotFound(fmt.Sprintf("PC에 원본 %s가 없습니다. 경로를 확인한 뒤 새 전송을 시작하세요", src)))
		case st.Bool("link"):
			return t.verdict(ctx, seq, linkRefused(src))
		case st.Bool("dir"):
			return t.verdict(ctx, seq, sourceChanged(src))
		}
		_ = os.Remove(part)
		if err := t.updateNow(seq, func(f *store.TransferFile) {
			f.Size, f.MTime, f.SHA256, f.Refresh, f.BytesDone, f.Part = int64(st.Int("size")), st.String("mtime"), "", false, 0, false
		}); err != nil {
			return t.verdict(ctx, seq, err)
		}
		f = t.file(seq)
	}
	if _, err := os.Lstat(part); f.SHA256 != "" && errors.Is(err, fs.ErrNotExist) {
		if dest, outcome, ok := settleLocal(ctx, target, policy, f); ok {
			t.doneFile(seq, dest, outcome)
			return resDone, nil
		}
		t.update(seq, func(f *store.TransferFile) { f.SHA256, f.BytesDone = "", 0 })
		f = t.file(seq)
	}
	exists, existing, cerr := conflict(target, policy)
	if cerr != nil {
		t.dropLocalPart(seq)
		return t.verdict(ctx, seq, cerr)
	}
	if exists && policy == "skip" {
		t.dropLocalPart(seq)
		t.doneFile(seq, existing, OutcomeSkipped)
		return resDone, nil
	}
	pf, have, e := openPart(part)
	if e != nil {
		return t.verdict(ctx, seq, e)
	}
	if have > f.Size {
		if err := pf.Truncate(0); err != nil {
			pf.Close()
			return t.verdict(ctx, seq, localError(err, fmt.Sprintf("임시 파일 %s를 비우지 못했습니다", part), "transfer.resume으로 다시 시도하세요"))
		}
		_, _ = pf.Seek(0, io.SeekStart)
		have = 0
	}
	t.update(seq, func(f *store.TransferFile) { f.Part = true })
	t.progress(seq, have, 0)
	var gerr error
	if have < f.Size || f.Size == 0 {
		gerr = t.receiveGet(ctx, h, seq, src, pf, have)
	}
	serr := pf.Sync()
	if cerr := pf.Close(); serr == nil {
		serr = cerr
	}
	switch {
	case gerr != nil:
		if ctx.Err() == nil && !keepPart(gerr) {
			t.dropLocalPart(seq)
		}
		return t.verdict(ctx, seq, gerr)
	case serr != nil:
		return t.verdict(ctx, seq, localError(serr, fmt.Sprintf("임시 파일 %s를 디스크에 쓰지 못했습니다", part), "여유 공간을 확인한 뒤 transfer.resume으로 다시 시도하세요"))
	}
	return t.verifyPull(ctx, h, seq, src, target, part)
}

// verifyPull checks the received part file of the file seq against its
// source on the host, whose size and modification time have to be those of
// the fingerprint and whose SHA-256 the agent computes, and puts it at its
// destination: a source that changed is a TRANSFER_CONFLICT, a part whose
// SHA-256 differs is received again once.
func (t *transfer) verifyPull(ctx context.Context, h *conn.Host, seq int, src, target, part string) (result, error) {
	f := t.file(seq)
	t.update(seq, func(f *store.TransferFile) { f.State = StateVerifying })
	st, _, err := call(ctx, h, "fs.stat", proto.Message{"path": src, "sha256": true}, hashWait(f.Size))
	if err != nil {
		return t.verdict(ctx, seq, err)
	}
	if changedRemote(st, f) {
		t.dropLocalPart(seq)
		return t.verdict(ctx, seq, sourceChanged(src))
	}
	sum := st.String("sha256")
	if err := t.updateNow(seq, func(f *store.TransferFile) { f.SHA256 = sum }); err != nil {
		return t.verdict(ctx, seq, err)
	}
	local, err := hashLocal(ctx, part)
	if err != nil {
		return t.verdict(ctx, seq, err)
	}
	if local != sum {
		return t.restart(ctx, h, seq, apierr.New(wire.CodeHashMismatch,
			fmt.Sprintf("PC의 %s에서 받은 내용이 원본과 거듭 달랐습니다. 네트워크와 디스크 상태를 확인한 뒤 transfer.resume으로 다시 받으세요", src), false, apierr.NextRetry))
	}
	if mt, err := time.Parse(time.RFC3339Nano, f.MTime); err == nil {
		_ = os.Chtimes(part, mt, mt)
	}
	dest, outcome, cerr := commitLocal(part, target, t.row.OnConflict)
	if cerr != nil {
		t.dropLocalPart(seq)
		return t.verdict(ctx, seq, cerr)
	}
	t.doneFile(seq, dest, outcome)
	return resDone, nil
}

// receiveGet appends the bytes of the file seq from offset have to the part
// file pf, read from a get stream (docs/protocol.md §2) whose acceptance
// has to show the size and modification time of the fingerprint.
func (t *transfer) receiveGet(ctx context.Context, h *conn.Host, seq int, src string, pf *os.File, have int64) error {
	f := t.file(seq)
	st, _, err := h.OpenStream(ctx, wire.StreamHeader{S: "get", Path: src, Offset: have})
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() {
		st.CancelRead()
		st.CancelWrite()
	})
	defer stop()
	defer st.Close()
	_ = st.CloseWrite()
	br := bufio.NewReaderSize(st, lineMax)
	accept, err := readLine(st, br, streamIdle)
	if err != nil {
		return err
	}
	if accept["ok"] != true {
		return lineError(accept)
	}
	mtime := accept.String("mtime")
	if int64(accept.Int("size")) != f.Size || !sameSecond(mtime, f.MTime) || have+int64(accept.Int("length")) != f.Size {
		return sourceChanged(src)
	}
	if mtime != f.MTime {
		t.update(seq, func(f *store.TransferFile) { f.MTime = mtime })
	}
	t.opened(f.Rel, have)
	buf := make([]byte, chunkSize)
	done := have
	for done < f.Size {
		_ = st.SetReadDeadline(time.Now().Add(streamIdle))
		n, rerr := io.ReadFull(br, buf[:min(int64(chunkSize), f.Size-done)])
		if n > 0 {
			if _, err := pf.Write(buf[:n]); err != nil {
				return localError(err, fmt.Sprintf("이 Mac의 임시 파일 %s에 쓰지 못했습니다", pf.Name()), "여유 공간을 확인한 뒤 transfer.resume으로 이어서 받으세요")
			}
			done += int64(n)
			t.progress(seq, done, int64(n))
			t.wrote(f.Rel, done)
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return &streamError{rerr}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// complete finishes a transfer whose files are all finished; a move whose
// files all moved removes its source first.
func (t *transfer) complete(ctx context.Context, h *conn.Host) result {
	t.mu.Lock()
	var files []store.TransferFile
	failed, skipped := false, 0
	for _, f := range t.files {
		if f.State == StateFailed {
			failed = true
		}
		if !f.Dir {
			files = append(files, f)
			if f.Outcome == OutcomeSkipped {
				skipped++
			}
		}
	}
	t.mu.Unlock()
	var retained *apierr.Error
	switch {
	case !moving(t.row.Direction) || failed:
	case len(files) > 0 && skipped == len(files):
	case skipped > 0:
		retained = apierr.New(wire.CodeTransferConflict, fmt.Sprintf("건너뛴 파일 %d개가 있어 원본을 지우지 않고 남겼습니다. 건너뛴 파일을 확인한 뒤 원본을 직접 정리하세요", skipped),
			false, apierr.NextCheckState)
	case pushing(t.row.Direction):
		t.setState(StateVerifying)
		retained = t.removeLocalSource(files)
	default:
		t.setState(StateVerifying)
		r, e := t.removeRemoteSource(ctx, h, files)
		if r != resDone {
			return r
		}
		retained = e
	}
	if ctx.Err() != nil {
		return resStopped
	}
	t.mu.Lock()
	t.finishLocked(retained)
	t.mu.Unlock()
	return resDone
}

// removeRemoteSource moves the source of a move on the host to its
// Recycle Bin (fs.trash) after its files moved: a file whose size and
// modification time are those of its fingerprint, or a folder whose
// entries are those of the plan. It returns why the source stays; a source
// that is gone is removed.
func (t *transfer) removeRemoteSource(ctx context.Context, h *conn.Host, files []store.TransferFile) (result, *apierr.Error) {
	src := t.row.Remote
	kept := func(e *apierr.Error) *apierr.Error {
		return apierr.New(e.Code, "이 Mac에 받은 파일은 검증을 마쳤지만 PC의 원본을 휴지통으로 옮기지 않고 남겼습니다. "+e.Message, false, apierr.NextCheckState)
	}
	fail := func(err error) (result, *apierr.Error) {
		switch {
		case ctx.Err() != nil:
			return resStopped, nil
		case transient(err):
			return resRetry, nil
		}
		return resDone, kept(asAPI(err))
	}
	if !t.row.Folder {
		st, _, err := call(ctx, h, "fs.stat", proto.Message{"path": src}, statWait)
		if err != nil {
			return fail(err)
		}
		if !st.Bool("exists") {
			return resDone, nil
		}
		if changedRemote(st, files[0]) {
			return resDone, kept(sourceChanged(src))
		}
	} else {
		entries, err := listRemote(ctx, h, src, t.row.Sep)
		if apierr.CodeOf(err) == wire.CodeNotFound {
			return resDone, nil
		}
		if err != nil {
			return fail(err)
		}
		if e := t.samePlan(entries); e != nil {
			return resDone, kept(e)
		}
	}
	for range 2 {
		_, info, err := call(ctx, h, "fs.trash", proto.Message{"path": src}, opWait)
		if err == nil {
			return resDone, nil
		}
		code := apierr.CodeOf(err)
		if ctx.Err() == nil && (code == wire.CodeExecutionOutcomeUnknown || code == wire.CodeTimeout && info.Sent) {
			if st, _, serr := call(ctx, h, "fs.stat", proto.Message{"path": src}, statWait); serr == nil && !st.Bool("exists") {
				return resDone, nil
			}
			continue
		}
		return fail(err)
	}
	return resDone, kept(apierr.New(wire.CodeExecutionOutcomeUnknown, fmt.Sprintf("PC의 %s를 휴지통으로 옮겼는지 확인하지 못했습니다. PC에서 원본을 확인하세요", src), false, apierr.NextCheckState))
}

// samePlan returns nil when the entries of the source folder on the host
// are those of the plan with the files unchanged, and the difference
// otherwise.
func (t *transfer) samePlan(entries []remoteEntry) *apierr.Error {
	t.mu.Lock()
	planned := make(map[string]store.TransferFile, len(t.files))
	for _, f := range t.files {
		planned[f.Rel] = f
	}
	t.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	for _, e := range entries {
		f, ok := planned[e.rel]
		full := remoteJoin(t.row.Remote, e.rel, t.row.Sep)
		switch {
		case !ok:
			return apierr.New(wire.CodeTransferConflict, fmt.Sprintf("원본 폴더에 옮기지 않은 %s가 생겼습니다. PC에서 원본 폴더를 확인한 뒤 직접 정리하세요", full), false, "")
		case e.link || e.dir != f.Dir || !e.dir && (e.size != f.Size || !sameSecond(e.mtime, f.MTime)):
			return sourceChanged(full)
		}
	}
	return nil
}

// cancelFiles cancels the files of a stopped transfer that are not done:
// a commit whose answer was lost is settled first, and the part files are
// removed, on the host while it is ready.
func (t *transfer) cancelFiles() {
	t.mu.Lock()
	var seqs []int
	for i, f := range t.files {
		if !f.Dir && f.State != StateDone {
			seqs = append(seqs, i)
		}
	}
	t.mu.Unlock()
	var h *conn.Host
	if hc := t.m.opt.Conns.Get(t.row.HostID); hc != nil && hostReady(hc) {
		h = hc
	}
	ctx, cancel := context.WithTimeout(t.m.base, settleWait)
	defer cancel()
	for _, seq := range seqs {
		f := t.file(seq)
		switch {
		case !pushing(t.row.Direction):
			if _, err := os.Lstat(partPath(t.localPath(f), t.row.TransferID)); f.SHA256 != "" && errors.Is(err, fs.ErrNotExist) {
				if dest, outcome, ok := settleLocal(ctx, t.localPath(f), t.row.OnConflict, f); ok {
					t.doneFile(seq, dest, outcome)
					continue
				}
			}
			t.dropLocalPart(seq)
		case h != nil && f.CommitRID != "":
			if res, err := h.Resolve(ctx, f.CommitRID); err == nil {
				t.doneFile(seq, res.String("path"), res.String("outcome"))
				continue
			}
			_ = t.abortPart(ctx, h, seq)
		case h != nil:
			_ = t.abortPart(ctx, h, seq)
		}
		t.update(seq, func(f *store.TransferFile) {
			if f.State != StateDone {
				f.State, f.Error = StateCancelled, nil
			}
		})
	}
}

// cleanupParts removes, once the host is ready, the part files a cancelled
// transfer left on it.
func (t *transfer) cleanupParts(ctx context.Context) {
	h, err := t.readyHost(ctx)
	if err != nil {
		return
	}
	t.mu.Lock()
	var seqs []int
	for i, f := range t.files {
		if !f.Dir && f.Part && f.State != StateDone {
			seqs = append(seqs, i)
		}
	}
	t.mu.Unlock()
	for _, seq := range seqs {
		if f := t.file(seq); f.CommitRID != "" {
			rctx, cancel := context.WithTimeout(ctx, settleWait)
			res, err := h.Resolve(rctx, f.CommitRID)
			cancel()
			if err == nil {
				t.doneFile(seq, res.String("path"), res.String("outcome"))
				continue
			}
		}
		if err := t.abortPart(ctx, h, seq); err != nil {
			t.m.lg.Printf("transfer %s: remove the part file of %s: %v", t.row.TransferID, t.file(seq).Rel, err)
			return
		}
	}
}
