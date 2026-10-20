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
	"syscall"
	"unicode/utf16"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// invalidArgument is an INVALID_ARGUMENT error with msg.
func invalidArgument(msg string) error { return coded(wire.CodeInvalidArgument, msg, false) }

// isWindowsSeparator reports whether c separates the names of a Windows path.
func isWindowsSeparator(c byte) bool { return c == '\\' || c == '/' }

// stripLongPrefix returns the Windows path p without a \\?\ prefix before a
// drive letter, and with a \\?\UNC\ prefix turned into \\.
func stripLongPrefix(p string) string {
	if hasPrefixFold(p, `\\?\UNC\`) {
		return `\\` + p[8:]
	}
	return withoutLongPrefix(p)
}

// hasPrefixFold reports whether s starts with prefix in any letter case.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// isVolumeGUIDPath reports whether the Windows path p starts with
// \\?\Volume{.
func isVolumeGUIDPath(p string) bool { return hasPrefixFold(p, `\\?\Volume{`) }

// isDevicePath reports whether the Windows path p names a device: it starts
// with \\.\, or with \\?\ followed by anything other than a drive letter
// and a colon, UNC\ or Volume{.
func isDevicePath(p string) bool {
	if len(p) >= 4 && isWindowsSeparator(p[0]) && isWindowsSeparator(p[1]) && p[2] == '.' && isWindowsSeparator(p[3]) {
		return true
	}
	if !strings.HasPrefix(p, `\\?\`) {
		return false
	}
	rest := p[4:]
	drive := len(rest) >= 2 && rest[1] == ':' && (rest[0]|0x20 >= 'a' && rest[0]|0x20 <= 'z') && (len(rest) == 2 || rest[2] == '\\')
	return !drive && !hasPrefixFold(rest, `UNC\`) && !hasPrefixFold(rest, "Volume{")
}

// normalizeWindowsPath returns the Windows path p cleaned and without a \\?\
// prefix: \\?\C:\… becomes C:\…, \\?\UNC\server\share\… becomes
// \\server\share\…, and \\?\Volume{…}\… becomes the same path below the
// first drive or folder the volume is mounted at (volumeDOSPath). A device
// path (isDevicePath) and a volume that is mounted nowhere are
// INVALID_ARGUMENT errors.
func normalizeWindowsPath(p string) (string, error) {
	p = filepath.Clean(p)
	if isDevicePath(p) {
		return "", invalidArgument(fmt.Sprintf("'%s' is a Windows device path; pass a path on a drive or a share, such as C:\\Users\\name\\file.txt", p))
	}
	if isVolumeGUIDPath(p) {
		dos, err := volumeDOSPath(p)
		if err != nil {
			return "", invalidArgument(fmt.Sprintf("'%s' names a volume that has no drive letter or folder to reach it by (%v); mount the volume at a drive letter and pass that path", p, err))
		}
		return filepath.Clean(dos), nil
	}
	return stripLongPrefix(p), nil
}

// windowsVolumeLen returns the length of the volume that starts the Windows
// path p: 2 for a drive ("C:"), the length of \\server\share for a share or
// of \\?\name for another \\?\ path, and 0 for a path without one.
func windowsVolumeLen(p string) int {
	if len(p) >= 2 && p[1] == ':' {
		return 2
	}
	if len(p) < 2 || !isWindowsSeparator(p[0]) || !isWindowsSeparator(p[1]) {
		return 0
	}
	n := 2
	for n < len(p) && !isWindowsSeparator(p[n]) {
		n++
	}
	if n < len(p) {
		n++
	}
	for n < len(p) && !isWindowsSeparator(p[n]) {
		n++
	}
	return n
}

// windowsNameError returns the INVALID_ARGUMENT error for name, one name of
// the path p, when Windows does not hold it as written: it has a control
// character or one of < > : " | ? *, it ends with a dot or a space, or it is
// a device name (wire.CheckWindowsName). Other names answer nil.
func windowsNameError(p, name string) error {
	switch fault, r := wire.CheckWindowsName(name); fault {
	case wire.NameBadChar:
		return invalidArgument(fmt.Sprintf("'%s' has the character %q in the name '%s', which Windows does not allow in names; pass a name without it", p, r, name))
	case wire.NameTrailing:
		return invalidArgument(fmt.Sprintf("'%s' has the name '%s', which ends with a dot or a space that Windows drops; pass the name without it", p, name))
	case wire.NameDevice:
		return invalidArgument(fmt.Sprintf("'%s' has the name '%s', which Windows reserves for a device; pass another name", p, name))
	}
	return nil
}

// windowsPathError returns the INVALID_ARGUMENT error for the absolute
// Windows path p when it names a device (isDevicePath) or one of the names
// after its volume fails windowsNameError; other paths answer nil.
func windowsPathError(p string) error {
	if isDevicePath(p) {
		return invalidArgument(fmt.Sprintf("'%s' is a Windows device path; pass a path on a drive or a share, such as C:\\Users\\name\\file.txt", p))
	}
	rest := p[windowsVolumeLen(p):]
	for _, name := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		if err := windowsNameError(p, name); err != nil {
			return err
		}
	}
	return nil
}

// checkPath returns the absolute path p cleaned, and on Windows without a
// \\?\ prefix (normalizeWindowsPath), when the file operations may act on it:
// on Windows p passes windowsPathError, and with roots p passes checkRoots.
// A relative p is an INVALID_ARGUMENT error.
func checkPath(p string, roots []string) (string, error) {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		var err error
		if p, err = normalizeWindowsPath(p); err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(p) {
		return "", invalidArgument(fmt.Sprintf("'%s' is not an absolute path; pass a full path", p))
	}
	if runtime.GOOS == "windows" {
		if err := windowsPathError(p); err != nil {
			return "", err
		}
	}
	if err := checkRoots(p, roots); err != nil {
		return "", err
	}
	return p, nil
}

// rootsArg returns the folders of the "roots" field of req, an array of
// absolute paths, cleaned; nil when req has no roots or an empty array.
func rootsArg(req proto.Message) ([]string, error) {
	v, ok := req["roots"]
	if !ok || v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, invalidArgument("roots must be an array of absolute folder paths")
	}
	var roots []string
	for _, e := range list {
		r, ok := e.(string)
		if !ok {
			return nil, invalidArgument("roots must be an array of absolute folder paths")
		}
		roots = append(roots, r)
	}
	return cleanRoots(roots)
}

// cleanRoots returns the absolute folder paths list cleaned, on Windows
// without a \\?\ prefix (normalizeWindowsPath); nil for an empty list.
func cleanRoots(list []string) ([]string, error) {
	var roots []string
	for _, r := range list {
		if r == "" {
			return nil, invalidArgument("roots must be an array of absolute folder paths")
		}
		r = filepath.Clean(r)
		if runtime.GOOS == "windows" {
			var err error
			if r, err = normalizeWindowsPath(r); err != nil {
				return nil, err
			}
		}
		if !filepath.IsAbs(r) {
			return nil, invalidArgument(fmt.Sprintf("the root '%s' in roots is not an absolute path; pass absolute folder paths", r))
		}
		roots = append(roots, r)
	}
	return roots, nil
}

// checkedPathArg returns the path of field key of req, resolved against the
// home of v and checked by checkPath against roots; every error it returns
// carries a code.
func checkedPathArg(v homeView, req proto.Message, key string, roots []string) (string, error) {
	p, err := pathArgIn(v, req, key)
	if err != nil {
		return "", invalidArgument(err.Error())
	}
	return checkPath(p, roots)
}

// missing reports whether err, the error of an Lstat, says that the entry
// does not exist.
func missing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// resolvedPath returns path with the links, junctions, short names and
// letter case of the part of it that exists resolved (finalPath) and the
// rest appended as it is. With follow unset the last element of path is kept
// and only the folder it is in is resolved. A link on the way whose target
// does not exist is an error.
func resolvedPath(path string, follow bool) (string, error) {
	if !follow {
		dir := filepath.Dir(path)
		if dir == path {
			return resolvedPath(path, true)
		}
		rd, err := resolvedPath(dir, true)
		if err != nil {
			return "", err
		}
		return filepath.Join(rd, filepath.Base(path)), nil
	}
	var rest []string
	for cur := path; ; {
		_, err := os.Lstat(cur)
		if err == nil {
			fp, err := finalPath(cur)
			if missing(err) {
				return "", fmt.Errorf("'%s' is a link whose target does not exist", cur)
			}
			if err != nil {
				return "", err
			}
			return filepath.Join(append([]string{fp}, rest...)...), nil
		}
		if !missing(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return path, nil
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}

// checkRoots returns nil when roots is empty, or when path, with the folder
// it is in resolved, and what path leads to, with all of it resolved
// (resolvedPath), both lie in a folder of roots, each resolved the same way.
// Otherwise it returns a PERMISSION_DENIED error.
func checkRoots(path string, roots []string) error {
	if len(roots) == 0 {
		return nil
	}
	fold := runtime.GOOS == "windows"
	allowed := make([]string, 0, len(roots))
	for _, r := range roots {
		if rr, err := resolvedPath(r, true); err == nil {
			r = rr
		}
		allowed = append(allowed, r)
	}
	list := strings.Join(roots, ", ")
	for _, follow := range []bool{false, true} {
		rp, err := resolvedPath(path, follow)
		if err != nil {
			return coded(wire.CodePermissionDenied, fmt.Sprintf("'%s' cannot be resolved (%v), so it cannot be shown to lie in the folders this host allows (%s); pass a path inside them", path, err, list), false)
		}
		if slices.ContainsFunc(allowed, func(r string) bool { return pathContains(r, rp, filepath.Separator, fold) }) {
			continue
		}
		if rp == path || fold && strings.EqualFold(rp, path) {
			return coded(wire.CodePermissionDenied, fmt.Sprintf("'%s' lies outside the folders this host allows (%s); pass a path inside them", path, list), false)
		}
		return coded(wire.CodePermissionDenied, fmt.Sprintf("'%s' leads to '%s', outside the folders this host allows (%s); pass a path inside them", path, rp, list), false)
	}
	return nil
}

// utf16Len returns the length of s in UTF-16 code units, the unit of
// Windows path lengths.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// fsFailure returns err, an error of the file system, as an error with the
// code it stands for, whose sentence is what, the reason and the next step:
// NOT_FOUND for a missing entry, DISK_FULL for a full volume,
// PERMISSION_DENIED (retryable) for an entry another program holds open,
// PERMISSION_DENIED for a refused access, INTERNAL otherwise.
func fsFailure(what string, err error) error {
	code, retry, next := wire.CodeInternal, false, "check the path with fs.stat and retry"
	switch {
	case errors.Is(err, fs.ErrNotExist):
		code, next = wire.CodeNotFound, "check the path with fs.stat"
	case isDiskFull(err):
		code, next = wire.CodeDiskFull, "free space on that volume and retry"
	case isInUse(err):
		code, retry, next = wire.CodePermissionDenied, true, "another program has it open, so close that program and retry"
	case errors.Is(err, fs.ErrPermission):
		code, next = wire.CodePermissionDenied, "check its permissions and whether it is read-only, then retry"
	}
	return coded(code, fmt.Sprintf("%s: %s; %s", what, strings.TrimSuffix(err.Error(), "."), next), retry)
}
