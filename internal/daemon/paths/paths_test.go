package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathsFollowDeskhandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DESKHAND_HOME", home)
	for got, want := range map[string]string{
		Socket():         filepath.Join(home, "run", "d.sock"),
		DB():             filepath.Join(home, "deskhand.db"),
		Artifacts():      filepath.Join(home, "artifacts"),
		HostDir("pc"):    filepath.Join(home, "hosts", "pc"),
		DaemonLog():      filepath.Join(home, "logs", "daemon.log"),
		Lock():           filepath.Join(home, "run", "daemon.lock"),
		ControllerKey():  filepath.Join(home, "identity", "controller.key"),
		ControllerCert(): filepath.Join(home, "identity", "controller.crt"),
	} {
		if got != want {
			t.Errorf("path %s, want %s", got, want)
		}
	}
	if err := Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{Run(), Logs(), Identity(), Hosts(), Artifacts()} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("folder %s: %v, %v", d, fi, err)
		}
	}
}

func TestDefaultHome(t *testing.T) {
	t.Setenv("DESKHAND_HOME", "")
	if !strings.HasSuffix(Home(), filepath.Join("Library", "Application Support", "Deskhand")) {
		t.Fatalf("home %s", Home())
	}
}

func TestCheckSocketLength(t *testing.T) {
	if err := CheckSocket("/" + strings.Repeat("a", 102)); err != nil {
		t.Fatalf("103-byte path refused: %v", err)
	}
	if err := CheckSocket("/" + strings.Repeat("a", 103)); err == nil || !strings.Contains(err.Error(), "DESKHAND_HOME") {
		t.Fatalf("104-byte path: %v", err)
	}
}
