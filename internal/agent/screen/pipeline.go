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
	"sync"
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
	// busyLimit is how long the encoder may refuse every frame before it
	// is made again.
	busyLimit = 3 * time.Second
	// staleSubmission is how long a submission waits for its access unit
	// in the stats before it is forgotten.
	staleSubmission = 10 * time.Second
	// flushWait bounds the wait for the access units of the frames given to
	// the encoder before a paused frame.
	flushWait = 200 * time.Millisecond
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
	Clock      Clock
	Logger     *log.Logger
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

// pausedReason is the reason of the paused frame for an outage of the
// capture.
func pausedReason(u *capture.Unavailable) string {
	switch u.Reason {
	case capture.ReasonSecureDesktop:
		return wire.PausedSecureDesktop
	case capture.ReasonSessionLocked:
		return wire.PausedSessionLocked
	}
	return wire.PausedDisplayUnavailable
}

// Start runs the pipeline for the stream with header params until ctx
// ends, which returns nil, or until out fails, which returns its error.
// It writes the frames of docs/protocol.md §4 with out, from one
// goroutine at a time. A header it does not accept, a display that does
// not exist, or a PC that can neither capture nor encode is a *Refusal,
// returned before anything is written.
func (p *Pipeline) Start(ctx context.Context, params wire.StreamHeader, out func(kind byte, payload []byte) error) error {
	set, err := settingsOf(params)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &run{p: p, ctx: ctx, cancel: cancel, clock: p.opt.Clock, log: p.opt.Logger, out: out, set: set, stats: newWindow()}
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
	return <-done
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

	// outMu orders the writes of out; wrote and writeErr are under it.
	outMu    sync.Mutex
	out      func(kind byte, payload []byte) error
	wrote    bool
	writeErr error

	src       capture.Source
	openAtUS  uint64
	openRetry time.Duration

	enc                        encode.Encoder
	encSrcW, encSrcH           int
	encW, encH, encFPS, encBPS int
	encAtUS                    uint64
	encRetry                   time.Duration
	fwdStop, fwdDone           chan struct{}
	seq                        atomic.Uint64
	// inflight counts the frames given to the encoder whose access units
	// were not written yet.
	inflight atomic.Int64

	// last is the newest image of the source. pending reports that it is
	// to be encoded, and repeat that it was encoded already and goes again
	// as an image captured now.
	last              capture.Frame
	haveLast, pending bool
	repeat            bool
	forceKey          bool
	needInfo          bool
	lastEncodeUS      uint64
	busySinceUS       uint64
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

// write writes one frame with out; after a failure of out it returns that
// failure and ends the pipeline.
func (r *run) write(kind byte, payload []byte) error {
	r.outMu.Lock()
	defer r.outMu.Unlock()
	if r.writeErr != nil {
		return r.writeErr
	}
	r.wrote = true
	if err := r.out(kind, payload); err != nil {
		r.writeErr = err
		r.cancel()
		return err
	}
	return nil
}

func (r *run) writeJSON(kind byte, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return r.write(kind, b)
}

func (r *run) failure() error {
	r.outMu.Lock()
	defer r.outMu.Unlock()
	return r.writeErr
}

func (r *run) hasWritten() bool {
	r.outMu.Lock()
	defer r.outMu.Unlock()
	return r.wrote
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
		r.haveLast, r.pending = false, false
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

// pause writes a paused frame with reason unless the last one had it,
// after the access units of the frames the encoder has.
func (r *run) pause(reason string) error {
	if r.paused == reason {
		return nil
	}
	r.flush()
	r.paused = reason
	r.log.Printf("video: display %d paused: %s", r.set.display, reason)
	return r.writeJSON(wire.KindVideoPaused, wire.VideoPaused{Reason: reason})
}

// flush waits up to flushWait until the access units of the frames given
// to the encoder are written.
func (r *run) flush() {
	deadline := time.Now().Add(flushWait)
	for r.enc != nil && r.inflight.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
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
	if !r.hasWritten() {
		switch {
		case errors.Is(err, capture.ErrUnsupported):
			return &Refusal{Code: wire.CodeCapabilityUnsupported, Message: err.Error()}
		case errors.Is(err, capture.ErrNoDisplay):
			return &Refusal{Code: wire.CodeNotFound, Message: err.Error()}
		}
	}
	reason := wire.PausedDisplayUnavailable
	var u *capture.Unavailable
	if errors.As(err, &u) {
		reason = pausedReason(u)
	}
	r.log.Printf("video: display %d cannot be opened: %v", r.set.display, err)
	r.openAtUS = now + uint64(backoff(&r.openRetry)/time.Microsecond)
	return r.pause(reason)
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

// closeEncoder closes the encoder and waits for its forwarder.
func (r *run) closeEncoder() {
	if r.enc == nil {
		return
	}
	r.enc.Close()
	close(r.fwdStop)
	<-r.fwdDone
	r.enc = nil
	r.inflight.Store(0)
}

// next waits for a frame of the source up to timeout. While the encoder
// has frames of a Desktop Duplication source, it looks for a frame without
// waiting and pauses pollStep between looks.
func (r *run) next(now uint64) (capture.Frame, error) {
	timeout := r.timeout(now)
	if !r.dd || r.inflight.Load() == 0 || timeout == 0 {
		return r.src.Next(r.ctx, timeout)
	}
	f, err := r.src.Next(r.ctx, 0)
	if errors.Is(err, capture.ErrNoNewFrame) {
		_ = r.clock.Sleep(r.ctx, min(timeout, pollStep))
	}
	return f, err
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
		if r.paused != "" {
			r.needInfo, r.forceKey = true, true
		}
	case errors.Is(err, capture.ErrNoNewFrame):
	case errors.Is(err, capture.ErrReset):
		r.haveLast, r.pending = false, false
	case errors.As(err, &u):
		r.haveLast, r.pending = false, false
		return r.pause(pausedReason(u))
	case r.ctx.Err() != nil:
	default:
		r.log.Printf("video: capture of display %d failed: %v; opening it again", r.set.display, err)
		r.closeSource()
		r.openAtUS = r.clock.NowUS() + uint64(backoff(&r.openRetry)/time.Microsecond)
		return r.pause(wire.PausedDisplayUnavailable)
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
// one again for a key frame request, or the last one again after
// keepalive without an access unit; within the pacing of max_fps.
func (r *run) due(now uint64) bool {
	if !r.haveLast {
		return false
	}
	if !r.pending && (r.forceKey || (r.enc != nil && now >= r.lastEncodeUS+uint64(keepalive/time.Microsecond))) {
		r.pending, r.repeat = true, true
	}
	return r.pending && r.refill(now) >= 1 && now >= r.encAtUS
}

// timeout is how long Next may wait: until the next pacing slot of a
// pending image, the keepalive of a static one, or the next stats frame,
// and at most poll.
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
	}
	return d
}

// encode encodes the pending image, making the encoder first when there
// is none for the image's size and the settings.
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
	if r.needInfo {
		if err := r.writeInfo(f); err != nil {
			return err
		}
	}
	r.stats.submit(f.MonoUS, now)
	r.inflight.Add(1)
	err := r.enc.Encode(f, r.forceKey)
	if err != nil {
		r.inflight.Add(-1)
	}
	switch {
	case err == nil:
		r.pending, r.repeat, r.forceKey = false, false, false
		r.lastEncodeUS, r.busySinceUS, r.encRetry = now, 0, 0
		r.tokens--
	case errors.Is(err, encode.ErrBusy):
		r.stats.forget(f.MonoUS)
		r.stats.drop()
		r.encAtUS = now + r.interval()/2
		if r.busySinceUS == 0 {
			r.busySinceUS = now
		} else if now-r.busySinceUS > uint64(busyLimit/time.Microsecond) {
			r.log.Printf("video: %s took no frame for %v; making it again", r.enc.Info().Name, busyLimit)
			r.closeEncoder()
			r.busySinceUS = 0
		}
	default:
		r.stats.forget(f.MonoUS)
		r.log.Printf("video: %s failed: %v; making it again", r.enc.Info().Name, err)
		r.closeEncoder()
		r.encAtUS = now + uint64(backoff(&r.encRetry)/time.Microsecond)
	}
	return nil
}

// makeEncoder replaces the encoder with one for f at outW×outH. A PC
// without an encoder is a refusal before anything was written; another
// failure is a paused frame and a later retry.
func (r *run) makeEncoder(f capture.Frame, outW, outH int, now uint64) error {
	r.closeEncoder()
	bps := r.set.bitrate
	if bps == 0 {
		bps = DefaultBitrate(outW)
	}
	enc, err := r.p.opt.NewEncoder(r.src.Device(), f.Width, f.Height, outW, outH, r.set.maxFPS, bps)
	if err != nil {
		if !r.hasWritten() && (errors.Is(err, encode.ErrNoEncoder) || errors.Is(err, encode.ErrUnsupported)) {
			return &Refusal{Code: wire.CodeCapabilityUnsupported, Message: err.Error()}
		}
		r.log.Printf("video: no encoder for display %d at %dx%d: %v", r.set.display, outW, outH, err)
		r.encAtUS = now + uint64(backoff(&r.encRetry)/time.Microsecond)
		return r.pause(wire.PausedDisplayUnavailable)
	}
	info := enc.Info()
	r.log.Printf("video: display %d %dx%d to %dx%d at %d fps, %d bps on %s (hardware %v)", r.set.display, f.Width, f.Height,
		outW, outH, r.set.maxFPS, bps, info.Name, info.Hardware)
	r.enc, r.encSrcW, r.encSrcH, r.encW, r.encH, r.encFPS, r.encBPS = enc, f.Width, f.Height, outW, outH, r.set.maxFPS, bps
	r.needInfo, r.forceKey = true, true
	r.fwdStop, r.fwdDone = make(chan struct{}), make(chan struct{})
	go r.forward(enc, r.fwdStop, r.fwdDone)
	return nil
}

// forward writes the access units of enc until its output closes or stop
// closes.
func (r *run) forward(enc encode.Encoder, stop, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		case au, ok := <-enc.Output():
			if !ok {
				return
			}
			au.Seq = r.seq.Add(1)
			r.stats.sent(au)
			if r.write(wire.KindAU, au.Marshal()) != nil {
				return
			}
			r.inflight.Add(-1)
		}
	}
}

// writeInfo writes the info frame of the encoder for frames like f.
func (r *run) writeInfo(f capture.Frame) error {
	method := capture.MethodGDI
	if f.Texture != 0 {
		method = capture.MethodDD
	}
	info := r.enc.Info()
	r.needInfo, r.paused = false, ""
	return r.writeJSON(wire.KindVideoInfo, wire.VideoInfo{Codec: "h264", Width: r.encW, Height: r.encH, SrcWidth: f.Width, SrcHeight: f.Height,
		FPS: r.encFPS, Bitrate: r.encBPS, Encoder: info.Name, Hardware: info.Hardware, Capture: method, Display: r.set.display})
}

// maybeStats writes the stats frame of the second that ended, while
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
	return r.writeJSON(wire.KindVideoStats, s)
}
