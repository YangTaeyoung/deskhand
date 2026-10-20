//go:build windows

package server

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// diskFullError is the error of a write to a full volume.
func diskFullError() error { return windows.ERROR_DISK_FULL }

// icacls runs icacls with args.
func icacls(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command(systemProgram("icacls.exe"), args...).CombinedOutput(); err != nil {
		t.Fatalf("icacls %v: %v %s", args, err, out)
	}
}

// openWithoutSharing opens path without sharing it and returns the function
// that closes it.
func openWithoutSharing(t *testing.T, path string) func() {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	undo := func() {
		if !closed {
			closed = true
			windows.CloseHandle(h)
		}
	}
	t.Cleanup(undo)
	return undo
}

// replaceDenials are the ways a destination refuses its replacement: it is
// read-only, access control entries deny deleting it and the entries of
// its folder, or another program has it open without sharing it.
func replaceDenials() []denial {
	return []denial{
		{
			name: "read-only attribute",
			apply: func(t *testing.T, path string) func() {
				setAttributes(t, path, windows.FILE_ATTRIBUTE_READONLY)
				undo := func() { setAttributes(t, path, windows.FILE_ATTRIBUTE_NORMAL) }
				t.Cleanup(undo)
				return undo
			},
		},
		{
			name: "access control entries that deny the deletion",
			apply: func(t *testing.T, path string) func() {
				dir := filepath.Dir(path)
				icacls(t, path, "/deny", "*S-1-1-0:(DE)")
				icacls(t, dir, "/deny", "*S-1-1-0:(DC)")
				done := false
				undo := func() {
					if !done {
						done = true
						icacls(t, dir, "/remove:d", "*S-1-1-0")
						icacls(t, path, "/remove:d", "*S-1-1-0")
					}
				}
				t.Cleanup(undo)
				return undo
			},
		},
		{
			name:      "open in another program without sharing",
			retryable: true,
			apply:     openWithoutSharing,
		},
	}
}

// attributes returns the file attributes of path.
func attributes(t *testing.T, path string) uint32 {
	t.Helper()
	p, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		t.Fatal(err)
	}
	a, err := windows.GetFileAttributes(p)
	if err != nil {
		t.Fatalf("attributes of %s: %v", path, err)
	}
	return a
}

func TestThePartFileIsHiddenAndTheCommittedFileIsNot(t *testing.T) {
	realPC(t)
	c, s, home := xferHome(t)
	dst := filepath.Join(home, "shown.txt")
	data := []byte("shown after the commit")
	c.ok("xfer.prepare", proto.Message{"transfer_id": "th", "path": dst, "size": len(data)})
	part := dst + ".deskhand-part-th"
	if attributes(t, part)&windows.FILE_ATTRIBUTE_HIDDEN == 0 {
		t.Fatal("the part file is not hidden")
	}
	if _, res := put(t, s, wire.StreamHeader{TransferID: "th", Path: dst, Size: int64(len(data))}, bytes.NewReader(data)); res["ok"] != true {
		t.Fatalf("put %v", res)
	}
	if attributes(t, part)&windows.FILE_ATTRIBUTE_HIDDEN == 0 {
		t.Fatal("the part file is not hidden after a put")
	}
	c.ok("xfer.commit", proto.Message{"transfer_id": "th", "path": dst, "sha256": sha(data)})
	if attributes(t, dst)&windows.FILE_ATTRIBUTE_HIDDEN != 0 {
		t.Fatal("the committed file is hidden")
	}
}

func TestATransferReachesAPathLongerThanMaxPath(t *testing.T) {
	realPC(t)
	c, s, home := xferHome(t)
	dir := longPath(filepath.Join(home, "deep"), 300)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "far.bin")
	writeFile(t, dst, "the old far file")
	data := randomBytes(5000)
	c.ok("xfer.prepare", proto.Message{"transfer_id": "tl", "path": dst, "size": len(data), "on_conflict": "replace"})
	if _, res := put(t, s, wire.StreamHeader{TransferID: "tl", Path: dst, Size: int64(len(data))}, bytes.NewReader(data)); res["ok"] != true {
		t.Fatalf("put %v", res)
	}
	acc, got, fin := get(t, s, wire.StreamHeader{Path: dst + ".deskhand-part-tl"})
	if acc["ok"] != true || !bytes.Equal(got, data) || !fin {
		t.Fatalf("get of the part file %v, %d bytes", acc, len(got))
	}
	if res := c.ok("xfer.commit", proto.Message{"transfer_id": "tl", "path": dst, "sha256": sha(data), "on_conflict": "replace"}); res.String("outcome") != "written" {
		t.Fatalf("commit %v", res)
	}
	if readFile(t, dst) != string(data) {
		t.Fatal("the far file differs from the data")
	}
	if strings.Contains(strings.Join(names(t, dir), " "), "deskhand-part") {
		t.Fatal("a part file is left")
	}
}
