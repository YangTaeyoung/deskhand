package wire

import (
	"maps"
	"slices"
)

// writerOps are the UI ops that carry the writer generation in gen: the
// agent runs one only when its gen is not older than the current writer
// generation (docs/protocol.md §3.1), and the daemon sends each with the gen
// of its control state.
var writerOps = map[string]bool{"ui.act": true, "win.action": true, "clipboard.set": true, "app.launch": true,
	"debug.electron.launch": true}

// WriterOp reports whether op is a writer op.
func WriterOp(op string) bool { return writerOps[op] }

// WriterOps returns the writer ops in sorted order.
func WriterOps() []string { return slices.Sorted(maps.Keys(writerOps)) }
