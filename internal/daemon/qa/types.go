package qa

import (
	"context"
	"regexp"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// KindQA is the kind of the job that follows a run.
const KindQA = jobs.KindQA

// Phases of a step.
const (
	PhaseSetup    = "setup"
	PhaseSteps    = "steps"
	PhaseTeardown = "teardown"
)

// Run statuses.
const (
	RunRunning   = "running"
	RunPassed    = "passed"
	RunFailed    = "failed"
	RunBlocked   = "blocked"
	RunCancelled = "cancelled"
	RunError     = "error"
)

// Step statuses.
const (
	StepPending = "pending"
	StepRunning = "running"
	StepPass    = "pass"
	StepFail    = "fail"
	StepBlocked = "blocked"
	StepNotRun  = "not_run"
)

// DefaultCollectBytes is how much of the end of a file collect keeps.
const DefaultCollectBytes = 8 << 20

// Capability is the state of one host capability.
type Capability struct {
	State  string
	Reason string
}

// Agent is the connection to a host's agent.
type Agent interface {
	Call(ctx context.Context, op string, params proto.Message, opts conn.CallOpts) (proto.Message, error)
	// Caps returns the host's capabilities, nil while they are not known.
	Caps() map[string]Capability
}

// Host is a paired PC.
type Host struct {
	HostID       string
	Name         string
	OS           string
	Arch         string
	Address      string
	AgentVersion string
}

// Hosts finds the hosts and their agents.
type Hosts interface {
	Resolve(ref string) (Host, error)
	Agent(hostID string) Agent
}

// Control tells whether automated UI input may go to a host.
type Control interface {
	// Check returns the writer generation the step's UI ops carry when it
	// may, and an error coded CONTROL_HELD_BY_HUMAN when a person holds
	// control or CONTROL_PREEMPTED when another AI client does.
	Check(ctx context.Context, hostID string, c model.Caller) (uint64, error)
	// Recheck returns nil while a UI op of the step, carrying gen, may still
	// go out, and the error that refuses it otherwise; it runs right before
	// the op is written (conn.CallOpts.Ready).
	Recheck(hostID string, c model.Caller, gen uint64) error
}

// Input sends mouse and keyboard input to a host; x and y are pixels of
// the virtual desktop.
type Input interface {
	Click(ctx context.Context, hostID string, x, y int, button string, count int) (map[string]any, error)
	Type(ctx context.Context, hostID, text, method string) (map[string]any, error)
	Key(ctx context.Context, hostID, keys string) (map[string]any, error)
}

// Pusher copies a file or folder from this Mac to a host and returns the
// transfer once it has ended.
type Pusher interface {
	Push(ctx context.Context, hostID, local, remote, onConflict string, c model.Caller) (model.TransferDetail, error)
}

// ErrorInfo is the code and sentence of a failure.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Assertion is one checked expectation of a step.
type Assertion struct {
	Kind     string `json:"kind"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
	Passed   bool   `json:"passed"`
}

// StepResult is a step of a run and what became of it (QAStep).
type StepResult struct {
	Index          int              `json:"index"`
	Phase          string           `json:"phase"`
	Name           string           `json:"name"`
	Action         string           `json:"action"`
	Line           int              `json:"line"`
	Params         map[string]any   `json:"params"`
	Optional       bool             `json:"optional"`
	ContinueOnFail bool             `json:"continue_on_fail"`
	Status         string           `json:"status"`
	StartedAt      string           `json:"started_at,omitempty"`
	DurationMS     *int64           `json:"duration_ms,omitempty"`
	Message        string           `json:"message,omitempty"`
	Error          *ErrorInfo       `json:"error,omitempty"`
	Assertions     []Assertion      `json:"assertions"`
	Result         map[string]any   `json:"result,omitempty"`
	Evidence       []model.Artifact `json:"evidence"`
}

// Unsupported is a step kind the host or the daemon could not do.
type Unsupported struct {
	Action string `json:"action"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// ReportPaths are the files of a result bundle.
type ReportPaths struct {
	JSON     string `json:"json"`
	MD       string `json:"md"`
	HTML     string `json:"html"`
	Manifest string `json:"manifest"`
}

// Run is a run of a scenario (QARun).
type Run struct {
	RunID           string        `json:"run_id"`
	JobID           string        `json:"job_id"`
	HostID          string        `json:"host_id"`
	Name            string        `json:"name"`
	ScenarioPath    string        `json:"scenario_path,omitempty"`
	Status          string        `json:"status"`
	Rev             int64         `json:"rev"`
	StartedAt       string        `json:"started_at"`
	UpdatedAt       string        `json:"updated_at"`
	FinishedAt      string        `json:"finished_at,omitempty"`
	StepsTotal      int           `json:"steps_total"`
	StepsPassed     int           `json:"steps_passed"`
	StepsFailed     int           `json:"steps_failed"`
	StepsBlocked    int           `json:"steps_blocked"`
	StepsNotRun     int           `json:"steps_not_run"`
	CurrentStep     *int          `json:"current_step,omitempty"`
	CancelRequested bool          `json:"cancel_requested"`
	Error           *ErrorInfo    `json:"error,omitempty"`
	Unsupported     []Unsupported `json:"unsupported"`
	ReportDir       string        `json:"report_dir,omitempty"`
	Report          *ReportPaths  `json:"report,omitempty"`
	Steps           []StepResult  `json:"steps,omitempty"`
}

// Selector names UI elements.
type Selector struct {
	Window       string
	Role         string
	Name         string
	NameContains string
	AutomationID string
	Index        *int
}

// PushSpec is a push step.
type PushSpec struct {
	From, To, OnConflict string
	Mkdir                bool
}

// LaunchSpec is a launch step.
type LaunchSpec struct {
	Path         string
	Args         []string
	Cwd          string
	WaitWindowMS *int64
}

// WaitWindowSpec is a wait_window step.
type WaitWindowSpec struct {
	Title, TitleContains, Process string
	PID                           int
	Gone                          bool
}

// ClickSpec is a click step: Element, or X and Y from the window's corner.
type ClickSpec struct {
	Element *Selector
	X, Y    *int
	Window  string
	Button  string
	Count   int
}

// TypeSpec is a type step.
type TypeSpec struct{ Text, Method string }

// KeySpec is a key step.
type KeySpec struct{ Keys string }

// UIActSpec is a ui_act step.
type UIActSpec struct {
	Selector Selector
	Action   string
	Value    string
}

// AssertUISpec is an assert_ui step.
type AssertUISpec struct {
	Selector     Selector
	Exists       *bool
	Text         *string
	TextContains *string
	TextMatches  *regexp.Regexp
	Enabled      *bool
	Count        *int
}

// AssertScreenTextSpec is an assert_screen_text step.
type AssertScreenTextSpec struct {
	Window      string
	Contains    []string
	NotContains []string
	Matches     *regexp.Regexp
}

// AssertFileSpec is an assert_file step.
type AssertFileSpec struct {
	Path        string
	Exists      *bool
	Contains    []string
	NotContains []string
	Matches     *regexp.Regexp
	SHA256      string
	Size        *int64
}

// ExecSpec is an exec step.
type ExecSpec struct {
	Argv           []string
	Cmd, Shell     string
	Cwd            string
	ExpectCode     int
	StdoutContains []string
	StderrContains []string
}

// ScreenshotSpec is a screenshot step.
type ScreenshotSpec struct {
	Name     string
	Display  int
	Window   string
	MaxWidth int
}

// EventsQuery is the diag.events query of a collect step.
type EventsQuery struct {
	Log, Since, Until, Provider string
	Levels                      []string
	Max                         int
}

// CrashesQuery is the diag.crashes query of a collect step.
type CrashesQuery struct{ Since string }

// CollectSpec is a collect step: File, Events or Crashes.
type CollectSpec struct {
	File     string
	MaxBytes int64
	Events   *EventsQuery
	Crashes  *CrashesQuery
	Name     string
}

// SleepSpec is a sleep step.
type SleepSpec struct{ MS int64 }

// CloseSpec is a close step.
type CloseSpec struct{ Window string }
