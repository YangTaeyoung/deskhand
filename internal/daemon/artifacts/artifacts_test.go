package artifacts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// bundle writes a job folder with two files and its manifest.
func bundle(t *testing.T, jobID string) string {
	t.Helper()
	dir := Dir(jobID)
	if err := os.MkdirAll(filepath.Join(dir, "evidence"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"report.json": "{}\n", "evidence/01-exec-stdout.txt": "ok\n"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := Manifest{SchemaVersion: 1, JobID: jobID}
	for _, rel := range []string{"report.json", "evidence/01-exec-stdout.txt"} {
		f, err := Describe(dir, rel, nil)
		if err != nil {
			t.Fatal(err)
		}
		m.Files = append(m.Files, f)
	}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestExportRefusesABundleThatNoLongerMatchesItsManifest(t *testing.T) {
	t.Setenv("DESKHAND_HOME", t.TempDir())
	out := t.TempDir()

	bundle(t, "j-ok")
	got, err := Export(model.Job{JobID: "j-ok"}, out)
	if err != nil || got.Files != 3 || got.Manifest != filepath.Join(out, "j-ok", ManifestName) {
		t.Fatalf("export %+v %v", got, err)
	}

	dir := bundle(t, "j-missing")
	if err := os.Remove(filepath.Join(dir, "evidence", "01-exec-stdout.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(model.Job{JobID: "j-missing"}, out); apierr.CodeOf(err) != wire.CodeHashMismatch {
		t.Fatalf("export with a missing file: %v", err)
	}

	dir = bundle(t, "j-extra")
	if err := os.WriteFile(filepath.Join(dir, "evidence", "02-added.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(model.Job{JobID: "j-extra"}, out); apierr.CodeOf(err) != wire.CodeHashMismatch {
		t.Fatalf("export with an extra file: %v", err)
	}

	dir = bundle(t, "j-changed")
	if err := os.WriteFile(filepath.Join(dir, "report.json"), []byte("{\"x\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(model.Job{JobID: "j-changed"}, out); apierr.CodeOf(err) != wire.CodeHashMismatch {
		t.Fatalf("export with a changed file: %v", err)
	}
	for _, id := range []string{"j-missing", "j-extra", "j-changed"} {
		if _, err := os.Stat(filepath.Join(out, id)); !os.IsNotExist(err) {
			t.Fatalf("a refused export left %s: %v", id, err)
		}
		if _, err := os.Stat(filepath.Join(out, id+".deskhand-part")); !os.IsNotExist(err) {
			t.Fatalf("a refused export left its part folder: %v", err)
		}
	}
}
