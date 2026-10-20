package server

import (
	"fmt"
	"os"

	"github.com/YangTaeyoung/deskhand/internal/agent/pty"
	"github.com/YangTaeyoung/deskhand/internal/agent/sysfolder"
)

// ptyUTF8Script runs first in an interactive PowerShell: it makes the
// console's input and output code page UTF-8 and the encoding PowerShell
// pipes to programs UTF-8 without a byte order mark.
const ptyUTF8Script = "[Console]::InputEncoding = [Console]::OutputEncoding = $OutputEncoding = [Text.UTF8Encoding]::new($false)"

// ptyShells lists the shells a pty can start, in this order: Windows
// PowerShell, PowerShell 7 when pwshProgram finds it, and cmd.
func ptyShells() []string {
	var shells []string
	if ps, err := windowsPowerShell(windowsFolder); err == nil && isFile(ps.program()) {
		shells = append(shells, "powershell")
	}
	if _, err := pwshProgram(); err == nil {
		shells = append(shells, "pwsh")
	}
	if _, system, err := systemFolders(windowsFolder); err == nil && isFile(system+`\cmd.exe`) {
		shells = append(shells, "cmd")
	}
	return shells
}

// knownPTYShell reports whether name is a shell this agent can start when
// it is installed.
func knownPTYShell(name string) bool { return name == "powershell" || name == "pwsh" || name == "cmd" }

// pwshProgram returns pwsh.exe of PowerShell 7: the one resolveProgram
// finds in the machine's PATH or under App Paths, else the one in
// PowerShell\7 of the Program Files folder.
func pwshProgram() (string, error) {
	if p, err := resolveProgram("pwsh.exe"); err == nil && isFile(p) {
		return p, nil
	}
	folders, err := sysfolder.MachineVars()
	if err != nil {
		return "", err
	}
	if dir := folders["programfiles"]; dir != "" {
		if p := dir + `\PowerShell\7\pwsh.exe`; isFile(p) {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

// ptyShellSpec returns the program of the shell name with env: powershell
// and pwsh without the logo, staying open after ptyUTF8Script; cmd with the
// raw command line that sets code page 65001 and stays open.
func ptyShellSpec(name string, env []string) (pty.Spec, error) {
	switch name {
	case "powershell":
		ps, err := windowsPowerShell(windowsFolder)
		if err != nil {
			return pty.Spec{}, err
		}
		return pty.Spec{Path: ps.program(), Args: []string{ps.program(), "-NoLogo", "-NoExit", "-Command", ptyUTF8Script}, Env: env}, nil
	case "pwsh":
		p, err := pwshProgram()
		if err != nil {
			return pty.Spec{}, fmt.Errorf("pwsh.exe could not be located (%v); install PowerShell 7 or open another shell", err)
		}
		return pty.Spec{Path: p, Args: []string{p, "-NoLogo", "-NoExit", "-Command", ptyUTF8Script}, Env: env}, nil
	case "cmd":
		_, system, err := systemFolders(windowsFolder)
		if err != nil {
			return pty.Spec{}, fmt.Errorf("cmd.exe could not be located (%v); restart deskhand-agent and retry", err)
		}
		return pty.Spec{Path: system + `\cmd.exe`, CmdLine: `cmd.exe /K chcp 65001>nul`, Env: env}, nil
	}
	return pty.Spec{}, invalidArgument(fmt.Sprintf("unknown shell '%s'", name))
}
