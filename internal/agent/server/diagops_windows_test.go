//go:build windows

package server

import (
	"os"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestDiagOpsAnswerThroughTheAgent(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	sys := c.ok("diag.system", nil)
	osInfo, _ := sys["os"].(map[string]any)
	if name, _ := osInfo["name"].(string); !strings.HasPrefix(name, "Windows") || sys.String("computer_name") == "" || sys.Int("uptime_s") <= 0 {
		t.Fatalf("diag.system %v", sys)
	}
	svc := c.ok("diag.services", proto.Message{"name": "eventlog"})
	list, _ := svc["services"].([]any)
	found := false
	for _, s := range list {
		m := s.(map[string]any)
		found = found || (m["name"] == "EventLog" && m["state"] == "running")
	}
	if !found {
		t.Fatalf("diag.services eventlog %v", svc)
	}
	ev := c.ok("diag.events", proto.Message{"max": 3, "since": wire.FormatDiagTime(time.Now().AddDate(0, 0, -7))})
	if events, _ := ev["events"].([]any); len(events) == 0 || len(events) > 3 {
		t.Fatalf("diag.events %v", ev)
	}
	if res := c.call("diag.events", proto.Message{"provider": `a'b"c`}); res.String("code") != wire.CodeInvalidArgument {
		t.Fatalf("diag.events with both quotes %v", res)
	}
	cr := c.ok("diag.crashes", nil)
	if _, ok := cr["dumps"].([]any); !ok {
		t.Fatalf("diag.crashes %v", cr)
	}
}

func TestProcListReadsThePathCommandLineAndUserOfTheAgent(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	self := listed(c, os.Getpid())
	exe, _ := os.Executable()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if self == nil || !strings.EqualFold(self["path"].(string), exe) || !strings.Contains(self["cmd"].(string), "test.") || !strings.EqualFold(self["user"].(string), u.Username) {
		t.Fatalf("self %v, expected %s run by %s", self, exe, u.Username)
	}
	created, ok := pidCreation(os.Getpid())
	if !ok || self["started"] != wire.FormatDiagTime(created) {
		t.Fatalf("started %v, expected %s", self["started"], wire.FormatDiagTime(created))
	}
	if idle := listed(c, 0); idle == nil || idle["name"] != idleProcessName || idle["started"] == nil {
		t.Fatalf("pid 0 %v", idle)
	}
}
