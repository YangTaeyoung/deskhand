package screen

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
)

var (
	user32                         = windows.NewLazySystemDLL("user32.dll")
	gdi32                          = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi                         = windows.NewLazySystemDLL("dwmapi.dll")
	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW           = user32.NewProc("RegisterClassExW")
	procUnregisterClassW           = user32.NewProc("UnregisterClassW")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDestroyWindow              = user32.NewProc("DestroyWindow")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	procPeekMessageW               = user32.NewProc("PeekMessageW")
	procTranslateMessage           = user32.NewProc("TranslateMessage")
	procDispatchMessageW           = user32.NewProc("DispatchMessageW")
	procDefWindowProcW             = user32.NewProc("DefWindowProcW")
	procGetDC                      = user32.NewProc("GetDC")
	procReleaseDC                  = user32.NewProc("ReleaseDC")
	procFillRect                   = user32.NewProc("FillRect")
	procCreateSolidBrush           = gdi32.NewProc("CreateSolidBrush")
	procDeleteObject               = gdi32.NewProc("DeleteObject")
	procDwmFlush                   = dwmapi.NewProc("DwmFlush")
	procGetModuleHandleW           = kernel32.NewProc("GetModuleHandleW")
)

const (
	wsPopup           = 0x80000000
	wsExTopmost       = 0x00000008
	wsExTransparent   = 0x00000020
	wsExToolWindow    = 0x00000080
	wsExLayered       = 0x00080000
	wsExNoActivate    = 0x08000000
	lwaAlpha          = 0x2
	swShowNoActivate  = 4
	pmRemove          = 1
	motionCell        = 16
	motionCellsPerRow = 4
)

// wndClassEx is WNDCLASSEXW.
type wndClassEx struct {
	size, style                     uint32
	wndProc                         uintptr
	clsExtra, wndExtra              int32
	instance, icon, cursor, bgBrush uintptr
	menuName, className             *uint16
	iconSm                          uintptr
}

type winRect struct{ left, top, right, bottom int32 }

type winMsg struct {
	hwnd           uintptr
	message        uint32
	wParam, lParam uintptr
	time           uint32
	x, y           int32
	private        uint32
}

// motionEnv names the window a test binary run by startMotion shows:
// "x,y,w,h,seconds".
const motionEnv = "DESKHAND_MOTION_WINDOW"

func TestMain(m *testing.M) {
	if spec := os.Getenv(motionEnv); spec != "" {
		os.Exit(runMotion(spec))
	}
	os.Exit(m.Run())
}

// runMotion shows the motion window spec names for its seconds and
// returns the exit code.
func runMotion(spec string) int {
	var x, y, w, h, secs int
	if _, err := fmt.Sscanf(spec, "%d,%d,%d,%d,%d", &x, &y, &w, &h, &secs); err != nil {
		return 2
	}
	display.EnsureDPIAware()
	stop, err := motionWindow(x, y, w, h)
	if err != nil {
		return 1
	}
	time.Sleep(time.Duration(secs) * time.Second)
	stop()
	return 0
}

// startMotion runs the test binary again to show the motion window in a
// process of its own for secs seconds; it ends with the test.
func startMotion(t *testing.T, x, y, w, h, secs int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%d,%d,%d,%d,%d", motionEnv, x, y, w, h, secs))
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the motion window process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
}

// showMotion is motionWindow in the test process; the test fails when
// the window cannot be shown.
func showMotion(t *testing.T, x, y, w, h int) (stop func()) {
	t.Helper()
	stop, err := motionWindow(x, y, w, h)
	if err != nil {
		t.Fatalf("showing the motion window: %v", err)
	}
	return stop
}

// motionWindow shows a borderless, click-through, never-activated window
// with w×h pixels at (x, y) of the virtual desktop that draws a moving
// pattern once per composition until the returned stop is called.
func motionWindow(x, y, w, h int) (stop func(), err error) {
	var quit atomic.Bool
	ready := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		instance, _, _ := procGetModuleHandleW.Call(0)
		className, _ := windows.UTF16PtrFromString("DeskhandMotionTest")
		wc := wndClassEx{wndProc: procDefWindowProcW.Addr(), instance: instance, className: className}
		wc.size = uint32(unsafe.Sizeof(wc))
		if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
			ready <- err
			return
		}
		defer procUnregisterClassW.Call(uintptr(unsafe.Pointer(className)), instance)
		hwnd, _, err := procCreateWindowExW.Call(wsExTopmost|wsExTransparent|wsExToolWindow|wsExLayered|wsExNoActivate,
			uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), wsPopup,
			uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, instance, 0)
		if hwnd == 0 {
			ready <- err
			return
		}
		defer procDestroyWindow.Call(hwnd)
		procSetLayeredWindowAttributes.Call(hwnd, 0, 255, lwaAlpha)
		procShowWindow.Call(hwnd, swShowNoActivate)
		ready <- nil
		for frame := 0; !quit.Load(); frame++ {
			var m winMsg
			for {
				if r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove); r == 0 {
					break
				}
				procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
				procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
			}
			drawMotion(hwnd, w, h, frame)
			procDwmFlush.Call()
		}
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	var once atomic.Bool
	return func() {
		if once.CompareAndSwap(false, true) {
			quit.Store(true)
			<-done
		}
	}, nil
}

// drawMotion fills the window with cells whose shade moves with frame,
// like text scrolling past colored bars.
func drawMotion(hwnd uintptr, w, h, frame int) {
	hdc, _, _ := procGetDC.Call(hwnd)
	if hdc == 0 {
		return
	}
	defer procReleaseDC.Call(hwnd, hdc)
	brushes := map[uint32]uintptr{}
	defer func() {
		for _, b := range brushes {
			procDeleteObject.Call(b)
		}
	}()
	fill := func(l, t, r, b int, color uint32) {
		br, ok := brushes[color]
		if !ok {
			br, _, _ = procCreateSolidBrush.Call(uintptr(color))
			brushes[color] = br
		}
		rc := winRect{int32(l), int32(t), int32(r), int32(b)}
		procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), br)
	}
	bars := 8
	for i := 0; i < bars; i++ {
		v := uint32((i*32 + frame*4) % 256)
		fill(i*w/bars, 0, (i+1)*w/bars, h, v|(255-v)<<8|uint32((i*70)%256)<<16)
	}
	for row := 0; row*motionCell < h; row++ {
		for col := 0; col*motionCell < w; col += 1 + (row+frame)%motionCellsPerRow {
			n := uint32((row*131 + col*17 + frame*7) % 97)
			if n%3 == 0 {
				g := n * 2
				fill(col*motionCell, row*motionCell, col*motionCell+motionCell-4, row*motionCell+motionCell-4, g|g<<8|g<<16)
			}
		}
	}
}
