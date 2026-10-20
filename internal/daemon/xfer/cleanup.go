package xfer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

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

// cancelFiles cancels the files of a stopped transfer that are not done
// and removes their part files, on the host while it is ready (cancelPart,
// which settles a commit whose outcome is not known).
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
		case h != nil:
			if done, _ := t.cancelPart(ctx, h, seq); done {
				continue
			}
		}
		t.update(seq, func(f *store.TransferFile) {
			if f.State != StateDone {
				f.State, f.Error = StateCancelled, nil
			}
		})
	}
}

// cleanupParts removes, once the host is ready, the part files a cancelled
// transfer left on it (cancelPart).
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
		if _, err := t.cancelPart(ctx, h, seq); err != nil {
			t.m.lg.Printf("transfer %s: remove the part file of %s: %v", t.row.TransferID, t.file(seq).Rel, err)
			return
		}
	}
}
