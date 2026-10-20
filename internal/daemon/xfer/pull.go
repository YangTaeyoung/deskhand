package xfer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

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
