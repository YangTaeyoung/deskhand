//go:build windows

package encode

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
)

// New makes an encoder of srcW×srcH frames into outW×outH access units at
// fps and bps on dev: a hardware encoder MFT when one accepts the device,
// else the software encoder MFT. Call it on a goroutine locked to its
// thread after InitThread.
func New(dev *d3d.Device, srcW, srcH, outW, outH, fps, bps int) (Encoder, error) {
	hw, hwErr := newHW(dev, srcW, srcH, outW, outH, fps, bps)
	if hwErr == nil {
		return hw, nil
	}
	sw, swErr := newSW(dev, srcW, srcH, outW, outH, fps, bps)
	if swErr == nil {
		return sw, nil
	}
	return nil, fmt.Errorf("%w (hardware: %v; software: %v)", ErrNoEncoder, hwErr, swErr)
}

// NewSoftware is New with the software encoder MFT only.
func NewSoftware(dev *d3d.Device, srcW, srcH, outW, outH, fps, bps int) (Encoder, error) {
	sw, err := newSW(dev, srcW, srcH, outW, outH, fps, bps)
	if err != nil {
		return nil, fmt.Errorf("%w (software: %v)", ErrNoEncoder, err)
	}
	return sw, nil
}

// InitThread makes the calling thread ready for encoders: a member of the
// multithreaded apartment with Media Foundation started. Call it on a
// goroutine locked to its thread and call leave there when done.
func InitThread() (leave func(), err error) {
	leave, err = com.InitMTA()
	if err != nil {
		return nil, err
	}
	if err := startup(); err != nil {
		leave()
		return nil, err
	}
	return leave, nil
}

// probeRetry is how long Probe keeps a failure.
const probeRetry = time.Minute

var probeCache struct {
	mu  sync.Mutex
	at  time.Time
	ok  bool
	err error
}

// Probe reports whether an encoder can be made: nil once one was made and
// closed again on a device of the first display, or, in a session without
// displays, once an H.264 encoder MFT is registered; else an error
// wrapping ErrNoEncoder. It keeps a success for the life of the process
// and a failure for probeRetry.
func Probe() error {
	probeCache.mu.Lock()
	defer probeCache.mu.Unlock()
	if probeCache.ok || (!probeCache.at.IsZero() && time.Since(probeCache.at) < probeRetry) {
		return probeCache.err
	}
	err := probe()
	probeCache.at, probeCache.ok, probeCache.err = time.Now(), err == nil, err
	return err
}

func probe() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	leave, err := InitThread()
	if err != nil {
		return fmt.Errorf("%w (%v)", ErrNoEncoder, err)
	}
	defer leave()
	registered := 0
	for _, flags := range []uint32{enumHardware | enumSortAndFilter, enumSyncMFT | enumLocalMFT | enumSortAndFilter} {
		entries, err := enumEncoders(flags)
		if err == nil {
			registered += len(entries)
			releaseEntries(entries)
		}
	}
	if registered == 0 {
		return ErrNoEncoder
	}
	outs, err := d3d.Outputs()
	if err != nil {
		return nil
	}
	for _, o := range outs {
		if !o.Attached {
			continue
		}
		dev, err := d3d.NewDeviceForOutput(o.Name)
		if err != nil {
			return nil
		}
		defer dev.Close()
		enc, err := New(dev, 640, 360, 640, 360, 30, 2_000_000)
		if err != nil {
			return err
		}
		enc.Close()
		return nil
	}
	return nil
}
