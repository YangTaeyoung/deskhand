// Package observe gives AI clients the hosts' screens and input
// (docs/local-api.md §3 관찰·입력): still captures with a capture_id whose
// coordinates map to display pixels, mouse and keyboard input under
// control arbitration with honest outcomes, and observations that wait
// for the screen to settle.
package observe

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/control"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Agent is the connection to a host's agent.
type Agent interface {
	Call(ctx context.Context, op string, params proto.Message, opts conn.CallOpts) (proto.Message, error)
	State() conn.ConnState
	Generation() uint64
}

// Agents finds the agent connection of a host.
type Agents interface {
	// Agent returns the connection of hostID, or nil when there is none.
	Agent(hostID string) Agent
}

// AgentsFrom returns the Agents of the daemon's connection manager.
func AgentsFrom(m *conn.Manager) Agents { return connAgents{m: m} }

type connAgents struct{ m *conn.Manager }

func (c connAgents) Agent(hostID string) Agent {
	if h := c.m.Get(hostID); h != nil {
		return h
	}
	return nil
}

// Control sends input under control arbitration (control.Manager.Send).
type Control interface {
	Send(ctx context.Context, hostID string, c model.Caller, evs []wire.InputEvent, opt control.SendOpts) ([]wire.InputAck, error)
}

const (
	// DefaultCaptureTTL is how long a capture_id's coordinates stay usable.
	DefaultCaptureTTL = 10 * time.Minute
	// defaultFileKeep is how long the capture images stay on disk.
	defaultFileKeep = 24 * time.Hour
	// pruneEvery is the least time between two prunes of the capture
	// images.
	pruneEvery = time.Hour
	// defaultSettleInterval is the time between two still images of a
	// settle.
	defaultSettleInterval = 100 * time.Millisecond
)

// Options configure a Service.
type Options struct {
	Agents  Agents
	Control Control
	// Dir holds the capture images, one folder per host; "" means
	// <home>/artifacts/captures.
	Dir    string
	Logger *log.Logger
	// CaptureTTL is how long a capture_id's coordinates stay usable
	// (DefaultCaptureTTL when 0).
	CaptureTTL time.Duration
	// FileKeep is how long the capture images stay on disk (24 h when 0).
	FileKeep time.Duration
	// SettleInterval is the time between two still images of a settle
	// (100 ms when 0); the screen counts as settled once it has not changed
	// for two of them.
	SettleInterval time.Duration
}

// hostState is what the service follows of one host: the display epoch,
// which every display.changed raises, the count of the agent's window
// events with uiCh, closed and replaced on each, and the fingerprint of
// the screen seen last.
type hostState struct {
	epoch    uint64
	uiEvents uint64
	uiCh     chan struct{}
	last     *fingerprint
}

// Service serves the captures, input and observations of the hosts.
type Service struct {
	opt Options
	lg  *log.Logger

	mu       sync.Mutex
	captures map[string]*captureRec
	hosts    map[string]*hostState
	prunedAt time.Time
}

// New returns a Service.
func New(opt Options) *Service {
	lg := opt.Logger
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	if opt.Dir == "" {
		opt.Dir = filepath.Join(paths.Artifacts(), "captures")
	}
	if opt.CaptureTTL <= 0 {
		opt.CaptureTTL = DefaultCaptureTTL
	}
	if opt.FileKeep <= 0 {
		opt.FileKeep = defaultFileKeep
	}
	if opt.SettleInterval <= 0 {
		opt.SettleInterval = defaultSettleInterval
	}
	return &Service{opt: opt, lg: lg, captures: map[string]*captureRec{}, hosts: map[string]*hostState{}}
}

// hostLocked returns the state of hostID, adding it; s.mu is held.
func (s *Service) hostLocked(hostID string) *hostState {
	h := s.hosts[hostID]
	if h == nil {
		h = &hostState{uiCh: make(chan struct{})}
		s.hosts[hostID] = h
	}
	return h
}

// uiWindowEvents are the agent's window events (docs/protocol.md §3.1).
var uiWindowEvents = map[string]bool{"window.opened": true, "window.closed": true, "window.foreground": true, "window.title": true,
	"dialog.opened": true}

// HandleConnEvent follows the agent events of the connections: a
// display.changed makes the captures taken before it stale, and a window
// event counts as a change of the screen while it settles.
func (s *Service) HandleConnEvent(ev conn.Event) {
	if ev.Kind != conn.KindAgentEvent {
		return
	}
	name := ev.Payload.String("name")
	switch {
	case name == "display.changed":
		s.mu.Lock()
		s.hostLocked(ev.HostID).epoch++
		s.mu.Unlock()
	case uiWindowEvents[name]:
		s.mu.Lock()
		h := s.hostLocked(ev.HostID)
		h.uiEvents++
		close(h.uiCh)
		h.uiCh = make(chan struct{})
		s.mu.Unlock()
	}
}

// epoch returns the display epoch of hostID.
func (s *Service) epoch(hostID string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostLocked(hostID).epoch
}

// uiEvents returns the count of hostID's window events and the channel
// the next one closes.
func (s *Service) uiEvents(hostID string) (uint64, chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.hostLocked(hostID)
	return h.uiEvents, h.uiCh
}

// lastFP returns the fingerprint of the screen of hostID seen last.
func (s *Service) lastFP(hostID string) *fingerprint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostLocked(hostID).last
}

// setLastFP records fp as the screen of hostID seen last.
func (s *Service) setLastFP(hostID string, fp *fingerprint) {
	if fp == nil {
		return
	}
	s.mu.Lock()
	s.hostLocked(hostID).last = fp
	s.mu.Unlock()
}

func (s *Service) agent(hostID string) Agent {
	if s.opt.Agents == nil {
		return nil
	}
	return s.opt.Agents.Agent(hostID)
}
