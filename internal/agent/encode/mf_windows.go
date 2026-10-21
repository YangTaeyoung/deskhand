//go:build windows

package encode

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
)

var (
	mfplat = windows.NewLazySystemDLL("mfplat.dll")

	procMFStartup                 = mfplat.NewProc("MFStartup")
	procMFTEnumEx                 = mfplat.NewProc("MFTEnumEx")
	procMFCreateMediaType         = mfplat.NewProc("MFCreateMediaType")
	procMFCreateSample            = mfplat.NewProc("MFCreateSample")
	procMFCreateMemoryBuffer      = mfplat.NewProc("MFCreateMemoryBuffer")
	procMFCreateDXGISurfaceBuffer = mfplat.NewProc("MFCreateDXGISurfaceBuffer")
	procMFCreateDXGIDeviceManager = mfplat.NewProc("MFCreateDXGIDeviceManager")
)

// Interface ids, categories, formats and attributes of Media Foundation
// (mfapi.h, mfobjects.h, mftransform.h, mfidl.h, strmif.h).
var (
	iidIMFTransform           = com.MustGUID("bf94c121-5b05-4e6f-8000-ba598961414d")
	iidIMFMediaEventGenerator = com.MustGUID("2cd0bd52-bcd5-4b89-b62c-eadc0c031e7d")
	iidIMF2DBuffer            = com.MustGUID("7dc9d5f9-9ed9-44ec-9bbf-0600bb589fbb")
	iidIMFShutdown            = com.MustGUID("97ec2ea4-0e42-4937-97ac-9d6d328824e1")
	iidICodecAPI              = com.MustGUID("901db4c7-31ce-41a2-85dc-8fa0bf41b8da")
	iidID3D11Texture2D        = com.MustGUID("6f15aaf2-d208-4e89-9ab4-489535d34f9c")

	mftCategoryVideoEncoder = com.MustGUID("f79eac7d-e545-4387-bdee-d647d7bde42a")
	mfMediaTypeVideo        = com.MustGUID("73646976-0000-0010-8000-00aa00389b71")
	mfVideoFormatNV12       = com.MustGUID("3231564e-0000-0010-8000-00aa00389b71")
	mfVideoFormatH264       = com.MustGUID("34363248-0000-0010-8000-00aa00389b71")

	mfMTMajorType          = com.MustGUID("48eba18e-f8c9-4687-bf11-0a74c9f96a8f")
	mfMTSubtype            = com.MustGUID("f7e34c9a-42e8-4714-b74b-cb29d72c35e5")
	mfMTFrameSize          = com.MustGUID("1652c33d-d6b2-4012-b834-72030849a37d")
	mfMTFrameRate          = com.MustGUID("c459a2e8-3d2c-4e44-b132-fee5156c7bb0")
	mfMTPixelAspectRatio   = com.MustGUID("c6376a1e-8d0a-4027-be45-6d9a0ad39bb6")
	mfMTInterlaceMode      = com.MustGUID("e2724bb8-e676-4806-b4b2-a8d6efb44ccd")
	mfMTAvgBitrate         = com.MustGUID("20332624-fb0d-4d9e-bd0d-cbf6786c102e")
	mfMTMPEG2Profile       = com.MustGUID("ad76a80b-2d5c-4e0b-b375-64e520137036")
	mfMTMPEG2Level         = com.MustGUID("96f66574-11c5-4015-8666-bff516436da7")
	mfMTMPEGSequenceHeader = com.MustGUID("3c036de7-3ad0-4c9e-9216-ee6d6ac21cb3")
	mfMTYUVMatrix          = com.MustGUID("3e23d450-2c75-4d25-a00e-b91670d12327")
	mfMTVideoNominalRange  = com.MustGUID("c21b8ee5-b956-4071-8daf-325edf5cab11")
	mfMTVideoPrimaries     = com.MustGUID("dbfbe4d7-0740-4ee0-8192-850ab0e21935")
	mfMTTransferFunction   = com.MustGUID("5fb0fce9-be5c-4935-a811-ec838f8eed93")

	mfTransformAsync       = com.MustGUID("f81a699a-649a-497d-8c73-29f8fed6ad7a")
	mfTransformAsyncUnlock = com.MustGUID("e5666d6b-3422-4eb6-a421-da7db1f8e207")
	mfLowLatency           = com.MustGUID("9c27891a-ed7a-40e1-88e8-b22727a024ee")
	mftFriendlyName        = com.MustGUID("314ffbae-5b41-4c95-9c19-4e7d586face3")
	mftEnumHardwareURL     = com.MustGUID("2fb866ac-b078-4942-ab6c-003d05cda674")
	mftEnumHardwareVendor  = com.MustGUID("3aecb0cc-035b-4bcc-8185-2b8d551ef3af")
)

// vtable indices of the Media Foundation interfaces.
const (
	attrGetUINT32          = 7
	attrGetAllocatedString = 13
	attrGetBlobSize        = 14
	attrGetBlob            = 15
	attrSetUINT32          = 21
	attrSetUINT64          = 22
	attrSetGUID            = 24

	eventGetType   = 33
	eventGetStatus = 35

	sampleGetSampleTime             = 35
	sampleSetSampleTime             = 36
	sampleSetSampleDuration         = 38
	sampleConvertToContiguousBuffer = 41
	sampleAddBuffer                 = 42
	bufferLock                      = 3
	bufferUnlock                    = 4
	bufferSetCurrentLength          = 6
	buffer2DGetContiguousLength     = 7
	activateActivateObject          = 33
	activateShutdownObject          = 34
	deviceManagerResetDevice        = 7
	eventGeneratorGetEvent          = 3
	shutdownShutdown                = 3
	transformGetOutputStreamInfo    = 7
	transformGetAttributes          = 8
	transformGetOutputAvailableType = 14
	transformSetInputType           = 15
	transformSetOutputType          = 16
	transformGetOutputCurrentType   = 18
	transformProcessMessage         = 23
	transformProcessInput           = 24
	transformProcessOutput          = 25
)

// MFTEnumEx flags, MFT messages, event types and stream flags.
const (
	enumSyncMFT       = 0x1
	enumHardware      = 0x4
	enumLocalMFT      = 0x10
	enumSortAndFilter = 0x40

	msgCommandDrain         = 0x1
	msgSetD3DManager        = 0x2
	msgNotifyBeginStreaming = 0x10000000
	msgNotifyEndStreaming   = 0x10000001
	msgNotifyEndOfStream    = 0x10000002
	msgNotifyStartOfStream  = 0x10000003

	meError                  = 1
	meTransformNeedInput     = 601
	meTransformHaveOutput    = 602
	meTransformDrainComplete = 603

	outputProvidesSamples   = 0x100
	outputCanProvideSamples = 0x200
)

// Values of media type attributes and the H.264 High profile.
const (
	mfVersion            = 0x00020070 // MF_VERSION
	mfStartupLite        = 1
	interlaceProgressive = 2
	transferMatrixBT709  = 1
	nominalRange16to235  = 2
	primariesBT709       = 2
	transferFunction709  = 5
	profileHigh          = 100
	hundredNS            = 10_000_000
)

var startup = sync.OnceValue(func() error {
	r, _, _ := procMFStartup.Call(mfVersion, mfStartupLite)
	if hr := com.HRESULT(uint32(r)); hr.Failed() {
		return fmt.Errorf("MFStartup: %w", hr)
	}
	return nil
})

func setUINT32(obj uintptr, key *com.GUID, v uint32) com.HRESULT {
	return com.Call(obj, attrSetUINT32, uintptr(unsafe.Pointer(key)), uintptr(v))
}

func setUINT64(obj uintptr, key *com.GUID, v uint64) com.HRESULT {
	return com.Call(obj, attrSetUINT64, uintptr(unsafe.Pointer(key)), uintptr(v))
}

func setGUID(obj uintptr, key, v *com.GUID) com.HRESULT {
	return com.Call(obj, attrSetGUID, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(v)))
}

func getUINT32(obj uintptr, key *com.GUID) (uint32, com.HRESULT) {
	var v uint32
	hr := com.Call(obj, attrGetUINT32, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&v)))
	return v, hr
}

// getString returns the string attribute key of obj, or "" without one.
func getString(obj uintptr, key *com.GUID) string {
	var p *uint16
	var n uint32
	if hr := com.Call(obj, attrGetAllocatedString, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&n))); hr.Failed() || p == nil {
		return ""
	}
	s := windows.UTF16PtrToString(p)
	com.TaskMemFree(uintptr(unsafe.Pointer(p)))
	return s
}

// getBlob returns the blob attribute key of obj, or nil without one.
func getBlob(obj uintptr, key *com.GUID) []byte {
	var n uint32
	if hr := com.Call(obj, attrGetBlobSize, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&n))); hr.Failed() || n == 0 {
		return nil
	}
	b := make([]byte, n)
	if hr := com.Call(obj, attrGetBlob, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&b[0])), uintptr(n), uintptr(unsafe.Pointer(&n))); hr.Failed() {
		return nil
	}
	return b[:n]
}

// format is one video media type of an encoder: NV12 in or H.264 out.
type format struct {
	w, h, fps, bps int
	level          uint32
}

// newVideoType makes the media type of f with subtype sub: progressive,
// square pixels, BT.709 limited range, and for H.264 the bitrate, the
// High profile and f.level when it is not 0.
func newVideoType(sub *com.GUID, f format) (uintptr, error) {
	var t uintptr
	r, _, _ := procMFCreateMediaType.Call(uintptr(unsafe.Pointer(&t)))
	if hr := com.HRESULT(uint32(r)); hr.Failed() {
		return 0, fmt.Errorf("MFCreateMediaType: %w", hr)
	}
	results := []com.HRESULT{
		setGUID(t, &mfMTMajorType, &mfMediaTypeVideo),
		setGUID(t, &mfMTSubtype, sub),
		setUINT64(t, &mfMTFrameSize, uint64(f.w)<<32|uint64(f.h)),
		setUINT64(t, &mfMTFrameRate, uint64(f.fps)<<32|1),
		setUINT64(t, &mfMTPixelAspectRatio, 1<<32|1),
		setUINT32(t, &mfMTInterlaceMode, interlaceProgressive),
		setUINT32(t, &mfMTYUVMatrix, transferMatrixBT709),
		setUINT32(t, &mfMTVideoNominalRange, nominalRange16to235),
		setUINT32(t, &mfMTVideoPrimaries, primariesBT709),
		setUINT32(t, &mfMTTransferFunction, transferFunction709),
	}
	if *sub == mfVideoFormatH264 {
		results = append(results, setUINT32(t, &mfMTAvgBitrate, uint32(f.bps)), setUINT32(t, &mfMTMPEG2Profile, profileHigh))
		if f.level != 0 {
			results = append(results, setUINT32(t, &mfMTMPEG2Level, f.level))
		}
	}
	for _, hr := range results {
		if hr.Failed() {
			com.Release(t)
			return 0, fmt.Errorf("IMFMediaType::Set: %w", hr)
		}
	}
	return t, nil
}

// setTypes sets the H.264 output type and then the NV12 input type of
// mft for f, and when the encoder refuses the output type with a level
// sets it again without one.
func setTypes(mft uintptr, f format) error {
	set := func(sub *com.GUID, index int, f format) error {
		t, err := newVideoType(sub, f)
		if err != nil {
			return err
		}
		defer com.Release(t)
		return com.Call(mft, index, 0, t, 0).Err()
	}
	err := set(&mfVideoFormatH264, transformSetOutputType, f)
	if err != nil && f.level != 0 {
		f.level = 0
		err = set(&mfVideoFormatH264, transformSetOutputType, f)
	}
	if err != nil {
		return fmt.Errorf("IMFTransform::SetOutputType H.264 %dx%d at %d fps: %w", f.w, f.h, f.fps, err)
	}
	if err := set(&mfVideoFormatNV12, transformSetInputType, f); err != nil {
		return fmt.Errorf("IMFTransform::SetInputType NV12 %dx%d: %w", f.w, f.h, err)
	}
	return nil
}

// sequenceHeader returns the SPS and PPS of the current output type of
// mft, or nil when it has none.
func sequenceHeader(mft uintptr) []byte {
	var t uintptr
	if hr := com.Call(mft, transformGetOutputCurrentType, 0, uintptr(unsafe.Pointer(&t))); hr.Failed() {
		return nil
	}
	defer com.Release(t)
	return getBlob(t, &mfMTMPEGSequenceHeader)
}

// renegotiate sets the first output type mft offers after it reported a
// stream change.
func renegotiate(mft uintptr) error {
	var t uintptr
	if hr := com.Call(mft, transformGetOutputAvailableType, 0, 0, uintptr(unsafe.Pointer(&t))); hr.Failed() {
		return fmt.Errorf("IMFTransform::GetOutputAvailableType: %w", hr)
	}
	defer com.Release(t)
	if hr := com.Call(mft, transformSetOutputType, 0, t, 0); hr.Failed() {
		return fmt.Errorf("IMFTransform::SetOutputType after a stream change: %w", hr)
	}
	return nil
}

// mftEntry is one encoder MFT of an enumeration.
type mftEntry struct {
	act          uintptr
	name, vendor string
	hardware     bool
}

// registerTypeInfo is MFT_REGISTER_TYPE_INFO.
type registerTypeInfo struct{ major, sub com.GUID }

// enumEncoders lists the video encoder MFTs from NV12 to H.264 that
// MFTEnumEx finds with flags.
func enumEncoders(flags uint32) ([]mftEntry, error) {
	in := registerTypeInfo{mfMediaTypeVideo, mfVideoFormatNV12}
	out := registerTypeInfo{mfMediaTypeVideo, mfVideoFormatH264}
	var arr *uintptr
	var n uint32
	r, _, _ := procMFTEnumEx.Call(uintptr(unsafe.Pointer(&mftCategoryVideoEncoder)), uintptr(flags), uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Pointer(&out)), uintptr(unsafe.Pointer(&arr)), uintptr(unsafe.Pointer(&n)))
	if hr := com.HRESULT(uint32(r)); hr.Failed() {
		return nil, fmt.Errorf("MFTEnumEx: %w", hr)
	}
	if arr == nil {
		return nil, nil
	}
	defer com.TaskMemFree(uintptr(unsafe.Pointer(arr)))
	acts := unsafe.Slice(arr, n)
	entries := make([]mftEntry, 0, n)
	for _, a := range acts {
		entries = append(entries, mftEntry{act: a, name: getString(a, &mftFriendlyName), vendor: getString(a, &mftEnumHardwareVendor),
			hardware: getString(a, &mftEnumHardwareURL) != ""})
	}
	return entries, nil
}

func releaseEntries(entries []mftEntry) {
	for _, e := range entries {
		com.Release(e.act)
	}
}

// vendorFirst orders entries with those of the adapter vendor vendorID
// first, keeping the order within each group.
func vendorFirst(entries []mftEntry, vendorID uint32) []mftEntry {
	want := fmt.Sprintf("VEN_%04X", vendorID)
	out := make([]mftEntry, 0, len(entries))
	for _, e := range entries {
		if strings.EqualFold(e.vendor, want) {
			out = append(out, e)
		}
	}
	for _, e := range entries {
		if !strings.EqualFold(e.vendor, want) {
			out = append(out, e)
		}
	}
	return out
}

// newMemoryBuffer creates an IMFMediaBuffer of size bytes.
func newMemoryBuffer(size int) (uintptr, error) {
	var buf uintptr
	r, _, _ := procMFCreateMemoryBuffer.Call(uintptr(size), uintptr(unsafe.Pointer(&buf)))
	if hr := com.HRESULT(uint32(r)); hr.Failed() {
		return 0, fmt.Errorf("MFCreateMemoryBuffer(%d): %w", size, hr)
	}
	return buf, nil
}

// newSample creates an IMFSample with buf, which it references, at time
// and duration in 100 ns units.
func newSample(buf uintptr, time, duration int64) (uintptr, error) {
	var s uintptr
	r, _, _ := procMFCreateSample.Call(uintptr(unsafe.Pointer(&s)))
	if hr := com.HRESULT(uint32(r)); hr.Failed() {
		return 0, fmt.Errorf("MFCreateSample: %w", hr)
	}
	if hr := com.Call(s, sampleAddBuffer, buf); hr.Failed() {
		com.Release(s)
		return 0, fmt.Errorf("IMFSample::AddBuffer: %w", hr)
	}
	com.Call(s, sampleSetSampleTime, uintptr(time))
	com.Call(s, sampleSetSampleDuration, uintptr(duration))
	return s, nil
}

// textureSample creates an IMFSample over the ID3D11Texture2D tex.
func textureSample(tex uintptr, time, duration int64) (uintptr, error) {
	var buf uintptr
	r, _, _ := procMFCreateDXGISurfaceBuffer.Call(uintptr(unsafe.Pointer(&iidID3D11Texture2D)), tex, 0, 0, uintptr(unsafe.Pointer(&buf)))
	if hr := com.HRESULT(uint32(r)); hr.Failed() {
		return 0, fmt.Errorf("MFCreateDXGISurfaceBuffer: %w", hr)
	}
	defer com.Release(buf)
	if b2, hr := com.QueryInterface(buf, &iidIMF2DBuffer); hr.Succeeded() {
		var n uint32
		if com.Call(b2, buffer2DGetContiguousLength, uintptr(unsafe.Pointer(&n))).Succeeded() {
			com.Call(buf, bufferSetCurrentLength, uintptr(n))
		}
		com.Release(b2)
	}
	return newSample(buf, time, duration)
}

// sampleData copies the bytes of the sample s.
func sampleData(s uintptr) ([]byte, error) {
	var buf uintptr
	if hr := com.Call(s, sampleConvertToContiguousBuffer, uintptr(unsafe.Pointer(&buf))); hr.Failed() {
		return nil, fmt.Errorf("IMFSample::ConvertToContiguousBuffer: %w", hr)
	}
	defer com.Release(buf)
	var p *byte
	var maxLen, cur uint32
	if hr := com.Call(buf, bufferLock, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&maxLen)), uintptr(unsafe.Pointer(&cur))); hr.Failed() {
		return nil, fmt.Errorf("IMFMediaBuffer::Lock: %w", hr)
	}
	data := make([]byte, cur)
	copy(data, unsafe.Slice(p, cur))
	com.Call(buf, bufferUnlock)
	return data, nil
}

func sampleTime(s uintptr) int64 {
	var t int64
	com.Call(s, sampleGetSampleTime, uintptr(unsafe.Pointer(&t)))
	return t
}

// outputDataBuffer is MFT_OUTPUT_DATA_BUFFER.
type outputDataBuffer struct {
	streamID uint32
	sample   uintptr
	status   uint32
	events   uintptr
}

// outputStreamInfo is MFT_OUTPUT_STREAM_INFO.
type outputStreamInfo struct{ flags, size, alignment uint32 }

func processMessage(mft uintptr, msg, param uintptr) com.HRESULT {
	return com.Call(mft, transformProcessMessage, msg, param)
}
