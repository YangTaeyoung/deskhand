package wire

import (
	"maps"
	"slices"
)

// journaledOps are the agent ops that change state on the PC. The daemon
// sends their requests with a rid and the agent records those requests in
// its journal (docs/protocol.md §3).
var journaledOps = map[string]bool{
	"exec.run": true, "exec.start": true, "exec.write": true, "exec.kill": true, "proc.kill": true,
	"fs.write": true, "fs.rename": true, "fs.mkdir": true, "fs.delete": true, "fs.copy": true, "fs.trash": true,
	"xfer.commit": true, "xfer.abort": true, "app.launch": true, "debug.electron.launch": true, "clipboard.set": true,
	"win.action": true, "ui.act": true,
}

// Journaled reports whether op is a journaled op.
func Journaled(op string) bool { return journaledOps[op] }

// JournaledOps returns the journaled ops in sorted order.
func JournaledOps() []string { return slices.Sorted(maps.Keys(journaledOps)) }
