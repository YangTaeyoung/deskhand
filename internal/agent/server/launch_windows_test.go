//go:build windows

package server

import (
	"strconv"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestLaunchReturnsItsCreationTimeForGuardedCleanup(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	before := time.Now().Add(-time.Second)
	res := c.ok("app.launch", launchExit(0, proto.Message{"wait_window_ms": 0}))
	started, err := wire.ParseDiagTime(res.String("started"))
	if err != nil || started.Before(before) || started.After(time.Now()) {
		t.Fatalf("launch did not identify the created process: %v (%v)", res, err)
	}
	c.event("process.exited")
}

// exitProgram returns the app.launch path and args of a program that exits
// with code.
func exitProgram(code int) (string, []string) {
	return "cmd.exe", []string{"/c", "exit", strconv.Itoa(code)}
}
