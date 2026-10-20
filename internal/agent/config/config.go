// Package config reads and writes agent.json, the settings of deskhand-agent.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DefaultListen is the UDP address the agent listens on when agent.json names none.
const DefaultListen = ":47600"

// Agent is the content of agent.json.
type Agent struct {
	Listen                 string   `json:"listen"`
	ControllerFingerprints []string `json:"controller_fingerprints"`
	DataDir                string   `json:"data_dir"`
	Name                   string   `json:"name"`
}

// DefaultDataDir returns C:\ProgramData\Deskhand on Windows and, elsewhere,
// the agent folder of DESKHAND_HOME or of the user's Deskhand folder.
func DefaultDataDir() string {
	if runtime.GOOS == "windows" {
		return `C:\ProgramData\Deskhand`
	}
	if home := os.Getenv("DESKHAND_HOME"); home != "" {
		return filepath.Join(home, "agent")
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "Deskhand", "agent")
	}
	return filepath.Join(os.TempDir(), "Deskhand", "agent")
}

// DefaultPath returns agent.json in DefaultDataDir.
func DefaultPath() string {
	return filepath.Join(DefaultDataDir(), "agent.json")
}

// KeyPaths returns the certificate and key files that sit next to the
// agent.json at path.
func KeyPaths(path string) (cert, key string) {
	dir := filepath.Dir(path)
	return filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key")
}

// ValidFingerprint reports whether fp is 64 lowercase hexadecimal digits,
// the form transport.Fingerprint returns.
func ValidFingerprint(fp string) bool {
	if len(fp) != 64 {
		return false
	}
	for _, c := range fp {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Load reads agent.json at path and fills Listen and DataDir with their
// defaults when they are empty. A config without a valid controller
// fingerprint is an error.
func Load(path string) (Agent, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Agent{}, fmt.Errorf("%s does not exist; run deskhand-agent init first", path)
	}
	if err != nil {
		return Agent{}, fmt.Errorf("read %s: %w", path, err)
	}
	var a Agent
	if err := json.Unmarshal(b, &a); err != nil {
		return Agent{}, fmt.Errorf("%s is not valid JSON (%v); run deskhand-agent init --force again", path, err)
	}
	if a.Listen == "" {
		a.Listen = DefaultListen
	}
	if a.DataDir == "" {
		a.DataDir = DefaultDataDir()
	}
	if len(a.ControllerFingerprints) == 0 {
		return Agent{}, fmt.Errorf("%s lists no controller fingerprint; run deskhand-agent init --controller-fp with the Mac's fingerprint", path)
	}
	for _, fp := range a.ControllerFingerprints {
		if !ValidFingerprint(fp) {
			return Agent{}, fmt.Errorf("%s holds the controller fingerprint %q, which is not 64 lowercase hex digits; run deskhand-agent init --force again", path, fp)
		}
	}
	return a, nil
}

// Save writes a as agent.json at path, creating its folder, through a
// temporary file that replaces the old one.
func Save(path string, a Agent) error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
