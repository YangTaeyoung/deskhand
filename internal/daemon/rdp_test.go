package daemon_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestRDPLocalRPCForwardsMetadataAndHonorsHostAuthorization(t *testing.T) {
	var queries atomic.Int32
	locked := false
	e := setup(t, setupOpts{trustDaemon: true, agentTune: func(opt *server.Options) {
		opt.RDPListener = func(context.Context) (wire.RDPListener, error) {
			queries.Add(1)
			return wire.RDPListener{Enabled: true, Port: 3397, CertificateSHA256: strings.Repeat("ab", 32)}, nil
		}
		opt.SessionInfo = func() (session.Info, error) {
			return session.Info{SessionID: 7, User: `PC\desk`, SID: "S-1-5-21-1-1001", Protocol: "rdp", ConnectState: "active", Locked: &locked}, nil
		}
	}})
	e.waitState(t, "ready")
	got := e.call(t, "session.rdp", map[string]any{"host_id": "pc", "session_id": 999, "port": 1, "certificate_sha256": "untrusted"})
	if len(got) != 9 || got["port"] != float64(3397) || got["session_id"] != float64(7) || got["user"] != `PC\desk` || got["sid"] != "S-1-5-21-1-1001" ||
		got["certificate_sha256"] != strings.Repeat("AB", 32) || got["enabled"] != true || got["locked"] != false || got["protocol"] != "rdp" || got["connect_state"] != "active" {
		t.Fatalf("exact local result: %v", got)
	}
	ai := dialAs(t, "mcp", "ai")
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"screen": false}})
	before := queries.Load()
	if _, err := callErr(t, ai, "session.rdp", map[string]any{"host_id": "pc"}); err == nil || err.Code != wire.CodePermissionDenied {
		t.Fatalf("AI scope: %v", err)
	}
	if queries.Load() != before {
		t.Fatal("unauthorized request reached agent")
	}
	for _, tc := range []struct {
		params any
		code   string
	}{
		{map[string]any{}, wire.CodeInvalidArgument},
		{map[string]any{"host_id": 3}, wire.CodeInvalidArgument},
		{map[string]any{"host_id": "unknown"}, wire.CodeNotFound},
	} {
		if _, err := callErr(t, e.c, "session.rdp", tc.params); err == nil || err.Code != tc.code {
			t.Fatalf("params %v: %v", tc.params, err)
		}
	}
	// A human retains the same read when the AI screen scope is disabled.
	e.call(t, "session.rdp", map[string]any{"host_id": "pc"})
}

func TestRDPLocalRPCRejectsUnpairedAgent(t *testing.T) {
	var queries atomic.Int32
	e := setup(t, setupOpts{trustDaemon: false, agentTune: func(opt *server.Options) {
		opt.RDPListener = func(context.Context) (wire.RDPListener, error) {
			queries.Add(1)
			return wire.RDPListener{}, errors.New("must not run")
		}
	}})
	e.waitState(t, "blocked")
	if _, err := callErr(t, e.c, "session.rdp", map[string]any{"host_id": "pc"}); err == nil || err.Code != wire.CodeAuthRequired {
		t.Fatalf("unpaired agent: %v", err)
	}
	if queries.Load() != 0 {
		t.Fatal("unpaired metadata query reached agent")
	}
}

func TestInputLocalStreamNoVideoCursorOrCaptureAndReleasesOnDisconnect(t *testing.T) {
	agent := newScreenAgent()
	var captures atomic.Int32
	e := setup(t, setupOpts{trustDaemon: true, agentStreams: agent.streams(), agentTune: func(opt *server.Options) {
		opt.SessionInfo = func() (session.Info, error) {
			return session.Info{SessionID: 7, SID: "S-1-5-21-1-1001"}, nil
		}
		// No displays or capture support are needed for the local input relay.
		opt.Displays = func() ([]display.Info, error) { return []display.Info{}, nil }
		opt.Capture = func(int, string, int, int) (capture.StillResult, error) {
			captures.Add(1)
			return capture.StillResult{}, errors.New("capture must not run")
		}
	}})
	e.waitState(t, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// SessionInfo is fetched asynchronously after the paired connection is ready.
	waitFor(t, "authoritative session.info", func() bool {
		hosts := e.call(t, "host.list", nil)["hosts"].([]any)
		current, _ := hosts[0].(map[string]any)["session"].(map[string]any)
		return current["sid"] == "S-1-5-21-1-1001"
	})
	generation := e.call(t, "host.list", nil)["hosts"].([]any)[0].(map[string]any)["connection_generation"]
	params := map[string]any{"host_id": "pc", "session_id": uint32(7), "sid": "S-1-5-21-1-1001", "connection_generation": generation}
	for _, field := range []string{"sid", "session_id", "connection_generation"} {
		bad := make(map[string]any)
		for k, v := range params {
			bad[k] = v
		}
		switch field {
		case "sid":
			bad[field] = "S-1-5-21-2-1002"
		case "session_id":
			bad[field] = uint32(8)
		case "connection_generation":
			bad[field] = generation.(float64) + 1
		}
		stream, err := client.Attach(ctx, client.Options{}, "app", "test", "input", bad)
		if err == nil {
			stream.Close()
			t.Fatalf("accepted mismatched %s", field)
		}
		var ce *apierr.Error
		if !errors.As(err, &ce) || ce.Code != wire.CodeSessionChanged {
			t.Fatalf("mismatched %s: %v", field, err)
		}
	}
	for _, who := range []string{"mcp", "app"} {
		s, err := client.Attach(ctx, client.Options{Start: func(context.Context) error { return errors.New("no daemon") }}, who, "test", "input", params)
		if who == "mcp" {
			if err == nil {
				s.Close()
				t.Fatal("AI input attach accepted")
			}
			var ce *apierr.Error
			if !errors.As(err, &ce) || ce.Code != wire.CodePermissionDenied {
				t.Fatalf("AI refusal: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Result) != 1 || s.Result["host_id"] != "pc" {
			t.Fatalf("input attach result: %v", s.Result)
		}
		if err := s.WriteFrame(wire.KindClientInput, []byte(`{"k":"key","sc":29,"down":true}`)); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "key input", func() bool { return len(agent.inputEvents()) == 1 })
		s.Close()
		waitFor(t, "disconnect release", func() bool { evs := agent.inputEvents(); return len(evs) == 2 && evs[1].K == "release_all" })
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.videos) != 0 || agent.cursors != 0 || captures.Load() != 0 {
		t.Fatal("input attach opened media")
	}
}
