package hosts

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/YangTaeyoung/deskhand/internal/install"
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

// FindAgentFiles returns the Windows agent builds for arch:
// deskhand-agent-windows-<arch>.exe in the first of AgentDirs that has it
// and deskhand-agentw-windows-<arch>.exe in the same folder.
func FindAgentFiles(arch string) (install.AgentFiles, error) {
	name := "deskhand-agent-windows-" + arch + ".exe"
	wname := "deskhand-agentw-windows-" + arch + ".exe"
	for _, dir := range AgentDirs() {
		p := filepath.Join(dir, name)
		if !isFile(p) {
			continue
		}
		w := filepath.Join(dir, wname)
		if !isFile(w) {
			return install.AgentFiles{}, fmt.Errorf("%s 옆에 %s가 없습니다. 같은 릴리스의 %s를 %s에 두거나 deskhand를 다시 설치하세요", p, wname, wname, dir)
		}
		return install.AgentFiles{Console: p, Window: w}, nil
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
	return install.AgentFiles{}, fmt.Errorf("%s을 찾지 못했습니다. %s와 함께 실행 파일 옆 %s, 앱 번들의 %s, DESKHAND_AGENT_DIR %s 중 한 곳에 두세요", name, wname, exeDir, bundle, env)
}

// isFile reports whether p names a regular file.
func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
