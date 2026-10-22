package screen

import (
	"encoding/json"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// InputBinding identifies the verified agent session an input-only viewer
// may control. It never changes during the viewer's lifetime.
type InputBinding struct {
	SessionID            uint32 `json:"session_id"`
	SID                  string `json:"sid"`
	ConnectionGeneration uint64 `json:"connection_generation"`
}

var errInputSessionChanged = apierr.New(wire.CodeSessionChanged, "검증한 입력 세션이 현재 에이전트와 다릅니다. 세션을 다시 확인한 뒤 입력 스트림을 새로 연결하세요", false, apierr.NextCheckState)

// currentInputBinding reads only the current paired connection's session.info,
// never a client claim or persisted hello. Generation checks bracket the read
// because SessionInfo and Generation each take the connection lock separately.
func currentInputBinding(a Agent) (InputBinding, bool) {
	if a == nil {
		return InputBinding{}, false
	}
	gen := a.Generation()
	var info struct {
		SessionID *uint32 `json:"session_id"`
		SID       string  `json:"sid"`
	}
	raw, err := json.Marshal(a.SessionInfo())
	if err != nil || json.Unmarshal(raw, &info) != nil || info.SessionID == nil || info.SID == "" || gen == 0 {
		return InputBinding{}, false
	}
	state := a.State()
	if (state != conn.StateReady && state != conn.StateDegraded) || a.Generation() != gen {
		return InputBinding{}, false
	}
	return InputBinding{SessionID: *info.SessionID, SID: info.SID, ConnectionGeneration: gen}, true
}

func (b InputBinding) matches(a Agent) bool {
	current, ok := currentInputBinding(a)
	return ok && current == b
}

// revokeInputs also runs for idle viewers. Forwarding checks do not depend on
// event delivery, so buffered bus events cannot keep an old viewer authorized.
func (h *Hub) revokeInputs(hostID string) {
	h.mu.Lock()
	var viewers []*Viewer
	for v := range h.inputs {
		if v.hostID == hostID {
			viewers = append(viewers, v)
		}
	}
	h.mu.Unlock()
	for _, v := range viewers {
		if !v.binding.matches(h.agent(hostID)) {
			v.Close()
		}
	}
}
