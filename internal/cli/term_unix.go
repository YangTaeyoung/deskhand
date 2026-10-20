//go:build darwin || linux

package cli

import (
	"errors"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// stdinTerminal is the terminal of the process's stdin.
type stdinTerminal struct{}

// Size returns the window size of stdin's terminal, 80 by 24 when it
// reports none.
func (stdinTerminal) Size() (int, int, bool) {
	if !stdinIsTerminal() {
		return 0, 0, false
	}
	ws, err := unix.IoctlGetWinsize(int(os.Stdin.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 || ws.Row == 0 {
		return 80, 24, true
	}
	return int(ws.Col), int(ws.Row), true
}

// Raw turns off echo, line editing, signals and output processing of
// stdin's terminal and makes reads return each byte as it comes.
func (stdinTerminal) Raw() (func(), error) {
	if !stdinIsTerminal() {
		return nil, errors.New("stdin is not a terminal")
	}
	fd := int(os.Stdin.Fd())
	old, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &raw); err != nil {
		return nil, err
	}
	return func() { _ = unix.IoctlSetTermios(fd, ioctlWriteTermios, old) }, nil
}

// Resizes delivers SIGWINCH as a value on the returned channel.
func (stdinTerminal) Resizes() (<-chan struct{}, func()) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	out := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sig:
				select {
				case out <- struct{}{}:
				default:
				}
			case <-done:
				return
			}
		}
	}()
	return out, func() {
		signal.Stop(sig)
		close(done)
	}
}
