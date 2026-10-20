//go:build windows

package server

import (
	"syscall"
	"time"
	"unsafe"
)

var procGetVolumePathName = syscall.NewLazyDLL("kernel32.dll").NewProc("GetVolumePathNameW")

// volumeRoots returns the root of the volume that holds path, named as path
// names it (GetVolumePathNameW).
func volumeRoots(path string) ([]string, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	buf := make([]uint16, len(path)+syscall.MAX_PATH)
	if r, _, err := procGetVolumePathName.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); r == 0 {
		return nil, err
	}
	return []string{syscall.UTF16ToString(buf)}, nil
}

// fileReadAttributes is FILE_READ_ATTRIBUTES.
const fileReadAttributes = 0x80

// platformFileID returns the volume serial number and file index of path,
// read on a handle opened with FILE_READ_ATTRIBUTES access and read, write
// and delete sharing.
func platformFileID(path string, follow bool) (fileID, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return fileID{}, err
	}
	flags := uint32(syscall.FILE_FLAG_BACKUP_SEMANTICS)
	if !follow {
		flags |= syscall.FILE_FLAG_OPEN_REPARSE_POINT
	}
	h, err := syscall.CreateFile(name, fileReadAttributes,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, flags, 0)
	if err != nil {
		return fileID{}, err
	}
	defer syscall.CloseHandle(h)
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		return fileID{}, err
	}
	return fileID{volume: uint64(info.VolumeSerialNumber), index: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)}, nil
}

// clipboardPollFastest and clipboardPollSlowest bound the time between two
// clipboard polls, which read the clipboard sequence number here.
const (
	clipboardPollFastest = 400 * time.Millisecond
	clipboardPollSlowest = 400 * time.Millisecond
)
