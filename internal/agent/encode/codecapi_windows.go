//go:build windows

package encode

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
)

// ICodecAPI properties of the encoders (codecapi.h).
var (
	codecLowLatencyMode       = com.MustGUID("9c27891a-ed7a-40e1-88e8-b22727a024ee")
	codecRateControlMode      = com.MustGUID("1c0608e9-370c-4710-8a58-cb6181c42423")
	codecMeanBitRate          = com.MustGUID("f7222374-2144-4815-b550-a37f8e12ee52")
	codecGOPSize              = com.MustGUID("95f31b26-95a4-41aa-9303-246a7fc6eef1")
	codecDefaultBPictureCount = com.MustGUID("8d390aac-dc5c-4200-b57f-814d04babab2")
	codecForceKeyFrame        = com.MustGUID("398c1b98-8353-475a-9ef2-8f265d260345")
)

// codecAPISetValue is the vtable index of ICodecAPI::SetValue.
const codecAPISetValue = 9

// VARIANT types and values.
const (
	vtBool      = 11
	vtUI4       = 19
	variantTrue = 0xFFFF
	// rateControlCBR is eAVEncCommonRateControlMode_CBR.
	rateControlCBR = 0
)

// variant is VARIANT on a 64-bit system.
type variant struct {
	vt       uint16
	reserved [3]uint16
	val      uint64
	record   uintptr
}

// codecAPI is the ICodecAPI of an encoder MFT, 0 when it has none.
type codecAPI uintptr

// codecAPIOf returns the ICodecAPI of mft, or 0 when it has none.
func codecAPIOf(mft uintptr) codecAPI {
	api, hr := com.QueryInterface(mft, &iidICodecAPI)
	if hr.Failed() {
		return 0
	}
	return codecAPI(api)
}

func (c codecAPI) set(key *com.GUID, vt uint16, v uint64) com.HRESULT {
	if c == 0 {
		return com.E_NOINTERFACE
	}
	val := variant{vt: vt, val: v}
	return com.Call(uintptr(c), codecAPISetValue, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&val)))
}

// GOP sizes of the encoders: an IDR at the start and on request only.
const (
	hwGOP = 0
	swGOP = math.MaxUint32
)

// configure sets low-latency mode, constant bitrate bps, GOP size gop and
// no B frames. An encoder without a property keeps its default.
func (c codecAPI) configure(bps int, gop uint32) {
	c.set(&codecLowLatencyMode, vtBool, variantTrue)
	c.set(&codecRateControlMode, vtUI4, rateControlCBR)
	c.set(&codecMeanBitRate, vtUI4, uint64(bps))
	c.set(&codecGOPSize, vtUI4, uint64(gop))
	c.set(&codecDefaultBPictureCount, vtUI4, 0)
}

// forceKeyFrame makes the next frame an IDR.
func (c codecAPI) forceKeyFrame() error {
	if hr := c.set(&codecForceKeyFrame, vtUI4, 1); hr.Failed() {
		return fmt.Errorf("ICodecAPI::SetValue(AVEncVideoForceKeyFrame): %w", hr)
	}
	return nil
}

// setBitrate changes the mean bitrate.
func (c codecAPI) setBitrate(bps int) error {
	if hr := c.set(&codecMeanBitRate, vtUI4, uint64(bps)); hr.Failed() {
		return fmt.Errorf("ICodecAPI::SetValue(AVEncCommonMeanBitRate %d): %w", bps, hr)
	}
	return nil
}

func (c codecAPI) release() { com.Release(uintptr(c)) }
