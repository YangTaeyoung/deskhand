//go:build windows

package server

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// GetFinalPathNameByHandleW flags.
const (
	fileNameNormalized = 0x0
	volumeNameDOS      = 0x0
	volumeNameGUID     = 0x1
)

// extendedPath returns the absolute path p in the \\?\ form that Windows
// takes past MAX_PATH: \\?\C:\… for a drive and \\?\UNC\server\share\… for
// a share. A path already in a \\?\ or \\.\ form is returned as it is.
func extendedPath(p string) string {
	switch {
	case strings.HasPrefix(p, `\\?\`), strings.HasPrefix(p, `\\.\`):
		return p
	case strings.HasPrefix(p, `\\`):
		return `\\?\UNC\` + p[2:]
	}
	return `\\?\` + p
}

// finalPath returns the path of what p leads to with its links, junctions,
// short names and letter case resolved (GetFinalPathNameByHandleW), named
// by its drive letter or share, or by its volume GUID when it has neither,
// without a \\?\ prefix before a drive or a share.
func finalPath(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(extendedPath(p))
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(name, fileReadAttributes,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &fs.PathError{Op: "open", Path: p, Err: err}
	}
	defer windows.CloseHandle(h)
	var last error
	for _, volume := range []uint32{volumeNameDOS, volumeNameGUID} {
		s, err := finalPathOf(h, fileNameNormalized|volume)
		if err == nil {
			return stripLongPrefix(s), nil
		}
		last = err
	}
	return "", &fs.PathError{Op: "GetFinalPathNameByHandle", Path: p, Err: last}
}

// finalPathOf returns the path of the open handle h in the form flags asks
// GetFinalPathNameByHandleW for.
func finalPathOf(h windows.Handle, flags uint32) (string, error) {
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), flags)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n+1)
	}
}

// volumeDOSPath returns the path p, which starts with \\?\Volume{…}, below
// the first drive or folder that volume is mounted at
// (GetVolumePathNamesForVolumeNameW).
func volumeDOSPath(p string) (string, error) {
	end := strings.IndexByte(p, '}')
	if end < 0 {
		return "", errors.New("the volume name has no closing brace")
	}
	name, err := windows.UTF16PtrFromString(p[:end+1] + `\`)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		var n uint32
		err := windows.GetVolumePathNamesForVolumeName(name, &buf[0], uint32(len(buf)), &n)
		if errors.Is(err, windows.ERROR_MORE_DATA) {
			buf = make([]uint16, n)
			continue
		}
		if err != nil {
			return "", err
		}
		break
	}
	first := windows.UTF16ToString(buf)
	if first == "" {
		return "", errors.New("it is mounted nowhere")
	}
	return filepath.Join(first, strings.TrimLeft(p[end+1:], `\`)), nil
}

// actualName returns the name of the existing entry p as its folder holds
// it (FindFirstFileW), which differs from the last element of p in letter
// case when only the case differs.
func actualName(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(extendedPath(p))
	if err != nil {
		return "", err
	}
	var data windows.Win32finddata
	h, err := windows.FindFirstFile(name, &data)
	if err != nil {
		return "", &fs.PathError{Op: "FindFirstFile", Path: p, Err: err}
	}
	windows.FindClose(h)
	return windows.UTF16ToString(data.FileName[:]), nil
}

// renameNoReplace renames the file or folder from to the path to, which must
// not exist (MoveFileExW without MOVEFILE_REPLACE_EXISTING, written through
// to disk); when to exists the error matches fs.ErrExist.
func renameNoReplace(from, to string) error {
	f, err := windows.UTF16PtrFromString(extendedPath(from))
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(extendedPath(to))
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(f, t, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

// isDiskFull reports whether err says that the volume is full.
func isDiskFull(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}

// isInUse reports whether err says that another program holds the entry
// open without sharing it.
func isInUse(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
