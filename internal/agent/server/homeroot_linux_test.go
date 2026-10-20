//go:build linux

package server

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// owner returns the user and group ids of path, without following a link.
func owner(t *testing.T, path string) (int, int) {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	return int(st.Uid), int(st.Gid)
}

func TestEntriesCreatedInTheHomeBelongToTheSessionUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("giving files to another user needs root")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(home, "kept.txt")
	if err := os.WriteFile(existing, []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	const uid, gid = 65534, 65534
	c := sessionClient(t, session.Session{User: "nobody", UID: uid, GID: gid, Home: home}, true)
	data := base64.StdEncoding.EncodeToString([]byte("x"))
	c.ok("fs.write", proto.Message{"path": "proj/src/main.c.deskhand-part", "data": data, "mkdirs": true, "truncate": true})
	c.ok("fs.rename", proto.Message{"from": "proj/src/main.c.deskhand-part", "to": "proj/src/main.c"})
	c.ok("fs.mkdir", proto.Message{"path": "proj/out/bin"})
	c.ok("fs.write", proto.Message{"path": "kept.txt", "data": data, "append": true})
	for _, rel := range []string{"proj", "proj/src", "proj/src/main.c", "proj/out", "proj/out/bin"} {
		if u, g := owner(t, filepath.Join(home, rel)); u != uid || g != gid {
			t.Errorf("%s belongs to %d:%d", rel, u, g)
		}
	}
	if u, _ := owner(t, existing); u != 0 {
		t.Errorf("kept.txt, which existed, now belongs to %d", u)
	}
	if fi, _ := os.Stat(filepath.Join(home, "proj", "src", "main.c")); fi.Mode().Perm() != 0o644 {
		t.Errorf("main.c mode %v", fi.Mode())
	}
}

func TestAHardLinkedFileOfTheSessionUserIsWritten(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("giving files to another user needs root")
	}
	home := t.TempDir()
	own := filepath.Join(home, "own.txt")
	if err := os.WriteFile(own, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(own, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(own, filepath.Join(home, "second.txt")); err != nil {
		t.Fatal(err)
	}
	c := sessionClient(t, session.Session{User: "nobody", UID: 65534, GID: 65534, Home: home}, true)
	c.ok("fs.write", proto.Message{"path": "second.txt", "data": base64.StdEncoding.EncodeToString([]byte("new")), "truncate": true})
	if b, _ := os.ReadFile(own); string(b) != "new" {
		t.Fatalf("own.txt holds %q", b)
	}
}
