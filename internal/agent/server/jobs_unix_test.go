//go:build !windows

package server

import (
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
)

func TestJobLifecycle(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	start := c.ok("exec.start", proto.Message{"cmd": "for i in 1 2 3; do echo $i; sleep 0.1; done"})
	job := start.String("job")
	if job == "" || start.Int("pid") <= 0 {
		t.Fatalf("start %v", start)
	}
	out, _, last := waitJobExited(t, c, job)
	if out != "1\n2\n3\n" || last.Int("code") != 0 {
		t.Fatalf("out %q last %v", out, last)
	}
	list := c.ok("exec.list", nil)
	jobs, _ := list["jobs"].([]any)
	found := false
	for _, j := range jobs {
		m, _ := j.(map[string]any)
		if m["job"] == job {
			found = true
			if m["state"] != "exited" || m["cmd"] == "" || m["started"] == "" {
				t.Fatalf("listed job %v", m)
			}
		}
	}
	if !found {
		t.Fatalf("job %s missing from %v", job, list)
	}
	ev := c.event("job.exited")
	if ev.String("job") != job || ev.Int("code") != 0 {
		t.Fatalf("event %v", ev)
	}
}

func TestJobStdinAndKill(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	job := c.ok("exec.start", proto.Message{"cmd": "cat"}).String("job")
	t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })
	c.ok("exec.write", proto.Message{"job": job, "data": "hi\n"})

	deadline := time.Now().Add(8 * time.Second)
	got := ""
	for got != "hi\n" {
		got += c.ok("exec.read", proto.Message{"job": job, "out_offset": len(got)}).String("out")
		if time.Now().After(deadline) {
			t.Fatalf("output %q", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := c.ok("exec.kill", proto.Message{"job": job}).String("state"); st != "exited" {
		t.Fatalf("state after kill %q", st)
	}
	res := c.call("exec.kill", proto.Message{"job": "nope"})
	if res["ok"] != false || res.String("error") != "no job 'nope'; call exec.list" {
		t.Fatalf("unknown job: %v", res)
	}
}

func TestJobStdinEOFEndsJob(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	job := c.ok("exec.start", proto.Message{"cmd": "cat"}).String("job")
	t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })
	c.ok("exec.write", proto.Message{"job": job, "data": "last\n", "eof": true})
	out, _, last := waitJobExited(t, c, job)
	if out != "last\n" || last.Int("code") != 0 {
		t.Fatalf("out %q last %v", out, last)
	}
	res := c.call("exec.write", proto.Message{"job": job, "data": "more"})
	if res["ok"] != false {
		t.Fatalf("write after exit: %v", res)
	}
}

func TestJobStartWithStdinReadsItThenEOF(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	job := c.ok("exec.start", proto.Message{"cmd": "cat", "stdin": "a\nb\n"}).String("job")
	t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })
	out, _, last := waitJobExited(t, c, job)
	if out != "a\nb\n" || last.Int("code") != 0 {
		t.Fatalf("out %q last %v", out, last)
	}
	res := c.call("exec.write", proto.Message{"job": job, "data": "more"})
	if res["ok"] != false {
		t.Fatalf("write to a job started with stdin: %v", res)
	}
}

func TestJobStartWithEmptyStdinSeesEOFAtOnce(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	job := c.ok("exec.start", proto.Message{"cmd": "cat; echo end", "stdin": ""}).String("job")
	t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })
	out, _, last := waitJobExited(t, c, job)
	if out != "end\n" || last.Int("code") != 0 {
		t.Fatalf("out %q last %v", out, last)
	}
}

func TestJobBufferDropsOldest(t *testing.T) {
	old := jobBufferBytes
	jobBufferBytes = 1024
	t.Cleanup(func() { jobBufferBytes = old })
	c, _ := newRPCClient(t, Options{})
	job := c.ok("exec.start", proto.Message{"cmd": "head -c 5000 /dev/zero | tr '\\0' 'a'"}).String("job")
	_, _, _ = waitJobExited(t, c, job)
	res := c.ok("exec.read", proto.Message{"job": job})
	if !res.Bool("dropped") || res.Int("out_next") != 5000 || len(res.String("out")) != 1024 {
		t.Fatalf("dropped=%v out_next=%d len=%d", res.Bool("dropped"), res.Int("out_next"), len(res.String("out")))
	}
}

func TestJobReadCapsMax(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	job := c.ok("exec.start", proto.Message{"cmd": "printf abcdefghij"}).String("job")
	waitJobExited(t, c, job)
	res := c.ok("exec.read", proto.Message{"job": job, "max": 4})
	if res.String("out") != "abcd" || res.Int("out_next") != 4 {
		t.Fatalf("res %v", res)
	}
}

func TestJobWriteTimesOutWhenInputIsNotRead(t *testing.T) {
	old := stdinWriteTimeout
	stdinWriteTimeout = 200 * time.Millisecond
	t.Cleanup(func() { stdinWriteTimeout = old })
	c, _ := newRPCClient(t, Options{})
	job := c.ok("exec.start", proto.Message{"cmd": "sleep 30"}).String("job")
	t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })
	started := time.Now()
	res := c.call("exec.write", proto.Message{"job": job, "data": strings.Repeat("x", 512<<10)})
	if time.Since(started) > 2*time.Second {
		t.Fatalf("write took %v", time.Since(started))
	}
	if res["ok"] != false || res.String("error") != "job '"+job+"' is not reading its input; the data may still reach it later, so exec.kill it or wait before writing again" {
		t.Fatalf("res %v", res)
	}
	if st := c.ok("exec.kill", proto.Message{"job": job}).String("state"); st != "exited" {
		t.Fatalf("state %q", st)
	}
}

func TestExecReadWithASmallMaxAdvancesOverMultibyteCharacters(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	job := c.ok("exec.start", proto.Message{"cmd": "printf '\\303\\251\\342\\202\\254x'"}).String("job")
	waitJobExited(t, c, job)
	got, next := "", 0
	for i := 0; i < 10 && next < 6; i++ {
		res := c.ok("exec.read", proto.Message{"job": job, "out_offset": next, "max": 1})
		got += res.String("out")
		next = res.Int("out_next")
	}
	if got != "é€x" || next != 6 {
		t.Fatalf("read %q up to %d", got, next)
	}
}

func TestExecReadReportsEOFOnlyAfterTheLastByteOfAnExitedJob(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	const total = 600 * 1024
	job := c.ok("exec.start", proto.Message{"cmd": "head -c 614400 /dev/zero | tr '\\000' a; echo e >&2"}).String("job")
	deadline := time.Now().Add(8 * time.Second)
	for c.ok("exec.read", proto.Message{"job": job, "out_offset": total, "err_offset": 2}).String("state") != "exited" {
		if time.Now().After(deadline) {
			t.Fatal("the job did not exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
	outOff, errOff, got, reads := 0, 0, 0, 0
	for {
		res := c.ok("exec.read", proto.Message{"job": job, "out_offset": outOff, "err_offset": errOff})
		reads++
		got += len(res.String("out"))
		outOff, errOff = res.Int("out_next"), res.Int("err_next")
		if res["eof"] == true {
			break
		}
		if res["eof"] != false {
			t.Fatalf("read %d has no eof: %v", reads, res)
		}
		if reads > 10 {
			t.Fatal("no eof after 10 reads")
		}
	}
	if got != total || errOff != 2 || reads != 3 {
		t.Fatalf("read %d bytes and err_next %d in %d reads", got, errOff, reads)
	}
	if again := c.ok("exec.read", proto.Message{"job": job, "out_offset": 0, "err_offset": 2}); again["eof"] != false {
		t.Fatalf("a read from 0 reports eof: %v", again["eof"])
	}
}

func TestExecReadOfARunningJobIsNotEOF(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	job := c.ok("exec.start", proto.Message{"cmd": "sleep 30"}).String("job")
	t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })
	if res := c.ok("exec.read", proto.Message{"job": job}); res["eof"] != false || res.String("state") != "running" {
		t.Fatalf("read %v", res)
	}
}

func TestExecReadOfAnUnknownJobIsNotFound(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	if res := c.call("exec.read", proto.Message{"job": "j99"}); res["ok"] != false || res.String("code") != "NOT_FOUND" {
		t.Fatalf("read %v", res)
	}
}
