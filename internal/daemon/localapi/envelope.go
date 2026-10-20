package localapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// outcome is what happened to a remote operation.
type outcome struct {
	status string
	action model.Action
	err    *apierr.Error
}

// outcomeOf classifies the result of a Call: done, refused or never sent
// (not started), or unknown when the request went out and no answer came.
func outcomeOf(info conn.CallInfo, err error) outcome {
	if err == nil {
		return outcome{status: model.StatusSucceeded, action: model.Executed()}
	}
	e := apierr.As(err)
	if e.Code == wire.CodeExecutionOutcomeUnknown || info.Sent && !info.Answered {
		return outcome{status: model.StatusUnknown, action: model.UnknownAction(), err: e}
	}
	return outcome{status: model.StatusFailed, action: model.NotStarted(), err: e}
}

// requestProblem returns err as a params error when the daemon refused the
// request before sending it (it cannot be encoded or is too long), and nil
// for any other error.
func requestProblem(err error) error {
	if conn.IsRequestError(err) {
		return &paramError{e: apierr.As(err)}
	}
	return nil
}

// timelineOutcome maps an envelope status to a timeline outcome.
func timelineOutcome(status string) string {
	switch status {
	case model.StatusFailed:
		return model.TimelineFailed
	case model.StatusUnknown:
		return model.TimelineUnknown
	}
	return model.TimelineOK
}

// envelope returns the envelope of an operation on hostID.
func (s *Server) envelope(hostID, jobID string, info conn.CallInfo, o outcome, arts []model.Artifact) model.Envelope {
	gen := info.Generation
	if !info.Sent {
		if h := s.d.Hosts; h != nil {
			if m, err := h.Get(hostID); err == nil {
				gen = m.ConnectionGeneration
			}
		}
	}
	return model.Envelope{RequestID: info.RID, JobID: jobID, HostID: hostID, SessionID: s.d.Hosts.SessionID(hostID),
		ConnectionGeneration: gen, Status: o.status, Action: o.action, Error: o.err, Artifacts: arts}
}

// detached returns a context that outlives the client's connection, so
// that a started operation is recorded to its end, bounded by d and by the
// server's Close.
func (s *Server) detached(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.base, d)
}

// artifactThreshold is the output size from which exec.run also saves a
// stream as an artifact.
const artifactThreshold = 64 << 10

// saveArtifact writes data as the artifact name of a job.
func saveArtifact(jobID, name, kind, mime string, data []byte) (model.Artifact, error) {
	dir := filepath.Join(paths.Artifacts(), jobID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return model.Artifact{}, err
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return model.Artifact{}, err
	}
	sum := sha256.Sum256(data)
	return model.Artifact{ArtifactID: ids.New(ids.Artifact), JobID: jobID, Kind: kind, Name: name, Path: p, Size: int64(len(data)),
		MIME: mime, SHA256: hex.EncodeToString(sum[:]), CreatedAt: model.Time(time.Now())}, nil
}

// remoteTimeout is the bound of a remote operation other than exec.run.
const remoteTimeout = 2 * time.Minute

func hostRequired(id string) error {
	if id == "" {
		return apierr.Invalid("host_id가 없습니다. host.list로 PC의 host_id나 이름을 확인해 주세요")
	}
	return nil
}

func required(field, method string) error {
	return apierr.Invalid(fmt.Sprintf("%s에는 %s가 필요합니다. 파라미터를 확인하세요", method, field))
}
