package install

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/transport"
)

const (
	testFP      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testAgentFP = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testKeyPEM  = "-----BEGIN PRIVATE KEY-----\nSECRETKEYBYTES\n-----END PRIVATE KEY-----\n"
)

type call struct {
	name  string
	args  []string
	stdin string
}

type fakeRemote struct {
	calls     []call
	arch      string
	osName    string
	initOut   string
	initErr   string
	initFail  bool
	versionOS string
	initCode  int
}

func newFake() *fakeRemote {
	return &fakeRemote{arch: "X64", osName: "windows", versionOS: "windows", initCode: 4}
}

func (f *fakeRemote) run(_ context.Context, name string, args []string, stdin string) (string, string, error) {
	f.calls = append(f.calls, call{name, args, stdin})
	if name == "scp" {
		return "", "", nil
	}
	if name == "ssh" && containsArg(args, "-G") {
		return "user bob\nhostname 192.168.0.20\nport 22\n", "", nil
	}
	switch {
	case strings.Contains(stdin, "deskhand-probe"):
		return "deskhand-probe os=" + f.osName + " arch=" + f.arch + "\n", "", nil
	case strings.Contains(stdin, "deskhand-short="):
		return "deskhand-short=C:\\Users\\bob\\AppData\\Local\\Temp\\deskhand-install\n", "", nil
	case strings.Contains(stdin, "& $exe init "):
		if f.initFail {
			return "", f.initErr, exitErr(f.initCode)
		}
		out := f.initOut
		if out == "" {
			out = `{"fingerprint":"` + testAgentFP + `","name":"x"}`
		}
		return "banner\n" + out + "\n", "", nil
	case strings.Contains(stdin, "& $exe install"):
		return "", "", nil
	case strings.Contains(stdin, "' version"):
		return `{"version":"dev","os":"` + f.versionOS + `","arch":"amd64"}` + "\n", "", nil
	}
	return "", "", nil
}

func (f *fakeRemote) ssh() SSH { return SSH{Alias: "windows", Exec: f.run} }

func testIdentity() transport.Identity {
	return transport.Identity{CertPEM: []byte("CERT"), KeyPEM: []byte(testKeyPEM), Fingerprint: testFP}
}

func pair(t *testing.T, f *fakeRemote, opts ...PairOptions) (PairResult, []string, []string, error) {
	t.Helper()
	var steps, archs []string
	res, err := Pair(context.Background(), f.ssh(), "사무실 PC", testIdentity(),
		func(arch string) (AgentFiles, error) {
			archs = append(archs, arch)
			return AgentFiles{Console: "/bin/agent-" + arch, Window: "/bin/agentw-" + arch}, nil
		},
		func(s string) { steps = append(steps, s) }, opts...)
	return res, steps, archs, err
}

func TestPairStepsInOrder(t *testing.T) {
	f := newFake()
	res, steps, _, err := pair(t, f)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"probe", "copy", "init", "install", "verify"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	if res.Address != "192.168.0.20" || res.Port != 47600 || res.AgentFingerprint != testAgentFP ||
		res.OS != "windows" || res.Arch != "amd64" || res.Name != "사무실 PC" {
		t.Fatalf("result = %+v", res)
	}
}

func TestPairPassesNoSecretsInArgv(t *testing.T) {
	f := newFake()
	if _, _, _, err := pair(t, f); err != nil {
		t.Fatal(err)
	}
	sawFP := false
	for _, c := range f.calls {
		all := c.name + " " + strings.Join(c.args, " ") + " " + c.stdin
		if strings.Contains(all, "SECRETKEYBYTES") || strings.Contains(all, "PRIVATE KEY") {
			t.Fatalf("secret leaked in %s %v", c.name, c.args)
		}
		if strings.Contains(c.stdin, testFP) {
			sawFP = true
		}
		if c.name == "ssh" && !containsPair(c.args, "BatchMode=yes") || c.name == "ssh" && !containsPair(c.args, "ClearAllForwardings=yes") {
			t.Fatalf("missing ssh options: %v", c.args)
		}
	}
	if !sawFP {
		t.Fatal("controller fingerprint was not sent to init")
	}
}

func containsPair(args []string, v string) bool {
	for i, a := range args {
		if a == "-o" && i+1 < len(args) && args[i+1] == v {
			return true
		}
	}
	return false
}

func TestPairArchSelection(t *testing.T) {
	for raw, want := range map[string]string{"ARM64": "arm64", "Arm64": "arm64", "X64": "amd64"} {
		f := newFake()
		f.arch = raw
		_, _, archs, err := pair(t, f)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(archs, []string{want}) {
			t.Fatalf("%s: archs = %v, want %s", raw, archs, want)
		}
	}
}

func TestPairRejectsUnsupportedArch(t *testing.T) {
	f := newFake()
	f.arch = "X86"
	if _, _, _, err := pair(t, f); err == nil || !strings.Contains(err.Error(), "X86") {
		t.Fatalf("err = %v", err)
	}
}

func TestPairRejectsNonWindows(t *testing.T) {
	f := newFake()
	f.osName = "other"
	_, steps, _, err := pair(t, f)
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("err = %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %v", steps)
	}
}

func TestPairNonWindowsShellMissingPowerShell(t *testing.T) {
	s := SSH{Alias: "linux", Exec: func(context.Context, string, []string, string) (string, string, error) {
		return "", "bash: powershell: command not found\n", errors.New("exit status 127")
	}}
	_, err := Pair(context.Background(), s, "n", testIdentity(),
		func(string) (AgentFiles, error) { return AgentFiles{}, nil }, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("err = %v", err)
	}
}

func TestPairAlreadyPairedWithoutForce(t *testing.T) {
	f := newFake()
	f.initFail = true
	f.initErr = "unrelated text\n"
	_, _, _, err := pair(t, f)
	if !errors.Is(err, ErrAlreadyPaired) || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range f.calls {
		if strings.Contains(c.stdin, "--force") {
			t.Fatal("--force sent without Force option")
		}
	}
}

func TestPairForcePassesForceToInit(t *testing.T) {
	f := newFake()
	if _, _, _, err := pair(t, f, PairOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range f.calls {
		if strings.Contains(c.stdin, "& $exe init ") && strings.Contains(c.stdin, "--force") {
			found = true
		}
	}
	if !found {
		t.Fatal("init did not receive --force")
	}
}

func TestPairNameTravelsAsBase64(t *testing.T) {
	f := newFake()
	if _, _, _, err := pair(t, f); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if strings.Contains(c.stdin, "사무실") {
			t.Fatal("name sent as raw non-ASCII text on stdin")
		}
	}
}

func TestPairRejectsBadInitOutput(t *testing.T) {
	f := newFake()
	f.initOut = `{"fingerprint":"zz"}`
	if _, _, _, err := pair(t, f); err == nil {
		t.Fatal("expected error")
	}
}

func TestPairVerifyRejectsNonWindowsVersion(t *testing.T) {
	f := newFake()
	f.versionOS = "linux"
	if _, _, _, err := pair(t, f); err == nil {
		t.Fatal("expected error")
	}
}

func TestPairBadControllerFingerprint(t *testing.T) {
	id := testIdentity()
	id.Fingerprint = "xyz"
	f := newFake()
	_, err := Pair(context.Background(), f.ssh(), "n", id, func(string) (AgentFiles, error) { return AgentFiles{}, nil }, func(string) {})
	if err == nil || len(f.calls) != 0 {
		t.Fatalf("err = %v, calls = %d", err, len(f.calls))
	}
}

func TestCopyUsesScpWithSameOptions(t *testing.T) {
	f := newFake()
	err := f.ssh().Copy(context.Background(), "/tmp/a b/agent.exe", `C:\Users\bob smith\Temp\deskhand-install\deskhand-agent.exe`)
	if err != nil {
		t.Fatal(err)
	}
	c := f.calls[0]
	want := []string{"-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", "--", "/tmp/a b/agent.exe",
		"windows:C:/Users/bob smith/Temp/deskhand-install/deskhand-agent.exe"}
	if c.name != "scp" || !reflect.DeepEqual(c.args, want) {
		t.Fatalf("%s %q", c.name, c.args)
	}
}

func TestResolveAddress(t *testing.T) {
	f := newFake()
	addr, err := f.ssh().ResolveAddress(context.Background())
	if err != nil || addr != "192.168.0.20" {
		t.Fatalf("%q %v", addr, err)
	}
}

func TestRunStripsPostQuantumWarning(t *testing.T) {
	s := SSH{Alias: "windows", Exec: func(context.Context, string, []string, string) (string, string, error) {
		return "", "** WARNING: connection is not using a post-quantum key exchange algorithm.\n** The server may need to be upgraded.\nreal problem\n", errors.New("exit status 1")
	}}
	_, stderr, err := s.Run(context.Background(), "x")
	if err == nil || stderr != "real problem\n" {
		t.Fatalf("stderr = %q err = %v", stderr, err)
	}
}

func TestRunPrefixesUTF8AndUsesPowerShellArgs(t *testing.T) {
	f := newFake()
	f.ssh().Run(context.Background(), "Write-Output 1")
	c := f.calls[0]
	want := []string{"-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", "windows", "powershell", "-NoProfile", "-NonInteractive", "-Command", "-"}
	if !reflect.DeepEqual(c.args, want) || !strings.HasPrefix(c.stdin, "[Console]::OutputEncoding=[Text.Encoding]::UTF8") {
		t.Fatalf("%q %q", c.args, c.stdin)
	}
}

func TestRejectsAliasThatLooksLikeOption(t *testing.T) {
	f := newFake()
	s := SSH{Alias: "-oProxyCommand=x", Exec: f.run}
	if _, _, err := s.Run(context.Background(), "x"); err == nil || len(f.calls) != 0 {
		t.Fatal("alias accepted")
	}
}

func TestUnpairPurge(t *testing.T) {
	f := newFake()
	if err := Unpair(context.Background(), f.ssh(), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.calls[0].stdin, `& 'C:\Program Files\Deskhand\deskhand-agent.exe' uninstall --purge`+"\nexit $LASTEXITCODE") {
		t.Fatalf("%q", f.calls[0].stdin)
	}
	f = newFake()
	Unpair(context.Background(), f.ssh(), false)
	if strings.Contains(f.calls[0].stdin, "--purge") {
		t.Fatal("purge without request")
	}
}

func containsArg(args []string, v string) bool {
	for _, a := range args {
		if a == v {
			return true
		}
	}
	return false
}

type exitErr int

func (e exitErr) Error() string { return "exit status" }
func (e exitErr) ExitCode() int { return int(e) }

func TestPairOtherInitFailureIsNotAlreadyPaired(t *testing.T) {
	f := newFake()
	f.initFail = true
	f.initErr = "boom\n"
	f.initCode = 1
	if _, _, _, err := pair(t, f); err == nil || errors.Is(err, ErrAlreadyPaired) {
		t.Fatalf("err = %v", err)
	}
}

func TestPairRejectsUnsafeNames(t *testing.T) {
	for _, n := range []string{`a" --controller-fp "x`, `pc\`, "pc\x01", "", " pc", "pc ", strings.Repeat("가", 65), "a;b", "$(x)", "a'b"} {
		f := newFake()
		_, err := Pair(context.Background(), f.ssh(), n, testIdentity(), func(string) (AgentFiles, error) { return AgentFiles{}, nil }, func(string) {})
		if err == nil || len(f.calls) != 0 {
			t.Fatalf("name %q: err = %v, calls = %d", n, err, len(f.calls))
		}
	}
	f := newFake()
	if _, err := Pair(context.Background(), f.ssh(), "사무실 PC-1_a.b", testIdentity(), func(a string) (AgentFiles, error) { return AgentFiles{Console: "/x", Window: "/xw"}, nil }, func(string) {}); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsUnsafeAliases(t *testing.T) {
	for _, a := range []string{"", "-x", "a b", "a/b", "host:22", "a\tb", "a;b"} {
		f := newFake()
		if _, _, err := (SSH{Alias: a, Exec: f.run}).Run(context.Background(), "x"); err == nil || len(f.calls) != 0 {
			t.Fatalf("alias %q accepted", a)
		}
	}
	f := newFake()
	if _, _, err := (SSH{Alias: "me@win-1.local_x", Exec: f.run}).Run(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
}

func TestAgentScriptsCallTheConsoleAgentAndExitWithItsCode(t *testing.T) {
	f := newFake()
	if _, _, _, err := pair(t, f); err != nil {
		t.Fatal(err)
	}
	if err := Unpair(context.Background(), f.ssh(), true); err != nil {
		t.Fatal(err)
	}
	scripts := []string{AgentVersionScript}
	for _, c := range f.calls {
		if c.name == "ssh" && strings.Contains(c.stdin, "deskhand-agent.exe") && !strings.Contains(c.stdin, "deskhand-short=") {
			scripts = append(scripts, c.stdin)
		}
	}
	if len(scripts) != 5 {
		t.Fatalf("%d agent scripts: %q", len(scripts), scripts)
	}
	for _, s := range scripts {
		if !strings.Contains(s, "& ") || !strings.Contains(s, "$LASTEXITCODE") || strings.Contains(s, "Start-Process") ||
			strings.Contains(s, "deskhand-agentw") {
			t.Fatalf("script %q", s)
		}
	}
}

func TestPairCopiesBothAgentBuilds(t *testing.T) {
	f := newFake()
	if _, _, _, err := pair(t, f); err != nil {
		t.Fatal(err)
	}
	var copies [][]string
	for _, c := range f.calls {
		if c.name == "scp" {
			copies = append(copies, c.args[len(c.args)-2:])
		}
	}
	want := [][]string{
		{"/bin/agent-amd64", "windows:C:/Users/bob/AppData/Local/Temp/deskhand-install/deskhand-agent.exe"},
		{"/bin/agentw-amd64", "windows:C:/Users/bob/AppData/Local/Temp/deskhand-install/deskhand-agentw.exe"},
	}
	if !reflect.DeepEqual(copies, want) {
		t.Fatalf("copies %q, want %q", copies, want)
	}
}

func TestInitPassesTheNameAsOneArgument(t *testing.T) {
	f := newFake()
	if _, _, _, err := pair(t, f); err != nil {
		t.Fatal(err)
	}
	want := "& $exe init --name $name --controller-fp '" + testFP + "'\nexit $LASTEXITCODE"
	for _, c := range f.calls {
		if strings.Contains(c.stdin, want) {
			return
		}
	}
	t.Fatalf("no init script with %q", want)
}

func TestInitAgentRejectsABadControllerFingerprint(t *testing.T) {
	for _, fp := range []string{strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("a", 62) + "' ", ""} {
		f := newFake()
		if _, err := initAgent(context.Background(), f.ssh(), "pc", fp, false); err == nil || len(f.calls) != 0 {
			t.Fatalf("fingerprint %q: err = %v, calls = %d", fp, err, len(f.calls))
		}
	}
}
