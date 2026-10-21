package wire

import (
	"slices"
	"testing"
)

func TestWriterOpsAreTheUIOpsOfTheWriterGeneration(t *testing.T) {
	want := []string{"app.launch", "clipboard.set", "debug.electron.launch", "ui.act", "win.action"}
	if got := WriterOps(); !slices.Equal(got, want) {
		t.Fatalf("WriterOps %v", got)
	}
	for _, op := range want {
		if !WriterOp(op) {
			t.Errorf("%s is not a writer op", op)
		}
	}
	for _, op := range []string{"ui.tree", "ui.find", "win.list", "clipboard.get", "screen.capture", "exec.run", "input.release_all", "control.set_writer"} {
		if WriterOp(op) {
			t.Errorf("%s is a writer op", op)
		}
	}
}
