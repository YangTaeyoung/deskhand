package input

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/clipboard"
	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

var (
	procEnumWindows         = user32.NewProc("EnumWindows")
	procGetClassNameW       = user32.NewProc("GetClassNameW")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procGetClientRect       = user32.NewProc("GetClientRect")
	procClientToScreen      = user32.NewProc("ClientToScreen")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetCursorPos        = user32.NewProc("SetCursorPos")
	procFindWindowExW       = user32.NewProc("FindWindowExW")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procWindowFromPoint     = user32.NewProc("WindowFromPoint")
	procGetAncestor         = user32.NewProc("GetAncestor")
	procGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	procGetCursorInfo       = user32.NewProc("GetCursorInfo")
	procSwitchToThisWindow  = user32.NewProc("SwitchToThisWindow")
	procAttachThreadInput   = user32.NewProc("AttachThreadInput")
	procBringWindowToTop    = user32.NewProc("BringWindowToTop")
	procGetCurrentThreadId  = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentThreadId")
)

const (
	fixtureClass     = "DeskhandFixtureWindow"
	wmClose          = 0x0010
	gaRoot           = 2
	vkShift          = 0x10
	createNewConsole = 0x00000010
)

type point struct{ x, y int32 }

type winRect struct{ left, top, right, bottom int32 }

func TestWinInputMatchesTheSizeOfINPUT(t *testing.T) {
	want := uintptr(40)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = 28
	}
	if got := unsafe.Sizeof(winInput{}); got != want {
		t.Fatalf("winInput is %d bytes, INPUT is %d", got, want)
	}
}

func needsUserSession(t *testing.T) {
	t.Helper()
	if os.Getenv("DESKHAND_WINTEST") == "" {
		t.Skip("needs the signed-in user's session; run through scripts/wintest.sh")
	}
	display.EnsureDPIAware()
}

// fixture is a deskhand-fixture process the test started and its window.
type fixture struct {
	t    *testing.T
	cmd  *exec.Cmd
	hwnd uintptr
	done chan struct{}
}

// startFixture starts deskhand-fixture.exe from the working folder, waits
// for its window and brings it to the foreground; the test fails without
// sending input when it cannot. The fixture is closed when the test ends.
func startFixture(t *testing.T) *fixture {
	t.Helper()
	needsUserSession(t)
	exe, err := filepath.Abs("deskhand-fixture.exe")
	if err == nil {
		_, err = os.Stat(exe)
	}
	if err != nil {
		t.Fatalf("deskhand-fixture.exe is not next to the test (%v); run the test through scripts/wintest.sh", err)
	}
	cmd := exec.Command(exe, "--log", filepath.Join(t.TempDir(), "fixture.log"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(f.done)
	}()
	t.Cleanup(f.close)
	deadline := time.Now().Add(15 * time.Second)
	for f.hwnd == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the fixture window did not appear within 15 s")
		}
		time.Sleep(50 * time.Millisecond)
		f.hwnd = findWindow(uint32(cmd.Process.Pid), fixtureClass)
	}
	f.foreground()
	return f
}

// close asks the fixture to close and kills it after 5 s.
func (f *fixture) close() {
	if f.hwnd != 0 {
		procPostMessageW.Call(f.hwnd, wmClose, 0, 0)
	}
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		_ = f.cmd.Process.Kill()
		<-f.done
	}
}

// cpuTime returns the processor time the fixture process has used.
func (f *fixture) cpuTime() time.Duration {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(f.cmd.Process.Pid))
	if err != nil {
		f.t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		f.t.Fatal(err)
	}
	ticks := func(t windows.Filetime) int64 { return int64(t.HighDateTime)<<32 | int64(t.LowDateTime) }
	return time.Duration((ticks(k) + ticks(u)) * 100)
}

// findWindow returns the visible top-level window of class in process pid.
func findWindow(pid uint32, class string) uintptr {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var owner uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner != pid || windowClass(hwnd) != class {
			return 1
		}
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		found = hwnd
		return 0
	})
	procEnumWindows.Call(cb, 0)
	return found
}

func windowClass(hwnd uintptr) string {
	var buf [256]uint16
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

func windowTitle(hwnd uintptr) string {
	var buf [256]uint16
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

func foregroundWindow() uintptr {
	h, _, _ := procGetForegroundWindow.Call()
	return h
}

// tryForeground brings hwnd to the foreground with SetForegroundWindow,
// then SwitchToThisWindow, then SetForegroundWindow with the test's thread
// input attached to the foreground window's thread, and reports whether it
// is in front. None of them sends input.
func tryForeground(hwnd uintptr) bool {
	ways := []func(){
		func() { procSetForegroundWindow.Call(hwnd) },
		func() { procSwitchToThisWindow.Call(hwnd, 1) },
		func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			fgThread, _, _ := procGetWindowThreadProcessId.Call(foregroundWindow(), 0)
			own, _, _ := procGetCurrentThreadId.Call()
			if fgThread == 0 || fgThread == own {
				return
			}
			if r, _, _ := procAttachThreadInput.Call(own, fgThread, 1); r == 0 {
				return
			}
			procBringWindowToTop.Call(hwnd)
			procSetForegroundWindow.Call(hwnd)
			procAttachThreadInput.Call(own, fgThread, 0)
		},
	}
	for _, try := range ways {
		if foregroundWindow() == hwnd {
			return true
		}
		try()
		for range 10 {
			if foregroundWindow() == hwnd {
				return true
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return foregroundWindow() == hwnd
}

// bringToForeground brings hwnd to the foreground and fails the test when
// it cannot.
func bringToForeground(t *testing.T, hwnd uintptr) {
	t.Helper()
	if !tryForeground(hwnd) {
		fg := foregroundWindow()
		t.Fatalf("window %#x is not in the foreground (the foreground window is %#x %q); no input was sent", hwnd, fg, windowTitle(fg))
	}
}

func (f *fixture) foreground() { bringToForeground(f.t, f.hwnd) }

// clientRect returns the client area of the fixture window in screen
// pixels.
func (f *fixture) clientRect() winRect {
	var r winRect
	procGetClientRect.Call(f.hwnd, uintptr(unsafe.Pointer(&r)))
	p := point{}
	procClientToScreen.Call(f.hwnd, uintptr(unsafe.Pointer(&p)))
	return winRect{left: p.x, top: p.y, right: p.x + r.right, bottom: p.y + r.bottom}
}

func (r winRect) contains(x, y int) bool {
	return x >= int(r.left) && y >= int(r.top) && x < int(r.right) && y < int(r.bottom)
}

func cursorPos() point {
	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p
}

// rootAt returns the top-level window at screen point (x, y).
func rootAt(x, y int32) uintptr {
	h, _, _ := procWindowFromPoint.Call(uintptr(uint32(x)) | uintptr(uint32(y))<<32)
	root, _, _ := procGetAncestor.Call(h, gaRoot)
	return root
}

var errNotTarget = errors.New("the input was not sent because the target window was not in front")

// guarded sends input only while the target window is in the foreground,
// re-checks the foreground after every batch, and sends a mouse move or
// button only inside client (when set) and over the target or its child.
// Every batch, release-only ones included, is checked.
type guarded struct {
	t      *testing.T
	sys    windowsSystem
	target func() uintptr
	client func() winRect
}

func (g guarded) send(in []raw) (int, error) {
	target := g.target()
	if fg := foregroundWindow(); fg != target {
		g.t.Errorf("the foreground window is %#x %q, not the target %#x; %d inputs were not sent", fg, windowTitle(fg), target, len(in))
		return 0, errNotTarget
	}
	for _, r := range in {
		if !r.mouse {
			continue
		}
		x, y := int(cursorPos().x), int(cursorPos().y)
		if r.flags&mouseeventfAbsolute != 0 {
			vs := g.sys.virtualScreen()
			x, y = vs.x+int(int64(r.dx)*int64(vs.w)>>16), vs.y+int(int64(r.dy)*int64(vs.h)>>16)
		}
		if g.client != nil && !g.client().contains(x, y) {
			g.t.Errorf("mouse input at (%d,%d) is outside the target's client area %+v; %d inputs were not sent", x, y, g.client(), len(in))
			return 0, errNotTarget
		}
		if r.flags&(mouseeventfLeftDown|mouseeventfRightDown|mouseeventfMiddleDown|mouseeventfXDown) != 0 {
			if root := rootAt(int32(x), int32(y)); root != target {
				g.t.Errorf("a button press at (%d,%d) is over %#x %q, not the target; not sent", x, y, root, windowTitle(root))
				return 0, errNotTarget
			}
		}
	}
	n, err := g.sys.send(in)
	if fg := foregroundWindow(); fg != target {
		g.t.Errorf("the foreground window became %#x %q during a send; failing hard", fg, windowTitle(fg))
		return n, errNotTarget
	}
	return n, err
}

func (g guarded) virtualScreen() rect                              { return g.sys.virtualScreen() }
func (g guarded) keyScan(vk uint16) (uint16, bool)                 { return g.sys.keyScan(vk) }
func (g guarded) foregroundAbove() bool                            { return g.sys.foregroundAbove() }
func (g guarded) clipboard() clipboardAccess                       { return g.sys.clipboard() }
func (g guarded) drain(ctx context.Context, d time.Duration) error { return g.sys.drain(ctx, d) }
func (f *fixture) logger() *log.Logger                             { return log.New(testWriter{f.t}, "", 0) }
func (f *fixture) injector() *Injector {
	return newInjector(guarded{t: f.t, target: func() uintptr { return f.hwnd }, client: f.clientRect}, Options{Logger: f.logger()})
}
func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

type testWriter struct{ t *testing.T }

// fixtureEdits maps the fixture's edit controls to their control ids,
// which UI Automation reports as their AutomationId.
var fixtureEdits = map[string]int{"Message": 101, "Transcript": 103}

// uiaValue reads the text of the fixture's edit control name through UI
// Automation: the value of its ValuePattern, or, when the Win32 edit
// proxy is not applied, its Name, which UI Automation takes from the
// window text.
func (f *fixture) uiaValue(name string) string {
	f.t.Helper()
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
$w=[System.Windows.Automation.AutomationElement]::FromHandle([IntPtr]%d)
$c=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::AutomationIdProperty,'%d')
$e=$w.FindFirst([System.Windows.Automation.TreeScope]::Children,$c)
if($e -eq $null){throw 'the fixture has no control with the AutomationId %d'}
$p=$null
if($e.TryGetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern,[ref]$p)){$v=$p.Current.Value}else{$v=$e.Current.Name}
[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($v))`, f.hwnd, fixtureEdits[name], fixtureEdits[name])
	ps := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			f.t.Fatalf("UI Automation read of %s: %v: %s", name, err, ee.Stderr)
		}
		f.t.Fatalf("UI Automation read of %s: %v", name, err)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		f.t.Fatalf("UI Automation read of %s: %q: %v", name, out, err)
	}
	return string(b)
}

// waitValue reads the edit control name through UI Automation until ok
// accepts its value, for at most timeout, and returns the last value.
func (f *fixture) waitValue(name string, timeout time.Duration, ok func(string) bool) string {
	f.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		v := f.uiaValue(name)
		if ok(v) || time.Now().After(deadline) {
			return v
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// settledValue reads the edit control name until two reads 500 ms apart
// agree, for at most timeout.
func (f *fixture) settledValue(name string, timeout time.Duration) string {
	f.t.Helper()
	deadline := time.Now().Add(timeout)
	last := f.uiaValue(name)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		v := f.uiaValue(name)
		if v == last {
			return v
		}
		last = v
	}
	return last
}

func keyIsDown(vk uintptr) bool {
	r, _, _ := procGetAsyncKeyState.Call(vk)
	return r&0x8000 != 0
}

func TestInputInSessionZeroIsUnavailable(t *testing.T) {
	if os.Getenv("SSH_CONNECTION") == "" {
		t.Skip("needs a process started by sshd, which runs in Session 0; run scripts/wintest.sh --ssh")
	}
	info, err := session.Current()
	if err != nil || info.SessionID != 0 {
		t.Fatalf("this process is in session %d (%v), not Session 0; run scripts/wintest.sh --ssh", info.SessionID, err)
	}
	j := New(Options{Logger: log.New(testWriter{t}, "", 0)})
	for _, ev := range []wire.InputEvent{keyDown(1, 0, 0x2A), {Seq: 2, K: "move", X: 1, Y: 1}, {Seq: 3, K: "text", Text: "a"}} {
		ack := j.Apply(ev)
		mustFail(t, ack, wire.CodeDisplayUnavailable)
		if !strings.Contains(ack.Error, "session0") {
			t.Fatalf("ack %+v", ack)
		}
	}
	if p := j.Pressed(); len(p) != 0 {
		t.Fatalf("pressed %+v", p)
	}
}

func TestFixtureReceivesKeysAndText(t *testing.T) {
	f := startFixture(t)
	j := f.injector()
	mustOK(t, j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: "Hello 한글 😀!", Method: "unicode"}))
	mustOK(t, j.Apply(wire.InputEvent{Seq: 2, K: "key", SC: 0x0E, Down: true}))
	mustOK(t, j.Apply(wire.InputEvent{Seq: 3, K: "key", SC: 0x0E}))
	want := "Hello 한글 😀"
	if got := f.waitValue("Message", 10*time.Second, func(v string) bool { return v == want }); got != want {
		t.Fatalf("the Message box holds %q, want %q", got, want)
	}
	mustOK(t, j.Apply(wire.InputEvent{Seq: 4, K: "text", Text: "\n", Method: "unicode"}))
	transcript := f.waitValue("Transcript", 10*time.Second, func(v string) bool { return strings.Contains(v, "You: "+want) })
	if !strings.Contains(transcript, "You: "+want) {
		t.Fatalf("Enter did not send the message; the transcript is %q", transcript)
	}
	if p := j.Pressed(); len(p) != 0 {
		t.Fatalf("pressed %+v", p)
	}
	t.Logf("Message %q; transcript %q", want, firstLine(transcript))
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	return line
}

func TestFixtureReceivesALongTextWhole(t *testing.T) {
	f := startFixture(t)
	j := f.injector()
	var b strings.Builder
	for i := 0; utf8.RuneCountInString(b.String()) < 20*chunkChars; i++ {
		fmt.Fprintf(&b, "%d가", i)
		if i%50 == 0 {
			b.WriteString("😀")
		}
	}
	text := b.String()
	cpu0, self0 := f.cpuTime(), selfCPUTime(t)
	start := time.Now()
	mustOK(t, j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: text}))
	sent := time.Since(start)
	cpu, self := f.cpuTime()-cpu0, selfCPUTime(t)-self0
	got := f.settledValue("Message", 30*time.Second)
	if got != text {
		t.Fatalf("the Message box holds %d characters, want the %d sent (first difference at %d)", utf8.RuneCountInString(got),
			utf8.RuneCountInString(text), firstDifference(got, text))
	}
	t.Logf("%d characters sent in %s and received whole; the fixture used %s of CPU and the agent %s of CPU (drain polling) meanwhile",
		utf8.RuneCountInString(text), sent, cpu, self)
}

// selfCPUTime returns the processor time this process has used.
func selfCPUTime(t *testing.T) time.Duration {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u); err != nil {
		t.Fatal(err)
	}
	ticks := func(ft windows.Filetime) int64 { return int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime) }
	return time.Duration((ticks(k) + ticks(u)) * 100)
}

func firstDifference(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	for i := range min(len(ra), len(rb)) {
		if ra[i] != rb[i] {
			return i
		}
	}
	return min(len(ra), len(rb))
}

type winInfo struct {
	hwnd         uintptr
	class, title string
}

// topLevelWindows lists the visible top-level windows with a title.
func topLevelWindows() []winInfo {
	var out []winInfo
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(hwnd); v != 0 {
			if title := windowTitle(hwnd); title != "" {
				out = append(out, winInfo{hwnd, windowClass(hwnd), title})
			}
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return out
}

// findWindowByTitle returns the first visible top-level window whose title
// contains substr.
func findWindowByTitle(substr string) uintptr {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if strings.Contains(windowTitle(hwnd), substr) {
			found = hwnd
			return 0
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return found
}

func TestDrainKeepsTypingIntoANonGUIConsoleWindow(t *testing.T) {
	needsUserSession(t)
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	// conhost.exe forces the classic console host, so the window is a
	// ConsoleWindowClass window this test can find and focus, even when
	// the default terminal is Windows Terminal.
	conhost := filepath.Join(os.Getenv("SystemRoot"), "System32", "conhost.exe")
	marker := fmt.Sprintf("deskhand-con-%d", time.Now().UnixNano())
	cmd := exec.Command(conhost, shell, "/K", "title "+marker)
	cmd.Dir = "."
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	var hwnd uintptr
	deadline := time.Now().Add(12 * time.Second)
	for hwnd == 0 {
		if time.Now().After(deadline) {
			t.Logf("open top-level windows while looking for %q:", marker)
			for _, w := range topLevelWindows() {
				t.Logf("  %#x class %q title %q", w.hwnd, w.class, w.title)
			}
			t.Skip("no classic console window appeared (the default terminal is likely Windows Terminal, which hosts consoles in its own window); the drain classification is covered by TestDrainStep*")
		}
		time.Sleep(100 * time.Millisecond)
		hwnd = findWindowByTitle(marker)
	}
	if !tryForeground(hwnd) {
		t.Skip("the launched console could not be brought to the foreground in this session")
	}
	// A console parked outside GetMessage must not be reported as not
	// responding; drain returns without error.
	start := time.Now()
	if err := (windowsSystem{}).drain(context.Background(), 3*time.Second); err != nil {
		t.Fatalf("drain reported the idle console as not responding: %v", err)
	}
	t.Logf("drain on the idle console window returned in %s with no error", time.Since(start))

	out := "console_echo.txt"
	_ = os.Remove(out)
	t.Cleanup(func() { _ = os.Remove(out) })
	body := strings.Repeat("a", 180)
	line := fmt.Sprintf("echo %s> %s\r\n", body, out)
	j := newInjector(guarded{t: t, target: func() uintptr { return hwnd }}, Options{Logger: log.New(testWriter{t}, "", 0)})
	if !tryForeground(hwnd) {
		t.Skip("the console lost the foreground before the command was typed")
	}
	ack := j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: line})
	if !ack.OK {
		t.Logf("the console lost the foreground while typing (%s); the drain check above is the assertion", ack.Error)
		return
	}
	for range 100 {
		if b, err := os.ReadFile(out); err == nil && strings.Contains(string(b), body) {
			chunks := (utf8.RuneCountInString(line) + chunkChars - 1) / chunkChars
			t.Logf("typed a %d-char command into the console across %d chunks; it ran and wrote the file", utf8.RuneCountInString(line), chunks)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Log("the console did not write the file in time; the drain check above is the assertion")
}

func TestPreemptionCutsALongTextAndReleasesHeldKeys(t *testing.T) {
	f := startFixture(t)
	j := f.injector()
	var preempted []Preemption
	j.opt.Preempted = func(p Preemption) { preempted = append(preempted, p) }
	j.SetWriter(1)
	mustOK(t, j.Apply(wire.InputEvent{Seq: 1, Gen: 1, K: "key", SC: 0x2A, Down: true}))
	t.Cleanup(func() { _, _ = j.ReleaseAll() })
	if !keyIsDown(vkShift) {
		t.Fatal("Shift is not down after the key event")
	}
	text := strings.Repeat("가", 20000)
	raised := make(chan struct{})
	go func() {
		defer close(raised)
		time.Sleep(50 * time.Millisecond)
		j.SetWriter(2)
	}()
	start := time.Now()
	ack := j.Apply(wire.InputEvent{Seq: 2, Gen: 1, K: "text", Text: text})
	cut := time.Since(start)
	<-raised
	mustFail(t, ack, wire.CodeControlPreempted)
	m := regexp.MustCompile(`typed (\d+) of 20000`).FindStringSubmatch(ack.Error)
	if m == nil {
		t.Fatalf("ack %+v", ack)
	}
	typed, _ := strconv.Atoi(m[1])
	if typed == 0 || typed >= 20000 {
		t.Fatalf("typed %d of 20000", typed)
	}
	if keyIsDown(vkShift) {
		t.Fatal("Shift is still down after the preemption")
	}
	if p := j.Pressed(); len(p) != 0 {
		t.Fatalf("pressed %+v", p)
	}
	if len(preempted) != 2 || !slices.Equal(preempted[1].Released, []Pressed{{K: "key", SC: 0x2A}}) {
		t.Fatalf("preemptions %+v", preempted)
	}
	mustFail(t, j.Apply(wire.InputEvent{Seq: 3, Gen: 1, K: "text", Text: "x"}), wire.CodeControlPreempted)
	got := utf8.RuneCountInString(f.settledValue("Message", 60*time.Second))
	if got != typed {
		t.Fatalf("the Message box holds %d characters, the injector typed %d", got, typed)
	}
	t.Logf("preempted after %s: typed %d of 20000 characters, all of them arrived; Shift released (%+v)", cut, typed, preempted[1])
}

func TestPasteRestoresClipboard(t *testing.T) {
	f := startFixture(t)
	user, err := clipboard.Save()
	if err != nil {
		t.Fatalf("the user's clipboard could not be saved, so the test does not touch it: %v", err)
	}
	t.Cleanup(func() {
		if err := user.Restore(); err != nil {
			t.Errorf("restoring the user's clipboard: %v", err)
		}
	})
	sentinel := fmt.Sprintf("deskhand 센티넬 %d", time.Now().UnixNano())
	if err := clipboard.SetPrivateText(sentinel); err != nil {
		t.Fatal(err)
	}
	before, err := clipboard.Save()
	if err != nil {
		t.Fatal(err)
	}
	j := f.injector()
	written := 0
	j.opt.ClipboardWritten = func() { written++ }
	want := "Paste 붙여넣기 😀"
	mustOK(t, j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: want, Method: "paste"}))
	if got := f.waitValue("Message", 10*time.Second, func(v string) bool { return v == want }); got != want {
		t.Fatalf("the Message box holds %q, want %q", got, want)
	}
	text, err := clipboard.New().Text()
	if err != nil || text != sentinel {
		t.Fatalf("the clipboard holds %q (%v) after the paste, want %q", text, err, sentinel)
	}
	after, err := clipboard.Save()
	if err != nil {
		t.Fatal(err)
	}
	b, a := before.Formats(), after.Formats()
	slices.Sort(b)
	slices.Sort(a)
	if !slices.Equal(a, b) {
		t.Fatalf("formats %v after the paste, %v before", a, b)
	}
	if written != 2 {
		t.Fatalf("ClipboardWritten ran %d times", written)
	}
	t.Logf("pasted %q; the clipboard holds %q again in the formats %v (skipped %v)", want, text, a, after.Skipped)
}

func TestAbsoluteMovesAndAClickLandInTheFixture(t *testing.T) {
	f := startFixture(t)
	orig := cursorPos()
	t.Cleanup(func() { procSetCursorPos.Call(uintptr(orig.x), uintptr(orig.y)) })
	j := f.injector()
	client := f.clientRect()
	displays, err := display.List()
	if err != nil {
		t.Fatal(err)
	}
	cx, cy := int(client.left+client.right)/2, int(client.top+client.bottom)/2
	var d display.Info
	for _, x := range displays {
		if cx >= x.X && cy >= x.Y && cx < x.X+x.Width && cy < x.Y+x.Height {
			d = x
		}
	}
	if d.Width == 0 {
		t.Fatalf("no display holds the fixture's client area %+v: %+v", client, displays)
	}
	var points []point
	for _, fx := range []float64{0.02, 0.13, 0.37, 0.5, 0.71, 0.98} {
		for _, fy := range []float64{0.02, 0.5, 0.98} {
			points = append(points, point{client.left + int32(fx*float64(client.right-client.left)), client.top + int32(fy*float64(client.bottom-client.top))})
		}
	}
	seq := uint64(0)
	for _, p := range points {
		seq++
		mustOK(t, j.Apply(wire.InputEvent{Seq: seq, K: "move", Display: d.Index, X: int(p.x) - d.X, Y: int(p.y) - d.Y}))
		if got := cursorPos(); got != p {
			t.Fatalf("a move to (%d,%d) on display %d put the cursor at (%d,%d)", int(p.x)-d.X, int(p.y)-d.Y, d.Index, got.x, got.y)
		}
	}
	ci := struct {
		size, flags uint32
		cursor      uintptr
		x, y        int32
	}{size: 24}
	procGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci)))
	t.Logf("%d moves on display %d landed on their pixels; the cursor after them: flags %#x handle %#x", len(points), d.Index, ci.flags, ci.cursor)

	same, _, _ := procFindWindowExW.Call(f.hwnd, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Button"))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Same"))))
	if same == 0 {
		t.Fatal("the fixture has no Same button")
	}
	var br winRect
	procGetWindowRect.Call(same, uintptr(unsafe.Pointer(&br)))
	target := point{(br.left + br.right) / 2, (br.top + br.bottom) / 2}
	if root := rootAt(target.x, target.y); root != f.hwnd {
		t.Fatalf("the window at the Same button's centre is %#x %q, not the fixture", root, windowTitle(root))
	}
	mustOK(t, j.Apply(wire.InputEvent{Seq: 100, K: "move", Display: d.Index, X: int(target.x) - d.X, Y: int(target.y) - d.Y}))
	mustOK(t, j.Apply(wire.InputEvent{Seq: 101, K: "button", B: "left", Down: true}))
	mustOK(t, j.Apply(wire.InputEvent{Seq: 102, K: "button", B: "left"}))
	transcript := f.waitValue("Transcript", 10*time.Second, func(v string) bool { return strings.Contains(v, "Same 1") })
	if !strings.Contains(transcript, "Same 1") {
		t.Fatalf("the click did not press Same; the transcript is %q", transcript)
	}
}

func TestInputAckLatency(t *testing.T) {
	f := startFixture(t)
	j := f.injector()
	mustOK(t, j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: "latency"}))
	c, first := serveTestStream(t, j, 0)
	if first["ok"] != true {
		t.Fatalf("first line %v", first)
	}
	var times []time.Duration
	for i := range 100 {
		ev := wire.InputEvent{Seq: uint64(i + 1), K: "key", SC: 0x4B, Ext: true, Down: i%2 == 0}
		start := time.Now()
		c.send(ev)
		a := c.ack()
		times = append(times, time.Since(start))
		if !a.OK || a.Seq != ev.Seq {
			t.Fatalf("ack %+v", a)
		}
	}
	c.closeAndWait()
	slices.Sort(times)
	var sessionCost time.Duration
	for range 100 {
		start := time.Now()
		if _, err := session.Current(); err != nil {
			t.Fatal(err)
		}
		sessionCost += time.Since(start)
	}
	var deskCost time.Duration
	for range 100 {
		start := time.Now()
		if d := session.InputDesktop(); d != session.DesktopDefault {
			t.Fatalf("input desktop %q", d)
		}
		deskCost += time.Since(start)
	}
	t.Logf("ack latency over 100 key events: p50 %s, p95 %s, max %s; session.Current takes %s and session.InputDesktop %s on average",
		times[49], times[94], times[99], sessionCost/100, deskCost/100)
}
