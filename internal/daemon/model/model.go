// Package model holds the JSON shapes of the local API (docs/local-api.md
// §2, §5): snake_case keys, times in RFC 3339 UTC with milliseconds.
package model

import (
	"encoding/json"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

// SchemaVersion is the schema_version of result envelopes.
const SchemaVersion = 1

// Owners of a host's input (ControlState.Owner).
const (
	OwnerNone  = "none"
	OwnerHuman = "human"
	OwnerAI    = "ai"
)

// Envelope statuses.
const (
	StatusSucceeded      = "succeeded"
	StatusFailed         = "failed"
	StatusRunning        = "running"
	StatusWaitingForUser = "waiting_for_user"
	StatusUnknown        = "unknown"
	StatusCancelled      = "cancelled"
)

// Action outcomes. OutcomePartial is an input of which only some events
// were injected.
const (
	OutcomeDone       = "done"
	OutcomeNotStarted = "not_started"
	OutcomeUnknown    = "unknown"
	OutcomePartial    = "partial"
)

// Time formats t for the API; the zero time is "".
func Time(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return store.FormatTime(t)
}

// Capability is the state of one host capability; Shells lists the shells
// of the pty capability.
type Capability struct {
	State  string   `json:"state"`
	Reason string   `json:"reason,omitempty"`
	Shells []string `json:"shells,omitempty"`
}

// SessionInfo is the desktop session of a host. Locked is nil when the
// agent did not report the lock state.
type SessionInfo struct {
	SessionID    int    `json:"session_id"`
	User         string `json:"user"`
	SID          string `json:"sid,omitempty"`
	ConnectState string `json:"connect_state"`
	Protocol     string `json:"protocol"`
	Locked       *bool  `json:"locked,omitempty"`
	InputDesktop string `json:"input_desktop"`
	Integrity    string `json:"integrity,omitempty"`
}

// Host is a paired PC; Reason says why it is not ready.
type Host struct {
	HostID               string                `json:"host_id"`
	Name                 string                `json:"name"`
	OS                   string                `json:"os"`
	Arch                 string                `json:"arch"`
	Address              string                `json:"address"`
	Port                 int                   `json:"port"`
	SSHAlias             string                `json:"ssh_alias"`
	State                string                `json:"state"`
	Caps                 map[string]Capability `json:"caps"`
	Session              *SessionInfo          `json:"session,omitempty"`
	Displays             []any                 `json:"displays"`
	Owner                string                `json:"owner"`
	OwnerClient          string                `json:"owner_client,omitempty"`
	AIScope              AIScope               `json:"ai_scope"`
	AgentVersion         string                `json:"agent_version,omitempty"`
	ConnectionGeneration uint64                `json:"connection_generation"`
	LastSeen             string                `json:"last_seen,omitempty"`
	Reason               string                `json:"reason,omitempty"`
}

// ControlState is the control ownership of a host: OwnerClient and
// OwnerClientID are the hello name and the client id of the client that
// holds it, Gen the writer generation.
type ControlState struct {
	Owner         string `json:"owner"`
	OwnerClient   string `json:"owner_client,omitempty"`
	OwnerClientID string `json:"owner_client_id,omitempty"`
	Gen           uint64 `json:"gen"`
	AIPaused      bool   `json:"ai_paused"`
}

// SessionInspect is the flat result of session.inspect.
type SessionInspect struct {
	SessionInfo
	ControlState
	HostID               string                `json:"host_id"`
	Displays             []any                 `json:"displays"`
	Caps                 map[string]Capability `json:"caps"`
	ConnectionGeneration uint64                `json:"connection_generation"`
}

// Artifact is a file an operation produced.
type Artifact struct {
	ArtifactID string `json:"artifact_id"`
	JobID      string `json:"job_id,omitempty"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	MIME       string `json:"mime"`
	SHA256     string `json:"sha256"`
	CreatedAt  string `json:"created_at"`
}

// Job is a background or recorded operation. Its result never carries the
// command output; OutputBytes counts the bytes of the recorded stdout and
// stderr, which jobs.output returns.
type Job struct {
	JobID       string          `json:"job_id"`
	HostID      string          `json:"host_id"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	RequestID   string          `json:"request_id,omitempty"`
	Summary     string          `json:"summary"`
	Progress    *float64        `json:"progress,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	FinishedAt  string          `json:"finished_at,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	OutputBytes *int64          `json:"output_bytes,omitempty"`
}

// outputKeys are the result members that hold command output.
var outputKeys = []string{"stdout", "stderr"}

// JobFrom returns the API form of a stored job: its result without
// outputKeys, whose byte count becomes OutputBytes.
func JobFrom(j store.Job) Job {
	out := Job{JobID: j.JobID, HostID: j.HostID, Kind: j.Kind, Status: j.Status, RequestID: j.RequestID, Summary: j.Summary,
		CreatedAt: Time(j.CreatedAt), UpdatedAt: Time(j.UpdatedAt), Result: j.Result}
	if j.FinishedAt != nil {
		out.FinishedAt = Time(*j.FinishedAt)
	}
	var res map[string]json.RawMessage
	if len(j.Result) == 0 || json.Unmarshal(j.Result, &res) != nil {
		return out
	}
	var n int64
	found := false
	for _, k := range outputKeys {
		raw, ok := res[k]
		if !ok {
			continue
		}
		found = true
		var s string
		if json.Unmarshal(raw, &s) == nil {
			n += int64(len(s))
		}
		delete(res, k)
	}
	if !found {
		return out
	}
	if b, err := json.Marshal(res); err == nil {
		out.Result = b
	}
	out.OutputBytes = &n
	return out
}

// FileEntry is a remote file or folder.
type FileEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	MTime    string `json:"mtime"`
	Link     bool   `json:"link"`
	Hidden   *bool  `json:"hidden,omitempty"`
	Readonly *bool  `json:"readonly,omitempty"`
}

// TransferFile is one file of a transfer; Path is where it was put.
type TransferFile struct {
	Rel       string        `json:"rel"`
	Size      int64         `json:"size"`
	BytesDone int64         `json:"bytes_done"`
	State     string        `json:"state"`
	Outcome   string        `json:"outcome,omitempty"`
	SHA256    string        `json:"sha256,omitempty"`
	Path      string        `json:"path,omitempty"`
	Error     *apierr.Error `json:"error,omitempty"`
}

// Transfer is a file or folder transfer between this Mac and a host, with
// the counts and bytes of its files; TransferDetail adds the files. Rev
// grows by one with every change of the transfer.
type Transfer struct {
	TransferID  string        `json:"transfer_id"`
	HostID      string        `json:"host_id"`
	Direction   string        `json:"direction"`
	Local       string        `json:"local"`
	Remote      string        `json:"remote"`
	OnConflict  string        `json:"on_conflict"`
	State       string        `json:"state"`
	BytesDone   int64         `json:"bytes_done"`
	BytesTotal  int64         `json:"bytes_total"`
	FilesDone   int           `json:"files_done"`
	FilesFailed int           `json:"files_failed"`
	FilesTotal  int           `json:"files_total"`
	RateBPS     int64         `json:"rate_bps"`
	Outcome     string        `json:"outcome,omitempty"`
	Error       *apierr.Error `json:"error,omitempty"`
	Rev         int64         `json:"rev"`
	UpdatedAt   string        `json:"updated_at"`
}

// TransferDetail is a Transfer with its files.
type TransferDetail struct {
	Transfer
	Files []TransferFile `json:"files"`
}

// ProcessInfo is a remote process.
type ProcessInfo struct {
	PID               int      `json:"pid"`
	PPID              int      `json:"ppid"`
	Name              string   `json:"name"`
	Path              string   `json:"path,omitempty"`
	Cmd               string   `json:"cmd,omitempty"`
	MemKB             int      `json:"mem_kb"`
	CPUPct            *float64 `json:"cpu_pct,omitempty"`
	User              string   `json:"user,omitempty"`
	Started           string   `json:"started,omitempty"`
	StartedByDeskhand bool     `json:"started_by_deskhand"`
	Self              bool     `json:"self"`
}

// Timeline actors, kinds and outcomes.
const (
	ActorHuman  = "human"
	ActorAI     = "ai"
	ActorSystem = "system"

	TimelineExec       = "exec"
	TimelineFile       = "file"
	TimelineConnection = "connection"
	TimelineInstall    = "install"
	TimelineQA         = "qa"
	TimelineControl    = "control"
	TimelineInput      = "input"
	TimelineObserve    = "observe"

	TimelineOK      = "ok"
	TimelineFailed  = "failed"
	TimelineUnknown = "unknown"
	TimelineDenied  = "denied"
)

// TimelineEntry is one recorded action.
type TimelineEntry struct {
	Seq        int64      `json:"seq"`
	TS         string     `json:"ts"`
	HostID     string     `json:"host_id"`
	Actor      string     `json:"actor"`
	ClientID   string     `json:"client_id,omitempty"`
	ClientName string     `json:"client_name,omitempty"`
	Kind       string     `json:"kind"`
	Summary    string     `json:"summary"`
	Outcome    string     `json:"outcome"`
	JobID      string     `json:"job_id,omitempty"`
	Artifacts  []Artifact `json:"artifacts"`
}

// TimelineFrom returns the API form of a stored timeline row.
func TimelineFrom(e store.Timeline) TimelineEntry {
	out := TimelineEntry{Seq: e.Seq, TS: Time(e.TS), HostID: e.HostID, Actor: e.Actor, ClientID: e.ClientID, ClientName: e.ClientName,
		Kind: e.Kind, Summary: e.Summary, Outcome: e.Outcome, JobID: e.JobID, Artifacts: []Artifact{}}
	if len(e.Artifacts) > 0 {
		_ = json.Unmarshal(e.Artifacts, &out.Artifacts)
		if out.Artifacts == nil {
			out.Artifacts = []Artifact{}
		}
	}
	return out
}

// Check is one host.doctor item.
type Check struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// Check statuses.
const (
	CheckPass = "pass"
	CheckFail = "fail"
	CheckWarn = "warn"
	CheckSkip = "skip"
)

// Action says whether an operation ran; Executed is null when that is
// unknown.
type Action struct {
	Executed *bool  `json:"executed"`
	Outcome  string `json:"outcome"`
}

// Executed returns the action of an operation that ran.
func Executed() Action { t := true; return Action{Executed: &t, Outcome: OutcomeDone} }

// NotStarted returns the action of an operation that did not run.
func NotStarted() Action { f := false; return Action{Executed: &f, Outcome: OutcomeNotStarted} }

// UnknownAction returns the action of an operation whose outcome is unknown.
func UnknownAction() Action { return Action{Outcome: OutcomeUnknown} }

// PartialAction returns the action of an input of which only some events
// ran.
func PartialAction() Action { f := false; return Action{Executed: &f, Outcome: OutcomePartial} }

// Envelope is the common result of the methods that change state or run
// something remotely (local-api.md §2).
type Envelope struct {
	RequestID            string
	JobID                string
	HostID               string
	SessionID            string
	ConnectionGeneration uint64
	Status               string
	Action               Action
	Error                *apierr.Error
	NextAction           string
	Artifacts            []Artifact
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Map returns the envelope fields followed by fields, in one object.
func (e Envelope) Map(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+11)
	for k, v := range fields {
		out[k] = v
	}
	arts := e.Artifacts
	if arts == nil {
		arts = []Artifact{}
	}
	next := e.NextAction
	if next == "" && e.Error != nil {
		next = e.Error.NextAction
	}
	var errv any
	if e.Error != nil {
		errv = e.Error
	}
	out["schema_version"] = SchemaVersion
	out["request_id"] = nullable(e.RequestID)
	out["job_id"] = nullable(e.JobID)
	out["host_id"] = e.HostID
	out["session_id"] = nullable(e.SessionID)
	out["connection_generation"] = e.ConnectionGeneration
	out["status"] = e.Status
	out["action"] = e.Action
	out["error"] = errv
	out["next_action"] = nullable(next)
	out["artifacts"] = arts
	return out
}

// Caller is who asked for an operation: a local API client, or the daemon.
type Caller struct {
	Actor      string
	ClientID   string
	ClientName string
}

// System is the daemon as a caller.
var System = Caller{Actor: ActorSystem}

// ActorOf returns the actor of a local API client kind: app and cli are
// people, mcp is an AI.
func ActorOf(clientKind string) string {
	if clientKind == "mcp" {
		return ActorAI
	}
	return ActorHuman
}

// Entry returns a timeline row of c for hostID.
func (c Caller) Entry(hostID, kind, summary, outcome, jobID string) store.Timeline {
	return store.Timeline{TS: time.Now(), HostID: hostID, Actor: c.Actor, ClientID: c.ClientID, ClientName: c.ClientName,
		Kind: kind, Summary: summary, Outcome: outcome, JobID: jobID}
}
