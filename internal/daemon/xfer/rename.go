package xfer

import (
	"errors"
	"io/fs"
	"os"
)

// linkRename renames the file from to the path to, which must not exist,
// by linking it to its new name and removing the old one; a volume without
// links checks that to does not exist and renames. When to exists the
// error matches fs.ErrExist.
func linkRename(from, to string) error {
	err := os.Link(from, to)
	if err == nil {
		_ = os.Remove(from)
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return err
	}
	if _, err := os.Lstat(to); err == nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: fs.ErrExist}
	}
	return os.Rename(from, to)
}
