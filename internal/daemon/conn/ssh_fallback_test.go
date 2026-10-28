package conn

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"testing"
)

func TestSSHFallbackOnlyForMacUnreachableWithConfiguredWindowsSSH(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("fallback is macOS-specific")
	}
	target := Target{SSHAlias: "windows", OS: "windows", Arch: "amd64"}
	if !shouldTrySSHFallback(target, fmt.Errorf("dial: %w", syscall.EHOSTUNREACH)) {
		t.Fatal("expected fallback")
	}
	if shouldTrySSHFallback(target, syscall.ECONNREFUSED) {
		t.Fatal("refused must not trigger SSH")
	}
	if shouldTrySSHFallback(target, errors.New("certificate mismatch")) {
		t.Fatal("must not bypass certificate failure")
	}
	target.SSHAlias = ""
	if shouldTrySSHFallback(target, syscall.EHOSTUNREACH) {
		t.Fatal("requires configured SSH")
	}
}

func TestRemoteTempDirectoryRejectsCommandSyntax(t *testing.T) {
	for _, s := range []string{`C:\Users\alice\AppData\Local\Temp`, `C:\Users\A B\Temp`, `C:\Users\홍길동\Temp`} {
		if !safeWindowsDir.MatchString(s) {
			t.Fatalf("valid path rejected: %q", s)
		}
	}
	for _, s := range []string{`C:\Temp';exit`, `C:\Temp&evil`, `C:\%TEMP%`, `C:\Temp"bad`, `relative`, `C:\Temp` + "\nfoo"} {
		if safeWindowsDir.MatchString(s) {
			t.Fatalf("unsafe path accepted: %q", s)
		}
	}
}
