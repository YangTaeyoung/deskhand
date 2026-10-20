package pty

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procUpdateProcThreadAttribute = windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")

// Console is a program running on a ConPTY pseudo console of its own. The
// console reads its input from one anonymous pipe and writes the screen as
// UTF-8 text with VT sequences to another.
type Console struct {
	hpc     windows.Handle
	in      *os.File
	out     *os.File
	process windows.Handle
	pid     int

	shutOnce  sync.Once
	closeOnce sync.Once
}

// Start creates a pseudo console of spec's size and starts spec's program
// attached to it, in the session and with the token of this process. The
// program inherits no handles; its standard handles are those of the
// pseudo console.
func Start(spec Spec) (*Console, error) {
	if err := CheckSize(spec.Cols, spec.Rows); err != nil {
		return nil, err
	}
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("create the input pipe: %w", err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		return nil, fmt.Errorf("create the output pipe: %w", err)
	}
	c := &Console{in: os.NewFile(uintptr(inWrite), "conpty-in"), out: os.NewFile(uintptr(outRead), "conpty-out")}
	err := windows.CreatePseudoConsole(windows.Coord{X: int16(spec.Cols), Y: int16(spec.Rows)}, inRead, outWrite, 0, &c.hpc)
	windows.CloseHandle(inRead)
	windows.CloseHandle(outWrite)
	if err != nil {
		c.in.Close()
		c.out.Close()
		return nil, fmt.Errorf("create the pseudo console: %w", err)
	}
	if err := c.startProcess(spec); err != nil {
		windows.ClosePseudoConsole(c.hpc)
		c.in.Close()
		c.out.Close()
		return nil, err
	}
	return c, nil
}

// startProcess creates spec's process with the pseudo console attribute.
func (c *Console) startProcess(spec Spec) error {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fmt.Errorf("allocate the process attributes: %w", err)
	}
	defer attrs.Delete()
	if r, _, err := procUpdateProcThreadAttribute.Call(uintptr(unsafe.Pointer(attrs.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(c.hpc), unsafe.Sizeof(c.hpc), 0, 0); r == 0 {
		return fmt.Errorf("set the pseudo console attribute: %w", err)
	}
	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.ProcThreadAttributeList = attrs.List()
	app, err := windows.UTF16PtrFromString(spec.Path)
	if err != nil {
		return err
	}
	line := spec.CmdLine
	if line == "" {
		line = windows.ComposeCommandLine(spec.Args)
	}
	cmdline, err := windows.UTF16FromString(line)
	if err != nil {
		return err
	}
	var dir *uint16
	if spec.Dir != "" {
		if dir, err = windows.UTF16PtrFromString(spec.Dir); err != nil {
			return err
		}
	}
	env, err := envBlock(spec.Env)
	if err != nil {
		return err
	}
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(app, &cmdline[0], nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		&env[0], dir, &si.StartupInfo, &pi); err != nil {
		return fmt.Errorf("start %s: %w", spec.Path, err)
	}
	windows.CloseHandle(pi.Thread)
	c.process, c.pid = pi.Process, int(pi.ProcessId)
	return nil
}

// envBlock returns env as a Windows environment block: each NAME=value
// ending with a zero, and a zero after the last.
func envBlock(env []string) ([]uint16, error) {
	var block []uint16
	for _, kv := range env {
		for _, r := range kv {
			if r == 0 {
				return nil, errors.New("an environment variable contains a NUL character")
			}
		}
		block = append(block, utf16.Encode([]rune(kv))...)
		block = append(block, 0)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}

// Read reads the console's output; it reaches the end after Shutdown, once
// the console has written what it held.
func (c *Console) Read(p []byte) (int, error) { return c.out.Read(p) }

// Write sends input to the console as UTF-8 text with VT sequences.
func (c *Console) Write(p []byte) (int, error) { return c.in.Write(p) }

// Resize sets the size of the console.
func (c *Console) Resize(cols, rows int) error {
	if err := CheckSize(cols, rows); err != nil {
		return err
	}
	return windows.ResizePseudoConsole(c.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

// Pid returns the process id of the program.
func (c *Console) Pid() int { return c.pid }

// Wait waits for the program to exit and returns its exit code.
func (c *Console) Wait() (int, error) {
	if _, err := windows.WaitForSingleObject(c.process, windows.INFINITE); err != nil {
		return -1, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(c.process, &code); err != nil {
		return -1, err
	}
	return int(int32(code)), nil
}

// Shutdown closes the pseudo console, which ends the processes still
// attached to it; Read reaches the end of the output after it. It may wait
// until the output is read.
func (c *Console) Shutdown() {
	c.shutOnce.Do(func() { windows.ClosePseudoConsole(c.hpc) })
}

// Close shuts the console down and closes the pipes and the process handle.
func (c *Console) Close() error {
	c.Shutdown()
	c.closeOnce.Do(func() {
		c.in.Close()
		c.out.Close()
		windows.CloseHandle(c.process)
	})
	return nil
}
