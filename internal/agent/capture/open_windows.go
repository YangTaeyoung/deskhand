//go:build windows

package capture

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
)

// probeTTL is how long Probe keeps its answer.
const probeTTL = 5 * time.Second

// openMu is held while a source is created and for the whole of a still
// capture.
var openMu sync.Mutex

// Open opens a source of the display with index displayIndex of
// display.List on a device of its own: Desktop Duplication, or GDI when
// duplication is refused, unsupported or already open for the display in
// this process, or when the display is rotated. When neither works the
// error is an *Unavailable if the session or the failure names a reason.
// Call it on a goroutine locked to its thread and use the source there.
func Open(displayIndex int) (Source, error) {
	openMu.Lock()
	defer openMu.Unlock()
	d, err := findDisplay(displayIndex)
	if err != nil {
		return nil, err
	}
	dev, err := d3d.NewDeviceForOutput(d.Name)
	if err != nil {
		return nil, classifyErr(err)
	}
	src, err := openOn(dev, d, true)
	if err != nil {
		dev.Close()
	}
	return src, err
}

// openOn opens a source of display d on dev, which the source closes with
// itself when owned is set; on an error dev stays open.
func openOn(dev *d3d.Device, d display.Info, owned bool) (Source, error) {
	dd := &ddSource{dev: dev, disp: d, ownsDev: owned}
	hr, err := dd.duplicate()
	switch {
	case err == nil && hr.Succeeded():
		dd.reg = register()
		return dd, nil
	case err != nil && !errors.Is(err, errRotated):
		return nil, err
	case err == nil && !gdiFallback(hr):
		if u := classifyNow(hr); u != nil {
			return nil, u
		}
		return nil, fmt.Errorf("IDXGIOutput1::DuplicateOutput of %s: %w", d.Name, hr)
	}
	g, gerr := openGDI(dev, d)
	if gerr == nil {
		g.ownsDev = owned
		g.reg = register()
		return g, nil
	}
	why := hr
	if !accessDenied(why) {
		why = hresultOf(gerr)
	}
	if u := classifyNow(why); u != nil {
		return nil, u
	}
	return nil, fmt.Errorf("display %s cannot be captured: DuplicateOutput %v, GDI %v", d.Name, hr, gerr)
}

// gdiFallback reports whether a DuplicateOutput failure leaves GDI to
// try: access denied, duplication unsupported or at its limit, or the
// output duplicated already in this process (E_INVALIDARG).
func gdiFallback(hr com.HRESULT) bool {
	switch hr {
	case com.E_ACCESSDENIED, com.DXGI_ERROR_ACCESS_DENIED, com.DXGI_ERROR_UNSUPPORTED,
		com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE, com.E_INVALIDARG:
		return true
	}
	return false
}

// noDisplayError is the error for a display index the desktop does not
// have.
type noDisplayError struct{ index, count int }

func (e *noDisplayError) Error() string {
	return fmt.Sprintf("display %d does not exist on the PC, which has %d; call display.list and use one of its indexes", e.index, e.count)
}

func (e *noDisplayError) Is(target error) bool { return target == ErrNoDisplay }

// findDisplay returns the display with index of display.List, making the
// process DPI aware first.
func findDisplay(index int) (display.Info, error) {
	display.EnsureDPIAware()
	list, err := display.List()
	if err != nil {
		return display.Info{}, classifyErr(err)
	}
	if len(list) == 0 {
		return display.Info{}, &Unavailable{Reason: ReasonNoOutput, Detail: "no display is attached to the desktop"}
	}
	for _, d := range list {
		if d.Index == index {
			return d, nil
		}
	}
	return display.Info{}, &noDisplayError{index: index, count: len(list)}
}

// classifyNow is Classify with the session as it is now; without session
// information it classifies by hr alone.
func classifyNow(hr com.HRESULT) *Unavailable {
	info, err := session.Current()
	if err != nil {
		info = session.Info{SessionID: 1}
	}
	return Classify(hr, info)
}

// classifyErr returns the *Unavailable that the HRESULT or Win32 error in
// err and the session name, with err as its detail, or err itself.
func classifyErr(err error) error {
	var hr com.HRESULT
	if !errors.As(err, &hr) {
		hr = hresultOf(err)
	}
	if u := classifyNow(hr); u != nil {
		u.Detail = err.Error()
		return u
	}
	return err
}

// hresultOf turns a Win32 error into an HRESULT: E_ACCESSDENIED for
// ERROR_ACCESS_DENIED, HRESULT_FROM_WIN32 for another code and E_FAIL
// without one.
func hresultOf(err error) com.HRESULT {
	var errno syscall.Errno
	switch {
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return com.E_ACCESSDENIED
	case errors.As(err, &errno) && errno != 0:
		return com.HRESULT(0x80070000 | uint32(errno)&0xFFFF)
	}
	return com.E_FAIL
}

// entry is one open source in the registry, with the outage it reports.
type entry struct{ state atomic.Pointer[Unavailable] }

var registry struct {
	mu   sync.Mutex
	open map[*entry]struct{}
}

func register() *entry {
	e := &entry{}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.open == nil {
		registry.open = map[*entry]struct{}{}
	}
	registry.open[e] = struct{}{}
	return e
}

// set records the outage the source reports, nil when it captures; a nil
// entry, the one of an unregistered source, records nothing.
func (e *entry) set(u *Unavailable) {
	if e != nil {
		e.state.Store(u)
	}
}

func (e *entry) close() {
	if e == nil {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.open, e)
}

// openState reports whether a source is open and the outage one of them
// reports, if any.
func openState() (bool, *Unavailable) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for e := range registry.open {
		if u := e.state.Load(); u != nil {
			return true, u
		}
	}
	return len(registry.open) > 0, nil
}

var probeCache struct {
	mu  sync.Mutex
	at  time.Time
	err error
}

// Probe reports whether the screen can be captured now: nil when one
// pixel of the primary display can be copied, else an *Unavailable when
// the session or the failure names a reason, or the failure. While a
// source is open it answers that source's state. It keeps its answer for
// probeTTL.
func Probe() error {
	probeCache.mu.Lock()
	defer probeCache.mu.Unlock()
	if !probeCache.at.IsZero() && time.Since(probeCache.at) < probeTTL {
		return probeCache.err
	}
	err := probe()
	probeCache.at, probeCache.err = time.Now(), err
	return err
}

// forgetProbe drops the answer Probe keeps.
func forgetProbe() {
	probeCache.mu.Lock()
	defer probeCache.mu.Unlock()
	probeCache.at = time.Time{}
}

func probe() error {
	if info, err := session.Current(); err == nil && (info.SessionID == 0 || info.InputDesktop == session.DesktopWinlogon) {
		if u := Classify(com.S_OK, info); u != nil {
			return u
		}
	}
	if isOpen, u := openState(); isOpen {
		if u != nil {
			return u
		}
		return nil
	}
	d, err := findDisplay(0)
	if err != nil {
		return err
	}
	if err := copyPixel(d.X, d.Y); err != nil {
		return classifyErr(err)
	}
	return nil
}

var (
	kernel32                      = windows.NewLazySystemDLL("kernel32.dll")
	procQueryPerformanceCounter   = kernel32.NewProc("QueryPerformanceCounter")
	procQueryPerformanceFrequency = kernel32.NewProc("QueryPerformanceFrequency")
)

var qpcFrequency = sync.OnceValue(func() int64 {
	var f int64
	procQueryPerformanceFrequency.Call(uintptr(unsafe.Pointer(&f)))
	return f
})

// qpcMono converts the QueryPerformanceCounter reading qpc of the last
// second to the monoclock; an older or future reading gives now.
func qpcMono(qpc int64) uint64 {
	var now int64
	procQueryPerformanceCounter.Call(uintptr(unsafe.Pointer(&now)))
	mono := monoclock.NowUS()
	freq := qpcFrequency()
	if freq <= 0 || qpc > now || now-qpc > freq {
		return mono
	}
	ago := uint64((now - qpc) * 1_000_000 / freq)
	if ago > mono {
		return 0
	}
	return mono - ago
}
