package xfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// eventInterval is the shortest time between two transfer.updated
	// events of a transfer whose files progress.
	eventInterval = 250 * time.Millisecond
	// rateRefresh is how soon a transfer whose rate is above 0 publishes
	// again.
	rateRefresh = time.Second
	// rateWindow is the time over which rate_bps is measured.
	rateWindow = 3 * time.Second
	// retryMin and retryMax bound the wait before another attempt of a
	// transfer whose attempt failed for a reason that can pass.
	retryMin = 500 * time.Millisecond
	retryMax = 10 * time.Second
	// hostPoll is how often a waiting transfer checks that its host is
	// still paired.
	hostPoll = 5 * time.Second
	// settleWait bounds the settlement of a commit and the removal of part
	// files when a transfer is cancelled.
	settleWait = 10 * time.Second
)

// errHostGone ends the run of a transfer whose host was removed.
var errHostGone = errors.New("the host was removed")

// rateSample is the number of bytes moved by a time.
type rateSample struct {
	at time.Time
	n  int64
}

// transfer is a live transfer: its row and manifest as stored, the changes
// not stored yet and its run. files[i] has Seq i.
type transfer struct {
	m *Manager
	// refs counts the holders of the live transfer (Manager.acquire and
	// its run).
	refs atomic.Int32
	// opMu orders Pause, Resume and Cancel.
	opMu sync.Mutex

	mu        sync.Mutex
	row       store.Transfer
	files     []store.TransferFile
	dirty     map[int]bool
	run       *runner
	timer     *time.Timer
	timerSeq  int
	due       time.Time
	lastFlush time.Time
	closed    bool
	// moved counts the bytes the runs moved over the connection; samples
	// hold it at the flushes for rate.
	moved   int64
	samples []rateSample
	rate    int64
	// strikes counts the attempts of a file that failed without progress
	// while the connection stayed; restarts the restarts of a file from 0
	// after its check failed.
	strikes  map[int]int
	restarts map[int]int
	// removing is set while a move removes its source, until the transfer
	// ends.
	removing bool
}

func newTransfer(m *Manager, row store.Transfer, files []store.TransferFile) *transfer {
	sort.Slice(files, func(i, j int) bool { return files[i].Seq < files[j].Seq })
	for i := range files {
		files[i].Seq = i
	}
	return &transfer{m: m, row: row, files: files, dirty: map[int]bool{}, strikes: map[int]int{}, restarts: map[int]int{}}
}

// decodeError returns the error object stored as raw, or nil.
func decodeError(raw json.RawMessage) *apierr.Error {
	if len(raw) == 0 {
		return nil
	}
	var e apierr.Error
	if json.Unmarshal(raw, &e) != nil || e.Code == "" {
		return nil
	}
	return &e
}

// encodeError returns e as stored, or nil.
func encodeError(e *apierr.Error) json.RawMessage {
	if e == nil {
		return nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	return b
}

// snapshot returns the API form of a transfer whose rate is rate while it
// runs.
func snapshot(row store.Transfer, files []store.TransferFile, rate int64) model.Transfer {
	out := model.Transfer{TransferID: row.TransferID, HostID: row.HostID, Direction: row.Direction, Local: row.Local, Remote: row.Remote,
		OnConflict: row.OnConflict, State: row.State, Outcome: row.Outcome, Error: decodeError(row.Error), Files: []model.TransferFile{},
		Rev: row.Rev, UpdatedAt: model.Time(row.UpdatedAt)}
	for _, f := range files {
		if f.Dir {
			continue
		}
		out.Files = append(out.Files, model.TransferFile{Rel: f.Rel, Size: f.Size, BytesDone: f.BytesDone, State: f.State, Outcome: f.Outcome,
			SHA256: f.SHA256, Path: f.Path, Error: decodeError(f.Error)})
		out.FilesTotal++
		out.BytesTotal += f.Size
		out.BytesDone += f.BytesDone
		if f.State == StateDone {
			out.FilesDone++
		}
	}
	if row.State == StateRunning || row.State == StateVerifying {
		out.RateBPS = rate
	}
	return out
}

func (t *transfer) snapshotLocked() model.Transfer {
	return snapshot(t.row, t.files, t.rate)
}

// publishLocked stores the transfer with the next rev and publishes it
// now; a change of the state of a transfer is published so. t.mu is held.
func (t *transfer) publishLocked() {
	t.flushLocked()
}

// changedLocked publishes the changes of the transfer now when the last
// event is eventInterval old, else when it will be. t.mu is held.
func (t *transfer) changedLocked() {
	due := t.lastFlush.Add(eventInterval)
	if !time.Now().Before(due) {
		t.flushLocked()
		return
	}
	if t.timer != nil && !t.due.After(due) {
		return
	}
	t.armLocked(due)
}

// armLocked flushes the transfer at due, instead of a flush armed before.
func (t *transfer) armLocked(due time.Time) {
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timerSeq++
	seq := t.timerSeq
	t.due = due
	t.timer = time.AfterFunc(time.Until(due), func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.timerSeq != seq || t.timer == nil {
			return
		}
		t.timer = nil
		t.flushLocked()
	})
}

// flushLocked stores the row and the changed files of the transfer with
// the next rev and publishes transfer.updated; a rev that could not be
// stored is not published and the flush runs again after rateRefresh. t.mu
// is held.
func (t *transfer) flushLocked() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
		t.timerSeq++
	}
	if t.closed {
		return
	}
	now := time.Now()
	t.row.Rev++
	t.row.UpdatedAt = now
	if err := t.m.opt.Store.SaveTransfer(t.row, t.dirtyFilesLocked(), false); err != nil {
		t.m.lg.Printf("transfer %s: store rev %d: %v", t.row.TransferID, t.row.Rev, err)
		t.armLocked(now.Add(rateRefresh))
		return
	}
	t.sampleLocked(now)
	t.dirty = map[int]bool{}
	t.lastFlush = now
	snap := t.snapshotLocked()
	t.m.opt.Bus.PublishStoring(t.row.HostID, "", events.TransferUpdated, map[string]any{"transfer": snap}, map[string]any{
		"transfer_id": snap.TransferID, "rev": snap.Rev, "state": snap.State, "outcome": snap.Outcome, "bytes_done": snap.BytesDone,
		"bytes_total": snap.BytesTotal, "files_done": snap.FilesDone, "files_total": snap.FilesTotal})
	if snap.RateBPS > 0 {
		t.armLocked(now.Add(rateRefresh))
	}
}

// saveLocked stores the row and the changed files of the transfer without
// publishing them. t.mu is held.
func (t *transfer) saveLocked() error {
	err := t.m.opt.Store.SaveTransfer(t.row, t.dirtyFilesLocked(), false)
	if err == nil {
		t.dirty = map[int]bool{}
	}
	return err
}

func (t *transfer) dirtyFilesLocked() []store.TransferFile {
	out := make([]store.TransferFile, 0, len(t.dirty))
	for seq := range t.dirty {
		out = append(out, t.files[seq])
	}
	return out
}

// sampleLocked records the bytes moved by now and measures the rate over
// the samples of the last rateWindow.
func (t *transfer) sampleLocked(now time.Time) {
	t.samples = append(t.samples, rateSample{at: now, n: t.moved})
	for len(t.samples) > 2 && now.Sub(t.samples[1].at) >= rateWindow {
		t.samples = t.samples[1:]
	}
	first := t.samples[0]
	span := now.Sub(first.at)
	switch {
	case span >= eventInterval:
		t.rate = int64(float64(t.moved-first.n) / span.Seconds())
	case len(t.samples) == 1:
		t.rate = 0
	}
}

// file returns a copy of the file seq.
func (t *transfer) file(seq int) store.TransferFile {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.files[seq]
}

// update changes the file seq with fn and publishes the change within
// eventInterval.
func (t *transfer) update(seq int, fn func(f *store.TransferFile)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(&t.files[seq])
	t.dirty[seq] = true
	t.changedLocked()
}

// updateNow changes the file seq with fn and stores the change before it
// returns; it is published within eventInterval.
func (t *transfer) updateNow(seq int, fn func(f *store.TransferFile)) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(&t.files[seq])
	t.dirty[seq] = true
	if err := t.saveLocked(); err != nil {
		return apierr.Internal(fmt.Sprintf("전송 기록을 저장하지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	t.changedLocked()
	return nil
}

// progress records that done bytes of the file seq are written, moved of
// them over the connection now.
func (t *transfer) progress(seq int, done, moved int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.files[seq].BytesDone = done
	t.moved += moved
	t.dirty[seq] = true
	t.changedLocked()
}

// setState sets the state of the transfer and publishes it now when it
// changes.
func (t *transfer) setState(state string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.row.State != state {
		t.row.State = state
		t.publishLocked()
	}
}

// callerLocked returns who started the transfer.
func (t *transfer) callerLocked() model.Caller {
	return model.Caller{Actor: t.row.Actor, ClientID: t.row.ClientID, ClientName: t.row.ClientName}
}

// recordLocked adds a timeline entry of c about the transfer.
func (t *transfer) recordLocked(c model.Caller, summary, outcome string) {
	_, _ = t.m.opt.Bus.Record(c.Entry(t.row.HostID, model.TimelineFile, summary, outcome, ""))
}

// verb names what a transfer of direction does.
func verb(direction string) string {
	switch direction {
	case Pull:
		return "받기"
	case MovePush:
		return "이동해 보내기"
	case MovePull:
		return "이동해 받기"
	}
	return "보내기"
}

// route names the source and the destination of a transfer.
func route(direction, local, hostID, remote string) string {
	if pushing(direction) {
		return fmt.Sprintf("%s → %s:%s", local, hostID, remote)
	}
	return fmt.Sprintf("%s:%s → %s", hostID, remote, local)
}

func (t *transfer) verbLocked() string { return verb(t.row.Direction) }

func (t *transfer) routeLocked() string {
	return route(t.row.Direction, t.row.Local, t.row.HostID, t.row.Remote)
}

func (t *transfer) doneFilesLocked() int {
	n := 0
	for _, f := range t.files {
		if !f.Dir && f.State == StateDone {
			n++
		}
	}
	return n
}

// pushing reports whether direction sends from this Mac.
func pushing(direction string) bool { return direction == Push || direction == MovePush }

// moving reports whether direction removes the source.
func moving(direction string) bool { return direction == MovePush || direction == MovePull }

// launchLocked starts a run of the transfer unless one runs or the manager
// is closed. t.mu is held and the caller holds the transfer.
func (t *transfer) launchLocked() {
	if t.run != nil {
		return
	}
	m := t.m
	m.lifeMu.Lock()
	if m.closed {
		m.lifeMu.Unlock()
		return
	}
	m.wg.Add(1)
	m.lifeMu.Unlock()
	ctx, cancel := context.WithCancelCause(m.base)
	r := &runner{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	t.run = r
	t.refs.Add(1)
	go func() {
		defer m.wg.Done()
		t.loop(r)
		cancel(nil)
		t.mu.Lock()
		if t.run == r {
			t.run = nil
		}
		t.mu.Unlock()
		close(r.done)
		m.release(t)
	}()
}

// stopRun ends the run of the transfer with cause and waits for it.
func (t *transfer) stopRun(cause error) {
	t.mu.Lock()
	r := t.run
	t.mu.Unlock()
	if r == nil {
		return
	}
	r.cancel(cause)
	<-r.done
}

// loop runs attempts of the transfer while it is queued or running, waiting
// for its host before each, until it finishes, its run is stopped or its
// host is removed. A cancelled transfer removes its part files on the host
// instead.
func (t *transfer) loop(r *runner) {
	wait := retryMin
	for r.ctx.Err() == nil {
		t.mu.Lock()
		state := t.row.State
		t.mu.Unlock()
		switch state {
		case StateCancelled:
			t.cleanupParts(r.ctx)
			return
		case StateQueued, StateRunning, StateVerifying:
		default:
			return
		}
		h, err := t.readyHost(r.ctx)
		if errors.Is(err, errHostGone) {
			t.failAll(apierr.New(wire.CodeNotFound, "PC가 Deskhand에서 삭제돼 전송을 끝냈습니다. PC를 다시 추가한 뒤 새 전송을 시작하세요", false, apierr.NextFixParams))
			return
		}
		if err != nil {
			return
		}
		t.mu.Lock()
		before := t.moved
		t.mu.Unlock()
		if t.attempt(r.ctx, h) != resRetry {
			return
		}
		t.mu.Lock()
		if t.moved > before {
			wait = retryMin
		}
		if t.row.State == StateRunning || t.row.State == StateVerifying {
			t.row.State = StateQueued
			t.publishLocked()
		}
		t.mu.Unlock()
		if sleep(r.ctx, wait) != nil {
			return
		}
		wait = min(2*wait, retryMax)
	}
}

// readyHost waits until the host of the transfer is ready or degraded and
// returns it; the transfer is queued meanwhile. A host that is no longer
// paired is errHostGone.
func (t *transfer) readyHost(ctx context.Context) (*conn.Host, error) {
	ready := func(s conn.ConnState) bool { return s == conn.StateReady || s == conn.StateDegraded }
	for {
		if _, err := t.m.opt.Store.GetHost(t.row.HostID); errors.Is(err, store.ErrNotFound) {
			return nil, errHostGone
		}
		h := t.m.opt.Conns.Get(t.row.HostID)
		if h != nil && ready(h.State()) {
			return h, nil
		}
		t.mu.Lock()
		if t.row.State == StateRunning || t.row.State == StateVerifying {
			t.row.State = StateQueued
			t.publishLocked()
		}
		t.mu.Unlock()
		if h == nil {
			if sleep(ctx, hostPoll) != nil {
				return nil, ctx.Err()
			}
			continue
		}
		wctx, cancel := context.WithTimeout(ctx, hostPoll)
		err := h.WaitState(wctx, ready)
		cancel()
		if err == nil {
			return h, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
}

// sleep waits d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-tm.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// interruptedLocked makes a transfer the daemon left running queued, with
// its unfinished files queued.
func (t *transfer) interruptedLocked() {
	if t.row.State == StateRunning || t.row.State == StateVerifying {
		t.row.State = StateQueued
	}
	for i := range t.files {
		if f := &t.files[i]; f.State == StateRunning || f.State == StateVerifying || f.State == StatePaused {
			f.State = StateQueued
			t.dirty[i] = true
		}
	}
	t.publishLocked()
}

// failAll fails the files of the transfer that are not finished with e
// and finishes the transfer; a transfer without a manifest fails with e.
func (t *transfer) failAll(e *apierr.Error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.row.Planned {
		t.failPlanLocked(e)
		return
	}
	for i := range t.files {
		if f := &t.files[i]; f.State != StateDone && f.State != StateFailed {
			f.State, f.Error = StateFailed, encodeError(e)
			t.dirty[i] = true
		}
	}
	t.finishLocked(nil)
}

// finishLocked ends a transfer whose files are all finished: done when none
// failed, failed (outcome partial or failed) when some did. retained, when
// set, says why a move kept its source. t.mu is held.
func (t *transfer) finishLocked(retained *apierr.Error) {
	var failed []store.TransferFile
	var dirFailed *store.TransferFile
	outcomes := map[string]int{}
	done, files := 0, 0
	for i, f := range t.files {
		if f.Dir {
			if f.State == StateFailed && dirFailed == nil {
				dirFailed = &t.files[i]
			}
			continue
		}
		files++
		switch f.State {
		case StateDone:
			done++
			outcomes[f.Outcome]++
		default:
			failed = append(failed, f)
		}
	}
	now := time.Now()
	t.row.FinishedAt = &now
	t.removing = false
	summary, outcome := t.routeLocked(), model.TimelineOK
	switch {
	case len(failed) > 0 || dirFailed != nil:
		t.row.State = StateFailed
		t.row.Outcome = OutcomeFailed
		if done > 0 {
			t.row.Outcome = OutcomePartial
		}
		first := dirFailed
		if len(failed) > 0 {
			first = &failed[0]
		}
		e := decodeError(first.Error)
		if e == nil {
			e = apierr.Internal("이유를 기록하지 못한 실패입니다. 데몬 로그를 확인하세요")
		}
		if files > 1 || dirFailed != nil {
			n := len(failed)
			if dirFailed != nil && n == 0 {
				n = 1
			}
			e = apierr.New(e.Code, fmt.Sprintf("%d개 항목을 옮기지 못했습니다. 첫 항목 %s: %s", n, first.Rel, e.Message), e.Retryable, e.NextAction)
		}
		t.row.Error = encodeError(e)
		summary = fmt.Sprintf("%s 실패: %s (%s)", t.verbLocked(), summary, e.Message)
		outcome = model.TimelineFailed
	default:
		t.row.State = StateDone
		t.row.Error = nil
		t.row.Outcome = OutcomeWritten
		if len(outcomes) == 1 {
			for o := range outcomes {
				t.row.Outcome = o
			}
		}
		summary = fmt.Sprintf("%s 끝: %s (파일 %d개)", t.verbLocked(), summary, done)
		if retained != nil {
			t.row.Outcome = OutcomeSourceRetained
			t.row.Error = encodeError(retained)
			summary = fmt.Sprintf("%s 끝, 원본 남김: %s (%s)", t.verbLocked(), t.routeLocked(), retained.Message)
			outcome = model.TimelineFailed
		}
	}
	t.recordLocked(t.callerLocked(), summary, outcome)
	t.publishLocked()
}
