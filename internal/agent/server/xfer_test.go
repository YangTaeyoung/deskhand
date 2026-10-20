package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// fakeStream is the agent's end of a put or get stream: the agent reads in
// and writes out.
type fakeStream struct {
	in io.Reader

	mu                          sync.Mutex
	out                         bytes.Buffer
	closed, stopped, reset      bool
	readDeadline, writeDeadline time.Time
}

func (f *fakeStream) Read(p []byte) (int, error) { return f.in.Read(p) }

func (f *fakeStream) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reset || f.closed {
		return 0, errors.New("write on a stream that is closed or reset")
	}
	return f.out.Write(p)
}

func (f *fakeStream) SetReadDeadline(d time.Time) error {
	f.mu.Lock()
	f.readDeadline = d
	f.mu.Unlock()
	return nil
}

func (f *fakeStream) SetWriteDeadline(d time.Time) error {
	f.mu.Lock()
	f.writeDeadline = d
	f.mu.Unlock()
	return nil
}

func (f *fakeStream) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeStream) CancelRead() {
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
}

func (f *fakeStream) CancelWrite() {
	f.mu.Lock()
	f.reset = true
	f.mu.Unlock()
}

// written returns the bytes the agent wrote and whether it ended its
// direction with a FIN (closed and not reset).
func (f *fakeStream) written() ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.out.Bytes()), f.closed && !f.reset
}

// jsonLine decodes the JSON line line.
func jsonLine(t *testing.T, line []byte) proto.Message {
	t.Helper()
	var m proto.Message
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("line %q: %v", line, err)
	}
	return m
}

// put serves one put stream with header h and the bytes of body, and
// returns the lines the agent wrote: its acceptance or refusal, and after
// an acceptance its result.
func put(t *testing.T, s *Server, h wire.StreamHeader, body io.Reader) (first, result proto.Message) {
	t.Helper()
	h.S = "put"
	st := &fakeStream{in: body}
	s.ServePut(context.Background(), st, h)
	out, fin := st.written()
	if !fin {
		t.Fatalf("the put stream did not end with a FIN: %q", out)
	}
	lines := bytes.Split(bytes.TrimSuffix(out, []byte("\n")), []byte("\n"))
	first = jsonLine(t, lines[0])
	if len(lines) > 1 {
		result = jsonLine(t, lines[1])
	}
	if len(lines) > 2 || first["ok"] == true && result == nil || first["ok"] != true && result != nil {
		t.Fatalf("the put stream carried %q", out)
	}
	return first, result
}

// get serves one get stream with header h and returns the agent's first
// line, the bytes after it and whether the agent ended its direction with
// a FIN.
func get(t *testing.T, s *Server, h wire.StreamHeader) (proto.Message, []byte, bool) {
	t.Helper()
	h.S = "get"
	st := &fakeStream{in: strings.NewReader("")}
	s.ServeGet(context.Background(), st, h)
	out, fin := st.written()
	line, rest, ok := bytes.Cut(out, []byte("\n"))
	if !ok {
		t.Fatalf("the get stream carried no line: %q", out)
	}
	return jsonLine(t, line), rest, fin
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// xferHome starts an agent whose home is a new opsDir.
func xferHome(t *testing.T) (*rpcClient, *Server, string) {
	t.Helper()
	home := opsDir(t)
	c, s := newRPCClient(t, Options{HomeDir: home})
	return c, s, home
}

// brokenReader reads r and then fails with err.
type brokenReader struct {
	r   io.Reader
	err error
}

func (b *brokenReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err == io.EOF {
		return n, b.err
	}
	return n, err
}

func TestPutResumeFromOffset(t *testing.T) {
	c, s, home := xferHome(t)
	dst := filepath.Join(home, "big.bin")
	part := dst + ".deskhand-part-t1"
	data := randomBytes(3<<20 + 123)
	sum := sha(data)
	mtime := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	size := int64(len(data))
	h := wire.StreamHeader{TransferID: "t1", Path: dst, Size: size, MTime: mtime.Format(time.RFC3339Nano)}

	prep := c.ok("xfer.prepare", proto.Message{"transfer_id": "t1", "path": dst, "size": size, "mtime": h.MTime, "sha256": sum})
	if prep.Int("part_size") != 0 || prep.String("part_sha256") != sha(nil) || prep["dest_exists"] != false || prep.Int("free_bytes") <= 0 {
		t.Fatalf("prepare of a new transfer %v", prep)
	}
	if fi, err := os.Lstat(part); err != nil || fi.Size() != 0 {
		t.Fatalf("the part file after prepare: %v %v", fi, err)
	}

	n1, n2 := int64(1<<20+7), int64(2<<20+11)
	acc, res := put(t, s, h, bytes.NewReader(data[:n1]))
	if acc["ok"] != true || acc.Int("part_size") != 0 || res["ok"] != true || res.Int("received") != int(n1) || res.Int("part_size") != int(n1) {
		t.Fatalf("first put %v %v", acc, res)
	}

	h.Offset = n1
	acc, res = put(t, s, h, &brokenReader{bytes.NewReader(data[n1:n2]), errors.New("the stream was reset")})
	if acc["ok"] != true || res["ok"] != false || res.String("code") == "" || res.Int("received") != int(n2-n1) || res.Int("part_size") != int(n2) {
		t.Fatalf("put cut off by a reset %v %v", acc, res)
	}
	prep = c.ok("xfer.prepare", proto.Message{"transfer_id": "t1", "path": dst, "size": size, "mtime": h.MTime})
	if prep.Int("part_size") != int(n2) || prep.String("part_sha256") != sha(data[:n2]) {
		t.Fatalf("prepare after a cut-off put %v", prep)
	}

	for _, wrong := range []int64{0, n1, n2 + 10} {
		h.Offset = wrong
		acc, _ = put(t, s, h, bytes.NewReader(data[wrong:]))
		failsWith(t, acc, wire.CodeInvalidArgument, fmt.Sprint(wrong))
		failsWith(t, acc, wire.CodeInvalidArgument, fmt.Sprint(n2))
	}
	if got := readFile(t, part); got != string(data[:n2]) {
		t.Fatalf("a put at another offset changed the part file (%d bytes)", len(got))
	}

	h.Offset = n2
	acc, res = put(t, s, h, bytes.NewReader(append(slices.Clone(data[n2:]), 'x')))
	failsWith(t, res, wire.CodeInvalidArgument, fmt.Sprint(size))
	if got := readFile(t, part); got != string(data[:n2]) {
		t.Fatalf("a put past the size left %d bytes in the part file", len(got))
	}
	acc, res = put(t, s, h, bytes.NewReader(data[n2:]))
	if acc["ok"] != true || res["ok"] != true || res.Int("received") != int(size-n2) || res.Int("part_size") != int(size) {
		t.Fatalf("last put %v %v", acc, res)
	}

	res = c.ok("xfer.commit", proto.Message{"transfer_id": "t1", "path": dst, "sha256": sum, "rid": "r-commit-t1"})
	if res.String("path") != dst || res.String("outcome") != "written" {
		t.Fatalf("commit %v", res)
	}
	if readFile(t, dst) != string(data) {
		t.Fatal("the committed file differs from the data")
	}
	if fi, err := os.Stat(dst); err != nil || !fi.ModTime().Equal(mtime) {
		t.Fatalf("mtime of the committed file %v %v", fi.ModTime(), err)
	}
	if _, err := os.Lstat(part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the part file is still there after the commit: %v", err)
	}
	again := c.ok("xfer.commit", proto.Message{"transfer_id": "t1", "path": dst, "sha256": sum, "rid": "r-commit-t1"})
	if again["replayed"] != true || again.String("outcome") != "written" {
		t.Fatalf("the commit with the same rid %v", again)
	}
}

func TestCommitVerifiesHash(t *testing.T) {
	c, s, home := xferHome(t)
	dst := filepath.Join(home, "report.txt")
	writeFile(t, dst, "old content")
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(dst, old, old); err != nil {
		t.Fatal(err)
	}
	data := []byte("new content that replaces the old one")
	c.ok("xfer.prepare", proto.Message{"transfer_id": "t2", "path": dst, "size": len(data), "on_conflict": "replace"})
	if _, res := put(t, s, wire.StreamHeader{TransferID: "t2", Path: dst, Size: int64(len(data))}, bytes.NewReader(data)); res["ok"] != true {
		t.Fatalf("put %v", res)
	}
	part := dst + ".deskhand-part-t2"

	for _, wrong := range []string{sha([]byte("something else")), sha(data[:10])} {
		res := c.call("xfer.commit", proto.Message{"transfer_id": "t2", "path": dst, "sha256": wrong, "on_conflict": "replace"})
		failsWith(t, res, wire.CodeHashMismatch, sha(data))
		if readFile(t, dst) != "old content" {
			t.Fatal("a commit with another sha256 changed the destination")
		}
		if fi, err := os.Stat(dst); err != nil || !fi.ModTime().Equal(old) {
			t.Fatalf("a commit with another sha256 changed the destination's mtime: %v %v", fi.ModTime(), err)
		}
		if readFile(t, part) != string(data) {
			t.Fatal("a commit with another sha256 changed the part file")
		}
	}
	failsWith(t, c.call("xfer.commit", proto.Message{"transfer_id": "t2", "path": dst, "sha256": "abc"}), wire.CodeInvalidArgument, "sha256")

	res := c.ok("xfer.commit", proto.Message{"transfer_id": "t2", "path": dst, "sha256": strings.ToUpper(sha(data)), "on_conflict": "replace"})
	if res.String("outcome") != "written" || readFile(t, dst) != string(data) {
		t.Fatalf("commit with the sha256 of the data %v", res)
	}
}

// denial makes a destination refuse its replacement until undo runs.
type denial struct {
	name      string
	retryable bool
	apply     func(t *testing.T, path string) (undo func())
}

func TestCommitFailureLeavesNoCorruptTarget(t *testing.T) {
	t.Run("disk full while receiving", func(t *testing.T) {
		c, s, home := xferHome(t)
		dst := filepath.Join(home, "kept.txt")
		writeFile(t, dst, "kept")
		part := dst + ".deskhand-part-t4"
		data := randomBytes(3 << 20)
		c.ok("xfer.prepare", proto.Message{"transfer_id": "t4", "path": dst, "size": len(data), "on_conflict": "replace"})
		write := writePart
		t.Cleanup(func() { writePart = write })
		written := 0
		writePart = func(f *os.File, b []byte) (int, error) {
			if written+len(b) > 1<<20 {
				n, _ := f.Write(b[:1<<20-written])
				written += n
				return n, diskFullError()
			}
			n, err := f.Write(b)
			written += n
			return n, err
		}
		_, res := put(t, s, wire.StreamHeader{TransferID: "t4", Path: dst, Size: int64(len(data))}, bytes.NewReader(data))
		failsWith(t, res, wire.CodeDiskFull, "free")
		if _, err := os.Lstat(part); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the part file is still there after the volume filled up: %v", err)
		}
		if readFile(t, dst) != "kept" {
			t.Fatal("the destination changed")
		}
		writePart = write
		if prep := c.ok("xfer.prepare", proto.Message{"transfer_id": "t4", "path": dst, "size": len(data), "on_conflict": "replace"}); prep.Int("part_size") != 0 {
			t.Fatalf("prepare after the volume filled up %v", prep)
		}
	})

	t.Run("no room for the rest", func(t *testing.T) {
		c, _, home := xferHome(t)
		dst := filepath.Join(home, "big.bin")
		free := volumeFree
		t.Cleanup(func() { volumeFree = free })
		volumeFree = func(string) (uint64, error) { return 1000, nil }
		failsWith(t, c.call("xfer.prepare", proto.Message{"transfer_id": "t5", "path": dst, "size": 5000}), wire.CodeDiskFull, "1000")
		if got := names(t, home); len(got) != 0 {
			t.Fatalf("prepare without room left %v", got)
		}
	})

	for _, d := range replaceDenials() {
		t.Run(d.name, func(t *testing.T) {
			c, s, home := xferHome(t)
			dst := filepath.Join(home, "target.txt")
			writeFile(t, dst, "the old target")
			data := []byte("the new target")
			c.ok("xfer.prepare", proto.Message{"transfer_id": "t6", "path": dst, "size": len(data), "on_conflict": "replace"})
			if _, res := put(t, s, wire.StreamHeader{TransferID: "t6", Path: dst, Size: int64(len(data))}, bytes.NewReader(data)); res["ok"] != true {
				t.Fatalf("put %v", res)
			}
			part := dst + ".deskhand-part-t6"
			undo := d.apply(t, dst)
			commit := proto.Message{"transfer_id": "t6", "path": dst, "sha256": sha(data), "on_conflict": "replace"}
			res := c.call("xfer.commit", commit)
			failsWith(t, res, wire.CodePermissionDenied, dst)
			if res["retryable"] != d.retryable {
				t.Fatalf("retryable %v, want %v", res["retryable"], d.retryable)
			}
			undo()
			if readFile(t, dst) != "the old target" {
				t.Fatal("the failed commit changed the destination")
			}
			if readFile(t, part) != string(data) {
				t.Fatal("the failed commit changed the part file")
			}
			if res := c.ok("xfer.commit", commit); res.String("outcome") != "written" || readFile(t, dst) != string(data) {
				t.Fatalf("the commit after the destination was freed %v", res)
			}
		})
	}
}

func TestGetRange(t *testing.T) {
	_, s, home := xferHome(t)
	file := filepath.Join(home, "data.bin")
	data := randomBytes(1000)
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		offset, length int64
		want           []byte
	}{
		{100, 200, data[100:300]},
		{900, 0, data[900:]},
		{0, 0, data},
		{990, 50, data[990:]},
		{1000, 0, nil},
	} {
		acc, got, fin := get(t, s, wire.StreamHeader{Path: file, Offset: tc.offset, Length: tc.length})
		if acc["ok"] != true || acc.Int("size") != 1000 || acc.Int("offset") != int(tc.offset) || acc.Int("length") != len(tc.want) || acc.String("mtime") == "" {
			t.Fatalf("get %d+%d answered %v", tc.offset, tc.length, acc)
		}
		if !bytes.Equal(got, tc.want) || !fin {
			t.Fatalf("get %d+%d carried %d bytes, FIN %v", tc.offset, tc.length, len(got), fin)
		}
	}
	acc, rest, _ := get(t, s, wire.StreamHeader{Path: file, Offset: 1001})
	failsWith(t, acc, wire.CodeInvalidArgument, "1001")
	failsWith(t, acc, wire.CodeInvalidArgument, "1000")
	if len(rest) != 0 {
		t.Fatalf("a refused get carried %d bytes", len(rest))
	}
	for _, h := range []wire.StreamHeader{{Path: file, Offset: -1}, {Path: file, Length: -1}, {Path: home}, {Path: ""}} {
		acc, _, _ := get(t, s, h)
		if acc["ok"] != false || acc.String("code") != wire.CodeInvalidArgument && acc.String("code") != wire.CodeNotFound {
			t.Fatalf("get %+v answered %v", h, acc)
		}
	}
	acc, _, _ = get(t, s, wire.StreamHeader{Path: filepath.Join(home, "missing.bin")})
	failsWith(t, acc, wire.CodeNotFound, "missing.bin")
	acc, _, _ = get(t, s, wire.StreamHeader{Path: file, Roots: []string{filepath.Join(home, "elsewhere")}})
	failsWith(t, acc, wire.CodePermissionDenied, "outside")
}

func TestAbortRemovesOnlyPart(t *testing.T) {
	c, s, home := xferHome(t)
	dst := filepath.Join(home, "doc.txt")
	writeFile(t, dst, "keep me")
	writeFile(t, filepath.Join(home, "neighbour.txt"), "neighbour")
	other := dst + ".deskhand-part-t-other"
	writeFile(t, other, "another transfer")
	c.ok("xfer.prepare", proto.Message{"transfer_id": "t7", "path": dst, "size": 5, "on_conflict": "replace"})
	if _, res := put(t, s, wire.StreamHeader{TransferID: "t7", Path: dst, Size: 5}, strings.NewReader("abc")); res["ok"] != true {
		t.Fatalf("put %v", res)
	}
	part := dst + ".deskhand-part-t7"

	if res := c.ok("xfer.abort", proto.Message{"transfer_id": "t7", "path": dst, "rid": "r-abort-t7"}); res["removed"] != true {
		t.Fatalf("abort %v", res)
	}
	if _, err := os.Lstat(part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the part file is still there: %v", err)
	}
	if readFile(t, dst) != "keep me" || readFile(t, filepath.Join(home, "neighbour.txt")) != "neighbour" || readFile(t, other) != "another transfer" {
		t.Fatal("abort changed a file other than its part file")
	}
	if res := c.ok("xfer.abort", proto.Message{"transfer_id": "t7", "path": dst, "rid": "r-abort-t7"}); res["replayed"] != true || res["removed"] != true {
		t.Fatalf("abort with the same rid %v", res)
	}
	if res := c.ok("xfer.abort", proto.Message{"transfer_id": "t7", "path": dst}); res["removed"] != false {
		t.Fatalf("abort without a part file %v", res)
	}

	folder := dst + ".deskhand-part-t8"
	writeFile(t, filepath.Join(folder, "inner.txt"), "inner")
	failsWith(t, c.call("xfer.abort", proto.Message{"transfer_id": "t8", "path": dst}), wire.CodeInvalidArgument, folder)
	if readFile(t, filepath.Join(folder, "inner.txt")) != "inner" {
		t.Fatal("abort changed a folder with the name of a part file")
	}
	if got, want := names(t, home), []string{"doc.txt", "doc.txt.deskhand-part-t-other", "doc.txt.deskhand-part-t8", "neighbour.txt"}; !slices.Equal(got, want) {
		t.Fatalf("the folder holds %v, want %v", got, want)
	}
}

func TestPrepareChecksTheDestination(t *testing.T) {
	c, _, home := xferHome(t)
	dst := filepath.Join(home, "exists.txt")
	writeFile(t, dst, "here")

	for _, req := range []proto.Message{{}, {"on_conflict": "fail"}} {
		req["transfer_id"], req["path"], req["size"] = "t9", dst, 3
		failsWith(t, c.call("xfer.prepare", req), wire.CodeTransferConflict, "on_conflict")
	}
	if _, err := os.Lstat(dst + ".deskhand-part-t9"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a conflicting prepare left a part file: %v", err)
	}
	for _, policy := range []string{"skip", "replace", "rename"} {
		res := c.ok("xfer.prepare", proto.Message{"transfer_id": "t-" + policy, "path": dst, "size": 3, "on_conflict": policy})
		if res["dest_exists"] != true {
			t.Fatalf("prepare with %s %v", policy, res)
		}
	}
	failsWith(t, c.call("xfer.prepare", proto.Message{"transfer_id": "t9", "path": dst, "size": 3, "on_conflict": "merge"}), wire.CodeInvalidArgument, "on_conflict")
	writeFile(t, filepath.Join(home, "folder", "x.txt"), "x")
	failsWith(t, c.call("xfer.prepare", proto.Message{"transfer_id": "t9", "path": filepath.Join(home, "folder"), "size": 3, "on_conflict": "replace"}),
		wire.CodeTransferConflict, "folder")

	missing := filepath.Join(home, "no such folder", "f.txt")
	failsWith(t, c.call("xfer.prepare", proto.Message{"transfer_id": "t9", "path": missing, "size": 3}), wire.CodeNotFound, "fs.mkdir")
	for _, id := range []any{"", "a/b", "a b", strings.Repeat("x", 65), 7} {
		failsWith(t, c.call("xfer.prepare", proto.Message{"transfer_id": id, "path": filepath.Join(home, "f.txt"), "size": 3}), wire.CodeInvalidArgument, "transfer_id")
	}
	for _, size := range []any{-1, 1.5, "3", nil} {
		failsWith(t, c.call("xfer.prepare", proto.Message{"transfer_id": "t9", "path": filepath.Join(home, "f.txt"), "size": size}), wire.CodeInvalidArgument, "size")
	}
	failsWith(t, c.call("xfer.prepare", proto.Message{"transfer_id": "t9", "path": filepath.Join(home, "f.txt"), "size": 3, "mtime": "yesterday"}), wire.CodeInvalidArgument, "mtime")
	failsWith(t, c.call("xfer.prepare", proto.Message{"transfer_id": "t9", "path": filepath.Join(home, strings.Repeat("n", 240)+".txt"), "size": 3}), wire.CodeInvalidArgument, "shorter name")
	failsWith(t, c.call("xfer.prepare", proto.Message{"transfer_id": "t9", "path": filepath.Join(home, "f.txt"), "size": 3, "roots": []any{filepath.Join(home, "folder")}}), wire.CodePermissionDenied, "outside")
}

func TestCommitConflictPoliciesMatchCopy(t *testing.T) {
	c, s, home := xferHome(t)
	dst := filepath.Join(home, "dst.txt")
	writeFile(t, dst, "old")
	data := []byte("new")
	send := func(id, policy string) string {
		t.Helper()
		c.ok("xfer.prepare", proto.Message{"transfer_id": id, "path": dst, "size": len(data), "on_conflict": policy})
		if _, res := put(t, s, wire.StreamHeader{TransferID: id, Path: dst, Size: int64(len(data))}, bytes.NewReader(data)); res["ok"] != true {
			t.Fatalf("put %v", res)
		}
		return dst + ".deskhand-part-" + id
	}

	part := send("ta", "replace")
	failsWith(t, c.call("xfer.commit", proto.Message{"transfer_id": "ta", "path": dst, "sha256": sha(data)}), wire.CodeTransferConflict, "on_conflict")
	failsWith(t, c.call("xfer.commit", proto.Message{"transfer_id": "ta", "path": dst, "sha256": sha(data), "on_conflict": "fail"}), wire.CodeTransferConflict, "on_conflict")
	if readFile(t, dst) != "old" || readFile(t, part) != "new" {
		t.Fatal("a commit with fail changed a file")
	}

	res := c.ok("xfer.commit", proto.Message{"transfer_id": "ta", "path": dst, "sha256": sha(data), "on_conflict": "skip"})
	if res.String("path") != dst || res.String("outcome") != "skipped" || readFile(t, dst) != "old" {
		t.Fatalf("commit with skip %v", res)
	}
	if _, err := os.Lstat(part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the part file is still there after a skip: %v", err)
	}

	for i, want := range []string{"dst (1).txt", "dst (2).txt"} {
		send(fmt.Sprintf("tr%d", i), "rename")
		res := c.ok("xfer.commit", proto.Message{"transfer_id": fmt.Sprintf("tr%d", i), "path": dst, "sha256": sha(data), "on_conflict": "rename"})
		if got := filepath.Join(home, want); res.String("path") != got || res.String("outcome") != "renamed" || readFile(t, got) != "new" {
			t.Fatalf("commit with rename %d %v", i, res)
		}
	}
	copied := c.ok("fs.copy", proto.Message{"from": dst, "to": dst, "on_conflict": "rename"})
	if copied.String("path") != filepath.Join(home, "dst (3).txt") {
		t.Fatalf("fs.copy with rename %v", copied)
	}

	send("tp", "replace")
	if res := c.ok("xfer.commit", proto.Message{"transfer_id": "tp", "path": dst, "sha256": sha(data), "on_conflict": "replace"}); res.String("outcome") != "written" || readFile(t, dst) != "new" {
		t.Fatalf("commit with replace %v", res)
	}
	failsWith(t, c.call("xfer.commit", proto.Message{"transfer_id": "tp", "path": dst, "sha256": sha(data)}), wire.CodeNotFound, "xfer.prepare")

	if caseInsensitive(t, home) {
		upper := filepath.Join(home, "DST.TXT")
		for _, policy := range []string{"fail", "replace"} {
			failsWith(t, c.call("xfer.prepare", proto.Message{"transfer_id": "tu", "path": upper, "size": len(data), "on_conflict": policy}), wire.CodeTransferConflict, "dst.txt")
		}
		c.ok("xfer.prepare", proto.Message{"transfer_id": "tu", "path": upper, "size": len(data), "on_conflict": "rename"})
		if _, res := put(t, s, wire.StreamHeader{TransferID: "tu", Path: upper, Size: int64(len(data))}, bytes.NewReader(data)); res["ok"] != true {
			t.Fatalf("put %v", res)
		}
		for _, policy := range []string{"fail", "replace"} {
			failsWith(t, c.call("xfer.commit", proto.Message{"transfer_id": "tu", "path": upper, "sha256": sha(data), "on_conflict": policy}), wire.CodeTransferConflict, "dst.txt")
		}
	}
}

// caseInsensitive reports whether the volume of dir takes names that
// differ only in letter case for one name.
func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "case-probe")
	writeFile(t, probe, "")
	defer os.Remove(probe)
	_, err := os.Stat(filepath.Join(dir, "CASE-PROBE"))
	return err == nil
}

// gate is a reader that returns data, then waits for release before its
// end.
type gate struct {
	data     []byte
	started  chan struct{}
	release  chan struct{}
	sentData bool
}

func (g *gate) Read(p []byte) (int, error) {
	if !g.sentData {
		g.sentData = true
		close(g.started)
		return copy(p, g.data), nil
	}
	<-g.release
	return 0, io.EOF
}

func TestATransferPartTakesOneOperationAtATime(t *testing.T) {
	c, s, home := xferHome(t)
	dst := filepath.Join(home, "busy.bin")
	h := wire.StreamHeader{TransferID: "tb", Path: dst, Size: 6}
	c.ok("xfer.prepare", proto.Message{"transfer_id": "tb", "path": dst, "size": 6})
	g := &gate{data: []byte("abc"), started: make(chan struct{}), release: make(chan struct{})}
	held := &fakeStream{in: g}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h := h
		h.S = "put"
		s.ServePut(context.Background(), held, h)
	}()
	<-g.started
	for op, req := range map[string]proto.Message{
		"xfer.prepare": {"transfer_id": "tb", "path": dst, "size": 6},
		"xfer.commit":  {"transfer_id": "tb", "path": dst, "sha256": sha([]byte("abc"))},
		"xfer.abort":   {"transfer_id": "tb", "path": dst},
	} {
		res := c.call(op, req)
		failsWith(t, res, wire.CodeInProgress, "retry")
		if res["retryable"] != true {
			t.Fatalf("%s during a put %v", op, res)
		}
	}
	h.Offset = 3
	acc, _ := put(t, s, h, strings.NewReader("def"))
	failsWith(t, acc, wire.CodeInProgress, "retry")
	close(g.release)
	<-done
	out, _ := held.written()
	if lines := bytes.Split(bytes.TrimSpace(out), []byte("\n")); len(lines) != 2 || jsonLine(t, lines[1])["ok"] != true || jsonLine(t, lines[1]).Int("part_size") != 3 {
		t.Fatalf("the held put wrote %q", out)
	}
	if _, res := put(t, s, h, strings.NewReader("def")); res["ok"] != true || res.Int("part_size") != 6 {
		t.Fatalf("put after the held one %v", res)
	}
}

func TestTransferStreamsRefuseBadHeaders(t *testing.T) {
	_, s, home := xferHome(t)
	dst := filepath.Join(home, "f.txt")
	for _, tc := range []struct {
		h    wire.StreamHeader
		code string
		part string
	}{
		{wire.StreamHeader{Path: dst, Size: 3}, wire.CodeInvalidArgument, "transfer_id"},
		{wire.StreamHeader{TransferID: "t1", Size: 3}, wire.CodeInvalidArgument, "path"},
		{wire.StreamHeader{TransferID: "t1", Path: dst, Size: 3, Offset: 4}, wire.CodeInvalidArgument, "3"},
		{wire.StreamHeader{TransferID: "t1", Path: dst, Size: -1}, wire.CodeInvalidArgument, "size"},
		{wire.StreamHeader{TransferID: "t1", Path: dst, Size: 3, MTime: "noon"}, wire.CodeInvalidArgument, "mtime"},
		{wire.StreamHeader{TransferID: "t1", Path: filepath.Join(home, "none", "f.txt"), Size: 3}, wire.CodeNotFound, "fs.mkdir"},
		{wire.StreamHeader{TransferID: "t1", Path: dst, Size: 3, Roots: []string{filepath.Join(home, "x")}}, wire.CodePermissionDenied, "outside"},
	} {
		acc, _ := put(t, s, tc.h, strings.NewReader("abc"))
		failsWith(t, acc, tc.code, tc.part)
	}
	if got := names(t, home); len(got) != 0 {
		t.Fatalf("refused puts left %v", got)
	}

	link := dst + ".deskhand-part-tl"
	target := filepath.Join(home, "elsewhere.txt")
	writeFile(t, target, "elsewhere")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot make a link: %v", err)
	}
	acc, _ := put(t, s, wire.StreamHeader{TransferID: "tl", Path: dst, Size: 3}, strings.NewReader("abc"))
	failsWith(t, acc, wire.CodeInvalidArgument, link)
	if readFile(t, target) != "elsewhere" {
		t.Fatal("a put wrote through a link with the name of a part file")
	}
}

func TestTransferStreamsNeedTheFileOps(t *testing.T) {
	s := New(Options{HomeDir: t.TempDir(), Clipboard: &fakeClipboard{}, Logger: log.New(io.Discard, "", 0)})
	acc, _ := put(t, s, wire.StreamHeader{TransferID: "t1", Path: filepath.Join(t.TempDir(), "f"), Size: 1}, strings.NewReader("a"))
	failsWith(t, acc, wire.CodeCapabilityUnsupported, "deskhand-agent run")
	st := &fakeStream{in: strings.NewReader("")}
	s.ServeGet(context.Background(), st, wire.StreamHeader{S: "get", Path: t.TempDir()})
	out, _ := st.written()
	line, _, _ := bufio.NewReader(bytes.NewReader(out)).ReadLine()
	failsWith(t, jsonLine(t, line), wire.CodeCapabilityUnsupported, "deskhand-agent run")
}
