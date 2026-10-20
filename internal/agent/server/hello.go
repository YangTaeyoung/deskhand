package server

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"runtime"

	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// capState is the state of one capability in hello.caps and caps.changed:
// ready, unavailable, unsupported or needs_user, with a reason unless ready.
type capState struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// capStates maps each capability key of docs/protocol.md §3 to its state.
type capStates map[string]capState

const (
	capReady       = "ready"
	capUnavailable = "unavailable"
	capUnsupported = "unsupported"
)

// notImplemented is the reason of the capabilities this agent does not have.
const notImplemented = "not implemented in this agent version"

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

// checkCaps returns the state of every capability key. exec, files and
// processes follow Options.AllowExec, uia the UI helper and clipboard the
// clipboard's availability; the other keys are unsupported.
func (s *Server) checkCaps() capStates {
	ready := capState{State: capReady}
	caps := capStates{}
	for _, k := range []string{"screen", "input", "pty", "tunnel", "diag"} {
		caps[k] = capState{State: capUnsupported, Reason: notImplemented}
	}
	for _, k := range []string{"exec", "files", "processes"} {
		if s.opt.AllowExec {
			caps[k] = ready
		} else {
			caps[k] = capState{State: capUnavailable, Reason: errExecDisabled}
		}
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
		"host": host, "caps": caps, "session": s.helloSession(), "displays": []any{}, "boot_id": bootID}
}
