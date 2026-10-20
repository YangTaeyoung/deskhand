package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// restartableAgent is a server on DataDir dir whose listener and connection
// stop closes; another one on the same dir stands for the agent after a
// restart.
type restartableAgent struct {
	s    *Server
	c    *rpcClient
	stop func()
}

func startAgentOn(t *testing.T, dir string) *restartableAgent {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), AllowExec: true, DataDir: dir})
	go s.Serve(l)
	conn, r, w := dialAgent(t, l.Addr().String())
	expect(t, r, "hello")
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		conn.Close()
		l.Close()
		s.Close()
	}
	t.Cleanup(stop)
	return &restartableAgent{s: s, c: &rpcClient{t: t, r: r, w: w}, stop: stop}
}

// appendLine is a command that appends one line to path.
func appendLine(path string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("Add-Content -LiteralPath '%s' -Value ran", path)
	}
	return fmt.Sprintf("echo ran >> '%s'", path)
}

func sleepFor(seconds int) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("Start-Sleep -Seconds %d", seconds)
	}
	return fmt.Sprintf("sleep %d", seconds)
}

func exitWith(code int) string {
	return fmt.Sprintf("exit %d", code)
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Split(strings.TrimSpace(string(b)), "\n"))
}

func TestJournalReplaysSameRid(t *testing.T) {
	marks := filepath.Join(t.TempDir(), "marks.txt")
	a := startAgentOn(t, t.TempDir())
	req := proto.Message{"rid": "r-replay-1", "cmd": appendLine(marks)}
	first := a.c.ok("exec.run", req)
	if first.Has("replayed") || first.Int("code") != 0 {
		t.Fatalf("first run %v", first)
	}
	second := a.c.ok("exec.run", req)
	if second["replayed"] != true || second.Int("code") != 0 {
		t.Fatalf("second run %v", second)
	}
	if n := countLines(t, marks); n != 1 {
		t.Fatalf("the command ran %d times", n)
	}
	status := a.c.ok("req.status", proto.Message{"rid": "r-replay-1"})
	if status.String("state") != "done" || status.Int("code") != 0 || !status.Has("stdout") {
		t.Fatalf("req.status %v", status)
	}
}

func TestJournalInProgress(t *testing.T) {
	a := startAgentOn(t, t.TempDir())
	send(t, a.c.w, proto.Message{"t": "req", "id": 100, "op": "exec.run", "rid": "r-slow", "cmd": sleepFor(2)})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if a.c.ok("req.status", proto.Message{"rid": "r-slow"}).String("state") == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("req.status never reported the request as running")
		}
		time.Sleep(10 * time.Millisecond)
	}
	again := a.c.call("exec.run", proto.Message{"rid": "r-slow", "cmd": sleepFor(2)})
	if again["ok"] != false || again.String("code") != "IN_PROGRESS" || again.String("error") == "" {
		t.Fatalf("the second request %v", again)
	}
	for {
		m := expect(t, a.c.r, "res")
		if m.Int("id") == 100 {
			if m["ok"] != true {
				t.Fatalf("the first request %v", m)
			}
			break
		}
	}
}

func TestReqStatusUnknownRid(t *testing.T) {
	a := startAgentOn(t, t.TempDir())
	res := a.c.ok("req.status", proto.Message{"rid": "r-never-sent"})
	if res.String("state") != "unknown" {
		t.Fatalf("req.status %v", res)
	}
}

func TestJournalSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	a := startAgentOn(t, dir)
	job := a.c.ok("exec.start", proto.Message{"rid": "r-start-3", "cmd": exitWith(3)}).String("job")
	if _, _, last := waitJobExited(t, a.c, job); last.Int("code") != 3 {
		t.Fatalf("the job ended with %v", last)
	}
	a.stop()

	b := startAgentOn(t, dir)
	jobs, _ := b.c.ok("exec.list", nil)["jobs"].([]any)
	var listed map[string]any
	for _, j := range jobs {
		if m, _ := j.(map[string]any); m["job"] == job {
			listed = m
		}
	}
	if listed == nil || listed["state"] != "exited" || listed["code"] != float64(3) {
		t.Fatalf("exec.list after the restart %v", jobs)
	}
	replay := b.c.ok("exec.start", proto.Message{"rid": "r-start-3", "cmd": exitWith(3)})
	if replay["replayed"] != true || replay.String("job") != job {
		t.Fatalf("exec.start with the same rid after the restart %v", replay)
	}
	next := b.c.ok("exec.start", proto.Message{"cmd": exitWith(0)}).String("job")
	if next == job {
		t.Fatalf("a new job after the restart reused the id %s", job)
	}
	waitJobExited(t, b.c, next)
}

func TestJournalReplaysAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	marks := filepath.Join(t.TempDir(), "marks.txt")
	a := startAgentOn(t, dir)
	a.c.ok("exec.run", proto.Message{"rid": "r-across", "cmd": appendLine(marks)})
	a.stop()

	b := startAgentOn(t, dir)
	res := b.c.ok("exec.run", proto.Message{"rid": "r-across", "cmd": appendLine(marks)})
	if res["replayed"] != true {
		t.Fatalf("exec.run after the restart %v", res)
	}
	if n := countLines(t, marks); n != 1 {
		t.Fatalf("the command ran %d times", n)
	}
}

func TestAJobRunningWhenTheAgentStoppedIsListedAsLost(t *testing.T) {
	dir := t.TempDir()
	a := startAgentOn(t, dir)
	job := a.c.ok("exec.start", proto.Message{"cmd": sleepFor(30)}).String("job")
	t.Cleanup(func() { a.c.call("exec.kill", proto.Message{"job": job}) })

	b := startAgentOn(t, dir)
	jobs, _ := b.c.ok("exec.list", nil)["jobs"].([]any)
	var listed map[string]any
	for _, j := range jobs {
		if m, _ := j.(map[string]any); m["job"] == job {
			listed = m
		}
	}
	if listed == nil || listed["state"] != "lost" || listed["code"] != nil {
		t.Fatalf("exec.list after the restart %v", jobs)
	}
	read := b.c.ok("exec.read", proto.Message{"job": job})
	if read.String("state") != "lost" || read.Has("code") {
		t.Fatalf("exec.read of a lost job %v", read)
	}
}

func TestAJobsOutputIsReadAfterRestart(t *testing.T) {
	dir := t.TempDir()
	a := startAgentOn(t, dir)
	job := a.c.ok("exec.start", proto.Message{"cmd": "echo kept"}).String("job")
	out, _, _ := waitJobExited(t, a.c, job)
	a.stop()

	b := startAgentOn(t, dir)
	again, _, last := waitJobExited(t, b.c, job)
	if again != out || !strings.Contains(again, "kept") || last.Int("out_next") != len(out) {
		t.Fatalf("output after the restart %q (before %q), last %v", again, out, last)
	}
}

func TestAnInterruptedRequestIsNotRunAgain(t *testing.T) {
	dir := t.TempDir()
	marks := filepath.Join(t.TempDir(), "marks.txt")
	if err := os.MkdirAll(filepath.Join(dir, "journal"), 0o700); err != nil {
		t.Fatal(err)
	}
	record, _ := json.Marshal(map[string]any{"rid": "r-cut", "op": "exec.run", "state": "running", "started": time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(dir, "journal", "r-cut.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	a := startAgentOn(t, dir)
	res := a.c.call("exec.run", proto.Message{"rid": "r-cut", "cmd": appendLine(marks)})
	if res["ok"] != false || res.String("code") != "EXECUTION_OUTCOME_UNKNOWN" || res["replayed"] != true {
		t.Fatalf("exec.run of an interrupted rid %v", res)
	}
	if n := countLines(t, marks); n != 0 {
		t.Fatalf("the command ran %d times", n)
	}
	status := a.c.call("req.status", proto.Message{"rid": "r-cut"})
	if status.String("state") != "done" || status["ok"] != false || status.String("code") != "EXECUTION_OUTCOME_UNKNOWN" {
		t.Fatalf("req.status %v", status)
	}
}

func TestJournalForgetsRequestsAfter24Hours(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "journal"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Hour).UTC()
	record, _ := json.Marshal(map[string]any{"rid": "r-old", "op": "exec.run", "state": "done", "started": old, "finished": old,
		"res": map[string]any{"ok": true, "code": 0}})
	path := filepath.Join(dir, "journal", "r-old.json")
	if err := os.WriteFile(path, record, 0o600); err != nil {
		t.Fatal(err)
	}
	a := startAgentOn(t, dir)
	if st := a.c.ok("req.status", proto.Message{"rid": "r-old"}).String("state"); st != "unknown" {
		t.Fatalf("a request of 25 hours ago is %s", st)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the record of 25 hours ago is still on disk: %v", err)
	}
}

func TestARidThatIsNotAPlainNameIsRefused(t *testing.T) {
	marks := filepath.Join(t.TempDir(), "marks.txt")
	a := startAgentOn(t, t.TempDir())
	for _, rid := range []string{"../escape", "a/b", "CON", strings.Repeat("x", 129)} {
		res := a.c.call("exec.run", proto.Message{"rid": rid, "cmd": appendLine(marks)})
		if res["ok"] != false || res.String("code") != "INVALID_ARGUMENT" {
			t.Fatalf("rid %q: %v", rid, res)
		}
	}
	if n := countLines(t, marks); n != 0 {
		t.Fatalf("the command ran %d times", n)
	}
}

func TestARidReusedForAnotherOpIsRefused(t *testing.T) {
	dir := t.TempDir()
	a := startAgentOn(t, t.TempDir())
	a.c.ok("fs.mkdir", proto.Message{"rid": "r-mixed", "path": filepath.Join(dir, "made")})
	res := a.c.call("fs.delete", proto.Message{"rid": "r-mixed", "path": filepath.Join(dir, "made")})
	if res["ok"] != false || res.String("code") != "INVALID_ARGUMENT" {
		t.Fatalf("fs.delete with the rid of an fs.mkdir %v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "made")); err != nil {
		t.Fatalf("the folder was removed: %v", err)
	}
}

func TestARequestWithoutRidIsNotJournaled(t *testing.T) {
	dir := t.TempDir()
	marks := filepath.Join(t.TempDir(), "marks.txt")
	a := startAgentOn(t, dir)
	a.c.ok("exec.run", proto.Message{"cmd": appendLine(marks)})
	a.c.ok("exec.run", proto.Message{"cmd": appendLine(marks)})
	if n := countLines(t, marks); n != 2 {
		t.Fatalf("the command ran %d times", n)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "journal"))
	if len(entries) != 0 {
		t.Fatalf("the journal holds %d records", len(entries))
	}
}

func TestTheAgentJournalsExactlyTheWireJournaledOps(t *testing.T) {
	a := startAgentOn(t, t.TempDir())
	for i, op := range append(wire.JournaledOps(), "exec.list", "exec.read", "fs.list", "fs.stat", "proc.list", "ping") {
		rid := fmt.Sprintf("r-op-%d", i)
		a.c.call(op, proto.Message{"rid": rid})
		want := "unknown"
		if wire.Journaled(op) {
			want = "done"
		}
		if st := a.c.call("req.status", proto.Message{"rid": rid}).String("state"); st != want {
			t.Errorf("req.status after %s is %s, want %s", op, st, want)
		}
	}
}

func TestAReadOnlyOpWithRidIsNotJournaled(t *testing.T) {
	dir := t.TempDir()
	a := startAgentOn(t, dir)
	a.c.ok("exec.list", proto.Message{"rid": "r-read"})
	if res := a.c.ok("exec.list", proto.Message{"rid": "r-read"}); res.Has("replayed") {
		t.Fatalf("exec.list was replayed: %v", res)
	}
	if st := a.c.ok("req.status", proto.Message{"rid": "r-read"}).String("state"); st != "unknown" {
		t.Fatalf("req.status of a read-only op is %s", st)
	}
}

func TestAReplayAfterAFailedJournalWriteCarriesTheAnswer(t *testing.T) {
	marks := filepath.Join(t.TempDir(), "marks.txt")
	a := startAgentOn(t, t.TempDir())
	j := a.s.journal
	j.mu.Lock()
	write := j.writeRecord
	j.writeRecord = func(rec journalRecord) error {
		if rec.State == journalDone {
			return errors.New("the disk is full")
		}
		return write(rec)
	}
	j.mu.Unlock()
	req := proto.Message{"rid": "r-unwritten", "cmd": appendLine(marks)}
	first := a.c.ok("exec.run", req)
	second := a.c.ok("exec.run", req)
	if second["replayed"] != true || second.Int("code") != first.Int("code") || !second.Has("stdout") {
		t.Fatalf("replay after a failed write %v", second)
	}
	status := a.c.ok("req.status", proto.Message{"rid": "r-unwritten"})
	if status.String("state") != "done" || !status.Has("stdout") {
		t.Fatalf("req.status after a failed write %v", status)
	}
	if n := countLines(t, marks); n != 1 {
		t.Fatalf("the command ran %d times", n)
	}
}

func TestAFinishedEntryWhoseRecordHasNoAnswerIsAnsweredAsUnknownOutcome(t *testing.T) {
	dir := t.TempDir()
	a := startAgentOn(t, dir)
	a.c.ok("exec.run", proto.Message{"rid": "r-overwritten", "cmd": "echo x"})
	record, _ := json.Marshal(map[string]any{"rid": "r-overwritten", "op": "exec.run", "state": "running", "started": time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(dir, "journal", "r-overwritten.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	res := a.c.call("exec.run", proto.Message{"rid": "r-overwritten", "cmd": "echo x"})
	if res["ok"] != false || res.String("code") != "EXECUTION_OUTCOME_UNKNOWN" || res["replayed"] != true {
		t.Fatalf("replay of a record without an answer %v", res)
	}
}

func TestAPresentButInvalidRidIsRefusedOnEveryOp(t *testing.T) {
	marks := filepath.Join(t.TempDir(), "marks.txt")
	a := startAgentOn(t, t.TempDir())
	for _, rid := range []any{5, "", "../x", nil, true} {
		for _, op := range []string{"ping", "exec.list", "exec.run", "req.status"} {
			res := a.c.call(op, proto.Message{"rid": rid, "cmd": appendLine(marks)})
			if res["ok"] != false || res.String("code") != "INVALID_ARGUMENT" || res.Has("pong") {
				t.Fatalf("%s with rid %#v: %v", op, rid, res)
			}
		}
	}
	if n := countLines(t, marks); n != 0 {
		t.Fatalf("the command ran %d times", n)
	}
	if res := a.c.ok("ping", proto.Message{"rid": "r-valid"}); res["pong"] != true {
		t.Fatalf("ping with a valid rid %v", res)
	}
}
