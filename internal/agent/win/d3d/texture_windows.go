//go:build windows

package d3d

import (
	"fmt"
	"unsafe"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
)

// DXGI formats of textures.
const (
	FormatBGRA uint32 = 87  // DXGI_FORMAT_B8G8R8A8_UNORM
	FormatNV12 uint32 = 103 // DXGI_FORMAT_NV12
)

// D3D11_USAGE values.
const (
	UsageDefault uint32 = 0
	UsageStaging uint32 = 3
)

// D3D11_BIND_FLAG values.
const (
	BindShaderResource uint32 = 0x8
	BindRenderTarget   uint32 = 0x20
	BindDecoder        uint32 = 0x200
	BindVideoEncoder   uint32 = 0x400
)

// D3D11_CPU_ACCESS_FLAG values.
const (
	CPUAccessWrite uint32 = 0x10000
	CPUAccessRead  uint32 = 0x20000
)

// D3D11_MAP values.
const (
	MapRead  uint32 = 1
	MapWrite uint32 = 2
)

// vtable indices (docs/adr/0001-screen-pipeline.md).
const (
	deviceCreateTexture2D    = 5
	texture2DGetDesc         = 10
	contextMap               = 14
	contextUnmap             = 15
	contextCopyResource      = 47
	contextUpdateSubresource = 48
	contextFlush             = 111
)

// TextureDesc is D3D11_TEXTURE2D_DESC.
type TextureDesc struct {
	Width, Height, MipLevels, ArraySize, Format, SampleCount, SampleQuality, Usage, BindFlags, CPUAccessFlags, MiscFlags uint32
}

// Mapped is D3D11_MAPPED_SUBRESOURCE: the CPU address of a mapped
// subresource and its row and depth pitches in bytes.
type Mapped struct {
	Data       unsafe.Pointer
	RowPitch   uint32
	DepthPitch uint32
}

// NewTexture creates a w×h ID3D11Texture2D with one mip level and one array
// slice.
func (d *Device) NewTexture(w, h int, format, usage, bind, cpuAccess uint32) (uintptr, error) {
	desc := TextureDesc{Width: uint32(w), Height: uint32(h), MipLevels: 1, ArraySize: 1, Format: format, SampleCount: 1,
		Usage: usage, BindFlags: bind, CPUAccessFlags: cpuAccess}
	var tex uintptr
	if hr := com.Call(d.Device, deviceCreateTexture2D, uintptr(unsafe.Pointer(&desc)), 0, uintptr(unsafe.Pointer(&tex))); hr.Failed() {
		return 0, fmt.Errorf("ID3D11Device::CreateTexture2D %dx%d format %d usage %d bind 0x%x: %w", w, h, format, usage, bind, hr)
	}
	return tex, nil
}

// DescOf returns the description of the ID3D11Texture2D tex.
func DescOf(tex uintptr) TextureDesc {
	var desc TextureDesc
	com.CallRaw(tex, texture2DGetDesc, uintptr(unsafe.Pointer(&desc)))
	return desc
}

// CopyResource copies all of src into dst on the immediate context.
func (d *Device) CopyResource(dst, src uintptr) {
	com.CallRaw(d.Context, contextCopyResource, dst, src)
}

// UpdateSubresource writes data, rows of rowPitch bytes, into subresource 0
// of res.
func (d *Device) UpdateSubresource(res uintptr, data []byte, rowPitch int) {
	if len(data) == 0 {
		return
	}
	com.CallRaw(d.Context, contextUpdateSubresource, res, 0, 0, uintptr(unsafe.Pointer(&data[0])), uintptr(rowPitch), 0)
}

// Map maps subresource 0 of res for mapType (MapRead for a staging
// texture with CPUAccessRead); Unmap ends the mapping.
func (d *Device) Map(res uintptr, mapType uint32) (Mapped, error) {
	var m Mapped
	if hr := com.Call(d.Context, contextMap, res, 0, uintptr(mapType), 0, uintptr(unsafe.Pointer(&m))); hr.Failed() {
		return Mapped{}, fmt.Errorf("ID3D11DeviceContext::Map: %w", hr)
	}
	return m, nil
}

// Unmap ends the mapping of subresource 0 of res.
func (d *Device) Unmap(res uintptr) {
	com.CallRaw(d.Context, contextUnmap, res, 0)
}

// Flush sends the queued commands of the immediate context to the GPU.
func (d *Device) Flush() {
	com.CallRaw(d.Context, contextFlush)
}
