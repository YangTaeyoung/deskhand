package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// textLimit bounds command output echoed in the text summary.
const textLimit = 1500

// nextActionHints explain the daemon's next_action values to an agent.
var nextActionHints = map[string]string{
	"check_state":    "check the current state (job_get, session_inspect, files_stat, proc_list) before retrying",
	"host_repair":    "the agent on the PC needs repair; ask the user to run `deskhand host repair <name>`",
	"host_add_force": "the PC's agent is paired with another controller; ask the user to run `deskhand host add <name> --ssh <alias> --force`",
	"host_connect":   "the PC is unreachable; run host_doctor and ask the user to check it is on",
	"host_doctor":    "run host_doctor to find the cause",
	"retry":          "retry the same call shortly",
	"fix_params":     "fix the arguments and call again",
	"human_release":  "a person controls the PC; wait for them to release control and do not retry in a loop",
}

// hint explains a next_action; for job tools "check_state" must not send
// the agent back to the tool it just called.
func hint(action, tool string) string {
	if action == "check_state" && (tool == "job_get" || tool == "job_output") {
		return "check the target state directly (session_inspect, files_stat, proc_list) before retrying"
	}
	if h, ok := nextActionHints[action]; ok {
		return h
	}
	return action
}

func errorText(e map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Error %v: %v", e["code"], e["message"])
	if r, _ := e["retryable"].(bool); r {
		b.WriteString(" (retryable)")
	}
	if na, _ := e["next_action"].(string); na != "" {
		fmt.Fprintf(&b, "\nnext_action: %s - %s", na, hint(na, ""))
	}
	return b.String()
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func num(m map[string]any, k string) (int64, bool) {
	switch v := m[k].(type) {
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case float64:
		return int64(v), true
	}
	return 0, false
}

func clip(s string) string {
	if len(s) <= textLimit {
		return s
	}
	return s[:textLimit] + fmt.Sprintf("...[%d more bytes in structured output]", len(s)-textLimit)
}

// success wraps a daemon result: structured content unchanged, a short text
// summary. For envelope tools (sp.Mutating) a failed or unknown envelope is a
// tool error and gets top-level code, message and retryable copied from its
// error. Job tools report job state without IsError.
func success(tool string, sp spec, params map[string]any, res any) *mcp.CallToolResult {
	out := &mcp.CallToolResult{StructuredContent: res}
	m, ok := res.(map[string]any)
	if !ok {
		m = map[string]any{}
		if res != nil {
			b, _ := json.Marshal(res)
			out.Content = []mcp.Content{&mcp.TextContent{Text: string(b)}}
			return out
		}
		out.StructuredContent = m
	}
	var lines []string
	status := str(m, "status")
	errObj, _ := m["error"].(map[string]any)
	isUnknown := status == "unknown" || str2(m, "action", "outcome") == "unknown"
	if status != "" {
		line := "status: " + status
		if act, ok := m["action"].(map[string]any); ok {
			line += fmt.Sprintf(", action.outcome: %v", act["outcome"])
		}
		if id := str(m, "job_id"); id != "" {
			line += ", job_id: " + id
		}
		lines = append(lines, line)
	}
	if d := detail(tool, params, m); d != "" {
		lines = append(lines, d)
	}
	if errObj != nil {
		lines = append(lines, fmt.Sprintf("error %v: %v", errObj["code"], errObj["message"]))
	}
	na := str(m, "next_action")
	if na == "" && errObj != nil {
		na, _ = errObj["next_action"].(string)
	}
	if na != "" {
		lines = append(lines, fmt.Sprintf("next_action: %s - %s", na, hint(na, tool)))
	}
	if isUnknown {
		if sp.Mutating {
			ref := "job_get"
			if id := str(m, "job_id"); id != "" {
				ref = "job_get (job_id " + id + ")"
			}
			lines = append(lines, "Outcome unknown: the PC may have done it. Call "+ref+" or session_inspect and check the target state before any retry; do not resend blindly.")
		} else {
			lines = append(lines, "The job outcome is unknown. Check the target state directly (session_inspect, files_stat, proc_list) before any retry; do not resend blindly.")
		}
	}
	if sp.Mutating && ((status == "failed" && errObj != nil) || status == "unknown") {
		out.IsError = true
		if prev, ok := m["code"]; ok {
			if _, isStr := prev.(string); !isStr {
				m["exit_code"] = prev
				delete(m, "code")
			}
		}
		if errObj != nil {
			setDefault(m, "code", errObj["code"])
			setDefault(m, "message", errObj["message"])
			setDefault(m, "retryable", errObj["retryable"])
		} else {
			setDefault(m, "code", wire.CodeExecutionOutcomeUnknown)
			setDefault(m, "message", "The outcome of the action is unknown.")
			setDefault(m, "retryable", false)
		}
		if _, ok := m["next_action"]; !ok || m["next_action"] == nil {
			if status == "unknown" {
				m["next_action"] = apierr.NextCheckState
			}
		}
	}
	out.Content = []mcp.Content{&mcp.TextContent{Text: strings.Join(lines, "\n")}}
	return out
}

func setDefault(m map[string]any, k string, v any) {
	if v == nil {
		return
	}
	if _, ok := m[k]; !ok {
		m[k] = v
	}
}

func str2(m map[string]any, k1, k2 string) string {
	sub, _ := m[k1].(map[string]any)
	return str(sub, k2)
}

// detail returns the key result line of a tool.
func detail(tool string, params, m map[string]any) string {
	switch tool {
	case "hosts_list":
		hosts, _ := m["hosts"].([]any)
		var parts []string
		for _, h := range hosts {
			hm, _ := h.(map[string]any)
			parts = append(parts, fmt.Sprintf("%s (%s, %s)", str(hm, "host_id"), str(hm, "name"), str(hm, "state")))
		}
		if len(parts) == 0 {
			return "0 hosts registered; ask the user to run `deskhand host add`."
		}
		return fmt.Sprintf("%d hosts: %s", len(parts), strings.Join(parts, "; "))
	case "host_doctor":
		checks, _ := m["checks"].([]any)
		var bad []string
		for _, c := range checks {
			cm, _ := c.(map[string]any)
			if s := str(cm, "status"); s == "fail" || s == "warn" {
				bad = append(bad, fmt.Sprintf("%s %s: %s", str(cm, "id"), s, str(cm, "detail")))
			}
		}
		if len(bad) == 0 {
			return fmt.Sprintf("%d checks, all pass or skipped", len(checks))
		}
		return "problems: " + strings.Join(bad, "; ")
	case "session_inspect":
		locked := any("unknown")
		if l, ok := m["locked"]; ok && l != nil {
			locked = l
		}
		return fmt.Sprintf("session: user=%v connect_state=%v locked=%v input_desktop=%v owner=%v ai_paused=%v",
			m["user"], m["connect_state"], locked, m["input_desktop"], m["owner"], m["ai_paused"])
	case "exec_run":
		var b strings.Builder
		if c, ok := num(m, "code"); ok {
			fmt.Fprintf(&b, "exit code %d", c)
		}
		if s := str(m, "stdout"); s != "" {
			fmt.Fprintf(&b, "\nstdout:\n%s", clip(s))
		}
		if s := str(m, "stderr"); s != "" {
			fmt.Fprintf(&b, "\nstderr:\n%s", clip(s))
		}
		return strings.TrimSpace(b.String())
	case "job_get":
		return fmt.Sprintf("job %v kind=%v status=%v %v", m["job_id"], m["kind"], m["status"], m["summary"])
	case "job_output":
		var b strings.Builder
		chunks, _ := m["chunks"].([]any)
		for _, c := range chunks {
			cm, _ := c.(map[string]any)
			b.WriteString(str(cm, "data"))
		}
		s := fmt.Sprintf("state=%v next_cursor=%v", m["state"], m["next_cursor"])
		if b.Len() > 0 {
			s += "\noutput:\n" + clip(b.String())
		}
		return s
	case "files_list":
		entries, _ := m["entries"].([]any)
		var names []string
		for i, e := range entries {
			if i == 20 {
				names = append(names, "...")
				break
			}
			em, _ := e.(map[string]any)
			n := str(em, "name")
			if d, _ := em["dir"].(bool); d {
				n += "\\"
			}
			names = append(names, n)
		}
		return fmt.Sprintf("%d entries in %v: %s", len(entries), m["path"], strings.Join(names, ", "))
	case "files_stat":
		return fmt.Sprintf("exists=%v dir=%v size=%v", m["exists"], m["dir"], m["size"])
	case "files_read":
		data := str(m, "data")
		n := int64(len(data) / 4 * 3)
		if dec, err := base64.StdEncoding.DecodeString(data); err == nil {
			n = int64(len(dec))
		}
		off, _ := num(params, "offset")
		s := fmt.Sprintf("read %d bytes at offset %d, file size %v, eof=%v (base64 in data)", n, off, m["size"], m["eof"])
		if eof, _ := m["eof"].(bool); !eof {
			s += fmt.Sprintf("; next offset %d", off+n)
		}
		return s
	case "proc_list":
		procs, _ := m["procs"].([]any)
		return fmt.Sprintf("%d processes", len(procs))
	case "proc_kill":
		return fmt.Sprintf("killed=%v", m["killed"])
	case "app_launch":
		if m["pid"] != nil {
			return fmt.Sprintf("pid=%v", m["pid"])
		}
	}
	return ""
}
