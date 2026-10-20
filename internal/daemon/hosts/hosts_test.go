package hosts

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const agentFP = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// fakeSSH answers the PowerShell scripts of install.Pair and host.doctor.
type fakeSSH struct {
	mu         sync.Mutex
	paired     bool
	firewall   string
	sshDown    bool
	forcedInit bool
}

// exitCode is an error with an exit code, like *exec.ExitError.
type exitCode int

func (e exitCode) Error() string { return "exit status " + string(rune('0'+int(e))) }
func (e exitCode) ExitCode() int { return int(e) }

func (f *fakeSSH) run(_ context.Context, name string, args []string, stdin string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sshDown {
		return "", "ssh: connect to host pc port 22: Connection refused\n", &exec.ExitError{}
	}
	if name == "scp" {
		return "", "", nil
	}
	for _, a := range args {
		if a == "-G" {
			return "hostname 192.168.0.20\n", "", nil
		}
	}
	switch {
	case strings.Contains(stdin, "deskhand-probe"):
		return "deskhand-probe os=windows arch=X64\n", "", nil
	case strings.Contains(stdin, "deskhand-short="):
		return "deskhand-short=C:\\Temp\\deskhand-install\n", "", nil
	case strings.Contains(stdin, " init "):
		if f.paired && !strings.Contains(stdin, "--force") {
			return "", "key exists\n", exitCode(4)
		}
		f.forcedInit = strings.Contains(stdin, "--force")
		f.paired = true
		return `{"fingerprint":"` + agentFP + `"}` + "\n", "", nil
	case strings.Contains(stdin, " install"):
		return "", "", nil
	case strings.Contains(stdin, " version"):
		return `{"version":"v1","os":"windows","arch":"amd64"}` + "\n", "", nil
	case strings.Contains(stdin, "deskhand-ok"):
		return "deskhand-ok\n", "", nil
	case strings.Contains(stdin, "Get-NetFirewallRule"):
		return f.firewall + "\n", "", nil
	}
	return "", "", nil
}

type fixture struct {
	r   *Registry
	st  *store.Store
	sub *events.Sub
	ssh *fakeSSH
}

func newFixture(t *testing.T, dial conn.Dialer) fixture {
	t.Helper()
	t.Setenv("DESKHAND_HOME", t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bus := events.NewBus(st, nil)
	sub := bus.Subscribe()
	if dial == nil {
		dial = func(context.Context, conn.Target, transport.Identity) (conn.Session, error) {
			return nil, errors.New("network is unreachable")
		}
	}
	var r *Registry
	mgr := conn.NewManager(conn.Options{Dial: dial, OnEvent: func(ev conn.Event) { r.HandleConnEvent(ev) },
		Backoff: conn.BackoffConfig{Initial: time.Hour}, ReadyWait: 50 * time.Millisecond})
	t.Cleanup(mgr.Close)
	f := &fakeSSH{firewall: "deskhand-fw enabled=True"}
	id := transport.Identity{Fingerprint: strings.Repeat("a", 64)}
	agent := func(arch string) (string, error) { return "/agents/deskhand-agent-windows-" + arch + ".exe", nil }
	r = New(Options{Store: st, Bus: bus, Manager: mgr, Jobs: jobs.New(st, bus, mgr), Identity: id, SSHExec: f.run,
		AgentBinary: agent, Version: "v1", ConnectWait: 50 * time.Millisecond})
	return fixture{r: r, st: st, sub: sub, ssh: f}
}

func drain(sub *events.Sub) []events.Event {
	var out []events.Event
	for {
		select {
		case ev := <-sub.C:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func progressOf(evs []events.Event) []string {
	var out []string
	for _, ev := range evs {
		if ev.Kind == events.InstallProgress {
			out = append(out, ev.Fields["step"].(string)+":"+ev.Fields["status"].(string))
		}
	}
	return out
}

func kinds(evs []events.Event) map[string]int {
	out := map[string]int{}
	for _, ev := range evs {
		out[ev.Kind]++
	}
	return out
}

func TestAddPairsStoresAndConnects(t *testing.T) {
	fx := newFixture(t, nil)
	h, job, err := fx.r.Add(context.Background(), AddParams{Name: "Windows Dev", SSH: "windows"}, model.Caller{Actor: model.ActorHuman, ClientID: "c-1"})
	if err != nil {
		t.Fatal(err)
	}
	if h.HostID != "windows-dev" || h.Address != "192.168.0.20" || h.Port != 47600 || h.OS != "windows" || h.Arch != "amd64" ||
		h.SSHAlias != "windows" || h.Owner != "none" || h.Displays == nil || h.Caps == nil {
		t.Fatalf("host %+v", h)
	}
	if h.State != "reconnecting" && h.State != "connecting" {
		t.Fatalf("state of a host whose agent does not answer: %s", h.State)
	}
	row, err := fx.st.GetHost("windows-dev")
	if err != nil || row.AgentFP != agentFP {
		t.Fatalf("stored %+v, %v", row, err)
	}
	if job.Kind != "install" || job.Status != model.StatusSucceeded {
		t.Fatalf("job %+v", job)
	}
	evs := drain(fx.sub)
	want := []string{"probe:active", "probe:done", "copy:active", "copy:done", "init:active", "init:done", "install:active",
		"install:done", "verify:active", "verify:done"}
	if got := progressOf(evs); !reflect.DeepEqual(got, want) {
		t.Fatalf("progress %v", got)
	}
	k := kinds(evs)
	if k[events.HostAdded] != 1 || k[events.TimelineAppended] == 0 || k[events.JobUpdated] < 2 || k[events.ConnectionState] == 0 {
		t.Fatalf("event kinds %v", k)
	}
	for _, ev := range evs {
		if ev.Kind == events.InstallProgress && (ev.HostID != "windows-dev" || ev.Fields["name"] != "Windows Dev") {
			t.Fatalf("progress event %+v", ev)
		}
	}

	_, _, err = fx.r.Add(context.Background(), AddParams{Name: "Windows Dev", SSH: "windows"}, model.System)
	if e := apierr.As(err); e == nil || e.Code != wire.CodeInvalidArgument {
		t.Fatalf("second add with the same name: %v", err)
	}
}

func TestAddAlreadyPairedSuggestsRepair(t *testing.T) {
	fx := newFixture(t, nil)
	fx.ssh.paired = true
	_, job, err := fx.r.Add(context.Background(), AddParams{Name: "pc", SSH: "windows"}, model.System)
	e := apierr.As(err)
	if e == nil || e.Code != wire.CodeInvalidArgument || e.NextAction != "host_repair" || !strings.Contains(e.Message, "host repair") {
		t.Fatalf("error %#v", err)
	}
	if job.Status != model.StatusFailed {
		t.Fatalf("job %+v", job)
	}
	if _, err := fx.st.GetHost("pc"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a failed pairing stored the host: %v", err)
	}
	evs := drain(fx.sub)
	got := progressOf(evs)
	if got[len(got)-1] != "init:failed" {
		t.Fatalf("progress %v", got)
	}
	for _, ev := range evs {
		if ev.Kind == events.InstallProgress && ev.Fields["status"] == "failed" {
			ef := ev.Fields["error"].(map[string]any)
			if ef["code"] != wire.CodeInvalidArgument || ef["next_action"] != "host_repair" {
				t.Fatalf("failed step error %v", ef)
			}
		}
	}

	fx.ssh.paired = false
	if _, _, err := fx.r.Add(context.Background(), AddParams{Name: "pc", SSH: "windows"}, model.System); err != nil {
		t.Fatal(err)
	}
	fx.ssh.paired = true
	h, _, err := fx.r.Repair(context.Background(), "pc", model.System)
	if err != nil || !fx.ssh.forcedInit || h.HostID != "pc" {
		t.Fatalf("repair %+v, %v, forced %v", h, err, fx.ssh.forcedInit)
	}
}

func TestHostIDFromName(t *testing.T) {
	fx := newFixture(t, nil)
	if id := fx.r.hostID("Windows Dev"); id != "windows-dev" {
		t.Fatalf("id %q", id)
	}
	if id := fx.r.hostID("사무실 PC"); !strings.HasPrefix(id, "h-") || len(id) != 28 {
		t.Fatalf("id of a Korean name %q", id)
	}
	if err := fx.st.PutHost(store.Host{HostID: "pc", Name: "PC", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if id := fx.r.hostID("pc"); id != "pc-2" {
		t.Fatalf("id next to a taken one %q", id)
	}
}

func TestResolveByIDOrName(t *testing.T) {
	fx := newFixture(t, nil)
	if err := fx.st.PutHost(store.Host{HostID: "h-1", Name: "사무실 PC", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"h-1", "사무실 PC"} {
		if h, err := fx.r.Resolve(ref); err != nil || h.HostID != "h-1" {
			t.Fatalf("resolve %q: %+v, %v", ref, h, err)
		}
	}
	if _, err := fx.r.Resolve("nope"); apierr.CodeOf(err) != wire.CodeNotFound {
		t.Fatalf("resolve of an unknown host: %v", err)
	}
}

func TestDoctorReportsEveryCheck(t *testing.T) {
	fx := newFixture(t, nil)
	if err := fx.st.PutHost(store.Host{HostID: "pc", Name: "pc", Address: "192.168.0.20", Port: 47600, AgentFP: agentFP,
		SSHAlias: "windows", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := fx.r.Load(); err != nil {
		t.Fatal(err)
	}
	checks, err := fx.r.Doctor(context.Background(), "pc")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var order []string
	for _, c := range checks {
		got[c.ID] = c.Status
		order = append(order, c.ID)
		if c.Detail == "" {
			t.Errorf("check %s has no detail", c.ID)
		}
		if c.Status == model.CheckFail && c.Fix == "" {
			t.Errorf("failed check %s has no fix", c.ID)
		}
	}
	if want := []string{"ssh_reachable", "agent_version", "quic_connect", "fingerprint", "session", "firewall_rule"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("check order %v", order)
	}
	want := map[string]string{"ssh_reachable": "pass", "agent_version": "pass", "quic_connect": "fail", "fingerprint": "skip",
		"session": "skip", "firewall_rule": "pass"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("checks %v", got)
	}

	fx.ssh.sshDown = true
	checks, _ = fx.r.Doctor(context.Background(), "pc")
	if checks[0].Status != "fail" || checks[5].Status != "skip" {
		t.Fatalf("checks without SSH %+v", checks)
	}
}

func TestDoctorReportsFingerprintMismatch(t *testing.T) {
	fx := newFixture(t, func(context.Context, conn.Target, transport.Identity) (conn.Session, error) {
		return nil, transport.ErrPeerMismatch
	})
	if err := fx.st.PutHost(store.Host{HostID: "pc", Name: "pc", Address: "192.168.0.20", Port: 47600, AgentFP: agentFP,
		SSHAlias: "windows", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := fx.r.Load(); err != nil {
		t.Fatal(err)
	}
	checks, err := fx.r.Doctor(context.Background(), "pc")
	if err != nil {
		t.Fatal(err)
	}
	if checks[3].ID != "fingerprint" || checks[3].Status != "fail" || !strings.Contains(checks[3].Fix, "host repair pc") {
		t.Fatalf("fingerprint check %+v", checks[3])
	}
	if h, _ := fx.r.Get("pc"); h.State != "blocked" || !strings.Contains(h.Reason, "인증서가 페어링 때와 다릅니다") {
		t.Fatalf("host %+v", h)
	}
}

func TestFindAgentBinarySearchesAgentDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DESKHAND_AGENT_DIR", dir)
	if _, err := FindAgentBinary("arm64"); err == nil || !strings.Contains(err.Error(), "DESKHAND_AGENT_DIR "+dir) ||
		!strings.Contains(err.Error(), "Resources/agents") {
		t.Fatalf("missing binary error %v", err)
	}
	p := filepath.Join(dir, "deskhand-agent-windows-arm64.exe")
	if err := os.WriteFile(p, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := FindAgentBinary("arm64"); err != nil || got != p {
		t.Fatalf("found %q, %v", got, err)
	}
}

func TestDoctorLeavesDisconnectedHostOffline(t *testing.T) {
	fx := newFixture(t, nil)
	if err := fx.st.PutHost(store.Host{HostID: "pc", Name: "pc", Address: "192.168.0.20", Port: 47600, AgentFP: agentFP,
		SSHAlias: "windows", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if h, err := fx.r.Disconnect("pc"); err != nil || h.State != "offline" {
		t.Fatalf("disconnect %+v, %v", h, err)
	}
	checks, err := fx.r.Doctor(context.Background(), "pc")
	if err != nil {
		t.Fatal(err)
	}
	if checks[2].ID != "quic_connect" || !strings.Contains(checks[2].Detail, "다시 끊습니다") {
		t.Fatalf("quic_connect %+v", checks[2])
	}
	if h, _ := fx.r.Get("pc"); h.State != "offline" {
		t.Fatalf("state after doctor %s", h.State)
	}

	if _, err := fx.r.Connect(context.Background(), "pc"); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.r.Doctor(context.Background(), "pc"); err != nil {
		t.Fatal(err)
	}
	if h, _ := fx.r.Get("pc"); h.State == "offline" {
		t.Fatal("doctor disconnected a host that was connecting")
	}
}
