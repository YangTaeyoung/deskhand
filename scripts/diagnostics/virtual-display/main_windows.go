//go:build windows

// virtual-display holds a temporary Deskhand software display device open for a
// bounded performance experiment. It never installs a driver or changes trust.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type createInfo struct {
	Size          uint32
	InstanceID    *uint16
	HardwareIDs   *uint16
	CompatibleIDs *uint16
	ContainerID   *windows.GUID
	Capabilities  uint32
	Description   *uint16
	Location      *uint16
	Security      *windows.SECURITY_DESCRIPTOR
}

type created struct {
	hr       uint32
	instance string
}

func main() {
	seconds := flag.Int("seconds", 120, "hold the temporary software device for 1–600 seconds")
	flag.Parse()
	if *seconds < 1 || *seconds > 600 || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "seconds must be 1–600 and no positional arguments are accepted")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, time.Duration(*seconds)*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, duration time.Duration) error {
	dll := windows.NewLazySystemDLL("cfgmgr32.dll")
	create, closeDevice := dll.NewProc("SwDeviceCreate"), dll.NewProc("SwDeviceClose")
	if err := create.Find(); err != nil {
		return err
	}
	if err := closeDevice.Find(); err != nil {
		return err
	}
	result := make(chan created, 1)
	callback := windows.NewCallback(func(_ uintptr, hr uint32, _ uintptr, instance *uint16) uintptr {
		value := created{hr: hr}
		if instance != nil {
			value.instance = windows.UTF16PtrToString(instance)
		}
		select {
		case result <- value:
		default:
		}
		return 0
	})
	enumerator, _ := windows.UTF16PtrFromString("Deskhand")
	parent, _ := windows.UTF16PtrFromString(`HTREE\ROOT\0`)
	instance, _ := windows.UTF16PtrFromString("DeskhandVirtualDisplay")
	description, _ := windows.UTF16PtrFromString("Deskhand Virtual Display")
	hardware, _ := windows.UTF16FromString("DeskhandVirtualDisplay")
	hardware = append(hardware, 0) // MULTI_SZ: one string and a terminating empty string.
	info := createInfo{InstanceID: instance, HardwareIDs: &hardware[0], CompatibleIDs: &hardware[0],
		Capabilities: 0x1 | 0x2 | 0x8, Description: description}
	info.Size = uint32(unsafe.Sizeof(info))
	var handle uintptr
	hr, _, _ := create.Call(uintptr(unsafe.Pointer(enumerator)), uintptr(unsafe.Pointer(parent)),
		uintptr(unsafe.Pointer(&info)), 0, 0, callback, 0, uintptr(unsafe.Pointer(&handle)))
	runtime.KeepAlive(hardware)
	runtime.KeepAlive(info)
	if int32(hr) < 0 {
		return fmt.Errorf("SwDeviceCreate: HRESULT 0x%08x", uint32(hr))
	}
	// Closing also waits out any in-flight creation callback. The default
	// SWDeviceLifetimeHandle removes the display when this handle closes.
	defer closeDevice.Call(handle)
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	var device created
	select {
	case device = <-result:
		if int32(device.hr) < 0 {
			return fmt.Errorf("software device enumeration: HRESULT 0x%08x", device.hr)
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-timeout.C:
		return fmt.Errorf("software device enumeration timed out")
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"event": "enumerated", "instance_id": device.instance,
		"hold_seconds": duration.Seconds(), "note": "enumeration does not prove the driver has started"}); err != nil {
		return err
	}
	hold := time.NewTimer(duration)
	defer hold.Stop()
	select {
	case <-ctx.Done():
	case <-hold.C:
	}
	return nil
}
