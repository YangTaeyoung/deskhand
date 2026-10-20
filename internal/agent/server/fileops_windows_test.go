//go:build windows

package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// realPC skips the test unless scripts/wintest.sh runs it in the signed-in
// user's session (DESKHAND_WINTEST=1).
func realPC(t *testing.T) {
	t.Helper()
	if os.Getenv("DESKHAND_WINTEST") != "1" {
		t.Skip("runs on the real PC through scripts/wintest.sh")
	}
}

// findRecycled returns the item of the current user's Recycle Bin on the
// volume of original that was deleted from original at since or later.
func findRecycled(t *testing.T, original string, since time.Time) (recycleRecord, bool) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(filepath.VolumeName(original)+`\`, "$Recycle.Bin", user.User.Sid.String())
	r, ok, err := findRecycleRecord(bin, original, since)
	if err != nil {
		t.Fatalf("read the Recycle Bin %s: %v", bin, err)
	}
	return r, ok
}

// removeRecycled removes the item r from the Recycle Bin, and only it.
func removeRecycled(t *testing.T, r recycleRecord) {
	t.Helper()
	if err := os.RemoveAll(r.data); err != nil {
		t.Errorf("remove %s: %v", r.data, err)
	}
	if err := os.Remove(r.info); err != nil {
		t.Errorf("remove %s: %v", r.info, err)
	}
}

// expectRecycled checks that original is gone and that the Recycle Bin holds
// an item deleted from it at since or later whose $R entry passes check;
// only then it removes that item, and only it, when the test ends.
func expectRecycled(t *testing.T, original string, since time.Time, check func(data string) error) {
	t.Helper()
	if _, err := os.Lstat(original); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s is still there: %v", original, err)
	}
	r, ok := findRecycled(t, original, since)
	if !ok {
		t.Fatalf("%s is not in the Recycle Bin", original)
	}
	if err := check(r.data); err != nil {
		t.Fatalf("the item %s recycled from %s differs: %v", r.data, original, err)
	}
	t.Cleanup(func() { removeRecycled(t, r) })
}

// holds returns a check that the file rel below the recycled entry (rel ""
// for the entry itself) has the content want.
func holds(rel, want string) func(string) error {
	return func(data string) error {
		b, err := os.ReadFile(filepath.Join(data, rel))
		if err != nil {
			return err
		}
		if string(b) != want {
			return fmt.Errorf("content %q", b)
		}
		return nil
	}
}

// exactPath returns prefix extended with ASCII names to exactly n
// characters.
func exactPath(prefix string, n int, letter byte) string {
	p := prefix
	for n-len(p) > 60 {
		p += `\` + strings.Repeat(string(letter), 50)
	}
	if k := n - len(p) - 1; k > 0 {
		p += `\` + strings.Repeat(string(letter+1), k)
	}
	return p
}

// longPath returns a path below dir that is at least n characters long.
func longPath(dir string, n int) string {
	p := dir
	for i := 0; len(p) < n; i++ {
		p = filepath.Join(p, strings.Repeat(string(rune('a'+i%26)), 40))
	}
	return p
}

func junction(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command(systemProgram("cmd.exe"), "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
}

func TestTrashDoesNotPermanentlyDelete(t *testing.T) {
	realPC(t)
	c, home := opsHome(t)
	start := time.Now()

	file := filepath.Join(home, "휴지통 😀.txt")
	writeFile(t, file, "into the bin")
	if res := c.ok("fs.trash", proto.Message{"path": file}); res["trashed"] != true {
		t.Fatalf("trash a file %v", res)
	}
	expectRecycled(t, file, start, holds("", "into the bin"))

	folder := filepath.Join(home, "folder")
	writeFile(t, filepath.Join(folder, "sub", "x.txt"), "x")
	if res := c.ok("fs.trash", proto.Message{"path": folder}); res["trashed"] != true {
		t.Fatalf("trash a folder %v", res)
	}
	expectRecycled(t, folder, start, holds(filepath.Join("sub", "x.txt"), "x"))

	for _, attr := range []uint32{windows.FILE_ATTRIBUTE_READONLY, windows.FILE_ATTRIBUTE_HIDDEN} {
		p := filepath.Join(home, fmt.Sprintf("attribute %#x.txt", attr))
		writeFile(t, p, "attr")
		setAttributes(t, p, attr)
		if res := c.ok("fs.trash", proto.Message{"path": p}); res["trashed"] != true {
			t.Fatalf("trash a file with the attribute %#x %v", attr, res)
		}
		expectRecycled(t, p, start, holds("", "attr"))
	}

	if res := c.ok("fs.trash", proto.Message{"path": filepath.Join(home, "gone.txt")}); res["trashed"] != false {
		t.Fatalf("trash a missing path %v", res)
	}

	for i, n := range []int{shellPathLimit - 1, shellPathLimit} {
		f := exactPath(filepath.Join(home, fmt.Sprintf("edge%d", i)), n, 'a')
		writeFile(t, f, "edge")
		holder := filepath.Join(home, fmt.Sprintf("holder%d", i))
		inner := exactPath(holder, n, 'c')
		writeFile(t, inner, "inner")
		for _, tc := range []struct{ item, rel, content string }{{f, "", "edge"}, {holder, inner[len(holder)+1:], "inner"}} {
			res := c.call("fs.trash", proto.Message{"path": tc.item})
			if n < shellPathLimit {
				if res["trashed"] != true {
					t.Fatalf("trash of %s with a %d-character path %v", tc.item, n, res)
				}
				expectRecycled(t, tc.item, start, holds(tc.rel, tc.content))
				continue
			}
			failsWith(t, res, wire.CodeCapabilityUnsupported, fmt.Sprintf("shorter than %d characters", shellPathLimit))
			if readFile(t, filepath.Join(tc.item, tc.rel)) != tc.content {
				t.Fatalf("%s changed", tc.item)
			}
		}
	}

	deep := filepath.Join(longPath(filepath.Join(home, "deep"), 270), "f.txt")
	writeFile(t, deep, "deep")
	for _, p := range []string{deep, filepath.Join(home, "deep")} {
		failsWith(t, c.call("fs.trash", proto.Message{"path": p}), wire.CodeCapabilityUnsupported, "250")
	}
	if readFile(t, deep) != "deep" {
		t.Fatal("the deep file changed")
	}

	kept := filepath.Join(home, "kept.txt")
	writeFile(t, kept, "kept")
	share := adminShare("localhost", kept)
	if _, err := os.Stat(share); err != nil {
		t.Logf("the administrative share is not reachable (%v); the network volume case is not run", err)
	} else {
		failsWith(t, c.call("fs.trash", proto.Message{"path": share}), wire.CodeCapabilityUnsupported, "Recycle Bin")
		if readFile(t, kept) != "kept" {
			t.Fatal("the file trashed through the network volume changed")
		}
		if _, ok := findRecycled(t, kept, start); ok {
			t.Fatal("the file is in the Recycle Bin")
		}
	}
}

// shortPath returns the 8.3 form of the existing path p.
func shortPath(t *testing.T, p string) string {
	t.Helper()
	name, err := windows.UTF16PtrFromString(extendedPath(p))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 1024)
	n, err := windows.GetShortPathName(name, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatalf("GetShortPathName %s: %v", p, err)
	}
	return stripLongPrefix(windows.UTF16ToString(buf[:n]))
}

func TestTrashTakesTheCanonicalPath(t *testing.T) {
	realPC(t)
	c, home := opsHome(t)
	start := time.Now()

	t.Run("short names", func(t *testing.T) {
		parent := home
		for i := 0; len(parent) < 270; i++ {
			parent = filepath.Join(parent, fmt.Sprintf("a long folder name number %d with spaces in it", i))
		}
		file := filepath.Join(parent, "f.txt")
		writeFile(t, file, "short")
		short := shortPath(t, parent)
		if short == parent || utf16Len(filepath.Join(short, "f.txt")) >= shellPathLimit {
			t.Skipf("the volume gives no short names that fit (%s)", short)
		}
		failsWith(t, c.call("fs.trash", proto.Message{"path": filepath.Join(short, "f.txt")}), wire.CodeCapabilityUnsupported, "characters long")
		if readFile(t, file) != "short" {
			t.Fatal("the file changed")
		}
	})

	t.Run("volume GUID", func(t *testing.T) {
		root, err := windows.UTF16PtrFromString(filepath.VolumeName(home) + `\`)
		if err != nil {
			t.Fatal(err)
		}
		guid := make([]uint16, 64)
		if err := windows.GetVolumeNameForVolumeMountPoint(root, &guid[0], uint32(len(guid))); err != nil {
			t.Fatal(err)
		}
		volume := strings.TrimSuffix(windows.UTF16ToString(guid), `\`)
		file := filepath.Join(home, "volume.txt")
		writeFile(t, file, "volume")
		if res := c.ok("fs.trash", proto.Message{"path": volume + file[2:]}); res["trashed"] != true {
			t.Fatalf("trash through the volume GUID %v", res)
		}
		expectRecycled(t, file, start, holds("", "volume"))
		deep := filepath.Join(longPath(filepath.Join(home, "deep volume"), 270), "f.txt")
		writeFile(t, deep, "deep")
		failsWith(t, c.call("fs.trash", proto.Message{"path": volume + deep[2:]}), wire.CodeCapabilityUnsupported, "characters long")
		if readFile(t, deep) != "deep" {
			t.Fatal("the deep file changed")
		}
	})

	t.Run("folder link to the administrative share", func(t *testing.T) {
		target := filepath.Join(home, "target")
		writeFile(t, filepath.Join(target, "file.txt"), "target")
		share := adminShare("localhost", target)
		if _, err := os.Stat(share); err != nil {
			t.Skipf("the administrative share is not reachable: %v", err)
		}
		link := filepath.Join(home, "share-link")
		if out, err := exec.Command(systemProgram("cmd.exe"), "/c", "mklink", "/D", link, share).CombinedOutput(); err != nil {
			t.Skipf("mklink /D: %v %s", err, out)
		}
		failsWith(t, c.call("fs.trash", proto.Message{"path": filepath.Join(link, "file.txt")}), wire.CodeCapabilityUnsupported, "not on a drive letter")
		if readFile(t, filepath.Join(target, "file.txt")) != "target" {
			t.Fatal("the file behind the link changed")
		}
		if res := c.ok("fs.trash", proto.Message{"path": link}); res["trashed"] != true {
			t.Fatalf("trash the link itself %v", res)
		}
		expectRecycled(t, link, start, func(data string) error {
			fi, err := os.Lstat(data)
			if err == nil && fi.Mode()&fs.ModeSymlink == 0 {
				err = fmt.Errorf("mode %v", fi.Mode())
			}
			return err
		})
		if readFile(t, filepath.Join(target, "file.txt")) != "target" {
			t.Fatal("the folder the link led to changed")
		}
	})
}

func TestVerifyRecycledReportsAnItemNotInTheRecycleBin(t *testing.T) {
	realPC(t)
	path := filepath.Join(opsDir(t), "never trashed.txt")
	err := verifyRecycled(path, time.Now())
	if err == nil {
		t.Fatal("an item that was never trashed counts as recycled")
	}
	failsWith(t, failure(1, err), wire.CodeInternal, "was deleted permanently instead of being moved to the Recycle Bin")
}

// setAttributes sets the file attributes attr on path.
func setAttributes(t *testing.T, path string, attr uint32) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetFileAttributes(p, attr); err != nil {
		t.Fatal(err)
	}
}

// holdOpen opens path without sharing it until the test ends.
func holdOpen(t *testing.T, path string) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
}

func TestFileOpsOnAFileInUse(t *testing.T) {
	realPC(t)
	c, home := opsHome(t)
	held := filepath.Join(home, "held.txt")
	writeFile(t, held, "held")
	folder := filepath.Join(home, "folder")
	writeFile(t, filepath.Join(folder, "inner.txt"), "inner")
	holdOpen(t, held)
	holdOpen(t, filepath.Join(folder, "inner.txt"))
	for _, tc := range []struct {
		op  string
		req proto.Message
	}{
		{"fs.copy", proto.Message{"from": held, "to": filepath.Join(home, "copy.txt")}},
		{"fs.hash", proto.Message{"path": held}},
		{"fs.trash", proto.Message{"path": held}},
		{"fs.trash", proto.Message{"path": folder}},
	} {
		res := c.call(tc.op, tc.req)
		if res["ok"] != false || res.String("code") != wire.CodePermissionDenied || res["retryable"] != true || !strings.Contains(res.String("error"), "close") {
			t.Fatalf("%s %v: %v", tc.op, tc.req, res)
		}
	}
	if got := names(t, home); strings.Join(got, "|") != "folder|held.txt" {
		t.Fatalf("entries %q", got)
	}
	if _, ok := findRecycled(t, held, time.Time{}); ok {
		t.Fatal("the file in use is in the Recycle Bin")
	}
}

func TestPathEdgeCases(t *testing.T) {
	realPC(t)
	c, home := opsHome(t)
	digest := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	}

	t.Run("hangul and emoji", func(t *testing.T) {
		src := filepath.Join(home, "한글 폴더 😀", "파일 😀.txt")
		writeFile(t, src, "안녕 😀")
		dst := filepath.Join(home, "한글 폴더 😀", "복사 😀.txt")
		if res := c.ok("fs.copy", proto.Message{"from": src, "to": dst}); res.String("path") != dst || readFile(t, dst) != "안녕 😀" {
			t.Fatalf("copy %v", res)
		}
		if res := c.ok("fs.hash", proto.Message{"path": dst}); res.String("sha256") != digest("안녕 😀") {
			t.Fatalf("hash %v", res)
		}
	})

	t.Run("300 characters", func(t *testing.T) {
		dir := longPath(filepath.Join(home, "long"), 300)
		src := filepath.Join(dir, "원본.txt")
		writeFile(t, src, "long")
		if n := len(utf16.Encode([]rune(src))); n < 300 {
			t.Fatalf("the path is %d characters long", n)
		}
		dst := filepath.Join(dir, "복사.txt")
		if res := c.ok("fs.copy", proto.Message{"from": src, "to": dst}); res.String("path") != dst || readFile(t, dst) != "long" {
			t.Fatalf("copy %v", res)
		}
		if res := c.ok("fs.hash", proto.Message{"path": dst}); res.String("sha256") != digest("long") {
			t.Fatalf("hash %v", res)
		}
		res := c.ok("fs.copy", proto.Message{"from": filepath.Join(home, "long"), "to": filepath.Join(home, "long copy"), "recursive": true})
		if res.Int("copied") != 2 || readFile(t, filepath.Join(home, "long copy", dir[len(filepath.Join(home, "long")):], "복사.txt")) != "long" {
			t.Fatalf("copy of the folder %v", res)
		}
		failsWith(t, c.call("fs.trash", proto.Message{"path": src}), wire.CodeCapabilityUnsupported, "250")
		if readFile(t, src) != "long" {
			t.Fatal("the long file changed")
		}
	})

	t.Run("reserved names and trailing dots", func(t *testing.T) {
		src := filepath.Join(home, "plain.txt")
		writeFile(t, src, "p")
		for _, name := range []string{"CON.txt", "con", "NUL.tar.gz", "COM1", "LPT9.log", "name.", "name ", "a:stream", "a?b"} {
			failsWith(t, c.call("fs.copy", proto.Message{"from": src, "to": filepath.Join(home, name)}), wire.CodeInvalidArgument, "Windows")
			failsWith(t, c.call("fs.hash", proto.Message{"path": filepath.Join(home, name)}), wire.CodeInvalidArgument, "Windows")
		}
		failsWith(t, c.call("fs.copy", proto.Message{"from": src, "to": filepath.Join(home, "CON", "x.txt")}), wire.CodeInvalidArgument, "device")
		failsWith(t, c.call("fs.hash", proto.Message{"path": `\\.\PhysicalDrive0`}), wire.CodeInvalidArgument, "device")
		for _, e := range names(t, home) {
			if e != "plain.txt" && !strings.HasPrefix(e, "한글") && !strings.HasPrefix(e, "long") {
				t.Fatalf("an entry %q was created", e)
			}
		}
	})

	t.Run("letter case conflict", func(t *testing.T) {
		dir := filepath.Join(home, "case")
		existing := filepath.Join(dir, "Readme.md")
		writeFile(t, existing, "existing")
		src := filepath.Join(home, "plain.txt")
		writeFile(t, src, "p")
		upper := filepath.Join(dir, "README.md")
		for _, policy := range []string{"fail", "replace"} {
			failsWith(t, c.call("fs.copy", proto.Message{"from": src, "to": upper, "on_conflict": policy}), wire.CodeTransferConflict, "'Readme.md'")
		}
		if readFile(t, existing) != "existing" {
			t.Fatal("the existing file changed")
		}
		res := c.ok("fs.copy", proto.Message{"from": src, "to": upper, "on_conflict": "skip"})
		if res.String("path") != existing || res.String("outcome") != "skipped" {
			t.Fatalf("skip %v", res)
		}
		res = c.ok("fs.copy", proto.Message{"from": src, "to": upper, "on_conflict": "rename"})
		if want := filepath.Join(dir, "README (1).md"); res.String("path") != want || readFile(t, want) != "p" {
			t.Fatalf("rename %v", res)
		}
		if got := names(t, dir); strings.Join(got, "|") != "README (1).md|Readme.md" {
			t.Fatalf("entries %q", got)
		}
	})

	t.Run("junction out of the root", func(t *testing.T) {
		root, outside := filepath.Join(home, "root"), filepath.Join(home, "outside")
		writeFile(t, filepath.Join(root, "inside.txt"), "in")
		writeFile(t, filepath.Join(outside, "secret.txt"), "secret")
		junction(t, filepath.Join(root, "j"), outside)
		roots := []any{root}
		through := filepath.Join(root, "j", "secret.txt")
		failsWith(t, c.call("fs.hash", proto.Message{"path": through, "roots": roots}), wire.CodePermissionDenied, "outside")
		failsWith(t, c.call("fs.hash", proto.Message{"path": filepath.Join(home, "ROOT", "J", "secret.txt"), "roots": roots}), wire.CodePermissionDenied, "outside")
		c.ok("fs.hash", proto.Message{"path": filepath.Join(home, "ROOT", "inside.txt"), "roots": roots})
		failsWith(t, c.call("fs.copy", proto.Message{"from": filepath.Join(root, "inside.txt"), "to": filepath.Join(root, "j", "x.txt"), "roots": roots}),
			wire.CodePermissionDenied, "outside")
		failsWith(t, c.call("fs.copy", proto.Message{"from": through, "to": filepath.Join(root, "y.txt"), "roots": roots}), wire.CodePermissionDenied, "outside")
		failsWith(t, c.call("fs.trash", proto.Message{"path": through, "roots": roots}), wire.CodePermissionDenied, "outside")
		failsWith(t, c.call("fs.trash", proto.Message{"path": filepath.Join(root, "j"), "roots": roots}), wire.CodePermissionDenied, "outside")
		failsWith(t, c.call("fs.copy", proto.Message{"from": root, "to": filepath.Join(home, "root copy"), "recursive": true}), wire.CodeInvalidArgument, "link")
		if got := names(t, outside); strings.Join(got, "|") != "secret.txt" || readFile(t, filepath.Join(outside, "secret.txt")) != "secret" {
			t.Fatalf("outside holds %q", got)
		}
		if res := c.ok("fs.hash", proto.Message{"path": through}); res.String("sha256") != digest("secret") {
			t.Fatalf("hash without roots %v", res)
		}
	})
}
