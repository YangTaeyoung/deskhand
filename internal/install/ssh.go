// Package install installs and pairs the Windows agent over the user's
// existing SSH access.
package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs a local command with stdin and returns its output.
type Runner func(ctx context.Context, name string, args []string, stdin string) (stdout, stderr string, err error)

// SSH reaches one host by its ssh_config alias.
type SSH struct {
	Alias string
	// Exec runs the local ssh and scp commands; nil runs real processes.
	Exec Runner
}

// sshOptions are placed before every ssh and scp operand.
var sshOptions = []string{"-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes"}

const utf8Prefix = "[Console]::OutputEncoding=[Text.Encoding]::UTF8\n"

func execRunner(ctx context.Context, name string, args []string, stdin string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func (s SSH) runner() Runner {
	if s.Exec != nil {
		return s.Exec
	}
	return execRunner
}

func (s SSH) checkAlias() error {
	if s.Alias == "" || strings.HasPrefix(s.Alias, "-") || strings.ContainsAny(s.Alias, " \t\r\n") {
		return fmt.Errorf("SSH 별칭 %q이(가) 올바르지 않습니다. ~/.ssh/config에 있는 호스트 이름을 주세요", s.Alias)
	}
	return nil
}

// Run sends script to the remote PowerShell on stdin and returns its output.
// The OpenSSH warning lines ("** ...") are removed from stderr.
func (s SSH) Run(ctx context.Context, script string) (stdout, stderr string, err error) {
	if err := s.checkAlias(); err != nil {
		return "", "", err
	}
	args := append(append([]string{}, sshOptions...), s.Alias, "powershell", "-NoProfile", "-NonInteractive", "-Command", "-")
	stdout, stderr, err = s.runner()(ctx, "ssh", args, utf8Prefix+script+"\n")
	return stdout, StripSSHNoise(stderr), err
}

// Copy copies the local file to the remote Windows path with scp.
func (s SSH) Copy(ctx context.Context, local, remote string) error {
	if err := s.checkAlias(); err != nil {
		return err
	}
	args := append(append([]string{}, sshOptions...), "--", local, s.Alias+":"+strings.ReplaceAll(remote, `\`, "/"))
	_, stderr, err := s.runner()(ctx, "scp", args, "")
	if err != nil {
		return fmt.Errorf("에이전트 파일을 PC로 복사하지 못했습니다. SSH 접속과 PC의 임시 폴더 권한을 확인하세요: %s", firstLine(StripSSHNoise(stderr), err))
	}
	return nil
}

// ResolveAddress returns the hostname that ssh_config maps the alias to.
func (s SSH) ResolveAddress(ctx context.Context) (string, error) {
	if err := s.checkAlias(); err != nil {
		return "", err
	}
	args := append(append([]string{}, sshOptions...), "-G", s.Alias)
	stdout, stderr, err := s.runner()(ctx, "ssh", args, "")
	if err != nil {
		return "", fmt.Errorf("SSH 설정을 읽지 못했습니다. ~/.ssh/config의 호스트 항목을 확인하세요: %s", firstLine(StripSSHNoise(stderr), err))
	}
	for _, line := range strings.Split(stdout, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.EqualFold(f[0], "hostname") {
			return f[1], nil
		}
	}
	return "", errors.New("SSH 설정에서 hostname을 찾지 못했습니다. ~/.ssh/config의 호스트 항목에 HostName을 적으세요")
}

// StripSSHNoise removes the OpenSSH warning lines that start with "** ".
func StripSSHNoise(stderr string) string {
	var keep []string
	for _, line := range strings.SplitAfter(stderr, "\n") {
		if strings.HasPrefix(line, "** ") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "")
}

// firstLine returns the first non-empty line of text, or err's text.
func firstLine(text string, err error) string {
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return err.Error()
}
