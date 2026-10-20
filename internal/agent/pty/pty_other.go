//go:build darwin || linux

package pty

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Console is a program running on a POSIX pseudo-terminal in a session of
// its own, whose process group the program leads.
type Console struct {
	master *os.File
	cmd    *exec.Cmd

	closeOnce sync.Once
	closeErr  error
}

// Start opens a pseudo-terminal of spec's size and starts spec's program
// with the terminal as its controlling terminal and standard streams.
func Start(spec Spec) (*Console, error) {
	if err := CheckSize(spec.Cols, spec.Rows); err != nil {
		return nil, err
	}
	master, slaveName, err := openMaster()
	if err != nil {
		return nil, err
	}
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, err
	}
	defer slave.Close()
	if err := setSize(slave, spec.Cols, spec.Rows); err != nil {
		master.Close()
		return nil, err
	}
	c := &exec.Cmd{Path: spec.Path, Args: spec.Args, Dir: spec.Dir, Env: spec.Env, Stdin: slave, Stdout: slave, Stderr: slave,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}}
	if err := c.Start(); err != nil {
		master.Close()
		return nil, err
	}
	return &Console{master: master, cmd: c}, nil
}

// Read reads the program's terminal output; it fails once the program and
// every process holding the terminal ended, or after Close.
func (c *Console) Read(p []byte) (int, error) { return c.master.Read(p) }

// Write sends input to the program's terminal.
func (c *Console) Write(p []byte) (int, error) { return c.master.Write(p) }

// Resize sets the size of the terminal.
func (c *Console) Resize(cols, rows int) error {
	if err := CheckSize(cols, rows); err != nil {
		return err
	}
	return setSize(c.master, cols, rows)
}

// setSize sets the window size of the terminal of f.
func setSize(f *os.File, cols, rows int) error {
	return control(f, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(rows), Col: uint16(cols)})
	})
}

// control runs fn with the descriptor of f without changing its blocking
// mode.
func control(f *os.File, fn func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

// Pid returns the process id of the program.
func (c *Console) Pid() int { return c.cmd.Process.Pid }

// Wait waits for the program to exit and returns its exit code; a program
// ended by a signal has 128 plus the signal's number.
func (c *Console) Wait() (int, error) {
	err := c.cmd.Wait()
	st := c.cmd.ProcessState
	if st == nil {
		return -1, err
	}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), nil
	}
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return st.ExitCode(), err
	}
	return st.ExitCode(), nil
}

// Shutdown has nothing to end on this system: the terminal ends with the
// last process that holds it.
func (c *Console) Shutdown() {}

// Close closes the terminal's master side.
func (c *Console) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.master.Close() })
	return c.closeErr
}
