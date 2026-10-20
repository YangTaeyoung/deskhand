package mcpserver

import (
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const saveDoc = "Set true to also keep the result as a JSON artifact of a new diag job (result artifact_id and artifacts[0].path)."

type diagSystemInput struct {
	Host string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Save bool   `json:"save,omitempty" jsonschema:"Set true to also keep the result as a JSON artifact of a new diag job (result artifact_id and artifacts[0].path)."`
}

type diagServicesInput struct {
	Host string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Name string `json:"name,omitempty" jsonschema:"Only services whose name or display name contains this text, without regard to case, for example update."`
	Save bool   `json:"save,omitempty" jsonschema:"Set true to also keep the result as a JSON artifact of a new diag job (result artifact_id and artifacts[0].path)."`
}

type diagEventsInput struct {
	Host     string   `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Log      string   `json:"log,omitempty" jsonschema:"Event log to read: System (default) or Application."`
	Since    string   `json:"since,omitempty" jsonschema:"Oldest record time, RFC 3339 such as 2026-10-20T00:00:00Z; default 24 hours ago."`
	Until    string   `json:"until,omitempty" jsonschema:"Newest record time, RFC 3339; default now."`
	Levels   []string `json:"levels,omitempty" jsonschema:"Only these levels; default every level."`
	Provider string   `json:"provider,omitempty" jsonschema:"Only records of this provider, matched exactly, for example Service Control Manager or Application Error."`
	Max      int      `json:"max,omitempty" jsonschema:"Most records to return, 1 to 1000; default 200."`
	Save     bool     `json:"save,omitempty" jsonschema:"Set true to also keep the result as a JSON artifact of a new diag job (result artifact_id and artifacts[0].path)."`
}

type diagCrashesInput struct {
	Host  string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Since string `json:"since,omitempty" jsonschema:"Oldest crash time, RFC 3339 such as 2026-10-01T00:00:00Z; default 30 days ago."`
	Save  bool   `json:"save,omitempty" jsonschema:"Set true to also keep the result as a JSON artifact of a new diag job (result artifact_id and artifacts[0].path)."`
}

func (s *Server) registerDiag(srv *mcp.Server) {
	add[diagSystemInput](s, srv, "diag_system",
		"Read the PC's system summary: Windows name, version, build and edition, computer_name, cpu (name, cores, threads, usage_pct = busy share of all logical processors over 0.5 s), memory (total_bytes, used_bytes), disks (fixed, removable and RAM drives: root, label, fs, total_bytes, free_bytes), gpus (name, driver_version, memory_bytes of dedicated video memory), boot_time and uptime_s. Read-only. "+saveDoc,
		spec{Method: "diag.system", Envelope: true}, nil)
	add[diagServicesInput](s, srv, "diag_services",
		"List the PC's Windows services sorted by name: name, display_name, state (running, stopped, paused, start_pending, stop_pending, unknown), start_type (auto, delayed_auto, manual, disabled, unknown) and pid while it runs. Read-only: it does not start or stop services; use exec_run with sc.exe for that. "+saveDoc,
		spec{Method: "diag.services", Envelope: true}, nil)
	add[diagEventsInput](s, srv, "diag_events",
		"Read Windows event log records, newest first: record_id, time, level (critical, error, warning, information, verbose), provider, event_id and message. Filters: log (System or Application), since and until, levels and provider. At most max records come back; truncated true means more records matched, so narrow since/until, levels or provider, or raise max up to 1000. "+saveDoc,
		spec{Method: "diag.events", Envelope: true}, func(sc *jsonschema.Schema) {
			sc.Properties["log"].Enum = []any{}
			for _, l := range wire.DiagEventLogs {
				sc.Properties["log"].Enum = append(sc.Properties["log"].Enum, l)
			}
			items := sc.Properties["levels"].Items
			for _, l := range wire.DiagEventLevels {
				items.Enum = append(items.Enum, l)
			}
			one, max := 1.0, float64(wire.DiagEventsMaxLimit)
			sc.Properties["max"].Minimum = &one
			sc.Properties["max"].Maximum = &max
		})
	add[diagCrashesInput](s, srv, "diag_crashes",
		"List crash dumps and Windows Error Reporting crash and hang reports on the PC from since on (default the last 30 days), newest first: path, app (the program that crashed, Windows for kernel dumps), time, size_bytes and kind (minidump, full or report = Report.wer without a dump). Sources: LocalDumps folders (CrashDumps of every profile and DumpFolder settings), the WER report archive and queue of the machine and of every profile, and the kernel's minidump folder and memory dump. Copy a file to this Mac with files_pull using its path. "+saveDoc,
		spec{Method: "diag.crashes", Envelope: true}, nil)
}

// diagText returns the summary line of a diag tool's result.
func diagText(tool string, m map[string]any) string {
	switch tool {
	case "diag_system":
		o, _ := m["os"].(map[string]any)
		cpu, _ := m["cpu"].(map[string]any)
		mem, _ := m["memory"].(map[string]any)
		used, _ := num(mem, "used_bytes")
		total, _ := num(mem, "total_bytes")
		up, _ := num(m, "uptime_s")
		return fmt.Sprintf("%v: %v %v build %v; CPU %v%% busy; memory %d of %d MiB used; up %d h", m["computer_name"], o["name"], o["version"], o["build"],
			cpu["usage_pct"], used>>20, total>>20, up/3600)
	case "diag_services":
		list, _ := m["services"].([]any)
		return fmt.Sprintf("%d services", len(list))
	case "diag_events":
		list, _ := m["events"].([]any)
		s := fmt.Sprintf("%d events, newest first", len(list))
		if t, _ := m["truncated"].(bool); t {
			s += "; truncated: more records matched, narrow since/until, levels or provider, or raise max (up to 1000)"
		}
		return s
	case "diag_crashes":
		list, _ := m["dumps"].([]any)
		var apps []string
		for _, d := range list {
			dm, _ := d.(map[string]any)
			apps = append(apps, fmt.Sprintf("%v (%v)", dm["app"], dm["kind"]))
		}
		if len(apps) > 10 {
			apps = append(apps[:10], "…")
		}
		s := fmt.Sprintf("%d crash dumps and reports", len(list))
		if len(apps) > 0 {
			s += ": " + strings.Join(apps, ", ") + ". Copy one to this Mac with files_pull and its path."
		}
		return s
	}
	return ""
}
