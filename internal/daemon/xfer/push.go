package xfer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

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
