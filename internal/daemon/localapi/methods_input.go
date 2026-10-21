package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/observe"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// registerInput adds screen.capture, the input methods and observe
// (docs/local-api.md §3 관찰·입력).
func (s *Server) registerInput() {
	if s.d.Observe == nil {
		return
	}
	s.methods["screen.capture"] = s.screenCapture
	s.methods["observe"] = s.observe
	s.methods["input.click"] = s.inputClick
	s.methods["input.move"] = s.inputMove
	s.methods["input.drag"] = s.inputDrag
	s.methods["input.scroll"] = s.inputScroll
	s.methods["input.key"] = s.inputKey
	s.methods["input.type"] = s.inputType
}

type captureParams struct {
	HostID   string `json:"host_id"`
	Display  int    `json:"display"`
	MaxWidth *int   `json:"max_width"`
	Format   string `json:"format"`
	Quality  *int   `json:"quality"`
}

// observeHost decodes params into v and resolves host_id (field of v
// read by id) to its stored host.
func (s *Server) observeHost(raw json.RawMessage, v any, id func() string) (string, error) {
	if err := decode(raw, v); err != nil {
		return "", err
	}
	if err := hostRequired(id()); err != nil {
		return "", &paramError{e: apierr.As(err)}
	}
	_, row, err := s.d.Hosts.Conn(id())
	if err != nil {
		return "", err
	}
	return row.HostID, nil
}

// asParam returns err as a params error when it is INVALID_ARGUMENT.
func asParam(err error) error {
	if apierr.CodeOf(err) == wire.CodeInvalidArgument {
		return &paramError{e: apierr.As(err)}
	}
	return err
}

func (s *Server) screenCapture(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p captureParams
	hostID, err := s.observeHost(raw, &p, func() string { return p.HostID })
	if err != nil {
		return nil, err
	}
	c, err := s.d.Observe.Capture(ctx, hostID, observe.CaptureParams{Display: p.Display, MaxWidth: p.MaxWidth, Format: p.Format, Quality: p.Quality})
	if err != nil {
		_, _ = s.d.Bus.Record(sess.Caller().Entry(hostID, model.TimelineObserve, fmt.Sprintf("화면 캡처: 디스플레이 %d", p.Display), model.TimelineFailed, ""))
		return nil, asParam(err)
	}
	entry := sess.Caller().Entry(hostID, model.TimelineObserve,
		fmt.Sprintf("화면 캡처 %s: 디스플레이 %d, %d×%d", c.CaptureID, c.Display, c.Width, c.Height), model.TimelineOK, "")
	if b, err := json.Marshal([]model.Artifact{c.Artifact}); err == nil {
		entry.Artifacts = b
	}
	_, _ = s.d.Bus.Record(entry)
	return c, nil
}

// observeParam is the observe parameter of an input method: the
// observation to make once the input ran.
type observeParam struct {
	Level    string `json:"level"`
	SettleMS int    `json:"settle_ms"`
	Display  *int   `json:"display"`
}

type observeParams struct {
	HostID   string `json:"host_id"`
	Level    string `json:"level"`
	SettleMS int    `json:"settle_ms"`
	Display  int    `json:"display"`
	MaxWidth *int   `json:"max_width"`
	Format   string `json:"format"`
}

func (s *Server) observe(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p observeParams
	hostID, err := s.observeHost(raw, &p, func() string { return p.HostID })
	if err != nil {
		return nil, err
	}
	o, err := s.d.Observe.Observe(ctx, hostID, observe.ObserveParams{Level: p.Level, Settle: time.Duration(p.SettleMS) * time.Millisecond,
		Display: p.Display, MaxWidth: p.MaxWidth, Format: p.Format}, nil)
	if err != nil {
		return nil, asParam(err)
	}
	summary := fmt.Sprintf("관찰(%s)", o.Level)
	if o.Settled != nil {
		summary = fmt.Sprintf("관찰(%s, %d ms 기다림)", o.Level, o.WaitedMS)
	}
	entry := sess.Caller().Entry(hostID, model.TimelineObserve, summary, model.TimelineOK, "")
	if o.Capture != nil {
		if b, err := json.Marshal([]model.Artifact{o.Capture.Artifact}); err == nil {
			entry.Artifacts = b
		}
	}
	_, _ = s.d.Bus.Record(entry)
	return map[string]any{"host_id": hostID, "level": o.Level, "foreground": o.Foreground, "focused": o.Focused, "changed": o.Changed,
		"settled": o.Settled, "waited_ms": o.WaitedMS, "capture": o.Capture, "ui": o.UI, "errors": o.Errors,
		"connection_generation": o.ConnectionGeneration}, nil
}

// pointParams are the params of an input at a capture coordinate.
type pointParams struct {
	HostID    string        `json:"host_id"`
	CaptureID string        `json:"capture_id"`
	X         *float64      `json:"x"`
	Y         *float64      `json:"y"`
	Button    string        `json:"button"`
	Count     int           `json:"count"`
	DX        float64       `json:"dx"`
	DY        float64       `json:"dy"`
	ToX       *float64      `json:"to_x"`
	ToY       *float64      `json:"to_y"`
	Steps     int           `json:"steps"`
	Duration  int           `json:"duration_ms"`
	Observe   *observeParam `json:"observe"`
}

// at decodes the params of method and returns the host and the target of
// x, y. A capture whose coordinates no longer hold is the envelope of a
// refused input (refused non-nil), recorded in the timeline for the
// session's client; a point outside the image or missing params are
// params errors.
func (s *Server) at(sess *session, raw json.RawMessage, method string) (p pointParams, hostID string, t observe.Target, refused map[string]any, err error) {
	if hostID, err = s.observeHost(raw, &p, func() string { return p.HostID }); err != nil {
		return
	}
	if p.CaptureID == "" || p.X == nil || p.Y == nil {
		err = &paramError{e: apierr.Invalid(fmt.Sprintf("%s에는 capture_id와 그 이미지의 x, y가 필요합니다. screen.capture로 찍고 그 좌표를 주세요", method))}
		return
	}
	t, err = s.d.Observe.Locate(hostID, p.CaptureID, *p.X, *p.Y)
	if apierr.CodeOf(err) == wire.CodeStaleTarget {
		_, _ = s.d.Bus.Record(sess.Caller().Entry(hostID, model.TimelineInput, fmt.Sprintf("%s 거절: %s는 오래된 capture_id", method, p.CaptureID),
			model.TimelineFailed, ""))
		refused = s.refusedInput(hostID, err)
		err = nil
	}
	err = asParam(err)
	return
}

// refusedInput is the envelope of an input refused before it was sent.
func (s *Server) refusedInput(hostID string, err error) map[string]any {
	return s.envelope(hostID, "", conn.CallInfo{}, outcome{status: model.StatusFailed, action: model.NotStarted(), err: apierr.As(err)}, nil).
		Map(map[string]any{"events_total": 0, "events_done": 0, "observation": nil})
}

// inputEnvelope records r as a timeline entry of the session's client
// with summary and returns its envelope with fields; with obs, an
// observation follows an input that ran (observation, or
// observation_error when it could not be made).
func (s *Server) inputEnvelope(ctx context.Context, sess *session, hostID, summary string, r observe.Result, fields map[string]any,
	obs *observeParam, baseline string) map[string]any {
	outcomeTL := timelineOutcome(r.Status)
	if r.Err != nil && !r.Ran() && (r.Err.Code == wire.CodeControlHeldByHuman || r.Err.Code == wire.CodeControlPreempted) {
		outcomeTL = model.TimelineDenied
	}
	_, _ = s.d.Bus.Record(sess.Caller().Entry(hostID, model.TimelineInput, summary, outcomeTL, ""))
	if fields == nil {
		fields = map[string]any{}
	}
	fields["events_total"], fields["events_done"], fields["observation"] = r.Total, r.Done, nil
	if obs != nil && r.Ran() {
		display := 0
		if obs.Display != nil {
			display = *obs.Display
		} else if t, ok := fields["target"].(observe.Target); ok {
			display = t.Display
		}
		o, err := s.d.Observe.Observe(ctx, hostID, observe.ObserveParams{Level: obs.Level, Settle: time.Duration(obs.SettleMS) * time.Millisecond,
			Display: display}, s.d.Observe.Baseline(hostID, baseline))
		if err != nil {
			fields["observation_error"] = apierr.As(err)
		} else {
			fields["observation"] = o
		}
	}
	info := conn.CallInfo{Generation: r.ConnGen, Sent: r.ConnGen != 0}
	return s.envelope(hostID, "", info, outcome{status: r.Status, action: r.Action, err: r.Err}, nil).Map(fields)
}

// checkObserve returns the params error of an observe parameter.
func checkObserve(o *observeParam) error {
	if o == nil {
		return nil
	}
	if _, err := observe.CheckLevel(o.Level); err != nil {
		return asParam(err)
	}
	if o.SettleMS < 0 || time.Duration(o.SettleMS)*time.Millisecond > observe.MaxSettle {
		return &paramError{e: apierr.Invalid(fmt.Sprintf("observe.settle_ms는 0–%d로 주세요", observe.MaxSettle.Milliseconds()))}
	}
	return nil
}

func (s *Server) inputClick(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	p, hostID, t, refused, err := s.at(sess, raw, "input.click")
	if err != nil || refused != nil {
		return refused, err
	}
	if err := checkObserve(p.Observe); err != nil {
		return nil, err
	}
	evs, err := observe.ClickEvents(t, p.Button, p.Count)
	if err != nil {
		return nil, asParam(err)
	}
	r := s.d.Observe.SendWith(ctx, hostID, sess.Caller(), evs, p.CaptureID)
	summary := fmt.Sprintf("클릭(%s): 디스플레이 %d (%d, %d) · %s", buttonOf(p.Button), t.Display, t.X, t.Y, p.CaptureID)
	return s.inputEnvelope(ctx, sess, hostID, summary, r, map[string]any{"target": t}, p.Observe, p.CaptureID), nil
}

func buttonOf(b string) string {
	if b == "" {
		return "left"
	}
	return b
}

func (s *Server) inputMove(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	p, hostID, t, refused, err := s.at(sess, raw, "input.move")
	if err != nil || refused != nil {
		return refused, err
	}
	if err := checkObserve(p.Observe); err != nil {
		return nil, err
	}
	r := s.d.Observe.SendWith(ctx, hostID, sess.Caller(), observe.MoveEvents(t), p.CaptureID)
	summary := fmt.Sprintf("포인터 이동: 디스플레이 %d (%d, %d) · %s", t.Display, t.X, t.Y, p.CaptureID)
	return s.inputEnvelope(ctx, sess, hostID, summary, r, map[string]any{"target": t}, p.Observe, p.CaptureID), nil
}

func (s *Server) inputScroll(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	p, hostID, t, refused, err := s.at(sess, raw, "input.scroll")
	if err != nil || refused != nil {
		return refused, err
	}
	if err := checkObserve(p.Observe); err != nil {
		return nil, err
	}
	evs, err := observe.ScrollEvents(t, p.DX, p.DY)
	if err != nil {
		return nil, asParam(err)
	}
	r := s.d.Observe.SendWith(ctx, hostID, sess.Caller(), evs, p.CaptureID)
	summary := fmt.Sprintf("스크롤(%v, %v칸): 디스플레이 %d (%d, %d) · %s", p.DX, p.DY, t.Display, t.X, t.Y, p.CaptureID)
	return s.inputEnvelope(ctx, sess, hostID, summary, r, map[string]any{"target": t}, p.Observe, p.CaptureID), nil
}

func (s *Server) inputDrag(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	p, hostID, from, refused, err := s.at(sess, raw, "input.drag")
	if err != nil || refused != nil {
		return refused, err
	}
	if p.ToX == nil || p.ToY == nil {
		return nil, &paramError{e: apierr.Invalid("input.drag에는 끝점 to_x, to_y가 필요합니다. 같은 capture의 이미지 좌표로 주세요")}
	}
	if err := checkObserve(p.Observe); err != nil {
		return nil, err
	}
	to, err := s.d.Observe.Locate(hostID, p.CaptureID, *p.ToX, *p.ToY)
	if err != nil {
		return nil, asParam(err)
	}
	r, err := s.d.Observe.Drag(ctx, hostID, sess.Caller(), p.CaptureID, from, to,
		observe.DragParams{Button: p.Button, Steps: p.Steps, Duration: time.Duration(p.Duration) * time.Millisecond})
	if err != nil {
		return nil, asParam(err)
	}
	summary := fmt.Sprintf("드래그(%s): 디스플레이 %d (%d, %d) → (%d, %d) · %s", buttonOf(p.Button), from.Display, from.X, from.Y, to.X, to.Y, p.CaptureID)
	return s.inputEnvelope(ctx, sess, hostID, summary, r, map[string]any{"target": from, "to": to}, p.Observe, p.CaptureID), nil
}

type keyParams struct {
	HostID  string        `json:"host_id"`
	Keys    string        `json:"keys"`
	Text    string        `json:"text"`
	Method  string        `json:"method"`
	Observe *observeParam `json:"observe"`
}

func (s *Server) inputKey(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p keyParams
	hostID, err := s.observeHost(raw, &p, func() string { return p.HostID })
	if err != nil {
		return nil, err
	}
	if err := checkObserve(p.Observe); err != nil {
		return nil, err
	}
	evs, canon, err := observe.ParseKeys(p.Keys)
	if err != nil {
		return nil, asParam(err)
	}
	r := s.d.Observe.Send(ctx, hostID, sess.Caller(), evs, nil, false)
	return s.inputEnvelope(ctx, sess, hostID, "키 입력: "+canon, r, map[string]any{"keys": canon}, p.Observe, ""), nil
}

func (s *Server) inputType(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p keyParams
	hostID, err := s.observeHost(raw, &p, func() string { return p.HostID })
	if err != nil {
		return nil, err
	}
	if err := checkObserve(p.Observe); err != nil {
		return nil, err
	}
	evs, method, err := observe.TextEvents(p.Text, p.Method)
	if err != nil {
		return nil, asParam(err)
	}
	r := s.d.Observe.Send(ctx, hostID, sess.Caller(), evs, nil, false)
	chars := len([]rune(p.Text))
	summary := fmt.Sprintf("글자 입력(%s): %d자", method, chars)
	return s.inputEnvelope(ctx, sess, hostID, summary, r, map[string]any{"method": method, "chars": chars}, p.Observe, ""), nil
}
