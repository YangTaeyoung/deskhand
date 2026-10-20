// Package diag reads the diagnostics of the PC for the diag ops of
// docs/protocol.md §3.1: the system summary, the services, the event logs
// and the crash dumps. The readers use Windows APIs (the _windows.go files);
// outside Windows every reader returns ErrUnsupported.
package diag

import (
	"context"
	"errors"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// ErrUnsupported is the error of a reader on a system it cannot read.
var ErrUnsupported = errors.New("diagnostics are read from Windows only; this agent runs on another system")

// InvalidError is the error of a request the reader refuses as it is.
type InvalidError struct{ Text string }

func (e *InvalidError) Error() string { return e.Text }

// Source reads the diagnostics of a PC.
type Source interface {
	System(ctx context.Context) (System, error)
	// Services returns the services whose name or display name contains
	// filter without regard to case, by name.
	Services(ctx context.Context, filter string) ([]Service, error)
	// Events returns the records of q newest first.
	Events(ctx context.Context, q wire.DiagEventsQuery) (Events, error)
	// Crashes returns the crash dumps and reports from since on, newest
	// first.
	Crashes(ctx context.Context, since time.Time) ([]Dump, error)
}

// System is the result of diag.system.
type System struct {
	OS           OS     `json:"os"`
	ComputerName string `json:"computer_name"`
	CPU          CPU    `json:"cpu"`
	Memory       Memory `json:"memory"`
	Disks        []Disk `json:"disks"`
	GPUs         []GPU  `json:"gpus"`
	BootTime     string `json:"boot_time"`
	UptimeS      int64  `json:"uptime_s"`
}

// OS names the Windows installation.
type OS struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Build   string `json:"build"`
	Edition string `json:"edition"`
}

// CPU is the processor; UsagePct is the busy share of all logical
// processors over two samples SampleGap apart, 0–100.
type CPU struct {
	Name     string  `json:"name"`
	Cores    int     `json:"cores"`
	Threads  int     `json:"threads"`
	UsagePct float64 `json:"usage_pct"`
}

// Memory is the physical memory.
type Memory struct {
	TotalBytes uint64 `json:"total_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
}

// Disk is a volume with a drive letter.
type Disk struct {
	Root       string `json:"root"`
	Label      string `json:"label,omitempty"`
	FS         string `json:"fs"`
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
}

// GPU is a display adapter.
type GPU struct {
	Name          string `json:"name"`
	DriverVersion string `json:"driver_version,omitempty"`
	MemoryBytes   uint64 `json:"memory_bytes,omitempty"`
}

// Service is a Windows service.
type Service struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	State       string `json:"state"`
	StartType   string `json:"start_type"`
	PID         int    `json:"pid,omitempty"`
}

// Events is the result of diag.events.
type Events struct {
	Events    []Event `json:"events"`
	Truncated bool    `json:"truncated"`
}

// Event is an event log record.
type Event struct {
	RecordID uint64 `json:"record_id"`
	Time     string `json:"time"`
	Level    string `json:"level"`
	Provider string `json:"provider"`
	EventID  int    `json:"event_id"`
	Message  string `json:"message"`
}

// Dump is a crash dump or an error report file.
type Dump struct {
	Path      string `json:"path"`
	App       string `json:"app"`
	Time      string `json:"time"`
	SizeBytes int64  `json:"size_bytes"`
	Kind      string `json:"kind"`
}

// Dump kinds.
const (
	KindMinidump = "minidump"
	KindFull     = "full"
	KindReport   = "report"
)

// SampleGap is the time between the two CPU samples of diag.system and
// proc.list.
const SampleGap = 500 * time.Millisecond

// sleep waits d or until ctx ends, and returns ctx's error then.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
