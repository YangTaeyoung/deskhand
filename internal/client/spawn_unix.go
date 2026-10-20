//go:build unix

package client

import "syscall"

// detached puts the daemon in a session of its own.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// terminate sends SIGTERM to pid.
func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
