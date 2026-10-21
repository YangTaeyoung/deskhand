package observe

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/control"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// wheelDelta is one notch of a mouse wheel (WHEEL_DELTA).
	wheelDelta = 120
	// maxNotches bounds the notches of one input.scroll on each axis.
	maxNotches = 100
	// maxClicks bounds the count of one input.click.
	maxClicks = 3
	// defaultDragSteps and defaultDragDuration shape a drag without steps
	// and duration_ms; maxDragSteps and maxDragDuration bound them.
	defaultDragSteps    = 10
	defaultDragDuration = 300 * time.Millisecond
	maxDragSteps        = 100
	maxDragDuration     = 10 * time.Second
	// releaseTimeout bounds the release of a button a failed drag left
	// pressed.
	releaseTimeout = 5 * time.Second
)

// Result is what became of an input: the envelope's Status, Action and
// Error, how many of its Total events the agent acknowledged as injected
// (Done) and the generation of the connection it went out on (0 when it
// did not).
type Result struct {
	Status  string
	Action  model.Action
	Err     *apierr.Error
	Done    int
	Total   int
	ConnGen uint64
}

// Ran reports whether some of the input may have been injected.
func (r Result) Ran() bool { return r.Action.Outcome != model.OutcomeNotStarted }

// noTextInjected are the codes of a failed text event that the agent gives
// before it types anything.
var noTextInjected = map[string]bool{wire.CodeInvalidArgument: true, wire.CodeCapabilityUnsupported: true}

// classify returns the Result of evs from the acks that came back and the
// error of the send: succeeded only when every event was acknowledged as
// injected; unknown when the stream ended before an ack; partial when some
// events were injected and others were not, or a failed text may have
// typed a part; not_started when none was.
func classify(evs []wire.InputEvent, acks []wire.InputAck, err error) Result {
	r := Result{Total: len(evs)}
	ok, failed := 0, -1
	for i, a := range acks {
		if a.OK {
			ok++
		} else if failed < 0 {
			failed = i
		}
	}
	for _, a := range acks {
		if !a.OK {
			break
		}
		r.Done++
	}
	if err == nil && ok == len(evs) {
		r.Status, r.Action = model.StatusSucceeded, model.Executed()
		return r
	}
	e := apierr.As(err)
	if e == nil {
		e = apierr.Internal("PC의 에이전트가 입력 일부를 확인하지 않았습니다. 화면을 다시 확인하세요")
	}
	r.Err = e
	switch {
	case e.Code == wire.CodeExecutionOutcomeUnknown:
		r.Status, r.Action = model.StatusUnknown, model.UnknownAction()
		return r
	case ok > 0:
		r.Status, r.Action = model.StatusFailed, model.PartialAction()
	case failed >= 0 && evs[failed].K == "text" && !noTextInjected[e.Code]:
		r.Status, r.Action = model.StatusFailed, model.PartialAction()
	default:
		r.Status, r.Action = model.StatusFailed, model.NotStarted()
		return r
	}
	if r.Err.NextAction != apierr.NextCheckState {
		r.Err = apierr.New(e.Code, e.Message, false, apierr.NextCheckState)
	}
	return r
}

// Send sends evs as the input of client c on hostID (control.Send, as AI
// input whatever c's actor when asAI) and returns what became of it. With
// rec, the input goes out only while rec's coordinates hold on the
// connection of the input stream.
func (s *Service) Send(ctx context.Context, hostID string, c model.Caller, evs []wire.InputEvent, rec *captureRec, asAI bool) Result {
	if s.opt.Control == nil {
		return Result{Status: model.StatusFailed, Action: model.NotStarted(), Total: len(evs),
			Err: apierr.New(wire.CodeCapabilityUnsupported, "이 데몬은 입력을 보낼 수 없습니다. deskhand를 업데이트하세요", false, "")}
	}
	var connGen uint64
	acks, err := s.opt.Control.Send(ctx, hostID, c, evs, control.SendOpts{AsAI: asAI, Check: func(cg uint64) error {
		connGen = cg
		if rec != nil {
			return s.valid(rec, cg)
		}
		return nil
	}})
	r := classify(evs, acks, err)
	if r.Ran() {
		r.ConnGen = connGen
	}
	return r
}

// SendWith is Send with the record of capture captureID, which Locate
// accepted.
func (s *Service) SendWith(ctx context.Context, hostID string, c model.Caller, evs []wire.InputEvent, captureID string) Result {
	s.mu.Lock()
	rec := s.captures[captureID]
	s.mu.Unlock()
	if rec == nil {
		return Result{Status: model.StatusFailed, Action: model.NotStarted(), Total: len(evs),
			Err: staleCapture("capture_id %s를 모릅니다. screen.capture로 다시 캡처한 뒤 새 capture_id의 좌표로 다시 시도하세요", captureID)}
	}
	return s.Send(ctx, hostID, c, evs, rec, false)
}

// Baseline returns the fingerprint of capture captureID, nil when it is
// not known.
func (s *Service) Baseline(captureID string) *fingerprint {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec := s.captures[captureID]; rec != nil {
		return rec.fp
	}
	return nil
}

// buttons are the mouse buttons of input events.
var buttons = map[string]bool{"left": true, "right": true, "middle": true, "x1": true, "x2": true}

// CheckButton returns button, left when "", or INVALID_ARGUMENT.
func CheckButton(button string) (string, error) {
	if button == "" {
		return "left", nil
	}
	if !buttons[button] {
		return "", apierr.Invalid(fmt.Sprintf("button %q를 모릅니다. left, right, middle, x1, x2 중 하나로 주세요", button))
	}
	return button, nil
}

// move is the event that moves the pointer to t.
func move(t Target) wire.InputEvent {
	return wire.InputEvent{K: "move", Display: t.Display, X: t.X, Y: t.Y}
}

// ClickEvents moves the pointer to t and presses and releases button
// count times (1 when 0).
func ClickEvents(t Target, button string, count int) ([]wire.InputEvent, error) {
	b, err := CheckButton(button)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		count = 1
	}
	if count < 1 || count > maxClicks {
		return nil, apierr.Invalid(fmt.Sprintf("count %d는 쓸 수 없습니다. 1–%d번으로 주세요", count, maxClicks))
	}
	evs := []wire.InputEvent{move(t)}
	for range count {
		evs = append(evs, wire.InputEvent{K: "button", B: b, Down: true}, wire.InputEvent{K: "button", B: b})
	}
	return evs, nil
}

// MoveEvents moves the pointer to t.
func MoveEvents(t Target) []wire.InputEvent { return []wire.InputEvent{move(t)} }

// ScrollEvents moves the pointer to t and turns the wheel dx notches right
// and dy notches up (negative for left and down); a notch is 120 wheel
// units and fractions of a notch are kept.
func ScrollEvents(t Target, dx, dy float64) ([]wire.InputEvent, error) {
	if math.IsNaN(dx) || math.IsNaN(dy) || math.Abs(dx) > maxNotches || math.Abs(dy) > maxNotches {
		return nil, apierr.Invalid(fmt.Sprintf("dx·dy는 -%d–%d 칸 안의 수로 주세요", maxNotches, maxNotches))
	}
	wx, wy := int(math.Round(dx*wheelDelta)), int(math.Round(dy*wheelDelta))
	if wx == 0 && wy == 0 {
		return nil, apierr.Invalid("dx와 dy가 모두 0입니다. 위로는 dy 양수, 아래로는 dy 음수(칸 수)로 주세요")
	}
	return []wire.InputEvent{move(t), {K: "wheel", DX: wx, DY: wy}}, nil
}

// DragParams shape a drag: the button held, the moves between the ends
// (steps) and the time they take.
type DragParams struct {
	Button   string
	Steps    int
	Duration time.Duration
}

// Drag presses button at from, moves to to in Steps moves spread over
// Duration and releases it there, as the input of client c on hostID under
// capture captureID. A drag that fails after the press releases the
// button.
func (s *Service) Drag(ctx context.Context, hostID string, c model.Caller, captureID string, from, to Target, p DragParams) (Result, error) {
	b, err := CheckButton(p.Button)
	if err != nil {
		return Result{}, err
	}
	steps, dur := p.Steps, p.Duration
	if steps == 0 {
		steps = defaultDragSteps
	}
	if p.Duration == 0 {
		dur = defaultDragDuration
	}
	if steps < 1 || steps > maxDragSteps || dur < 0 || dur > maxDragDuration {
		return Result{}, apierr.Invalid(fmt.Sprintf("steps는 1–%d, duration_ms는 0–%d로 주세요", maxDragSteps, maxDragDuration.Milliseconds()))
	}
	s.mu.Lock()
	rec := s.captures[captureID]
	s.mu.Unlock()
	batches := [][]wire.InputEvent{{move(from), {K: "button", B: b, Down: true}}}
	for i := 1; i < steps; i++ {
		t := Target{Display: to.Display, X: from.X + (to.X-from.X)*i/steps, Y: from.Y + (to.Y-from.Y)*i/steps}
		batches = append(batches, []wire.InputEvent{move(t)})
	}
	batches = append(batches, []wire.InputEvent{move(to), {K: "button", B: b}})
	gap := dur / time.Duration(len(batches)-1)
	total := 0
	for _, bt := range batches {
		total += len(bt)
	}
	sum := Result{Total: total}
	pressed := false
	for i, bt := range batches {
		if i > 0 && gap > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(gap):
			}
		}
		r := s.Send(ctx, hostID, c, bt, rec, false)
		if r.ConnGen != 0 {
			sum.ConnGen = r.ConnGen
		}
		sum.Done += r.Done
		if i == 0 && r.Done == len(bt) {
			pressed = true
		}
		if r.Status == model.StatusSucceeded {
			continue
		}
		if pressed && r.Status == model.StatusFailed {
			s.release(hostID, c, b, rec)
		}
		sum.Status, sum.Err = r.Status, r.Err
		switch {
		case r.Status == model.StatusUnknown:
			sum.Action = model.UnknownAction()
		case sum.Done > 0:
			sum.Action = model.PartialAction()
			if sum.Err.NextAction != apierr.NextCheckState {
				sum.Err = apierr.New(sum.Err.Code, sum.Err.Message, false, apierr.NextCheckState)
			}
		default:
			sum.Action = r.Action
		}
		return sum, nil
	}
	sum.Status, sum.Action = model.StatusSucceeded, model.Executed()
	return sum, nil
}

// release lets go of button after a drag that failed with it pressed.
func (s *Service) release(hostID string, c model.Caller, button string, rec *captureRec) {
	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	if r := s.Send(ctx, hostID, c, []wire.InputEvent{{K: "button", B: button}}, rec, false); r.Status != model.StatusSucceeded {
		s.lg.Printf("observe: release the %s button of a failed drag on %s: %v", button, hostID, r.Err)
	}
}

// screenTarget returns the display of hostID that holds the virtual
// desktop point (x, y), read with display.list, as a target, and the
// generation of the connection the list came on.
func (s *Service) screenTarget(ctx context.Context, hostID string, x, y int) (Target, uint64, error) {
	a := s.agent(hostID)
	if a == nil {
		return Target{}, 0, apierr.NotFound(fmt.Sprintf("PC %s가 등록돼 있지 않습니다. host.list로 PC를 확인하세요", hostID))
	}
	info := conn.CallInfo{}
	res, err := a.Call(ctx, "display.list", proto.Message{}, conn.CallOpts{Info: &info})
	if err != nil {
		return Target{}, 0, err
	}
	list, _ := res["displays"].([]any)
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		d := proto.Message(m)
		dx, dy, w, h := d.Int("x"), d.Int("y"), d.Int("width"), d.Int("height")
		if x >= dx && x < dx+w && y >= dy && y < dy+h {
			return Target{Display: d.Int("index"), X: x - dx, Y: y - dy, ScreenX: x, ScreenY: y}, info.Generation, nil
		}
	}
	return Target{}, 0, apierr.Invalid(fmt.Sprintf("화면 좌표 (%d, %d)는 PC의 어느 디스플레이에도 없습니다. 창 위치를 다시 확인하세요", x, y))
}

// resultError is the error a QA step gets for r: nil when it succeeded.
func resultError(r Result) error {
	if r.Status == model.StatusSucceeded || r.Err == nil {
		return nil
	}
	return r.Err
}

// Click clicks button count times at the virtual desktop point (x, y) of
// hostID as AI input of client c, for a QA step (qa.Input).
func (s *Service) Click(ctx context.Context, hostID string, c model.Caller, x, y int, button string, count int) (map[string]any, error) {
	t, connGen, err := s.screenTarget(ctx, hostID, x, y)
	if err != nil {
		return nil, err
	}
	evs, err := ClickEvents(t, button, count)
	if err != nil {
		return nil, err
	}
	r := s.sendAt(ctx, hostID, c, evs, connGen)
	if err := resultError(r); err != nil {
		return nil, err
	}
	return map[string]any{"display": t.Display, "display_x": t.X, "display_y": t.Y}, nil
}

// sendAt sends evs as AI input of client c on hostID only on the
// connection connGen.
func (s *Service) sendAt(ctx context.Context, hostID string, c model.Caller, evs []wire.InputEvent, connGen uint64) Result {
	if s.opt.Control == nil {
		return Result{Status: model.StatusFailed, Action: model.NotStarted(), Total: len(evs),
			Err: apierr.New(wire.CodeCapabilityUnsupported, "이 데몬은 입력을 보낼 수 없습니다. deskhand를 업데이트하세요", false, "")}
	}
	acks, err := s.opt.Control.Send(ctx, hostID, c, evs, control.SendOpts{AsAI: true, Check: func(cg uint64) error {
		if cg != connGen {
			return apierr.New(wire.CodeStaleTarget, "디스플레이를 읽은 뒤 PC와 다시 연결됐습니다. 단계를 다시 실행하세요", true, apierr.NextRetry)
		}
		return nil
	}})
	return classify(evs, acks, err)
}

// Type types text with method (auto when "") on hostID as AI input of
// client c, for a QA step (qa.Input).
func (s *Service) Type(ctx context.Context, hostID string, c model.Caller, text, method string) (map[string]any, error) {
	evs, used, err := TextEvents(text, method)
	if err != nil {
		return nil, err
	}
	r := s.Send(ctx, hostID, c, evs, nil, true)
	if err := resultError(r); err != nil {
		return nil, err
	}
	return map[string]any{"method": used, "chars": agentChars(text)}, nil
}

// Key presses keys (ParseKeys) on hostID as AI input of client c, for a
// QA step (qa.Input).
func (s *Service) Key(ctx context.Context, hostID string, c model.Caller, keys string) (map[string]any, error) {
	evs, canon, err := ParseKeys(keys)
	if err != nil {
		return nil, err
	}
	r := s.Send(ctx, hostID, c, evs, nil, true)
	if err := resultError(r); err != nil {
		return nil, err
	}
	return map[string]any{"keys": canon}, nil
}
