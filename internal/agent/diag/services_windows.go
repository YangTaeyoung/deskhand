//go:build windows

package diag

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// readServices lists the Win32 services whose name or display name contains
// filter, through the service control manager, sorted by name.
func readServices(filter string) ([]Service, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, fmt.Errorf("the service control manager could not be opened (%v); retry", err)
	}
	defer windows.CloseServiceHandle(scm)
	out := []Service{}
	buf := make([]byte, 256<<10)
	var resume uint32
	for {
		var needed, count uint32
		err := windows.EnumServicesStatusEx(scm, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32, windows.SERVICE_STATE_ALL,
			&buf[0], uint32(len(buf)), &needed, &count, &resume, nil)
		if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
			return nil, fmt.Errorf("the services could not be listed (%v); retry", err)
		}
		if count > 0 {
			for _, e := range unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), count) {
				name, display := windows.UTF16PtrToString(e.ServiceName), windows.UTF16PtrToString(e.DisplayName)
				if !serviceMatches(filter, name, display) {
					continue
				}
				st := e.ServiceStatusProcess
				out = append(out, Service{Name: name, DisplayName: display, State: serviceState(st.CurrentState),
					StartType: serviceStartType(scm, e.ServiceName), PID: int(st.ProcessId)})
			}
		}
		if err == nil {
			break
		}
		if count == 0 {
			if int(needed) <= len(buf) {
				return nil, fmt.Errorf("the services could not be listed (%v); retry", err)
			}
			buf = make([]byte, needed)
		}
	}
	sortServices(out)
	return out, nil
}

// serviceStartType reads the start type of the service named name, unknown
// when its configuration cannot be read.
func serviceStartType(scm windows.Handle, name *uint16) string {
	h, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return startType(^uint32(0), false)
	}
	defer windows.CloseServiceHandle(h)
	var needed uint32
	_ = windows.QueryServiceConfig(h, nil, 0, &needed)
	if needed == 0 {
		return startType(^uint32(0), false)
	}
	b := make([]byte, needed)
	cfg := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&b[0]))
	if windows.QueryServiceConfig(h, cfg, needed, &needed) != nil {
		return startType(^uint32(0), false)
	}
	delayed := false
	if cfg.StartType == windows.SERVICE_AUTO_START {
		var info windows.SERVICE_DELAYED_AUTO_START_INFO
		var n uint32
		if windows.QueryServiceConfig2(h, windows.SERVICE_CONFIG_DELAYED_AUTO_START_INFO, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), &n) == nil {
			delayed = info.IsDelayedAutoStartUp != 0
		}
	}
	return startType(cfg.StartType, delayed)
}
