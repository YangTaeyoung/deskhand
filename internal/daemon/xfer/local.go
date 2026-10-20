package xfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// partInfix joins the destination and the transfer id in the name of a
// part file (docs/protocol.md §3.1).
const partInfix = ".deskhand-part-"

// partPath returns the part file of the transfer id for the destination
// target on this Mac.
func partPath(target, id string) string { return target + partInfix + id }

// localError returns the error of a failed file operation on this Mac:
// what failed, its cause and next, what to do.
func localError(err error, what, next string) *apierr.Error {
	code := wire.CodeInternal
	switch {
	case errors.Is(err, fs.ErrNotExist):
		code = wire.CodeNotFound
	case errors.Is(err, fs.ErrPermission):
		code = wire.CodePermissionDenied
	case errors.Is(err, syscall.ENOSPC):
		code = wire.CodeDiskFull
	}
	cause := err
	var pe *fs.PathError
	if errors.As(err, &pe) {
		cause = pe.Err
	}
	return apierr.New(code, fmt.Sprintf("%s(%v). %s", what, cause, next), false, "")
}

// sourceChanged is the error of a file whose source changed after its
// fingerprint was taken.
func sourceChanged(path string) *apierr.Error {
	return apierr.New(wire.CodeTransferConflict,
		fmt.Sprintf("원본 %s가 전송 중에 바뀌었습니다. 대상은 그대로이니 원본을 확인한 뒤 transfer.resume으로 지금 내용을 보내세요", path), false, apierr.NextRetry)
}

// fingerprint returns the size, modification time and SHA-256 of the
// regular file path on this Mac, read until ctx ends.
func fingerprint(ctx context.Context, path string) (int64, string, string, *apierr.Error) {
	li, err := os.Lstat(path)
	if err != nil {
		return 0, "", "", localError(err, fmt.Sprintf("이 Mac의 %s를 읽지 못했습니다", path), "경로와 권한을 확인한 뒤 transfer.resume으로 다시 시도하세요")
	}
	switch {
	case li.Mode()&fs.ModeSymlink != 0:
		return 0, "", "", linkRefused(path)
	case !li.Mode().IsRegular():
		return 0, "", "", specialRefused(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, "", "", localError(err, fmt.Sprintf("이 Mac의 %s를 열지 못했습니다", path), "권한을 확인한 뒤 transfer.resume으로 다시 시도하세요")
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, "", "", localError(err, fmt.Sprintf("이 Mac의 %s를 읽지 못했습니다", path), "transfer.resume으로 다시 시도하세요")
	}
	h := sha256.New()
	if _, err := io.CopyBuffer(h, ctxReader{ctx, f}, make([]byte, chunkSize)); err != nil {
		if ctx.Err() != nil {
			return 0, "", "", apierr.New(wire.CodeTimeout, "전송이 멈춰 원본을 끝까지 읽지 않았습니다. transfer.resume으로 이어 가세요", true, apierr.NextRetry)
		}
		return 0, "", "", localError(err, fmt.Sprintf("이 Mac의 %s를 읽지 못했습니다", path), "디스크 상태를 확인한 뒤 transfer.resume으로 다시 시도하세요")
	}
	return fi.Size(), fileTime(fi.ModTime()), hex.EncodeToString(h.Sum(nil)), nil
}

// ctxReader reads r until ctx ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// unchanged returns nil when the file path on this Mac still has the size
// and modification time of the fingerprint of f, and the error of its
// change otherwise.
func unchanged(path string, f store.TransferFile) *apierr.Error {
	fi, err := os.Lstat(path)
	if err != nil {
		return localError(err, fmt.Sprintf("이 Mac의 원본 %s를 읽지 못했습니다", path), "원본을 확인한 뒤 transfer.resume으로 다시 시도하세요")
	}
	if !fi.Mode().IsRegular() || fi.Size() != f.Size || fileTime(fi.ModTime()) != f.MTime {
		return sourceChanged(path)
	}
	return nil
}

// hashPrefix feeds the first n bytes of the file path on this Mac to h and
// returns their SHA-256.
func hashPrefix(ctx context.Context, path string, n int64, h hash.Hash) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", localError(err, fmt.Sprintf("이 Mac의 %s를 열지 못했습니다", path), "권한을 확인한 뒤 transfer.resume으로 다시 시도하세요")
	}
	defer f.Close()
	if _, err := io.CopyBuffer(h, ctxReader{ctx, io.LimitReader(f, n)}, make([]byte, chunkSize)); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", localError(err, fmt.Sprintf("이 Mac의 %s를 읽지 못했습니다", path), "디스크 상태를 확인한 뒤 transfer.resume으로 다시 시도하세요")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashLocal returns the SHA-256 of the file path on this Mac.
func hashLocal(ctx context.Context, path string) (string, error) {
	return hashPrefix(ctx, path, 1<<62, sha256.New())
}

// openPart opens the part file part of a pull on this Mac for appending,
// creating it hidden when it is missing, and returns its size. An entry
// with its name that is not a regular file is an error and stays.
func openPart(part string) (*os.File, int64, *apierr.Error) {
	fi, err := os.Lstat(part)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return nil, 0, localError(err, fmt.Sprintf("임시 파일 %s를 만들지 못했습니다", part), "대상 폴더의 권한과 여유 공간을 확인한 뒤 transfer.resume으로 다시 시도하세요")
		}
		_ = setHidden(part, true)
		return f, 0, nil
	case err != nil:
		return nil, 0, localError(err, fmt.Sprintf("임시 파일 %s를 읽지 못했습니다", part), "transfer.resume으로 다시 시도하세요")
	case !fi.Mode().IsRegular():
		return nil, 0, apierr.Invalid(fmt.Sprintf("%s는 받는 중인 파일의 임시 이름을 가진 다른 항목이라 건드리지 않았습니다. 그 항목을 치운 뒤 transfer.resume으로 다시 시도하세요", part))
	}
	f, err := os.OpenFile(part, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.Seek(0, io.SeekEnd)
		if err != nil {
			f.Close()
		}
	}
	if err != nil {
		return nil, 0, localError(err, fmt.Sprintf("임시 파일 %s를 열지 못했습니다", part), "권한을 확인한 뒤 transfer.resume으로 다시 시도하세요")
	}
	return f, fi.Size(), nil
}

// actualName returns the name of the existing entry p as its folder holds
// it: the last element of p when the folder has an entry of exactly that
// name, else the entry whose name differs from it only in letter case, else
// the last element of p.
func actualName(p string) string {
	base := filepath.Base(p)
	ents, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		return base
	}
	folded := ""
	for _, e := range ents {
		if e.Name() == base {
			return base
		}
		if folded == "" && strings.EqualFold(e.Name(), base) {
			folded = e.Name()
		}
	}
	if folded != "" {
		return folded
	}
	return base
}

// conflict applies the on_conflict policy to the destination target on
// this Mac before a file is put there, as the agent does (docs/protocol.md
// §3.1 fs.copy): it reports whether target exists and the path of the
// existing entry under the name its folder holds. With target there, fail,
// a name that differs only in letter case under fail or replace, and
// replace of a folder are TRANSFER_CONFLICT errors.
func conflict(target, policy string) (bool, string, *apierr.Error) {
	fi, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, "", nil
	}
	if err != nil {
		return false, "", localError(err, fmt.Sprintf("이 Mac의 %s를 읽지 못했습니다", target), "권한을 확인한 뒤 transfer.resume으로 다시 시도하세요")
	}
	name := actualName(target)
	existing := filepath.Join(filepath.Dir(target), name)
	refuse := func(msg string) (bool, string, *apierr.Error) {
		return true, existing, apierr.New(wire.CodeTransferConflict, msg, false, apierr.NextFixParams)
	}
	switch {
	case policy == "skip":
	case name != filepath.Base(target) && policy != "rename":
		return refuse(fmt.Sprintf("이 Mac에 %s가 대소문자만 다른 이름 %s로 있습니다. 다른 이름을 주거나 on_conflict skip이나 rename으로 다시 시도하세요", target, name))
	case policy == "fail":
		return refuse(fmt.Sprintf("이 Mac에 %s가 이미 있습니다. on_conflict replace, skip, rename 중 하나로 다시 시도하세요", target))
	case policy == "replace" && fi.IsDir():
		return refuse(fmt.Sprintf("이 Mac의 %s는 폴더라 replace로 바꾸지 않습니다. 폴더를 치우거나 on_conflict rename으로 다시 시도하세요", target))
	}
	return true, existing, nil
}

// commitLocal puts the verified part file part at the destination target
// on this Mac by the policy, as xfer.commit does on the host: skip removes
// the part and keeps an existing destination, rename takes the first free
// numbered name (wire.NumberedName), replace replaces a file and otherwise
// the part takes a name that does not exist. It returns where the file is
// and the outcome; on an error the destination is unchanged.
func commitLocal(part, target, policy string) (string, string, *apierr.Error) {
	exists, existing, cerr := conflict(target, policy)
	if cerr != nil {
		return "", "", cerr
	}
	if exists && policy == "skip" {
		if err := os.Remove(part); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", "", localError(err, fmt.Sprintf("임시 파일 %s를 지우지 못했습니다", part), "직접 지운 뒤 계속하세요")
		}
		return existing, OutcomeSkipped, nil
	}
	_ = setHidden(part, false)
	dest := target
	var err error
	switch policy {
	case "replace":
		err = os.Rename(part, target)
	case "rename":
		dest, err = renameToFreeName(part, target)
	default:
		err = renameNoReplace(part, target)
	}
	if err != nil {
		_ = setHidden(part, true)
		var e *apierr.Error
		switch {
		case errors.As(err, &e):
			return "", "", e
		case errors.Is(err, fs.ErrExist):
			return "", "", apierr.New(wire.CodeTransferConflict,
				fmt.Sprintf("이 Mac의 %s가 받는 동안 생겼습니다. on_conflict replace, skip, rename 중 하나로 다시 시도하세요", target), false, apierr.NextFixParams)
		}
		return "", "", localError(err, fmt.Sprintf("받은 파일을 %s에 두지 못했습니다", target), "대상 폴더의 권한을 확인한 뒤 transfer.resume으로 다시 시도하세요")
	}
	syncDir(filepath.Dir(dest))
	if dest != target {
		return dest, OutcomeRenamed, nil
	}
	return dest, OutcomeWritten, nil
}

// renameToFreeName renames part to target, or to the first numbered name
// of target that does not exist, and returns the name it took.
func renameToFreeName(part, target string) (string, error) {
	dir, base := filepath.Split(target)
	for k := 0; k <= wire.RenameMax; k++ {
		name := target
		if k > 0 {
			name = filepath.Join(dir, wire.NumberedName(base, k, false))
		}
		err := renameNoReplace(part, name)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", apierr.New(wire.CodeTransferConflict, fmt.Sprintf("이 Mac에 %s와 번호 붙은 이름이 (%d)까지 모두 있습니다. 다른 대상을 주세요", target, wire.RenameMax),
		false, apierr.NextFixParams)
}

// syncDir flushes the entries of the folder dir to disk.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

// settleLocal looks for the file f of a pull whose commit may have
// happened before the daemon stopped: the destination target or, under
// rename, its numbered names, whose SHA-256 is that of f. It returns the
// last such path and its outcome.
func settleLocal(ctx context.Context, target, policy string, f store.TransferFile) (string, string, bool) {
	match := ""
	dir, base := filepath.Split(target)
	for k := 0; k <= wire.RenameMax; k++ {
		name := target
		if k > 0 {
			name = filepath.Join(dir, wire.NumberedName(base, k, false))
		}
		fi, err := os.Lstat(name)
		if err != nil {
			break
		}
		if fi.Mode().IsRegular() && fi.Size() == f.Size {
			if sum, err := hashLocal(ctx, name); err == nil && sum == f.SHA256 {
				match = name
			}
		}
		if policy != "rename" {
			break
		}
	}
	switch {
	case match == "":
		return "", "", false
	case match != target:
		return match, OutcomeRenamed, true
	}
	return match, OutcomeWritten, true
}

// removeLocalSource removes the source of a move on this Mac after its
// files moved: a file, or a folder whose entries are those of the plan
// with the files unchanged, every file first and the folders deepest first.
// It returns why the source stays: an entry changed or appeared, a folder
// does not let this process remove its entries, or a removal failed. A
// source that is gone is removed.
func (t *transfer) removeLocalSource(files []store.TransferFile) *apierr.Error {
	root := t.row.Local
	kept := func(e *apierr.Error) *apierr.Error {
		return apierr.New(e.Code, "받은 쪽 파일은 검증을 마쳤지만 이 Mac의 원본을 지우지 않고 남겼습니다. "+e.Message, false, apierr.NextCheckState)
	}
	if !t.row.Folder {
		if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if e := unchanged(root, files[0]); e != nil {
			return kept(e)
		}
		if err := writable(filepath.Dir(root)); err != nil {
			return kept(localError(err, fmt.Sprintf("폴더 %s에서 항목을 지울 권한이 없습니다", filepath.Dir(root)), "원본을 직접 지우세요"))
		}
		if err := os.Remove(root); err != nil {
			return kept(localError(err, fmt.Sprintf("%s를 지우지 못했습니다", root), "원본을 직접 지우세요"))
		}
		return nil
	}
	planned := map[string]store.TransferFile{}
	t.mu.Lock()
	for _, f := range t.files {
		planned[f.Rel] = f
	}
	t.mu.Unlock()
	if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	dirs := []string{root}
	var entries []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		f, ok := planned[rel]
		switch {
		case !ok:
			return apierr.New(wire.CodeTransferConflict, fmt.Sprintf("원본 폴더에 옮기지 않은 %s가 생겼습니다. 원본 폴더를 확인한 뒤 직접 정리하세요", p), false, "")
		case d.IsDir() != f.Dir:
			return sourceChanged(p)
		case d.IsDir():
			dirs = append(dirs, p)
		default:
			if e := unchanged(p, f); e != nil {
				return e
			}
			entries = append(entries, p)
		}
		return nil
	})
	if err != nil {
		var e *apierr.Error
		if errors.As(err, &e) {
			return kept(e)
		}
		return kept(localError(err, fmt.Sprintf("원본 폴더 %s를 읽지 못했습니다", root), "원본을 확인한 뒤 직접 정리하세요"))
	}
	for _, d := range append([]string{filepath.Dir(root)}, dirs...) {
		if err := writable(d); err != nil {
			return kept(localError(err, fmt.Sprintf("폴더 %s에서 항목을 지울 권한이 없습니다", d), "권한을 확인한 뒤 원본을 직접 정리하세요"))
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, p := range append(entries, dirs...) {
		if err := os.Remove(p); err != nil {
			return kept(localError(err, fmt.Sprintf("%s를 지우지 못했습니다", p), "남은 원본을 직접 정리하세요"))
		}
	}
	return nil
}

// sameSecond reports whether the modification times a and b (RFC 3339)
// fall in the same second, the precision of fs.stat and fs.list.
func sameSecond(a, b string) bool {
	ta, err1 := time.Parse(time.RFC3339Nano, a)
	tb, err2 := time.Parse(time.RFC3339Nano, b)
	return err1 == nil && err2 == nil && ta.Truncate(time.Second).Equal(tb.Truncate(time.Second))
}
