package server

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"runtime"
	"slices"

	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// capState is the state of one capability in hello.caps and caps.changed:
// ready, unavailable, unsupported or needs_user, with a reason unless ready.
// Shells lists the shells of the pty capability.
type capState struct {
	State  string   `json:"state"`
	Reason string   `json:"reason,omitempty"`
	Shells []string `json:"shells,omitempty"`
}

// equal reports whether c and o have the same state, reason and shells.
func (c capState) equal(o capState) bool {
	return c.State == o.State && c.Reason == o.Reason && slices.Equal(c.Shells, o.Shells)
}

// capStates maps each capability key of docs/protocol.md §3 to its state.
type capStates map[string]capState

const (
	capReady       = "ready"
	capUnavailable = "unavailable"
	capUnsupported = "unsupported"
)

// errNoClipboard is the reason of an unavailable clipboard.
const errNoClipboard = "the clipboard of the desktop session cannot be reached; sign in to the PC's desktop"

// errNoUIHelper is the reason of an unavailable uia capability on Windows.
const errNoUIHelper = "the UI helper is not set up in this agent; restart deskhand-agent through its scheduled task"

// bootID names this agent process; it changes when the agent starts again.
var bootID = newBootID()

func newBootID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// errNoShell is the reason of a pty capability without a shell to start.
const errNoShell = "no shell this agent can start is installed on this PC"

// checkCaps returns the state of every capability key. screen follows
// Options.ScreenState (screenCap), input the agent's session (inputCap),
// exec, files, processes and tunnel Options.AllowExec, pty also the
// installed shells (ptyShells), diag also its source (diagCap), uia the UI
// helper and clipboard the clipboard's availability.
func (s *Server) checkCaps() capStates {
	ready := capState{State: capReady}
	caps := capStates{"diag": s.diagCap()}
	info, err := s.opt.SessionInfo()
	caps["screen"] = screenCap(s.opt.ScreenState())
	caps["input"] = inputCap(s.input.Supported(), info, err)
	for _, k := range []string{"exec", "files", "processes", "tunnel"} {
		if s.opt.AllowExec {
			caps[k] = ready
		} else {
			caps[k] = capState{State: capUnavailable, Reason: errExecDisabled}
		}
	}
	switch shells := ptyShells(); {
	case !s.opt.AllowExec:
		caps["pty"] = capState{State: capUnavailable, Reason: errExecDisabled}
	case len(shells) == 0:
		caps["pty"] = capState{State: capUnsupported, Reason: errNoShell}
	default:
		caps["pty"] = capState{State: capReady, Shells: shells}
	}
	switch {
	case s.opt.UI != nil:
		caps["uia"] = ready
	case runtime.GOOS != "windows":
		caps["uia"] = capState{State: capUnsupported, Reason: errNoUI}
	default:
		caps["uia"] = capState{State: capUnavailable, Reason: errNoUIHelper}
	}
	if s.opt.Clipboard.Available() {
		caps["clipboard"] = ready
	} else {
		caps["clipboard"] = capState{State: capUnavailable, Reason: errNoClipboard}
	}
	return caps
}

// version returns Options.Version, or "dev" when it is empty.
func (s *Server) version() string {
	if s.opt.Version == "" {
		return "dev"
	}
	return s.opt.Version
}

// helloMessage is the first line of an rpc stream, with caps as its
// capabilities.
func (s *Server) helloMessage(caps capStates) proto.Message {
	host, _ := os.Hostname()
	return proto.Message{"t": "hello", "v": proto.Version, "agent": s.version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"host": host, "caps": caps, "session": s.helloSession(), "displays": s.helloDisplays(), "boot_id": bootID}
}
