//go:build !windows

package server

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// clipboardPollFastest and clipboardPollSlowest bound the time between two
// clipboard polls, which run a clipboard tool here.
const (
	clipboardPollFastest = 500 * time.Millisecond
	clipboardPollSlowest = 4 * time.Second
)

// volumeRoots returns the root folder.
func volumeRoots(string) ([]string, error) { return []string{"/"}, nil }

// platformFileID returns the device and inode of path.
func platformFileID(path string, follow bool) (fileID, error) {
	stat := os.Lstat
	if follow {
		stat = os.Stat
	}
	fi, err := stat(path)
	if err != nil {
		return fileID{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, errors.New("the file system reports no device and inode")
	}
	return fileID{volume: uint64(st.Dev), index: uint64(st.Ino)}, nil
}

// platformEntryOwner reports no owners on this system.
func platformEntryOwner(string) (string, bool) { return "", false }
