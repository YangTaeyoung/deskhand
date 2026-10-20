package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/agent/config"
	"github.com/YangTaeyoung/deskhand/internal/transport"
)

const controllerFP = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runMain(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestInitPrintsOnlyTheFingerprintJSONAndWritesTheConfig(t *testing.T) {
	t.Setenv("DESKHAND_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "data", "agent.json")
	code, stdout, stderr := runCLI(t, "init", "--name", "office-pc", "--controller-fp", strings.ToUpper(controllerFP), "--config", path)
	if code != 0 {
		t.Fatalf("init exited %d: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout %q", stdout)
	}
	var printed map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &printed); err != nil || len(printed) != 2 || printed["name"] != "office-pc" {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	cert, key := config.KeyPaths(path)
	id, err := transport.LoadIdentity(cert, key)
	if err != nil || id.Fingerprint != printed["fingerprint"] {
		t.Fatalf("the key on disk has the fingerprint %s, the output %s: %v", id.Fingerprint, printed["fingerprint"], err)
	}
	if strings.Contains(stdout, "PRIVATE KEY") || strings.Contains(stderr, "PRIVATE KEY") {
		t.Fatal("init printed the key")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "office-pc" || cfg.Listen != ":47600" || cfg.DataDir != filepath.Dir(path) ||
		len(cfg.ControllerFingerprints) != 1 || cfg.ControllerFingerprints[0] != controllerFP {
		t.Fatalf("agent.json %+v", cfg)
	}
}

func TestInitRefusesToReplaceAKeyWithoutForce(t *testing.T) {
	t.Setenv("DESKHAND_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "agent.json")
	if code, _, stderr := runCLI(t, "init", "--name", "pc", "--controller-fp", controllerFP, "--config", path); code != 0 {
		t.Fatalf("first init: %s", stderr)
	}
	_, key := config.KeyPaths(path)
	before, _ := os.ReadFile(key)
	code, stdout, stderr := runCLI(t, "init", "--name", "pc", "--controller-fp", controllerFP, "--config", path)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "--force") {
		t.Fatalf("second init exited %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if after, _ := os.ReadFile(key); !bytes.Equal(before, after) {
		t.Fatal("the key changed without --force")
	}
	code, _, stderr = runCLI(t, "init", "--name", "pc", "--controller-fp", controllerFP, "--config", path, "--force")
	if code != 0 {
		t.Fatalf("init --force: %s", stderr)
	}
	if after, _ := os.ReadFile(key); bytes.Equal(before, after) {
		t.Fatal("init --force kept the old key")
	}
}

func TestInitRefusesABadFingerprintOrName(t *testing.T) {
	t.Setenv("DESKHAND_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "agent.json")
	for _, args := range [][]string{
		{"init", "--name", "pc", "--controller-fp", "abc", "--config", path},
		{"init", "--controller-fp", controllerFP, "--config", path},
	} {
		if code, stdout, _ := runCLI(t, args...); code == 0 || stdout != "" {
			t.Fatalf("%v exited %d with %q", args, code, stdout)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("agent.json was written: %v", err)
	}
}

func TestVersionPrintsJSON(t *testing.T) {
	code, stdout, _ := runCLI(t, "version")
	var v map[string]string
	if code != 0 || json.Unmarshal([]byte(stdout), &v) != nil || v["version"] != version || v["os"] == "" || v["arch"] == "" {
		t.Fatalf("version exited %d: %q", code, stdout)
	}
}

func TestAnUnknownCommandShowsTheUsage(t *testing.T) {
	if code, _, stderr := runCLI(t, "frobnicate"); code != 2 || !strings.Contains(stderr, "deskhand-agent init") {
		t.Fatalf("exited %d: %q", code, stderr)
	}
}
