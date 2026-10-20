// Package d3d enumerates the DXGI outputs of the desktop and creates D3D11
// devices and textures on the adapter of an output. Only Windows creates
// devices; on other systems the package has the Device type alone.
//
// COM objects of this package are released on the goroutine that created
// them; callers lock that goroutine to its thread (runtime.LockOSThread)
// for the life of a Device.
package d3d

// Device is a D3D11 device on the adapter of one output, with the
// output's IDXGIOutput1.
type Device struct {
	// Device is the ID3D11Device and Context its immediate
	// ID3D11DeviceContext; the device is multithread protected.
	Device  uintptr
	Context uintptr
	// Output is the IDXGIOutput1 of the output the device was made for.
	Output uintptr
	// OutputName and AdapterName name the output and its adapter;
	// AdapterLUID is the adapter's locally unique id.
	OutputName  string
	AdapterName string
	AdapterLUID uint64
	// VendorID is the PCI vendor id of the adapter, such as 0x10de.
	VendorID uint32
	// FeatureLevel is the D3D_FEATURE_LEVEL of the device.
	FeatureLevel uint32
	// Video reports whether the device was created with
	// D3D11_CREATE_DEVICE_VIDEO_SUPPORT.
	Video bool
}
