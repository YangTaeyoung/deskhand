//go:build windows

package capture

import (
	"context"
	"errors"
	"fmt"
	"image"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
)

// firstImageWait bounds the wait for the first image of a duplication.
const firstImageWait = 500 * time.Millisecond

// deviceIdle is how long the still worker keeps a device it did not use.
const deviceIdle = time.Minute

// stillMu lets one still capture run at a time.
var stillMu sync.Mutex

// still is a captured image before encoding.
type still struct {
	img    *image.RGBA
	srcW   int
	mono   uint64
	method string
	disp   display.Info
}

// CaptureStill takes one image of the display with index displayIndex,
// fitted to maxWidth (0 keeps the display's width), and encodes it as
// format, FormatPNG or FormatJPEG with quality 1–100. Desktop Duplication
// gets its first image within firstImageWait, else GDI takes it. While
// the screen reports access_lost it tries again for up to
// accessLostGrace.
func CaptureStill(displayIndex int, format string, maxWidth, quality int) (StillResult, error) {
	if format != FormatPNG && format != FormatJPEG {
		return StillResult{}, fmt.Errorf("format %q is neither %s nor %s", format, FormatPNG, FormatJPEG)
	}
	if format == FormatJPEG && (quality < 1 || quality > 100) {
		return StillResult{}, fmt.Errorf("JPEG quality %d is not within 1 to 100", quality)
	}
	if maxWidth < 0 {
		return StillResult{}, fmt.Errorf("max_width %d is negative", maxWidth)
	}
	stillMu.Lock()
	st, err := grabStill(displayIndex, maxWidth)
	stillMu.Unlock()
	if err != nil {
		return StillResult{}, err
	}
	data, err := encodeStill(st.img, format, quality)
	if err != nil {
		return StillResult{}, err
	}
	b := st.img.Bounds()
	return StillResult{CaptureID: newCaptureID(), Data: data, Width: b.Dx(), Height: b.Dy(), Scale: float64(b.Dx()) / float64(st.srcW),
		OriginX: st.disp.X, OriginY: st.disp.Y, DPI: st.disp.DPI, MonoUS: st.mono, Method: st.method}, nil
}

// grabStill captures the display on the still worker, retrying while it
// reports access_lost.
func grabStill(index, maxWidth int) (still, error) {
	start := time.Now()
	wait := 2 * firstRetry
	for {
		var st still
		err := onStillWorker(func(c *devCache) error {
			openMu.Lock()
			defer openMu.Unlock()
			var err error
			st, err = c.grab(index, maxWidth)
			return err
		})
		var u *Unavailable
		if !errors.As(err, &u) || u.Reason != ReasonAccessLost || time.Since(start)+wait > accessLostGrace {
			return st, err
		}
		time.Sleep(wait)
		wait = min(2*wait, maxRetry)
	}
}

// stillJobs takes the jobs of the still worker: one goroutine locked to its
// thread that keeps a device per display between stills and closes them
// after deviceIdle without a job.
var stillJobs = sync.OnceValue(func() chan<- func(*devCache) {
	jobs := make(chan func(*devCache))
	go func() {
		runtime.LockOSThread()
		c := &devCache{devs: map[display.Info]*keptDevice{}}
		idle := time.NewTimer(deviceIdle)
		for {
			select {
			case job := <-jobs:
				job(c)
				idle.Reset(deviceIdle)
			case <-idle.C:
				c.closeAll()
			}
		}
	}()
	return jobs
})

// onStillWorker runs fn on the still worker and returns its error; a panic
// of fn closes the worker's devices and becomes the error.
func onStillWorker(fn func(*devCache) error) error {
	done := make(chan error, 1)
	stillJobs() <- func(c *devCache) {
		defer func() {
			if r := recover(); r != nil {
				c.closeAll()
				done <- fmt.Errorf("the still capture failed internally: %v", r)
			}
		}()
		done <- fn(c)
	}
	return <-done
}

// devCache holds the still worker's devices, one per display state, each
// with the staging texture its stills copy the desktop image into.
type devCache struct {
	devs map[display.Info]*keptDevice
}

// keptDevice is a kept device with its staging texture of w×h pixels, 0
// before the first still.
type keptDevice struct {
	dev   *d3d.Device
	stage uintptr
	w, h  int
}

// staging returns the staging texture of w×h pixels, making it when the
// kept one has another size.
func (k *keptDevice) staging(w, h int) (uintptr, error) {
	if k.stage != 0 && k.w == w && k.h == h {
		return k.stage, nil
	}
	tex, err := k.dev.NewTexture(w, h, d3d.FormatBGRA, d3d.UsageStaging, 0, d3d.CPUAccessRead)
	if err != nil {
		return 0, err
	}
	com.Release(k.stage)
	k.stage, k.w, k.h = tex, w, h
	return tex, nil
}

func (k *keptDevice) close() {
	com.Release(k.stage)
	k.stage = 0
	k.dev.Close()
}

// device returns the kept device of d, or a new one, which it keeps; fresh
// reports a new one. A kept device of an earlier state of the display is
// closed.
func (c *devCache) device(d display.Info) (k *keptDevice, fresh bool, err error) {
	if k := c.devs[d]; k != nil {
		return k, false, nil
	}
	for state := range c.devs {
		if state.Name == d.Name {
			c.drop(state)
		}
	}
	dev, err := d3d.NewDeviceForOutput(d.Name)
	if err != nil {
		return nil, false, err
	}
	k = &keptDevice{dev: dev}
	c.devs[d] = k
	return k, true, nil
}

func (c *devCache) drop(d display.Info) {
	if k := c.devs[d]; k != nil {
		k.close()
		delete(c.devs, d)
	}
}

func (c *devCache) closeAll() {
	for d := range c.devs {
		c.drop(d)
	}
}

// grab captures display index on its kept device. When a kept device
// fails for a reason other than the session or the display, it captures
// once more on a new device.
func (c *devCache) grab(index, maxWidth int) (still, error) {
	d, err := findDisplay(index)
	if err != nil {
		return still{}, err
	}
	for retried := false; ; retried = true {
		k, fresh, err := c.device(d)
		if err != nil {
			return still{}, classifyErr(err)
		}
		st, err := grabOn(k, d, maxWidth)
		var u *Unavailable
		if err == nil || fresh || retried || errors.As(err, &u) {
			return st, err
		}
		c.drop(d)
	}
}

// grabOn takes the first image of display d with a still source on the
// kept device k, and with GDI when the duplication gives none in time.
func grabOn(k *keptDevice, d display.Info, maxWidth int) (still, error) {
	src, err := openOn(k.dev, d, false, k.staging)
	if err != nil {
		return still{}, err
	}
	defer src.Close()
	switch s := src.(type) {
	case *ddSource:
		st, err := s.still(maxWidth)
		if !errors.Is(err, ErrNoNewFrame) {
			return st, err
		}
		g, err := openGDI(nil, d, 1)
		if err != nil {
			return still{}, classifyErr(fmt.Errorf("%w (no duplicated image within %v either)", err, firstImageWait))
		}
		defer g.Close()
		return g.still(maxWidth)
	case *gdiSource:
		return s.still(maxWidth)
	}
	return still{}, fmt.Errorf("source %T has no still image", src)
}

// still waits up to firstImageWait for the first image and reads it from
// the staging texture, fitted to maxWidth. ErrNoNewFrame means no image
// came.
func (s *ddSource) still(maxWidth int) (still, error) {
	deadline := time.Now().Add(firstImageWait)
	for {
		f, err := s.Next(context.Background(), time.Until(deadline))
		switch {
		case err == nil && f.Dirty:
			img, err := s.readback(maxWidth)
			if err != nil {
				return still{}, err
			}
			return still{img: img, srcW: s.w, mono: f.MonoUS, method: MethodDD, disp: s.disp}, nil
		case err != nil && !errors.Is(err, ErrNoNewFrame) && !errors.Is(err, ErrReset):
			return still{}, err
		}
		if !time.Now().Before(deadline) {
			return still{}, ErrNoNewFrame
		}
	}
}

// readback maps the staging texture the desktop image was copied into and
// scales its pixels.
func (s *ddSource) readback(maxWidth int) (*image.RGBA, error) {
	m, err := s.dev.Map(s.tex, d3d.MapRead)
	if err != nil {
		return nil, err
	}
	defer s.dev.Unmap(s.tex)
	pitch := int(m.RowPitch)
	src := unsafe.Slice((*byte)(m.Data), pitch*(s.h-1)+s.w*4)
	ow, oh := scaleSize(s.w, s.h, maxWidth)
	return scaleBGRA(src, pitch, s.w, s.h, ow, oh), nil
}

// still takes the first frame of the source, fitted to maxWidth.
func (s *gdiSource) still(maxWidth int) (still, error) {
	f, err := s.Next(context.Background(), firstImageWait)
	if err != nil {
		return still{}, err
	}
	ow, oh := scaleSize(f.Width, f.Height, maxWidth)
	return still{img: scaleBGRA(f.CPU, f.Width*4, f.Width, f.Height, ow, oh), srcW: f.Width, mono: f.MonoUS, method: MethodGDI, disp: s.disp}, nil
}
