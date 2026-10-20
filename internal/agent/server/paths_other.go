//go:build !windows

package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// extendedPath returns p.
func extendedPath(p string) string { return p }

// finalPath returns the path of what p leads to with its links resolved.
func finalPath(p string) (string, error) { return filepath.EvalSymlinks(p) }

// actualName returns the name of the existing entry p as its folder holds
// it: the last element of p when the folder has an entry of exactly that
// name, else the entry whose name differs from it only in letter case, else
// the last element of p.
func actualName(p string) (string, error) {
	base := filepath.Base(p)
	ents, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		return "", err
	}
	folded := ""
	for _, e := range ents {
		if e.Name() == base {
			return base, nil
		}
		if folded == "" && strings.EqualFold(e.Name(), base) {
			folded = e.Name()
		}
	}
	if folded != "" {
		return folded, nil
	}
	return base, nil
}

// renameNoReplace renames the file or folder from to the path to, which must
// not exist; when to exists the error matches fs.ErrExist. A file is linked
// to its new name and its old name removed; a folder is renamed after a
// check that to does not exist.
func renameNoReplace(from, to string) error {
	if fi, err := os.Lstat(from); err == nil && !fi.IsDir() {
		err := os.Link(from, to)
		if err == nil {
			_ = os.Remove(from)
			return nil
		}
		if errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	if _, err := os.Lstat(to); err == nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: fs.ErrExist}
	}
	return os.Rename(from, to)
}

// isDiskFull reports whether err says that the volume or the user's quota
// is full.
func isDiskFull(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// isInUse reports whether err says that the entry is busy.
func isInUse(err error) bool { return errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.ETXTBSY) }

// platformTrash refuses: the Recycle Bin is a Windows one.
func platformTrash(path string, _ fs.FileInfo) error {
	return coded(wire.CodeCapabilityUnsupported, fmt.Sprintf("fs.trash moves entries to the Windows Recycle Bin, and this agent runs on %s; delete '%s' permanently with fs.delete instead", runtime.GOOS, path), false)
}
