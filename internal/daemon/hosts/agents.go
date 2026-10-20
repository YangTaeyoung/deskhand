package hosts

import (
	"fmt"
	"os"
	"path/filepath"
)

// AgentDirs returns the folders searched for agent binaries, in order: the
// folder of the running executable, ../Resources/agents next to it (the
// app bundle), and DESKHAND_AGENT_DIR when it is set.
func AgentDirs() []string {
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		dirs = append(dirs, dir, filepath.Clean(filepath.Join(dir, "..", "Resources", "agents")))
	}
	if d := os.Getenv("DESKHAND_AGENT_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	return dirs
}

// FindAgentBinary returns the Windows agent for arch:
// deskhand-agent-windows-<arch>.exe in the first of AgentDirs that has it.
func FindAgentBinary(arch string) (string, error) {
	name := "deskhand-agent-windows-" + arch + ".exe"
	for _, dir := range AgentDirs() {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
	}
	exeDir, bundle := "(실행 파일 위치를 알 수 없음)", "(실행 파일 위치를 알 수 없음)"
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
		bundle = filepath.Clean(filepath.Join(exeDir, "..", "Resources", "agents"))
	}
	env := os.Getenv("DESKHAND_AGENT_DIR")
	if env == "" {
		env = "(설정 안 됨)"
	}
	return "", fmt.Errorf("%s을 찾지 못했습니다. 실행 파일 옆 %s, 앱 번들의 %s, DESKHAND_AGENT_DIR %s 중 한 곳에 두세요", name, exeDir, bundle, env)
}
