package localapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// registerUI adds the UI Automation methods (docs/local-api.md §3 UI
// Automation): the agent ops of the same name with host_id.
func (s *Server) registerUI() {
	for _, op := range []string{"ui.tree", "ui.find", "ui.text", "ui.rect", "ui.focused", "ui.at", "win.list"} {
		s.methods[op] = s.uiRead(op)
	}
	s.methods["ui.act"] = s.uiWrite("ui.act")
	s.methods["win.action"] = s.uiWrite("win.action")
}

// uiParams decodes the params of a UI method: host_id and the agent op's
// own params.
func (s *Server) uiParams(raw json.RawMessage) (*conn.Host, string, proto.Message, error) {
	params := proto.Message{}
	if err := decode(raw, &params); err != nil {
		return nil, "", nil, err
	}
	ref, _ := params["host_id"].(string)
	if err := hostRequired(ref); err != nil {
		return nil, "", nil, &paramError{e: apierr.As(err)}
	}
	delete(params, "host_id")
	h, row, err := s.d.Hosts.Conn(ref)
	if err != nil {
		return nil, "", nil, err
	}
	return h, row.HostID, params, nil
}

// uiSummary is the timeline summary of a UI op with params.
func uiSummary(op string, params proto.Message) string {
	out := "UI Automation " + op
	if a := params.String("action"); a != "" {
		out += " " + a
	}
	for _, k := range []string{"ref", "name", "nameContains", "automationId", "window"} {
		if v := params.String(k); v != "" {
			return fmt.Sprintf("%s · %s=%s", out, k, v)
		}
	}
	return out
}

// uiRead serves a UI op that only reads: its answer with host_id and
// connection_generation, the agent's refusal as the error. ui.at also
// takes capture_id with x, y of that capture, sent as the virtual desktop
// point they land on. Each read is recorded in the timeline.
func (s *Server) uiRead(op string) handler {
	return func(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
		h, hostID, params, err := s.uiParams(raw)
		if err != nil {
			return nil, err
		}
		fields := map[string]any{}
		if id := params.String("capture_id"); op == "ui.at" && id != "" {
			x, okX := params["x"].(float64)
			y, okY := params["y"].(float64)
			if !okX || !okY || s.d.Observe == nil {
				return nil, &paramError{e: apierr.Invalid("capture_id와 함께 그 이미지의 x, y를 주세요")}
			}
			t, err := s.d.Observe.Locate(hostID, id, x, y)
			if err != nil {
				return nil, asParam(err)
			}
			delete(params, "capture_id")
			params["x"], params["y"] = t.ScreenX, t.ScreenY
			fields["target"] = t
		}
		info := conn.CallInfo{}
		res, err := h.Call(ctx, op, params, conn.CallOpts{Info: &info})
		if pe := requestProblem(err); pe != nil {
			return nil, pe
		}
		tl := model.TimelineOK
		if err != nil {
			tl = model.TimelineFailed
		}
		_, _ = s.d.Bus.Record(sess.Caller().Entry(hostID, model.TimelineObserve, uiSummary(op, params), tl, ""))
		if err != nil {
			return nil, err
		}
		for k, v := range res {
			fields[k] = v
		}
		fields["host_id"], fields["connection_generation"] = hostID, info.Generation
		return fields, nil
	}
}

// uiWrite serves ui.act or win.action: a writer op under control, as
// app.launch is. An AI client's request takes or renews its lease and is
// refused while a person holds control; the request goes out only while
// the writer it carries still holds right before it is written.
func (s *Server) uiWrite(op string) handler {
	return func(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
		h, hostID, params, err := s.uiParams(raw)
		if err != nil {
			return nil, err
		}
		summary := uiSummary(op, params)
		gen, err := s.writerGen(hostID, sess.Caller())
		if err != nil {
			_, _ = s.d.Bus.Record(sess.Caller().Entry(hostID, model.TimelineInput, summary, model.TimelineDenied, ""))
			return s.envelope(hostID, "", conn.CallInfo{}, outcome{status: model.StatusFailed, action: model.NotStarted(), err: apierr.As(err)}, nil).Map(nil), nil
		}
		params["gen"] = gen
		cctx, cancel := s.detached(remoteTimeout)
		defer cancel()
		info := conn.CallInfo{}
		res, err := h.Call(cctx, op, params, conn.CallOpts{Info: &info, Ready: s.writerCheck(hostID, sess.Caller(), gen)})
		if pe := requestProblem(err); pe != nil {
			return nil, pe
		}
		if apierr.CodeOf(err) == wire.CodeControlPreempted && info.Answered && s.d.Control != nil {
			s.d.Control.Resync(hostID, gen)
		}
		o := outcomeOf(info, err)
		fields := map[string]any{}
		if err == nil {
			for k, v := range res {
				fields[k] = v
			}
		}
		tl := timelineOutcome(o.status)
		if code := apierr.CodeOf(err); !info.Sent && (code == wire.CodeControlHeldByHuman || code == wire.CodeControlPreempted) {
			tl = model.TimelineDenied
		}
		_, _ = s.d.Bus.Record(sess.Caller().Entry(hostID, model.TimelineInput, summary, tl, ""))
		return s.envelope(hostID, "", info, o, nil).Map(fields), nil
	}
}
