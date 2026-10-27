//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/config"
	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/agent/sysfolder"
	"github.com/YangTaeyoung/deskhand/internal/agent/uia"
)

// elevatedRerun is the next step of an install failure that an elevated
// prompt resolves.
const elevatedRerun = "run deskhand-agent install again from an elevated prompt"

// prepareDataDir locks dataDir and its uia folder to Administrators and
// SYSTEM.
func prepareDataDir(dataDir string) error {
	if _, err := uia.LockHelperFolders(dataDir, filepath.Join(dataDir, "uia")); err != nil {
		return fmt.Errorf("the data folder %s could not be locked (%v); fix its owner and access list with icacls, then %s", dataDir, err, elevatedRerun)
	}
	return nil
}

// noHelper answers every UI operation with its error.
type noHelper struct{ err error }

func (n noHelper) CallContext(context.Context, string, map[string]any, time.Duration) (map[string]any, error) {
	return nil, n.err
}

// newUI returns the UI Automation helper whose folder is the uia folder of
// dataDir; srv returns the server its events go to.
func newUI(dataDir string, lg *log.Logger, srv func() *server.Server) server.UI {
	dir := filepath.Join(dataDir, "uia")
	if _, err := uia.LockHelperFolders(dataDir, dir); err != nil {
		return noHelper{fmt.Errorf("the UI helper cannot start because %s could not be locked (%v); restart deskhand-agent through its scheduled task", dir, err)}
	}
	helper := uia.New(uia.DefaultLauncher(dir, lg.Printf), func(name string, fields map[string]any) { srv().HandleUIEvent(name, fields) })
	helper.SetOnStart(func() { srv().HandleUIStart() })
	helper.SetLogger(lg)
	helper.SetLogPath(filepath.Join(dir, "helper.log"))
	return helper
}

// system32 returns the System32 folder of the system's Windows folder.
func system32() (string, error) {
	windows, err := sysfolder.WindowsFolder()
	if err != nil {
		return "", fmt.Errorf("the Windows folder could not be located (%v); %s", err, elevatedRerun)
	}
	return filepath.Join(windows, "System32"), nil
}

// createNoWindow is the CREATE_NO_WINDOW process creation flag.
const createNoWindow = 0x08000000

// tool runs program of System32 with args without a console window and
// returns its combined output.
func tool(program string, args ...string) ([]byte, error) {
	dir, err := system32()
	if err != nil {
		return nil, err
	}
	c := exec.Command(filepath.Join(dir, program), args...)
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return c.CombinedOutput()
}

// netsh runs netsh with the raw command line line without a console window.
func netsh(line string) ([]byte, error) {
	dir, err := system32()
	if err != nil {
		return nil, err
	}
	c := exec.Command(filepath.Join(dir, "netsh.exe"))
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, CreationFlags: createNoWindow}
	return c.CombinedOutput()
}

// commandError is what failed, the command's error and output, and next.
func commandError(what string, err error, out []byte, next string) error {
	return fmt.Errorf("%s (%v: %s); %s", what, err, strings.TrimSpace(string(out)), next)
}

// stopAgents ends the scheduled task and every other agent process of both
// builds.
func stopAgents() {
	_, _ = tool("schtasks.exe", taskEndArgs()...)
	for _, image := range agentImages {
		_, _ = tool("taskkill.exe", killOthersArgs(image, os.Getpid())...)
	}
}

// copyAgents copies the running program to installedExe and the
// deskhand-agentw.exe next to it to installedWindowExe, each unless it is
// already there.
func copyAgents() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("the path of this deskhand-agent.exe could not be read (%v); run it from the folder that holds it", err)
	}
	window := filepath.Join(filepath.Dir(exe), windowExeName)
	if _, err := os.Stat(window); err != nil {
		return fmt.Errorf("%s is missing (%v); put the %s of the same Deskhand release next to %s, then run install again", window, err, windowExeName, filepath.Base(exe))
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("could not create %s (%v); %s", installDir, err, elevatedRerun)
	}
	if err := copyFile(exe, installedExe); err != nil {
		return err
	}
	return copyFile(window, installedWindowExe)
}

// copyFile copies src to dst unless they name the same file, retrying while
// a stopping agent still holds dst, and checks the size of the copy.
func copyFile(src, dst string) error {
	if strings.EqualFold(filepath.Clean(src), filepath.Clean(dst)) {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("%s could not be read (%v); copy the Deskhand agent files to a local folder and run install from there", src, err)
	}
	var werr error
	for i := 0; i < 20; i++ {
		if werr = os.WriteFile(dst, data, 0o755); werr == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if werr != nil {
		return fmt.Errorf("could not write %s (%v); delete it, then %s", dst, werr, elevatedRerun)
	}
	st, err := os.Stat(dst)
	if err != nil || st.Size() != int64(len(data)) {
		return fmt.Errorf("the copy at %s could not be checked or has the wrong size; delete it, then %s", dst, elevatedRerun)
	}
	return nil
}

// install locks the data folder, copies both agent builds to installDir
// (copyAgents), registers the scheduled task of installedWindowExe for the
// user who runs it, with --config configPath when configPath is not empty,
// opens the agent's UDP port to the local subnet and starts the task.
// Running it again updates every part in place.
func install(cfg config.Agent, configPath string, stdout io.Writer) error {
	port, err := listenPort(cfg.Listen)
	if err != nil {
		return err
	}
	if err := prepareDataDir(cfg.DataDir); err != nil {
		return err
	}
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("the account running install could not be read (%v); %s", err, elevatedRerun)
	}
	stopAgents()
	if err := copyAgents(); err != nil {
		return err
	}
	xmlPath := filepath.Join(cfg.DataDir, "task.xml")
	if err := os.WriteFile(xmlPath, utf16File(taskXML(u.Username, u.Uid, configPath)), 0o600); err != nil {
		return fmt.Errorf("could not write %s (%v); %s", xmlPath, err, elevatedRerun)
	}
	out, err := tool("schtasks.exe", taskCreateArgs(xmlPath)...)
	_ = os.Remove(xmlPath)
	if err != nil {
		return commandError("the scheduled task "+taskName+" could not be created", err, out, elevatedRerun)
	}
	_, _ = netsh(firewallDeleteLine())
	if out, err := netsh(firewallAddLine(port)); err != nil {
		return commandError(fmt.Sprintf("the firewall rule %s for UDP port %d could not be added", firewallRule, port), err, out, elevatedRerun)
	}
	if out, err := tool("schtasks.exe", taskRunArgs()...); err != nil {
		return commandError("the scheduled task "+taskName+" was created but did not start", err, out,
			"start it with schtasks /Run /TN \""+taskName+"\", or sign out and in again")
	}
	fmt.Fprintf(stdout, "설치했습니다: %s, %s, 예약 작업 %q(%s 로그온 시 %s 시작), 방화벽 규칙 %q(UDP %d, 로컬 서브넷)\n",
		installedExe, installedWindowExe, taskName, u.Username, windowExeName, firewallRule, port)
	return nil
}

// uninstall removes the scheduled task, the firewall rule and installDir,
// and with purge also dataDir. It goes on after a failed step and returns
// the failures.
func uninstall(dataDir string, purge bool, stdout io.Writer) error {
	var errs []error
	stopAgents()
	if err := removeManagedTasks(removeScheduledTask); err != nil {
		errs = append(errs, err)
	}
	if out, err := netsh(firewallDeleteLine()); err != nil && ruleExists() {
		errs = append(errs, commandError("the firewall rule "+firewallRule+" could not be deleted", err, out, elevatedRerun))
	}
	if err := removeInstallDir(); err != nil {
		errs = append(errs, err)
	}
	if purge {
		if err := os.RemoveAll(dataDir); err != nil {
			errs = append(errs, fmt.Errorf("could not remove %s (%v); close the programs that use it, then run deskhand-agent uninstall --purge again", dataDir, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	what := "예약 작업, 방화벽 규칙, " + installDir
	if purge {
		what += ", " + dataDir
	}
	fmt.Fprintf(stdout, "제거했습니다: %s\n", what)
	return nil
}

// removeScheduledTask treats only a missing task as success; query failures
// such as access denied must not hide a task that remains installed. Task names
// come only from removeManagedTasks, never from caller input.
func removeScheduledTask(name string) error {
	script := `$ErrorActionPreference='Stop'
$service=New-Object -ComObject Schedule.Service
$service.Connect()
$folder=$service.GetFolder('\')
try { $task=$folder.GetTask('` + strings.ReplaceAll(name, "'", "''") + `') }
catch {
  if ($_.Exception.GetBaseException().HResult -eq -2147024894) { exit 0 }
  throw
}
if ($task.Name -eq 'Deskhand Console Handoff') {
  $definition=$task.Definition
  $expectedProgram=Join-Path ([Environment]::SystemDirectory) 'WindowsPowerShell\v1.0\powershell.exe'
  $expectedScript=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'Deskhand\setup\console-handoff.ps1'
  if ($definition.RegistrationInfo.Description -ne 'Deskhand console handoff (explicit unattended mode)' -or
      $definition.Actions.Count -ne 1 -or $definition.Actions.Item(1).Path -ine $expectedProgram -or
      -not $definition.Actions.Item(1).Arguments.Contains('"'+$expectedScript+'"')) {
    throw 'An unrelated task uses the handoff task name; it was not changed.'
  }
}
if ($task.State -eq 4) { $task.Stop(0) }
$folder.DeleteTask('` + strings.ReplaceAll(name, "'", "''") + `',0)`
	out, err := tool(`WindowsPowerShell\v1.0\powershell.exe`, "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		return commandError("the scheduled task "+name+" could not be deleted", err, out, elevatedRerun)
	}
	return nil
}

// ruleExists reports whether netsh finds the agent's firewall rule.
func ruleExists() bool {
	_, err := netsh(fmt.Sprintf(`netsh advfirewall firewall show rule name="%s"`, firewallRule))
	return err == nil
}

// removeInstallDir removes installDir. A running program inside it is first
// renamed into the temporary folder, where Windows lets it be renamed but not
// deleted.
func removeInstallDir() error {
	if exe, err := os.Executable(); err == nil && insideDir(installDir, exe) {
		aside := filepath.Join(os.TempDir(), fmt.Sprintf("deskhand-agent-%d.exe.old", os.Getpid()))
		if err := os.Rename(exe, aside); err != nil {
			return fmt.Errorf("could not move the running %s aside (%v); run uninstall from a copy outside %s", exe, err, installDir)
		}
	}
	if err := os.RemoveAll(installDir); err != nil {
		return fmt.Errorf("could not remove %s (%v); close the programs that use it, then run deskhand-agent uninstall again", installDir, err)
	}
	return nil
}

// insideDir reports whether path lies in dir or below it, without regard to
// case.
func insideDir(dir, path string) bool {
	rel, err := filepath.Rel(strings.ToLower(filepath.Clean(dir)), strings.ToLower(filepath.Clean(path)))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
