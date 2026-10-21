package observe

import (
	"context"
	"fmt"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Observation levels.
const (
	LevelNone    = "none"
	LevelSummary = "summary"
	LevelImage   = "image"
	LevelUI      = "ui"
)

const (
	// MaxSettle bounds settle_ms.
	MaxSettle = 30 * time.Second
	// uiTreeDepth and uiTreeMax shape the tree of level ui.
	uiTreeDepth = 12
	uiTreeMax   = 300
	// uiTimeout bounds each UI Automation read of an observation.
	uiTimeout = 20 * time.Second
)

// ObserveParams are the parameters of an observation: Level (summary when
// ""), how long it may wait for the screen to settle, and the display it
// watches and, for level image, captures.
type ObserveParams struct {
	Level    string
	Settle   time.Duration
	Display  int
	MaxWidth *int
	Format   string
}

// PartError is a part of an observation that could not be read.
type PartError struct {
	Part    string `json:"part"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Observation is what an observation saw: the foreground window and the
// focused element (levels summary, image and ui), whether the screen
// changed since the screen seen before (nil when either could not be
// seen), whether it settled (nil without a settle) and how long that
// took, the capture of level image and the UI tree of level ui.
type Observation struct {
	Level                string         `json:"level"`
	Foreground           map[string]any `json:"foreground"`
	Focused              map[string]any `json:"focused"`
	Changed              *bool          `json:"changed"`
	Settled              *bool          `json:"settled"`
	WaitedMS             int64          `json:"waited_ms"`
	Capture              *Capture       `json:"capture,omitempty"`
	UI                   map[string]any `json:"ui,omitempty"`
	Errors               []PartError    `json:"errors"`
	ConnectionGeneration uint64         `json:"connection_generation"`
}

// CheckLevel returns level, summary when "", or INVALID_ARGUMENT.
func CheckLevel(level string) (string, error) {
	switch level {
	case "":
		return LevelSummary, nil
	case LevelNone, LevelSummary, LevelImage, LevelUI:
		return level, nil
	}
	return "", apierr.Invalid(fmt.Sprintf("level %q를 모릅니다. none, summary, image, ui 중 하나로 주세요", level))
}

func (o *Observation) fail(part string, err error) {
	e := apierr.As(err)
	o.Errors = append(o.Errors, PartError{Part: part, Code: e.Code, Message: e.Message})
}

// Observe looks at hostID: it waits up to p.Settle for the screen to stop
// changing, then reads what p.Level asks for. changed compares the screen
// with baseline, or with the screen of hostID seen last when baseline is
// nil.
func (s *Service) Observe(ctx context.Context, hostID string, p ObserveParams, baseline *fingerprint) (Observation, error) {
	level, err := CheckLevel(p.Level)
	if err != nil {
		return Observation{}, err
	}
	if p.Settle < 0 || p.Settle > MaxSettle {
		return Observation{}, apierr.Invalid(fmt.Sprintf("settle_ms는 0–%d로 주세요", MaxSettle.Milliseconds()))
	}
	a := s.agent(hostID)
	if a == nil {
		return Observation{}, apierr.NotFound(fmt.Sprintf("PC %s가 등록돼 있지 않습니다. host.list로 PC를 확인하세요", hostID))
	}
	switch a.State() {
	case conn.StateReady, conn.StateDegraded, conn.StateConnecting:
	default:
		return Observation{}, apierr.New(wire.CodeOffline, "이 PC와 연결돼 있지 않습니다. deskhand host connect로 연결한 뒤 다시 관찰하세요", true, apierr.NextHostConnect)
	}
	if baseline == nil {
		baseline = s.lastFP(hostID)
	}
	o := Observation{Level: level, Errors: []PartError{}}
	start := time.Now()
	var final *fingerprint
	screenRead := false
	if p.Settle > 0 {
		fp, settled, err := s.settle(ctx, a, hostID, p.Display, p.Settle)
		o.Settled = &settled
		final, screenRead = fp, true
		if err != nil {
			o.fail("screen", err)
		}
	}
	o.WaitedMS = time.Since(start).Milliseconds()
	if level == LevelImage {
		c, err := s.Capture(ctx, hostID, CaptureParams{Display: p.Display, MaxWidth: p.MaxWidth, Format: p.Format})
		if err != nil {
			o.fail("capture", err)
		} else {
			o.Capture = &c
			if c.rec.fp != nil {
				final = c.rec.fp
			}
		}
		screenRead = true
	}
	if !screenRead {
		fp, err := s.thumb(ctx, a, p.Display)
		if err != nil {
			o.fail("screen", err)
		}
		final = fp
	}
	if baseline != nil && final != nil {
		changed := final.differs(baseline)
		o.Changed = &changed
	}
	s.setLastFP(hostID, final)
	if level != LevelNone {
		s.readUI(ctx, a, level, &o)
	}
	o.ConnectionGeneration = a.Generation()
	return o, nil
}

// readUI reads the foreground window and the focused element into o, and
// for level ui the tree of the foreground window.
func (s *Service) readUI(ctx context.Context, a Agent, level string, o *Observation) {
	call := func(op string, params proto.Message) (proto.Message, error) {
		cctx, cancel := context.WithTimeout(ctx, uiTimeout)
		defer cancel()
		return a.Call(cctx, op, params, conn.CallOpts{})
	}
	if res, err := call("win.list", proto.Message{}); err != nil {
		o.fail("foreground", err)
	} else {
		list, _ := res["windows"].([]any)
		for _, item := range list {
			if w, ok := item.(map[string]any); ok && w["foreground"] == true {
				o.Foreground = w
				break
			}
		}
	}
	if res, err := call("ui.focused", proto.Message{}); err != nil {
		o.fail("focused", err)
	} else if node, ok := res["node"].(map[string]any); ok {
		o.Focused = node
	}
	if level != LevelUI {
		return
	}
	window := "foreground"
	if id, ok := o.Foreground["id"].(string); ok && id != "" {
		window = id
	}
	if res, err := call("ui.tree", proto.Message{"window": window, "filter": "interactive", "depth": uiTreeDepth, "max": uiTreeMax}); err != nil {
		o.fail("ui", err)
	} else {
		o.UI = res
	}
}

// settle takes still images of display every SettleInterval until two
// intervals pass without a change of the screen or a window event, or d
// ends. It returns the last image's fingerprint and whether the screen
// settled. When the screen cannot be captured it waits for two intervals
// without a window event instead, and returns the capture's error.
func (s *Service) settle(ctx context.Context, a Agent, hostID string, display int, d time.Duration) (*fingerprint, bool, error) {
	interval := s.opt.SettleInterval
	quiet := 2 * interval
	deadline := time.Now().Add(d)
	var prev *fingerprint
	var stableFrom time.Time
	prevEv, _ := s.uiEvents(hostID)
	for {
		fp, err := s.thumb(ctx, a, display)
		if err != nil {
			return prev, s.settleOnEvents(ctx, hostID, deadline, quiet), err
		}
		ev, _ := s.uiEvents(hostID)
		now := time.Now()
		if prev == nil || fp.differs(prev) || ev != prevEv {
			stableFrom = now
		} else if now.Sub(stableFrom) >= quiet {
			return fp, true, nil
		}
		prev, prevEv = fp, ev
		if now.Add(interval).After(deadline) {
			return fp, false, nil
		}
		select {
		case <-ctx.Done():
			return fp, false, nil
		case <-time.After(interval):
		}
	}
}

// settleOnEvents waits until quiet passes without a window event of
// hostID, until deadline, and reports whether it did.
func (s *Service) settleOnEvents(ctx context.Context, hostID string, deadline time.Time, quiet time.Duration) bool {
	last := time.Now()
	for {
		_, ch := s.uiEvents(hostID)
		wait := min(time.Until(last.Add(quiet)), time.Until(deadline))
		if wait <= 0 {
			return time.Since(last) >= quiet
		}
		select {
		case <-ctx.Done():
			return false
		case <-ch:
			last = time.Now()
		case <-time.After(wait):
		}
	}
}
