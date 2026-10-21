package screen

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// agentVideo is an open video stream of the agent with its cursor stream.
type agentVideo struct {
	f       *feed
	agent   Agent
	st      conn.DataStream
	r       *bufio.Reader
	connGen uint64
	first   []byte

	wmu sync.Mutex

	mu       sync.Mutex
	target   quality
	applied  quality
	wantKey  bool
	lastKey  time.Time
	keyTimer *time.Timer
	cursorSt conn.DataStream
	ended    bool

	wake chan struct{}
	done chan struct{}
	once sync.Once
}

func newAgentVideo(f *feed, a Agent, st conn.DataStream, br *bufio.Reader, connGen uint64, q quality, first []byte) *agentVideo {
	return &agentVideo{f: f, agent: a, st: st, r: br, connGen: connGen, first: first, target: q, applied: q,
		wake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (s *agentVideo) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// wantKeyframe asks for a keyframe, within keyframeGap of the last one
// once it has passed.
func (s *agentVideo) wantKeyframe() {
	s.mu.Lock()
	s.wantKey = true
	s.mu.Unlock()
	s.signal()
}

// tune asks for the quality q.
func (s *agentVideo) tune(q quality) {
	s.mu.Lock()
	changed := s.target != q
	s.target = q
	s.mu.Unlock()
	if changed {
		s.signal()
	}
}

// setCursor records the cursor stream cs and reports whether st is still
// open.
func (s *agentVideo) setCursor(cs conn.DataStream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return false
	}
	s.cursorSt = cs
	return true
}

// steer writes the control frames to the agent until the stream ends: the
// quality asked for, keyframe requests (one every KeyframeEvery too) and
// the latency notices of the feed every LatencyEvery.
func (s *agentVideo) steer() {
	h := s.f.h
	keys := time.NewTicker(h.opt.KeyframeEvery)
	defer keys.Stop()
	lat := time.NewTicker(h.opt.LatencyEvery)
	defer lat.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-keys.C:
			s.mu.Lock()
			s.wantKey = true
			s.mu.Unlock()
		case <-lat.C:
			s.f.latencyNotice(s)
			continue
		case <-s.wake:
		}
		if err := s.flush(); err != nil {
			if !s.isEnded() {
				h.lg.Printf("screen: %s display %d: write a control frame: %v", s.f.key.hostID, s.f.key.display, err)
			}
			s.abort()
			return
		}
	}
}

// flush writes the quality asked for when it changed and a keyframe
// request when one is due.
func (s *agentVideo) flush() error {
	s.mu.Lock()
	target, applied := s.target, s.applied
	s.applied = target
	key := false
	if s.wantKey {
		if wait := keyframeGap - time.Since(s.lastKey); wait > 0 {
			if s.keyTimer == nil {
				s.keyTimer = time.AfterFunc(wait, func() {
					s.mu.Lock()
					s.keyTimer = nil
					s.mu.Unlock()
					s.signal()
				})
			}
		} else {
			key, s.wantKey, s.lastKey = true, false, time.Now()
		}
	}
	s.mu.Unlock()
	if target.bitrate != applied.bitrate {
		if err := s.control(wire.VideoControl{Op: wire.ControlBitrate, BPS: target.bitrate}); err != nil {
			return err
		}
	}
	if target.maxWidth != applied.maxWidth || target.maxFPS != applied.maxFPS {
		if err := s.control(wire.VideoControl{Op: wire.ControlConfig, MaxWidth: target.maxWidth, MaxFPS: target.maxFPS}); err != nil {
			return err
		}
	}
	if key {
		return s.control(wire.VideoControl{Op: wire.ControlKeyframe})
	}
	return nil
}

func (s *agentVideo) control(c wire.VideoControl) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = s.st.SetWriteDeadline(time.Now().Add(controlTimeout))
	return wire.WriteFrame(s.st, wire.KindClientControl, b)
}

func (s *agentVideo) isEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

// finish marks the stream ended and returns its cursor stream.
func (s *agentVideo) finish() conn.DataStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
	if s.keyTimer != nil {
		s.keyTimer.Stop()
		s.keyTimer = nil
	}
	cs := s.cursorSt
	s.cursorSt = nil
	return cs
}

// close ends the daemon's direction of the video and cursor streams; the
// agent then stops them. Both are reset when the agent has not ended them
// within endWait.
func (s *agentVideo) close() {
	s.once.Do(func() {
		close(s.done)
		cs := s.finish()
		s.wmu.Lock()
		_ = s.st.CloseWrite()
		s.wmu.Unlock()
		if cs != nil {
			_ = cs.CloseWrite()
		}
		time.AfterFunc(endWait, func() {
			s.st.CancelRead()
			s.st.CancelWrite()
			if cs != nil {
				cs.CancelRead()
				cs.CancelWrite()
			}
		})
	})
}

// abort resets the video and cursor streams.
func (s *agentVideo) abort() {
	s.once.Do(func() {
		close(s.done)
		cs := s.finish()
		s.st.CancelRead()
		s.st.CancelWrite()
		if cs != nil {
			cs.CancelRead()
			cs.CancelWrite()
		}
	})
}

// readFrame reads one frame of r with its length prefix, of at most
// wire.MaxFrame bytes after it.
func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return nil, wire.ErrEmptyFrame
	}
	if n > wire.MaxFrame {
		return nil, wire.ErrFrameTooLarge
	}
	b := make([]byte, 4+int(n))
	copy(b, hdr[:])
	if _, err := io.ReadFull(r, b[4:]); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return b, nil
}

// encodeFrame returns the frame of kind and payload with its length
// prefix.
func encodeFrame(kind byte, payload []byte) []byte {
	b := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(b, uint32(1+len(payload)))
	b[4] = kind
	copy(b[5:], payload)
	return b
}
