//go:build !windows

package server

import (
	"encoding/base64"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// sessionClient connects to a server without a HomeDir whose session source
// answers sess and found.
func sessionClient(t *testing.T, sess session.Session, found bool) *rpcClient {
	t.Helper()
	_, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, AllowExec: true,
		Session: func() (session.Session, bool) { return sess, found }})
	expect(t, r, "hello")
	return &rpcClient{t: t, r: r, w: w}
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestHomeRelativePathsResolveAgainstTheSessionHome(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := sessionClient(t, session.Session{User: "deskhand", Home: home}, true)

	if got := c.ok("fs.stat", proto.Message{"path": "~"}).String("path"); got != home {
		t.Fatalf("fs.stat ~ = %q, want %q", got, home)
	}
	if got := c.ok("fs.stat", proto.Message{"path": "Desktop/big.bin"}).String("path"); got != filepath.Join(home, "Desktop", "big.bin") {
		t.Fatalf("fs.stat of a relative path = %q", got)
	}
	out := c.ok("exec.run", proto.Message{"cmd": "pwd"}).String("stdout")
	if got := realPath(t, strings.TrimSpace(out)); got != realPath(t, home) {
		t.Fatalf("exec.run without cwd ran in %q", out)
	}
	out = c.ok("exec.run", proto.Message{"cmd": "pwd", "cwd": "sub"}).String("stdout")
	if got := realPath(t, strings.TrimSpace(out)); got != realPath(t, filepath.Join(home, "sub")) {
		t.Fatalf("exec.run with cwd sub ran in %q", out)
	}
	res := c.call("fs.delete", proto.Message{"path": home, "recursive": true})
	if res["ok"] != false {
		t.Fatalf("fs.delete of the session home %v", res)
	}
}

func TestAMissingSessionHomeNamesHowToCreateIt(t *testing.T) {
	home := filepath.Join(t.TempDir(), "bob")
	c := sessionClient(t, session.Session{User: "bob", Home: home}, true)
	want := "the home folder '" + home + "' of the desktop user bob does not exist; create it with exec.run (mkdir '" +
		home + "' && chown bob: '" + home + "'), then retry"
	data := base64.StdEncoding.EncodeToString([]byte("x"))
	for _, res := range []proto.Message{
		c.call("fs.write", proto.Message{"path": "~/x", "data": data}),
		c.call("fs.mkdir", proto.Message{"path": home}),
	} {
		if res["ok"] != false || res.String("error") != want {
			t.Fatalf("answer %v, want the error %q", res, want)
		}
	}
}

func TestWithoutASessionPathsResolveAgainstTheAgentHome(t *testing.T) {
	want, err := os.UserHomeDir()
	if err != nil {
		t.Skip("this user has no home directory")
	}
	c := sessionClient(t, session.Session{}, false)
	if got := c.ok("fs.stat", proto.Message{"path": "~"}).String("path"); got != filepath.Clean(want) {
		t.Fatalf("fs.stat ~ = %q, want %q", got, want)
	}
}

func TestLaunchRunsInTheSessionEnvironment(t *testing.T) {
	home := t.TempDir()
	out := filepath.Join(home, "env.txt")
	env := []string{"HOME=" + home, "USER=deskhand", "PATH=/usr/bin:/bin", "WAYLAND_DISPLAY=wayland-0"}
	c := sessionClient(t, session.Session{User: "deskhand", Home: home, Env: env}, true)
	script := `printf '%s|%s|%s|%s' "$HOME" "$USER" "$WAYLAND_DISPLAY" "$EXTRA" > "$0"`
	c.ok("app.launch", proto.Message{"path": "/bin/sh", "args": []string{"-c", script, out},
		"env": map[string]any{"EXTRA": "x", "USER": "replaced"}, "wait_window_ms": 0})
	c.event("process.exited")
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := home + "|replaced|wayland-0|x"; string(got) != want {
		t.Fatalf("the program saw %q, want %q", got, want)
	}
}

func TestLaunchGoesThroughTheUsersServiceManagerScope(t *testing.T) {
	home := t.TempDir()
	runner := filepath.Join(home, "runner")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HOME/runner-args\"\nwhile [ \"$1\" != -- ]; do shift; done\nshift\nexec \"$@\"\n"
	if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	c := sessionClient(t, session.Session{User: "deskhand", Home: home, Env: env, ScopeRunner: runner, ScopeVersion: 259}, true)
	out := filepath.Join(home, "ran.txt")
	c.ok("app.launch", proto.Message{"path": "/bin/sh", "args": []string{"-c", `printf '%s' "$1" > "$0"`, out, "$HOME literal"},
		"wait_window_ms": 0})
	ev := c.event("process.exited")
	if ev.String("path") != "/bin/sh" {
		t.Fatalf("process.exited %v", ev)
	}
	if b, _ := os.ReadFile(out); string(b) != "$HOME literal" {
		t.Fatalf("the program wrote %q", b)
	}
	args, _ := os.ReadFile(filepath.Join(home, "runner-args"))
	want := "--user\n--scope\n--quiet\n--collect\n--expand-environment=no\n--\n/bin/sh\n-c\n"
	if !strings.HasPrefix(string(args), want) {
		t.Fatalf("the runner got %q", args)
	}
}

func TestLaunchOfAMissingProgramFailsAlsoThroughTheScope(t *testing.T) {
	home := t.TempDir()
	c := sessionClient(t, session.Session{User: "deskhand", Home: home, Env: []string{"HOME=" + home}, ScopeRunner: "/bin/sh", ScopeVersion: 259}, true)
	missing := filepath.Join(home, "missing")
	res := c.call("app.launch", proto.Message{"path": missing, "wait_window_ms": 0})
	if res["ok"] != false || !strings.HasPrefix(res.String("error"), "cannot start '"+missing+"': ") {
		t.Fatalf("app.launch %v", res)
	}
}

func TestSessionCommandsRunAsTheSessionUser(t *testing.T) {
	c := exec.Command("/bin/true")
	cred := &syscall.Credential{Uid: 1000, Gid: 1000, Groups: []uint32{4, 27}}
	runInSession(c, []string{"HOME=/home/deskhand"}, cred, nil)
	if c.SysProcAttr.Credential != cred || !c.SysProcAttr.Setsid || len(c.Env) != 1 || c.Path != "/bin/true" {
		t.Fatalf("attr %+v env %q", c.SysProcAttr, c.Env)
	}
	plain := exec.Command("/bin/true")
	runInSession(plain, []string{"HOME=/root"}, nil, nil)
	if plain.SysProcAttr != nil && plain.SysProcAttr.Credential != nil {
		t.Fatalf("a nil credential set %+v", plain.SysProcAttr)
	}
}

func TestSessionInfoReportsTheDesktopUser(t *testing.T) {
	c := sessionClient(t, session.Session{ID: "1", User: "deskhand", UID: 1000, Home: "/home/deskhand", Type: "wayland", Locked: true}, true)
	got := c.ok("session.info", nil)
	if got.String("user") != "deskhand" || got.Int("uid") != 1000 || got.String("home") != "/home/deskhand" ||
		got.String("type") != "wayland" || got.String("session") != "1" || got["locked"] != true {
		t.Fatalf("session.info %v", got)
	}
	none := sessionClient(t, session.Session{}, false)
	if got := none.ok("session.info", nil); got.Has("user") {
		t.Fatalf("session.info without a session %v", got)
	}
}

// toggleClipboard is a fakeClipboard whose availability can change.
type toggleClipboard struct {
	fakeClipboard
	on atomic.Bool
}

func (c *toggleClipboard) Available() bool { return c.on.Load() }

func TestCapsAreSentAgainWhenTheClipboardAppears(t *testing.T) {
	old := capsCheckInterval
	capsCheckInterval = 20 * time.Millisecond
	t.Cleanup(func() { capsCheckInterval = old })
	cb := &toggleClipboard{}
	_, r, _, _ := startServerS(t, Options{Clipboard: cb, HomeDir: t.TempDir()})
	if capSet(expect(t, r, "hello"))["clipboard"] {
		t.Fatal("hello lists a clipboard that is not there")
	}
	cb.on.Store(true)
	if !capSet(expectCaps(t, r))["clipboard"] {
		t.Fatal("the caps update lacks the clipboard")
	}
	cb.on.Store(false)
	if capSet(expectCaps(t, r))["clipboard"] {
		t.Fatal("the caps update keeps a clipboard that is gone")
	}
}

// gatedClipboard is a toggleClipboard whose Available waits for gate to
// close while held is set.
type gatedClipboard struct {
	toggleClipboard
	gate chan struct{}
	held atomic.Bool
}

func (c *gatedClipboard) Available() bool {
	if c.held.Load() {
		<-c.gate
	}
	return c.on.Load()
}

func TestTheHelloCarriesTheCapsFoundLastAndTheirCheckFollowsTheWelcome(t *testing.T) {
	cb := &gatedClipboard{gate: make(chan struct{})}
	cb.on.Store(true)
	_, r, _, first := startServerS(t, Options{Clipboard: cb, HomeDir: t.TempDir()})
	if !capSet(expect(t, r, "hello"))["clipboard"] {
		t.Fatal("the first hello lacks the clipboard")
	}
	cb.on.Store(false)
	cb.held.Store(true)
	c, err := net.Dial("tcp", first.RemoteAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(5 * time.Second))
	r2 := proto.NewReader(c)
	if !capSet(expect(t, r2, "hello"))["clipboard"] {
		t.Fatal("the second hello does not carry the caps found last")
	}
	send(t, proto.NewWriter(c), proto.Message{"t": "welcome", "v": 1})
	close(cb.gate)
	if capSet(expectCaps(t, r2))["clipboard"] {
		t.Fatal("the caps checked after the host's welcome keep a clipboard that is gone")
	}
}

func TestTheFirstHelloCarriesTheCapsCheckedWhenServeStarted(t *testing.T) {
	cb := &gatedClipboard{gate: make(chan struct{})}
	cb.on.Store(true)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s := New(Options{Clipboard: cb, HomeDir: t.TempDir()})
	go s.Serve(l)
	deadline := time.Now().Add(10 * time.Second)
	for s.lastCaps.Load() == nil {
		if time.Now().After(deadline) {
			t.Fatal("Serve has not checked the capabilities 10 s after it started")
		}
		time.Sleep(time.Millisecond)
	}
	cb.on.Store(false)
	cb.held.Store(true)
	t.Cleanup(func() { close(cb.gate) })
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if !capSet(expect(t, proto.NewReader(c), "hello"))["clipboard"] {
		t.Fatal("the first hello does not carry the caps checked when Serve started")
	}
}

// countedClipboard is a toggleClipboard that counts the calls of Available.
type countedClipboard struct {
	toggleClipboard
	calls atomic.Int64
}

func (c *countedClipboard) Available() bool {
	c.calls.Add(1)
	return c.on.Load()
}

func TestTheCapsOfTheHelloAreCheckedEveryIntervalWithoutAConnection(t *testing.T) {
	old := capsCheckInterval
	capsCheckInterval = 20 * time.Millisecond
	t.Cleanup(func() { capsCheckInterval = old })
	cb := &countedClipboard{}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s := New(Options{Clipboard: cb, HomeDir: t.TempDir()})
	go s.Serve(l)
	deadline := time.Now().Add(10 * time.Second)
	for s.lastCaps.Load() == nil {
		if time.Now().After(deadline) {
			t.Fatal("Serve has not checked the capabilities 10 s after it started")
		}
		time.Sleep(time.Millisecond)
	}
	cb.on.Store(true)
	checked := cb.calls.Load()
	for cb.calls.Load() < checked+2 {
		if time.Now().After(deadline) {
			t.Fatal("the capabilities were not checked twice within 10 s while no connection was open")
		}
		time.Sleep(time.Millisecond)
	}
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if !capSet(expect(t, proto.NewReader(c), "hello"))["clipboard"] {
		t.Fatal("the hello lacks the clipboard that appeared while no connection was open")
	}
}

func TestNothingFollowsTheHelloBeforeTheWelcomeAndTheCapsUpdateComesFirst(t *testing.T) {
	old := capsCheckInterval
	capsCheckInterval = 20 * time.Millisecond
	t.Cleanup(func() { capsCheckInterval = old })
	cb := &countedClipboard{}
	cb.on.Store(true)
	s, r, _, first := startServerS(t, Options{Clipboard: cb, HomeDir: t.TempDir()})
	expect(t, r, "hello")
	go func() {
		for {
			if _, err := r.Next(); err != nil {
				return
			}
		}
	}()

	c, err := net.Dial("tcp", first.RemoteAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(10 * time.Second))
	r2 := proto.NewReader(c)
	if !capSet(expect(t, r2, "hello"))["clipboard"] {
		t.Fatal("the hello lacks the clipboard")
	}
	cb.on.Store(false)
	checked := cb.calls.Load()
	deadline := time.Now().Add(10 * time.Second)
	for cb.calls.Load() < checked+2 {
		if time.Now().After(deadline) {
			t.Fatal("the capabilities were not checked twice within 10 s")
		}
		time.Sleep(time.Millisecond)
	}

	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
				s.emit("mark", proto.Message{})
				time.Sleep(time.Millisecond)
			}
		}
	}()
	defer func() { close(stop); <-stopped }()
	c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if m, err := r2.Next(); err == nil {
		t.Fatalf("before the host's welcome the agent sent %v", m)
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("before the host's welcome the read failed with %v", err)
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	send(t, proto.NewWriter(c), proto.Message{"t": "welcome", "v": 1})
	m, err := proto.NewReader(c).Next()
	if err != nil {
		t.Fatal(err)
	}
	if m.String("name") != "caps.changed" || capSet(m)["clipboard"] {
		t.Fatalf("the first message after the welcome is %v", m)
	}
}
