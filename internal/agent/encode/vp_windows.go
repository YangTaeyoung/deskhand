//go:build windows

package encode

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
)

var (
	iidID3D11VideoDevice  = com.MustGUID("10ec4d5b-975a-4689-b9e4-d0aac30fe333")
	iidID3D11VideoContext = com.MustGUID("61f21c45-3c0e-4a74-9cea-67100d9ad5e4")
)

// vtable indices (docs/adr/0001-screen-pipeline.md, d3d11.h).
const (
	videoDeviceCreateVideoProcessor = 4
	videoDeviceCreateInputView      = 8
	videoDeviceCreateOutputView     = 9
	videoDeviceCreateEnumerator     = 10
	enumeratorCheckFormat           = 8
	vctxSetOutputTargetRect         = 13
	vctxSetOutputColorSpace         = 15
	vctxSetStreamFrameFormat        = 27
	vctxSetStreamColorSpace         = 28
	vctxSetStreamSourceRect         = 30
	vctxSetStreamDestRect           = 31
	vctxSetStreamAutoProcessing     = 37
	vctxBlt                         = 53
	deviceCreateQuery               = 24
	contextEnd                      = 28
	contextGetData                  = 29
	contextFlush                    = 111
)

const (
	formatSupportInput  = 0x1
	formatSupportOutput = 0x2
	// viewTexture2D is D3D11_VPIV_DIMENSION_TEXTURE2D and
	// D3D11_VPOV_DIMENSION_TEXTURE2D.
	viewTexture2D = 1
	// outputColorSpace is D3D11_VIDEO_PROCESSOR_COLOR_SPACE with
	// YCbCr_Matrix BT.709 and Nominal_Range 16–235; the input's, 0, is
	// full-range RGB.
	outputColorSpace = 1<<2 | 1<<4
	// gpuWaitLimit bounds the wait for the video processor to finish.
	gpuWaitLimit = 500 * time.Millisecond
)

// contentDesc is D3D11_VIDEO_PROCESSOR_CONTENT_DESC.
type contentDesc struct {
	inputFrameFormat       uint32
	inRateNum, inRateDen   uint32
	inW, inH               uint32
	outRateNum, outRateDen uint32
	outW, outH             uint32
	usage                  uint32
}

// inputViewDesc is D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC.
type inputViewDesc struct{ fourCC, viewDimension, mipSlice, arraySlice uint32 }

// outputViewDesc is D3D11_VIDEO_PROCESSOR_OUTPUT_VIEW_DESC.
type outputViewDesc struct{ viewDimension, mipSlice, firstArraySlice, arraySize uint32 }

// rect is RECT.
type rect struct{ left, top, right, bottom int32 }

// vpStream is D3D11_VIDEO_PROCESSOR_STREAM.
type vpStream struct {
	enable, outputIndex, inputFrameOrField, pastFrames, futureFrames uint32
	_                                                                uint32
	past, input, future, pastRight, inputRight, futureRight          uintptr
}

// converter turns BGRA frames of the source size into NV12 images of the
// output size, BT.709 limited range, with the device's video processor.
type converter struct {
	dev                    *d3d.Device
	vdev, vctx, venum, vp  uintptr
	srcW, srcH, outW, outH int
	// viewed is the texture inView reads; upload is the texture CPU frames
	// are written to.
	viewed, inView uintptr
	upload         uintptr
	query          uintptr
}

func newConverter(dev *d3d.Device, srcW, srcH, outW, outH, fps int) (*converter, error) {
	c := &converter{dev: dev, srcW: srcW, srcH: srcH, outW: outW, outH: outH}
	ok := false
	defer func() {
		if !ok {
			c.close()
		}
	}()
	var hr com.HRESULT
	if c.vdev, hr = com.QueryInterface(dev.Device, &iidID3D11VideoDevice); hr.Failed() {
		return nil, fmt.Errorf("the device of %s has no video processor (ID3D11VideoDevice: %w)", dev.AdapterName, hr)
	}
	if c.vctx, hr = com.QueryInterface(dev.Context, &iidID3D11VideoContext); hr.Failed() {
		return nil, fmt.Errorf("ID3D11DeviceContext QueryInterface(ID3D11VideoContext): %w", hr)
	}
	desc := contentDesc{inRateNum: uint32(fps), inRateDen: 1, inW: uint32(srcW), inH: uint32(srcH),
		outRateNum: uint32(fps), outRateDen: 1, outW: uint32(outW), outH: uint32(outH)}
	if hr := com.Call(c.vdev, videoDeviceCreateEnumerator, uintptr(unsafe.Pointer(&desc)), uintptr(unsafe.Pointer(&c.venum))); hr.Failed() {
		return nil, fmt.Errorf("ID3D11VideoDevice::CreateVideoProcessorEnumerator %dx%d to %dx%d: %w", srcW, srcH, outW, outH, hr)
	}
	var in, out uint32
	com.Call(c.venum, enumeratorCheckFormat, uintptr(d3d.FormatBGRA), uintptr(unsafe.Pointer(&in)))
	com.Call(c.venum, enumeratorCheckFormat, uintptr(d3d.FormatNV12), uintptr(unsafe.Pointer(&out)))
	if in&formatSupportInput == 0 || out&formatSupportOutput == 0 {
		return nil, fmt.Errorf("the video processor of %s does not convert BGRA to NV12 (support 0x%x, 0x%x)", dev.AdapterName, in, out)
	}
	if hr := com.Call(c.vdev, videoDeviceCreateVideoProcessor, c.venum, 0, uintptr(unsafe.Pointer(&c.vp))); hr.Failed() {
		return nil, fmt.Errorf("ID3D11VideoDevice::CreateVideoProcessor: %w", hr)
	}
	inCS, outCS := uint32(0), uint32(outputColorSpace)
	com.CallRaw(c.vctx, vctxSetStreamColorSpace, c.vp, 0, uintptr(unsafe.Pointer(&inCS)))
	com.CallRaw(c.vctx, vctxSetOutputColorSpace, c.vp, uintptr(unsafe.Pointer(&outCS)))
	com.CallRaw(c.vctx, vctxSetStreamFrameFormat, c.vp, 0, 0)
	com.CallRaw(c.vctx, vctxSetStreamAutoProcessing, c.vp, 0, 0)
	src := rect{0, 0, int32(srcW), int32(srcH)}
	dst := rect{0, 0, int32(outW), int32(outH)}
	com.CallRaw(c.vctx, vctxSetStreamSourceRect, c.vp, 0, 1, uintptr(unsafe.Pointer(&src)))
	com.CallRaw(c.vctx, vctxSetStreamDestRect, c.vp, 0, 1, uintptr(unsafe.Pointer(&dst)))
	com.CallRaw(c.vctx, vctxSetOutputTargetRect, c.vp, 1, uintptr(unsafe.Pointer(&dst)))
	query := [2]uint32{0, 0}
	if hr := com.Call(dev.Device, deviceCreateQuery, uintptr(unsafe.Pointer(&query[0])), uintptr(unsafe.Pointer(&c.query))); hr.Failed() {
		return nil, fmt.Errorf("ID3D11Device::CreateQuery(EVENT): %w", hr)
	}
	ok = true
	return c, nil
}

// newTarget creates an NV12 texture of the output size with a video
// processor output view on it.
func (c *converter) newTarget() (tex, view uintptr, err error) {
	tex, err = c.dev.NewTexture(c.outW, c.outH, d3d.FormatNV12, d3d.UsageDefault, d3d.BindRenderTarget, 0)
	if err != nil {
		return 0, 0, err
	}
	desc := outputViewDesc{viewDimension: viewTexture2D}
	if hr := com.Call(c.vdev, videoDeviceCreateOutputView, tex, c.venum, uintptr(unsafe.Pointer(&desc)), uintptr(unsafe.Pointer(&view))); hr.Failed() {
		com.Release(tex)
		return 0, 0, fmt.Errorf("ID3D11VideoDevice::CreateVideoProcessorOutputView: %w", hr)
	}
	return tex, view, nil
}

// convert draws f into the target of view. A frame on the GDI path is
// written to the upload texture first.
func (c *converter) convert(f capture.Frame, view uintptr) error {
	if f.Width != c.srcW || f.Height != c.srcH {
		return fmt.Errorf("a %dx%d frame for an encoder of %dx%d frames; make the encoder again for the new size", f.Width, f.Height, c.srcW, c.srcH)
	}
	in := f.Texture
	if in == 0 {
		if len(f.CPU) < f.Width*f.Height*4 {
			return fmt.Errorf("the %dx%d frame has neither a texture nor %d bytes of pixels", f.Width, f.Height, f.Width*f.Height*4)
		}
		if c.upload == 0 {
			tex, err := c.dev.NewTexture(c.srcW, c.srcH, d3d.FormatBGRA, d3d.UsageDefault, d3d.BindRenderTarget|d3d.BindShaderResource, 0)
			if err != nil {
				return err
			}
			c.upload = tex
		}
		c.dev.UpdateSubresource(c.upload, f.CPU, f.Width*4)
		in = c.upload
	}
	if in != c.viewed {
		com.Release(c.inView)
		c.viewed, c.inView = 0, 0
		desc := inputViewDesc{viewDimension: viewTexture2D}
		var iv uintptr
		if hr := com.Call(c.vdev, videoDeviceCreateInputView, in, c.venum, uintptr(unsafe.Pointer(&desc)), uintptr(unsafe.Pointer(&iv))); hr.Failed() {
			return fmt.Errorf("ID3D11VideoDevice::CreateVideoProcessorInputView: %w", hr)
		}
		c.viewed, c.inView = in, iv
	}
	s := vpStream{enable: 1, input: c.inView}
	if hr := com.Call(c.vctx, vctxBlt, c.vp, view, 0, 1, uintptr(unsafe.Pointer(&s))); hr.Failed() {
		return fmt.Errorf("ID3D11VideoContext::VideoProcessorBlt: %w", hr)
	}
	return nil
}

// wait returns once the GPU has done the commands queued so far, or after
// gpuWaitLimit.
func (c *converter) wait() {
	com.CallRaw(c.dev.Context, contextEnd, c.query)
	com.CallRaw(c.dev.Context, contextFlush)
	start := time.Now()
	for {
		var done int32
		hr := com.Call(c.dev.Context, contextGetData, c.query, uintptr(unsafe.Pointer(&done)), 4, 0)
		if hr == com.S_OK || hr.Failed() || time.Since(start) > gpuWaitLimit {
			return
		}
		if time.Since(start) < 2*time.Millisecond {
			runtime.Gosched()
		} else {
			time.Sleep(100 * time.Microsecond)
		}
	}
}

func (c *converter) close() {
	for _, p := range []*uintptr{&c.inView, &c.upload, &c.query, &c.vp, &c.venum, &c.vctx, &c.vdev} {
		com.Release(*p)
		*p = 0
	}
	c.viewed = 0
}
