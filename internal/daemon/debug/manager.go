package debug

import (
	"context"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Agent is a host connection the debugger reaches: it opens tunnels and
// calls the agent's ops. *conn.Host is one.
type Agent interface {
	tunnel.Host
	Call(ctx context.Context, op string, params proto.Message, opts conn.CallOpts) (proto.Message, error)
}

// HostInfo is the resolved host a session runs on.
type HostInfo struct {
	HostID string
	Name   string
}

// Hosts resolves the host of a session and its connection.
type Hosts interface {
	// Conn returns the agent connection of ref and its resolved host.
	Conn(ref string) (Agent, HostInfo, error)
	// SessionID returns the envelope session id of a host, or "".
	SessionID(hostID string) string
}

// Timeouts bound the daemon's waits; zero fields take the defaults.
type Timeouts struct {
	// Probe bounds the wait for an inspector port to answer.
	Probe time.Duration
	// Poll is the wait between two probes.
	Poll time.Duration
	// Call bounds one agent op the launch sends.
	Call time.Duration
	// Evaluate bounds one debug.evaluate before terminateExecution.
	Evaluate time.Duration
	// Retain is how long a closed session stays in the list.
	Retain time.Duration
}

func (t Timeouts) withDefaults() Timeouts {
	if t.Probe == 0 {
		t.Probe = 20 * time.Second
	}
	if t.Poll == 0 {
		t.Poll = 300 * time.Millisecond
	}
	if t.Call == 0 {
		t.Call = 30 * time.Second
	}
	if t.Evaluate == 0 {
		t.Evaluate = 30 * time.Second
	}
	if t.Retain == 0 {
		t.Retain = 10 * time.Minute
	}
	return t
}

// Deps are what a Manager uses.
type Deps struct {
	Tunnels  *tunnel.Manager
	Hosts    Hosts
	Bus      *events.Bus
	Jobs     *jobs.Registry
	Version  string
	Logger   *log.Logger
	Timeouts Timeouts
	// Dial opens a CDP connection to a tunnel's local address; nil dials
	// the real WebSocket. Tests inject it.
	Dial func(ctx context.Context, addr, path string, handler func(cdpEvent)) (*cdpConn, error)
}

// Manager owns the debug sessions.
type Manager struct {
	d    Deps
	lg   *log.Logger
	tmo  Timeouts
	dial func(ctx context.Context, addr, path string, handler func(cdpEvent)) (*cdpConn, error)

	base context.Context
	stop context.CancelFunc

	mu           sync.Mutex
	sessions     map[string]*session
	pendingKills map[string][]pendingKill
	closed       bool
	wg           sync.WaitGroup
}

// New returns a Manager.
func New(d Deps) *Manager {
	if d.Logger == nil {
		d.Logger = log.New(io.Discard, "", 0)
	}
	dial := d.Dial
	if dial == nil {
		dial = dialCDP
	}
	m := &Manager{d: d, lg: d.Logger, tmo: d.Timeouts.withDefaults(), dial: dial, sessions: map[string]*session{}}
	m.base, m.stop = context.WithCancel(context.Background())
	return m
}

// Shutdown ends every session's connections without recording a close.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.closed = true
	live := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		live = append(live, s)
	}
	m.mu.Unlock()
	m.stop()
	for _, s := range live {
		s.shutdown()
	}
	m.wg.Wait()
}

// get returns the session debugID, open or recently closed.
func (m *Manager) get(debugID string) (*session, error) {
	m.mu.Lock()
	s := m.sessions[debugID]
	m.mu.Unlock()
	if s == nil {
		return nil, notFound(debugID)
	}
	return s, nil
}

// live returns an open session; a closed one is NOT_FOUND for the methods
// that change it.
func (m *Manager) live(debugID string) (*session, error) {
	s, err := m.get(debugID)
	if err != nil {
		return nil, err
	}
	if s.snapshot().State == StateClosed {
		return nil, notFound(debugID)
	}
	return s, nil
}

func notFound(debugID string) *apierr.Error {
	return apierr.NotFound(fmt.Sprintf("디버그 세션 %s가 없습니다. debug.list로 debug_id를 확인하세요", debugID))
}

// List returns the sessions of hostID ("" for every host), in creation
// order, dropping those closed longer than the retention.
func (m *Manager) List(hostID string) []Session {
	m.sweep()
	m.mu.Lock()
	var live []*session
	for _, s := range m.sessions {
		if hostID == "" || s.hostID == hostID {
			live = append(live, s)
		}
	}
	m.mu.Unlock()
	out := make([]Session, 0, len(live))
	for _, s := range live {
		out = append(out, s.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DebugID < out[j].DebugID })
	return out
}

// Meta returns a session's host id and job id.
func (m *Manager) Meta(debugID string) (hostID, jobID string, ok bool) {
	s, err := m.get(debugID)
	if err != nil {
		return "", "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostID, s.jobID, true
}

// Get returns the session debugID.
func (m *Manager) Get(debugID string) (Session, error) {
	s, err := m.get(debugID)
	if err != nil {
		return Session{}, err
	}
	return s.snapshot(), nil
}

// sweep drops sessions closed longer than the retention.
func (m *Manager) sweep() {
	now := time.Now()
	m.mu.Lock()
	for id, s := range m.sessions {
		if t := s.closedTime(); !t.IsZero() && now.Sub(t) > m.tmo.Retain {
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
}

// HostDropped closes every open session of hostID with connection_lost and,
// for launch sessions, ends the launched process tree once the host is back.
func (m *Manager) HostDropped(hostID string) {
	m.mu.Lock()
	var live []*session
	for _, s := range m.sessions {
		if s.hostID == hostID {
			live = append(live, s)
		}
	}
	m.mu.Unlock()
	for _, s := range live {
		s.connectionLost()
	}
}

// LaunchParams are the parameters of debug.electron.launch.
type LaunchParams struct {
	HostRef           string
	Path              string
	Args              []string
	Cwd               string
	PauseOnStart      bool
	Breakpoints       []BreakpointSpec
	PauseOnExceptions string
}

// AttachParams are the parameters of debug.attach.
type AttachParams struct {
	HostRef           string
	Port              int
	PID               int
	Role              string
	Breakpoints       []BreakpointSpec
	PauseOnExceptions string
}

// newSession stores a session shell for the host.
func (m *Manager) newSession(kind string, host HostInfo, agent Agent, c model.Caller) (*session, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, apierr.New(wire.CodeOffline, "데몬이 끝나는 중입니다. 데몬이 다시 시작한 뒤 시도하세요", true, apierr.NextRetry)
	}
	id := ids.New(debugPrefix)
	s := newSessionShell(m, id, kind, host, agent, c)
	m.sessions[id] = s
	m.mu.Unlock()
	return s, nil
}

func exceptionsMode(mode string) (string, error) {
	switch mode {
	case "", exceptionsDefault:
		return exceptionsDefault, nil
	case "none", "all":
		return mode, nil
	}
	return "", apierr.Invalid("pause_on_exceptions는 none, uncaught, all 중 하나여야 합니다")
}

func validSpecs(specs []BreakpointSpec) error {
	for _, sp := range specs {
		if err := validSpec(sp); err != nil {
			return err
		}
	}
	return nil
}

func validSpec(sp BreakpointSpec) error {
	switch sp.Match {
	case matchFileName, matchURL, matchURLRegex:
	default:
		return apierr.Invalid("match는 file_name, url, url_regex 중 하나여야 합니다")
	}
	if strings.TrimSpace(sp.Pattern) == "" {
		return apierr.Invalid("pattern이 비었습니다. 중단점을 걸 스크립트 경로나 URL을 주세요")
	}
	if sp.Line < 1 {
		return apierr.Invalid("line은 1 이상이어야 합니다")
	}
	return nil
}

// record writes a timeline entry for a session action.
func (m *Manager) record(s *session, c model.Caller, summary, outcome string) {
	if m.d.Bus == nil {
		return
	}
	_, _ = m.d.Bus.Record(c.Entry(s.hostID, timelineKind, summary, outcome, s.jobID))
}
