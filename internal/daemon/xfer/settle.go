package xfer

import (
	"context"
	"fmt"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

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
