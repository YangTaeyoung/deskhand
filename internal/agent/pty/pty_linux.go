package pty

import (
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// openMaster opens a new pseudo-terminal master, unlocks its slave and
// returns the master and the slave's device path.
func openMaster() (*os.File, string, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", err
	}
	var n uint32
	err = control(master, func(fd int) error {
		if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
			return err
		}
		var err error
		n, err = unix.IoctlGetUint32(fd, unix.TIOCGPTN)
		return err
	})
	if err != nil {
		master.Close()
		return nil, "", err
	}
	return master, "/dev/pts/" + strconv.FormatUint(uint64(n), 10), nil
}
