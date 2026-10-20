//go:build windows

package encode

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// hwSlots is the number of NV12 textures frames are converted into; a
	// slot is free again once the encoder returned its access unit.
	hwSlots = 4
	// inputWait bounds the wait of Encode for the encoder to ask for input.
	inputWait = 50 * time.Millisecond
	// drainWait and shutdownWait bound the waits of Close for the encoder's
	// event loop to end after a drain and after a shutdown.
	drainWait    = time.Second
	shutdownWait = time.Second
)

// pending is a frame given to the encoder whose access unit has not come
// back.
type pending struct {
	time        int64
	slot        int
	captureMono uint64
}

// hwEncoder drives an asynchronous hardware encoder MFT: Encode converts
// the frame into a free NV12 slot and gives it to the encoder when the
// encoder asks for input; the event loop, on a thread of its own, counts
// the requests for input and turns each output into an access unit.
type hwEncoder struct {
	dev     *d3d.Device
	conv    *converter
	slots   [hwSlots]struct{ tex, view uintptr }
	free    chan int
	entries []mftEntry
	act     uintptr
	mgr     uintptr
	mft     uintptr
	attrs   uintptr
	events  uintptr
	api     codecAPI
	info    EncoderInfo
	format  format
	// provides reports that the encoder allocates its output samples;
	// outSize is the size of an output sample the encoder does not.
	provides bool
	outSize  int

	needInput chan struct{}
	out       chan wire.AU
	closing   chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	mu    sync.Mutex
	queue []pending
	err   error
	clock sampleClock
}

// newHW sets up the first hardware encoder MFT that accepts the device,
// those of the adapter's vendor first.
func newHW(dev *d3d.Device, srcW, srcH, outW, outH, fps, bps int) (*hwEncoder, error) {
	if err := checkSizes(srcW, srcH, outW, outH, fps, bps); err != nil {
		return nil, err
	}
	if err := startup(); err != nil {
		return nil, err
	}
	entries, err := enumEncoders(enumHardware | enumSortAndFilter)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("no hardware H.264 encoder MFT is registered")
	}
	e := &hwEncoder{dev: dev, entries: entries, free: make(chan int, hwSlots), needInput: make(chan struct{}, 64),
		out: make(chan wire.AU, 16), closing: make(chan struct{}), done: make(chan struct{}),
		format: format{w: outW, h: outH, fps: fps, bps: bps, level: h264Level(outW, outH, fps, bps)}}
	ok := false
	defer func() {
		if !ok {
			e.release()
		}
	}()
	if e.conv, err = newConverter(dev, srcW, srcH, outW, outH, fps); err != nil {
		return nil, err
	}
	for i := range e.slots {
		tex, view, err := e.conv.newTarget()
		if err != nil {
			return nil, err
		}
		e.slots[i].tex, e.slots[i].view = tex, view
		e.free <- i
	}
	var token uint32
	r, _, _ := procMFCreateDXGIDeviceManager.Call(uintptr(unsafe.Pointer(&token)), uintptr(unsafe.Pointer(&e.mgr)))
	if hr := com.HRESULT(uint32(r)); hr.Failed() {
		return nil, fmt.Errorf("MFCreateDXGIDeviceManager: %w", hr)
	}
	if hr := com.Call(e.mgr, deviceManagerResetDevice, dev.Device, uintptr(token)); hr.Failed() {
		return nil, fmt.Errorf("IMFDXGIDeviceManager::ResetDevice: %w", hr)
	}
	var tried []string
	for _, entry := range vendorFirst(entries, dev.VendorID) {
		err := e.setUp(entry)
		if err == nil {
			ok = true
			go e.loop()
			return e, nil
		}
		tried = append(tried, fmt.Sprintf("%s: %v", entry.name, err))
		e.tearDown()
	}
	return nil, fmt.Errorf("no hardware H.264 encoder accepts %dx%d at %d fps on %s (%s)", outW, outH, fps, dev.AdapterName, strings.Join(tried, "; "))
}

// setUp activates the MFT of entry and prepares it for streaming:
// unlock, D3D manager, output type, input type, codec properties.
func (e *hwEncoder) setUp(entry mftEntry) error {
	if hr := com.Call(entry.act, activateActivateObject, uintptr(unsafe.Pointer(&iidIMFTransform)), uintptr(unsafe.Pointer(&e.mft))); hr.Failed() {
		e.mft = 0
		return fmt.Errorf("IMFActivate::ActivateObject: %w", hr)
	}
	e.act = entry.act
	if hr := com.Call(e.mft, transformGetAttributes, uintptr(unsafe.Pointer(&e.attrs))); hr.Failed() {
		e.attrs = 0
		return fmt.Errorf("IMFTransform::GetAttributes: %w", hr)
	}
	if async, hr := getUINT32(e.attrs, &mfTransformAsync); hr.Failed() || async == 0 {
		return errors.New("the MFT is not asynchronous")
	}
	if hr := setUINT32(e.attrs, &mfTransformAsyncUnlock, 1); hr.Failed() {
		return fmt.Errorf("MF_TRANSFORM_ASYNC_UNLOCK: %w", hr)
	}
	setUINT32(e.attrs, &mfLowLatency, 1)
	if hr := processMessage(e.mft, msgSetD3DManager, e.mgr); hr.Failed() {
		return fmt.Errorf("MFT_MESSAGE_SET_D3D_MANAGER: %w", hr)
	}
	e.api = codecAPIOf(e.mft)
	e.api.configure(e.format.bps, hwGOP)
	if err := setTypes(e.mft, e.format); err != nil {
		return err
	}
	e.api.configure(e.format.bps, hwGOP)
	var osi outputStreamInfo
	com.Call(e.mft, transformGetOutputStreamInfo, 0, uintptr(unsafe.Pointer(&osi)))
	e.provides = osi.flags&(outputProvidesSamples|outputCanProvideSamples) != 0
	e.outSize = max(int(osi.size), e.format.w*e.format.h*3/2)
	var hr com.HRESULT
	if e.events, hr = com.QueryInterface(e.mft, &iidIMFMediaEventGenerator); hr.Failed() {
		e.events = 0
		return fmt.Errorf("IMFTransform QueryInterface(IMFMediaEventGenerator): %w", hr)
	}
	if hr := processMessage(e.mft, msgNotifyBeginStreaming, 0); hr.Failed() {
		return fmt.Errorf("MFT_MESSAGE_NOTIFY_BEGIN_STREAMING: %w", hr)
	}
	if hr := processMessage(e.mft, msgNotifyStartOfStream, 0); hr.Failed() {
		return fmt.Errorf("MFT_MESSAGE_NOTIFY_START_OF_STREAM: %w", hr)
	}
	e.info = EncoderInfo{Name: entry.name, Hardware: true, Width: e.format.w, Height: e.format.h}
	return nil
}

// tearDown releases the MFT setUp activated.
func (e *hwEncoder) tearDown() {
	if e.mft != 0 {
		if sd, hr := com.QueryInterface(e.mft, &iidIMFShutdown); hr.Succeeded() {
			com.Call(sd, shutdownShutdown)
			com.Release(sd)
		}
	}
	e.api.release()
	com.Release(e.events)
	com.Release(e.attrs)
	com.Release(e.mft)
	if e.act != 0 {
		com.Call(e.act, activateShutdownObject)
	}
	e.api, e.events, e.attrs, e.mft, e.act = 0, 0, 0, 0, 0
}

// release frees everything newHW made.
func (e *hwEncoder) release() {
	e.tearDown()
	releaseEntries(e.entries)
	e.entries = nil
	com.Release(e.mgr)
	e.mgr = 0
	for i := range e.slots {
		com.Release(e.slots[i].view)
		com.Release(e.slots[i].tex)
		e.slots[i].view, e.slots[i].tex = 0, 0
	}
	if e.conv != nil {
		e.conv.close()
	}
}

func (e *hwEncoder) Info() EncoderInfo { return e.info }

func (e *hwEncoder) Output() <-chan wire.AU { return e.out }

// failure is the error that ended the event loop.
func (e *hwEncoder) failure() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	return errors.New("the hardware encoder stopped; make it again")
}

func (e *hwEncoder) Encode(f capture.Frame, forceKey bool) error {
	if f.Width != e.conv.srcW || f.Height != e.conv.srcH {
		return fmt.Errorf("a %dx%d frame for an encoder of %dx%d frames; make the encoder again for the new size", f.Width, f.Height, e.conv.srcW, e.conv.srcH)
	}
	select {
	case <-e.done:
		return e.failure()
	default:
	}
	var slot int
	select {
	case slot = <-e.free:
	default:
		return ErrBusy
	}
	timer := time.NewTimer(inputWait)
	defer timer.Stop()
	select {
	case <-e.needInput:
	case <-e.done:
		e.free <- slot
		return e.failure()
	case <-timer.C:
		e.free <- slot
		return ErrBusy
	}
	err := e.submit(f, slot, forceKey)
	if err != nil && !errors.Is(err, ErrBusy) {
		select {
		case e.needInput <- struct{}{}:
		default:
		}
	}
	return err
}

// submit converts f into slot and gives it to the encoder, which asked
// for input. On a failure the slot is free again.
func (e *hwEncoder) submit(f capture.Frame, slot int, forceKey bool) error {
	if err := e.conv.convert(f, e.slots[slot].view); err != nil {
		e.free <- slot
		return err
	}
	e.conv.wait()
	duration := int64(hundredNS / e.format.fps)
	e.mu.Lock()
	t := e.clock.next(f.MonoUS)
	e.queue = append(e.queue, pending{time: t, slot: slot, captureMono: f.MonoUS})
	e.mu.Unlock()
	s, err := textureSample(e.slots[slot].tex, t, duration)
	if err == nil && forceKey {
		err = e.api.forceKeyFrame()
	}
	if err == nil {
		hr := com.Call(e.mft, transformProcessInput, 0, s, 0)
		switch {
		case hr == com.MF_E_NOTACCEPTING:
			err = ErrBusy
		case hr.Failed():
			err = fmt.Errorf("IMFTransform::ProcessInput: %w", hr)
		}
	}
	com.Release(s)
	if err != nil {
		e.drop(t)
	}
	return err
}

// drop removes the pending frame of sample time t, which the encoder did
// not take, and frees its slot unless take freed it.
func (e *hwEncoder) drop(t int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, p := range e.queue {
		if p.time == t {
			e.queue = append(e.queue[:i], e.queue[i+1:]...)
			e.free <- p.slot
			return
		}
	}
}

func (e *hwEncoder) SetBitrate(bps int) error {
	if bps <= 0 {
		return fmt.Errorf("bitrate %d is not positive", bps)
	}
	if err := e.api.setBitrate(bps); err != nil {
		return err
	}
	e.format.bps = bps
	return nil
}

// loop serves the events of the MFT until it drained, failed or shut
// down.
func (e *hwEncoder) loop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(e.done)
	defer close(e.out)
	leave, err := InitThread()
	if err != nil {
		e.fail(err)
		return
	}
	defer leave()
	for {
		var ev uintptr
		if hr := com.Call(e.events, eventGeneratorGetEvent, 0, uintptr(unsafe.Pointer(&ev))); hr.Failed() {
			e.fail(fmt.Errorf("IMFMediaEventGenerator::GetEvent: %w", hr))
			return
		}
		var typ uint32
		var status com.HRESULT
		com.Call(ev, eventGetType, uintptr(unsafe.Pointer(&typ)))
		com.Call(ev, eventGetStatus, uintptr(unsafe.Pointer(&status)))
		com.Release(ev)
		if status.Failed() || typ == meError {
			e.fail(fmt.Errorf("the hardware encoder reported event %d with %w", typ, status))
			return
		}
		switch typ {
		case meTransformNeedInput:
			select {
			case e.needInput <- struct{}{}:
			default:
			}
		case meTransformHaveOutput:
			if err := e.pull(); err != nil {
				e.fail(err)
				return
			}
		case meTransformDrainComplete:
			return
		}
	}
}

func (e *hwEncoder) fail(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err == nil {
		e.err = err
	}
}

// pull takes the output the MFT announced and sends its access unit.
func (e *hwEncoder) pull() error {
	ob := outputDataBuffer{}
	if !e.provides {
		buf, err := newMemoryBuffer(e.outSize)
		if err != nil {
			return err
		}
		s, err := newSample(buf, 0, 0)
		com.Release(buf)
		if err != nil {
			return err
		}
		ob.sample = s
	}
	var status uint32
	hr := com.Call(e.mft, transformProcessOutput, 0, 1, uintptr(unsafe.Pointer(&ob)), uintptr(unsafe.Pointer(&status)))
	doneMono := monoclock.NowUS()
	defer func() {
		com.Release(ob.sample)
		com.Release(ob.events)
	}()
	switch {
	case hr == com.MF_E_TRANSFORM_STREAM_CHANGE:
		return renegotiate(e.mft)
	case hr == com.MF_E_TRANSFORM_NEED_MORE_INPUT:
		return nil
	case hr.Failed():
		return fmt.Errorf("IMFTransform::ProcessOutput: %w", hr)
	}
	data, err := sampleData(ob.sample)
	if err != nil {
		return err
	}
	p := e.take(sampleTime(ob.sample))
	au := accessUnit(data, e.mft, e.format.w, e.format.h, p.captureMono, doneMono)
	select {
	case e.out <- au:
	case <-e.closing:
	}
	return nil
}

// take removes the pending frame of the output at time t, and those
// before it, and frees their slots.
func (e *hwEncoder) take(t int64) pending {
	e.mu.Lock()
	defer e.mu.Unlock()
	for len(e.queue) > 0 && e.queue[0].time < t {
		e.free <- e.queue[0].slot
		e.queue = e.queue[1:]
	}
	if len(e.queue) > 0 && e.queue[0].time == t {
		p := e.queue[0]
		e.queue = e.queue[1:]
		e.free <- p.slot
		return p
	}
	return pending{time: t, captureMono: e.clock.mono(t)}
}

// Close drains the MFT, ends its event loop, discarding the access units
// still to come, and releases everything.
func (e *hwEncoder) Close() {
	e.closeOnce.Do(func() {
		close(e.closing)
		if !e.wait(0) {
			processMessage(e.mft, msgNotifyEndOfStream, 0)
			processMessage(e.mft, msgCommandDrain, 0)
			if !e.wait(drainWait) {
				if sd, hr := com.QueryInterface(e.mft, &iidIMFShutdown); hr.Succeeded() {
					com.Call(sd, shutdownShutdown)
					com.Release(sd)
				}
				if !e.wait(shutdownWait) {
					return
				}
			}
		}
		processMessage(e.mft, msgNotifyEndStreaming, 0)
		e.release()
	})
}

// wait reports whether the event loop ended within d.
func (e *hwEncoder) wait(d time.Duration) bool {
	if d == 0 {
		select {
		case <-e.done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-e.done:
		return true
	case <-timer.C:
		return false
	}
}

// accessUnit makes the access unit of the encoded data of a w×h frame,
// putting the SPS and PPS of mft's output type before an IDR that lacks
// them.
func accessUnit(data []byte, mft uintptr, w, h int, captureMono, doneMono uint64) wire.AU {
	key, config := describe(data)
	if key && !config {
		if seq := sequenceHeader(mft); len(seq) > 0 {
			data = append(seq, data...)
			key, config = describe(data)
		}
	}
	return wire.AU{Key: key, HasConfig: config, Width: uint32(w), Height: uint32(h), CaptureMonoUS: captureMono,
		EncodeDoneMonoUS: max(doneMono, captureMono), Data: data}
}
