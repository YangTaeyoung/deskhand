package cli

import (
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
)

// openFile shows a file with the system's default app.
func openFile(path string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, path).Run()
}

// interrupts returns a channel of the interrupt and terminate signals that
// arrive from now on and the function that stops it.
func interrupts() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	return ch, func() { signal.Stop(ch) }
}
