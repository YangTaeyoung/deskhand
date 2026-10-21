package localapi

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/observe"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// scopeKind is the part of a host's AI scope a method needs.
type scopeKind int

const (
	scopeNone scopeKind = iota
	scopeScreen
	scopeFiles
	scopeArtifacts
	scopeQA
)

// scopeMethods are the methods an AI client's scope limits (docs/local-api.md
// §3 AI 범위).
var scopeMethods = map[string]scopeKind{
	"screen.capture": scopeScreen, "observe": scopeScreen,
	"input.click": scopeScreen, "input.move": scopeScreen, "input.drag": scopeScreen,
	"input.scroll": scopeScreen, "input.key": scopeScreen, "input.type": scopeScreen,
	"ui.tree": scopeScreen, "ui.find": scopeScreen, "ui.text": scopeScreen, "ui.rect": scopeScreen,
	"ui.focused": scopeScreen, "ui.at": scopeScreen, "ui.act": scopeScreen,
	"win.list": scopeScreen, "win.action": scopeScreen,
	"files.list": scopeFiles, "files.stat": scopeFiles, "files.read": scopeFiles, "files.write": scopeFiles,
	"files.mkdir": scopeFiles, "files.rename": scopeFiles, "files.delete": scopeFiles, "files.copy": scopeFiles,
	"files.trash": scopeFiles, "files.hash": scopeFiles,
	"transfer.start": scopeFiles,
	"artifacts.list": scopeArtifacts, "artifacts.export": scopeArtifacts,
	"qa.run": scopeQA,
}

// scopeRefusal is PERMISSION_DENIED for what the person did not let AI
// clients reach.
func scopeRefusal(what string) *apierr.Error {
	return apierr.New(wire.CodePermissionDenied,
		what+" 사람이 이 PC에서 AI에게 허용하지 않았습니다. Deskhand 앱의 개인정보 설정에서 허용한 뒤 다시 시도하도록 사용자에게 알려 주세요", false, "")
}

// hostAdminMethods are the methods that add, remove, repair, connect,
// disconnect or change a host; AI clients never call them.
var hostAdminMethods = map[string]bool{
	"host.add": true, "host.remove": true, "host.repair": true,
	"host.connect": true, "host.disconnect": true, "host.update": true,
}

// privilegedMethods run commands, terminals, processes, apps, debuggers or
// tunnels on a host; they reach the screen, the files and the clipboard
// around the scope, so an AI client may call them only on a host with no
// AI limit. hostOptional lists those that take no host_id.
var privilegedMethods = map[string]bool{
	"exec.run": true, "exec.start": true, "proc.list": true, "proc.kill": true, "app.launch": true,
	"pty.open": true, "tunnel.open": true, "tunnel.close": true, "tunnel.list": true,
	"debug.list": true, "debug.get": true, "debug.electron.launch": true, "debug.attach": true,
	"debug.target.attach": true, "debug.breakpoint.set": true, "debug.breakpoint.update": true,
	"debug.breakpoint.remove": true, "debug.breakpoints.activate": true, "debug.exceptions": true,
	"debug.pause": true, "debug.resume": true, "debug.step": true, "debug.state": true,
	"debug.variables": true, "debug.evaluate": true, "debug.scripts": true, "debug.source": true,
	"debug.console": true, "debug.console.clear": true, "debug.close": true,
}

var hostOptional = map[string]bool{"debug.list": true, "tunnel.list": true}

// scopeParams are the params checkScope reads.
type scopeParams struct {
	HostID     string `json:"host_id"`
	JobID      string `json:"job_id"`
	TransferID string `json:"transfer_id"`
	DebugID    string `json:"debug_id"`
	TunnelID   string `json:"tunnel_id"`
	LocalPort  int    `json:"local_port"`
	Path       string `json:"path"`
	From       string `json:"from"`
	To         string `json:"to"`
	Remote     string `json:"remote"`
	Text       string `json:"text"`
	Method     string `json:"method"`
}

// checkScope refuses a call of an AI client that the host's AI scope does
// not allow; calls of other clients are never limited. A call without a
// known host is left to the method.
func (s *Server) checkScope(sess *session, method string, raw json.RawMessage) error {
	if sess.Caller().Actor != model.ActorAI {
		return nil
	}
	var p scopeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		p = scopeParams{}
	}
	if hostAdminMethods[method] {
		hostID := ""
		if row, err := s.d.Hosts.Resolve(p.HostID); err == nil {
			hostID = row.HostID
		}
		return s.denied(sess, hostID, model.TimelineInstall, method,
			apierr.New(wire.CodePermissionDenied, "AI 클라이언트는 PC를 추가·삭제·복구·연결·설정할 수 없습니다. Deskhand 앱이나 deskhand CLI에서 사람이 해야 합니다", false, ""))
	}
	if privilegedMethods[method] {
		return s.checkPrivileged(sess, method, s.privilegedHost(method, p))
	}
	switch method {
	case "jobs.get", "jobs.output":
		job, err := s.d.Jobs.Get(sess.ctx, p.JobID)
		if err != nil {
			return nil
		}
		if !s.aiJobAllowed(job) {
			return s.denied(sess, job.HostID, model.TimelineExec, method, scopeRefusal("제한된 PC의 job을"))
		}
		return nil
	case "transfer.get", "transfer.resume":
		if s.d.Transfers == nil {
			return nil
		}
		t, err := s.d.Transfers.Get(p.TransferID)
		if err != nil {
			return nil
		}
		if s.d.Hosts.AIScope(t.HostID).Files != model.FilesAll {
			return s.denied(sess, t.HostID, model.TimelineFile, method, scopeRefusal("파일 접근이 제한된 PC의 전송을"))
		}
		return nil
	case "transfer.list":
		if p.HostID == "" {
			return nil
		}
		row, err := s.d.Hosts.Resolve(p.HostID)
		if err != nil {
			return err
		}
		if s.d.Hosts.AIScope(row.HostID).Files != model.FilesAll {
			return s.denied(sess, row.HostID, model.TimelineFile, method, scopeRefusal("파일 접근이 제한된 PC의 전송을"))
		}
		return nil
	}
	kind, ok := scopeMethods[method]
	if !ok {
		return nil
	}
	switch kind {
	case scopeQA:
		if p.HostID == "" {
			hosts, err := s.d.Hosts.List()
			if err != nil {
				return err
			}
			for _, h := range hosts {
				if !h.AIScope.Screen {
					return s.denied(sess, h.HostID, model.TimelineObserve, method, scopeRefusal("QA 실행이 쓰는 화면과 입력을"))
				}
			}
			return nil
		}
	case scopeArtifacts:
		job, err := s.d.Jobs.Get(sess.ctx, p.JobID)
		if err != nil {
			return nil
		}
		p.HostID = job.HostID
	}
	if p.HostID == "" {
		return nil
	}
	row, err := s.d.Hosts.Resolve(p.HostID)
	if err != nil {
		return err
	}
	scope := s.d.Hosts.AIScope(row.HostID)
	switch kind {
	case scopeScreen, scopeQA, scopeArtifacts:
		if !scope.Screen {
			kindName := model.TimelineObserve
			if strings.HasPrefix(method, "input.") || method == "ui.act" || method == "win.action" {
				kindName = model.TimelineInput
			}
			return s.denied(sess, row.HostID, kindName, method, scopeRefusal("화면과 그 위의 입력을"))
		}
		if method == "input.type" && !scope.Clipboard {
			if _, used, err := observe.TextEvents(p.Text, p.Method); err == nil && used == observe.MethodPaste {
				return s.denied(sess, row.HostID, model.TimelineInput, method,
					scopeRefusal("클립보드를 거치는 입력(이모지·제어 문자·2000자 넘는 글)을"))
			}
		}
	case scopeFiles:
		if scope.Files == model.FilesAll {
			return nil
		}
		if method == "transfer.start" {
			return s.denied(sess, row.HostID, model.TimelineFile, method, scopeRefusal("파일 접근이 제한된 PC의 전송을"))
		}
		paths := []string{p.Path}
		if method == "files.rename" || method == "files.copy" {
			paths = []string{p.From, p.To}
		}
		for _, path := range paths {
			if !scope.AllowsFile(row.OS, path) {
				what := "파일 접근을"
				if scope.Files != model.FilesNone {
					what = fmt.Sprintf("작업 루트 밖의 경로 %q를", path)
				}
				return s.denied(sess, row.HostID, model.TimelineFile, method, scopeRefusal(what))
			}
		}
	}
	return nil
}

// privilegedHost returns the host a privileged method acts on: the host of
// its debug session or tunnel, else params.host_id; empty when unknown.
func (s *Server) privilegedHost(method string, p scopeParams) string {
	switch {
	case strings.HasPrefix(method, "debug.") && p.DebugID != "" && s.d.Debug != nil:
		if hostID, _, ok := s.d.Debug.Meta(p.DebugID); ok {
			return hostID
		}
		return ""
	case method == "tunnel.close" && s.d.Tunnels != nil:
		if p.TunnelID != "" {
			if info, ok := s.d.Tunnels.Get(p.TunnelID); ok {
				return info.HostID
			}
			return ""
		}
		if info, ok := s.d.Tunnels.FindLocal(p.LocalPort); ok {
			return info.HostID
		}
		return ""
	}
	return p.HostID
}

// checkPrivileged refuses a privileged method of an AI client on a host
// with any AI limit. Without a host it refuses only the methods that list
// across hosts, while any host is limited.
func (s *Server) checkPrivileged(sess *session, method, hostRef string) error {
	if hostRef == "" {
		if hostOptional[method] && s.d.Hosts.AnyAIRestricted() {
			return s.denied(sess, "", model.TimelineExec, method, scopeRefusal("AI 범위가 제한된 PC가 있어 PC 전체의 명령·디버그·터널 목록을"))
		}
		return nil
	}
	row, err := s.d.Hosts.Resolve(hostRef)
	if err != nil {
		return err
	}
	if s.d.Hosts.AIScope(row.HostID).Restricted() {
		return s.denied(sess, row.HostID, model.TimelineExec, method,
			scopeRefusal("제한된 PC에서 명령 실행·터미널·프로세스·앱 실행·디버그·터널을"))
	}
	return nil
}

// aiJobAllowed reports whether an AI client may read job j: a transfer job
// follows the files scope, a QA job the screen scope and any other job
// needs a host with no AI limit.
func (s *Server) aiJobAllowed(j model.Job) bool {
	if j.HostID == "" {
		return true
	}
	scope := s.d.Hosts.AIScope(j.HostID)
	switch j.Kind {
	case jobs.KindTransfer:
		return scope.Files == model.FilesAll
	case jobs.KindQA:
		return scope.Screen
	}
	return !scope.Restricted()
}

// denied records the refusal on the timeline and returns it.
func (s *Server) denied(sess *session, hostID, kind, method string, e *apierr.Error) error {
	if s.d.Bus != nil {
		_, _ = s.d.Bus.Record(sess.Caller().Entry(hostID, kind, method+" 거절: AI 범위 밖", model.TimelineDenied, ""))
	}
	return e
}
