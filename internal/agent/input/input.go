// Package input injects the mouse, keyboard and text input of
// docs/protocol.md §6 into the desktop of the agent's session, behind the
// writer generation that decides whose input is accepted.
package input

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Options configure an Injector.
type Options struct {
	Logger *log.Logger
	// SessionInfo describes the agent's session; nil means
	// session.Current. The injector reads it again when InputDesktop
	// differs from it and at least every sessionCacheFor.
	SessionInfo func() (session.Info, error)
	// InputDesktop names the desktop that takes input now, read before
	// each injection; nil means session.InputDesktop.
	InputDesktop func() string
	// Displays lists the displays of move events; nil means display.List.
	Displays func() ([]display.Info, error)
	// Preempted runs after the writer generation rose and the held input
	// was released.
	Preempted func(Preemption)
	// ClipboardWritten runs after a paste wrote the clipboard and after it
	// restored it.
	ClipboardWritten func()
}

// Preemption is a rise of the writer generation from Previous to Gen with
// the keys and buttons it released.
type Preemption struct {
	Gen, Previous uint64
	Released      []Pressed
}

// Error is an input failure with its docs/protocol.md §8 code.
type Error struct {
	Code, Msg string
}

func (e *Error) Error() string { return e.Msg }

func fail(code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// rect is a rectangle of physical pixels.
type rect struct{ x, y, w, h int }

// raw is one INPUT of SendInput: a mouse input (dx, dy, data, flags) or a
// keyboard input (vk, scan, flags).
type raw struct {
	mouse  bool
	dx, dy int32
	data   uint32
	vk     uint16
	scan   uint16
	flags  uint32
}

// INPUT flags of SendInput.
const (
	keyeventfExtendedKey = 0x0001
	keyeventfKeyUp       = 0x0002
	keyeventfUnicode     = 0x0004
	keyeventfScancode    = 0x0008

	mouseeventfMove        = 0x0001
	mouseeventfLeftDown    = 0x0002
	mouseeventfLeftUp      = 0x0004
	mouseeventfRightDown   = 0x0008
	mouseeventfRightUp     = 0x0010
	mouseeventfMiddleDown  = 0x0020
	mouseeventfMiddleUp    = 0x0040
	mouseeventfXDown       = 0x0080
	mouseeventfXUp         = 0x0100
	mouseeventfWheel       = 0x0800
	mouseeventfHWheel      = 0x1000
	mouseeventfVirtualDesk = 0x4000
	mouseeventfAbsolute    = 0x8000

	xbutton1 = 1
	xbutton2 = 2
)

// Virtual keys and set 1 scan codes the injector sends on its own.
const (
	vkTab      = 0x09
	vkReturn   = 0x0D
	vkControl  = 0x11
	vkV        = 0x56
	scanTab    = 0x0F
	scanReturn = 0x1C
)

// system is what the injector needs from the operating system.
type system interface {
	// send injects in, in order, and returns how many inputs it injected;
	// the error explains a shortfall.
	send(in []raw) (int, error)
	// virtualScreen is the virtual desktop in physical pixels.
	virtualScreen() rect
	// keyScan returns the scan code of virtual key vk and whether the key
	// is an extended key.
	keyScan(vk uint16) (scan uint16, extended bool)
	// foregroundAbove reports whether the foreground window belongs to a
	// process of a higher integrity level than the agent's.
	foregroundAbove() bool
	// clipboard is the clipboard of paste, nil without one.
	clipboard() clipboardAccess
	// drain waits for the thread of the foreground window to read the
	// input sent to it, for at most timeout or until ctx ends. It errs
	// only when the window stops responding.
	drain(ctx context.Context, timeout time.Duration) error
}

// clipboardAccess is the clipboard a paste goes through.
type clipboardAccess interface {
	// save copies the clipboard and returns the function that puts the
	// copy back.
	save() (restore func() error, err error)
	// setText replaces the clipboard with text.
	setText(text string) error
}

const (
	// displayCacheFor is how long the display list of move events is
	// reused.
	displayCacheFor = 2 * time.Second
	// pasteMinWait is the least time between Ctrl+V and restoring the
	// clipboard, and pasteRestoreWait bounds the wait for the paste target
	// to drain that follows it.
	pasteMinWait     = 120 * time.Millisecond
	pasteRestoreWait = time.Second
	// sessionCacheFor is how long a read of Options.SessionInfo is reused
	// while the input desktop stays the same.
	sessionCacheFor = time.Second
	// maxGen bounds a writer generation, the whole numbers a JSON number
	// holds exactly.
	maxGen = 1 << 53
)

// Injector injects input events. The writer generation only rises; an
// event or operation of an older generation is refused with
// CONTROL_PREEMPTED, and a rise releases every held key and button.
type Injector struct {
	opt Options
	sys system

	gen    atomic.Uint64
	owners atomic.Uint64

	// mu orders the injections and guards held.
	mu   sync.Mutex
	held map[keyID]*heldInput

	// pasteMu lets one paste use the clipboard at a time.
	pasteMu sync.Mutex

	dispMu sync.Mutex
	disp   []display.Info
	dispAt time.Time

	sessMu sync.Mutex
	sess   session.Info
	sessOK bool
	sessAt time.Time

	// lastText is when a chunk of text was last sent, in UnixNano.
	lastText atomic.Int64
	// injected is the monoclock reading after the last send that injected
	// an input, and acted after the last that injected one other than a
	// pointer move; 0 before the first.
	injected, acted atomic.Uint64

	pasteMin, pasteDelay time.Duration
}

// New returns an injector for the desktop of the agent's session.
func New(opt Options) *Injector { return newInjector(platformSystem(), opt) }

func newInjector(sys system, opt Options) *Injector {
	if opt.Logger == nil {
		opt.Logger = log.Default()
	}
	if opt.SessionInfo == nil {
		opt.SessionInfo = session.Current
	}
	if opt.InputDesktop == nil {
		opt.InputDesktop = session.InputDesktop
	}
	if opt.Displays == nil {
		opt.Displays = display.List
	}
	return &Injector{opt: opt, sys: sys, held: map[keyID]*heldInput{}, pasteMin: pasteMinWait, pasteDelay: pasteRestoreWait}
}

// Supported reports whether this system injects input.
func (j *Injector) Supported() bool { return j.sys != nil }

// Gen returns the writer generation.
func (j *Injector) Gen() uint64 { return j.gen.Load() }

// SetWriter raises the writer generation to gen and returns the generation
// after it. A rise releases every held key and button and then runs
// Options.Preempted; a gen that is not higher changes nothing.
func (j *Injector) SetWriter(gen uint64) uint64 {
	var prev uint64
	for {
		prev = j.gen.Load()
		if gen <= prev {
			return prev
		}
		if j.gen.CompareAndSwap(prev, gen) {
			break
		}
	}
	released, err := j.ReleaseAll()
	if err != nil {
		j.opt.Logger.Printf("input: releasing the held input of writer generation %d: %v", prev, err)
	}
	if j.opt.Preempted != nil {
		j.opt.Preempted(Preemption{Gen: gen, Previous: prev, Released: released})
	}
	return gen
}

// Admit checks an event or operation of writer generation gen: an older
// generation fails with CONTROL_PREEMPTED and a newer one becomes the
// writer generation through SetWriter. A generation that another writer
// raises past gen between the check and the rise also fails.
func (j *Injector) Admit(gen uint64) error {
	cur := j.gen.Load()
	switch {
	case gen < cur:
		return preempted(gen, cur)
	case gen > cur:
		if got := j.SetWriter(gen); got > gen {
			return preempted(gen, got)
		}
	}
	return nil
}

func preempted(gen, cur uint64) *Error {
	return fail(wire.CodeControlPreempted, "writer generation %d is older than the current %d because another writer has taken control; get control again before sending input", gen, cur)
}

// LastInjected returns the monoclock readings (docs/protocol.md mono_us)
// when Windows last took an input of this injector, from any input
// stream, operation or release, and when it last took one other than a
// pointer move; 0 before the first.
func (j *Injector) LastInjected() (input, action uint64) { return j.injected.Load(), j.acted.Load() }

// send injects in through the system and records the time when it
// injected any of it, and when any of that was more than a pointer move.
func (j *Injector) send(in []raw) (int, error) {
	n, err := j.sys.send(in)
	if n > 0 {
		now := max(monoclock.NowUS(), 1)
		j.injected.Store(now)
		if slices.ContainsFunc(in[:n], func(r raw) bool { return !pointerMove(r) }) {
			j.acted.Store(now)
		}
	}
	return n, err
}

// pointerMove reports whether r only moves the pointer.
func pointerMove(r raw) bool {
	return r.mouse && r.flags&^(mouseeventfMove|mouseeventfAbsolute|mouseeventfVirtualDesk) == 0
}

// Apply injects ev and returns its ack.
func (j *Injector) Apply(ev wire.InputEvent) wire.InputAck {
	return j.apply(context.Background(), 0, ev)
}

// apply injects ev for owner, the input stream that sent it (0 for none).
// A text stops when ctx ends, which an input stream cancels when the
// daemon's connection or its direction closes.
func (j *Injector) apply(ctx context.Context, owner uint64, ev wire.InputEvent) wire.InputAck {
	if ev.Gen > maxGen {
		return failedAck(ev.Seq, tooLargeGen(ev.Gen))
	}
	if err := j.Admit(ev.Gen); err != nil {
		return failedAck(ev.Seq, err)
	}
	if j.sys == nil {
		return failedAck(ev.Seq, errUnsupported())
	}
	var err error
	switch ev.K {
	case "text":
		switch ev.Method {
		case "", "unicode":
			err = j.typeText(ctx, owner, ev)
		case "paste":
			err = j.paste(ctx, owner, ev)
		default:
			err = fail(wire.CodeInvalidArgument, "text method '%s' is unknown; use unicode or paste", cut(ev.Method))
		}
	case "release_all":
		_, err = j.ReleaseAll()
	default:
		err = j.locked(ev.Gen, func() error {
			in, err := j.translate(ev)
			if err != nil {
				return err
			}
			return j.inject(owner, in)
		})
	}
	if err != nil {
		return failedAck(ev.Seq, err)
	}
	return wire.InputAck{Seq: ev.Seq, OK: true, DoneMonoUS: monoclock.NowUS()}
}

// tooLargeGen is the error of a writer generation past maxGen.
func tooLargeGen(gen uint64) *Error {
	return fail(wire.CodeInvalidArgument, "writer generation %d is too large; it must be a whole number from 0 to 2^53", gen)
}

// streamClosed is the error of a text that stopped because its input
// stream ended; its ack does not reach the daemon that has gone.
func streamClosed(format string, a ...any) *Error {
	return &Error{Code: wire.CodeControlPreempted, Msg: fmt.Sprintf(format, a...)}
}

// locked runs f with mu held once the generation gen is still the writer's
// and the session's desktop takes input, after releasing the input left
// held by a failed release.
func (j *Injector) locked(gen uint64, f func() error) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if cur := j.gen.Load(); gen < cur {
		return preempted(gen, cur)
	}
	if err := j.ready(); err != nil {
		return err
	}
	j.releaseStaleLocked()
	return f()
}

// ready fails when the session's desktop cannot take input: Session 0 or a
// disconnected session (DISPLAY_UNAVAILABLE), a locked PC
// (SESSION_LOCKED) or the secure desktop (ELEVATION_REQUIRED). A session
// that cannot be read does not stop the injection.
func (j *Injector) ready() error {
	info, ok := j.session()
	if !ok {
		return nil
	}
	return outageError(info)
}

// session returns Options.SessionInfo, read again when the input desktop
// differs from the one it named or it is sessionCacheFor old.
func (j *Injector) session() (session.Info, bool) {
	desk := j.opt.InputDesktop()
	j.sessMu.Lock()
	defer j.sessMu.Unlock()
	if !j.sessOK || desk != j.sess.InputDesktop || time.Since(j.sessAt) >= sessionCacheFor {
		info, err := j.opt.SessionInfo()
		j.sess, j.sessOK, j.sessAt = info, err == nil, time.Now()
	}
	return j.sess, j.sessOK
}

func outageError(info session.Info) error {
	u := capture.Classify(com.HRESULT(0), info)
	if u == nil {
		return nil
	}
	switch u.Reason {
	case capture.ReasonSessionLocked:
		return &Error{Code: wire.CodeSessionLocked, Msg: u.Error()}
	case capture.ReasonSecureDesktop:
		return &Error{Code: wire.CodeElevationRequired, Msg: u.Error()}
	}
	return &Error{Code: wire.CodeDisplayUnavailable, Msg: u.Error()}
}

// Outage names why the desktop of the session info describes cannot take
// input — session0, session_locked, secure_desktop or session_disconnected
// — or returns "" when it can.
func Outage(info session.Info) string {
	if u := capture.Classify(com.HRESULT(0), info); u != nil {
		return u.Reason
	}
	return ""
}

func errUnsupported() *Error {
	return fail(wire.CodeCapabilityUnsupported, "input injection needs a Windows agent; send input to a Windows PC")
}

// failedAck is the ack of the event seq that failed with err.
func failedAck(seq uint64, err error) wire.InputAck {
	var e *Error
	if errors.As(err, &e) {
		return wire.InputAck{Seq: seq, Code: e.Code, Error: e.Msg}
	}
	return wire.InputAck{Seq: seq, Code: wire.CodeInternal, Error: err.Error()}
}

// cut shortens a value quoted in an error to 32 bytes.
func cut(s string) string {
	if len(s) > 32 {
		return s[:32]
	}
	return s
}

// translate turns a move, button, wheel or key event into inputs.
func (j *Injector) translate(ev wire.InputEvent) ([]raw, error) {
	switch ev.K {
	case "move":
		return j.moveInputs(ev)
	case "button":
		r, err := buttonInput(ev.B, ev.Down)
		if err != nil {
			return nil, err
		}
		return []raw{r}, nil
	case "wheel":
		var in []raw
		if ev.DY != 0 {
			in = append(in, raw{mouse: true, data: uint32(int32(ev.DY)), flags: mouseeventfWheel})
		}
		if ev.DX != 0 {
			in = append(in, raw{mouse: true, data: uint32(int32(ev.DX)), flags: mouseeventfHWheel})
		}
		return in, nil
	case "key":
		r, err := j.keyInput(ev)
		if err != nil {
			return nil, err
		}
		return []raw{r}, nil
	}
	return nil, fail(wire.CodeInvalidArgument, "input event k '%s' is unknown; use move, button, wheel, key, text or release_all", cut(ev.K))
}

// buttonInput is the input that presses or releases button b.
func buttonInput(b string, down bool) (raw, error) {
	r := raw{mouse: true}
	switch b {
	case "left":
		r.flags = mouseeventfLeftDown
	case "right":
		r.flags = mouseeventfRightDown
	case "middle":
		r.flags = mouseeventfMiddleDown
	case "x1":
		r.flags, r.data = mouseeventfXDown, xbutton1
	case "x2":
		r.flags, r.data = mouseeventfXDown, xbutton2
	default:
		return raw{}, fail(wire.CodeInvalidArgument, "button b '%s' is unknown; use left, right, middle, x1 or x2", cut(b))
	}
	if !down {
		r.flags <<= 1
	}
	return r, nil
}

// keyInput is the input of a key event: by scan code when sc is set, with
// the E0 prefix when ext is, otherwise by virtual key.
func (j *Injector) keyInput(ev wire.InputEvent) (raw, error) {
	var r raw
	switch {
	case ev.SC > 0xFF:
		return raw{}, fail(wire.CodeInvalidArgument, "key sc %#x is not a set 1 scan code; send its low byte with ext true for an E0 key", ev.SC)
	case ev.SC != 0:
		r = raw{scan: ev.SC, flags: keyeventfScancode}
		if ev.Ext {
			r.flags |= keyeventfExtendedKey
		}
	case ev.VK != 0 && ev.VK < 0xFF:
		scan, ext := j.sys.keyScan(ev.VK)
		r = raw{vk: ev.VK, scan: scan}
		if ext || ev.Ext {
			r.flags |= keyeventfExtendedKey
		}
	default:
		return raw{}, fail(wire.CodeInvalidArgument, "a key event needs sc (a scan code from 1 to 0xFF) or vk (a virtual key from 1 to 0xFE)")
	}
	if !ev.Down {
		r.flags |= keyeventfKeyUp
	}
	return r, nil
}

// moveInputs is the absolute move to pixel (x, y) of a display, clamped
// to the display.
func (j *Injector) moveInputs(ev wire.InputEvent) ([]raw, error) {
	vs := j.sys.virtualScreen()
	d, ok := j.display(ev.Display, vs)
	if !ok || vs.w <= 0 || vs.h <= 0 {
		return nil, fail(wire.CodeDisplayUnavailable, "display %d is not attached to the PC's desktop (%s); call display.list and move on one of its displays", ev.Display, capture.ReasonNoOutput)
	}
	x := min(max(ev.X, 0), d.Width-1)
	y := min(max(ev.Y, 0), d.Height-1)
	return []raw{{mouse: true, dx: normalize(d.X+x-vs.x, vs.w), dy: normalize(d.Y+y-vs.y, vs.h),
		flags: mouseeventfMove | mouseeventfAbsolute | mouseeventfVirtualDesk}}, nil
}

// normalize maps pixel p of an axis of w pixels to the smallest 0–65535
// coordinate that SendInput maps back to p.
func normalize(p, w int) int32 {
	return int32((int64(p)*65536 + int64(w) - 1) / int64(w))
}

// display returns display index from the display list, which is read
// again when it is older than displayCacheFor, lacks the index or places
// the display outside the virtual desktop vs.
func (j *Injector) display(index int, vs rect) (display.Info, bool) {
	j.dispMu.Lock()
	defer j.dispMu.Unlock()
	find := func() (display.Info, bool) {
		for _, d := range j.disp {
			if d.Index == index && d.Width > 0 && d.Height > 0 && d.X >= vs.x && d.Y >= vs.y && d.X+d.Width <= vs.x+vs.w && d.Y+d.Height <= vs.y+vs.h {
				return d, true
			}
		}
		return display.Info{}, false
	}
	if time.Since(j.dispAt) < displayCacheFor {
		if d, ok := find(); ok {
			return d, true
		}
	}
	list, err := j.opt.Displays()
	if err != nil {
		return display.Info{}, false
	}
	j.disp, j.dispAt = list, time.Now()
	return find()
}

// inject sends in for owner with mu held and records the keys and buttons
// it pressed and released. When only part of in was injected, the keys
// and buttons it pressed that a later input of in would have released are
// released, and the error says why the rest was not injected.
func (j *Injector) inject(owner uint64, in []raw) error {
	if len(in) == 0 {
		return nil
	}
	n, err := j.send(in)
	for _, r := range in[:n] {
		j.track(owner, r)
	}
	if n == len(in) {
		return nil
	}
	j.unwind(owner, in[:n], in[n:])
	return j.shortSend(n, len(in), err)
}

// shortSend is the error of a send that injected n of want inputs.
func (j *Injector) shortSend(n, want int, err error) error {
	if info, ierr := j.opt.SessionInfo(); ierr == nil {
		if oerr := outageError(info); oerr != nil {
			return oerr
		}
	}
	if j.sys.foregroundAbove() {
		return fail(wire.CodeElevationRequired, "the window in front runs at a higher integrity level than deskhand-agent, so Windows blocks input to it (UIPI); answer it at the PC or run deskhand-agent with the same rights")
	}
	return fail(wire.CodeInternal, "Windows injected %d of %d inputs (%v); try again, and check the PC's desktop if it persists", n, want, err)
}
