// Package server implements the agent side of docs/protocol.md.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/clipboard"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// Options configure the agent.
type Options struct {
	AllowExec bool
	Clipboard clipboard.Clipboard
	// HomeDir is the home directory of home-relative paths, file drops and
	// the default working directory; empty means the home directory of the
	// desktop session, or the agent's own without a session.
	HomeDir string
	// Session returns the desktop session whose user runs app.launch and the
	// clipboard tools; nil means the platform default (loginctl outside
	// Windows, none on Windows), or none when HomeDir is set.
	Session func() (session.Session, bool)
	// Shutdown runs an OS shutdown; nil uses the platform default.
	Shutdown func() error
	Logger   *log.Logger
	// UI serves the ui and win ops; nil means the agent has no ui capability.
	UI UI
	// DeployRoot is a folder the agent keeps locked; nil means none.
	DeployRoot *DeployRoot
	// DataDir holds the request journal (journal/) and the exec.start jobs
	// (jobs/); empty keeps both in memory only.
	DataDir string
	// Version is the agent version the hello carries; empty means "dev".
	Version string
}

// DeployRoot names a folder that the fs operations create through Lock when
// they would create it, and whose lock fs.stat reports.
type DeployRoot struct {
	Path string
	// Lock creates the folder locked.
	Lock func() error
	// Locked reports whether the folder is locked now.
	Locked func() bool
}

// welcomeTimeout bounds the wait for the host's welcome after the hello.
const welcomeTimeout = 10 * time.Second

// Server serves rpc streams; each runs on its own until it closes.
type Server struct {
	opt Options

	ops     map[string]opEntry
	connMu  sync.Mutex
	conns   map[*conn]struct{}
	jobs    *jobTable
	journal *journal

	// deployMu orders the creations of the deploy root.
	deployMu sync.Mutex

	launchMu sync.Mutex
	// claimed holds the ids of windows an app.launch has taken; a concurrent
	// launch does not take one of them. An id leaves the set when its window
	// closes, and the set is emptied when a UI helper starts.
	claimed claimSet

	ownMu   sync.Mutex
	ownSeq  uint64
	ownTime time.Time
	// lastSeq is the clipboard sequence the watcher saw last.
	lastSeq uint64

	// capsOnce starts the capabilities watcher and watchOnce the clipboard
	// watcher; clipKick asks the clipboard watcher to poll at its shortest
	// interval again.
	capsOnce  sync.Once
	watchOnce sync.Once
	// timing holds capsCheckInterval, clipboardPollMin and clipboardPollMax
	// as they were when the server was made.
	timing   struct{ caps, pollMin, pollMax time.Duration }
	clipKick chan struct{}

	// ownHome is the agent's own home directory, used without HomeDir and
	// without a session.
	ownHome string

	// lastCaps holds the capabilities caps returned last.
	lastCaps atomic.Pointer[capStates]
}

func New(opt Options) *Server {
	if opt.Logger == nil {
		opt.Logger = log.Default()
	}
	if opt.Session == nil && opt.HomeDir == "" {
		opt.Session = defaultSessionSource()
	}
	if opt.Clipboard == nil {
		opt.Clipboard = newClipboard(opt.Session)
	}
	s := &Server{opt: opt, ops: map[string]opEntry{}, conns: map[*conn]struct{}{}, jobs: newJobTable(), ownHome: agentHome(),
		clipKick: make(chan struct{}, 1)}
	s.timing.caps, s.timing.pollMin, s.timing.pollMax = capsCheckInterval, clipboardPollMin, clipboardPollMax
	journalDir := ""
	if opt.DataDir != "" {
		journalDir = filepath.Join(opt.DataDir, "journal")
		s.jobs.store = &jobStore{dir: filepath.Join(opt.DataDir, "jobs"), lg: opt.Logger}
		s.jobs.restore(s.jobs.store.load())
	}
	s.journal = openJournal(journalDir, opt.Logger)
	s.registerBuiltins()
	return s
}

// Close shuts the UI helper down when the server has one.
func (s *Server) Close() {
	if c, ok := s.opt.UI.(interface{ Close() }); ok {
		c.Close()
	}
}

// Serve accepts connections on l until it is closed and serves each as one
// rpc stream with ServeConn.
func (s *Server) Serve(l net.Listener) error {
	s.Start()
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go s.ServeConn(context.Background(), c, c.RemoteAddr().String())
	}
}

// Start checks the capabilities once and starts watchCaps; it runs before
// the first stream is served.
func (s *Server) Start() {
	s.caps()
	s.startCapsWatcher()
}

// session returns the desktop session, when there is one.
func (s *Server) session() (session.Session, bool) {
	if s.opt.Session == nil {
		return session.Session{}, false
	}
	return s.opt.Session()
}

// homeDir returns the home directory that view reads.
func (s *Server) homeDir() string {
	return s.view().dir
}

// capsCheckInterval is the time between two checks of the capabilities by
// watchCaps.
var capsCheckInterval = 5 * time.Second

// caps checks the capabilities, which on Linux looks up the desktop
// session, and records them as the ones found last.
func (s *Server) caps() capStates {
	caps := s.checkCaps()
	s.lastCaps.Store(&caps)
	return caps
}

// helloCaps returns the capabilities found last, or checks them when none
// were found yet.
func (s *Server) helloCaps() capStates {
	if caps := s.lastCaps.Load(); caps != nil {
		return *caps
	}
	return s.caps()
}

// fileXfer is a file push in progress: f is open on name in fsys, path is
// its absolute path.
type fileXfer struct {
	f    *os.File
	fsys writeFS
	name string
	path string
	size int
	got  int
	h    interface{ Write([]byte) (int, error) }
	sum  func() []byte
}

// discard closes the push and removes its file.
func (x *fileXfer) discard() {
	x.f.Close()
	_ = x.fsys.Remove(x.name)
	x.fsys.Close()
}

// finish closes the push and keeps its file.
func (x *fileXfer) finish() {
	x.f.Close()
	x.fsys.Close()
}

// beginDrop creates the file of a file push named base(name) in the Desktop
// folder of the home directory, or in its Downloads folder for dest
// "downloads", creating that folder when missing. A name that is taken gets
// " (2)", " (3)", … before its extension. In the desktop session's home the
// folder and the file are created through an os.Root and belong to the
// session's user.
func (s *Server) beginDrop(dest, name string) (*fileXfer, error) {
	name = filepath.Base(name)
	if name == "." || name == "" || name == ".." || name == string(filepath.Separator) {
		return nil, errors.New("bad name")
	}
	folder := "Desktop"
	if dest == "downloads" {
		folder = "Downloads"
	}
	v := s.view()
	dir := filepath.Join(v.dir, folder)
	fsys, dirName, err := s.writerIn(v, dir)
	if err != nil {
		return nil, err
	}
	if _, err := fsys.Stat(dirName); errors.Is(err, fs.ErrNotExist) {
		_ = fsys.MkdirAll(dirName)
	}
	fileName := uniqueName(fsys, filepath.Join(dirName, name))
	f, err := fsys.OpenFile(fileName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		err = fsys.explain(fileName, err)
		fsys.Close()
		return nil, err
	}
	h := sha256.New()
	return &fileXfer{f: f, fsys: fsys, name: fileName, path: filepath.Join(dir, filepath.Base(fileName)), h: h,
		sum: func() []byte { return h.Sum(nil) }}, nil
}

// greet writes the hello with caps and reads the host's first message, which
// has to be its welcome within welcomeTimeout. It reports whether the welcome
// arrived and returns the controller name it carries.
func (s *Server) greet(rw io.ReadWriteCloser, r *proto.Reader, caps capStates, peer string) (string, bool) {
	lg := s.opt.Logger
	setWriteDeadline(rw, connWriteTimeout)
	if err := proto.NewWriter(rw).Send(s.helloMessage(caps)); err != nil {
		lg.Printf("hello to %s: %v", peer, err)
		return "", false
	}
	timer := time.AfterFunc(welcomeTimeout, func() { _ = rw.Close() })
	m, err := r.Next()
	if !timer.Stop() {
		lg.Printf("no welcome from %s within %s; closing the rpc stream", peer, welcomeTimeout)
		return "", false
	}
	if err != nil {
		if err != io.EOF {
			lg.Printf("read from %s before its welcome: %v", peer, err)
		}
		return "", false
	}
	if m.Type() != "welcome" {
		lg.Printf("the first message from %s is %q, not welcome; closing the rpc stream", peer, m.Type())
		return "", false
	}
	return m.String("controller"), true
}

// ServeConn serves one rpc stream: it writes the hello, waits for the
// host's welcome and then answers requests and sends events until the stream
// or ctx ends. peer names the host in the log.
func (s *Server) ServeConn(ctx context.Context, rw io.ReadWriteCloser, peer string) {
	defer rw.Close()
	lg := s.opt.Logger
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopClosing := context.AfterFunc(ctx, func() { _ = rw.Close() })
	defer stopClosing()

	r := proto.NewReader(rw)
	sentCaps := s.helloCaps()
	controller, ok := s.greet(rw, r, sentCaps, peer)
	if !ok {
		return
	}
	lg.Printf("connection from %s (controller %q)", peer, controller)
	cn := newConn(rw, lg)
	go cn.writeLoop()
	defer cn.stopWriter()
	send := cn.send

	cn.setCaps(sentCaps)
	s.sendCapsUpdate(cn)
	s.addConn(cn)
	defer s.removeConn(cn)

	quit := make(chan struct{})
	defer close(quit)
	s.startWatchers()
	clipOps := newSerialQueue(quit)
	go func() { // keepalive
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
				send(proto.Message{"t": "ping"})
			}
		}
	}()

	inFlight := make(chan struct{}, maxInFlight)
	xfers := map[string]*fileXfer{}
	defer func() {
		for _, x := range xfers {
			x.discard()
		}
	}()
	for {
		m, err := r.Next()
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				lg.Printf("read: %v", err)
			}
			return
		}
		switch m.Type() {
		case "ping":
			send(proto.Message{"t": "pong"})
		case "pong", "welcome":
		case "req":
			select {
			case inFlight <- struct{}{}:
				go func(m proto.Message) {
					defer func() { <-inFlight }()
					s.dispatch(ctx, m, cn.sendReply)
				}(m)
			default:
				send(proto.Message{"t": "res", "id": m["id"], "ok": false, "error": tooManyRequests()})
			}
		case "clipboard.get":
			reply := proto.Message{"t": "clipboard.text"}
			if id, ok := m["id"]; ok {
				reply["id"] = id
			}
			queued := clipOps.run(func() {
				text, err := s.opt.Clipboard.Text()
				if err != nil {
					lg.Printf("clipboard.get: %v", err)
				}
				reply["text"] = cutUTF8Bytes(text, clipboardMaxBytes)
				send(reply)
			})
			if !queued {
				lg.Printf("clipboard.get: %s", errClipboardQueueFull)
				reply["text"], reply["error"] = "", errClipboardQueueFull
				send(reply)
			}
		case "clipboard.set":
			text := m.String("text")
			queued := clipOps.run(func() {
				if err := s.opt.Clipboard.SetText(text); err != nil {
					lg.Printf("clipboard.set: %v", err)
				} else {
					s.markOwnClipboard()
				}
			})
			if !queued {
				lg.Printf("clipboard.set dropped: %s", errClipboardQueueFull)
			}
		case "resolution.set":
			if err := setResolution(m.Int("w"), m.Int("h")); err != nil {
				lg.Printf("resolution.set: %v", err)
			}
			if wdt, hgt, ok := currentResolution(); ok {
				send(proto.Message{"t": "resolution", "w": wdt, "h": hgt})
			}
		case "file.begin":
			id := m.String("id")
			x, err := s.beginDrop(m.String("dest"), m.String("name"))
			if err != nil {
				send(proto.Message{"t": "file.done", "id": id, "error": err.Error()})
				continue
			}
			x.size = m.Int("size")
			xfers[id] = x
		case "file.chunk":
			x := xfers[m.String("id")]
			if x == nil {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(m.String("data"))
			if err == nil && len(data) > fsChunkMax {
				err = fmt.Errorf("a file.chunk carries at most %d bytes; send smaller chunks", fsChunkMax)
			}
			if err == nil {
				_, err = x.f.Write(data)
				x.h.Write(data)
				x.got += len(data)
			}
			if err != nil {
				x.discard()
				delete(xfers, m.String("id"))
				send(proto.Message{"t": "file.done", "id": m.String("id"), "error": err.Error()})
			}
		case "file.end":
			id := m.String("id")
			x := xfers[id]
			if x == nil {
				continue
			}
			delete(xfers, id)
			if want := m.String("sha256"); (want != "" && want != hex.EncodeToString(x.sum())) || (x.size > 0 && x.got != x.size) {
				x.discard()
				send(proto.Message{"t": "file.done", "id": id, "error": "digest or size mismatch"})
				continue
			}
			x.finish()
			send(proto.Message{"t": "file.done", "id": id, "path": x.path})
		case "shutdown":
			fn := s.opt.Shutdown
			if fn == nil {
				fn = platformShutdown
			}
			if err := fn(); err != nil {
				lg.Printf("shutdown: %v", err)
			} else {
				s.Close()
			}
		case "exec":
			if !s.opt.AllowExec {
				send(proto.Message{"t": "exec.result", "code": -1, "out": "exec disabled"})
				continue
			}
			out, code := runShell(m.String("cmd"))
			send(proto.Message{"t": "exec.result", "code": code, "out": out})
		}
	}
}

// uniqueName returns p, or p with " (2)", " (3)", … before its extension,
// whichever is the first name fsys has no entry for.
func uniqueName(fsys writeFS, p string) string {
	if _, err := fsys.Lstat(p); err != nil {
		return p
	}
	ext := filepath.Ext(p)
	base := p[:len(p)-len(ext)]
	for i := 2; ; i++ {
		q := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := fsys.Lstat(q); err != nil {
			return q
		}
	}
}

var (
	// clipboardPollMin is the time between two clipboard polls after a
	// change; each poll that finds no change doubles it up to
	// clipboardPollMax.
	clipboardPollMin = clipboardPollFastest
	clipboardPollMax = clipboardPollSlowest
)

// startCapsWatcher starts watchCaps once per agent.
func (s *Server) startCapsWatcher() {
	s.capsOnce.Do(func() { go s.watchCaps() })
}

// startWatchers starts, once per agent, the watchers of the capabilities and
// of the clipboard.
func (s *Server) startWatchers() {
	s.startCapsWatcher()
	s.watchOnce.Do(func() { go s.watchClipboard() })
}

// openConns returns the open connections.
func (s *Server) openConns() []*conn {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	return conns
}

// capsChanged is the caps.changed event with all of caps.
func capsChanged(caps capStates) proto.Message {
	return proto.Message{"t": "ev", "name": "caps.changed", "caps": caps}
}

// sendCapsUpdate runs when the host's welcome on c has arrived: it checks
// the capabilities, sends caps.changed to c when they differ from the
// hello's, and from then on lets watchCaps update c.
func (s *Server) sendCapsUpdate(c *conn) {
	if caps := s.caps(); c.replaceCaps(caps) {
		c.send(capsChanged(caps))
	}
	c.followCaps()
}

// watchCaps checks the capabilities every capsCheckInterval, also while no
// stream is open, so that a hello carries a check at most one interval old,
// and sends caps.changed to each open stream that sendCapsUpdate has
// followed and whose last capabilities differ.
func (s *Server) watchCaps() {
	for {
		time.Sleep(s.timing.caps)
		caps := s.caps()
		for _, c := range s.openConns() {
			if c.capsFollowedYet() && c.replaceCaps(caps) {
				c.send(capsChanged(caps))
			}
		}
	}
}

// markOwnClipboard records the clipboard after the agent set it, so that the
// watcher does not report it as a change.
func (s *Server) markOwnClipboard() {
	seq := s.opt.Clipboard.Sequence()
	s.ownMu.Lock()
	s.ownSeq, s.ownTime, s.lastSeq = seq, time.Now(), seq
	s.ownMu.Unlock()
	select {
	case s.clipKick <- struct{}{}:
	default:
	}
}

// watchClipboard polls the clipboard while a connection is open and the
// clipboard is available, every clipboardPollMin after a change and up to
// every clipboardPollMax while nothing changes, and sends a change the agent
// did not make to every open connection as clipboard.changed.
func (s *Server) watchClipboard() {
	s.ownMu.Lock()
	s.lastSeq = s.opt.Clipboard.Sequence()
	s.ownMu.Unlock()
	wait := s.timing.pollMin
	for {
		select {
		case <-time.After(wait):
		case <-s.clipKick:
			wait = s.timing.pollMin
			continue
		}
		wait = min(2*wait, s.timing.pollMax)
		if len(s.openConns()) == 0 || !s.opt.Clipboard.Available() {
			continue
		}
		seq := s.opt.Clipboard.Sequence()
		s.ownMu.Lock()
		changed := seq != s.lastSeq
		s.lastSeq = seq
		own := seq == s.ownSeq && time.Since(s.ownTime) < 2*time.Second
		s.ownMu.Unlock()
		if !changed {
			continue
		}
		wait = s.timing.pollMin
		if own {
			continue
		}
		text, err := s.opt.Clipboard.Text()
		if err != nil || text == "" {
			continue
		}
		msg := proto.Message{"t": "clipboard.changed", "text": cutUTF8Bytes(text, clipboardMaxBytes)}
		for _, c := range s.openConns() {
			c.send(msg)
		}
	}
}

// serialQueue runs functions one after another on a goroutine of its own
// until done is closed.
type serialQueue chan func()

// clipboardQueueSize bounds the clipboard operations a connection holds.
const clipboardQueueSize = 64

func newSerialQueue(done chan struct{}) serialQueue {
	q := make(serialQueue, clipboardQueueSize)
	go func() {
		for {
			select {
			case <-done:
				return
			case f := <-q:
				f()
			}
		}
	}()
	return q
}

// errClipboardQueueFull answers a clipboard operation that finds the queue
// of its connection full.
var errClipboardQueueFull = fmt.Sprintf("%d clipboard operations of this connection are waiting for the PC's clipboard tool; retry once it answers", clipboardQueueSize)

// run queues f and reports whether it did; a full queue does not take f.
func (q serialQueue) run(f func()) bool {
	select {
	case q <- f:
		return true
	default:
		return false
	}
}

func runShell(cmd string) (string, int) {
	sys, err := agentSystem()
	if err != nil {
		return err.Error(), 1
	}
	c, err := buildCommand(proto.Message{"cmd": cmd}, sys)
	if err != nil {
		return err.Error(), 1
	}
	hideWindow(c)
	// The child's input is a pipe at end-of-file.
	c.Stdin = strings.NewReader("")
	combined := newRing(runStreamBytes)
	c.Stdout, c.Stderr = combined, combined
	err = c.Run()
	out, cut := combined.snapshot()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return streamText(out, cut), code
}

// Logger returns the logger of Options.Logger.
func (s *Server) Logger() *log.Logger { return s.opt.Logger }
