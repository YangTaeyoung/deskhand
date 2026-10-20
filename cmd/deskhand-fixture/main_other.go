//go:build !windows

// deskhand-fixture is a Windows test application; see main_windows.go.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "deskhand-fixture runs only on Windows")
	os.Exit(1)
}
