package localapi

import (
	"context"
	"encoding/json"
	"io"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/pty"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func (s *Server) registerPTY() {
	if s.d.PTYs == nil {
		return
	}
	s.methods["pty.open"] = s.ptyOpen
	s.streams["pty"] = s.ptyStream
}

// ptyOpen starts a shell on the host as a pty job and returns the envelope
// with pty_id, which stream.attach{kind:"pty"} takes, and the job.
func (s *Server) ptyOpen(_ context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p pty.OpenParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.Check(); err != nil {
		return nil, &paramError{e: apierr.As(err)}
	}
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	p.HostID = row.HostID
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	opened, err := s.d.PTYs.Open(cctx, h, p, sess.Caller())
	if err != nil {
		return nil, err
	}
	o := outcome{status: opened.Status, action: opened.Action, err: opened.Err}
	fields := map[string]any{"pty_id": nil, "job": opened.Job}
	if opened.Action.Executed != nil && *opened.Action.Executed {
		fields["pty_id"] = opened.PTYID
	}
	_, _ = s.d.Bus.Record(sess.Caller().Entry(row.HostID, model.TimelineExec, "터미널 열기: "+opened.Job.Summary, timelineOutcome(o.status), opened.Job.JobID))
	info := conn.CallInfo{RID: opened.RID, Generation: opened.Generation, Sent: true}
	return s.envelope(row.HostID, opened.Job.JobID, info, o, nil).Map(fields), nil
}

// ptyStream attaches a stream to the pty that pty_id or job_id names; the
// result carries the job id, the shell's pid and the host.
func (s *Server) ptyStream(ctx context.Context, _ *session, raw json.RawMessage) (any, func(io.ReadWriteCloser), error) {
	var ref pty.Ref
	if err := decode(raw, &ref); err != nil {
		return nil, nil, err
	}
	relay, err := s.d.PTYs.Attach(ctx, ref)
	if err != nil {
		if apierr.CodeOf(err) == wire.CodeInvalidArgument {
			return nil, nil, &paramError{e: apierr.As(err)}
		}
		return nil, nil, err
	}
	return map[string]any{"job_id": relay.Job(), "pid": relay.PID(), "host_id": relay.HostID()}, relay.Run, nil
}
