package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func (s *Server) registerXfer() {
	s.register("xfer.prepare", true, s.xferPrepare)
	s.register("xfer.commit", true, s.xferCommit)
	s.register("xfer.abort", true, s.xferAbort)
}

// byteCount returns the field key of req, a whole number of bytes, 0 or
// more; a missing field is an error.
func byteCount(req proto.Message, key string) (int64, error) {
	v, ok := req[key]
	if !ok || v == nil {
		return 0, invalidArgument(fmt.Sprintf("%s is missing; pass the size of the whole file in bytes", key))
	}
	f, isNumber := v.(float64)
	if !isNumber || f < 0 || f != math.Trunc(f) || f > 1<<53 {
		return 0, invalidArgument(fmt.Sprintf("%s must be a whole number of bytes, 0 or more, not %s", key, cutChars(fmt.Sprintf("%v", v), 64)))
	}
	return int64(f), nil
}

// digestArg returns the field key of req, a SHA-256 in hex, in lower case,
// or "" when it is missing and need is not set.
func digestArg(req proto.Message, key string, need bool) (string, error) {
	v, ok := req[key]
	if !ok || v == nil {
		if need {
			return "", invalidArgument(fmt.Sprintf("%s is missing; pass the SHA-256 of the whole file in hex", key))
		}
		return "", nil
	}
	s, isString := v.(string)
	if _, err := hex.DecodeString(s); !isString || len(s) != 64 || err != nil {
		return "", invalidArgument(fmt.Sprintf("%s must be the SHA-256 of the whole file as 64 hex digits, not %s", key, cutChars(fmt.Sprintf("%q", fmt.Sprint(v)), 70)))
	}
	return strings.ToLower(s), nil
}

// mtimeArg returns the modification time of the field "mtime" of req
// (parseMTime), the zero time when it is missing.
func mtimeArg(req proto.Message) (time.Time, error) {
	v, ok := req["mtime"]
	if !ok || v == nil {
		return time.Time{}, nil
	}
	s, isString := v.(string)
	if !isString {
		return time.Time{}, invalidArgument(fmt.Sprintf("mtime %s is not a string; pass an RFC 3339 time such as 2026-10-20T12:34:56.789Z", cutChars(fmt.Sprint(v), 64)))
	}
	return parseMTime(s)
}

// xferPaths returns the destination of the transfer of req and its part
// file (transferPaths) with the roots of req.
func xferPaths(v homeView, req proto.Message) (target, part string, err error) {
	roots, err := rootsArg(req)
	if err != nil {
		return "", "", err
	}
	return transferPaths(v, req["transfer_id"], req.String("path"), roots)
}

// xferPrepare readies the part file of a transfer to path: it applies
// on_conflict to an existing destination as xfer.commit will (fail and a
// name that differs only in letter case are TRANSFER_CONFLICT errors here
// already), refuses with DISK_FULL a file whose missing bytes do not fit on
// the volume, creates the part file hidden when there is none and gives it
// the modification time mtime. It answers the size of the part file, the
// SHA-256 of its bytes, the free bytes of the volume and whether the
// destination exists.
func (s *Server) xferPrepare(ctx context.Context, req proto.Message) (proto.Message, error) {
	v := s.view()
	target, part, err := xferPaths(v, req)
	if err != nil {
		return nil, err
	}
	size, err := byteCount(req, "size")
	if err != nil {
		return nil, err
	}
	mtime, err := mtimeArg(req)
	if err != nil {
		return nil, err
	}
	if _, err := digestArg(req, "sha256", false); err != nil {
		return nil, err
	}
	policy, err := conflictPolicy(req)
	if err != nil {
		return nil, err
	}
	if err := s.receivingFolder(v, target); err != nil {
		return nil, err
	}
	unlock, err := lockPart(part)
	if err != nil {
		return nil, err
	}
	defer unlock()
	exists, _, err := applyConflictPolicy("", target, false, policy, "transfer")
	if err != nil {
		return nil, err
	}
	fi, err := partInfo(part)
	if err != nil {
		return nil, err
	}
	var have int64
	if fi != nil {
		have = fi.Size()
	}
	free, err := volumeFree(filepath.Dir(target))
	if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot read the free space of the volume of '%s'", target), err)
	}
	if need := size - have; need > 0 && uint64(need) > free && !(exists && policy == "skip") {
		return nil, coded(wire.CodeDiskFull, fmt.Sprintf("'%s' needs %d more bytes and its volume has %d bytes free; free space there and retry", target, need, free), false)
	}
	if fi == nil {
		if err := createPart(part); err != nil {
			return nil, err
		}
	}
	f, err := os.Open(part)
	if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot open the part file '%s'", part), err)
	}
	sum, n, err := hashFile(ctx, f)
	f.Close()
	if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot read the part file '%s'", part), err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(part, mtime, mtime); err != nil {
			return nil, fsFailure(fmt.Sprintf("cannot set the modification time of the part file '%s'", part), err)
		}
	}
	return proto.Message{"part_size": n, "part_sha256": sum, "free_bytes": free, "dest_exists": exists}, nil
}

// xferCommit puts the part file of a transfer at its destination: it checks
// that the SHA-256 of the part file is sha256 (HASH_MISMATCH otherwise,
// with the destination and the part file unchanged), flushes it to disk,
// gives it back its modification time and applies on_conflict as fs.copy
// does: skip removes the part file and leaves the destination, fail and a
// name that differs only in letter case under fail or replace are
// TRANSFER_CONFLICT errors, rename takes the first free numbered name, and
// replace replaces a file (replaceFile). A destination that cannot be
// replaced or written, such as a locked or read-only one, is an error with
// the destination unchanged and the part file kept for a retry.
func (s *Server) xferCommit(ctx context.Context, req proto.Message) (proto.Message, error) {
	v := s.view()
	target, part, err := xferPaths(v, req)
	if err != nil {
		return nil, err
	}
	want, err := digestArg(req, "sha256", true)
	if err != nil {
		return nil, err
	}
	policy, err := conflictPolicy(req)
	if err != nil {
		return nil, err
	}
	if err := s.receivingFolder(v, target); err != nil {
		return nil, err
	}
	unlock, err := lockPart(part)
	if err != nil {
		return nil, err
	}
	defer unlock()
	fi, err := partInfo(part)
	if err != nil {
		return nil, err
	}
	if fi == nil {
		return nil, coded(wire.CodeNotFound, fmt.Sprintf("there is no part file '%s': nothing was received for this transfer, or it was committed or aborted; call xfer.prepare and put the file", part), false)
	}
	if err := verifyPart(ctx, part, want); err != nil {
		return nil, err
	}
	if err := os.Chtimes(part, fi.ModTime(), fi.ModTime()); err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot set the modification time of the part file '%s'", part), err)
	}
	exists, existing, err := applyConflictPolicy("", target, false, policy, "transfer")
	if err != nil {
		return nil, err
	}
	if exists && policy == "skip" {
		if err := os.Remove(part); err != nil {
			return nil, fsFailure(fmt.Sprintf("'%s' exists and is kept, but its part file '%s' could not be removed", existing, part), err)
		}
		return proto.Message{"path": existing, "outcome": "skipped"}, nil
	}
	if err := setHidden(part, false); err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot show the part file '%s'", part), err)
	}
	dest := target
	switch policy {
	case "replace":
		err = replaceFile(part, target)
	case "rename":
		dest, err = renameToFreeName(part, target, false)
	default:
		err = renameNoReplace(part, target)
	}
	if err != nil {
		_ = setHidden(part, true)
		return nil, commitError(target, err)
	}
	outcome := "written"
	if dest != target {
		outcome = "renamed"
	}
	return proto.Message{"path": dest, "outcome": outcome}, nil
}

// verifyPart checks that the SHA-256 of the part file part is want and
// flushes the part file to disk.
func verifyPart(ctx context.Context, part, want string) error {
	f, err := os.OpenFile(part, os.O_RDWR, 0)
	if err != nil {
		return fsFailure(fmt.Sprintf("cannot open the part file '%s'", part), err)
	}
	got, n, err := hashFile(ctx, f)
	if err == nil && got == want {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case ctx.Err() != nil:
		return coded(wire.CodeInternal, fmt.Sprintf("the check of the part file '%s' stopped because the connection closed, and nothing was changed; send xfer.commit again with a new rid", part), true)
	case err != nil:
		return fsFailure(fmt.Sprintf("cannot read the part file '%s'", part), err)
	case got != want:
		return coded(wire.CodeHashMismatch, fmt.Sprintf("the %d bytes received in '%s' have the SHA-256 %s, not %s; the destination and the part file are unchanged, so call xfer.abort and send the file again", n, part, got, want), false)
	}
	return nil
}

// commitError is the error of a commit whose rename of the part file to
// target failed with err, after which the destination and the part file
// are unchanged.
func commitError(target string, err error) error {
	var ce *codedError
	switch {
	case errors.As(err, &ce):
		return err
	case errors.Is(err, fs.ErrExist):
		return coded(wire.CodeTransferConflict, fmt.Sprintf("'%s' appeared while the file was received; pass on_conflict replace, skip or rename to transfer anyway", target), false)
	}
	return fsFailure(fmt.Sprintf("cannot put the received file at '%s', which is unchanged, as is the part file", target), err)
}

// xferAbort removes the part file of a transfer, and nothing else. It
// answers whether there was one; an entry with the name of the part file
// that is not a regular file is left alone with an INVALID_ARGUMENT error.
func (s *Server) xferAbort(_ context.Context, req proto.Message) (proto.Message, error) {
	_, part, err := xferPaths(s.view(), req)
	if err != nil {
		return nil, err
	}
	unlock, err := lockPart(part)
	if err != nil {
		return nil, err
	}
	defer unlock()
	fi, err := partInfo(part)
	if err != nil {
		return nil, err
	}
	if fi == nil {
		return proto.Message{"removed": false}, nil
	}
	if err := os.Remove(part); err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot remove the part file '%s'", part), err)
	}
	return proto.Message{"removed": true}, nil
}
