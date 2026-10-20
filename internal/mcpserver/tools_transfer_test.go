package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func detailOf(extra map[string]any) map[string]any {
	m := map[string]any{"transfer_id": "t-1", "host_id": "windows-dev", "direction": "push", "local": "/tmp/b", "remote": `C:\b`, "on_conflict": "fail",
		"state": "done", "bytes_done": 10, "bytes_total": 10, "files_done": 1, "files_failed": 0, "files_total": 1, "rate_bps": 0, "outcome": "written",
		"rev": 4, "updated_at": "2026-10-20T00:00:00.000Z", "files": []any{}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestTransferParamMapping(t *testing.T) {
	ok := func(map[string]any) (any, *rpcFail) {
		return envelope(map[string]any{"status": "running", "transfer_id": "t-1", "job_id": "j-1"}), nil
	}
	detail := func(map[string]any) (any, *rpcFail) { return detailOf(nil), nil }
	f := startFakeAPI(t, map[string]handler{
		"transfer.start": ok, "files.copy": func(map[string]any) (any, *rpcFail) {
			return envelope(map[string]any{"path": `C:\b`, "copied": 1, "outcome": "written"}), nil
		},
		"files.trash": func(map[string]any) (any, *rpcFail) { return envelope(map[string]any{"trashed": true}), nil },
		"files.hash": func(map[string]any) (any, *rpcFail) {
			return envelope(map[string]any{"sha256": "ab", "offset": 0, "length": 3, "size": 3}), nil
		},
		"transfer.get": detail, "transfer.pause": detail, "transfer.resume": detail, "transfer.cancel": detail,
		"transfer.list": func(map[string]any) (any, *rpcFail) { return map[string]any{"transfers": []any{detailOf(nil)}}, nil },
	})
	cs := connect(t)
	cases := []struct {
		tool   string
		args   map[string]any
		method string
		want   map[string]any
	}{
		{"files_push", map[string]any{"host": "win", "local": "/tmp/b", "remote": `C:\b`, "on_conflict": "rename"}, "transfer.start",
			map[string]any{"host_id": "win", "local": "/tmp/b", "remote": `C:\b`, "on_conflict": "rename", "direction": "push"}},
		{"files_pull", map[string]any{"host": "win", "local": "/tmp/b", "remote": `C:\b`}, "transfer.start",
			map[string]any{"host_id": "win", "local": "/tmp/b", "remote": `C:\b`, "direction": "pull"}},
		{"files_move", map[string]any{"host": "win", "direction": "pull", "local": "/tmp/b", "remote": `C:\b`}, "transfer.start",
			map[string]any{"host_id": "win", "local": "/tmp/b", "remote": `C:\b`, "direction": "move_pull"}},
		{"files_move", map[string]any{"host": "win", "direction": "push", "local": "/tmp/b", "remote": `C:\b`, "on_conflict": "skip"}, "transfer.start",
			map[string]any{"host_id": "win", "local": "/tmp/b", "remote": `C:\b`, "on_conflict": "skip", "direction": "move_push"}},
		{"files_copy", map[string]any{"host": "win", "from": `C:\a`, "to": `C:\b`, "on_conflict": "replace", "recursive": true}, "files.copy",
			map[string]any{"host_id": "win", "from": `C:\a`, "to": `C:\b`, "on_conflict": "replace", "recursive": true}},
		{"files_trash", map[string]any{"host": "win", "path": `C:\a`}, "files.trash", map[string]any{"host_id": "win", "path": `C:\a`}},
		{"files_hash", map[string]any{"host": "win", "path": `C:\a`, "offset": 2, "length": 3}, "files.hash",
			map[string]any{"host_id": "win", "path": `C:\a`, "offset": float64(2), "length": float64(3)}},
		{"transfer_get", map[string]any{"transfer_id": "t-1"}, "transfer.get", map[string]any{"transfer_id": "t-1"}},
		{"transfer_list", map[string]any{"host": "win"}, "transfer.list", map[string]any{"host_id": "win"}},
		{"transfer_pause", map[string]any{"transfer_id": "t-1"}, "transfer.pause", map[string]any{"transfer_id": "t-1"}},
		{"transfer_resume", map[string]any{"transfer_id": "t-1"}, "transfer.resume", map[string]any{"transfer_id": "t-1"}},
		{"transfer_cancel", map[string]any{"transfer_id": "t-1"}, "transfer.cancel", map[string]any{"transfer_id": "t-1"}},
		{"files_list", map[string]any{"host": "win", "path": `C:\`, "cursor": "c1", "limit": 50}, "files.list",
			map[string]any{"host_id": "win", "path": `C:\`, "cursor": "c1", "limit": float64(50)}},
	}
	f.handlers["files.list"] = func(map[string]any) (any, *rpcFail) { return map[string]any{"entries": []any{}, "total": 0}, nil }
	for _, c := range cases {
		res := callTool(t, cs, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s: %s", c.tool, text(res))
			continue
		}
		got := f.called(c.method)
		gj, _ := json.Marshal(got[len(got)-1])
		wj, _ := json.Marshal(c.want)
		if string(gj) != string(wj) {
			t.Errorf("%s params = %s, want %s", c.tool, gj, wj)
		}
	}
}

func TestLongTransferReturnsEnvelopeWithPollHint(t *testing.T) {
	startFakeAPI(t, map[string]handler{"transfer.start": func(map[string]any) (any, *rpcFail) {
		return envelope(map[string]any{"status": "running", "transfer_id": "t-9", "job_id": "j-3"}), nil
	}})
	res := callTool(t, connect(t), "files_push", map[string]any{"host": "h", "local": "/tmp/x", "remote": `C:\x`})
	m := structured(t, res)
	if res.IsError || m["transfer_id"] != "t-9" || m["job_id"] != "j-3" || m["next_action"] != "transfer_get" {
		t.Fatalf("structured = %v", m)
	}
	if !strings.Contains(text(res), "transfer_get") || !strings.Contains(text(res), "t-9") {
		t.Fatalf("text = %q", text(res))
	}
}

func TestStartRefusalIsToolErrorWithoutTransferID(t *testing.T) {
	startFakeAPI(t, map[string]handler{"transfer.start": func(map[string]any) (any, *rpcFail) {
		return envelope(map[string]any{"status": "failed", "transfer_id": nil, "action": map[string]any{"executed": false, "outcome": "not_started"},
			"error": map[string]any{"code": "NOT_FOUND", "message": "source missing", "retryable": false, "next_action": "fix_params"}, "next_action": "fix_params"}), nil
	}})
	res := callTool(t, connect(t), "files_pull", map[string]any{"host": "h", "local": "/tmp/x", "remote": `C:\x`})
	m := structured(t, res)
	if !res.IsError || m["code"] != "NOT_FOUND" || !strings.Contains(text(res), "nothing was copied") {
		t.Fatalf("structured = %v text %q", m, text(res))
	}
}

func TestMoveResultStatesSourceOutcome(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]any
		src   string
		text  string
	}{
		{"trashed", map[string]any{"direction": "move_pull"}, "trashed", "Recycle Bin"},
		{"deleted", map[string]any{"direction": "move_push"}, "deleted", "deleted from this Mac"},
		{"retained", map[string]any{"direction": "move_pull", "outcome": "copied_source_retained", "files": []any{
			map[string]any{"rel": "a", "state": "done", "outcome": "skipped"}, map[string]any{"rel": "b", "state": "failed"}}}, "retained", "source was kept (2 files were not moved)"},
		{"pending", map[string]any{"direction": "move_push", "state": "running", "outcome": nil}, "pending", "untouched"},
		{"cancelled", map[string]any{"direction": "move_push", "state": "cancelled", "outcome": nil}, "retained", "source was kept"},
	}
	for _, c := range cases {
		startFakeAPI(t, map[string]handler{"transfer.get": func(map[string]any) (any, *rpcFail) { return detailOf(c.extra), nil }})
		res := callTool(t, connect(t), "transfer_get", map[string]any{"transfer_id": "t-1"})
		m := structured(t, res)
		if res.IsError || m["source"] != c.src || !strings.Contains(text(res), c.text) {
			t.Errorf("%s: source %v text %q", c.name, m["source"], text(res))
		}
	}
}

func TestTransferGetOfCopyHasNoSourceField(t *testing.T) {
	startFakeAPI(t, map[string]handler{"transfer.get": func(map[string]any) (any, *rpcFail) { return detailOf(nil), nil }})
	m := structured(t, callTool(t, connect(t), "transfer_get", map[string]any{"transfer_id": "t-1"}))
	if _, ok := m["source"]; ok {
		t.Fatalf("structured = %v", m)
	}
}

func TestRunningTransferGetPointsBackToTransferGet(t *testing.T) {
	startFakeAPI(t, map[string]handler{"transfer.get": func(map[string]any) (any, *rpcFail) {
		return detailOf(map[string]any{"state": "running", "outcome": nil}), nil
	}})
	res := callTool(t, connect(t), "transfer_get", map[string]any{"transfer_id": "t-1"})
	if m := structured(t, res); m["next_action"] != "transfer_get" || !strings.Contains(text(res), "next_action: transfer_get") {
		t.Fatalf("structured = %v text %q", m, text(res))
	}
}

func TestFilesTrashReportsWhetherAnythingWasRemoved(t *testing.T) {
	startFakeAPI(t, map[string]handler{"files.trash": func(map[string]any) (any, *rpcFail) {
		return envelope(map[string]any{"trashed": false}), nil
	}})
	res := callTool(t, connect(t), "files_trash", map[string]any{"host": "h", "path": `C:\x`})
	if res.IsError || !strings.Contains(text(res), "trashed=false: nothing was removed") {
		t.Fatalf("text = %q", text(res))
	}
}

func TestFilesListTextMentionsNextCursor(t *testing.T) {
	startFakeAPI(t, map[string]handler{"files.list": func(map[string]any) (any, *rpcFail) {
		return map[string]any{"path": `C:\x`, "entries": []any{map[string]any{"name": "a"}}, "total": 3, "next_cursor": "cc"}, nil
	}})
	res := callTool(t, connect(t), "files_list", map[string]any{"host": "h", "path": `C:\x`})
	if m := structured(t, res); m["next_cursor"] != "cc" || !strings.Contains(text(res), "next_cursor") {
		t.Fatalf("structured = %v text %q", m, text(res))
	}
}

func TestTransferInputsAreSchemaValidated(t *testing.T) {
	f := startFakeAPI(t, nil)
	cs := connect(t)
	bad := []struct {
		tool string
		args map[string]any
	}{
		{"files_move", map[string]any{"host": "h", "direction": "sideways", "local": "/a", "remote": `C:\a`}},
		{"files_push", map[string]any{"host": "h", "local": "/a", "remote": `C:\a`, "on_conflict": "merge"}},
		{"files_list", map[string]any{"host": "h", "path": `C:\`, "limit": 5001}},
		{"files_copy", map[string]any{"host": "h", "from": `C:\a`}},
		{"transfer_get", map[string]any{}},
	}
	for _, c := range bad {
		if res := callTool(t, cs, c.tool, c.args); !res.IsError {
			t.Errorf("%s %v accepted", c.tool, c.args)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("daemon called: %v", f.calls)
	}
}

func TestMoveToolDescribesSourceSemantics(t *testing.T) {
	res, err := connect(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name == "files_move" {
			for _, want := range []string{"Recycle Bin", "deleted", "kept", "source field"} {
				if !strings.Contains(tool.Description, want) {
					t.Errorf("files_move description lacks %q", want)
				}
			}
			return
		}
	}
	t.Fatal("files_move not listed")
}
