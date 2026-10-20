package model

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

func keys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestEnvelopeHasEveryField(t *testing.T) {
	m := Envelope{HostID: "pc", Status: StatusSucceeded, Action: Executed()}.Map(map[string]any{"code": 0})
	want := []string{"action", "artifacts", "code", "connection_generation", "error", "host_id", "job_id", "next_action", "request_id",
		"schema_version", "session_id", "status"}
	if got := keys(t, m); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys %v", got)
	}
	b, _ := json.Marshal(m)
	var back map[string]any
	_ = json.Unmarshal(b, &back)
	if back["request_id"] != nil || back["error"] != nil || back["next_action"] != nil || back["artifacts"] == nil {
		t.Fatalf("empty envelope fields %s", b)
	}
	e := apierr.New("OFFLINE", "꺼져 있습니다. 켜세요", true, apierr.NextHostConnect)
	failed := Envelope{HostID: "pc", Status: StatusFailed, Action: NotStarted(), Error: e}.Map(nil)
	if failed["next_action"] != apierr.NextHostConnect {
		t.Fatalf("next_action of a failed envelope %v", failed["next_action"])
	}
	b, _ = json.Marshal(UnknownAction())
	if string(b) != `{"executed":null,"outcome":"unknown"}` {
		t.Fatalf("unknown action %s", b)
	}
}

func TestSessionInspectIsFlat(t *testing.T) {
	locked := false
	in := SessionInspect{SessionInfo: SessionInfo{SessionID: 1, User: "bob", ConnectState: "active", Protocol: "console", Locked: &locked, InputDesktop: "Default"},
		ControlState: ControlState{Owner: OwnerNone}, HostID: "pc", Displays: []any{}, Caps: map[string]Capability{}}
	want := []string{"ai_paused", "caps", "connect_state", "connection_generation", "displays", "gen", "host_id", "input_desktop", "locked",
		"owner", "protocol", "session_id", "user"}
	if got := keys(t, in); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys %v", got)
	}
}

func TestTimesHaveMilliseconds(t *testing.T) {
	ts := time.Date(2026, 10, 20, 12, 34, 56, 789_000_000, time.FixedZone("KST", 9*3600))
	if got := Time(ts); got != "2026-10-20T03:34:56.789Z" {
		t.Fatalf("time %s", got)
	}
	j := JobFrom(store.Job{JobID: "j", HostID: "h", Kind: "exec", Status: StatusRunning, CreatedAt: ts, UpdatedAt: ts})
	if j.CreatedAt != "2026-10-20T03:34:56.789Z" || j.FinishedAt != "" {
		t.Fatalf("job %+v", j)
	}
	want := []string{"created_at", "host_id", "job_id", "kind", "status", "summary", "updated_at"}
	if got := keys(t, j); !reflect.DeepEqual(got, want) {
		t.Fatalf("job keys %v", got)
	}
}

func TestJobLeavesTheOutputOutAndCountsItsBytes(t *testing.T) {
	now := time.Now()
	j := JobFrom(store.Job{JobID: "j-1", HostID: "pc", Kind: "exec", Status: StatusSucceeded, CreatedAt: now, UpdatedAt: now,
		Result: json.RawMessage(`{"code":0,"stdout":"안녕\n","stderr":"e","timed_out":false}`)})
	var res map[string]any
	if err := json.Unmarshal(j.Result, &res); err != nil {
		t.Fatal(err)
	}
	if _, ok := res["stdout"]; ok {
		t.Fatalf("result keeps stdout: %s", j.Result)
	}
	if _, ok := res["stderr"]; ok {
		t.Fatalf("result keeps stderr: %s", j.Result)
	}
	if res["code"] != float64(0) || res["timed_out"] != false {
		t.Fatalf("result %s", j.Result)
	}
	if j.OutputBytes == nil || *j.OutputBytes != int64(len("안녕\n")+1) {
		t.Fatalf("output_bytes %v", j.OutputBytes)
	}
	plain := JobFrom(store.Job{JobID: "j-2", Kind: "install", Status: StatusSucceeded, Result: json.RawMessage(`{"host_id":"pc"}`)})
	if plain.OutputBytes != nil || string(plain.Result) != `{"host_id":"pc"}` {
		t.Fatalf("job without output %+v", plain)
	}
	if got := keys(t, plain); slices.Contains(got, "output_bytes") {
		t.Fatalf("keys %v", got)
	}
}
