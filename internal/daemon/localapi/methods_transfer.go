package localapi

import (
	"context"
	"encoding/json"

	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/xfer"
)

func (s *Server) registerTransfer() {
	if s.d.Transfers == nil {
		return
	}
	s.methods["transfer.start"] = s.transferStart
	s.methods["transfer.list"] = s.transferList
	s.methods["transfer.get"] = s.transferGet
	s.methods["transfer.pause"] = s.transferPause
	s.methods["transfer.resume"] = s.transferResume
	s.methods["transfer.cancel"] = s.transferCancel
}

type transferParams struct {
	TransferID string `json:"transfer_id"`
	HostID     string `json:"host_id"`
	Direction  string `json:"direction"`
	Local      string `json:"local"`
	Remote     string `json:"remote"`
	OnConflict string `json:"on_conflict"`
}

// transferStart starts a transfer and returns its envelope with
// transfer_id and the job that mirrors it: running when it started, failed
// and not started when the source, the destination folder or the host does
// not let it start.
func (s *Server) transferStart(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p transferParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := hostRequired(p.HostID); err != nil {
		return nil, err
	}
	_, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	res, err := s.d.Transfers.Start(cctx, xfer.StartParams{HostID: row.HostID, Direction: p.Direction, Local: p.Local, Remote: p.Remote,
		OnConflict: p.OnConflict}, sess.Caller())
	if err != nil {
		return nil, err
	}
	if res.Err != nil {
		o := outcome{status: model.StatusFailed, action: model.NotStarted(), err: res.Err}
		return s.envelope(row.HostID, "", res.Info, o, nil).Map(map[string]any{"transfer_id": nil}), nil
	}
	o := outcome{status: model.StatusRunning, action: model.Executed()}
	return s.envelope(row.HostID, res.JobID, res.Info, o, nil).Map(map[string]any{"transfer_id": res.Transfer.TransferID}), nil
}

func (s *Server) transferList(_ context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p transferParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	hostID := ""
	if p.HostID != "" {
		row, err := s.d.Hosts.Resolve(p.HostID)
		if err != nil {
			return nil, err
		}
		hostID = row.HostID
	}
	list, err := s.d.Transfers.List(hostID)
	if err != nil {
		return nil, err
	}
	if sess.Caller().Actor == model.ActorAI {
		allowed := list[:0:0]
		for _, t := range list {
			if s.d.Hosts.AIScope(t.HostID).Files == model.FilesAll {
				allowed = append(allowed, t)
			}
		}
		list = allowed
	}
	return map[string]any{"transfers": list}, nil
}

// transferID returns the transfer_id of the params of method.
func transferID(raw json.RawMessage, method string) (string, error) {
	var p transferParams
	if err := decode(raw, &p); err != nil {
		return "", err
	}
	if p.TransferID == "" {
		return "", required("transfer_id", method)
	}
	return p.TransferID, nil
}

func (s *Server) transferGet(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	id, err := transferID(raw, "transfer.get")
	if err != nil {
		return nil, err
	}
	return s.d.Transfers.Get(id)
}

func (s *Server) transferPause(_ context.Context, sess *session, raw json.RawMessage) (any, error) {
	id, err := transferID(raw, "transfer.pause")
	if err != nil {
		return nil, err
	}
	return s.d.Transfers.Pause(id, sess.Caller())
}

func (s *Server) transferResume(_ context.Context, sess *session, raw json.RawMessage) (any, error) {
	id, err := transferID(raw, "transfer.resume")
	if err != nil {
		return nil, err
	}
	return s.d.Transfers.Resume(id, sess.Caller())
}

func (s *Server) transferCancel(_ context.Context, sess *session, raw json.RawMessage) (any, error) {
	id, err := transferID(raw, "transfer.cancel")
	if err != nil {
		return nil, err
	}
	return s.d.Transfers.Cancel(id, sess.Caller())
}
