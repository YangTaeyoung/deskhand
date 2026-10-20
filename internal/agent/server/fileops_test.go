package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// opsDir returns a new folder for one test that is removed when the test
// ends: in the working folder of the test binary when scripts/wintest.sh
// runs it (DESKHAND_WINTEST=1), in the test's temporary folder otherwise.
func opsDir(t *testing.T) string {
	t.Helper()
	if os.Getenv("DESKHAND_WINTEST") != "1" {
		return t.TempDir()
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(wd, "t")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// opsHome starts an agent whose home is a new opsDir.
func opsHome(t *testing.T) (*rpcClient, string) {
	t.Helper()
	home := opsDir(t)
	c, _ := newRPCClient(t, Options{HomeDir: home})
	return c, home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// names returns the sorted names in dir.
func names(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

// failsWith checks that res is a failure with code whose error contains part.
func failsWith(t *testing.T, res proto.Message, code, part string) {
	t.Helper()
	if res["ok"] != false || res.String("code") != code || !strings.Contains(res.String("error"), part) {
		t.Fatalf("expected %s with %q, got %v", code, part, res)
	}
}

func TestCopyConflictPolicies(t *testing.T) {
	c, home := opsHome(t)
	src, dst := filepath.Join(home, "src.txt"), filepath.Join(home, "dst.txt")
	writeFile(t, src, "new")
	mtime := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chtimes(src, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	res := c.ok("fs.copy", proto.Message{"from": src, "to": dst})
	if res.String("path") != dst || res.Int("copied") != 1 || res.String("outcome") != "written" || readFile(t, dst) != "new" {
		t.Fatalf("copy to a free path %v", res)
	}
	if fi, err := os.Stat(dst); err != nil || !fi.ModTime().Equal(mtime) {
		t.Fatalf("mtime of the copy %v %v", fi.ModTime(), err)
	}

	writeFile(t, dst, "old")
	for _, req := range []proto.Message{{"from": src, "to": dst}, {"from": src, "to": dst, "on_conflict": "fail"}} {
		failsWith(t, c.call("fs.copy", req), wire.CodeTransferConflict, "on_conflict")
	}
	if readFile(t, dst) != "old" {
		t.Fatal("fail changed the destination")
	}

	res = c.ok("fs.copy", proto.Message{"from": src, "to": dst, "on_conflict": "skip"})
	if res.String("path") != dst || res.Int("copied") != 0 || res.String("outcome") != "skipped" || readFile(t, dst) != "old" {
		t.Fatalf("skip %v", res)
	}

	res = c.ok("fs.copy", proto.Message{"from": src, "to": dst, "on_conflict": "replace"})
	if res.String("path") != dst || res.Int("copied") != 1 || res.String("outcome") != "written" || readFile(t, dst) != "new" {
		t.Fatalf("replace %v", res)
	}

	writeFile(t, dst, "old")
	for i, want := range []string{"dst (1).txt", "dst (2).txt"} {
		res = c.ok("fs.copy", proto.Message{"from": src, "to": dst, "on_conflict": "rename"})
		got := filepath.Join(home, want)
		if res.String("path") != got || res.Int("copied") != 1 || res.String("outcome") != "renamed" || readFile(t, got) != "new" {
			t.Fatalf("rename %d %v", i, res)
		}
	}
	if readFile(t, dst) != "old" {
		t.Fatal("rename changed the destination")
	}

	failsWith(t, c.call("fs.copy", proto.Message{"from": src, "to": dst, "on_conflict": "merge"}), wire.CodeInvalidArgument, "on_conflict")
	if got, want := names(t, home), []string{"dst (1).txt", "dst (2).txt", "dst.txt", "src.txt"}; !slices.Equal(got, want) {
		t.Fatalf("entries %q, expected %q", got, want)
	}
}

func TestCopyFolderAppliesThePolicyToTheFolder(t *testing.T) {
	c, home := opsHome(t)
	src, dst := filepath.Join(home, "src"), filepath.Join(home, "dst")
	writeFile(t, filepath.Join(src, "a.txt"), "a")
	writeFile(t, filepath.Join(src, "sub", "b.txt"), "b")
	if err := os.Mkdir(filepath.Join(src, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	failsWith(t, c.call("fs.copy", proto.Message{"from": src, "to": dst}), wire.CodeInvalidArgument, "recursive")
	res := c.ok("fs.copy", proto.Message{"from": src, "to": dst, "recursive": true})
	if res.String("path") != dst || res.Int("copied") != 2 || res.String("outcome") != "written" {
		t.Fatalf("copy %v", res)
	}
	if readFile(t, filepath.Join(dst, "a.txt")) != "a" || readFile(t, filepath.Join(dst, "sub", "b.txt")) != "b" {
		t.Fatal("contents differ")
	}
	if fi, err := os.Stat(filepath.Join(dst, "empty")); err != nil || !fi.IsDir() {
		t.Fatalf("empty folder %v", err)
	}

	writeFile(t, filepath.Join(dst, "a.txt"), "changed")
	failsWith(t, c.call("fs.copy", proto.Message{"from": src, "to": dst, "recursive": true}), wire.CodeTransferConflict, "on_conflict")
	failsWith(t, c.call("fs.copy", proto.Message{"from": src, "to": dst, "recursive": true, "on_conflict": "replace"}), wire.CodeTransferConflict, "folder")
	if readFile(t, filepath.Join(dst, "a.txt")) != "changed" {
		t.Fatal("the destination folder changed")
	}
	res = c.ok("fs.copy", proto.Message{"from": src, "to": dst, "recursive": true, "on_conflict": "skip"})
	if res.String("outcome") != "skipped" || res.Int("copied") != 0 {
		t.Fatalf("skip %v", res)
	}
	res = c.ok("fs.copy", proto.Message{"from": src, "to": dst, "recursive": true, "on_conflict": "rename"})
	renamed := filepath.Join(home, "dst (1)")
	if res.String("path") != renamed || res.String("outcome") != "renamed" || readFile(t, filepath.Join(renamed, "sub", "b.txt")) != "b" {
		t.Fatalf("rename %v", res)
	}

	failsWith(t, c.call("fs.copy", proto.Message{"from": src, "to": filepath.Join(src, "sub", "inner"), "recursive": true}),
		wire.CodeInvalidArgument, "into itself")
	if _, err := os.Lstat(filepath.Join(src, "sub", "inner")); err == nil {
		t.Fatal("a copy into the source folder was made")
	}
	res = c.ok("fs.copy", proto.Message{"from": src, "to": src, "recursive": true, "on_conflict": "rename"})
	if twin := filepath.Join(home, "src (1)"); res.String("path") != twin || res.Int("copied") != 2 || readFile(t, filepath.Join(twin, "a.txt")) != "a" {
		t.Fatalf("copy of a folder onto itself with rename %v", res)
	}
	if got, want := names(t, home), []string{"dst", "dst (1)", "src", "src (1)"}; !slices.Equal(got, want) {
		t.Fatalf("entries %q, expected %q", got, want)
	}
}

func TestCopyRefusesLinksMissingPathsAndMissingFolders(t *testing.T) {
	c, home := opsHome(t)
	writeFile(t, filepath.Join(home, "f.txt"), "f")
	failsWith(t, c.call("fs.copy", proto.Message{"from": filepath.Join(home, "gone.txt"), "to": filepath.Join(home, "x.txt")}), wire.CodeNotFound, "does not exist")
	failsWith(t, c.call("fs.copy", proto.Message{"from": filepath.Join(home, "f.txt"), "to": filepath.Join(home, "no", "x.txt")}), wire.CodeNotFound, "fs.mkdir")
	failsWith(t, c.call("fs.copy", proto.Message{"from": filepath.Join(home, "f.txt")}), wire.CodeInvalidArgument, "missing path")

	outside := opsDir(t)
	writeFile(t, filepath.Join(outside, "secret.txt"), "s")
	writeFile(t, filepath.Join(home, "tree", "own.txt"), "o")
	symlink(t, outside, filepath.Join(home, "tree", "link"))
	failsWith(t, c.call("fs.copy", proto.Message{"from": filepath.Join(home, "tree"), "to": filepath.Join(home, "copy"), "recursive": true}),
		wire.CodeInvalidArgument, "link")
	failsWith(t, c.call("fs.copy", proto.Message{"from": filepath.Join(home, "tree", "link"), "to": filepath.Join(home, "copy"), "recursive": true}),
		wire.CodeInvalidArgument, "link")
	if got, want := names(t, home), []string{"f.txt", "tree"}; !slices.Equal(got, want) {
		t.Fatalf("entries %q, expected %q", got, want)
	}
}

func TestCopyStopsWhenTheConnectionCloses(t *testing.T) {
	home := opsDir(t)
	s := New(Options{Clipboard: &fakeClipboard{}, HomeDir: home, AllowExec: true})
	defer s.Close()
	writeFile(t, filepath.Join(home, "src", "a.txt"), "a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, from := range []string{filepath.Join(home, "src"), filepath.Join(home, "src", "a.txt")} {
		_, err := s.fsCopy(ctx, proto.Message{"from": from, "to": filepath.Join(home, "dst"), "recursive": true})
		res := failure(1, err)
		failsWith(t, res, wire.CodeInternal, "connection closed")
	}
	if got, want := names(t, home), []string{"src"}; !slices.Equal(got, want) {
		t.Fatalf("entries %q, expected %q", got, want)
	}
}

func TestHashRange(t *testing.T) {
	c, home := opsHome(t)
	data := make([]byte, 1<<20+17)
	rand.Read(data)
	path := filepath.Join(home, "data.bin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := func(b []byte) string {
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	size := len(data)
	for _, tc := range []struct {
		offset, length int
		want           []byte
	}{
		{0, 0, data},
		{100, 5000, data[100:5100]},
		{size - 10, 100, data[size-10:]},
		{size, 0, nil},
	} {
		res := c.ok("fs.hash", proto.Message{"path": path, "offset": tc.offset, "length": tc.length})
		if res.String("sha256") != digest(tc.want) || res.Int("offset") != tc.offset || res.Int("length") != len(tc.want) || res.Int("size") != size {
			t.Fatalf("offset %d length %d: %v", tc.offset, tc.length, res)
		}
	}
	if res := c.ok("fs.hash", proto.Message{"path": "data.bin"}); res.String("sha256") != digest(data) {
		t.Fatalf("home-relative path %v", res)
	}
	failsWith(t, c.call("fs.hash", proto.Message{"path": path, "offset": size + 1}), wire.CodeInvalidArgument, "past the end")
	failsWith(t, c.call("fs.hash", proto.Message{"path": path, "offset": -1}), wire.CodeInvalidArgument, "offset")
	failsWith(t, c.call("fs.hash", proto.Message{"path": path, "length": -1}), wire.CodeInvalidArgument, "length")
	failsWith(t, c.call("fs.hash", proto.Message{"path": home}), wire.CodeInvalidArgument, "folder")
	failsWith(t, c.call("fs.hash", proto.Message{"path": filepath.Join(home, "gone")}), wire.CodeNotFound, "does not exist")
}

func TestRootsConfineTheFileOps(t *testing.T) {
	c, home := opsHome(t)
	root, outside := filepath.Join(home, "root"), filepath.Join(home, "outside")
	writeFile(t, filepath.Join(root, "inside.txt"), "in")
	writeFile(t, filepath.Join(outside, "secret.txt"), "secret")
	symlink(t, outside, filepath.Join(root, "link"))
	symlink(t, root, filepath.Join(home, "root-link"))
	roots := []any{root}

	failsWith(t, c.call("fs.hash", proto.Message{"path": filepath.Join(root, "link", "secret.txt"), "roots": roots}), wire.CodePermissionDenied, "outside")
	failsWith(t, c.call("fs.hash", proto.Message{"path": filepath.Join(outside, "secret.txt"), "roots": roots}), wire.CodePermissionDenied, "outside")
	c.ok("fs.hash", proto.Message{"path": filepath.Join(root, "inside.txt"), "roots": roots})
	c.ok("fs.hash", proto.Message{"path": filepath.Join(root, "inside.txt"), "roots": []any{filepath.Join(home, "root-link")}})
	c.ok("fs.hash", proto.Message{"path": filepath.Join(root, "link", "secret.txt")})

	failsWith(t, c.call("fs.copy", proto.Message{"from": filepath.Join(root, "inside.txt"), "to": filepath.Join(root, "link", "x.txt"), "roots": roots}),
		wire.CodePermissionDenied, "outside")
	failsWith(t, c.call("fs.copy", proto.Message{"from": filepath.Join(outside, "secret.txt"), "to": filepath.Join(root, "y.txt"), "roots": roots}),
		wire.CodePermissionDenied, "outside")
	failsWith(t, c.call("fs.trash", proto.Message{"path": filepath.Join(root, "link"), "roots": roots}), wire.CodePermissionDenied, "outside")
	failsWith(t, c.call("fs.trash", proto.Message{"path": filepath.Join(outside, "secret.txt"), "roots": roots}), wire.CodePermissionDenied, "outside")
	res := c.ok("fs.copy", proto.Message{"from": filepath.Join(root, "inside.txt"), "to": filepath.Join(root, "copy.txt"), "roots": roots})
	if res.String("path") != filepath.Join(root, "copy.txt") {
		t.Fatalf("copy inside the root %v", res)
	}
	if got, want := names(t, outside), []string{"secret.txt"}; !slices.Equal(got, want) {
		t.Fatalf("outside holds %q", got)
	}

	failsWith(t, c.call("fs.hash", proto.Message{"path": filepath.Join(root, "inside.txt"), "roots": "root"}), wire.CodeInvalidArgument, "roots")
	failsWith(t, c.call("fs.hash", proto.Message{"path": filepath.Join(root, "inside.txt"), "roots": []any{"root"}}), wire.CodeInvalidArgument, "absolute")
}

func TestTrashOutsideWindowsIsUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fs.trash uses the Recycle Bin on Windows")
	}
	c, home := opsHome(t)
	path := filepath.Join(home, "f.txt")
	writeFile(t, path, "kept")
	failsWith(t, c.call("fs.trash", proto.Message{"path": path}), wire.CodeCapabilityUnsupported, "fs.delete")
	if readFile(t, path) != "kept" {
		t.Fatal("the file changed")
	}
	if res := c.ok("fs.trash", proto.Message{"path": filepath.Join(home, "gone")}); res["trashed"] != false {
		t.Fatalf("missing path %v", res)
	}
}

func TestWindowsPathRules(t *testing.T) {
	for _, p := range []string{
		`C:\x\CON`, `C:\x\con.txt`, `C:\x\Nul.tar.gz`, `C:\x\COM1`, `C:\x\lpt9.log`, `C:\x\COM¹`, `C:\x\CONIN$`, `C:\x\AUX .txt`,
		`C:\CON\x.txt`, `C:\x\name.`, `C:\x\name `, `C:\x\a<b`, `C:\x\a:stream`, `C:\x\a?b`, "C:\\x\\a\x01b", `\\srv\share\PRN`,
		`\\.\PhysicalDrive0`, `//./C:/x`, `\\?\GLOBALROOT\Device\HarddiskVolume1\x`,
	} {
		if err := windowsPathError(p); err == nil || !strings.Contains(err.Error(), "Windows") {
			t.Errorf("%q: %v", p, err)
		}
	}
	for _, p := range []string{
		`C:\`, `C:\x\CONSOLE.txt`, `C:\x\COM10`, `C:\x\com`, `C:\x\LPT`, `C:\x\a.b`, `C:\x\.hidden`, `C:\한글 폴더 😀\파일.txt`,
		`\\srv\share\x`, `\\?\Volume{0f1e2d3c-0000-0000-0000-100000000000}\x`, `C:/x/y`,
	} {
		if err := windowsPathError(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	for in, want := range map[string]string{`\\?\C:\x`: `C:\x`, `\\?\UNC\srv\share\x`: `\\srv\share\x`, `\\?\unc\srv\share`: `\\srv\share`, `C:\x`: `C:\x`} {
		if got := stripLongPrefix(in); got != want {
			t.Errorf("stripLongPrefix(%q) = %q, expected %q", in, got, want)
		}
	}
}

func TestNumberedName(t *testing.T) {
	for _, tc := range []struct {
		path   string
		folder bool
		want   string
	}{
		{filepath.Join("d", "a.txt"), false, filepath.Join("d", "a (2).txt")},
		{filepath.Join("d", "a.tar.gz"), false, filepath.Join("d", "a.tar (2).gz")},
		{filepath.Join("d", ".bashrc"), false, filepath.Join("d", ".bashrc (2)")},
		{filepath.Join("d", "v1.2"), true, filepath.Join("d", "v1.2 (2)")},
	} {
		if got := numberedName(tc.path, 2, tc.folder); got != tc.want {
			t.Errorf("numberedName(%q) = %q, expected %q", tc.path, got, tc.want)
		}
	}
}
