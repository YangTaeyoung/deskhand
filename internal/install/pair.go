package install

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/YangTaeyoung/deskhand/internal/transport"
)

// DefaultPort is the agent's UDP port.
const DefaultPort = 47600

// Pair steps reported through the progress callback.
const (
	StepProbe   = "probe"
	StepCopy    = "copy"
	StepInit    = "init"
	StepInstall = "install"
	StepVerify  = "verify"
)

// agentExitKeyExists is the exit code of `deskhand-agent init` when a key
// already exists.
const agentExitKeyExists = 4

// validName accepts 1-64 letters, digits, spaces and - _ . without a leading
// or trailing space.
func validName(name string) error {
	n := 0
	for _, c := range name {
		n++
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune(" -_.", c) {
			return errors.New("PC 이름에는 글자, 숫자, 공백, - _ . 만 쓸 수 있습니다. 이름을 고쳐서 다시 실행하세요")
		}
	}
	if n < 1 || n > 64 || name != strings.TrimSpace(name) {
		return errors.New("PC 이름은 앞뒤 공백 없이 1~64자여야 합니다. 이름을 고쳐서 다시 실행하세요")
	}
	return nil
}

const (
	remoteExe    = `C:\Program Files\Deskhand\deskhand-agent.exe`
	tempDirSetup = "$d = Join-Path $env:TEMP 'deskhand-install'\n"
	tempExe      = "$exe = Join-Path $d 'deskhand-agent.exe'\n"
)

// PairResult describes a freshly paired PC.
type PairResult struct {
	Name, Address    string
	Port             int
	AgentFingerprint string
	OS, Arch         string
}

// PairOptions tunes Pair.
type PairOptions struct {
	// Force replaces the key of an agent that is already paired.
	Force bool
}

// ErrAlreadyPaired is returned when the PC already has an agent key and
// PairOptions.Force is not set.
var ErrAlreadyPaired = errors.New("이 PC에는 이미 Deskhand 에이전트가 페어링돼 있습니다. 다시 페어링하려면 deskhand host repair <이름>을 실행하세요")

// psQuote returns s as a PowerShell single-quoted literal.
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

const probeScript = `$ErrorActionPreference = 'Stop'
$os = if ($env:OS -eq 'Windows_NT') { 'windows' } else { 'other' }
$arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
Write-Output ("deskhand-probe os=$os arch=$arch")`

const prepareScript = tempDirSetup + `$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Force -Path $d | Out-Null
$s = $d
try { $s = (New-Object -ComObject Scripting.FileSystemObject).GetFolder($d).ShortPath } catch {}
Write-Output ("deskhand-short=$s")`

// Pair installs the agent on the host behind s and pairs it with controller.
// progress receives the step names probe, copy, init, install and verify.
func Pair(ctx context.Context, s SSH, name string, controller transport.Identity,
	agentBinary func(arch string) (string, error), progress func(step string), opts ...PairOptions) (PairResult, error) {
	var opt PairOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if progress == nil {
		progress = func(string) {}
	}
	if err := validName(name); err != nil {
		return PairResult{}, err
	}
	if !validFingerprint(controller.Fingerprint) {
		return PairResult{}, errors.New("컨트롤러 지문이 올바르지 않습니다. deskhand 데몬의 인증서를 다시 만드세요")
	}

	progress(StepProbe)
	arch, err := probe(ctx, s)
	if err != nil {
		return PairResult{}, err
	}

	progress(StepCopy)
	local, err := agentBinary(arch)
	if err != nil {
		return PairResult{}, fmt.Errorf("%s용 에이전트 파일을 찾지 못했습니다. deskhand를 다시 설치하세요: %w", arch, err)
	}
	if err := copyAgent(ctx, s, local); err != nil {
		return PairResult{}, err
	}

	progress(StepInit)
	agentFP, err := initAgent(ctx, s, name, controller.Fingerprint, opt.Force)
	if err != nil {
		return PairResult{}, err
	}

	progress(StepInstall)
	script := tempDirSetup + tempExe + "$ErrorActionPreference = 'Continue'\n& $exe install\nexit $LASTEXITCODE"
	if _, stderr, err := s.Run(ctx, script); err != nil {
		return PairResult{}, fmt.Errorf("에이전트를 설치하지 못했습니다. SSH 계정이 관리자 권한인지 확인하세요: %s", firstLine(stderr, err))
	}

	progress(StepVerify)
	osName, remoteArch, err := verify(ctx, s)
	if err != nil {
		return PairResult{}, err
	}
	addr, err := s.ResolveAddress(ctx)
	if err != nil {
		return PairResult{}, err
	}
	return PairResult{Name: name, Address: addr, Port: DefaultPort, AgentFingerprint: agentFP, OS: osName, Arch: remoteArch}, nil
}

func validFingerprint(fp string) bool {
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

// probe returns the Go architecture name of the remote Windows PC.
func probe(ctx context.Context, s SSH) (string, error) {
	stdout, stderr, err := s.Run(ctx, probeScript)
	if err != nil {
		low := strings.ToLower(stderr)
		if strings.Contains(low, "not found") || strings.Contains(low, "not recognized") {
			return "", errors.New("Windows PC가 아닙니다. Deskhand v1은 Windows PC만 지원합니다")
		}
		return "", fmt.Errorf("PC에 접속하지 못했습니다. `ssh %s`가 비밀번호 없이 되는지 확인하세요: %s", s.Alias, firstLine(stderr, err))
	}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "deskhand-probe ") {
			continue
		}
		kv := parseKV(strings.TrimPrefix(line, "deskhand-probe "))
		if kv["os"] != "windows" {
			return "", errors.New("Windows PC가 아닙니다. Deskhand v1은 Windows PC만 지원합니다")
		}
		switch strings.ToLower(kv["arch"]) {
		case "x64":
			return "amd64", nil
		case "arm64":
			return "arm64", nil
		}
		return "", fmt.Errorf("지원하지 않는 CPU 구조(%s)입니다. x64 또는 ARM64 Windows PC를 쓰세요", kv["arch"])
	}
	return "", errors.New("PC의 OS 정보를 읽지 못했습니다. PC에서 PowerShell이 동작하는지 확인하세요")
}

func parseKV(s string) map[string]string {
	m := map[string]string{}
	for _, f := range strings.Fields(s) {
		if k, v, ok := strings.Cut(f, "="); ok {
			m[k] = v
		}
	}
	return m
}

// copyAgent creates the remote temp directory and copies local into it.
func copyAgent(ctx context.Context, s SSH, local string) error {
	stdout, stderr, err := s.Run(ctx, prepareScript)
	if err != nil {
		return fmt.Errorf("PC에 임시 폴더를 만들지 못했습니다. PC의 디스크와 권한을 확인하세요: %s", firstLine(stderr, err))
	}
	dir := ""
	for _, line := range strings.Split(stdout, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "deskhand-short="); ok {
			dir = v
		}
	}
	if dir == "" {
		return errors.New("PC의 임시 폴더 경로를 읽지 못했습니다. PC에서 PowerShell이 동작하는지 확인하세요")
	}
	return s.Copy(ctx, local, strings.TrimRight(dir, `\/`)+`\deskhand-agent.exe`)
}

// initAgent runs `deskhand-agent init` and returns the agent fingerprint.
func initAgent(ctx context.Context, s SSH, name, controllerFP string, force bool) (string, error) {
	nameB64 := base64.StdEncoding.EncodeToString([]byte(name))
	var b strings.Builder
	b.WriteString(tempDirSetup + tempExe)
	b.WriteString("$ErrorActionPreference = 'Continue'\n")
	fmt.Fprintf(&b, "$name = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String(%s))\n", psQuote(nameB64))
	fmt.Fprintf(&b, "& $exe init --name $name --controller-fp %s", psQuote(controllerFP))
	if force {
		b.WriteString(" --force")
	}
	b.WriteString("\nexit $LASTEXITCODE")
	stdout, stderr, err := s.Run(ctx, b.String())
	if err != nil {
		var ee interface{ ExitCode() int }
		if errors.As(err, &ee) && ee.ExitCode() == agentExitKeyExists {
			return "", ErrAlreadyPaired
		}
		return "", fmt.Errorf("에이전트 키를 만들지 못했습니다. PC의 C:\\ProgramData 쓰기 권한을 확인하세요: %s", firstLine(stderr, err))
	}
	var out struct {
		Fingerprint string `json:"fingerprint"`
	}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &out) == nil {
			break
		}
	}
	if !validFingerprint(out.Fingerprint) {
		return "", errors.New("에이전트가 지문을 돌려주지 않았습니다. deskhand-agent 버전을 확인하고 다시 시도하세요")
	}
	return out.Fingerprint, nil
}

// verify runs the installed agent's version command, removes the temporary
// copy and returns the remote OS and architecture.
func verify(ctx context.Context, s SSH) (osName, arch string, err error) {
	script := tempDirSetup + "$ErrorActionPreference = 'Continue'\n& " + psQuote(remoteExe) +
		" version\n$code = $LASTEXITCODE\nRemove-Item -Recurse -Force -Path $d -ErrorAction SilentlyContinue\nexit $code"
	stdout, stderr, err := s.Run(ctx, script)
	if err != nil {
		return "", "", fmt.Errorf("설치된 에이전트가 응답하지 않습니다. PC에서 'Deskhand Agent' 작업이 있는지 확인하세요: %s", firstLine(stderr, err))
	}
	var v struct{ Version, OS, Arch string }
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &v) == nil {
			break
		}
	}
	if v.OS != "windows" || v.Arch == "" {
		return "", "", errors.New("설치된 에이전트의 버전 정보가 올바르지 않습니다. 다시 페어링하세요")
	}
	return v.OS, v.Arch, nil
}

// Unpair removes the agent from the host; purge also deletes its data.
func Unpair(ctx context.Context, s SSH, purge bool) error {
	script := "$ErrorActionPreference = 'Continue'\n& " + psQuote(remoteExe) + " uninstall"
	if purge {
		script += " --purge"
	}
	script += "\nexit $LASTEXITCODE"
	if _, stderr, err := s.Run(ctx, script); err != nil {
		return fmt.Errorf("에이전트를 제거하지 못했습니다. SSH 계정이 관리자 권한인지 확인하세요: %s", firstLine(stderr, err))
	}
	return nil
}
