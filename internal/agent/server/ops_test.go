package server

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/agent/session"
)

// foundationOps are the ops of phases 1 and 3 that the agent serves
// (docs/protocol.md §3.1).
var foundationOps = []string{
	"ping",
	"exec.run", "exec.start", "exec.read", "exec.write", "exec.kill", "exec.list",
	"proc.list", "proc.kill",
	"fs.stat", "fs.list", "fs.read", "fs.tail", "fs.write", "fs.rename", "fs.mkdir", "fs.delete", "fs.copy", "fs.trash", "fs.hash",
	"xfer.prepare", "xfer.commit", "xfer.abort",
	"app.launch",
	"win.list", "win.action",
	"ui.tree", "ui.find", "ui.text", "ui.rect", "ui.act", "ui.focused", "ui.at", "ui.ping",
	"clipboard.get", "clipboard.set",
	"pty.list",
	"debug.ports", "debug.electron.launch", "debug.tree", "debug.attach.pid",
	"diag.system", "diag.services", "diag.events", "diag.crashes",
}

// mediaOps are the ops of the media track that the agent serves
// (docs/protocol.md §3.1).
var mediaOps = []string{
	"time.sync", "session.info", "session.rdp", "display.list", "screen.capture", "input.state", "input.release_all", "control.set_writer",
}

func TestTheAgentRegistersTheOpsOfBothTracks(t *testing.T) {
	s := New(Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	defer s.Close()
	got := slices.Sorted(maps.Keys(s.ops))
	want := slices.Sorted(slices.Values(slices.Concat(foundationOps, mediaOps)))
	if !slices.Equal(got, want) {
		t.Fatalf("registered ops\n%v\nwant\n%v", got, want)
	}
}

func TestSessionInfoAnswersTheWindowsSessionWhenADesktopSessionIsSet(t *testing.T) {
	c, _ := newRPCClient(t, Options{
		Session:     func() (session.Session, bool) { return session.Session{ID: "c2", User: "desk"}, true },
		SessionInfo: func() (session.Info, error) { return testSession, nil },
	})
	got := c.ok("session.info", nil)
	if fields := onlyKeys(got, wantSessionFields); !reflect.DeepEqual(fields, wantSessionFields) {
		t.Fatalf("session.info %v", got)
	}
	if _, ok := got["session"]; ok {
		t.Fatalf("session.info carries the desktop session: %v", got)
	}
}
