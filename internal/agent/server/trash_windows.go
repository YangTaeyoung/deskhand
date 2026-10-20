//go:build windows

package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

var (
	shell32                = windows.NewLazySystemDLL("shell32.dll")
	procSHFileOperationW   = shell32.NewProc("SHFileOperationW")
	procSHQueryRecycleBinW = shell32.NewProc("SHQueryRecycleBinW")
)

// SHFileOperationW function and flags.
const (
	foDelete               = 0x3
	fofSilent              = 0x4
	fofNoConfirmation      = 0x10
	fofAllowUndo           = 0x40
	fofNoErrorUI           = 0x400
	fofNoConnectedElements = 0x2000
)

// shellPathLimit bounds the paths fs.trash gives the Recycle Bin: the path
// of the entry and of everything in it must be shorter, in UTF-16 code
// units.
const shellPathLimit = 250

// Registry keys of the Recycle Bin settings, below HKEY_CURRENT_USER and
// HKEY_LOCAL_MACHINE.
const (
	bitBucketVolumes = `Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\`
	explorerPolicies = `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`
)

// shFileOpStruct is SHFILEOPSTRUCTW of 64-bit Windows.
type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

// shQueryRBInfo is SHQUERYRBINFO of 64-bit Windows.
type shQueryRBInfo struct {
	cbSize      uint32
	i64Size     int64
	i64NumItems int64
}

// platformTrash moves the entry at path, whose Lstat is fi, to the current
// user's Recycle Bin: it takes the canonical form of path (canonicalPath),
// checks that the Recycle Bin keeps the entry there (recyclable) and moves
// it (shellRecycle).
func platformTrash(path string, fi fs.FileInfo) error {
	canonical, err := canonicalPath(path)
	if err != nil {
		return err
	}
	if err := recyclable(canonical, fi); err != nil {
		return err
	}
	return shellRecycle(canonical)
}

// isDrivePath reports whether p starts with a drive letter, a colon and a
// separator.
func isDrivePath(p string) bool {
	return len(p) >= 3 && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z') && p[1] == ':' && p[2] == '\\'
}

// canonicalPath returns path with its folder resolved (resolvedPath: links,
// junctions, short names and letter case) and its last element as the long
// name its folder holds (actualName), so that a link is kept as itself. A
// canonical form that does not start with a drive letter (X:\…), such as a
// share, is a CAPABILITY_UNSUPPORTED error.
func canonicalPath(path string) (string, error) {
	dir, err := resolvedPath(filepath.Dir(path), true)
	if err != nil {
		return "", fsFailure(fmt.Sprintf("cannot resolve the folder of '%s'", path), err)
	}
	name, err := actualName(path)
	if err != nil {
		return "", fsFailure(fmt.Sprintf("cannot read '%s'", path), err)
	}
	canonical := filepath.Join(dir, name)
	if !isDrivePath(canonical) {
		return "", unsupportedTrash(path, fmt.Sprintf("it lies at '%s', which is not on a drive letter of this PC", canonical), "delete it permanently with fs.delete instead")
	}
	return canonical, nil
}

// unsupportedTrash is the CAPABILITY_UNSUPPORTED error of an entry the
// Recycle Bin does not keep, with the reason why and the next step.
func unsupportedTrash(path, why, next string) error {
	return coded(wire.CodeCapabilityUnsupported, fmt.Sprintf("the Recycle Bin cannot keep '%s' because %s; %s", path, why, next), false)
}

// recyclable returns nil when the Recycle Bin of the current user keeps the
// entry at the canonical path path, whose Lstat is fi: path is not in a
// Recycle Bin, path is shorter than shellPathLimit, it lies on a fixed local
// volume that has a Recycle Bin, no policy of HKEY_LOCAL_MACHINE or
// HKEY_CURRENT_USER and no setting of the volume removes deleted files at
// once, every path in the folder it names is shorter than shellPathLimit,
// and its size, with everything in it, is within the volume's Recycle Bin
// size. Otherwise it returns a CAPABILITY_UNSUPPORTED error, or the error
// that kept it from checking.
func recyclable(path string, fi fs.FileInfo) error {
	const permanently = "delete it permanently with fs.delete instead"
	rest := path[windowsVolumeLen(path):]
	if first, _, _ := strings.Cut(strings.TrimLeft(rest, `\`), `\`); strings.EqualFold(first, "$Recycle.Bin") {
		return unsupportedTrash(path, "it is in the Recycle Bin already", "delete it permanently with fs.delete, or leave it there")
	}
	if n := utf16Len(path); n >= shellPathLimit {
		return unsupportedTrash(path, fmt.Sprintf("its path is %d characters long and fs.trash takes paths shorter than %d characters", n, shellPathLimit),
			"move it to a shorter path with fs.rename and trash it there, or "+permanently)
	}
	root, err := volumeRoot(path)
	if err != nil {
		return fsFailure(fmt.Sprintf("cannot find the volume of '%s'", path), err)
	}
	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return err
	}
	if t := windows.GetDriveType(rootPtr); t != windows.DRIVE_FIXED {
		return unsupportedTrash(path, fmt.Sprintf("%s is %s, which has no Recycle Bin", root, driveKind(t)), permanently)
	}
	guid := make([]uint16, 64)
	if err := windows.GetVolumeNameForVolumeMountPoint(rootPtr, &guid[0], uint32(len(guid))); err != nil {
		return unsupportedTrash(path, fmt.Sprintf("%s is not the root of a volume (it may be a drive made with subst), so it has no Recycle Bin", root), permanently)
	}
	info := shQueryRBInfo{cbSize: uint32(unsafe.Sizeof(shQueryRBInfo{}))}
	if hr, _, _ := procSHQueryRecycleBinW.Call(uintptr(unsafe.Pointer(rootPtr)), uintptr(unsafe.Pointer(&info))); uint32(hr) != 0 {
		return unsupportedTrash(path, fmt.Sprintf("the volume %s has no Recycle Bin for this user (SHQueryRecycleBin 0x%08X)", root, uint32(hr)), permanently)
	}
	noRecycle, err := policyValues("NoRecycleFiles")
	if err != nil {
		return unreadableSetting(path, err)
	}
	if slices.ContainsFunc(noRecycle, func(v uint64) bool { return v != 0 }) {
		return unsupportedTrash(path, "the policy \"Do not move deleted files to the Recycle Bin\" is on", permanently)
	}
	volume := strings.TrimSuffix(strings.TrimPrefix(windows.UTF16ToString(guid), `\\?\Volume`), `\`)
	limit, err := recycleBinLimit(root, volume)
	var unread *settingError
	if errors.As(err, &unread) {
		return unreadableSetting(path, err)
	}
	if err != nil {
		return unsupportedTrash(path, err.Error(), permanently)
	}
	size, err := treeSize(path, fi)
	if err != nil {
		return err
	}
	if size > limit {
		return unsupportedTrash(path, fmt.Sprintf("it takes %d bytes, more than the %d bytes the Recycle Bin of %s holds", size, limit, root),
			permanently+", or raise the size of that Recycle Bin")
	}
	return nil
}

// treeSize returns the size of the entry at path, whose Lstat is fi, with
// the sizes of the files in the folder it names, without following links;
// a path in that folder as long as shellPathLimit or longer is a
// CAPABILITY_UNSUPPORTED error.
func treeSize(path string, fi fs.FileInfo) (int64, error) {
	if !fi.IsDir() {
		return fi.Size(), nil
	}
	var size int64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fsFailure(fmt.Sprintf("cannot read '%s'", p), err)
		}
		if n := utf16Len(p); n >= shellPathLimit {
			return unsupportedTrash(path, fmt.Sprintf("it holds '%s', whose path is %d characters long, and fs.trash takes paths shorter than %d characters", p, n, shellPathLimit),
				"move what it holds to shorter paths, or delete it permanently with fs.delete instead")
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return fsFailure(fmt.Sprintf("cannot read '%s'", p), err)
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// volumeRoot returns the root of the volume that holds path, such as C:\
// (GetVolumePathNameW).
func volumeRoot(path string) (string, error) {
	roots, err := volumeRoots(path)
	if err != nil {
		return "", err
	}
	return roots[0], nil
}

// driveKind names the drive type t of GetDriveTypeW.
func driveKind(t uint32) string {
	switch t {
	case windows.DRIVE_REMOVABLE:
		return "a removable drive"
	case windows.DRIVE_REMOTE:
		return "a network drive"
	case windows.DRIVE_CDROM:
		return "a CD or DVD drive"
	case windows.DRIVE_RAMDISK:
		return "a RAM disk"
	}
	return "not a fixed local drive"
}

// policyValues returns the DWORD name of the Explorer policies of
// HKEY_LOCAL_MACHINE and of HKEY_CURRENT_USER, each that is set, or the
// *settingError of the first that cannot be read.
func policyValues(name string) ([]uint64, error) {
	var out []uint64
	for _, hive := range []struct {
		key  registry.Key
		name string
	}{{registry.LOCAL_MACHINE, "HKEY_LOCAL_MACHINE"}, {registry.CURRENT_USER, "HKEY_CURRENT_USER"}} {
		v, ok, err := dwordValue(hive.key, hive.name, explorerPolicies, name)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// readDWORD reads the integer value name of the key path below hive.
var readDWORD = registryDWORD

// registryDWORD reads the integer value name of the key path below hive.
func registryDWORD(hive registry.Key, path, name string) (uint64, error) {
	k, err := registry.OpenKey(hive, path, registry.QUERY_VALUE)
	if err != nil {
		return 0, err
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(name)
	return v, err
}

// settingError is the failure to read a Recycle Bin setting or policy.
type settingError struct {
	name, key string
	err       error
}

func (e *settingError) Error() string {
	return fmt.Sprintf("the setting %s in %s could not be read (%v), so whether it keeps deleted files is unknown", e.name, e.key, e.err)
}

func (e *settingError) Unwrap() error { return e.err }

// unreadableSetting is the CAPABILITY_UNSUPPORTED error of fs.trash for
// path while the Recycle Bin setting of err cannot be read.
func unreadableSetting(path string, err error) error {
	return unsupportedTrash(path, err.Error(), "let the account of the agent read that setting and retry, or delete it permanently with fs.delete instead")
}

// dwordValue returns the integer value name of the key path below hive,
// whose name is hiveName, and whether it is set: a key or value that does
// not exist (registry.ErrNotExist) is not set, and any other failure of the
// read, such as a denied access or a value that is not an integer, is a
// *settingError.
func dwordValue(hive registry.Key, hiveName, path, name string) (uint64, bool, error) {
	v, err := readDWORD(hive, path, name)
	switch {
	case errors.Is(err, registry.ErrNotExist):
		return 0, false, nil
	case err != nil:
		return 0, false, &settingError{name: name, key: hiveName + `\` + path, err: err}
	}
	return v, true, nil
}

// recycleBinLimit returns the most bytes the Recycle Bin of the volume with
// the root root and the GUID volume ({…}) holds: the smallest of the
// MaxCapacity (MiB) of the volume's Recycle Bin settings and the shares of
// the volume's size in percent that the RecycleBinSize policies of
// HKEY_LOCAL_MACHINE and HKEY_CURRENT_USER set. A setting that removes
// deleted files at once (NukeOnDelete), a size policy while the volume's
// size cannot be read, and no limit at all are errors whose text says so;
// a setting that cannot be read is a *settingError.
func recycleBinLimit(root, volume string) (int64, error) {
	key := bitBucketVolumes + volume
	nuke, ok, err := dwordValue(registry.CURRENT_USER, "HKEY_CURRENT_USER", key, "NukeOnDelete")
	if err != nil {
		return 0, err
	}
	if ok && nuke != 0 {
		return 0, fmt.Errorf("the Recycle Bin of %s is set to remove files immediately when they are deleted", root)
	}
	limit := int64(-1)
	mb, ok, err := dwordValue(registry.CURRENT_USER, "HKEY_CURRENT_USER", key, "MaxCapacity")
	if err != nil {
		return 0, err
	}
	if ok {
		limit = int64(mb) << 20
	}
	pcts, err := policyValues("RecycleBinSize")
	if err != nil {
		return 0, err
	}
	if len(pcts) > 0 {
		var total uint64
		p, err := windows.UTF16PtrFromString(root)
		if err == nil {
			err = windows.GetDiskFreeSpaceEx(p, nil, &total, nil)
		}
		if err != nil {
			return 0, fmt.Errorf("the size of %s, which the RecycleBinSize policy needs, could not be read (%v)", root, err)
		}
		if byPolicy := int64(total / 100 * slices.Min(pcts)); limit < 0 || byPolicy < limit {
			limit = byPolicy
		}
	}
	if limit < 0 {
		return 0, fmt.Errorf("the size of the Recycle Bin of %s could not be read from its settings", root)
	}
	return limit, nil
}

// shellRecycle deletes path with SHFileOperationW and FOF_ALLOWUNDO, which
// moves it to the Recycle Bin, without any confirmation, progress or error
// window and without the entries the shell connects to path, such as the
// folder of a saved web page, and then checks that the Recycle Bin holds it
// (verifyRecycled). It runs on its thread in a single-threaded COM
// apartment.
func shellRecycle(path string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil || err == windows.Errno(1) {
		defer windows.CoUninitialize()
	}
	from, err := windows.UTF16FromString(path)
	if err != nil {
		return invalidArgument(fmt.Sprintf("'%s' holds a NUL character; pass a path without it", path))
	}
	from = append(from, 0)
	op := shFileOpStruct{wFunc: foDelete, pFrom: &from[0], fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI | fofNoConnectedElements}
	start := time.Now()
	r, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	runtime.KeepAlive(from)
	if code := uint32(r); code != 0 {
		return shellDeleteError(path, code)
	}
	if op.fAnyOperationsAborted != 0 {
		return coded(wire.CodeInternal, fmt.Sprintf("the Recycle Bin stopped before it moved '%s'; check it with fs.stat and retry", path), true)
	}
	if _, err := os.Lstat(path); err == nil {
		return coded(wire.CodeInternal, fmt.Sprintf("'%s' is still there after the move to the Recycle Bin; check it with fs.stat and retry", path), true)
	}
	return verifyRecycled(path, start)
}

// verifyRecycled returns nil when the current user's Recycle Bin on the
// volume of path holds an item deleted from path at since or later
// (findRecycleRecord), and an INTERNAL error otherwise.
func verifyRecycled(path string, since time.Time) error {
	root, err := volumeRoot(path)
	sid := processUserSID()
	if err == nil && sid == "" {
		err = errors.New("the account of the agent could not be read")
	}
	if err != nil {
		return coded(wire.CodeInternal, fmt.Sprintf("'%s' is gone, and the Recycle Bin could not be found to confirm that it holds it (%v); look for it in the Recycle Bin", path, err), false)
	}
	_, found, err := findRecycleRecord(filepath.Join(root, "$Recycle.Bin", sid), path, since)
	if err != nil {
		return coded(wire.CodeInternal, fmt.Sprintf("'%s' is gone, and the Recycle Bin could not be read to confirm that it holds it (%v); look for it in the Recycle Bin", path, err), false)
	}
	if !found {
		return coded(wire.CodeInternal, fmt.Sprintf("'%s' was deleted permanently instead of being moved to the Recycle Bin; restore it from a backup if it is needed", path), false)
	}
	return nil
}

// shellDeleteError is the error of the SHFileOperationW result code for
// path, after which nothing was moved.
func shellDeleteError(path string, code uint32) error {
	switch code {
	case 0x20, 0x21:
		return coded(wire.CodePermissionDenied, fmt.Sprintf("another program has '%s' open, so it was not moved to the Recycle Bin; close that program and retry", path), true)
	case 0x7C:
		return coded(wire.CodePermissionDenied, fmt.Sprintf("the Recycle Bin could not move '%s' (SHFileOperation error 0x7C); a program may have it or a file in it open, so close that program and retry", path), true)
	case 0x5, 0x78:
		return coded(wire.CodePermissionDenied, fmt.Sprintf("access to '%s' is denied, so it was not moved to the Recycle Bin; check its permissions and retry", path), false)
	case 0x2, 0x3:
		return coded(wire.CodeNotFound, fmt.Sprintf("'%s' was gone when it was to be moved to the Recycle Bin; check the path with fs.stat", path), false)
	}
	return coded(wire.CodeInternal, fmt.Sprintf("the Recycle Bin refused '%s' (SHFileOperation error 0x%X); check it with fs.stat and retry", path, code), false)
}
