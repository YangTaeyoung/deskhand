package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

const fp = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestConfigRoundTrip(t *testing.T) {
	t.Setenv("DESKHAND_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "sub", "agent.json")
	want := Agent{Listen: "0.0.0.0:47601", ControllerFingerprints: []string{fp}, DataDir: t.TempDir(), Name: "office-pc"}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded %+v, saved %+v", got, want)
	}
	b, _ := os.ReadFile(path)
	for _, key := range []string{`"listen"`, `"controller_fingerprints"`, `"data_dir"`, `"name"`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("agent.json lacks %s: %s", key, b)
		}
	}
}

func TestLoadFillsDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DESKHAND_HOME", home)
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := os.WriteFile(path, []byte(`{"controller_fingerprints":["`+fp+`"],"name":"pc"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Listen != ":47600" || got.DataDir != DefaultDataDir() {
		t.Fatalf("defaults %+v", got)
	}
	if runtime.GOOS == "windows" && got.DataDir != `C:\ProgramData\Deskhand` {
		t.Fatalf("Windows data folder %s", got.DataDir)
	}
	if runtime.GOOS != "windows" && got.DataDir != filepath.Join(home, "agent") {
		t.Fatalf("data folder %s outside DESKHAND_HOME %s", got.DataDir, home)
	}
}

func TestLoadRefusesAConfigWithoutAController(t *testing.T) {
	t.Setenv("DESKHAND_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := os.WriteFile(path, []byte(`{"name":"pc"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "init") {
		t.Fatalf("Load of a config without a controller: %v", err)
	}
}

func TestValidFingerprint(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{{fp, true}, {strings.ToUpper(fp), false}, {fp[:63], false}, {fp[:63] + "g", false}, {"", false}} {
		if got := ValidFingerprint(c.in); got != c.want {
			t.Fatalf("ValidFingerprint(%q) = %v", c.in, got)
		}
	}
}
