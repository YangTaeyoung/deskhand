package hosts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Doctor check ids (docs/local-api.md §6).
const (
	CheckSSH         = "ssh_reachable"
	CheckAgent       = "agent_version"
	CheckQUIC        = "quic_connect"
	CheckFingerprint = "fingerprint"
	CheckSession     = "session"
	CheckFirewall    = "firewall_rule"
)

// sshCheckTimeout bounds each SSH command of host.doctor.
const sshCheckTimeout = 20 * time.Second

const (
	sshProbeScript   = "Write-Output 'deskhand-ok'"
	agentVersionPS   = "$ErrorActionPreference = 'Continue'\n& 'C:\\Program Files\\Deskhand\\deskhand-agent.exe' version\nexit $LASTEXITCODE"
	firewallScript   = "$r = Get-NetFirewallRule -DisplayName 'Deskhand Agent' -ErrorAction SilentlyContinue | Select-Object -First 1\nif ($r) { Write-Output ('deskhand-fw enabled=' + $r.Enabled) } else { Write-Output 'deskhand-fw none' }"
	firewallRuleName = "Deskhand Agent"
)

func callOpts() conn.CallOpts { return conn.CallOpts{} }

func isUnsupported(err error) bool { return apierr.CodeOf(err) == wire.CodeCapabilityUnsupported }

// Doctor checks the host's SSH access, agent version, QUIC connection,
// certificate fingerprint, desktop session and firewall rule.
func (r *Registry) Doctor(ctx context.Context, ref string) ([]model.Check, error) {
	h, row, err := r.Conn(ref)
	if err != nil {
		return nil, err
	}
	name := row.Name
	repair := fmt.Sprintf("deskhand host repair %s을 실행하세요", name)
	var checks []model.Check
	add := func(id, status, detail, fix string) {
		checks = append(checks, model.Check{ID: id, Status: status, Detail: detail, Fix: fix})
	}

	sshOK := false
	if row.SSHAlias == "" {
		add(CheckSSH, model.CheckSkip, "SSH 별칭이 등록돼 있지 않습니다", "host.remove 후 host.add --ssh <별칭>으로 다시 추가하세요")
	} else {
		sctx, cancel := context.WithTimeout(ctx, sshCheckTimeout)
		out, stderr, err := r.ssh(row.SSHAlias).Run(sctx, sshProbeScript)
		cancel()
		if err == nil && strings.Contains(out, "deskhand-ok") {
			sshOK = true
			add(CheckSSH, model.CheckPass, fmt.Sprintf("ssh %s로 PowerShell에 접속했습니다", row.SSHAlias), "")
		} else {
			add(CheckSSH, model.CheckFail, fmt.Sprintf("ssh %s 접속 실패: %s", row.SSHAlias, firstLine(stderr, err)),
				fmt.Sprintf("터미널에서 ssh %s가 비밀번호 없이 되는지 확인하세요", row.SSHAlias))
		}
	}

	st := h.State()
	if st != conn.StateReady && st != conn.StateDegraded && st != conn.StateBlocked {
		h.Connect()
		r.waitConnected(ctx, h)
		st = h.State()
	}
	connected := st == conn.StateReady || st == conn.StateDegraded

	r.agentCheck(ctx, h, row.SSHAlias, sshOK, connected, repair, add)

	switch {
	case connected:
		add(CheckQUIC, model.CheckPass, fmt.Sprintf("%s:%d에 연결돼 있습니다 (세대 %d)", row.Address, row.Port, h.Generation()), "")
	case st == conn.StateBlocked:
		add(CheckQUIC, model.CheckFail, reasonOf(h), repair)
	default:
		add(CheckQUIC, model.CheckFail, reasonOf(h),
			fmt.Sprintf("PC가 켜져 있는지, 같은 네트워크인지, UDP %d가 방화벽에서 열려 있는지 확인하세요", row.Port))
	}

	switch h.BlockReason() {
	case conn.BlockFingerprint:
		add(CheckFingerprint, model.CheckFail, "PC 에이전트의 인증서 지문이 페어링 때와 다릅니다", repair)
	case conn.BlockRejected:
		add(CheckFingerprint, model.CheckFail, "PC 에이전트가 이 Mac의 인증서를 거절했습니다", repair)
	default:
		if connected {
			add(CheckFingerprint, model.CheckPass, "인증서 지문이 페어링 때와 같습니다", "")
		} else {
			add(CheckFingerprint, model.CheckSkip, "연결되지 않아 지문을 확인하지 못했습니다", "quic_connect 항목을 먼저 해결하세요")
		}
	}

	r.sessionCheck(ctx, h, connected, add)
	r.firewallCheck(ctx, row.SSHAlias, sshOK, repair, add)
	return checks, nil
}

func reasonOf(h *conn.Host) string {
	if err := h.LastError(); err != nil {
		return apierr.As(err).Message
	}
	return "연결되지 않았습니다"
}

func firstLine(text string, err error) string {
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	if err != nil {
		return err.Error()
	}
	return "응답 없음"
}

func (r *Registry) agentCheck(ctx context.Context, h *conn.Host, alias string, sshOK, connected bool, repair string,
	add func(id, status, detail, fix string)) {
	version := ""
	if connected {
		version = h.Hello().Agent
	} else if sshOK {
		sctx, cancel := context.WithTimeout(ctx, sshCheckTimeout)
		out, stderr, err := r.ssh(alias).Run(sctx, agentVersionPS)
		cancel()
		if err != nil {
			add(CheckAgent, model.CheckFail, "설치된 에이전트가 응답하지 않습니다: "+firstLine(stderr, err), repair)
			return
		}
		for _, line := range strings.Split(out, "\n") {
			var v struct{ Version string }
			if line = strings.TrimSpace(line); strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &v) == nil {
				version = v.Version
				break
			}
		}
	}
	switch {
	case version == "":
		add(CheckAgent, model.CheckSkip, "SSH도 연결도 되지 않아 에이전트 버전을 읽지 못했습니다", "ssh_reachable 항목을 먼저 해결하세요")
	case r.opt.Version != "" && version != r.opt.Version:
		add(CheckAgent, model.CheckWarn, fmt.Sprintf("에이전트 %s, 데몬 %s로 버전이 다릅니다", version, r.opt.Version), repair)
	default:
		add(CheckAgent, model.CheckPass, "에이전트 버전 "+version, "")
	}
}

func (r *Registry) sessionCheck(ctx context.Context, h *conn.Host, connected bool, add func(id, status, detail, fix string)) {
	if !connected {
		add(CheckSession, model.CheckSkip, "연결되지 않아 세션을 확인하지 못했습니다", "quic_connect 항목을 먼저 해결하세요")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var info proto.Message
	res, err := h.Call(cctx, "session.info", nil, callOpts())
	switch {
	case err == nil:
		info = res
	case isUnsupported(err):
		info = h.SessionInfo()
	default:
		add(CheckSession, model.CheckFail, "session.info 실패: "+apierr.As(err).Message, "잠시 뒤 deskhand host doctor를 다시 실행하세요")
		return
	}
	s := sessionInfo(h.Hello().Session, info)
	if s == nil {
		add(CheckSession, model.CheckWarn, "에이전트가 데스크톱 세션을 보고하지 않습니다", "PC에 사용자가 로그인해 있는지 확인하세요")
		return
	}
	detail := fmt.Sprintf("세션 %d, 사용자 %s, 상태 %s, 프로토콜 %s, 잠김 %t", s.SessionID, s.User, s.ConnectState, s.Protocol, s.Locked)
	switch {
	case s.Locked:
		add(CheckSession, model.CheckWarn, detail, "PC 화면 잠금을 해제하세요")
	case s.ConnectState == "disconnected":
		add(CheckSession, model.CheckWarn, detail, "PC에 다시 로그인하거나 원격 데스크톱 세션을 다시 연결하세요")
	default:
		add(CheckSession, model.CheckPass, detail, "")
	}
}

func (r *Registry) firewallCheck(ctx context.Context, alias string, sshOK bool, repair string, add func(id, status, detail, fix string)) {
	if !sshOK {
		add(CheckFirewall, model.CheckSkip, "SSH가 되지 않아 방화벽 규칙을 확인하지 못했습니다", "ssh_reachable 항목을 먼저 해결하세요")
		return
	}
	sctx, cancel := context.WithTimeout(ctx, sshCheckTimeout)
	out, stderr, err := r.ssh(alias).Run(sctx, firewallScript)
	cancel()
	switch {
	case err != nil:
		add(CheckFirewall, model.CheckFail, "방화벽 규칙을 읽지 못했습니다: "+firstLine(stderr, err), "PC에서 PowerShell Get-NetFirewallRule이 되는지 확인하세요")
	case strings.Contains(out, "deskhand-fw enabled=True"):
		add(CheckFirewall, model.CheckPass, fmt.Sprintf("방화벽 규칙 '%s'이 켜져 있습니다", firewallRuleName), "")
	case strings.Contains(out, "deskhand-fw enabled="):
		add(CheckFirewall, model.CheckWarn, fmt.Sprintf("방화벽 규칙 '%s'이 꺼져 있습니다", firewallRuleName), repair)
	default:
		add(CheckFirewall, model.CheckFail, fmt.Sprintf("방화벽 규칙 '%s'이 없습니다", firewallRuleName), repair)
	}
}
