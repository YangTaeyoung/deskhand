package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func tables(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func columns(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "deskhand.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStoreMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deskhand.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"events", "hosts", "jobs", "requests", "timeline"}
	if got := tables(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("tables after the first open = %v, want %v", got, want)
	}
	for table, cols := range map[string][]string{
		"hosts":    {"host_id", "name", "address", "port", "agent_fp", "os", "arch", "ssh_alias", "created_at"},
		"jobs":     {"job_id", "host_id", "kind", "request_id", "status", "params_json", "result_json", "created_at", "updated_at"},
		"events":   {"seq", "ts", "host_id", "job_id", "kind", "payload_json"},
		"requests": {"request_id", "host_id", "op", "state", "result_json", "created_at"},
	} {
		have := map[string]bool{}
		for _, c := range columns(t, s, table) {
			have[c] = true
		}
		for _, c := range cols {
			if !have[c] {
				t.Fatalf("table %s has no column %s (has %v)", table, c, columns(t, s, table))
			}
		}
	}
	if err := s.PutHost(Host{HostID: "pc", Name: "PC", Address: "10.0.0.2", Port: 47600, AgentFP: "fp", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s.Close()
	if got := tables(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("tables after the second open = %v, want %v", got, want)
	}
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d (%v), want %d", v, err, schemaVersion)
	}
	if h, err := s.GetHost("pc"); err != nil || h.Name != "PC" {
		t.Fatalf("host after reopening = %+v, %v", h, err)
	}
}

func TestHostsRoundTrip(t *testing.T) {
	s := openTemp(t)
	created := time.Date(2026, 10, 20, 1, 2, 3, 456_000_000, time.UTC)
	h := Host{HostID: "windows-dev", Name: "사무실 PC", Address: "192.168.0.20", Port: 47600, AgentFP: "ab", OS: "windows",
		Arch: "amd64", SSHAlias: "windows", CreatedAt: created}
	if err := s.PutHost(h); err != nil {
		t.Fatal(err)
	}
	if err := s.PutHost(Host{HostID: "other", Name: "사무실 PC", CreatedAt: created}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("second host with the same name: %v", err)
	}
	got, err := s.HostByName("사무실 PC")
	if err != nil {
		t.Fatal(err)
	}
	if got != h {
		t.Fatalf("host = %+v, want %+v", got, h)
	}
	h.AgentFP = "cd"
	if err := s.UpdateHost(h); err != nil {
		t.Fatal(err)
	}
	seen := created.Add(time.Minute)
	if err := s.TouchHost("windows-dev", seen); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListHosts()
	if err != nil || len(list) != 1 || list[0].AgentFP != "cd" || !list[0].LastSeen.Equal(seen) {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := s.DeleteHost("windows-dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetHost("windows-dev"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted host: %v", err)
	}
}

func TestJobsRoundTrip(t *testing.T) {
	s := openTemp(t)
	now := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	j := Job{JobID: "j-1", HostID: "pc", Kind: "exec", RequestID: "r-1", Status: "running", Summary: "echo hi",
		Params: json.RawMessage(`{"argv":["echo","hi"]}`), CreatedAt: now, UpdatedAt: now}
	if err := s.CreateJob(j); err != nil {
		t.Fatal(err)
	}
	done := now.Add(time.Second)
	if err := s.FinishJob("j-1", "succeeded", json.RawMessage(`{"code":0}`), done); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob("j-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "succeeded" || string(got.Result) != `{"code":0}` || got.FinishedAt == nil || !got.FinishedAt.Equal(done) ||
		!got.UpdatedAt.Equal(done) || string(got.Params) != `{"argv":["echo","hi"]}` {
		t.Fatalf("job = %+v", got)
	}
	if err := s.CreateJob(Job{JobID: "j-2", HostID: "pc", Kind: "exec", Status: "running", RemoteJob: "7", CreatedAt: done, UpdatedAt: done}); err != nil {
		t.Fatal(err)
	}
	if r, err := s.JobByRemote("pc", "7"); err != nil || r.JobID != "j-2" {
		t.Fatalf("job by remote id = %+v, %v", r, err)
	}
	list, err := s.ListJobs(JobFilter{HostID: "pc", Status: "running"})
	if err != nil || len(list) != 1 || list[0].JobID != "j-2" {
		t.Fatalf("running jobs = %+v, %v", list, err)
	}
	all, err := s.ListJobs(JobFilter{})
	if err != nil || len(all) != 2 || all[0].JobID != "j-2" {
		t.Fatalf("all jobs, newest first = %+v, %v", all, err)
	}
}

func TestRequestsAndEvents(t *testing.T) {
	s := openTemp(t)
	if err := s.RequestSent("r-1", "pc", "exec.run"); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestSent("r-2", "pc", "fs.write"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRequestState("r-1", RequestUnknown, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRequestState("r-1", RequestDone, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRequest("r-1")
	if err != nil || r.State != RequestDone || string(r.Result) != `{"ok":true}` || r.Op != "exec.run" {
		t.Fatalf("request = %+v, %v", r, err)
	}
	if n, err := s.MarkSentUnknown(); err != nil || n != 1 {
		t.Fatalf("requests marked unknown = %d, %v", n, err)
	}
	if r, _ := s.GetRequest("r-2"); r.State != RequestUnknown {
		t.Fatalf("in-flight request after MarkSentUnknown = %+v", r)
	}
	seq1, err := s.AppendEvent(time.Now(), "pc", "", "connection.state", json.RawMessage(`{"state":"ready"}`))
	if err != nil {
		t.Fatal(err)
	}
	seq2, err := s.AppendEvent(time.Now(), "pc", "j-1", "jobs.changed", json.RawMessage(`{}`))
	if err != nil || seq2 != seq1+1 {
		t.Fatalf("seq %d then %d (%v)", seq1, seq2, err)
	}
}

func TestTimelineNewestFirst(t *testing.T) {
	s := openTemp(t)
	base := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	for i, host := range []string{"a", "b", "a", "a"} {
		if _, err := s.AppendTimeline(Timeline{TS: base.Add(time.Duration(i) * time.Minute), HostID: host, Actor: "ai", Kind: "exec",
			Summary: fmt.Sprintf("run %d", i), Outcome: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListTimeline(TimelineFilter{HostID: "a", Limit: 2})
	if err != nil || len(got) != 2 || got[0].Summary != "run 3" || got[1].Summary != "run 2" || string(got[0].Artifacts) != "[]" {
		t.Fatalf("first page %+v, %v", got, err)
	}
	more, err := s.ListTimeline(TimelineFilter{HostID: "a", Before: got[1].Seq})
	if err != nil || len(more) != 1 || more[0].Summary != "run 0" {
		t.Fatalf("second page %+v, %v", more, err)
	}
	since, err := s.ListTimeline(TimelineFilter{Since: base.Add(time.Minute)})
	if err != nil || len(since) != 3 {
		t.Fatalf("since %+v, %v", since, err)
	}
}

func TestPruneKeepsRecentAndUnsettled(t *testing.T) {
	s := openTemp(t)
	old := time.Now().Add(-48 * time.Hour)
	if _, err := s.AppendEvent(old, "pc", "", "connection.state", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent(time.Now(), "pc", "", "connection.state", nil); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct{ rid, state string }{{"r-done", RequestDone}, {"r-unknown", RequestUnknown}} {
		if err := s.RequestSent(r.rid, "pc", "exec.run"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetRequestState(r.rid, r.state, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Prune(time.Now().Add(-time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("events left %d, %v", n, err)
	}
	if _, err := s.GetRequest("r-done"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("settled request kept: %v", err)
	}
	if r, err := s.GetRequest("r-unknown"); err != nil || r.State != RequestUnknown {
		t.Fatalf("unknown request %+v, %v", r, err)
	}
}
