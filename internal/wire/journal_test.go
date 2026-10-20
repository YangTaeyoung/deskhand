package wire

import (
	"slices"
	"testing"
)

func TestJournaledOpsAreTheStateChangingOps(t *testing.T) {
	want := []string{"app.launch", "clipboard.set", "debug.electron.launch", "exec.kill", "exec.run", "exec.start", "exec.write",
		"fs.copy", "fs.delete", "fs.mkdir", "fs.rename", "fs.trash", "fs.write", "proc.kill", "ui.act", "win.action", "xfer.abort", "xfer.commit"}
	if got := JournaledOps(); !slices.Equal(got, want) {
		t.Fatalf("JournaledOps %v", got)
	}
	for _, op := range want {
		if !Journaled(op) {
			t.Errorf("%s is not journaled", op)
		}
	}
	for _, op := range []string{"fs.list", "fs.read", "fs.hash", "proc.list", "exec.read", "exec.list", "ping", "req.status", "session.info", "xfer.prepare"} {
		if Journaled(op) {
			t.Errorf("%s is journaled", op)
		}
	}
}
