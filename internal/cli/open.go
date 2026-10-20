package cli

import (
	"os/exec"
	"runtime"
)

// openFile shows a file with the system's default app.
func openFile(path string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, path).Run()
}
