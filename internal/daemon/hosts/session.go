package hosts

import (
	"context"
	"strconv"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// unknown fills the SessionInfo fields the agent did not report.
const unknown = "unknown"

// inspectTimeout bounds session.inspect and the session check of
// host.doctor, including the wait for a connecting host.
const inspectTimeout = 30 * time.Second

// sessionInfo merges the hello's session with a session.info answer, the
// answer winning, into a SessionInfo; nil when neither names a session.
func sessionInfo(hello, info proto.Message) *model.SessionInfo {
	src := proto.Message{}
	for k, v := range hello {
		src[k] = v
	}
	for k, v := range info {
		src[k] = v
	}
	id, hasID := intField(src, "session_id")
	if !hasID {
		id, hasID = intField(src, "session")
	}
	user := src.String("user")
	if !hasID && user == "" {
		return nil
	}
	s := &model.SessionInfo{SessionID: id, User: user, SID: src.String("sid"), ConnectState: orUnknown(src.String("connect_state")),
		Protocol: orUnknown(src.String("protocol")), InputDesktop: orUnknown(src.String("input_desktop")),
		Integrity: src.String("integrity")}
	if locked, ok := src["locked"].(bool); ok {
		s.Locked = &locked
	}
	return s
}

func orUnknown(s string) string {
	if s == "" {
		return unknown
	}
	return s
}

// intField reads an integer that is a JSON number or a decimal string.
func intField(m proto.Message, key string) (int, bool) {
	switch v := m[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case uint32:
		return int(v), true
	case string:
		n, err := strconv.Atoi(v)
		return n, err == nil
	}
	return 0, false
}

// Inspect describes the host's desktop session: session.info, displays,
// capabilities, control state and connection generation.
func (r *Registry) Inspect(ctx context.Context, ref string) (model.SessionInspect, error) {
	h, row, err := r.Conn(ref)
	if err != nil {
		return model.SessionInspect{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, inspectTimeout)
	defer cancel()
	var info proto.Message
	res, err := h.Call(cctx, "session.info", nil, callOpts())
	switch {
	case err == nil:
		info = res
	case isUnsupported(err):
		info = h.SessionInfo()
	default:
		return model.SessionInspect{}, err
	}
	m := r.Model(row)
	s := sessionInfo(h.Hello().Session, info)
	if s == nil {
		s = &model.SessionInfo{ConnectState: unknown, Protocol: unknown, InputDesktop: unknown}
	}
	return model.SessionInspect{
		SessionInfo:          *s,
		ControlState:         r.opt.Control(row.HostID),
		HostID:               row.HostID,
		Displays:             m.Displays,
		Caps:                 m.Caps,
		ConnectionGeneration: h.Generation(),
	}, nil
}
