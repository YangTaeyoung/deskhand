package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/input"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// errInputUnsupported is the reason of the input capability without an
// injector for this system.
const errInputUnsupported = "input injection needs a Windows agent"

// Input returns the injector of the input stream and of the input ops.
func (s *Server) Input() *input.Injector { return s.input }

// newInjector returns the injector of the server: its session and
// displays are the server's, a rise of the writer generation is the event
// input.preempted, and a paste's clipboard writes are the agent's own.
func (s *Server) newInjector() *input.Injector {
	return input.New(input.Options{
		Logger:      s.opt.Logger,
		SessionInfo: s.opt.SessionInfo,
		Displays:    s.opt.Displays,
		Preempted: func(p input.Preemption) {
			released := p.Released
			if released == nil {
				released = []input.Pressed{}
			}
			s.emit("input.preempted", proto.Message{"gen": p.Gen, "previous": p.Previous, "released": released})
		},
		ClipboardWritten: s.markOwnClipboard,
	})
}

// registerInput adds input.state, input.release_all and
// control.set_writer.
func (s *Server) registerInput() {
	s.register("input.state", false, func(context.Context, proto.Message) (proto.Message, error) {
		return proto.Message{"gen": s.input.Gen(), "pressed": s.input.Pressed()}, nil
	})
	s.register("input.release_all", false, func(context.Context, proto.Message) (proto.Message, error) {
		released, err := s.input.ReleaseAll()
		if released == nil {
			released = []input.Pressed{}
		}
		if err != nil {
			return nil, releaseError(released, err)
		}
		return proto.Message{"released": released}, nil
	})
	s.register("control.set_writer", false, func(_ context.Context, req proto.Message) (proto.Message, error) {
		gen, ok := wholeGen(req["gen"])
		if !ok {
			return nil, coded(wire.CodeInvalidArgument, "control.set_writer needs gen, a whole number of 0 or more", false)
		}
		return proto.Message{"gen": s.input.SetWriter(gen)}, nil
	})
}

// writerGated runs h only when the request's gen, 0 when absent, is not
// older than the writer generation; a newer gen becomes the writer
// generation.
func (s *Server) writerGated(op string, h Handler) Handler {
	return func(ctx context.Context, req proto.Message) (proto.Message, error) {
		gen := uint64(0)
		if v, present := req["gen"]; present {
			var ok bool
			if gen, ok = wholeGen(v); !ok {
				return nil, coded(wire.CodeInvalidArgument, fmt.Sprintf("%s gen must be a whole number of 0 or more, the writer generation of the request", op), false)
			}
		}
		if err := s.input.Admit(gen); err != nil {
			return nil, inputError(err)
		}
		return h(ctx, req)
	}
}

// wholeGen reads a writer generation: a whole JSON number of 0 or more.
func wholeGen(v any) (uint64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int:
		f = float64(n)
	default:
		return 0, false
	}
	if f < 0 || f != math.Trunc(f) || f > 1<<53 {
		return 0, false
	}
	return uint64(f), true
}

// inputError gives an input failure its code; it is not retryable.
func inputError(err error) error {
	var e *input.Error
	if errors.As(err, &e) {
		return coded(e.Code, e.Msg, false)
	}
	return coded(wire.CodeInternal, err.Error(), true)
}

// releaseError is the error of a release_all that freed released but
// failed on the rest; its message names what was freed.
func releaseError(released []input.Pressed, err error) error {
	var e *input.Error
	code, msg := wire.CodeInternal, err.Error()
	if errors.As(err, &e) {
		code, msg = e.Code, e.Msg
	}
	freed := "nothing"
	if len(released) > 0 {
		names := make([]string, len(released))
		for i, p := range released {
			names[i] = pressedName(p)
		}
		freed = strings.Join(names, ", ")
	}
	return coded(code, fmt.Sprintf("released %s; the rest could not be released: %s", freed, msg), false)
}

// pressedName names a held key or button for a message.
func pressedName(p input.Pressed) string {
	switch {
	case p.B != "":
		return "button " + p.B
	case p.VK != 0:
		return fmt.Sprintf("vk %#x", p.VK)
	case p.Ext:
		return fmt.Sprintf("sc %#x (ext)", p.SC)
	default:
		return fmt.Sprintf("sc %#x", p.SC)
	}
}

// inputCap is the input capability: unsupported without an injector or
// without Windows session information, unavailable when the session
// cannot be read, in Session 0 or disconnected, needs_user while the PC is
// locked or shows the secure desktop, and ready otherwise.
func inputCap(supported bool, info session.Info, err error) capState {
	switch {
	case !supported:
		return capState{State: capUnsupported, Reason: errInputUnsupported}
	case errors.Is(err, session.ErrUnsupported):
		return capState{State: capUnsupported, Reason: err.Error()}
	case err != nil:
		return capState{State: capUnavailable, Reason: err.Error()}
	}
	switch reason := input.Outage(info); reason {
	case "":
		return capState{State: capReady}
	case capture.ReasonSessionLocked, capture.ReasonSecureDesktop:
		return capState{State: capNeedsUser, Reason: reason}
	default:
		return capState{State: capUnavailable, Reason: reason}
	}
}
