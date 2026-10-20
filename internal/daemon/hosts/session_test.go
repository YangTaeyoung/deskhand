package hosts

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/proto"
)

func TestSessionLockStateIsLeftOutWhenTheAgentDidNotReportIt(t *testing.T) {
	s := sessionInfo(proto.Message{"session_id": float64(1), "user": "pc\\me", "connect_state": "active"}, nil)
	if s.Locked != nil {
		t.Fatalf("locked %v without a report", *s.Locked)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), `"locked"`) {
		t.Fatalf("json %s", b)
	}
	if s.Protocol != "unknown" || s.InputDesktop != "unknown" {
		t.Fatalf("session %+v", s)
	}
	for _, locked := range []bool{true, false} {
		s = sessionInfo(proto.Message{"session_id": float64(1), "user": "me"}, proto.Message{"locked": locked})
		if s.Locked == nil || *s.Locked != locked {
			t.Fatalf("locked %v reported, got %v", locked, s.Locked)
		}
	}
}
