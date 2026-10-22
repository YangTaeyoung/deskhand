package localapi

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

// APIVersion is the local API version hello reports.
const APIVersion = 1

func (s *Server) registerSession() {
	s.methods["hello"] = s.hello
	s.methods["events.subscribe"] = s.eventsSubscribe
	s.methods["timeline.list"] = s.timelineList
	s.methods["session.rdp"] = s.sessionRDP
}

type helloParams struct {
	Client  string `json:"client"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (s *Server) hello(_ context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p helloParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	switch p.Client {
	case "app", "cli", "mcp":
	default:
		return nil, apierr.Invalid(`hello의 client는 "app", "cli", "mcp" 중 하나여야 합니다. client 값을 고쳐 다시 보내세요`)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if !sess.hello {
		sess.caller = model.Caller{Actor: model.ActorOf(p.Client), ClientID: newClientID(), ClientName: p.Name}
		sess.hello = true
	}
	return map[string]any{"client_id": sess.caller.ClientID, "daemon_version": s.d.Version, "api_version": APIVersion}, nil
}

type subscribeParams struct {
	HostID string   `json:"host_id"`
	Kinds  []string `json:"kinds"`
}

func (s *Server) eventsSubscribe(_ context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p subscribeParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	f := &filter{kinds: map[string]bool{}}
	if p.HostID != "" {
		row, err := s.d.Hosts.Resolve(p.HostID)
		if err != nil {
			return nil, err
		}
		f.hostID = row.HostID
	}
	for _, k := range p.Kinds {
		f.kinds[k] = true
	}
	sess.subscribe(f)
	kinds := p.Kinds
	if kinds == nil {
		kinds = []string{}
	}
	return map[string]any{"subscribed": true, "host_id": f.hostID, "kinds": kinds}, nil
}

type timelineParams struct {
	HostID string `json:"host_id"`
	Since  string `json:"since"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

// timelineMax bounds the entries of one timeline.list page.
const timelineMax = 1000

func (s *Server) timelineList(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p timelineParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	f := store.TimelineFilter{Limit: p.Limit}
	if p.HostID != "" {
		row, err := s.d.Hosts.Resolve(p.HostID)
		if err != nil {
			return nil, err
		}
		f.HostID = row.HostID
	}
	if p.Since != "" {
		t, err := time.Parse(time.RFC3339Nano, p.Since)
		if err != nil {
			return nil, apierr.Invalid("since는 RFC 3339 시각이어야 합니다(예: 2026-10-20T12:34:56.789Z). since를 고쳐 다시 보내세요")
		}
		f.Since = t
	}
	if p.Cursor != "" {
		n, err := strconv.ParseInt(p.Cursor, 10, 64)
		if err != nil || n <= 0 {
			return nil, apierr.Invalid("cursor가 올바르지 않습니다. 이전 timeline.list 결과의 next_cursor를 그대로 주세요")
		}
		f.Before = n
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > timelineMax {
		f.Limit = timelineMax
	}
	want := f.Limit
	f.Limit++
	rows, err := s.d.Store.ListTimeline(f)
	if err != nil {
		return nil, apierr.Internal("타임라인을 읽지 못했습니다. 데몬 로그를 확인하세요")
	}
	out := map[string]any{}
	if len(rows) > want {
		rows = rows[:want]
		out["next_cursor"] = strconv.FormatInt(rows[len(rows)-1].Seq, 10)
	}
	entries := make([]model.TimelineEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, model.TimelineFrom(r))
	}
	out["entries"] = entries
	return out, nil
}
