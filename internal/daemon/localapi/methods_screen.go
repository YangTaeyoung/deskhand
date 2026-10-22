package localapi

import (
	"context"
	"encoding/json"
	"io"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/screen"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func (s *Server) registerScreen() {
	if s.d.Screens == nil {
		return
	}
	s.streams["screen"] = s.screenStream
	s.streams["input"] = s.inputStream
}

func (s *Server) inputStream(ctx context.Context, sess *session, raw json.RawMessage) (any, func(io.ReadWriteCloser), error) {
	if sess.Caller().Actor == model.ActorAI {
		return nil, nil, apierr.New(wire.CodePermissionDenied, "AI 클라이언트는 사람 입력 스트림에 붙을 수 없습니다", false, "")
	}
	// Keep the input-only contract exact, including key spelling. The legacy
	// screen params intentionally retain their existing permissive decoder.
	var fields map[string]json.RawMessage
	if err := decode(raw, &fields); err != nil {
		return nil, nil, err
	}
	for k := range fields {
		switch k {
		case "kind", "host_id", "session_id", "sid", "connection_generation":
		default:
			return nil, nil, &paramError{e: apierr.Invalid("input 파라미터를 모릅니다: " + k)}
		}
	}
	var p struct {
		HostID               string  `json:"host_id"`
		SessionID            *uint32 `json:"session_id"`
		SID                  string  `json:"sid"`
		ConnectionGeneration *uint64 `json:"connection_generation"`
	}
	if err := decode(raw, &p); err != nil {
		return nil, nil, err
	}
	if err := hostRequired(p.HostID); err != nil {
		return nil, nil, &paramError{e: apierr.As(err)}
	}
	if p.SessionID == nil || p.SID == "" || p.ConnectionGeneration == nil || *p.ConnectionGeneration == 0 {
		return nil, nil, &paramError{e: apierr.Invalid("input에는 session_id(uint32), sid, connection_generation(uint64)가 필요합니다")}
	}
	_, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, nil, err
	}
	v, err := s.d.Screens.AttachInput(ctx, row.HostID, screen.InputBinding{SessionID: *p.SessionID, SID: p.SID, ConnectionGeneration: *p.ConnectionGeneration}, sess.Caller())
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{"host_id": row.HostID}, v.Run, nil
}

// screenParams are the params of stream.attach{kind:"screen"}.
type screenParams struct {
	HostID  string `json:"host_id"`
	Display int    `json:"display"`
	Preset  string `json:"preset"`
	MaxFPS  int    `json:"max_fps"`
}

// screenStream attaches a viewer of the session's client to a display of
// the host; the result carries the host, the display and the preset.
func (s *Server) screenStream(ctx context.Context, sess *session, raw json.RawMessage) (any, func(io.ReadWriteCloser), error) {
	var p screenParams
	if err := decode(raw, &p); err != nil {
		return nil, nil, err
	}
	if err := hostRequired(p.HostID); err != nil {
		return nil, nil, &paramError{e: apierr.As(err)}
	}
	_, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, nil, err
	}
	if sess.Caller().Actor == model.ActorAI && !s.d.Hosts.AIScope(row.HostID).Screen {
		return nil, nil, s.denied(sess, row.HostID, model.TimelineObserve, "stream.attach", scopeRefusal("화면 스트림을"))
	}
	v, err := s.d.Screens.Attach(ctx, row.HostID, p.Display, screen.Preset(p.Preset), p.MaxFPS, sess.Caller())
	if err != nil {
		if apierr.CodeOf(err) == wire.CodeInvalidArgument {
			return nil, nil, &paramError{e: apierr.As(err)}
		}
		return nil, nil, err
	}
	if sess.Caller().Actor == model.ActorAI && !s.d.Hosts.AIScope(row.HostID).Screen {
		v.Close()
		return nil, nil, s.denied(sess, row.HostID, model.TimelineObserve, "stream.attach", scopeRefusal("화면 스트림을"))
	}
	return map[string]any{"host_id": row.HostID, "display": p.Display, "preset": string(v.Preset()), "max_fps": p.MaxFPS}, v.Run, nil
}
