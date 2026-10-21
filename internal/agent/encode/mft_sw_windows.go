//go:build windows

package encode

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// swBuffers is the number of NV12 memory buffers frames are read back
// into.
const swBuffers = 3

// swInput is one frame queued for the software encoder: buffer buf of the
// pool at sample time t.
type swInput struct {
	buf         int
	t           int64
	captureMono uint64
	forceKey    bool
}

type bitrateRequest struct {
	bps   int
	reply chan error
}

// swEncoder drives the synchronous software H.264 encoder MFT on a thread
// of its own. Encode converts the frame with the video processor, reads
// the NV12 image back into a free memory buffer of the pool and queues it;
// the encoder thread gives it to the MFT and takes the output at once.
type swEncoder struct {
	dev          *d3d.Device
	conv         *converter
	target, view uintptr
	stage        uintptr
	bufs         [swBuffers]struct {
		buf  uintptr
		busy atomic.Bool
	}
	format format
	clock  sampleClock
	// queued counts the frames queued or in the MFT whose access units did
	// not come out yet.
	queued atomic.Int64

	in        chan swInput
	bitrate   chan bitrateRequest
	out       chan wire.AU
	ready     chan error
	closing   chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	mu   sync.Mutex
	info EncoderInfo
	err  error
}

func newSW(dev *d3d.Device, srcW, srcH, outW, outH, fps, bps int) (*swEncoder, error) {
	if err := checkSizes(srcW, srcH, outW, outH, fps, bps); err != nil {
		return nil, err
	}
	if err := startup(); err != nil {
		return nil, err
	}
	e := &swEncoder{dev: dev, format: format{w: outW, h: outH, fps: fps, bps: bps, level: h264Level(outW, outH, fps, bps)},
		in: make(chan swInput, 1), bitrate: make(chan bitrateRequest), out: make(chan wire.AU, outputRoom), ready: make(chan error, 1),
		closing: make(chan struct{}), done: make(chan struct{})}
	ok := false
	defer func() {
		if !ok {
			e.release()
		}
	}()
	var err error
	if e.conv, err = newConverter(dev, srcW, srcH, outW, outH, fps); err != nil {
		return nil, err
	}
	if e.target, e.view, err = e.conv.newTarget(); err != nil {
		return nil, err
	}
	if e.stage, err = dev.NewTexture(outW, outH, d3d.FormatNV12, d3d.UsageStaging, 0, d3d.CPUAccessRead); err != nil {
		return nil, err
	}
	for i := range e.bufs {
		if e.bufs[i].buf, err = newMemoryBuffer(outW * outH * 3 / 2); err != nil {
			return nil, err
		}
	}
	go e.loop()
	if err := <-e.ready; err != nil {
		<-e.done
		return nil, err
	}
	ok = true
	return e, nil
}

// release frees what newSW made on the calling thread.
func (e *swEncoder) release() {
	for i := range e.bufs {
		com.Release(e.bufs[i].buf)
		e.bufs[i].buf = 0
	}
	com.Release(e.stage)
	com.Release(e.view)
	com.Release(e.target)
	e.stage, e.view, e.target = 0, 0, 0
	if e.conv != nil {
		e.conv.close()
	}
}

func (e *swEncoder) Info() EncoderInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.info
}

func (e *swEncoder) Output() <-chan wire.AU { return e.out }

func (e *swEncoder) Pending() int { return int(e.queued.Load()) + len(e.out) }

func (e *swEncoder) failure() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	return errors.New("the software encoder stopped; make it again")
}

func (e *swEncoder) fail(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err == nil {
		e.err = err
	}
}

// freeBuffer returns the index of a pool buffer that neither the queue nor
// the MFT holds, or -1.
func (e *swEncoder) freeBuffer() int {
	for i := range e.bufs {
		if e.bufs[i].busy.Load() {
			continue
		}
		com.AddRef(e.bufs[i].buf)
		if refs := com.CallRaw(e.bufs[i].buf, 2); refs == 1 {
			return i
		}
	}
	return -1
}

func (e *swEncoder) Encode(f capture.Frame, forceKey bool) error {
	if f.Width != e.conv.srcW || f.Height != e.conv.srcH {
		return fmt.Errorf("a %dx%d frame for an encoder of %dx%d frames; make the encoder again for the new size", f.Width, f.Height, e.conv.srcW, e.conv.srcH)
	}
	select {
	case <-e.done:
		return e.failure()
	default:
	}
	if len(e.in) > 0 {
		return ErrBusy
	}
	i := e.freeBuffer()
	if i < 0 {
		return ErrBusy
	}
	if err := e.conv.convert(f, e.view); err != nil {
		return err
	}
	if err := e.readBack(e.bufs[i].buf); err != nil {
		return err
	}
	e.bufs[i].busy.Store(true)
	e.queued.Add(1)
	select {
	case e.in <- swInput{buf: i, t: e.clock.next(f.MonoUS), captureMono: f.MonoUS, forceKey: forceKey}:
		return nil
	default:
		e.queued.Add(-1)
		e.bufs[i].busy.Store(false)
		return ErrBusy
	}
}

// readBack copies the NV12 target into buf, rows packed.
func (e *swEncoder) readBack(buf uintptr) error {
	e.dev.CopyResource(e.stage, e.target)
	m, err := e.dev.Map(e.stage, d3d.MapRead)
	if err != nil {
		return err
	}
	defer e.dev.Unmap(e.stage)
	w, h, pitch := e.format.w, e.format.h, int(m.RowPitch)
	size := w * h * 3 / 2
	var p *byte
	var maxLen, cur uint32
	if hr := com.Call(buf, bufferLock, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&maxLen)), uintptr(unsafe.Pointer(&cur))); hr.Failed() {
		return fmt.Errorf("IMFMediaBuffer::Lock: %w", hr)
	}
	dst := unsafe.Slice(p, size)
	src := unsafe.Slice((*byte)(m.Data), pitch*h*3/2)
	for y := 0; y < h*3/2; y++ {
		copy(dst[y*w:(y+1)*w], src[y*pitch:y*pitch+w])
	}
	com.Call(buf, bufferUnlock)
	com.Call(buf, bufferSetCurrentLength, uintptr(size))
	return nil
}

func (e *swEncoder) SetBitrate(bps int) error {
	if bps <= 0 {
		return fmt.Errorf("bitrate %d is not positive", bps)
	}
	r := bitrateRequest{bps: bps, reply: make(chan error, 1)}
	select {
	case e.bitrate <- r:
	case <-e.done:
		return e.failure()
	}
	return <-r.reply
}

// swThread is the state of the encoder thread.
type swThread struct {
	entries   []mftEntry
	act, mft  uintptr
	api       codecAPI
	outBuf    uintptr
	outSample uintptr
	queue     []pending
}

// loop sets the MFT up, reports the result on ready and serves the queue
// until Close or a failure.
func (e *swEncoder) loop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(e.done)
	defer close(e.out)
	leave, err := InitThread()
	if err != nil {
		e.ready <- err
		return
	}
	defer leave()
	var st swThread
	defer st.release()
	if err := e.setUp(&st); err != nil {
		e.ready <- err
		return
	}
	e.ready <- nil
	for {
		select {
		case <-e.closing:
			e.finish(&st)
			return
		case r := <-e.bitrate:
			err := st.api.setBitrate(r.bps)
			if err == nil {
				e.format.bps = r.bps
			}
			r.reply <- err
		case in := <-e.in:
			if err := e.encode(&st, in); err != nil {
				e.fail(err)
				return
			}
		}
	}
}

// setUp activates the first software encoder MFT that sets up and
// prepares it for streaming.
func (e *swEncoder) setUp(st *swThread) error {
	entries, err := enumEncoders(enumSyncMFT | enumLocalMFT | enumSortAndFilter)
	if err != nil {
		return err
	}
	st.entries = entries
	var tried []string
	for _, entry := range entries {
		if entry.hardware {
			continue
		}
		err := e.activate(st, entry)
		if err == nil {
			e.mu.Lock()
			e.info = EncoderInfo{Name: entry.name, Hardware: false, Width: e.format.w, Height: e.format.h}
			e.mu.Unlock()
			return nil
		}
		tried = append(tried, fmt.Sprintf("%s: %v", entry.name, err))
		st.tearDown()
	}
	if len(tried) == 0 {
		return errors.New("no software H.264 encoder MFT is registered")
	}
	return fmt.Errorf("no software H.264 encoder accepts %dx%d at %d fps (%s)", e.format.w, e.format.h, e.format.fps, strings.Join(tried, "; "))
}

// activate activates the MFT of entry: low latency, codec properties,
// output type, input type, an output sample, streaming.
func (e *swEncoder) activate(st *swThread, entry mftEntry) error {
	if hr := com.Call(entry.act, activateActivateObject, uintptr(unsafe.Pointer(&iidIMFTransform)), uintptr(unsafe.Pointer(&st.mft))); hr.Failed() {
		st.mft = 0
		return fmt.Errorf("IMFActivate::ActivateObject: %w", hr)
	}
	st.act = entry.act
	var attrs uintptr
	if hr := com.Call(st.mft, transformGetAttributes, uintptr(unsafe.Pointer(&attrs))); hr.Succeeded() && attrs != 0 {
		setUINT32(attrs, &mfLowLatency, 1)
		com.Release(attrs)
	}
	if st.api = codecAPIOf(st.mft); st.api == 0 {
		return errors.New("the MFT has no ICodecAPI")
	}
	st.api.configure(e.format.bps, swGOP)
	if err := setTypes(st.mft, e.format); err != nil {
		return err
	}
	st.api.configure(e.format.bps, swGOP)
	var osi outputStreamInfo
	com.Call(st.mft, transformGetOutputStreamInfo, 0, uintptr(unsafe.Pointer(&osi)))
	var err error
	if st.outBuf, err = newMemoryBuffer(max(int(osi.size), e.format.w*e.format.h*3/2)); err != nil {
		return err
	}
	if st.outSample, err = newSample(st.outBuf, 0, 0); err != nil {
		return err
	}
	if hr := processMessage(st.mft, msgNotifyBeginStreaming, 0); hr.Failed() {
		return fmt.Errorf("MFT_MESSAGE_NOTIFY_BEGIN_STREAMING: %w", hr)
	}
	if hr := processMessage(st.mft, msgNotifyStartOfStream, 0); hr.Failed() {
		return fmt.Errorf("MFT_MESSAGE_NOTIFY_START_OF_STREAM: %w", hr)
	}
	return nil
}

// encode gives the queued frame to the MFT and sends its output.
func (e *swEncoder) encode(st *swThread, in swInput) error {
	defer e.bufs[in.buf].busy.Store(false)
	if in.forceKey {
		if err := st.api.forceKeyFrame(); err != nil {
			return err
		}
	}
	s, err := newSample(e.bufs[in.buf].buf, in.t, int64(hundredNS/e.format.fps))
	if err != nil {
		return err
	}
	st.queue = append(st.queue, pending{time: in.t, captureMono: in.captureMono})
	hr := com.Call(st.mft, transformProcessInput, 0, s, 0)
	if hr == com.MF_E_NOTACCEPTING {
		if err := e.pull(st); err != nil {
			com.Release(s)
			return err
		}
		hr = com.Call(st.mft, transformProcessInput, 0, s, 0)
	}
	com.Release(s)
	if hr.Failed() {
		if n := len(st.queue); n > 0 && st.queue[n-1].time == in.t {
			st.queue = st.queue[:n-1]
			e.queued.Add(-1)
		}
		return fmt.Errorf("IMFTransform::ProcessInput: %w", hr)
	}
	return e.pull(st)
}

// pull takes the outputs of the MFT until it needs more input.
func (e *swEncoder) pull(st *swThread) error {
	for {
		com.Call(st.outBuf, bufferSetCurrentLength, 0)
		ob := outputDataBuffer{sample: st.outSample}
		var status uint32
		hr := com.Call(st.mft, transformProcessOutput, 0, 1, uintptr(unsafe.Pointer(&ob)), uintptr(unsafe.Pointer(&status)))
		doneMono := monoclock.NowUS()
		com.Release(ob.events)
		switch {
		case hr == com.MF_E_TRANSFORM_NEED_MORE_INPUT:
			return nil
		case hr == com.MF_E_TRANSFORM_STREAM_CHANGE:
			if err := renegotiate(st.mft); err != nil {
				return err
			}
			continue
		case hr.Failed():
			return fmt.Errorf("IMFTransform::ProcessOutput: %w", hr)
		}
		data, err := sampleData(st.outSample)
		if err != nil {
			return err
		}
		t := sampleTime(st.outSample)
		mono := doneMono
		for len(st.queue) > 0 && st.queue[0].time <= t {
			if st.queue[0].time == t {
				mono = st.queue[0].captureMono
			}
			st.queue = st.queue[1:]
			e.queued.Add(-1)
		}
		au := accessUnit(data, st.mft, e.format.w, e.format.h, mono, doneMono)
		select {
		case e.out <- au:
		case <-e.closing:
		}
	}
}

// finish drains the MFT, discarding its last outputs, and ends streaming.
func (e *swEncoder) finish(st *swThread) {
	processMessage(st.mft, msgNotifyEndOfStream, 0)
	processMessage(st.mft, msgCommandDrain, 0)
	_ = e.pull(st)
	processMessage(st.mft, msgNotifyEndStreaming, 0)
}

// tearDown releases the MFT activate activated.
func (st *swThread) tearDown() {
	com.Release(st.outSample)
	com.Release(st.outBuf)
	st.api.release()
	com.Release(st.mft)
	if st.act != 0 {
		com.Call(st.act, activateShutdownObject)
	}
	st.outSample, st.outBuf, st.api, st.mft, st.act = 0, 0, 0, 0, 0
}

func (st *swThread) release() {
	st.tearDown()
	releaseEntries(st.entries)
	*st = swThread{}
}

// Close stops the encoder thread, discarding the access units still to
// come, and releases everything.
func (e *swEncoder) Close() {
	e.closeOnce.Do(func() {
		close(e.closing)
		select {
		case <-e.done:
		case <-time.After(5 * time.Second):
			return
		}
		e.release()
	})
}
