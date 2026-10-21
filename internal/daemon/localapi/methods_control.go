package localapi

import (
	"context"
	"encoding/json"

	"github.com/YangTaeyoung/deskhand/internal/daemon/control"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

// registerControl adds the control methods (docs/local-api.md §3 제어권);
// each answers the ControlState after it.
func (s *Server) registerControl() {
	if s.d.Control == nil {
		return
	}
	s.methods["control.get"] = s.controlGet
	s.methods["control.acquire"] = s.controlAcquire
	s.methods["control.release"] = s.controlChange((*control.Manager).Release)
	s.methods["control.stop"] = s.controlChange((*control.Manager).Stop)
	s.methods["control.resume_ai"] = s.controlChange((*control.Manager).ResumeAI)
}

type controlParams struct {
	HostID string `json:"host_id"`
	As     string `json:"as"`
	Force  bool   `json:"force"`
}

// controlHost decodes the params and resolves their host.
func (s *Server) controlHost(raw json.RawMessage) (controlParams, string, error) {
	var p controlParams
	if err := decode(raw, &p); err != nil {
		return p, "", err
	}
	if err := hostRequired(p.HostID); err != nil {
		return p, "", err
	}
	row, err := s.d.Hosts.Resolve(p.HostID)
	if err != nil {
		return p, "", err
	}
	return p, row.HostID, nil
}

func (s *Server) controlGet(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	_, hostID, err := s.controlHost(raw)
	if err != nil {
		return nil, err
	}
	return s.d.Control.Get(hostID), nil
}

func (s *Server) controlAcquire(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	p, hostID, err := s.controlHost(raw)
	if err != nil {
		return nil, err
	}
	return s.d.Control.Acquire(ctx, hostID, control.Owner(p.As), sess.Caller(), p.Force)
}

// controlChange serves a control method that changes the state with fn.
func (s *Server) controlChange(fn func(*control.Manager, context.Context, string, model.Caller) (model.ControlState, error)) handler {
	return func(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
		_, hostID, err := s.controlHost(raw)
		if err != nil {
			return nil, err
		}
		return fn(s.d.Control, ctx, hostID, sess.Caller())
	}
}
