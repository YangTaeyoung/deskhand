//go:build !windows && !darwin && !linux

package pty

// Console is a program on a pseudo-terminal; this system has none.
type Console struct{}

// Start returns ErrUnsupported.
func Start(Spec) (*Console, error) { return nil, ErrUnsupported }

func (*Console) Read([]byte) (int, error)  { return 0, ErrUnsupported }
func (*Console) Write([]byte) (int, error) { return 0, ErrUnsupported }
func (*Console) Resize(int, int) error     { return ErrUnsupported }
func (*Console) Pid() int                  { return 0 }
func (*Console) Wait() (int, error)        { return -1, ErrUnsupported }
func (*Console) Shutdown()                 {}
func (*Console) Close() error              { return nil }
