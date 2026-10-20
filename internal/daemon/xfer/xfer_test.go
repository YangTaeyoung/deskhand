package xfer_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/xfer"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const mib = 1 << 20

// checkDone fails the test unless tr ended done with outcome.
func checkDone(t *testing.T, tr model.Transfer, outcome string) {
	t.Helper()
	if tr.State != "done" || tr.Outcome != outcome || tr.Error != nil || tr.FilesDone != tr.FilesTotal || tr.BytesDone != tr.BytesTotal ||
		tr.RateBPS != 0 {
		t.Fatalf("transfer is not done with %s: %s", outcome, describe(tr))
	}
}

func TestPushPullRoundTrip(t *testing.T) {
	h := newHarness(t, xfer.Hooks{})
	mtime := time.Date(2024, 7, 8, 9, 10, 11, 123456789, time.UTC)
	files := map[string][]byte{
		"note.txt": []byte("한글 텍스트\r\nsecond line\n"),
		"data.bin": randomBytes(t, 9*mib+7),
		"empty":    {},
	}
	for name, data := range files {
		src := filepath.Join(h.mac, name)
		writeFile(t, src, data)
		if err := os.Chtimes(src, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(h.agent.home, name)
		tr := h.waitFinished(h.start("push", src, dst, "").TransferID)
		checkDone(t, tr, "written")
		f := fileOf(t, tr, name)
		if tr.FilesTotal != 1 || tr.BytesTotal != int64(len(data)) || f.SHA256 != sum(data) || f.Outcome != "written" || f.Path != dst ||
			tr.Direction != "push" || tr.Local != src || tr.Remote != dst || tr.OnConflict != "fail" {
			t.Fatalf("pushed %s: %s", name, describe(tr))
		}
		sameBytes(t, "pushed "+name, readFile(t, dst), data)
		if fi, err := os.Stat(dst); err != nil || !fi.ModTime().Equal(mtime) {
			t.Fatalf("modification time of the pushed %s: %v, %v", name, fi.ModTime(), err)
		}
		back := filepath.Join(h.mac, "back", name)
		if err := os.MkdirAll(filepath.Dir(back), 0o755); err != nil {
			t.Fatal(err)
		}
		tr = h.waitFinished(h.start("pull", back, dst, "fail").TransferID)
		checkDone(t, tr, "written")
		if f := fileOf(t, tr, name); f.SHA256 != sum(data) || f.Path != back {
			t.Fatalf("pulled %s: %s", name, describe(tr))
		}
		sameBytes(t, "pulled "+name, readFile(t, back), data)
		if fi, err := os.Stat(back); err != nil || !fi.ModTime().Equal(mtime) {
			t.Fatalf("modification time of the pulled %s: %v, %v", name, fi.ModTime(), err)
		}
	}
	if p := append(parts(t, h.mac), parts(t, h.agent.home)...); len(p) != 0 {
		t.Fatalf("part files left: %v", p)
	}

	src := filepath.Join(h.mac, "tree")
	var total int64
	for i := range 1000 {
		data := []byte(fmt.Sprintf("file %d\n%s", i, strings.Repeat("x", i%97)))
		total += int64(len(data))
		writeFile(t, filepath.Join(src, fmt.Sprintf("d%02d", i%10), fmt.Sprintf("f%04d.txt", i)), data)
	}
	if err := os.MkdirAll(filepath.Join(src, "empty-dir", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := tree(t, src)
	dst := filepath.Join(h.agent.home, "tree")
	pushed := h.waitFinished(h.start("push", src, dst, "fail").TransferID)
	checkDone(t, pushed, "written")
	if pushed.FilesTotal != 1000 || pushed.BytesTotal != total || len(pushed.Files) != 1000 {
		t.Fatalf("pushed folder: %s", describe(pushed))
	}
	if got := tree(t, dst); len(got) != len(want) {
		t.Fatalf("the pushed folder has %d entries, want %d", len(got), len(want))
	} else {
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("pushed %s = %q, want %q", k, got[k], v)
			}
		}
	}
	back := filepath.Join(h.mac, "tree-back")
	pulled := h.waitFinished(h.start("pull", back, dst, "fail").TransferID)
	checkDone(t, pulled, "written")
	if got := tree(t, back); len(got) != len(want) {
		t.Fatalf("the pulled folder has %d entries, want %d", len(got), len(want))
	} else {
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("pulled %s = %q, want %q", k, got[k], v)
			}
		}
	}

	revs := h.updates(pulled.TransferID)
	if len(revs) < 2 || revs[len(revs)-1].State != "done" {
		t.Fatalf("transfer.updated of the pulled folder: %d events", len(revs))
	}
	for i := 1; i < len(revs); i++ {
		if revs[i].Rev <= revs[i-1].Rev || revs[i].UpdatedAt == "" {
			t.Fatalf("event %d has rev %d after %d (updated_at %q)", i, revs[i].Rev, revs[i-1].Rev, revs[i].UpdatedAt)
		}
		prev, _ := time.Parse(time.RFC3339Nano, revs[i-1].UpdatedAt)
		at, _ := time.Parse(time.RFC3339Nano, revs[i].UpdatedAt)
		if revs[i].State == revs[i-1].State && at.Sub(prev) < 249*time.Millisecond {
			t.Fatalf("events %d and %d of the same state came %s apart", i-1, i, at.Sub(prev))
		}
	}
	if last := revs[len(revs)-1]; last.Rev != h.get(pulled.TransferID).Rev {
		t.Fatalf("the last event has rev %d, the transfer %d", last.Rev, h.get(pulled.TransferID).Rev)
	}
	var started, ended int
	for _, e := range h.timeline() {
		if e.Kind != "file" || e.Actor != "human" || e.ClientID != "c-test" {
			continue
		}
		switch {
		case strings.Contains(e.Summary, "시작"):
			started++
		case e.Outcome == "ok":
			ended++
		}
	}
	if started != 8 || ended != 8 {
		t.Fatalf("timeline has %d start and %d end entries, want 8 each: %+v", started, ended, h.timeline())
	}
	list, err := h.manager().List("pc")
	if err != nil || len(list) != 8 || list[0].TransferID != pulled.TransferID {
		t.Fatalf("list %d transfers, %v", len(list), err)
	}
}

func TestTransferResumesAfterReconnectAndDaemonRestart(t *testing.T) {
	for _, direction := range []string{"push", "pull"} {
		t.Run(direction, func(t *testing.T) {
			first, second := newGate(), newGate()
			var mu sync.Mutex
			var opened []int64
			run := 0
			hooks := xfer.Hooks{
				Opened: func(_, rel string, offset int64) {
					mu.Lock()
					opened = append(opened, offset)
					mu.Unlock()
				},
				Wrote: func(_, rel string, done int64) {
					mu.Lock()
					n := run
					mu.Unlock()
					switch {
					case n == 0 && done >= 4*mib:
						first.wait()
					case n == 1 && done >= 8*mib:
						second.wait()
					}
				},
			}
			h := newHarness(t, hooks)
			data := randomBytes(t, 16*mib+3)
			local, remote := filepath.Join(h.mac, "big.bin"), filepath.Join(h.agent.home, "big.bin")
			part, receiving := remote, h.agent.home
			if direction == "push" {
				writeFile(t, local, data)
			} else {
				writeFile(t, remote, data)
				part, receiving = local, h.mac
			}
			id := h.start(direction, local, remote, "").TransferID
			<-first.reached
			waitPart(t, receiving, 4*mib)
			if p := parts(t, h.mac); direction == "pull" && (len(p) != 1 || !hidden(p[0])) {
				t.Fatalf("the part file of the pull %v is not hidden", p)
			}
			h.agent.halt()
			mu.Lock()
			run = 1
			mu.Unlock()
			first.release()
			h.wait(id, 20*time.Second, "waiting for the PC", func(tr model.Transfer) bool { return tr.State == "queued" })
			h.agent.start()
			<-second.reached
			waitPart(t, receiving, 8*mib)
			before := h.get(id)
			mu.Lock()
			run = 2
			mu.Unlock()
			closed := make(chan struct{})
			go func() {
				h.closeDaemon()
				close(closed)
			}()
			second.release()
			<-closed
			h.open()
			tr := h.waitFinished(id)
			checkDone(t, tr, "written")
			if tr.Rev <= before.Rev {
				t.Fatalf("rev %d after the daemon restart, %d before", tr.Rev, before.Rev)
			}
			sameBytes(t, "the transferred file", readFile(t, part), data)
			mu.Lock()
			defer mu.Unlock()
			if len(opened) < 3 || opened[0] != 0 || opened[1] < 4*mib || opened[len(opened)-1] < 8*mib {
				t.Fatalf("streams opened at offsets %v, want 0, then from the bytes received", opened)
			}
			if p := append(parts(t, h.mac), parts(t, h.agent.home)...); len(p) != 0 {
				t.Fatalf("part files left: %v", p)
			}
		})
	}
}

// waitPart waits until a part file below root holds at least n bytes.
func waitPart(t *testing.T, root string, n int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, p := range parts(t, root) {
			if fi, err := os.Stat(p); err == nil && fi.Size() >= n {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no part file below %s holds %d bytes", root, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPausedTransferStaysPausedAcrossDaemonRestart(t *testing.T) {
	g := newGate()
	h := newHarness(t, xfer.Hooks{Wrote: func(_, _ string, done int64) {
		if done >= 4*mib {
			g.wait()
		}
	}})
	data := randomBytes(t, 12*mib)
	src, dst := filepath.Join(h.mac, "p.bin"), filepath.Join(h.agent.home, "p.bin")
	writeFile(t, src, data)
	id := h.start("push", src, dst, "").TransferID
	<-g.reached
	res := make(chan model.Transfer, 1)
	go func() {
		tr, err := h.manager().Pause(id, human)
		if err != nil {
			t.Error(err)
		}
		res <- tr
	}()
	g.release()
	if tr := <-res; tr.State != "paused" || tr.RateBPS != 0 {
		t.Fatalf("paused transfer: %s", describe(tr))
	}
	h.closeDaemon()
	h.open()
	time.Sleep(time.Second)
	if tr := h.get(id); tr.State != "paused" {
		t.Fatalf("after the daemon restart: %s", describe(tr))
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("the paused transfer wrote its destination")
	}
	if tr, err := h.manager().Resume(id, human); err != nil || (tr.State != "queued" && tr.State != "running") {
		t.Fatalf("resume: %s, %v", describe(tr), err)
	}
	checkDone(t, h.waitFinished(id), "written")
	sameBytes(t, "the resumed file", readFile(t, dst), data)
	if _, err := h.manager().Pause(id, human); err == nil {
		t.Fatal("a finished transfer was paused")
	}
}

func TestSourceChangedMidTransferConflicts(t *testing.T) {
	for _, direction := range []string{"push", "pull"} {
		t.Run(direction, func(t *testing.T) {
			var once sync.Once
			var source string
			h := newHarness(t, xfer.Hooks{Wrote: func(_, _ string, done int64) {
				once.Do(func() {
					f, err := os.OpenFile(source, os.O_WRONLY, 0)
					if err != nil {
						t.Error(err)
						return
					}
					f.WriteAt([]byte("changed while it was sent"), 10*mib)
					f.Close()
					later := time.Now().Add(time.Hour)
					os.Chtimes(source, later, later)
				})
			}})
			data := randomBytes(t, 12*mib)
			local, remote := filepath.Join(h.mac, "c.bin"), filepath.Join(h.agent.home, "c.bin")
			target, receiving := remote, h.agent.home
			if direction == "push" {
				source = local
			} else {
				source, target, receiving = remote, local, h.mac
			}
			writeFile(t, source, data)
			writeFile(t, target, []byte("the old destination"))
			tr := h.waitFinished(h.start(direction, local, remote, "replace").TransferID)
			f := fileOf(t, tr, "c.bin")
			if tr.State != "failed" || tr.Outcome != "failed" || tr.Error == nil || tr.Error.Code != wire.CodeTransferConflict ||
				f.State != "failed" || f.Error == nil || f.Error.Code != wire.CodeTransferConflict {
				t.Fatalf("transfer of a changed source: %s", describe(tr))
			}
			if got := string(readFile(t, target)); got != "the old destination" {
				t.Fatalf("the destination became %d bytes", len(got))
			}
			if p := parts(t, receiving); len(p) != 0 {
				t.Fatalf("part files left: %v", p)
			}
		})
	}
}

func TestMoveKeepsSourceWhenDeleteFails(t *testing.T) {
	h := newHarness(t, xfer.Hooks{})
	t.Run("move_push file", func(t *testing.T) {
		locked := filepath.Join(h.mac, "locked")
		src := filepath.Join(locked, "a.txt")
		writeFile(t, src, []byte("moved a"))
		if err := os.Chmod(locked, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
		dst := filepath.Join(h.agent.home, "a.txt")
		tr := h.waitFinished(h.start("move_push", src, dst, "").TransferID)
		if tr.State != "done" || tr.Outcome != "copied_source_retained" || tr.Error == nil || tr.Error.Message == "" {
			t.Fatalf("move with an undeletable source: %s", describe(tr))
		}
		if string(readFile(t, dst)) != "moved a" || string(readFile(t, src)) != "moved a" {
			t.Fatal("the destination or the source is not there")
		}
	})
	t.Run("move_push folder", func(t *testing.T) {
		src := filepath.Join(h.mac, "dir")
		writeFile(t, filepath.Join(src, "one.txt"), []byte("1"))
		writeFile(t, filepath.Join(src, "sub", "two.txt"), []byte("2"))
		sub := filepath.Join(src, "sub")
		if err := os.Chmod(sub, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(sub, 0o755) })
		dst := filepath.Join(h.agent.home, "dir")
		tr := h.waitFinished(h.start("move_push", src, dst, "").TransferID)
		if tr.State != "done" || tr.Outcome != "copied_source_retained" || tr.Error == nil {
			t.Fatalf("folder move with an undeletable file: %s", describe(tr))
		}
		if got := tree(t, src); got["one.txt"] != "1" || got["sub/two.txt"] != "2" {
			t.Fatalf("the source folder lost files: %v", got)
		}
		if got := tree(t, dst); got["one.txt"] != "1" || got["sub/two.txt"] != "2" {
			t.Fatalf("the destination folder %v", got)
		}
	})
	t.Run("move_push deletes a source it can delete", func(t *testing.T) {
		src := filepath.Join(h.mac, "free")
		writeFile(t, filepath.Join(src, "x.txt"), []byte("x"))
		writeFile(t, filepath.Join(src, "y", "z.txt"), []byte("z"))
		dst := filepath.Join(h.agent.home, "free")
		checkDone(t, h.waitFinished(h.start("move_push", src, dst, "").TransferID), "written")
		if _, err := os.Lstat(src); !os.IsNotExist(err) {
			t.Fatalf("the moved source folder is still there: %v", err)
		}
		if got := tree(t, dst); got["x.txt"] != "x" || got["y/z.txt"] != "z" {
			t.Fatalf("the destination folder %v", got)
		}
	})
	t.Run("move_pull", func(t *testing.T) {
		src := filepath.Join(h.agent.home, "r.txt")
		writeFile(t, src, []byte("remote"))
		dst := filepath.Join(h.mac, "r.txt")
		tr := h.waitFinished(h.start("move_pull", dst, src, "").TransferID)
		if tr.State != "done" || tr.Outcome != "copied_source_retained" || tr.Error == nil || tr.Error.Code != wire.CodeCapabilityUnsupported {
			t.Fatalf("move from a PC without a Recycle Bin: %s", describe(tr))
		}
		if string(readFile(t, dst)) != "remote" || string(readFile(t, src)) != "remote" {
			t.Fatal("the destination or the source is not there")
		}
	})
	t.Run("move with a skipped file keeps the source", func(t *testing.T) {
		src := filepath.Join(h.mac, "s.txt")
		writeFile(t, src, []byte("new"))
		dst := filepath.Join(h.agent.home, "s.txt")
		writeFile(t, dst, []byte("old"))
		tr := h.waitFinished(h.start("move_push", src, dst, "skip").TransferID)
		checkDone(t, tr, "skipped")
		if string(readFile(t, src)) != "new" || string(readFile(t, dst)) != "old" {
			t.Fatal("a skipped move changed the source or the destination")
		}
	})
}

func TestConflictPoliciesAcrossFolder(t *testing.T) {
	h := newHarness(t, xfer.Hooks{})
	for _, direction := range []string{"push", "pull"} {
		for _, policy := range []string{"fail", "replace", "skip", "rename"} {
			t.Run(direction+" "+policy, func(t *testing.T) {
				name := direction + "-" + policy
				local, remote := filepath.Join(h.mac, name), filepath.Join(h.agent.home, name)
				src, dst := local, remote
				if direction == "pull" {
					src, dst = remote, local
				}
				writeFile(t, filepath.Join(src, "a.txt"), []byte("new a"))
				writeFile(t, filepath.Join(src, "b.txt"), []byte("new b"))
				writeFile(t, filepath.Join(src, "sub", "c.txt"), []byte("new c"))
				writeFile(t, filepath.Join(dst, "a.txt"), []byte("old a"))
				writeFile(t, filepath.Join(dst, "sub", "c.txt"), []byte("old c"))
				tr := h.waitFinished(h.start(direction, local, remote, policy).TransferID)
				got := tree(t, dst)
				a, b, c := fileOf(t, tr, "a.txt"), fileOf(t, tr, "b.txt"), fileOf(t, tr, "sub/c.txt")
				if b.State != "done" || b.Outcome != "written" || got["b.txt"] != "new b" {
					t.Fatalf("the new file: %s", describe(tr))
				}
				switch policy {
				case "fail":
					if tr.State != "failed" || tr.Outcome != "partial" || a.State != "failed" || a.Error == nil ||
						a.Error.Code != wire.CodeTransferConflict || c.State != "failed" || got["a.txt"] != "old a" || got["sub/c.txt"] != "old c" {
						t.Fatalf("fail: %s %v", describe(tr), got)
					}
				case "replace":
					checkDone(t, tr, "written")
					if got["a.txt"] != "new a" || got["sub/c.txt"] != "new c" || a.Outcome != "written" {
						t.Fatalf("replace: %s %v", describe(tr), got)
					}
				case "skip":
					checkDone(t, tr, "written")
					if got["a.txt"] != "old a" || got["sub/c.txt"] != "old c" || a.Outcome != "skipped" || c.Outcome != "skipped" {
						t.Fatalf("skip: %s %v", describe(tr), got)
					}
				case "rename":
					checkDone(t, tr, "written")
					if got["a.txt"] != "old a" || got["a (1).txt"] != "new a" || got["sub/c.txt"] != "old c" || got["sub/c (1).txt"] != "new c" ||
						a.Outcome != "renamed" || a.Path != filepath.Join(dst, "a (1).txt") {
						t.Fatalf("rename: %s %v", describe(tr), got)
					}
					again := h.waitFinished(h.start(direction, local, remote, policy).TransferID)
					if got := tree(t, dst); got["a (2).txt"] != "new a" || fileOf(t, again, "b.txt").Path != filepath.Join(dst, "b (1).txt") {
						t.Fatalf("second rename: %s %v", describe(again), got)
					}
				}
			})
		}
	}
	if p := append(parts(t, h.mac), parts(t, h.agent.home)...); len(p) != 0 {
		t.Fatalf("part files left: %v", p)
	}
}

func TestCancelKeepsCompletedFiles(t *testing.T) {
	for _, direction := range []string{"push", "pull"} {
		t.Run(direction, func(t *testing.T) {
			g := newGate()
			h := newHarness(t, xfer.Hooks{Wrote: func(_, rel string, done int64) {
				if rel == "big.bin" && done >= 4*mib {
					g.wait()
				}
			}})
			local, remote := filepath.Join(h.mac, "set"), filepath.Join(h.agent.home, "set")
			src, dst := local, remote
			if direction == "pull" {
				src, dst = remote, local
			}
			big := randomBytes(t, 16*mib)
			writeFile(t, filepath.Join(src, "one.txt"), []byte("one"))
			writeFile(t, filepath.Join(src, "two.txt"), []byte("two"))
			writeFile(t, filepath.Join(src, "big.bin"), big)
			id := h.start(direction, local, remote, "").TransferID
			<-g.reached
			h.wait(id, 20*time.Second, "done with the small files", func(tr model.Transfer) bool { return tr.FilesDone == 2 })
			res := make(chan model.Transfer, 1)
			go func() {
				tr, err := h.manager().Cancel(id, human)
				if err != nil {
					t.Error(err)
				}
				res <- tr
			}()
			g.release()
			tr := <-res
			if tr.State != "cancelled" || tr.FilesDone != 2 || fileOf(t, tr, "big.bin").State != "cancelled" || fileOf(t, tr, "one.txt").State != "done" {
				t.Fatalf("cancelled transfer: %s", describe(tr))
			}
			got := tree(t, dst)
			if got["one.txt"] != "one" || got["two.txt"] != "two" {
				t.Fatalf("the finished files are not kept: %v", got)
			}
			if _, ok := got["big.bin"]; ok {
				t.Fatal("the cancelled file reached its destination")
			}
			if p := parts(t, dst); len(p) != 0 {
				t.Fatalf("part files left: %v", p)
			}
			if tr, err := h.manager().Resume(id, human); err != nil || tr.State == "cancelled" {
				t.Fatalf("resume of the cancelled transfer: %s, %v", describe(tr), err)
			}
			checkDone(t, h.waitFinished(id), "written")
			sameBytes(t, "the file after the resume", readFile(t, filepath.Join(dst, "big.bin")), big)
		})
	}
}

func TestCommitWithLostAnswerAndReceiptSettlesByHash(t *testing.T) {
	h := newHarnessDropping(t, xfer.Hooks{}, "xfer.commit")
	reopen := h.holdReconnects()
	data := randomBytes(t, 3*mib)
	src, dst := filepath.Join(h.mac, "k.bin"), filepath.Join(h.agent.home, "k.bin")
	writeFile(t, src, data)
	id := h.start("push", src, dst, "fail").TransferID
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err := os.Stat(dst)
		if err == nil && h.hostState() == conn.StateReconnecting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the commit did not land with its answer dropped: %v, host %s", err, h.hostState())
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.agent.halt()
	for _, dir := range []string{"journal", "receipts"} {
		entries, _ := os.ReadDir(filepath.Join(h.agent.dataDir, dir))
		for _, e := range entries {
			os.Remove(filepath.Join(h.agent.dataDir, dir, e.Name()))
		}
	}
	h.agent.start()
	reopen()
	tr := h.waitFinished(id)
	checkDone(t, tr, "written")
	sameBytes(t, "the committed file", readFile(t, dst), data)
}

func TestStartRefusesWhatCannotStart(t *testing.T) {
	h := newHarness(t, xfer.Hooks{})
	ctx := context.Background()
	start := func(p xfer.StartParams) (xfer.Started, error) {
		p.HostID = "pc"
		return h.manager().Start(ctx, p, human)
	}
	for _, p := range []xfer.StartParams{
		{Direction: "sideways", Local: filepath.Join(h.mac, "x"), Remote: filepath.Join(h.agent.home, "x")},
		{Direction: "push", Local: "relative/x", Remote: filepath.Join(h.agent.home, "x")},
		{Direction: "push", Local: filepath.Join(h.mac, "x"), Remote: ""},
		{Direction: "push", Local: filepath.Join(h.mac, "x"), Remote: filepath.Join(h.agent.home, "x"), OnConflict: "merge"},
		{Direction: "push", Local: filepath.Join(h.mac, "x"), Remote: h.agent.home + "/sub/../x"},
		{Direction: "pull", Local: filepath.Join(h.mac, "x"), Remote: h.agent.home + `\.\x`},
	} {
		if _, err := start(p); err == nil {
			t.Fatalf("start %+v was accepted", p)
		}
	}
	res, err := start(xfer.StartParams{Direction: "push", Local: filepath.Join(h.mac, "missing"), Remote: filepath.Join(h.agent.home, "x")})
	if err != nil || res.Err == nil || res.Err.Code != wire.CodeNotFound || res.Transfer.TransferID != "" {
		t.Fatalf("push of a missing file: %+v, %v", res, err)
	}
	writeFile(t, filepath.Join(h.mac, "here.txt"), []byte("here"))
	res, err = start(xfer.StartParams{Direction: "push", Local: filepath.Join(h.mac, "here.txt"), Remote: filepath.Join(h.agent.home, "no", "such", "here.txt")})
	if err != nil || res.Err == nil || res.Err.Code != wire.CodeNotFound {
		t.Fatalf("push into a missing folder: %+v, %v", res, err)
	}
	res, err = start(xfer.StartParams{Direction: "pull", Local: filepath.Join(h.mac, "got.txt"), Remote: filepath.Join(h.agent.home, "missing.txt")})
	if err != nil || res.Err == nil || res.Err.Code != wire.CodeNotFound {
		t.Fatalf("pull of a missing file: %+v, %v", res, err)
	}
	h.agent.halt()
	deadline := time.Now().Add(10 * time.Second)
	for h.hostState() == conn.StateReady {
		if time.Now().After(deadline) {
			t.Fatal("the host stays ready after its agent stopped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	res, err = start(xfer.StartParams{Direction: "push", Local: filepath.Join(h.mac, "here.txt"), Remote: filepath.Join(h.agent.home, "late.txt")})
	if err != nil || res.Err == nil || res.Err.Code != wire.CodeOffline || res.Info.Sent {
		t.Fatalf("push to an offline PC: %+v, %v", res, err)
	}
	if list, err := h.manager().List("pc"); err != nil || len(list) != 0 {
		t.Fatalf("refused starts left %d transfers, %v", len(list), err)
	}
}

func TestCancelWhileOfflineRemovesThePartAfterADaemonRestart(t *testing.T) {
	g := newGate()
	h := newHarness(t, xfer.Hooks{Wrote: func(_, _ string, done int64) {
		if done >= 4*mib {
			g.wait()
		}
	}})
	src, dst := filepath.Join(h.mac, "o.bin"), filepath.Join(h.agent.home, "o.bin")
	writeFile(t, src, randomBytes(t, 12*mib))
	id := h.start("push", src, dst, "").TransferID
	<-g.reached
	waitPart(t, h.agent.home, 4*mib)
	h.agent.halt()
	g.release()
	h.wait(id, 20*time.Second, "waiting for the PC", func(tr model.Transfer) bool { return tr.State == "queued" })
	tr, err := h.manager().Cancel(id, human)
	if err != nil || tr.State != "cancelled" {
		t.Fatalf("cancel while offline: %s, %v", describe(tr), err)
	}
	if p := parts(t, h.agent.home); len(p) != 1 {
		t.Fatalf("part files on the stopped PC: %v", p)
	}
	h.closeDaemon()
	h.open()
	h.agent.start()
	deadline := time.Now().Add(20 * time.Second)
	for len(parts(t, h.agent.home)) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the part file stays on the PC: %v", parts(t, h.agent.home))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tr := h.get(id); tr.State != "cancelled" {
		t.Fatalf("after the clean-up: %s", describe(tr))
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("the cancelled file reached its destination")
	}
}

func TestTransferOfARemovedHostFails(t *testing.T) {
	g := newGate()
	h := newHarness(t, xfer.Hooks{Wrote: func(_, _ string, done int64) {
		if done >= 4*mib {
			g.wait()
		}
	}})
	src, dst := filepath.Join(h.mac, "r.bin"), filepath.Join(h.agent.home, "r.bin")
	writeFile(t, src, randomBytes(t, 12*mib))
	id := h.start("push", src, dst, "").TransferID
	<-g.reached
	h.mu.Lock()
	d := h.d
	h.mu.Unlock()
	if err := d.st.DeleteHost("pc"); err != nil {
		t.Fatal(err)
	}
	d.conns.Remove("pc")
	g.release()
	tr := h.waitFinished(id)
	if tr.State != "failed" || tr.Error == nil || tr.Error.Code != wire.CodeNotFound {
		t.Fatalf("transfer of a removed host: %s", describe(tr))
	}
}

func TestPushRefusesNamesThatLeaveTheFolderAndKeepsTheMoveSource(t *testing.T) {
	h := newHarness(t, xfer.Hooks{})
	for _, direction := range []string{"push", "move_push"} {
		src := filepath.Join(h.mac, direction)
		writeFile(t, filepath.Join(src, "ok.txt"), []byte("ok"))
		writeFile(t, filepath.Join(src, `..\..\evil.bat`), []byte("evil"))
		writeFile(t, filepath.Join(src, "a", "b.txt"), []byte("slash"))
		writeFile(t, filepath.Join(src, `a\b.txt`), []byte("backslash"))
		dst := filepath.Join(h.agent.home, direction)
		tr := h.waitFinished(h.start(direction, src, dst, "replace").TransferID)
		if tr.State != "failed" || tr.Outcome != "partial" {
			t.Fatalf("%s with names that leave the folder: %s", direction, describe(tr))
		}
		for _, rel := range []string{`..\..\evil.bat`, `a\b.txt`} {
			if f := fileOf(t, tr, rel); f.State != "failed" || f.Error == nil || f.Error.Code != wire.CodeInvalidArgument {
				t.Fatalf("%s %s: %s", direction, rel, describe(tr))
			}
		}
		if got := tree(t, dst); len(got) != 3 || got["ok.txt"] != "ok" || got["a/b.txt"] != "slash" {
			t.Fatalf("%s destination %v", direction, got)
		}
		if got := tree(t, src); got[`..\..\evil.bat`] != "evil" || got[`a\b.txt`] != "backslash" || got["a/b.txt"] != "slash" || got["ok.txt"] != "ok" {
			t.Fatalf("%s source %v", direction, got)
		}
	}
	for rel := range tree(t, h.agent.home) {
		if !strings.HasPrefix(rel, "push/") && !strings.HasPrefix(rel, "move_push/") && rel != "push" && rel != "move_push" {
			t.Fatalf("the transfers wrote %s outside their destination", rel)
		}
	}
}

func TestAMoveRemovingItsSourceRefusesPauseAndCancel(t *testing.T) {
	for _, direction := range []string{"move_push", "move_pull"} {
		t.Run(direction, func(t *testing.T) {
			g := newGate()
			h := newHarness(t, xfer.Hooks{Step: func(_ context.Context, _, step string) {
				if step == "remove" {
					g.wait()
				}
			}})
			t.Cleanup(g.release)
			local, remote := filepath.Join(h.mac, "m.txt"), filepath.Join(h.agent.home, "m.txt")
			src := local
			if direction == "move_pull" {
				src = remote
			}
			writeFile(t, src, []byte("moving"))
			id := h.start(direction, local, remote, "").TransferID
			<-g.reached
			for name, op := range map[string]func(string, model.Caller) (model.Transfer, error){"pause": h.manager().Pause, "cancel": h.manager().Cancel} {
				tr, err := within(t, 5*time.Second, name, func() (model.Transfer, error) { return op(id, human) })
				var e *apierr.Error
				if !errors.As(err, &e) || e.Code != wire.CodeInvalidArgument || !strings.Contains(e.Message, "원본 정리 중") {
					t.Fatalf("%s while the source is removed: %s, %v", name, describe(tr), err)
				}
			}
			g.release()
			tr := h.waitFinished(id)
			switch direction {
			case "move_push":
				checkDone(t, tr, "written")
				if _, err := os.Lstat(src); !os.IsNotExist(err) {
					t.Fatalf("the source of the move is still there: %v", err)
				}
			default:
				if tr.State != "done" || tr.Outcome != "copied_source_retained" {
					t.Fatalf("move_pull: %s", describe(tr))
				}
			}
		})
	}
}

func TestPauseOrCancelRacingTheEndLeavesTheTransferDone(t *testing.T) {
	var armed atomic.Bool
	reached := make(chan struct{}, 1)
	h := newHarness(t, xfer.Hooks{Step: func(ctx context.Context, _, step string) {
		if step == "finish" && armed.CompareAndSwap(true, false) {
			reached <- struct{}{}
			<-ctx.Done()
		}
	}})
	for i, op := range []func(string, model.Caller) (model.Transfer, error){h.manager().Pause, h.manager().Cancel} {
		src, dst := filepath.Join(h.mac, fmt.Sprintf("e%d.txt", i)), filepath.Join(h.agent.home, fmt.Sprintf("e%d.txt", i))
		writeFile(t, src, []byte("end"))
		armed.Store(true)
		id := h.start("push", src, dst, "").TransferID
		<-reached
		tr, err := within(t, 10*time.Second, fmt.Sprint("op ", i), func() (model.Transfer, error) { return op(id, human) })
		var e *apierr.Error
		if !errors.As(err, &e) || e.Code != wire.CodeInvalidArgument {
			t.Fatalf("op %d racing the end: %s, %v", i, describe(tr), err)
		}
		checkDone(t, h.waitFinished(id), "written")
		if got := string(readFile(t, dst)); got != "end" {
			t.Fatalf("destination %q", got)
		}
	}
}

// within returns what fn returns, failing the test when it takes longer
// than limit.
func within(t *testing.T, limit time.Duration, what string, fn func() (model.Transfer, error)) (model.Transfer, error) {
	t.Helper()
	type answer struct {
		tr  model.Transfer
		err error
	}
	ch := make(chan answer, 1)
	go func() {
		tr, err := fn()
		ch <- answer{tr, err}
	}()
	select {
	case a := <-ch:
		return a.tr, a.err
	case <-time.After(limit):
		t.Fatalf("%s did not answer within %s", what, limit)
	}
	return model.Transfer{}, nil
}

func TestAMovePullWhoseSourceIsGoneCountsItRemoved(t *testing.T) {
	var remove func()
	h := newHarness(t, xfer.Hooks{Step: func(_ context.Context, _, step string) {
		if step == "remove" && remove != nil {
			remove()
		}
	}})
	for _, folder := range []bool{false, true} {
		src, dst := filepath.Join(h.agent.home, fmt.Sprint("gone-", folder)), filepath.Join(h.mac, fmt.Sprint("gone-", folder))
		if folder {
			writeFile(t, filepath.Join(src, "in", "f.txt"), []byte("f"))
		} else {
			writeFile(t, src, []byte("f"))
		}
		remove = func() {
			if err := os.RemoveAll(src); err != nil {
				t.Error(err)
			}
		}
		tr := h.waitFinished(h.start("move_pull", dst, src, "").TransferID)
		checkDone(t, tr, "written")
	}
}
