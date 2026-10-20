package server

import (
	"context"
	"fmt"
	"math"

	"github.com/YangTaeyoung/deskhand/internal/agent/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// maxDebugPorts bounds the count of debug.ports.
const maxDebugPorts = 8

func (s *Server) registerTunnel() {
	s.register("debug.ports", true, s.debugPorts)
}

// ServeTunnel serves a tunnel stream (docs/protocol.md §2): a connection
// to a loopback port of this PC. An agent without exec refuses it.
func (s *Server) ServeTunnel(ctx context.Context, st TransferStream, h wire.StreamHeader) {
	if !s.opt.AllowExec {
		defer st.Close()
		refuseTransfer(st, coded(wire.CodeCapabilityUnsupported, errExecDisabled, false))
		return
	}
	tunnel.Serve(ctx, st, h)
}

// debugPorts answers count loopback TCP ports that are free at the time of
// the call.
func (s *Server) debugPorts(_ context.Context, req proto.Message) (proto.Message, error) {
	f, ok := req["count"].(float64)
	if n, isInt := req["count"].(int); isInt {
		f, ok = float64(n), true
	}
	if !ok || f != math.Trunc(f) || f < 1 || f > maxDebugPorts {
		return nil, invalidArgument(fmt.Sprintf("count must be a whole number from 1 to %d", maxDebugPorts))
	}
	ports, err := tunnel.FreePorts(int(f))
	if err != nil {
		return nil, fmt.Errorf("cannot bind a loopback port (%v); check that this PC allows local sockets", err)
	}
	out := make([]any, len(ports))
	for i, p := range ports {
		out[i] = p
	}
	return proto.Message{"ports": out}, nil
}
