// Package screen serves the video stream (docs/protocol.md §2, §4): a
// Pipeline captures one display, paces and encodes its frames and writes
// info, access unit, stats and paused frames; ServeVideo runs one for a
// stream and hands it the controls the daemon sends on that stream.
package screen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/encode"
	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Limits and defaults of the video stream's parameters.
const (
	DefaultMaxFPS = 60
	maxMaxFPS     = 240
	minBitrate    = 100_000
	maxBitrate    = 200_000_000
	minMaxWidth   = 64
	maxMaxWidth   = 16384
)

const (
	// keepalive is how long a static screen goes without an access unit
	// before the last image is encoded again.
	keepalive = 10 * time.Second
	// statsEvery is the interval of the stats frames.
	statsEvery = time.Second
	// poll bounds each wait for a frame and with it the delay of a
	// control.
	poll = 25 * time.Millisecond
	// pollStep is the pause between two looks at a Desktop Duplication
	// source while the encoder has frames.
	pollStep = time.Millisecond
	// burst is the room of the pacing bucket in frames: max_fps frames a
	// second, and a frame that comes up to a quarter interval early goes at
	// once.
	burst = 1.25
	// firstRetry and maxRetry bound the pause before opening the display
	// or making the encoder again after a failure; it doubles with each
	// failure in a row.
	firstRetry = 500 * time.Millisecond
	maxRetry   = 5 * time.Second
	// busyLimit is how long the encoder may hold or refuse frames without
	// giving an access unit, while none waits for the network, before it
	// is made again.
	busyLimit = 3 * time.Second
	// staleSubmission is how long a submission waits for its access unit
	// in the stats before it is forgotten.
	staleSubmission = 10 * time.Second
	// pollWindow is how long after the last frame given to the encoder the
	// pipeline still looks at a Desktop Duplication source without waiting.
	pollWindow = 100 * time.Millisecond
	// maxInFlight is the number of frames given to the encoder whose access
	// units are not written yet at which the pipeline skips frames.
	maxInFlight = 2
	// queueRoom is the room of the writer's queue in frames.
	queueRoom = 8
)

// Clock is the time of a pipeline: monoclock readings and waits.
type Clock interface {
	NowUS() uint64
	Sleep(ctx context.Context, d time.Duration) error
}

// realClock is the monoclock and the timers of the runtime.
type realClock struct{}

func (realClock) NowUS() uint64 { return monoclock.NowUS() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Options configure a Pipeline; a zero field takes the real thing.
type Options struct {
	// Open opens a capture source of a display; nil means capture.Open.
	Open func(display int) (capture.Source, error)
	// NewEncoder makes an encoder on the source's device; nil means
	// encode.New.
	NewEncoder func(dev *d3d.Device, srcW, srcH, outW, outH, fps, bps int) (encode.Encoder, error)
	// InitThread prepares the capture thread; nil means encode.InitThread.
	InitThread func() (leave func(), err error)
	// LastInput returns the Clock readings when an input was injected last
	// and when one other than a pointer move was, 0 for none; nil means no
	// input.
	LastInput func() (input, action uint64)
	Clock     Clock
	Logger    *log.Logger
}

// Refusal is the error of a stream header the pipeline does not accept,
// returned before it wrote anything; Code is a wire code.
type Refusal struct {
	Code    string
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// Pipeline streams one display. Start runs it; Control steers it while it
// runs.
type Pipeline struct {
	opt Options
	ctl chan wire.VideoControl
}

// New makes a pipeline with opt.
func New(opt Options) *Pipeline {
	if opt.Open == nil {
		opt.Open = capture.Open
	}
	if opt.NewEncoder == nil {
		opt.NewEncoder = encode.New
	}
	if opt.InitThread == nil {
		opt.InitThread = encode.InitThread
	}
	if opt.Clock == nil {
		opt.Clock = realClock{}
	}
	if opt.Logger == nil {
		opt.Logger = log.New(io.Discard, "", 0)
	}
	return &Pipeline{opt: opt, ctl: make(chan wire.VideoControl, 64)}
}

// Control queues c for the running pipeline; it drops c when 64 controls
// are queued already.
func (p *Pipeline) Control(c wire.VideoControl) {
	select {
	case p.ctl <- c:
	default:
		p.opt.Logger.Printf("video: control %q dropped; 64 controls are waiting", c.Op)
	}
}

// settings are the parameters of a stream: max_fps 0 is DefaultMaxFPS,
// maxWidth 0 the source width and bitrate 0 DefaultBitrate of the output
// width.
type settings struct {
	display, maxFPS, maxWidth, bitrate int
}

func invalid(format string, a ...any) *Refusal {
	return &Refusal{Code: wire.CodeInvalidArgument, Message: fmt.Sprintf(format, a...)}
}

// settingsOf checks the video header h.
func settingsOf(h wire.StreamHeader) (settings, error) {
	if h.Codec != "" && h.Codec != "h264" {
		return settings{}, &Refusal{Code: wire.CodeCapabilityUnsupported,
			Message: fmt.Sprintf("this agent encodes h264, not '%s'; open the video stream with codec h264", h.Codec)}
	}
	if h.Display < 0 {
		return settings{}, invalid("display %d is negative; use an index of display.list", h.Display)
	}
	s := settings{display: h.Display}
	var err *Refusal
	if s.maxFPS, err = checkMaxFPS(h.MaxFPS); err != nil {
		return settings{}, err
	}
	if s.maxWidth, err = checkMaxWidth(h.MaxWidth); err != nil {
		return settings{}, err
	}
	if h.Bitrate != 0 {
		if err := checkBitrate(h.Bitrate); err != nil {
			return settings{}, err
		}
	}
	s.bitrate = h.Bitrate
	return s, nil
}

func checkMaxFPS(n int) (int, *Refusal) {
	if n == 0 {
		return DefaultMaxFPS, nil
	}
	if n < 1 || n > maxMaxFPS {
		return 0, invalid("max_fps %d is out of range; use 1 to %d, or 0 for %d", n, maxMaxFPS, DefaultMaxFPS)
	}
	return n, nil
}

func checkMaxWidth(n int) (int, *Refusal) {
	if n != 0 && (n < minMaxWidth || n > maxMaxWidth) {
		return 0, invalid("max_width %d is out of range; use %d to %d, or 0 for the display's width", n, minMaxWidth, maxMaxWidth)
	}
	return n, nil
}

func checkBitrate(n int) *Refusal {
	if n < minBitrate || n > maxBitrate {
		return invalid("bitrate %d is out of range; use %d to %d bits per second, or 0 for the default", n, minBitrate, maxBitrate)
	}
	return nil
}

// OutputSize returns the size of the access units of a srcW×srcH display
// at maxWidth: the display's size when maxWidth is 0 or not smaller, else
// maxWidth wide with the aspect ratio kept; both even.
func OutputSize(srcW, srcH, maxWidth int) (int, int) {
	w, h := srcW, srcH
	if maxWidth > 0 && maxWidth < srcW {
		w, h = maxWidth, int(math.Round(float64(srcH)*float64(maxWidth)/float64(srcW)))
	}
	return max(w&^1, 2), max(h&^1, 2)
}

// DefaultBitrate is the bitrate of an output w pixels wide: 6 Mbps up to
// 1920, 12 Mbps up to 2560 and 20 Mbps above.
func DefaultBitrate(w int) int {
	switch {
	case w <= 1920:
		return 6_000_000
	case w <= 2560:
		return 12_000_000
	}
	return 20_000_000
}

// pausedReason is the reason of the paused frame for a failure of the
// capture: the fixed reason of its *capture.Unavailable, no_output for a
// display that does not exist and access_lost otherwise.
func pausedReason(err error) string {
	var u *capture.Unavailable
	switch {
	case errors.As(err, &u):
		return u.Reason
	case errors.Is(err, capture.ErrNoDisplay):
		return capture.ReasonNoOutput
	}
	return capture.ReasonAccessLost
}

// Start runs the pipeline for the stream with header params until ctx
// ends, which returns nil, or until out fails, which returns its error.
// It writes the frames of docs/protocol.md §4 with out, which one
// goroutine calls in order and which has to return once ctx ends. A
// header it does not accept, a display that does not exist, or a PC that
// can neither capture nor encode is a *Refusal, returned before anything
// is written.
func (p *Pipeline) Start(ctx context.Context, params wire.StreamHeader, out func(kind byte, payload []byte) error) error {
	set, err := settingsOf(params)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &run{p: p, ctx: ctx, cancel: cancel, clock: p.opt.Clock, log: p.opt.Logger, out: out, set: set, stats: newWindow(),
		queue: make(chan queued, queueRoom), writerDone: make(chan struct{})}
	go r.writeLoop()
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		leave, err := p.opt.InitThread()
		if err != nil {
			done <- fmt.Errorf("preparing the capture thread: %w", err)
			return
		}
		defer leave()
		done <- r.loop()
	}()
	err = <-done
	close(r.queue)
	<-r.writerDone
	return err
}

// queued is one frame waiting for the writer: an access unit of the
// encoder session epoch, or a frame of kind with payload.
type queued struct {
	kind    byte
	payload []byte
	au      *wire.AU
	epoch   uint64
}

// run is the state of a running pipeline, owned by its capture thread
// except where noted.
type run struct {
	p      *Pipeline
	ctx    context.Context
	cancel context.CancelFunc
	clock  Clock
	log    *log.Logger
	set    settings

	// out is called by the writer goroutine alone with the frames of queue
	// in order. wrote is set once a frame was queued; failed holds the
	// error of out.
	out        func(kind byte, payload []byte) error
	queue      chan queued
	writerDone chan struct{}
	wrote      atomic.Bool
	failed     atomic.Pointer[error]
	// epoch numbers the encoder sessions; the writer drops the access units
	// of an earlier one. received counts the access units taken from the
	// encoders and unwritten those of them not written yet. seq, of the
	// writer, numbers the access units written.
	epoch     atomic.Uint64
	received  atomic.Uint64
	unwritten atomic.Int64
	seq       uint64

	src       capture.Source
	openAtUS  uint64
	openRetry time.Duration

	// enc is the encoder, a hardware one when hardware is set.
	enc                        encode.Encoder
	hardware                   bool
	encSrcW, encSrcH           int
	encW, encH, encFPS, encBPS int
	encAtUS                    uint64
	encRetry                   time.Duration
	fwdStop, fwdDone           chan struct{}

	// last is the newest image of the source, taken at imageAtUS. pending
	// reports that it is to be encoded, and repeat that it was encoded
	// already and goes again as an image captured now.
	last              capture.Frame
	imageAtUS         uint64
	haveLast, pending bool
	repeat            bool
	forceKey          bool
	needInfo          bool
	lastEncodeUS      uint64
	// busy reports that the encoder has held or refused frames since
	// busySinceUS, when received was busyReceived.
	busy         bool
	busySinceUS  uint64
	busyReceived uint64
	// tokens is the pacing bucket, frames that may be encoded now, as it
	// was at tokensAtUS.
	tokens     float64
	tokensAtUS uint64
	// dd reports that the source gives textures, from Desktop Duplication.
	dd bool

	// paused is the reason of the paused frame written last, "" once an
	// info frame followed it; userPaused is set by the pause control.
	paused     string
	userPaused bool

	stats     *window
	statsAtUS uint64
}

// loop runs the pipeline on the capture thread.
func (r *run) loop() error {
	defer r.closeSource()
	r.statsAtUS = r.clock.NowUS()
	r.tokens, r.tokensAtUS = burst, r.statsAtUS
	for {
		if err := r.failure(); err != nil {
			return err
		}
		if r.ctx.Err() != nil {
			return nil
		}
		if err := r.controls(); err != nil {
			return err
		}
		now := r.clock.NowUS()
		if err := r.maybeStats(now); err != nil {
			return err
		}
		if r.userPaused {
			_ = r.clock.Sleep(r.ctx, poll)
			continue
		}
		if r.src == nil {
			if now < r.openAtUS {
				_ = r.clock.Sleep(r.ctx, min(poll, us(r.openAtUS-now)))
				continue
			}
			if err := r.open(now); err != nil {
				return err
			}
			continue
		}
		if r.due(now) {
			if err := r.encode(now); err != nil {
				return err
			}
			continue
		}
		f, err := r.next(now)
		if err := r.took(f, err); err != nil {
			return err
		}
	}
}

func us(n uint64) time.Duration { return time.Duration(n) * time.Microsecond }

// writeLoop writes the queued frames with out until the queue closes. It
// drops an access unit of an earlier epoch, and every frame after a
// failure of out, which ends the pipeline.
func (r *run) writeLoop() {
	defer close(r.writerDone)
	for q := range r.queue {
		if q.au == nil {
			if r.failure() == nil {
				r.send(q.kind, q.payload)
			}
			continue
		}
		if q.epoch == r.epoch.Load() && r.failure() == nil {
			r.seq++
			q.au.Seq = r.seq
			if r.send(wire.KindAU, q.au.Marshal()) {
				r.stats.sent(*q.au)
			}
		}
		r.unwritten.Add(-1)
	}
}

// send writes one frame with out and reports whether it was written.
func (r *run) send(kind byte, payload []byte) bool {
	if err := r.out(kind, payload); err != nil {
		r.failed.CompareAndSwap(nil, &err)
		r.cancel()
		return false
	}
	return true
}

// post queues a frame of the capture thread with v as its JSON body. A
// stats frame that finds the queue full is dropped; another frame waits
// for room until ctx ends.
func (r *run) post(kind byte, v any) error {
	if err := r.failure(); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.wrote.Store(true)
	q := queued{kind: kind, payload: b}
	if kind == wire.KindVideoStats {
		select {
		case r.queue <- q:
		default:
		}
		return nil
	}
	select {
	case r.queue <- q:
	case <-r.ctx.Done():
	}
	return nil
}

func (r *run) failure() error {
	if p := r.failed.Load(); p != nil {
		return *p
	}
	return nil
}

// inFlight counts the frames given to the encoder whose access units are
// not written yet.
func (r *run) inFlight() int {
	n := int(r.unwritten.Load())
	if r.enc != nil {
		n += r.enc.Pending()
	}
	return n
}

// controls applies the queued controls.
func (r *run) controls() error {
	for {
		select {
		case c := <-r.p.ctl:
			if err := r.apply(c); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

// again makes the last image pending as a repeat unless a newer one is.
func (r *run) again() {
	if r.haveLast && !r.pending {
		r.pending, r.repeat = true, true
	}
}

func (r *run) apply(c wire.VideoControl) error {
	switch c.Op {
	case wire.ControlKeyframe:
		r.forceKey = true
	case wire.ControlBitrate:
		if err := checkBitrate(c.BPS); err != nil {
			r.log.Printf("video: control bitrate ignored: %v", err)
			return nil
		}
		r.set.bitrate = c.BPS
		if r.enc != nil {
			if err := r.enc.SetBitrate(c.BPS); err != nil {
				r.log.Printf("video: %s cannot change its bitrate (%v); making it again", r.enc.Info().Name, err)
				r.closeEncoder()
				r.again()
			} else {
				r.encBPS = c.BPS
			}
		}
	case wire.ControlConfig:
		fps, err := checkMaxFPS(c.MaxFPS)
		if err == nil {
			_, err = checkMaxWidth(c.MaxWidth)
		}
		if err != nil {
			r.log.Printf("video: control config ignored: %v", err)
			return nil
		}
		r.set.maxFPS, r.set.maxWidth = fps, c.MaxWidth
		if r.enc != nil {
			r.closeEncoder()
			r.again()
		}
	case wire.ControlPause:
		if r.userPaused {
			return nil
		}
		r.userPaused = true
		r.closeSource()
		return r.pause(wire.PausedNoViewer)
	case wire.ControlResume:
		if r.userPaused {
			r.userPaused, r.openAtUS, r.openRetry = false, 0, 0
		}
	default:
		r.log.Printf("video: unknown control %q ignored", c.Op)
	}
	return nil
}

// pause closes the encoder, drops its access units not written yet and
// writes a paused frame with reason, unless the last paused frame had it.
func (r *run) pause(reason string) error {
	if r.paused == reason {
		return nil
	}
	r.closeEncoder()
	r.epoch.Add(1)
	r.paused = reason
	r.log.Printf("video: display %d paused: %s", r.set.display, reason)
	return r.post(wire.KindVideoPaused, wire.VideoPaused{Reason: reason})
}

// backoff returns the next pause after a failure, doubling *d.
func backoff(d *time.Duration) time.Duration {
	*d = min(max(2**d, firstRetry), maxRetry)
	return *d
}

// open opens the display. A display that does not exist or a system
// without capture is a refusal before anything was written; another
// failure is a paused frame and a later retry.
func (r *run) open(now uint64) error {
	src, err := r.p.opt.Open(r.set.display)
	if err == nil {
		r.src, r.openRetry = src, 0
		return nil
	}
	if !r.wrote.Load() {
		switch {
		case errors.Is(err, capture.ErrUnsupported):
			return &Refusal{Code: wire.CodeCapabilityUnsupported, Message: err.Error()}
		case errors.Is(err, capture.ErrNoDisplay):
			return &Refusal{Code: wire.CodeNotFound, Message: err.Error()}
		}
	}
	r.log.Printf("video: display %d cannot be opened: %v", r.set.display, err)
	r.openAtUS = now + uint64(backoff(&r.openRetry)/time.Microsecond)
	return r.pause(pausedReason(err))
}

// closeSource closes the encoder and then the source.
func (r *run) closeSource() {
	r.closeEncoder()
	if r.src != nil {
		r.src.Close()
		r.src = nil
	}
	r.haveLast, r.pending, r.dd = false, false, false
}

// closeEncoder closes the encoder and waits for its forwarder; the access
// units it queued stay queued.
func (r *run) closeEncoder() {
	if r.enc == nil {
		return
	}
	r.enc.Close()
	close(r.fwdStop)
	<-r.fwdDone
	r.enc, r.hardware = nil, false
}

// next waits for a frame of the source up to timeout. While polling it
// looks for a frame without waiting and pauses pollStep after every look
// that gives none.
func (r *run) next(now uint64) (capture.Frame, error) {
	timeout := r.timeout(now)
	if timeout == 0 || !r.polling(now) {
		return r.src.Next(r.ctx, timeout)
	}
	f, err := r.src.Next(r.ctx, 0)
	if err != nil {
		_ = r.clock.Sleep(r.ctx, min(timeout, pollStep))
	}
	return f, err
}

// polling reports whether the pipeline looks at its source without
// waiting: a Desktop Duplication source, not paused, with an encoder,
// while warming or while the encoder holds frames of which the last was
// given within pollWindow.
func (r *run) polling(now uint64) bool {
	if !r.dd || r.paused != "" || r.enc == nil {
		return false
	}
	return r.warming(now) || (now < r.lastEncodeUS+uint64(pollWindow/time.Microsecond) && r.enc.Pending() > 0)
}

// took handles the result of the source's Next.
func (r *run) took(f capture.Frame, err error) error {
	var u *capture.Unavailable
	switch {
	case err == nil:
		if !f.Dirty {
			return nil
		}
		r.last, r.haveLast, r.pending, r.repeat, r.dd = f, true, true, false, f.Texture != 0
		r.imageAtUS = r.clock.NowUS()
		if r.paused != "" {
			r.needInfo, r.forceKey = true, true
		}
	case errors.Is(err, capture.ErrNoNewFrame):
	case errors.Is(err, capture.ErrReset):
		r.haveLast, r.pending = false, false
	case errors.As(err, &u):
		r.haveLast, r.pending = false, false
		return r.pause(u.Reason)
	case r.ctx.Err() != nil:
	default:
		r.log.Printf("video: capture of display %d failed: %v; opening it again", r.set.display, err)
		r.closeSource()
		r.openAtUS = r.clock.NowUS() + uint64(backoff(&r.openRetry)/time.Microsecond)
		return r.pause(pausedReason(err))
	}
	return nil
}

// interval is the time between two frames at max_fps in µs.
func (r *run) interval() uint64 { return uint64(time.Second/time.Microsecond) / uint64(r.set.maxFPS) }

// refill adds the tokens of max_fps since the bucket was filled last, up
// to burst, and returns them.
func (r *run) refill(now uint64) float64 {
	if now > r.tokensAtUS {
		r.tokens = min(burst, r.tokens+float64(now-r.tokensAtUS)*float64(r.set.maxFPS)/1e6)
		r.tokensAtUS = now
	}
	return r.tokens
}

// slotUS is when the bucket holds a frame again.
func (r *run) slotUS(now uint64) uint64 {
	if t := r.refill(now); t < 1 {
		return now + uint64(math.Ceil((1-t)*1e6/float64(r.set.maxFPS)))
	}
	return now
}

// due reports whether an image is to be encoded now: a new one, the last
// one again for a key frame request, after keepalive without an access
// unit, or while warmDue holds after an input; within the pacing of
// max_fps.
func (r *run) due(now uint64) bool {
	if !r.haveLast {
		return false
	}
	if !r.pending && (r.forceKey || (r.enc != nil && (now >= r.lastEncodeUS+uint64(keepalive/time.Microsecond) ||
		warmDue(r.warmState(), now)))) {
		r.pending, r.repeat = true, true
	}
	return r.pending && r.refill(now) >= 1 && now >= r.encAtUS
}

// warmState is the state warmDue decides on now; it holds no input
// while the encoder is not a hardware one.
func (r *run) warmState() warmState {
	s := warmState{imageUS: r.imageAtUS, auUS: r.lastEncodeUS, maxFPS: r.set.maxFPS, inFlight: r.inFlight()}
	if r.p.opt.LastInput != nil && r.hardware {
		s.inputUS, s.actionUS = r.p.opt.LastInput()
	}
	return s
}

// warming reports whether the encoder is a hardware one and now is within
// warmWindow of the last input.
func (r *run) warming(now uint64) bool {
	if r.p.opt.LastInput == nil || !r.hardware {
		return false
	}
	in, _ := r.p.opt.LastInput()
	return in != 0 && now < in+uint64(warmWindow/time.Microsecond)
}

// timeout is how long Next may wait: until the next pacing slot of a
// pending image, the keepalive of a static one, its next frame while
// warming (pollStep while the encoder holds frames), or the next stats
// frame, and at most poll.
func (r *run) timeout(now uint64) time.Duration {
	until := func(t uint64) time.Duration {
		if t <= now {
			return 0
		}
		return us(t - now)
	}
	d := min(poll, until(r.statsAtUS+uint64(statsEvery/time.Microsecond)))
	switch {
	case r.pending:
		d = min(d, until(max(r.slotUS(now), r.encAtUS)))
	case r.haveLast && r.enc != nil:
		d = min(d, until(r.lastEncodeUS+uint64(keepalive/time.Microsecond)))
		if r.warming(now) {
			if s := r.warmState(); s.inFlight > 0 {
				d = min(d, pollStep)
			} else {
				d = min(d, until(s.earliestUS()))
			}
		}
	}
	return d
}

// encode encodes the pending image, making the encoder first when there
// is none for the image's size and the settings. With maxInFlight frames
// in flight the pacing slot passes without a frame: dropped while access
// units wait for the network, encoder busy otherwise.
func (r *run) encode(now uint64) error {
	f := r.last
	if r.repeat {
		f.MonoUS = now
	}
	outW, outH := OutputSize(f.Width, f.Height, r.set.maxWidth)
	if r.enc == nil || r.encSrcW != f.Width || r.encSrcH != f.Height || r.encW != outW || r.encH != outH || r.encFPS != r.set.maxFPS {
		if err := r.makeEncoder(f, outW, outH, now); err != nil || r.enc == nil {
			return err
		}
	}
	if r.inFlight() >= maxInFlight {
		r.tokens--
		if r.unwritten.Load() > 0 {
			r.stats.drop()
		} else {
			r.stats.busy()
		}
		r.checkStall(now)
		return nil
	}
	if r.needInfo {
		if err := r.writeInfo(f); err != nil {
			return err
		}
	}
	r.stats.submit(f.MonoUS, now)
	err := r.enc.Encode(f, r.forceKey)
	switch {
	case err == nil:
		r.pending, r.repeat, r.forceKey = false, false, false
		r.lastEncodeUS, r.busy, r.encRetry = now, false, 0
		r.tokens--
	case errors.Is(err, encode.ErrBusy):
		r.stats.forget(f.MonoUS)
		r.stats.busy()
		r.tokens--
		r.checkStall(now)
	default:
		r.stats.forget(f.MonoUS)
		r.log.Printf("video: %s failed: %v; making it again", r.enc.Info().Name, err)
		r.closeEncoder()
		r.encAtUS = now + uint64(backoff(&r.encRetry)/time.Microsecond)
	}
	return nil
}

// checkStall makes the encoder again once it has held or refused frames
// for busyLimit without giving an access unit while none waited for the
// network; the next frame then goes to a new encoder after an info frame
// as an IDR.
func (r *run) checkStall(now uint64) {
	got := r.received.Load()
	switch {
	case r.unwritten.Load() > 0:
		r.busy = false
	case !r.busy || got != r.busyReceived:
		r.busy, r.busySinceUS, r.busyReceived = true, now, got
	case now-r.busySinceUS > uint64(busyLimit/time.Microsecond):
		r.log.Printf("video: %s gave no access unit for %v; making it again", r.enc.Info().Name, busyLimit)
		r.closeEncoder()
		r.busy = false
	}
}

// makeEncoder replaces the encoder with one for f at outW×outH. A PC
// without an encoder is a refusal before anything was written; another
// failure is a paused frame, no_encoder, and a later retry.
func (r *run) makeEncoder(f capture.Frame, outW, outH int, now uint64) error {
	r.closeEncoder()
	bps := r.set.bitrate
	if bps == 0 {
		bps = DefaultBitrate(outW)
	}
	enc, err := r.p.opt.NewEncoder(r.src.Device(), f.Width, f.Height, outW, outH, r.set.maxFPS, bps)
	if err != nil {
		if !r.wrote.Load() && (errors.Is(err, encode.ErrNoEncoder) || errors.Is(err, encode.ErrUnsupported)) {
			return &Refusal{Code: wire.CodeCapabilityUnsupported, Message: err.Error()}
		}
		r.log.Printf("video: no encoder for display %d at %dx%d: %v", r.set.display, outW, outH, err)
		r.encAtUS = now + uint64(backoff(&r.encRetry)/time.Microsecond)
		return r.pause(wire.PausedNoEncoder)
	}
	info := enc.Info()
	r.log.Printf("video: display %d %dx%d to %dx%d at %d fps, %d bps on %s (hardware %v)", r.set.display, f.Width, f.Height,
		outW, outH, r.set.maxFPS, bps, info.Name, info.Hardware)
	r.enc, r.encSrcW, r.encSrcH, r.encW, r.encH, r.encFPS, r.encBPS = enc, f.Width, f.Height, outW, outH, r.set.maxFPS, bps
	r.hardware = info.Hardware
	r.needInfo, r.forceKey = true, true
	r.fwdStop, r.fwdDone = make(chan struct{}), make(chan struct{})
	go r.forward(enc, r.epoch.Load(), r.fwdStop, r.fwdDone)
	return nil
}

// forward queues the access units of enc, of encoder session epoch, until
// its output closes or stop closes.
func (r *run) forward(enc encode.Encoder, epoch uint64, stop, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		case au, ok := <-enc.Output():
			if !ok {
				return
			}
			r.received.Add(1)
			r.unwritten.Add(1)
			select {
			case r.queue <- queued{au: &au, epoch: epoch}:
			case <-stop:
				r.unwritten.Add(-1)
				return
			case <-r.ctx.Done():
				r.unwritten.Add(-1)
				return
			}
		}
	}
}

// writeInfo queues the info frame of the encoder for frames like f.
func (r *run) writeInfo(f capture.Frame) error {
	method := capture.MethodGDI
	if f.Texture != 0 {
		method = capture.MethodDD
	}
	info := r.enc.Info()
	r.needInfo, r.paused = false, ""
	return r.post(wire.KindVideoInfo, wire.VideoInfo{Codec: "h264", Width: r.encW, Height: r.encH, SrcWidth: f.Width, SrcHeight: f.Height,
		FPS: r.encFPS, Bitrate: r.encBPS, Encoder: info.Name, Hardware: info.Hardware, Capture: method, Display: r.set.display})
}

// maybeStats queues the stats frame of the second that ended, while
// access units flow.
func (r *run) maybeStats(now uint64) error {
	if now < r.statsAtUS+uint64(statsEvery/time.Microsecond) {
		return nil
	}
	s := r.stats.take(now-r.statsAtUS, now, uint64(staleSubmission/time.Microsecond))
	r.statsAtUS = now
	if r.enc == nil || r.paused != "" || r.userPaused {
		return nil
	}
	return r.post(wire.KindVideoStats, s)
}
