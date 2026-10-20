package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// partInfix joins the destination path and the transfer ID in the name
	// of a transfer's part file.
	partInfix = ".deskhand-part-"
	// transferIDMax bounds the length of a transfer ID.
	transferIDMax = 64
	// nameMax is the longest entry name the agent's file systems take: 255
	// UTF-16 code units on Windows, 255 bytes elsewhere.
	nameMax = 255
	// transferIdle bounds each read and write of a put or get stream.
	transferIdle = 30 * time.Second
)

// TransferStream is the agent's end of a put or get stream after its header
// line.
type TransferStream interface {
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

var (
	// writePart appends b to the part file f.
	writePart = func(f *os.File, b []byte) (int, error) { return f.Write(b) }
	// volumeFree returns the bytes the agent may still write on the volume
	// of the folder dir.
	volumeFree = platformVolumeFree
)

// partLocks holds the part files a put, xfer.prepare, xfer.commit or
// xfer.abort works on, by their paths in lower case.
var partLocks = struct {
	sync.Mutex
	held map[string]bool
}{held: map[string]bool{}}

// lockPart takes the part file part for one operation and returns the
// function that gives it back; a part file another operation holds is an
// IN_PROGRESS error.
func lockPart(part string) (func(), error) {
	key := strings.ToLower(part)
	partLocks.Lock()
	defer partLocks.Unlock()
	if partLocks.held[key] {
		return nil, coded(wire.CodeInProgress, fmt.Sprintf("another put, xfer.prepare, xfer.commit or xfer.abort works on '%s' now; wait for it to end and retry", part), true)
	}
	partLocks.held[key] = true
	return func() {
		partLocks.Lock()
		delete(partLocks.held, key)
		partLocks.Unlock()
	}, nil
}

// validTransferID reports whether id is 1 to transferIDMax letters, digits,
// '-' or '_'.
func validTransferID(id string) bool {
	if id == "" || len(id) > transferIDMax {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// nameLength returns the length of the entry name name in the unit of
// nameMax.
func nameLength(name string) int {
	if runtime.GOOS == "windows" {
		return utf16Len(name)
	}
	return len(name)
}

// transferPaths returns the destination path p of the transfer id, resolved
// against the home of v and checked against roots like the paths of the
// file ops, and its part file: <destination>.deskhand-part-<id> in the same
// folder.
func transferPaths(v homeView, id any, p string, roots []string) (target, part string, err error) {
	sid, ok := id.(string)
	if !ok || !validTransferID(sid) {
		return "", "", invalidArgument(fmt.Sprintf("transfer_id %s is not a string of 1 to %d letters, digits, '-' or '_'; pass the ID of the transfer",
			cutChars(fmt.Sprintf("%q", fmt.Sprint(id)), transferIDMax+2), transferIDMax))
	}
	target, err = checkedPathArg(v, proto.Message{"path": p}, "path", roots)
	if err != nil {
		return "", "", err
	}
	dir, base := filepath.Split(target)
	if filepath.Dir(target) == target {
		return "", "", invalidArgument(fmt.Sprintf("'%s' is the root of a volume; pass the path the file gets", target))
	}
	name := base + partInfix + sid
	if n := nameLength(name); n > nameMax {
		return "", "", invalidArgument(fmt.Sprintf("the name '%s' leaves no room for the name of its part file '%s', which is %d characters long where %d is the most; pass a shorter name", base, name, n, nameMax))
	}
	return target, filepath.Join(dir, name), nil
}

// receivingFolder returns nil when a transfer may write the destination
// target: it does not lie in the home folder of the desktop user of an agent
// outside Windows, the deploy root it lies in exists (ensureDeployRoot) and
// its folder exists.
func (s *Server) receivingFolder(v homeView, target string) error {
	if v.contains(target) {
		return coded(wire.CodeCapabilityUnsupported, fmt.Sprintf("transfers do not write into the home folder of the desktop user on this system ('%s'); write the file with fs.write instead", target), false)
	}
	if err := s.ensureDeployRoot(target); err != nil {
		return err
	}
	dir := filepath.Dir(target)
	fi, err := os.Stat(dir)
	if missing(err) || err == nil && !fi.IsDir() {
		return coded(wire.CodeNotFound, fmt.Sprintf("the folder '%s' does not exist; create it with fs.mkdir, then retry", dir), false)
	}
	if err != nil {
		return fsFailure(fmt.Sprintf("cannot read '%s'", dir), err)
	}
	return nil
}

// partInfo returns the Lstat of the part file part, or nil when it does not
// exist; an entry there that is not a regular file is an INVALID_ARGUMENT
// error.
func partInfo(part string) (fs.FileInfo, error) {
	fi, err := os.Lstat(part)
	if missing(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot read the part file '%s'", part), err)
	}
	if !fi.Mode().IsRegular() {
		return nil, invalidArgument(fmt.Sprintf("'%s' has the name of a part file but is not a regular file, so transfers leave it alone; remove or rename it, or use another transfer_id", part))
	}
	return fi, nil
}

// createPart creates the empty part file part, hidden on Windows.
func createPart(part string) error {
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fsFailure(fmt.Sprintf("cannot create the part file '%s'", part), err)
	}
	err = f.Close()
	if err == nil {
		err = setHidden(part, true)
	}
	if err != nil {
		_ = os.Remove(part)
		return fsFailure(fmt.Sprintf("cannot create the part file '%s'", part), err)
	}
	return nil
}

// hashFile returns the SHA-256 of the bytes of f from its start and their
// number, until ctx ends.
func hashFile(ctx context.Context, f *os.File) (string, int64, error) {
	h := sha256.New()
	n, err := io.CopyBuffer(h, ctxReader{ctx, io.NewSectionReader(f, 0, math.MaxInt64)}, make([]byte, fsCopyBuffer))
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// parseMTime parses the modification time s, an RFC 3339 time with or
// without fractions of a second; "" is the zero time.
func parseMTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, invalidArgument(fmt.Sprintf("mtime '%s' is not an RFC 3339 time such as 2026-10-20T12:34:56.789Z; pass the modification time of the file in that form", cutChars(s, 64)))
	}
	return t, nil
}

// transferLine is the JSON of a line the agent writes to a put or get
// stream: an acceptance, a refusal or the result of a put.
func transferLine(err error, fields proto.Message) proto.Message {
	m := proto.Message{"ok": err == nil}
	for k, v := range fields {
		m[k] = v
	}
	if err != nil {
		code, retryable := wire.CodeInternal, false
		var ce *codedError
		if errors.As(err, &ce) {
			code, retryable = ce.code, ce.retryable
		}
		m["code"], m["error"], m["retryable"] = code, err.Error(), retryable
	}
	return m
}

// writeLine writes m as one JSON line to st within transferIdle.
func writeLine(st TransferStream, m proto.Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_ = st.SetWriteDeadline(time.Now().Add(transferIdle))
	_, err = st.Write(append(b, '\n'))
	return err
}

// refuseTransfer writes the refusal line of err to st and asks the daemon
// to stop writing.
func refuseTransfer(st TransferStream, err error) {
	_ = writeLine(st, transferLine(err, nil))
	st.CancelRead()
}

// putTransfer is a put stream the agent accepted.
type putTransfer struct {
	part   string
	f      *os.File
	offset int64
	// room is the number of bytes from offset to the size of the file.
	room int64
	// mtime is the modification time the part file gets after the put.
	mtime  time.Time
	unlock func()
}

// openPut checks the header h of a put stream like xfer.prepare checks its
// request, takes the part file of the transfer, which has to hold exactly
// h.Offset bytes (a missing one, which it creates hidden, holds 0), and
// opens it for appending.
func (s *Server) openPut(h wire.StreamHeader) (*putTransfer, error) {
	if !s.opt.AllowExec {
		return nil, coded(wire.CodeCapabilityUnsupported, errExecDisabled, false)
	}
	v := s.view()
	roots, err := cleanRoots(h.Roots)
	if err != nil {
		return nil, err
	}
	target, part, err := transferPaths(v, h.TransferID, h.Path, roots)
	if err != nil {
		return nil, err
	}
	switch {
	case h.Size < 0:
		return nil, invalidArgument(fmt.Sprintf("size %d is negative; pass the size of the whole file", h.Size))
	case h.Offset < 0:
		return nil, invalidArgument(fmt.Sprintf("offset %d is negative; pass the part_size of xfer.prepare", h.Offset))
	case h.Offset > h.Size:
		return nil, invalidArgument(fmt.Sprintf("offset %d lies past the size %d of the file; pass an offset from 0 to %d", h.Offset, h.Size, h.Size))
	}
	mtime, err := parseMTime(h.MTime)
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
	p, err := openPartAt(part, h, mtime)
	if err != nil {
		unlock()
		return nil, err
	}
	p.unlock = unlock
	return p, nil
}

// openPartAt opens the part file part of the put with header h for
// appending at h.Offset, which has to be its size; mtime, when zero, is the
// part file's own modification time.
func openPartAt(part string, h wire.StreamHeader, mtime time.Time) (*putTransfer, error) {
	fi, err := partInfo(part)
	if err != nil {
		return nil, err
	}
	var size int64
	if fi != nil {
		size = fi.Size()
		if mtime.IsZero() {
			mtime = fi.ModTime()
		}
	}
	if size != h.Offset {
		return nil, invalidArgument(fmt.Sprintf("put offset %d differs from the %d bytes the part file '%s' holds; put from offset %d, or call xfer.abort to start again from 0", h.Offset, size, part, size))
	}
	if fi == nil {
		if err := createPart(part); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(part, os.O_WRONLY, 0)
	if err == nil {
		_, err = f.Seek(h.Offset, io.SeekStart)
		if err != nil {
			f.Close()
		}
	}
	if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot open the part file '%s'", part), err)
	}
	return &putTransfer{part: part, f: f, offset: h.Offset, room: h.Size - h.Offset, mtime: mtime}, nil
}

// errTooLong is the error of receive for a stream that carries more bytes
// than it has room for.
var errTooLong = errors.New("the stream carries more bytes than the file has")

// fill reads st into buf until buf is full, st ends or a read fails, each
// read within transferIdle.
func fill(st TransferStream, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		_ = st.SetReadDeadline(time.Now().Add(transferIdle))
		k, err := st.Read(buf[n:])
		n += k
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// writeError is a failed write of the part file.
type writeError struct{ err error }

func (e writeError) Error() string { return e.err.Error() }
func (e writeError) Unwrap() error { return e.err }

// receive appends the bytes of st up to its end to the part file of p and
// returns how many it wrote. A stream with more bytes than p.room is
// errTooLong, a failed write is a writeError, and a failed read is returned
// as it is.
func (p *putTransfer) receive(st TransferStream) (int64, error) {
	buf := make([]byte, fsCopyBuffer)
	var n int64
	for {
		k, rerr := fill(st, buf)
		if int64(k) > p.room-n {
			return n, errTooLong
		}
		if k > 0 {
			w, err := writePart(p.f, buf[:k])
			n += int64(w)
			if err != nil {
				return n, writeError{err}
			}
		}
		if rerr == io.EOF {
			return n, nil
		}
		if rerr != nil {
			return n, rerr
		}
	}
}

// finish closes the part file after receive returned received and err, and
// returns the error of the put: the part file of a stream with too many
// bytes is cut back to the offset of the put, that of a full volume is
// removed, and otherwise it keeps what it received and gets p.mtime.
func (p *putTransfer) finish(received int64, err error) (partSize int64, _ error) {
	var we writeError
	switch {
	case errors.Is(err, errTooLong):
		terr := p.f.Truncate(p.offset)
		p.f.Close()
		if terr != nil {
			return p.offset + received, fsFailure(fmt.Sprintf("the put stream carried more than the %d bytes from offset %d to the end of the file, and the part file '%s' could not be cut back to %d bytes", p.room, p.offset, p.part, p.offset), terr)
		}
		return p.offset, invalidArgument(fmt.Sprintf("the put stream carried more than the %d bytes from offset %d to the size %d of the file, so the part file is back at %d bytes; put only the rest of the file", p.room, p.offset, p.offset+p.room, p.offset))
	case errors.As(err, &we) && isDiskFull(we.err):
		p.f.Close()
		if rerr := os.Remove(p.part); rerr != nil {
			return p.offset + received, fsFailure(fmt.Sprintf("the volume of '%s' filled up after %d bytes and its part file could not be removed", p.part, p.offset+received), rerr)
		}
		return 0, coded(wire.CodeDiskFull, fmt.Sprintf("the volume of '%s' filled up after %d bytes, so its part file was removed to free the space; free at least %d bytes there and send the file again from offset 0", p.part, p.offset+received, p.offset+p.room), false)
	}
	cerr := p.f.Close()
	partSize = p.offset + received
	if err == nil && cerr != nil {
		err = writeError{cerr}
	}
	if !p.mtime.IsZero() {
		if terr := os.Chtimes(p.part, p.mtime, p.mtime); terr != nil && err == nil {
			err = writeError{terr}
		}
	}
	switch {
	case err == nil:
		return partSize, nil
	case errors.As(err, &we):
		return partSize, fsFailure(fmt.Sprintf("cannot write the part file '%s' after %d bytes", p.part, partSize), we.err)
	case errors.Is(err, os.ErrDeadlineExceeded):
		return partSize, coded(wire.CodeTimeout, fmt.Sprintf("no bytes arrived on the put stream for %s; the part file keeps its %d bytes, so call xfer.prepare and put from its part_size", transferIdle, partSize), true)
	}
	return partSize, coded(wire.CodeInternal, fmt.Sprintf("the put stream ended before its end (%v); the part file keeps its %d bytes, so call xfer.prepare and put from its part_size", err, partSize), true)
}

// ServePut serves the put stream st with header h (docs/protocol.md §2):
// openPut takes the part file of the transfer, the acceptance line carries
// its size, the bytes of the stream up to its end are appended to it (at
// most up to h.Size) and the result line carries the number received and
// the new size of the part file. A header openPut refuses gets the refusal
// line instead. It closes st.
func (s *Server) ServePut(ctx context.Context, st TransferStream, h wire.StreamHeader) {
	defer st.Close()
	stop := context.AfterFunc(ctx, func() {
		st.CancelRead()
		st.CancelWrite()
	})
	defer stop()
	p, err := s.openPut(h)
	if err != nil {
		refuseTransfer(st, err)
		return
	}
	defer p.unlock()
	if err := writeLine(st, transferLine(nil, proto.Message{"part_size": p.offset})); err != nil {
		p.f.Close()
		st.CancelRead()
		return
	}
	received, err := p.receive(st)
	if err != nil {
		st.CancelRead()
	}
	partSize, err := p.finish(received, err)
	_ = writeLine(st, transferLine(err, proto.Message{"received": received, "part_size": partSize}))
}

// openGet checks the header h of a get stream: its path, checked like the
// paths of the file ops, has to name a regular file whose size is h.Offset
// or more. It returns the open file, its Lstat and the number of bytes the
// stream carries: h.Length of them from h.Offset, or with h.Length 0 up to
// the end.
func (s *Server) openGet(h wire.StreamHeader) (*os.File, fs.FileInfo, int64, error) {
	if !s.opt.AllowExec {
		return nil, nil, 0, coded(wire.CodeCapabilityUnsupported, errExecDisabled, false)
	}
	roots, err := cleanRoots(h.Roots)
	if err != nil {
		return nil, nil, 0, err
	}
	path, err := checkedPathArg(s.view(), proto.Message{"path": h.Path}, "path", roots)
	if err != nil {
		return nil, nil, 0, err
	}
	if h.Offset < 0 || h.Length < 0 {
		return nil, nil, 0, invalidArgument(fmt.Sprintf("offset %d and length %d must be 0 or more; pass length 0 to read to the end of the file", h.Offset, h.Length))
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, 0, coded(wire.CodeNotFound, fmt.Sprintf("'%s' does not exist; check the path with fs.stat", path), false)
	}
	if err != nil {
		return nil, nil, 0, fsFailure(fmt.Sprintf("cannot open '%s'", path), err)
	}
	fi, err := f.Stat()
	switch {
	case err != nil:
		err = fsFailure(fmt.Sprintf("cannot read '%s'", path), err)
	case fi.IsDir():
		err = invalidArgument(fmt.Sprintf("'%s' is a folder; get reads regular files only, so list the folder with fs.list and get its files", path))
	case !fi.Mode().IsRegular():
		err = invalidArgument(fmt.Sprintf("'%s' is not a regular file; get reads regular files only", path))
	case h.Offset > fi.Size():
		err = invalidArgument(fmt.Sprintf("offset %d lies past the end of '%s' (%d bytes); pass %d or lower", h.Offset, path, fi.Size(), fi.Size()))
	}
	if err != nil {
		f.Close()
		return nil, nil, 0, err
	}
	n := fi.Size() - h.Offset
	if h.Length > 0 && h.Length < n {
		n = h.Length
	}
	return f, fi, n, nil
}

// deadlineWriter writes to st, each write within transferIdle.
type deadlineWriter struct{ st TransferStream }

func (w deadlineWriter) Write(p []byte) (int, error) {
	_ = w.st.SetWriteDeadline(time.Now().Add(transferIdle))
	return w.st.Write(p)
}

// ServeGet serves the get stream st with header h (docs/protocol.md §2):
// the acceptance line carries the size of the file, the offset, the number
// of bytes that follow (length) and the modification time, then come those
// bytes and the end of the agent's direction. A failure before all of them
// are written resets the agent's direction instead, and a header openGet
// refuses gets the refusal line. It closes st.
func (s *Server) ServeGet(ctx context.Context, st TransferStream, h wire.StreamHeader) {
	defer st.Close()
	defer st.CancelRead()
	stop := context.AfterFunc(ctx, st.CancelWrite)
	defer stop()
	f, fi, n, err := s.openGet(h)
	if err != nil {
		refuseTransfer(st, err)
		return
	}
	defer f.Close()
	accept := proto.Message{"size": fi.Size(), "offset": h.Offset, "length": n, "mtime": fi.ModTime().UTC().Format(time.RFC3339Nano)}
	if err := writeLine(st, transferLine(nil, accept)); err != nil {
		st.CancelWrite()
		return
	}
	sent, err := io.CopyBuffer(deadlineWriter{st}, io.NewSectionReader(f, h.Offset, n), make([]byte, fsCopyBuffer))
	if err != nil || sent != n {
		st.CancelWrite()
	}
}
