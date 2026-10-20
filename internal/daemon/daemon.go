// Package daemon assembles the Deskhand daemon: the store, the host
// connections, the jobs, the transfers and the local API on the unix
// socket. One daemon runs per home folder, held by the lock
// run/daemon.lock.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/flock"
	"github.com/YangTaeyoung/deskhand/internal/daemon/hosts"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/localapi"
	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
	"github.com/YangTaeyoung/deskhand/internal/daemon/pty"
	"github.com/YangTaeyoung/deskhand/internal/daemon/qa"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/daemon/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/daemon/xfer"
	"github.com/YangTaeyoung/deskhand/internal/install"
	"github.com/YangTaeyoung/deskhand/internal/transport"
)

// DropAfterSendEnv names the test hook that drops the connection when the
// answer to the first request of an op arrives (conn.Options.DropAfterSend).
const DropAfterSendEnv = "DESKHAND_TEST_DROP_AFTER_SEND"

const (
	// eventKeep is how long stored events are kept.
	eventKeep = 30 * 24 * time.Hour
	// requestKeep is how long settled requests are kept.
	requestKeep = 7 * 24 * time.Hour
)

// ErrAlreadyRunning is returned by New when another daemon holds the lock.
var ErrAlreadyRunning = errors.New("Deskhand 데몬이 이미 실행 중입니다. deskhand daemon status로 확인하세요")

// Options configure a daemon.
type Options struct {
	// Version is the daemon version reported by hello and host.doctor.
	Version string
	// SSHExec runs ssh and scp; nil runs the real commands.
	SSHExec install.Runner
	// AgentFiles returns the agent builds for an architecture; nil means
	// hosts.FindAgentFiles.
	AgentFiles func(arch string) (install.AgentFiles, error)
	// Dial connects to agents; nil means QUIC.
	Dial conn.Dialer
	// DropAfterSend overrides DESKHAND_TEST_DROP_AFTER_SEND when set.
	DropAfterSend string
	// Logger receives the daemon log; nil logs to stderr.
	Logger *log.Logger
}

// Daemon is a running daemon.
type Daemon struct {
	lock  *flock.Lock
	st    *store.Store
	mgr   *conn.Manager
	srv   *localapi.Server
	jobs  *jobs.Registry
	xfer  *xfer.Manager
	ptys  *pty.Hub
	tuns  *tunnel.Manager
	qa    *qa.Manager
	ln    net.Listener
	lg    *log.Logger
	Hosts *hosts.Registry
}

// ControllerIdentity loads the controller certificate of the home folder,
// creating it on first use with the common name
// deskhand-controller@<hostname> and a key of mode 0600.
func ControllerIdentity() (transport.Identity, error) {
	cert, key := paths.ControllerCert(), paths.ControllerKey()
	if _, err := os.Stat(cert); err == nil {
		id, err := transport.LoadIdentity(cert, key)
		if err != nil {
			return transport.Identity{}, fmt.Errorf("컨트롤러 인증서 %s를 읽지 못했습니다. 파일이 손상됐다면 지우고 모든 PC를 deskhand host repair로 다시 페어링하세요: %w", cert, err)
		}
		return id, nil
	}
	if err := os.MkdirAll(paths.Identity(), 0o700); err != nil {
		return transport.Identity{}, fmt.Errorf("인증서 폴더 %s를 만들지 못했습니다. 폴더 권한을 확인하세요: %w", paths.Identity(), err)
	}
	host, _ := os.Hostname()
	id, err := transport.NewIdentity("deskhand-controller@" + host)
	if err != nil {
		return transport.Identity{}, err
	}
	if err := id.Save(cert, key); err != nil {
		return transport.Identity{}, fmt.Errorf("컨트롤러 인증서를 저장하지 못했습니다. %s의 권한을 확인하세요: %w", paths.Identity(), err)
	}
	return id, nil
}

// New takes the daemon lock, opens the store, listens on the socket and
// starts connecting the stored hosts.
func New(opt Options) (*Daemon, error) {
	lg := opt.Logger
	if lg == nil {
		lg = log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)
	}
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	lock, err := flock.TryLock(paths.Lock())
	if errors.Is(err, flock.ErrLocked) {
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("데몬 잠금 파일 %s를 열지 못했습니다. 폴더 권한을 확인하세요: %w", paths.Lock(), err)
	}
	_ = lock.SetContent([]byte(strconv.Itoa(os.Getpid()) + "\n"))
	d := &Daemon{lock: lock, lg: lg}
	ok := false
	defer func() {
		if !ok {
			d.Close()
		}
	}()
	id, err := ControllerIdentity()
	if err != nil {
		return nil, err
	}
	if d.st, err = store.Open(paths.DB()); err != nil {
		return nil, err
	}
	if n, err := d.st.MarkSentUnknown(); err == nil && n > 0 {
		lg.Printf("%d requests were in flight when the daemon stopped; they are recorded unknown", n)
	}
	now := time.Now()
	if err := d.st.Prune(now.Add(-eventKeep), now.Add(-requestKeep)); err != nil {
		lg.Printf("prune the store: %v", err)
	}
	bus := events.NewBus(d.st, lg)
	drop := opt.DropAfterSend
	if drop == "" {
		drop = os.Getenv(DropAfterSendEnv)
	}
	if drop != "" {
		lg.Printf("test hook %s=%s is set", DropAfterSendEnv, drop)
	}
	controller, _ := os.Hostname()
	var reg *hosts.Registry
	var jreg *jobs.Registry
	var xmgr *xfer.Manager
	var hub *pty.Hub
	d.mgr = conn.NewManager(conn.Options{Identity: id, Controller: controller, Dial: opt.Dial, Requests: d.st,
		OnEvent: func(ev conn.Event) { reg.HandleConnEvent(ev) },
		OnReady: func(hostID string, _ uint64) {
			go jreg.Reconcile(hostID)
			go xmgr.HostReady(hostID)
			go hub.Refresh(context.Background(), hostID)
		},
		DropAfterSend: drop, Logger: lg})
	jreg = jobs.New(d.st, bus, d.mgr)
	d.jobs = jreg
	if n := jreg.SettleInterrupted(); n > 0 {
		lg.Printf("%d jobs were running without a remote job when the daemon stopped; they are recorded unknown", n)
	}
	jreg.ResumeTimeouts()
	reg = hosts.New(hosts.Options{Store: d.st, Bus: bus, Manager: d.mgr, Jobs: jreg, Identity: id, SSHExec: opt.SSHExec,
		AgentFiles: opt.AgentFiles, Version: opt.Version, Logger: lg})
	d.Hosts = reg
	xmgr = xfer.New(xfer.Options{Store: d.st, Bus: bus, Conns: d.mgr, Jobs: jreg, Logger: lg})
	d.xfer = xmgr
	hub = pty.New(pty.Options{Store: d.st, Conns: d.mgr, Jobs: jreg, Logger: lg})
	d.ptys = hub
	d.tuns = tunnel.New(lg)
	d.qa = qa.New(qa.Options{Store: d.st, Bus: bus, Jobs: jreg, Hosts: qa.HostsFrom(reg), Push: qa.XferPusher(xmgr), Version: opt.Version, Logger: lg})
	if n := d.qa.Load(); n > 0 {
		lg.Printf("%d QA runs were running when the daemon stopped; they are recorded as interrupted", n)
	}
	if d.ln, err = localapi.Listen(paths.Socket()); err != nil {
		return nil, err
	}
	d.srv = localapi.New(localapi.Deps{Store: d.st, Bus: bus, Hosts: reg, Jobs: jreg, Transfers: xmgr, PTYs: hub, Tunnels: d.tuns, QA: d.qa, Version: opt.Version, Logger: lg})
	if err := reg.Load(); err != nil {
		return nil, fmt.Errorf("저장된 PC 목록을 읽지 못했습니다. %s를 확인하세요: %w", paths.DB(), err)
	}
	if n := xmgr.Load(); n > 0 {
		lg.Printf("%d transfers were unfinished when the daemon stopped; they go on once their PC is connected", n)
	}
	ok = true
	lg.Printf("deskhand daemon %s listening on %s", opt.Version, paths.Socket())
	return d, nil
}

// Serve answers the local API until ctx ends, then closes the daemon.
func (d *Daemon) Serve(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() { errc <- d.srv.Serve(d.ln) }()
	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	d.Close()
	return err
}

// Close stops the local API, the connections and the store, removes the
// socket and gives the lock up.
func (d *Daemon) Close() {
	if d.srv != nil {
		d.srv.Close()
		d.srv = nil
	}
	if d.ln != nil {
		d.ln.Close()
		_ = os.Remove(paths.Socket())
		d.ln = nil
	}
	if d.tuns != nil {
		d.tuns.Shutdown()
		d.tuns = nil
	}
	if d.qa != nil {
		d.qa.Close()
		d.qa = nil
	}
	if d.xfer != nil {
		d.xfer.Close()
		d.xfer = nil
	}
	if d.jobs != nil {
		d.jobs.Close()
		d.jobs = nil
	}
	if d.mgr != nil {
		d.mgr.Close()
		d.mgr = nil
	}
	if d.st != nil {
		d.st.Close()
		d.st = nil
	}
	if d.lock != nil {
		d.lock.Release()
		d.lock = nil
	}
}

// Run runs a daemon until ctx ends.
func Run(ctx context.Context, opt Options) error {
	d, err := New(opt)
	if err != nil {
		return err
	}
	return d.Serve(ctx)
}
