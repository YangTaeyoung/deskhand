package jobs

import (
	"context"
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// bigOutputAgent serves a remote job "j1" that has exited with code 0 after
// writing total bytes to stdout; exec.read pages them like the agent, with
// eof once every byte was read.
func bigOutputAgent(total int) *scriptAgent {
	return &scriptAgent{handle: func(req proto.Message) []proto.Message {
		if req.String("op") != "exec.read" {
			return nil
		}
		off, max := req.Int("out_offset"), req.Int("max")
		if max <= 0 || max > 262144 {
			max = 262144
		}
		end := min(off+max, total)
		return []proto.Message{ok(proto.Message{"state": "exited", "code": 0, "out": strings.Repeat("a", end-off), "err": "",
			"out_next": end, "err_next": 0, "dropped": false, "eof": end == total})}
	}}
}

func TestRemoteOutputPagesToTheEndBeforeItEnds(t *testing.T) {
	const total = 600 * 1024
	e := newAgentEnv(t, bigOutputAgent(total))
	j, _ := e.r.Create("pc", KindExec, "r-1", "big", nil)
	if _, err := e.r.SetRemote(j.JobID, "j1", map[string]any{"job": "j1", "pid": 4}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cursor, got, pages := "", 0, 0
	for cursor != "end" {
		out, err := e.r.Output(ctx, j.JobID, cursor, 0)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, c := range out["chunks"].([]map[string]any) {
			got += len(c["data"].(string))
		}
		cursor = out["next_cursor"].(string)
		if cursor != "end" && out["status"] != model.StatusRunning {
			t.Fatalf("page %d: the job finished before its output ended: %v", pages, out["status"])
		}
		if pages > 10 {
			t.Fatalf("no end after %d pages, cursor %s", pages, cursor)
		}
	}
	if got != total || pages != 3 {
		t.Fatalf("read %d bytes in %d pages, want %d in 3", got, pages, total)
	}
	cur, _ := e.r.current(j.JobID)
	if cur.Status != model.StatusSucceeded {
		t.Fatalf("job after the end %+v", cur)
	}
	after, err := e.r.Output(ctx, j.JobID, "end", 0)
	if err != nil || len(after["chunks"].([]map[string]any)) != 0 || after["next_cursor"] != "end" || after["status"] != model.StatusSucceeded {
		t.Fatalf("output from the end %v, %v", after, err)
	}
}

func TestFinishedRemoteJobTheAgentForgotEnds(t *testing.T) {
	e := newAgentEnv(t, &scriptAgent{handle: func(req proto.Message) []proto.Message {
		if req.String("op") == "exec.read" {
			return []proto.Message{{"t": "res", "ok": false, "code": "NOT_FOUND", "error": "no job 'j1'; call exec.list"}}
		}
		return nil
	}})
	j, _ := e.r.Create("pc", KindExec, "r-1", "gone", nil)
	e.r.SetRemote(j.JobID, "j1", map[string]any{"job": "j1"})
	if _, err := e.r.Finish(j.JobID, model.StatusUnknown, map[string]any{"state": "lost"}); err != nil {
		t.Fatal(err)
	}
	out, err := e.r.Output(context.Background(), j.JobID, "0:0", 0)
	if err != nil || out["next_cursor"] != "end" || out["status"] != model.StatusUnknown {
		t.Fatalf("output %v, %v", out, err)
	}
}

func TestLocalJobOutputEndsOnlyWhenTheJobFinished(t *testing.T) {
	r, _ := newRegistry(t)
	ctx := context.Background()
	for _, kind := range []string{KindExec, KindInstall} {
		j, _ := r.Create("pc", kind, "", "x", nil)
		out, err := r.Output(ctx, j.JobID, "", 0)
		if err != nil || out["next_cursor"] != "" || out["status"] != model.StatusRunning {
			t.Fatalf("%s running: %v, %v", kind, out, err)
		}
		r.Finish(j.JobID, model.StatusSucceeded, map[string]any{"code": 0})
		out, err = r.Output(ctx, j.JobID, "", 0)
		if err != nil || out["next_cursor"] != "end" || out["status"] != model.StatusSucceeded {
			t.Fatalf("%s finished: %v, %v", kind, out, err)
		}
	}
}
