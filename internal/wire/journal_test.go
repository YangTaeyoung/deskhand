package wire

import (
	"slices"
	"testing"
)

func TestJournaledOpsAreTheStateChangingOps(t *testing.T) {
	want := []string{"app.launch", "clipboard.set", "exec.kill", "exec.run", "exec.start", "exec.write", "fs.delete", "fs.mkdir",
		"fs.rename", "fs.write", "proc.kill", "ui.act", "win.action"}
	if got := JournaledOps(); !slices.Equal(got, want) {
		t.Fatalf("JournaledOps %v", got)
	}
	for _, op := range want {
		if !Journaled(op) {
			t.Errorf("%s is not journaled", op)
		}
	}
	for _, op := range []string{"fs.list", "fs.read", "proc.list", "exec.read", "exec.list", "ping", "req.status", "session.info"} {
		if Journaled(op) {
			t.Errorf("%s is journaled", op)
		}
	}
}
