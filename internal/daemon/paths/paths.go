// Package paths names the files of the Deskhand daemon under its home
// folder: DESKHAND_HOME, or ~/Library/Application Support/Deskhand.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// MaxSocketPath is the longest unix socket path the daemon accepts; macOS
// keeps 104 bytes for it including the terminating NUL.
const MaxSocketPath = 103

// Home returns DESKHAND_HOME when it is set, or the Deskhand folder in the
// user's Application Support folder.
func Home() string {
	if h := os.Getenv("DESKHAND_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, "Library", "Application Support", "Deskhand")
}

// Run returns the folder of the socket and the lock files.
func Run() string { return filepath.Join(Home(), "run") }

// Socket returns the unix socket of the local API.
func Socket() string { return filepath.Join(Run(), "d.sock") }

// Lock returns the file the running daemon holds locked.
func Lock() string { return filepath.Join(Run(), "daemon.lock") }

// StartLock returns the file a client holds locked while it starts the daemon.
func StartLock() string { return filepath.Join(Run(), "start.lock") }

// DB returns the SQLite database.
func DB() string { return filepath.Join(Home(), "deskhand.db") }

// Artifacts returns the folder of job artifacts.
func Artifacts() string { return filepath.Join(Home(), "artifacts") }

// Hosts returns the folder of the per-host folders.
func Hosts() string { return filepath.Join(Home(), "hosts") }

// HostDir returns the folder of one host.
func HostDir(hostID string) string { return filepath.Join(Hosts(), hostID) }

// Logs returns the log folder.
func Logs() string { return filepath.Join(Home(), "logs") }

// DaemonLog returns the daemon's log file.
func DaemonLog() string { return filepath.Join(Logs(), "daemon.log") }

// Identity returns the folder of the controller certificate and key.
func Identity() string { return filepath.Join(Home(), "identity") }

// ControllerCert returns the controller certificate file.
func ControllerCert() string { return filepath.Join(Identity(), "controller.crt") }

// ControllerKey returns the controller key file.
func ControllerKey() string { return filepath.Join(Identity(), "controller.key") }

// Ensure creates the home folder and its run, logs, identity, hosts and
// artifacts folders with mode 0700.
func Ensure() error {
	for _, dir := range []string{Home(), Run(), Logs(), Identity(), Hosts(), Artifacts()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("Deskhand 폴더 %s를 만들지 못했습니다. 폴더 권한을 확인하세요: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("Deskhand 폴더 %s의 권한을 바꾸지 못했습니다. 폴더 소유자를 확인하세요: %w", dir, err)
		}
	}
	return nil
}

// CheckSocket returns an error when the socket path is longer than
// MaxSocketPath.
func CheckSocket(path string) error {
	if len(path) > MaxSocketPath {
		return fmt.Errorf("소켓 경로 %s가 %d바이트를 넘습니다. DESKHAND_HOME을 더 짧은 경로로 지정하세요", path, MaxSocketPath)
	}
	return nil
}
