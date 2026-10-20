package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/diag"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func (s *Server) registerDiag() {
	s.register("diag.system", true, s.diagSystem)
	s.register("diag.services", true, s.diagServices)
	s.register("diag.events", true, s.diagEvents)
	s.register("diag.crashes", true, s.diagCrashes)
}

// diagSource returns Options.Diag, or the diagnostics of this PC.
func (s *Server) diagSource() diag.Source {
	if s.opt.Diag != nil {
		return s.opt.Diag
	}
	return diag.Local()
}

// diagCap is the state of the diag capability: unavailable without exec,
// ready with Options.Diag or on Windows, unsupported elsewhere.
func (s *Server) diagCap() capState {
	switch {
	case !s.opt.AllowExec:
		return capState{State: capUnavailable, Reason: errExecDisabled}
	case s.opt.Diag != nil || diag.Supported:
		return capState{State: capReady}
	}
	return capState{State: capUnsupported, Reason: diag.ErrUnsupported.Error()}
}

// diagError returns err of a diag reader with its code: unsupported for
// diag.ErrUnsupported, invalid for a diag.InvalidError, internal and
// retryable otherwise.
func diagError(err error) error {
	var inv *diag.InvalidError
	switch {
	case errors.Is(err, diag.ErrUnsupported):
		return coded(wire.CodeCapabilityUnsupported, err.Error(), false)
	case errors.As(err, &inv):
		return invalidArgument(inv.Text)
	}
	return coded(wire.CodeInternal, err.Error(), true)
}

func (s *Server) diagSystem(ctx context.Context, _ proto.Message) (proto.Message, error) {
	sys, err := s.diagSource().System(ctx)
	if err != nil {
		return nil, diagError(err)
	}
	return proto.Message{"os": sys.OS, "computer_name": sys.ComputerName, "cpu": sys.CPU, "memory": sys.Memory, "disks": sys.Disks,
		"gpus": sys.GPUs, "boot_time": sys.BootTime, "uptime_s": sys.UptimeS}, nil
}

func (s *Server) diagServices(ctx context.Context, req proto.Message) (proto.Message, error) {
	name, err := optionalString(req, "name")
	if err != nil {
		return nil, err
	}
	services, err := s.diagSource().Services(ctx, name)
	if err != nil {
		return nil, diagError(err)
	}
	return proto.Message{"services": services}, nil
}

func (s *Server) diagEvents(ctx context.Context, req proto.Message) (proto.Message, error) {
	q, err := eventsQuery(req, time.Now())
	if err != nil {
		return nil, err
	}
	res, err := s.diagSource().Events(ctx, q)
	if err != nil {
		return nil, diagError(err)
	}
	return proto.Message{"events": res.Events, "truncated": res.Truncated}, nil
}

func (s *Server) diagCrashes(ctx context.Context, req proto.Message) (proto.Message, error) {
	raw, err := optionalString(req, "since")
	if err != nil {
		return nil, err
	}
	since, err := wire.DiagCrashesSince(raw, time.Now())
	if err != nil {
		return nil, invalidArgument(fmt.Sprintf("since '%s' is not an RFC 3339 time; pass it like 2026-10-20T00:00:00Z", cutChars(raw, 64)))
	}
	dumps, err := s.diagSource().Crashes(ctx, since)
	if err != nil {
		return nil, diagError(err)
	}
	return proto.Message{"dumps": dumps}, nil
}

// optionalString returns the string field key of req, "" when absent, and
// an invalid argument when it is not a string.
func optionalString(req proto.Message, key string) (string, error) {
	v, ok := req[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", invalidArgument(fmt.Sprintf("%s must be a string", key))
	}
	return s, nil
}

// eventsQuery reads the parameters of diag.events at now and checks them
// with wire.DiagEventsParams.Check.
func eventsQuery(req proto.Message, now time.Time) (wire.DiagEventsQuery, error) {
	var p wire.DiagEventsParams
	var err error
	for _, f := range []struct {
		key string
		dst *string
	}{{"log", &p.Log}, {"since", &p.Since}, {"until", &p.Until}, {"provider", &p.Provider}} {
		if *f.dst, err = optionalString(req, f.key); err != nil {
			return wire.DiagEventsQuery{}, err
		}
	}
	if v, ok := req["levels"]; ok && v != nil {
		if p.Levels = req.Strings("levels"); p.Levels == nil {
			return wire.DiagEventsQuery{}, invalidArgument(fmt.Sprintf("levels must be an array of %v", wire.DiagEventLevels))
		}
	}
	if v, ok := req["max"]; ok && v != nil {
		f, isNum := v.(float64)
		if !isNum || f != math.Trunc(f) || math.Abs(f) > math.MaxInt32 {
			return wire.DiagEventsQuery{}, invalidArgument(fmt.Sprintf("max must be a whole number from 1 to %d", wire.DiagEventsMaxLimit))
		}
		n := int(f)
		p.Max = &n
	}
	q, fault := p.Check(now)
	if fault == nil {
		return q, nil
	}
	switch fault.Field {
	case "log":
		return q, invalidArgument(fmt.Sprintf("unknown log '%s'; pass one of %v", cutChars(fault.Value, 64), wire.DiagEventLogs))
	case "since", "until":
		return q, invalidArgument(fmt.Sprintf("%s '%s' is not an RFC 3339 time at or after since; pass it like 2026-10-20T00:00:00Z", fault.Field, cutChars(fault.Value, 64)))
	case "levels":
		return q, invalidArgument(fmt.Sprintf("unknown level '%s'; pass levels from %v", cutChars(fault.Value, 64), wire.DiagEventLevels))
	}
	return q, invalidArgument(fmt.Sprintf("max must be a whole number from 1 to %d", wire.DiagEventsMaxLimit))
}
