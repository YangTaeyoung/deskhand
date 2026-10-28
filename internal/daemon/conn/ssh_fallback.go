package conn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/install"
	"github.com/YangTaeyoung/deskhand/internal/transport"
)

// Only an explicitly paired Windows SSH host can use this route. In particular,
// authentication and certificate errors never fall back to a different route.
func shouldTrySSHFallback(t Target, err error) bool {
	return runtime.GOOS == "darwin" && errors.Is(err, syscall.EHOSTUNREACH) && t.OS == "windows" &&
		(t.Arch == "amd64" || t.Arch == "arm64") && t.SSHAlias != ""
}

var safeWindowsDir = regexp.MustCompile(`^[A-Za-z]:\\[\pL\pN_ .()\\-]+$`)

func relayHelperPath(arch string) (string, error) {
	if arch != "amd64" && arch != "arm64" {
		return "", errors.New("unsupported relay architecture")
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	dirs := []string{dir, filepath.Clean(filepath.Join(dir, "..", "Resources", "agents"))}
	if d := os.Getenv("DESKHAND_AGENT_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	name := "deskhand-ssh-relay-windows-" + arch + ".exe"
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if info, e := os.Stat(p); e == nil && info.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", fmt.Errorf("SSH 복구 중계 파일 %s을 찾을 수 없습니다", name)
}

// stageRelay uses only the configured, host-key-checked SSH account and a
// content-addressed file in its own TEMP directory. It verifies the remote
// contents before executing them, and never replaces an existing file.
func stageRelay(ctx context.Context, t Target, local string) (string, error) {
	b, err := os.ReadFile(local)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	ssh := install.SSH{Alias: t.SSHAlias}
	out, _, err := ssh.Run(ctx, `[Console]::Out.WriteLine($env:TEMP)`)
	if err != nil {
		return "", fmt.Errorf("SSH 임시 폴더 확인: %w", err)
	}
	dir := strings.TrimSpace(out)
	if !safeWindowsDir.MatchString(dir) {
		return "", errors.New("SSH 임시 폴더 경로가 안전한 형식이 아닙니다")
	}
	remote := dir + `\deskhand-relay-` + hash + `.exe`
	// Recover staging files left by interrupted uploads after they have aged
	// well beyond a normal setup; never remove another version or live setup.
	cleanup := fmt.Sprintf(`Get-ChildItem -LiteralPath $env:TEMP -File -Filter 'deskhand-relay-%s.exe.part-*' -ErrorAction SilentlyContinue | Where-Object { $_.LastWriteTimeUtc -lt [DateTime]::UtcNow.AddHours(-1) } | Remove-Item -ErrorAction SilentlyContinue`, hash)
	_, _, _ = ssh.Run(ctx, cleanup)
	check := fmt.Sprintf(`$p='%s'; if (Test-Path -LiteralPath $p) { (Get-FileHash -Algorithm SHA256 -LiteralPath $p).Hash.ToLowerInvariant() }`, remote)
	got, _, err := ssh.Run(ctx, check)
	if err != nil {
		return "", err
	}
	got = strings.TrimSpace(got)
	if got != "" {
		if got != hash {
			return "", errors.New("기존 SSH 중계 파일의 해시가 다릅니다")
		}
		return remote, nil
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	part := remote + `.part-` + hex.EncodeToString(nonce[:])
	// A canceled SCP can leave a partial file; clean our unique staging path
	// with a separate, bounded context even when the dial context expired.
	needsCleanup := true
	defer func() {
		if !needsCleanup {
			return
		}
		cleanCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _, _ = ssh.Run(cleanCtx, fmt.Sprintf(`Remove-Item -LiteralPath '%s' -ErrorAction SilentlyContinue`, part))
	}()
	if err := ssh.Copy(ctx, local, part); err != nil {
		return "", err
	}
	// On concurrent setup, retain whichever verified file was installed first.
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'; $s='%s'; $d='%s'; $h='%s'; try { if ((Get-FileHash -Algorithm SHA256 -LiteralPath $s).Hash.ToLowerInvariant() -ne $h) { throw 'relay hash mismatch' }; if (!(Test-Path -LiteralPath $d)) { try { [IO.File]::Move($s,$d) } catch { if (!(Test-Path -LiteralPath $d)) { throw } } }; if ((Get-FileHash -Algorithm SHA256 -LiteralPath $d).Hash.ToLowerInvariant() -ne $h) { throw 'existing relay hash mismatch' } } finally { if (Test-Path -LiteralPath $s) { Remove-Item -LiteralPath $s -ErrorAction SilentlyContinue } }`, part, remote, hash)
	if _, stderr, err := ssh.Run(ctx, script); err != nil {
		return "", fmt.Errorf("SSH 중계 파일 확인: %s: %w", strings.TrimSpace(stderr), err)
	}
	needsCleanup = false
	return remote, nil
}

func dialOverSSH(ctx context.Context, t Target, id transport.Identity) (Session, error) {
	helper, err := relayHelperPath(t.Arch)
	if err != nil {
		return nil, err
	}
	remote, err := stageRelay(ctx, t, helper)
	if err != nil {
		return nil, err
	}
	peer := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: t.Port}
	command := `"` + remote + `" pc -agent 127.0.0.1:` + strconv.Itoa(t.Port)
	cmd := exec.Command("ssh", "-T", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", "-o", "ConnectTimeout=5", "--", t.SSHAlias, command)
	cmd.Stderr = io.Discard
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		_ = out.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		_ = out.Close()
		_ = in.Close()
		return nil, err
	}
	packet := newSSHPacketConn(out, in, peer, func() { _ = cmd.Process.Kill() })
	go func() { _ = cmd.Wait(); _ = packet.Close() }()
	conn, err := transport.DialPacket(ctx, packet, peer.String(), id, t.Fingerprint)
	if err != nil {
		_ = packet.Close()
		return nil, err
	}
	go func() { <-conn.Context().Done(); _ = packet.Close() }()
	return quicSession{conn}, nil
}
