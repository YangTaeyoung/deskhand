package server

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/agent/session"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestRDPOpUnsupportedOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows contract")
	}
	c, _ := newRPCClient(t, Options{})
	got := c.call("session.rdp", nil)
	if got["ok"] != false || got.String("code") != wire.CodeCapabilityUnsupported {
		t.Fatalf("session.rdp: %v", got)
	}
}

func TestRDPOpReturnsCurrentSessionAndActualListenerReadOnly(t *testing.T) {
	info := testSession
	var mu sync.Mutex
	var queried atomic.Int32
	c, _ := newRPCClient(t, Options{AllowExec: false,
		SessionInfo: func() (session.Info, error) { mu.Lock(); defer mu.Unlock(); return info, nil },
		RDPListener: func(ctx context.Context) (wire.RDPListener, error) {
			queried.Add(1)
			if deadline, ok := ctx.Deadline(); !ok || deadline.IsZero() {
				t.Error("listener query has no deadline")
			}
			return wire.RDPListener{Enabled: true, Port: 3397, CertificateSHA256: strings.Repeat("ab", 32)}, nil
		},
	})
	for _, id := range []uint32{7, 11} {
		mu.Lock()
		info.SessionID = id
		mu.Unlock()
		got := c.ok("session.rdp", map[string]any{"session_id": 999, "user": "attacker", "port": 1})
		if got["session_id"] != float64(id) || got["port"] != float64(3397) || got["enabled"] != true ||
			got["certificate_sha256"] != strings.Repeat("AB", 32) || got["sid"] != testSession.SID || got["user"] != testSession.User ||
			got["protocol"] != "console" || got["connect_state"] != "active" || got["locked"] != false || len(got) != 12 {
			t.Fatalf("RDP response: %v", got)
		}
	}
	if queried.Load() != 2 {
		t.Fatalf("listener queries = %d", queried.Load())
	}
	if wire.Journaled("session.rdp") || wire.WriterOp("session.rdp") {
		t.Fatal("metadata read classified as writer")
	}
}

func TestRDPOpMetadataFailuresAreExplicit(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		change                  func(*session.Info, *wire.RDPListener)
		listenerErr, sessionErr error
	}{
		{name: "listener unavailable", listenerErr: errors.New("access denied")},
		{name: "session unavailable", sessionErr: errors.New("session query failed")},
		{name: "unknown lock", change: func(i *session.Info, _ *wire.RDPListener) { i.Locked = nil }},
		{name: "no SID", change: func(i *session.Info, _ *wire.RDPListener) { i.SID = "" }},
		{name: "session zero", change: func(i *session.Info, _ *wire.RDPListener) { i.SessionID = 0 }},
		{name: "wrong port", change: func(_ *session.Info, r *wire.RDPListener) { r.Port = 65536 }},
		{name: "SHA1 instead of SHA256", change: func(_ *session.Info, r *wire.RDPListener) { r.CertificateSHA256 = strings.Repeat("ab", 20) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := testSession
			listener := wire.RDPListener{Enabled: true, Port: 3389, CertificateSHA256: strings.Repeat("ab", 32)}
			if tc.change != nil {
				tc.change(&info, &listener)
			}
			c, _ := newRPCClient(t, Options{SessionInfo: func() (session.Info, error) { return info, tc.sessionErr },
				RDPListener: func(context.Context) (wire.RDPListener, error) { return listener, tc.listenerErr }})
			got := c.call("session.rdp", nil)
			if got["ok"] != false || got.String("code") != wire.CodeInternal || got.String("error") == "" {
				t.Fatalf("failure: %v", got)
			}
		})
	}
}

func TestParseRDPListenerRejectsIncompleteAndMalformedOutput(t *testing.T) {
	good := `{"enabled":false,"port":3397,"certificate_sha256":"` + strings.Repeat("AB", 32) + `"}`
	if got, err := parseRDPListener([]byte(good)); err != nil || got.Enabled || got.Port != 3397 {
		t.Fatalf("valid disabled listener: %+v %v", got, err)
	}
	for _, raw := range []string{"", "null", "{}", strings.Replace(good, `"enabled":false,`, "", 1), strings.Replace(good, "false", "null", 1),
		strings.Replace(good, "3397", "0", 1), strings.Replace(good, "3397", "65536", 1), strings.Replace(good, "3397", "3.5", 1),
		strings.Replace(good, "AB", "GG", 1), strings.Replace(good, "AB", "A:", 1), good + " garbage", strings.Repeat(" ", maxRDPOutput+1)} {
		if _, err := parseRDPListener([]byte(raw)); err == nil {
			t.Fatalf("accepted malformed listener: %.120s", raw)
		}
	}
	var out rdpOutput
	if n, err := out.Write([]byte(strings.Repeat("x", maxRDPOutput+7))); n != maxRDPOutput+7 || err != nil || !out.overflow || len(out.data) != maxRDPOutput {
		t.Fatalf("output bound failed: %d %v", n, err)
	}
}
