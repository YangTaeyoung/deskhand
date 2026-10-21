package d3d

import (
	"errors"
	"runtime"
	"testing"
	"unsafe"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
)

// attachedOutput returns the name of the first output attached to the
// desktop, skipping the test in a session without outputs.
func attachedOutput(t *testing.T) string {
	t.Helper()
	outs, err := Outputs()
	var hr com.HRESULT
	if errors.As(err, &hr) && hr == com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE {
		t.Skipf("this session has no DXGI outputs (%v); run this test in the user's session through scripts/wintest.sh", err)
	}
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}
	for _, o := range outs {
		t.Logf("output %+v", o)
		if o.Attached {
			return o.Name
		}
	}
	t.Skip("no DXGI output is attached to the desktop")
	return ""
}

func TestNewDeviceForOutputCopiesATexture(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	name := attachedOutput(t)
	dev, err := NewDeviceForOutput(name)
	if err != nil {
		t.Fatalf("NewDeviceForOutput(%q): %v", name, err)
	}
	defer dev.Close()
	t.Logf("device on %q (%s) feature level 0x%x video %v", dev.OutputName, dev.AdapterName, dev.FeatureLevel, dev.Video)
	if dev.Device == 0 || dev.Context == 0 || dev.Output == 0 || dev.FeatureLevel < 0xa000 || dev.AdapterName == "" {
		t.Fatalf("device %+v", dev)
	}

	const w, h = 64, 16
	src, err := dev.NewTexture(w, h, FormatBGRA, UsageDefault, BindRenderTarget|BindShaderResource, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer com.Release(src)
	stage, err := dev.NewTexture(w, h, FormatBGRA, UsageStaging, 0, CPUAccessRead)
	if err != nil {
		t.Fatal(err)
	}
	defer com.Release(stage)
	if d := DescOf(stage); d.Width != w || d.Height != h || d.Format != FormatBGRA || d.Usage != UsageStaging {
		t.Fatalf("staging desc %+v", d)
	}

	pixels := make([]byte, w*h*4)
	for i := range pixels {
		pixels[i] = byte(i * 7)
	}
	dev.UpdateSubresource(src, pixels, w*4)
	dev.CopyResource(stage, src)
	m, err := dev.Map(stage, MapRead)
	if err != nil {
		t.Fatal(err)
	}
	got := unsafe.Slice((*byte)(m.Data), int(m.RowPitch)*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w*4; x++ {
			if got[y*int(m.RowPitch)+x] != pixels[y*w*4+x] {
				dev.Unmap(stage)
				t.Fatalf("byte %d of row %d is %d, want %d", x, y, got[y*int(m.RowPitch)+x], pixels[y*w*4+x])
			}
		}
	}
	dev.Unmap(stage)
}

func TestNewDeviceForUnknownOutputFails(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	attachedOutput(t)
	if dev, err := NewDeviceForOutput(`\\.\DISPLAY_NONE`); err == nil {
		dev.Close()
		t.Fatal("a device for an unknown output")
	}
}
