//go:build !windows

package main

import (
	"errors"
	"io"
	"log"
	"os"

	"github.com/YangTaeyoung/deskhand/internal/agent/config"
	"github.com/YangTaeyoung/deskhand/internal/agent/server"
)

// errWindowsOnly is the error of install and uninstall outside Windows.
var errWindowsOnly = errors.New("install and uninstall run on the Windows PC; run deskhand-agent run here instead")

// prepareDataDir creates dataDir, for the agent's user only when it is new.
func prepareDataDir(dataDir string) error {
	return os.MkdirAll(dataDir, 0o700)
}

// newUI returns nil: UI Automation exists only on Windows.
func newUI(string, *log.Logger, func() *server.Server) server.UI { return nil }

func install(config.Agent, string, io.Writer) error { return errWindowsOnly }

func uninstall(string, bool, io.Writer) error { return errWindowsOnly }
