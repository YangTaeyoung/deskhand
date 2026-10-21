// Package server implements the agent side of docs/protocol.md.
package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/clipboard"
	"github.com/YangTaeyoung/deskhand/internal/agent/diag"
	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/input"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Options configure the agent.
type Options struct {
	AllowExec bool
	Clipboard clipboard.Clipboard
	// HomeDir is the home directory of home-relative paths and the default
	// working directory; empty means the home directory of the
	// desktop session, or the agent's own without a session.
	HomeDir string
	// Session returns the desktop session whose user runs app.launch and the
	// clipboard tools; nil means the platform default (loginctl outside
	// Windows, none on Windows), or none when HomeDir is set.
	Session func() (session.Session, bool)
	Logger  *log.Logger
	// UI serves the ui and win ops; nil means the agent has no ui capability.
	UI UI
	// DeployRoot is a folder the agent keeps locked; nil means none.
	DeployRoot *DeployRoot
	// DataDir holds the request journal (journal/) and the exec.start jobs
	// (jobs/); empty keeps both in memory only.
	DataDir string
	// Version is the agent version the hello carries; empty means "dev".
	Version string
	// Diag reads the diagnostics of the diag ops; nil means diag.Local.
	Diag diag.Source
	// SessionInfo describes the Windows session the agent runs in, for
	// session.info, the hello's session and the screen and input
	// capabilities; nil means session.Current.
	SessionInfo func() (session.Info, error)
	// Displays lists the displays for display.list and the hello; nil means
	// display.List.
	Displays func() ([]display.Info, error)
	// Capture takes the still image of screen.capture; nil means
	// capture.CaptureStill.
	Capture func(display int, format string, maxWidth, quality int) (capture.StillResult, error)
	// ScreenState reports whether the screen can be captured and encoded,
	// for the screen capability; nil means screenState.
	ScreenState func() error
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
	ptys    *ptyTable
	journal *journal
	// receipts holds the outcomes of the transfers committed last.
	receipts *receiptStore
	// started holds the processes exec, app.launch and pty started.
	started startedTable
	// debug holds the apps launched for debug sessions.
	debug debugState

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

	// input injects the input stream's events and holds the writer
	// generation that the writer ops (wire.WriterOp) are checked against.
	input *input.Injector
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
	if opt.SessionInfo == nil {
		opt.SessionInfo = session.Current
	}
	if opt.Displays == nil {
		opt.Displays = display.List
	}
	if opt.Capture == nil {
		opt.Capture = capture.CaptureStill
	}
	if opt.ScreenState == nil {
		opt.ScreenState = screenState
	}
	s := &Server{opt: opt, ops: map[string]opEntry{}, conns: map[*conn]struct{}{}, jobs: newJobTable(), ptys: newPTYTable(), ownHome: agentHome(),
		clipKick: make(chan struct{}, 1)}
	s.timing.caps, s.timing.pollMin, s.timing.pollMax = capsCheckInterval, clipboardPollMin, clipboardPollMax
	journalDir := ""
	if opt.DataDir != "" {
		journalDir = filepath.Join(opt.DataDir, "journal")
		s.jobs.store = &jobStore{dir: filepath.Join(opt.DataDir, "jobs"), lg: opt.Logger}
		s.jobs.restore(s.jobs.store.load())
	}
	s.journal = openJournal(journalDir, opt.Logger)
	s.input = s.newInjector()
	s.registerBuiltins()
	return s
}

// Close releases every key and button the agent holds pressed, shuts the UI
// helper down when the server has one and ends the process trees of the
// running ptys.
func (s *Server) Close() {
	if released, err := s.input.ReleaseAll(); err != nil {
		s.opt.Logger.Printf("input: releasing the held keys and buttons at shutdown: %v", err)
	} else if len(released) > 0 {
		s.opt.Logger.Printf("input: released %d keys and buttons at shutdown", len(released))
	}
	if c, ok := s.opt.UI.(interface{ Close() }); ok {
		c.Close()
	}
	for _, p := range s.ptys.list() {
		if pid := p.processID(); pid > 0 && !p.hasExited() {
			_ = killTree(pid)
		}
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

// greet writes hello and reads the host's first message, which has to be
// its welcome within welcomeTimeout. It reports whether the welcome arrived
// and returns the controller name it carries.
func (s *Server) greet(rw io.ReadWriteCloser, r *proto.Reader, hello proto.Message, peer string) (string, bool) {
	lg := s.opt.Logger
	setWriteDeadline(rw, connWriteTimeout)
	if err := proto.NewWriter(rw).Send(hello); err != nil {
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
	s.startWatchers()
	sentCaps := s.helloCaps()
	hello := s.helloMessage(sentCaps)
	controller, ok := s.greet(rw, r, hello, peer)
	if !ok {
		return
	}
	lg.Printf("connection from %s (controller %q)", peer, controller)
	cn := newConn(rw, lg)
	go cn.writeLoop()
	defer cn.stopWriter()
	send := cn.send

	cn.setCaps(sentCaps)
	cn.setDesktop(jsonOf(hello["session"]), jsonOf(hello["displays"]))
	s.sendCapsUpdate(cn)
	s.addConn(cn)
	defer s.removeConn(cn)

	inFlight := make(chan struct{}, maxInFlight)
	for {
		m, err := r.Next()
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				lg.Printf("read: %v", err)
			}
			return
		}
		switch m.Type() {
		case "req":
			select {
			case inFlight <- struct{}{}:
				go func(m proto.Message) {
					defer func() { <-inFlight }()
					s.dispatch(ctx, m, cn.sendReply)
				}(m)
			default:
				send(TooManyRequests(m["id"]))
			}
		case "welcome":
		default:
			send(failure(m["id"], coded(wire.CodeInvalidArgument, fmt.Sprintf("the rpc stream carries only req messages, not '%s'; send {\"t\":\"req\",\"id\":…,\"op\":…}", cutChars(m.Type(), 64)), false)))
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
// of the clipboard; the clipboard watcher's first sequence number is read
// before startWatchers returns.
func (s *Server) startWatchers() {
	s.startCapsWatcher()
	s.watchOnce.Do(func() {
		s.ownMu.Lock()
		s.lastSeq = s.opt.Clipboard.Sequence()
		s.ownMu.Unlock()
		go s.watchClipboard()
	})
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
// followed and whose last capabilities differ. To those streams it also
// sends display.changed and session.changed when the displays or the
// session differ from the ones sent on the stream last, starting with the
// hello's.
func (s *Server) watchCaps() {
	for {
		time.Sleep(s.timing.caps)
		caps := s.caps()
		var followed []*conn
		for _, c := range s.openConns() {
			if c.capsFollowedYet() {
				followed = append(followed, c)
			}
		}
		if len(followed) == 0 {
			continue
		}
		displays, displaysOK := s.displaysNow()
		sess, sessOK := s.sessionNow()
		for _, c := range followed {
			if c.replaceCaps(caps) {
				c.send(capsChanged(caps))
			}
			sendDesktopChanges(c, displays, displaysOK, sess, sessOK)
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
// every clipboardPollMax while nothing changes, and sends a change from the
// sequence number startWatchers read that the agent did not make to every
// open stream as the event clipboard.changed.
func (s *Server) watchClipboard() {
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
		s.emit("clipboard.changed", proto.Message{"text": cutUTF8Bytes(text, clipboardMaxBytes)})
	}
}

// Logger returns the logger of Options.Logger.
func (s *Server) Logger() *log.Logger { return s.opt.Logger }
