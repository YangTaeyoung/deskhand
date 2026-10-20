// Package events stores the daemon's events and hands them to the local
// API subscribers (docs/local-api.md §6), and records the timeline.
package events

import (
	"encoding/json"
	"io"
	"log"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

// Event kinds.
const (
	ConnectionState  = "connection.state"
	HostAdded        = "host.added"
	HostUpdated      = "host.updated"
	HostRemoved      = "host.removed"
	InstallProgress  = "host.install.progress"
	CapsChanged      = "caps.changed"
	SessionChanged   = "session.changed"
	DisplayChanged   = "display.changed"
	TimelineAppended = "timeline.appended"
	JobUpdated       = "job.updated"
	TransferUpdated  = "transfer.updated"
	QAUpdated        = "qa.updated"
	EventsDropped    = "events.dropped"
)

// Event is one notification. Fields go into the notification next to seq,
// ts, kind and host_id.
type Event struct {
	Seq    int64
	TS     time.Time
	HostID string
	JobID  string
	Kind   string
	Fields map[string]any
}

// Params returns the params of the event notification: seq, ts, kind,
// host_id when set, and the fields.
func (e Event) Params() map[string]any {
	out := make(map[string]any, len(e.Fields)+4)
	for k, v := range e.Fields {
		out[k] = v
	}
	out["seq"], out["ts"], out["kind"] = e.Seq, model.Time(e.TS), e.Kind
	if e.HostID != "" {
		out["host_id"] = e.HostID
	}
	return out
}

// subBuffer is how many events wait for a slow subscriber before newer
// ones are dropped.
const subBuffer = 1024

// Sub receives the events published after it subscribed.
type Sub struct {
	C       chan Event
	bus     *Bus
	mu      sync.Mutex
	dropped int64
}

// TakeDropped returns and resets the count of events dropped because C was
// full.
func (s *Sub) TakeDropped() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.dropped
	s.dropped = 0
	return n
}

// Close stops the subscription.
func (s *Sub) Close() {
	s.bus.mu.Lock()
	delete(s.bus.subs, s)
	s.bus.mu.Unlock()
}

// Bus stores events and fans them out.
type Bus struct {
	st *store.Store
	lg *log.Logger

	mu   sync.Mutex
	subs map[*Sub]struct{}
}

// NewBus returns a Bus that stores events in st; lg may be nil.
func NewBus(st *store.Store, lg *log.Logger) *Bus {
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	return &Bus{st: st, lg: lg, subs: map[*Sub]struct{}{}}
}

// Subscribe returns a new subscription.
func (b *Bus) Subscribe() *Sub {
	s := &Sub{C: make(chan Event, subBuffer), bus: b}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Publish stores an event and hands it to every subscriber, in the order
// of its sequence number.
func (b *Bus) Publish(hostID, jobID, kind string, fields map[string]any) Event {
	return b.PublishStoring(hostID, jobID, kind, fields, fields)
}

// PublishStoring hands an event with fields to every subscriber like
// Publish and stores stored as its payload.
func (b *Bus) PublishStoring(hostID, jobID, kind string, fields, stored map[string]any) Event {
	ev := Event{TS: time.Now(), HostID: hostID, JobID: jobID, Kind: kind, Fields: fields}
	b.mu.Lock()
	defer b.mu.Unlock()
	payload, err := json.Marshal(stored)
	if err != nil {
		payload = []byte("{}")
	}
	seq, err := b.st.AppendEvent(ev.TS, hostID, jobID, kind, payload)
	if err != nil {
		b.lg.Printf("store event %s: %v", kind, err)
	}
	ev.Seq = seq
	for s := range b.subs {
		select {
		case s.C <- ev:
		default:
			s.mu.Lock()
			s.dropped++
			s.mu.Unlock()
		}
	}
	return ev
}

// Record stores a timeline entry and publishes timeline.appended.
func (b *Bus) Record(e store.Timeline) (model.TimelineEntry, error) {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	saved, err := b.st.AppendTimeline(e)
	if err != nil {
		b.lg.Printf("store timeline entry: %v", err)
		return model.TimelineEntry{}, err
	}
	entry := model.TimelineFrom(saved)
	b.Publish(e.HostID, e.JobID, TimelineAppended, map[string]any{"entry": entry})
	return entry, nil
}
