//go:build windows

package client

import (
	"os"
	"syscall"
)

// detached starts the daemon without a console tie.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }

// terminate stops the process pid.
func terminate(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
