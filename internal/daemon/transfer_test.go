package daemon_test

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
)

func TestTransferMethodsOverTheLocalAPI(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	watcher, err := client.DialOptions(ctx, "app", "watcher", client.Options{Start: func(context.Context) error { return errors.New("no daemon") }})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	evs, err := watcher.Subscribe(ctx, []string{"transfer.updated"})
	if err != nil {
		t.Fatal(err)
	}
	mac := t.TempDir()
	data := make([]byte, 5<<20+1)
	rand.Read(data)
	src := filepath.Join(mac, "a.bin")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(e.agent.home, "a.bin")
	res := e.call(t, "transfer.start", map[string]any{"direction": "push", "host_id": "pc", "local": src, "remote": dst})
	checkEnvelope(t, res)
	id, _ := res["transfer_id"].(string)
	if res["status"] != "running" || res["error"] != nil || !strings.HasPrefix(id, "t-") || res["host_id"] != "pc" ||
		res["action"].(map[string]any)["executed"] != true {
		t.Fatalf("transfer.start %v", res)
	}
	var tr map[string]any
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		tr = e.call(t, "transfer.get", map[string]any{"transfer_id": id})
		if tr["state"] == "done" || tr["state"] == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transfer %v", tr)
		}
	}
	for _, k := range []string{"transfer_id", "host_id", "direction", "local", "remote", "on_conflict", "state", "bytes_done", "bytes_total",
		"files_done", "files_total", "rate_bps", "files", "rev", "updated_at"} {
		if _, ok := tr[k]; !ok {
			t.Fatalf("transfer has no %s: %v", k, tr)
		}
	}
	files := tr["files"].([]any)
	if tr["state"] != "done" || tr["outcome"] != "written" || tr["bytes_done"] != float64(len(data)) || tr["on_conflict"] != "fail" ||
		len(files) != 1 || files[0].(map[string]any)["rel"] != "a.bin" || files[0].(map[string]any)["path"] != dst {
		t.Fatalf("transfer %v", tr)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != string(data) {
		t.Fatalf("pushed file: %d bytes, %v", len(b), err)
	}
	last := 0.0
	for last < tr["rev"].(float64) {
		select {
		case ev := <-evs:
			got := ev.Params["transfer"].(map[string]any)
			if got["transfer_id"] != id || got["rev"].(float64) <= last {
				t.Fatalf("event %v after rev %v", got, last)
			}
			last = got["rev"].(float64)
		case <-ctx.Done():
			t.Fatalf("transfer.updated events reached rev %v of %v", last, tr["rev"])
		}
	}
	list := e.call(t, "transfer.list", map[string]any{"host_id": "pc"})["transfers"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["transfer_id"] != id {
		t.Fatalf("transfer.list %v", list)
	}
	var cerr *client.Error
	if err := e.c.Call(ctx, "transfer.pause", map[string]any{"transfer_id": id}, nil); !errors.As(err, &cerr) || cerr.Code != "INVALID_ARGUMENT" {
		t.Fatalf("pause of a done transfer: %v", err)
	}
	if err := e.c.Call(ctx, "transfer.get", map[string]any{"transfer_id": "t-missing"}, nil); !errors.As(err, &cerr) || cerr.Code != "NOT_FOUND" {
		t.Fatalf("get of a missing transfer: %v", err)
	}
	if err := e.c.Call(ctx, "transfer.start", map[string]any{"direction": "up", "host_id": "pc", "local": src, "remote": dst}, nil); !errors.As(err, &cerr) || cerr.Code != "INVALID_ARGUMENT" {
		t.Fatalf("start with a wrong direction: %v", err)
	}
	refused := e.call(t, "transfer.start", map[string]any{"direction": "push", "host_id": "pc", "local": filepath.Join(mac, "missing"), "remote": dst})
	checkEnvelope(t, refused)
	if refused["status"] != "failed" || refused["action"].(map[string]any)["outcome"] != "not_started" ||
		refused["error"].(map[string]any)["code"] != "NOT_FOUND" || refused["transfer_id"] != nil {
		t.Fatalf("start of a missing file %v", refused)
	}
	back := filepath.Join(mac, "back.bin")
	pulled := e.call(t, "transfer.start", map[string]any{"direction": "pull", "host_id": "pc", "local": back, "remote": dst, "on_conflict": "rename"})
	pid := pulled["transfer_id"].(string)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		tr = e.call(t, "transfer.get", map[string]any{"transfer_id": pid})
		if tr["state"] == "done" || tr["state"] == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pull %v", tr)
		}
	}
	if b, err := os.ReadFile(back); tr["state"] != "done" || err != nil || string(b) != string(data) {
		t.Fatalf("pull %v, %v", tr, err)
	}
	entries := e.call(t, "timeline.list", map[string]any{"host_id": "pc", "limit": 20})["entries"].([]any)
	var summaries []string
	for _, en := range entries {
		m := en.(map[string]any)
		if m["kind"] == "file" && m["client_name"] == "itest" {
			summaries = append(summaries, m["summary"].(string))
		}
	}
	joined := strings.Join(summaries, "\n")
	for _, want := range []string{"보내기 시작", "보내기 끝", "받기 시작", "받기 끝", "보내기 시작 못 함"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("timeline has no %q:\n%s", want, joined)
		}
	}
}
